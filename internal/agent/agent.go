package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/gateway"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Agent struct {
	config       config.Config
	version      string
	logger       *slog.Logger
	generations  *GenerationManager
	applied      atomic.Int64
	probeEpoch   atomic.Int64
	appliedProbe atomic.Int64
	leaderStore  controlplane.StateStore
}

func New(cfg config.Config, version string, logger *slog.Logger) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	value := &Agent{config: cfg, version: version, logger: logger, generations: NewGenerationManager(cfg.NGINX)}
	value.probeEpoch.Store(-1)
	return value
}

func (a *Agent) Run(ctx context.Context) error {
	// Standalone Swarm gateways need the same elected-leader discovery as the
	// embedded gateway. They never migrate the database or participate in election.
	if a.leaderStore == nil && a.config.Storage.DSN != "" {
		store, err := storage.Open(a.config.Storage.Driver, a.config.Storage.DSN)
		if err != nil {
			return errors.New("open gateway leader store failed")
		}
		defer store.Close()
		a.SetLeaderStore(controlplane.SQLState{Store: store})
	}
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	state, err := a.generations.Prepare()
	if err != nil {
		return err
	}
	if err = validateGenerationPool(state, a.config.Agent.PoolID); err != nil {
		return err
	}
	a.applied.Store(state.ConfigRevision)
	a.logger.Info("restored last known good generation", "config_revision", state.ConfigRevision, "model_digest", state.ModelDigest,
		"renderer_version", state.RendererVersion, "rendered_digest", state.RenderedDigest)
	nginxDone, err := a.generations.Start()
	if err != nil {
		return err
	}
	connectionDone := make(chan error, 1)
	go func() { connectionDone <- a.connectionLoop(runCtx) }()
	select {
	case <-ctx.Done():
		err = nil
	case nginxErr := <-nginxDone:
		if nginxErr == nil {
			err = errors.New("NGINX exited unexpectedly")
		} else {
			err = fmt.Errorf("NGINX exited: %w", nginxErr)
		}
	case connectionErr := <-connectionDone:
		if !errors.Is(connectionErr, context.Canceled) {
			err = connectionErr
		}
	}
	runCancel()
	// The Swarm Gateway grants 30 seconds before Docker sends SIGKILL. Leave
	// five seconds for the Agent to exit after NGINX's graceful SIGQUIT.
	stopCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if stopErr := a.generations.Stop(stopCtx); err == nil && !errors.Is(stopErr, context.Canceled) {
		err = stopErr
	}
	return err
}

func validateGenerationPool(state GenerationState, expectedPoolID string) error {
	if state.ConfigRevision == 0 {
		return nil
	}
	if state.PoolID == "" {
		return fmt.Errorf("last known good generation revision %d has no pool identity", state.ConfigRevision)
	}
	if state.PoolID != expectedPoolID {
		return fmt.Errorf("last known good generation belongs to pool %q, not configured pool %q", state.PoolID, expectedPoolID)
	}
	return nil
}

func (a *Agent) connectionLoop(ctx context.Context) error {
	credentialsConfig, err := a.transportCredentials()
	if err != nil {
		return err
	}
	if a.leaderStore == nil {
		return errors.New("local gateway requires the shared Controller leader store")
	}
	for ctx.Err() == nil {
		err = a.connectLeader(ctx, credentialsConfig)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.logger.Warn("elected controller unavailable; retaining last known good configuration", "error", err)
		if err = sleep(ctx, time.Second); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (a *Agent) runStream(parent context.Context, client gatewayv2.GatewayControlClient) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		return err
	}
	state := a.generations.State()
	hello := &gatewayv2.Hello{GatewayId: a.config.Agent.GatewayID, PoolId: a.config.Agent.PoolID, Name: a.config.Agent.Name,
		AgentVersion: a.version, Address: a.config.Agent.PublicAddress, ProtocolVersion: gateway.ProtocolVersion,
		Http3Supported: supportsHTTP3(a.config.NGINX.Binary), Http3AdvertisedPort: int32(a.config.NGINX.HTTP3AdvertisedPort),
		RendererVersion: nginxx.RendererVersion, Mode: a.config.Agent.Mode, AppliedConfigRevision: state.ConfigRevision,
		AppliedModelDigest: state.ModelDigest, RenderedDigest: state.RenderedDigest,
		AppliedProbeRevision: a.appliedProbe.Load()}
	if state.needsRendererUpgrade() {
		// Keep serving the old generation, but request a fresh model instead of
		// claiming that its bytes were rendered by the new executable.
		hello.AppliedConfigRevision = 0
		hello.AppliedModelDigest = ""
		hello.RenderedDigest = ""
	}
	if err = stream.Send(&gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}); err != nil {
		return err
	}
	priority := make(chan *gatewayv2.AgentMessage, 32)
	probeOutgoing := make(chan *gatewayv2.AgentMessage, 2048)
	sendErrors := make(chan error, 1)
	go func() {
		sentRevision := hello.AppliedConfigRevision
		for {
			var message *gatewayv2.AgentMessage
			select {
			case message = <-priority:
			default:
				select {
				case <-ctx.Done():
					return
				case message = <-priority:
				case message = <-probeOutgoing:
				}
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
			if hb := message.GetHeartbeat(); hb != nil && hb.AppliedConfigRevision != sentRevision {
				continue
			}
			if sendErr := stream.Send(message); sendErr != nil {
				select {
				case sendErrors <- sendErr:
				default:
				}
				return
			}
			if ack := message.GetAck(); ack != nil {
				sentRevision = ack.ConfigRevision
			}
		}
	}()
	probes := NewProbeRunner(a.config.Agent.ProbeWorkers, probeOutgoing)
	defer probes.Stop()
	heartbeatEvery := a.config.Agent.HeartbeatInterval.Std()
	go func() {
		heartbeat := time.NewTicker(heartbeatEvery)
		defer heartbeat.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				if a.generations.State().needsRendererUpgrade() {
					continue
				}
				evidence := a.generations.Observe(ctx)
				current := evidence.State
				message := &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{
					LoadingConfirmed: evidence.LoadingConfirmed, StartupState: evidence.StartupState, DiskDigest: evidence.DiskDigest, EvidenceAtUnixNano: evidence.ObservedAt.UnixNano(), RefreshIntervalMillis: heartbeatEvery.Milliseconds(),
					AppliedConfigRevision: current.ConfigRevision, RendererVersion: current.RendererVersion,
					RenderedDigest: current.RenderedDigest, SentAtUnixNano: time.Now().UTC().UnixNano(),
					AppliedProbeRevision: a.appliedProbe.Load(), AppliedModelDigest: current.ModelDigest}}}
				if enqueue(ctx, priority, message) != nil {
					return
				}
			}
		}
	}()
	received := make(chan receivedControllerMessage, 1)
	go func() {
		for {
			message, receiveErr := stream.Recv()
			select {
			case received <- receivedControllerMessage{message: message, err: receiveErr}:
			case <-ctx.Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()
	var pending *pendingSnapshot
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case sendErr := <-sendErrors:
			return sendErr
		case value := <-received:
			if errors.Is(value.err, io.EOF) {
				return io.EOF
			}
			if value.err != nil {
				return value.err
			}
			message := value.message
			if err := checkLeader(ctx); err != nil {
				return err
			}
			if validation := message.GetValidationRequest(); validation != nil {
				if validation.Cancel && a.config.Agent.Mode == "sandbox" {
					if e := a.generations.CancelValidation(ctx, validation.TaskId); e != nil {
						return e
					}
					continue
				}
				if validation.Snapshot == nil || validation.Snapshot.PoolId != a.config.Agent.PoolID {
					return fmt.Errorf("invalid validation pool")
				}
				var result *gatewayv2.ValidationResult
				if validation.ActivateSandbox && a.config.Agent.Mode == "sandbox" {
					result = a.generations.ValidateSandbox(ctx, validation)
				} else {
					result = a.generations.Validate(ctx, validation, false)
				}
				if err := enqueue(ctx, priority, &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_ValidationResult{ValidationResult: result}}); err != nil {
					return err
				}
				continue
			}
			if a.config.Agent.Mode == "sandbox" && (message.GetSnapshotHeader() != nil || message.GetSnapshotChunk() != nil || message.GetProbePlan() != nil) {
				return fmt.Errorf("sandbox received a production command")
			}

			if header := message.GetSnapshotHeader(); header != nil {
				if pending != nil {
					return fmt.Errorf("received a new snapshot header before completing revision %d", pending.header.ConfigRevision)
				}
				if header.ChunkCount < 1 || header.ChunkCount > 4096 || header.CompressedSize < 0 || header.CompressedSize > gateway.MaxCompressedSize || header.UncompressedSize < 0 || header.UncompressedSize > gateway.MaxUncompressedSize {
					return fmt.Errorf("received an invalid snapshot header")
				}
				pending = &pendingSnapshot{header: header, chunks: make([]*gatewayv2.SnapshotChunk, 0, int(header.ChunkCount))}
				continue
			}
			if chunk := message.GetSnapshotChunk(); chunk != nil {
				if pending == nil || chunk.ConfigRevision != pending.header.ConfigRevision {
					return fmt.Errorf("received an unexpected snapshot chunk")
				}
				if chunk.Index != int32(len(pending.chunks)) || int32(len(pending.chunks)) >= pending.header.ChunkCount {
					return fmt.Errorf("received an out-of-order snapshot chunk")
				}
				pending.receivedBytes += int64(len(chunk.Data))
				if pending.receivedBytes > pending.header.CompressedSize || pending.receivedBytes > gateway.MaxCompressedSize {
					return fmt.Errorf("received snapshot data beyond the declared size")
				}
				pending.chunks = append(pending.chunks, chunk)
				if int32(len(pending.chunks)) == pending.header.ChunkCount {
					if pending.receivedBytes != pending.header.CompressedSize {
						return fmt.Errorf("received snapshot data does not match the declared size")
					}
					a.applySnapshot(ctx, pending, priority)
					pending = nil
				}
				continue
			}
			if plan := message.GetProbePlan(); plan != nil {
				if plan.PoolId != a.config.Agent.PoolID {
					return fmt.Errorf("received probe plan for a different pool")
				}
				currentEpoch, currentRevision := a.probeEpoch.Load(), a.appliedProbe.Load()
				if plan.Epoch < currentEpoch || plan.ProbeRevision < currentRevision {
					return fmt.Errorf("received stale probe plan epoch %d revision %d after epoch %d revision %d", plan.Epoch, plan.ProbeRevision, currentEpoch, currentRevision)
				}
				if err = probes.SetPlan(ctx, plan); err != nil {
					return fmt.Errorf("reject probe plan: %w", err)
				}
				a.probeEpoch.Store(plan.Epoch)
				a.appliedProbe.Store(plan.ProbeRevision)
			}
		}
	}
}

type receivedControllerMessage struct {
	message *gatewayv2.ControllerMessage
	err     error
}

type pendingSnapshot struct {
	header        *gatewayv2.SnapshotHeader
	chunks        []*gatewayv2.SnapshotChunk
	receivedBytes int64
}

func (a *Agent) applySnapshot(ctx context.Context, pending *pendingSnapshot, outgoing chan<- *gatewayv2.AgentMessage) {
	nack := func(cause error, result ApplyResult) {
		message := cause.Error()
		if len(message) > 2048 {
			message = message[:2048]
		}
		_ = enqueue(ctx, outgoing, &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Nack{Nack: &gatewayv2.ApplyNack{
			ConfigRevision: pending.header.ConfigRevision, ModelDigest: pending.header.ModelDigest, RendererVersion: nginxx.RendererVersion,
			Error: message, ObservedAtUnixNano: time.Now().UTC().UnixNano(),
			NginxValidateDurationNanos: int64(result.ValidationDuration), NginxReloadDurationNanos: int64(result.ReloadDuration), FailedPhase: result.FailedPhase}}})
		a.logger.Error("snapshot rejected; retaining last known good configuration", "config_revision", pending.header.ConfigRevision, "error", message)
	}
	current := a.generations.State()
	if !current.acceptsSnapshotRevision(pending.header.ConfigRevision, pending.header.ModelDigest) {
		nack(ErrOldRevision, ApplyResult{FailedPhase: "input"})
		return
	}
	snapshot, err := gateway.DecodeSnapshot(pending.header, pending.chunks)
	if err != nil {
		nack(err, ApplyResult{FailedPhase: "decode"})
		return
	}
	if snapshot.PoolId != a.config.Agent.PoolID {
		nack(fmt.Errorf("snapshot pool identity does not match this agent"), ApplyResult{FailedPhase: "identity"})
		return
	}
	result, err := a.generations.Apply(ctx, snapshot, pending.header.ModelDigest)
	if err != nil {
		nack(err, result)
		return
	}
	a.applied.Store(snapshot.ConfigRevision)
	state := a.generations.State()
	ack := &gatewayv2.ApplyAck{ConfigRevision: snapshot.ConfigRevision, ModelDigest: pending.header.ModelDigest,
		RendererVersion: state.RendererVersion, RenderedDigest: state.RenderedDigest,
		AppliedAtUnixNano: time.Now().UTC().UnixNano(), NginxValidateDurationNanos: int64(result.ValidationDuration),
		NginxReloadDurationNanos: int64(result.ReloadDuration)}
	_ = enqueue(ctx, outgoing, &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: ack}})
	if result.Warning != "" {
		a.logger.Warn("snapshot applied with generation cleanup warning", "config_revision", snapshot.ConfigRevision, "warning", result.Warning)
	}
	a.logger.Info("snapshot applied", "config_revision", snapshot.ConfigRevision, "model_digest", pending.header.ModelDigest,
		"rendered_digest", state.RenderedDigest)
}

func (a *Agent) transportCredentials() (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(a.config.Agent.TLSCertFile, a.config.Agent.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load agent TLS key pair: %w", err)
	}
	caPEM, err := os.ReadFile(a.config.Agent.CACertFile)
	if err != nil {
		return nil, fmt.Errorf("read controller CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("controller CA file contains no certificates")
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, RootCAs: roots, ServerName: a.config.Agent.ServerName, MinVersion: tls.VersionTLS13}, nil
}

func enqueue(ctx context.Context, outgoing chan<- *gatewayv2.AgentMessage, message *gatewayv2.AgentMessage) error {
	select {
	case outgoing <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
