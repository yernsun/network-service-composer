package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const identityHost = "nsc.local"

const ProtocolVersion int32 = 4

var errGatewayRevoked = errors.New("gateway is disabled or revoked")

type Server struct {
	gatewayv2.UnimplementedGatewayControlServer
	core           *core.Core
	store          storage.Store
	broker         *Broker
	offlineTimeout time.Duration
	reconcileDelay time.Duration
	reconcileMax   time.Duration
	metrics        *metricsx.Metrics
	logger         *slog.Logger
}

type ServerOptions struct {
	OfflineTimeout   time.Duration
	ReconcileDelay   time.Duration
	ReconcileMaxWait time.Duration
}

func NewServer(application *core.Core, broker *Broker, options ServerOptions, metrics *metricsx.Metrics, logger *slog.Logger) *Server {
	if broker == nil {
		broker = NewBroker()
	}
	if options.OfflineTimeout <= 0 {
		options.OfflineTimeout = 15 * time.Second
	}
	if options.ReconcileDelay < 0 {
		options.ReconcileDelay = 250 * time.Millisecond
	}
	if options.ReconcileMaxWait <= 0 {
		options.ReconcileMaxWait = 2 * time.Second
	}
	if options.ReconcileMaxWait < options.ReconcileDelay {
		options.ReconcileMaxWait = options.ReconcileDelay
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{core: application, store: application.Store(), broker: broker, offlineTimeout: options.OfflineTimeout,
		reconcileDelay: options.ReconcileDelay, reconcileMax: options.ReconcileMaxWait, metrics: metrics, logger: logger}
}

func (s *Server) Register(registrar grpc.ServiceRegistrar) {
	gatewayv2.RegisterGatewayControlServer(registrar, s)
}

type received struct {
	message *gatewayv2.AgentMessage
	err     error
}

func (s *Server) Connect(stream grpc.BidiStreamingServer[gatewayv2.AgentMessage, gatewayv2.ControllerMessage]) error {
	identity, fingerprint, err := identityFromContext(stream.Context())
	if err != nil {
		return status.Error(codes.Unauthenticated, err.Error())
	}
	first := make(chan received, 1)
	go func() {
		message, receiveErr := stream.Recv()
		first <- received{message: message, err: receiveErr}
	}()
	var hello *gatewayv2.Hello
	select {
	case <-stream.Context().Done():
		return stream.Context().Err()
	case <-time.After(10 * time.Second):
		return status.Error(codes.DeadlineExceeded, "gateway Hello timeout")
	case value := <-first:
		if value.err != nil {
			return value.err
		}
		hello = value.message.GetHello()
	}
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first gateway message must be Hello")
	}
	if hello.GatewayId != identity.GatewayID || hello.PoolId != identity.PoolID {
		return status.Error(codes.PermissionDenied, "Hello identity does not match the client certificate")
	}
	mode, modeErr := s.core.GatewayMode(stream.Context(), identity.GatewayID, identity.PoolID)
	announcedMode := hello.Mode
	if announcedMode == "" {
		announcedMode = "production"
	}
	if modeErr != nil || mode != announcedMode {
		return status.Error(codes.PermissionDenied, "gateway mode does not match registered identity")
	}
	if hello.ProtocolVersion != ProtocolVersion || hello.RendererVersion != nginxx.RendererVersion {
		now := time.Now().UTC()
		reason := "gateway protocol version mismatch"
		if hello.ProtocolVersion == ProtocolVersion {
			reason = "gateway renderer version mismatch"
		}
		incompatible := model.Gateway{ID: identity.GatewayID, PoolID: identity.PoolID, Name: hello.Name,
			CertificateHash: fingerprint, Enabled: true, Status: model.GatewayIncompatible,
			ProtocolVersion: hello.ProtocolVersion, RendererVersion: hello.RendererVersion,
			LastError: reason, LastSeenAt: &now,
			Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
		if mode == "production" {
			_ = s.store.UpsertGateway(stream.Context(), &incompatible)
		}
		return status.Errorf(codes.FailedPrecondition, "gateway protocol/renderer %d/%q is incompatible with required %d/%q",
			hello.ProtocolVersion, hello.RendererVersion, ProtocolVersion, nginxx.RendererVersion)
	}
	pool, err := s.store.GetPool(stream.Context(), identity.PoolID)
	if err != nil || !pool.Enabled {
		return status.Error(codes.PermissionDenied, "gateway pool is unavailable")
	}
	if err = s.core.InvalidateNginxValidation(stream.Context(), identity.PoolID); err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.core.InvalidateNginxValidation(ctx, identity.PoolID)
	}()
	if mode == "sandbox" {
		return s.connectSandbox(stream, identity.GatewayID, identity.PoolID)
	}
	if hello.AppliedConfigRevision < 0 || hello.AppliedConfigRevision > pool.DesiredRevision {
		return status.Error(codes.FailedPrecondition, "Hello applied revision is outside the pool revision range")
	}
	if hello.AppliedProbeRevision < 0 || hello.AppliedProbeRevision > pool.ProbeRevision {
		return status.Error(codes.FailedPrecondition, "Hello applied probe revision is outside the pool probe revision range")
	}
	if err = validateAppliedTuple(hello.AppliedConfigRevision, hello.AppliedModelDigest, hello.RenderedDigest); err != nil {
		return status.Error(codes.FailedPrecondition, "invalid Hello applied tuple: "+err.Error())
	}
	if hello.AppliedConfigRevision > 0 && hello.AppliedConfigRevision == pool.DesiredRevision {
		currentSnapshot, snapshotErr := s.core.BuildGatewaySnapshot(stream.Context(), identity.PoolID)
		if snapshotErr != nil {
			return status.Error(codes.Internal, "verify gateway last known good snapshot")
		}
		encoded, encodeErr := EncodeSnapshot(currentSnapshot, DefaultChunkSize)
		if encodeErr != nil {
			return status.Error(codes.Internal, "verify gateway last known good digest")
		}
		if hello.AppliedModelDigest != encoded.Header.ModelDigest {
			return status.Error(codes.FailedPrecondition, "Hello snapshot digest does not match the pool revision")
		}
	}
	now := time.Now().UTC()
	remoteAddress := hello.Address
	if remoteAddress == "" {
		if remote, ok := peer.FromContext(stream.Context()); ok && remote.Addr != nil {
			remoteAddress = remote.Addr.String()
		}
	}
	gatewayStatus := model.GatewayStale
	if hello.AppliedConfigRevision == pool.DesiredRevision {
		gatewayStatus = model.GatewayReady
	}
	gateway := model.Gateway{ID: identity.GatewayID, PoolID: identity.PoolID, Name: hello.Name, Address: remoteAddress,
		CertificateHash: fingerprint, Enabled: true, Status: gatewayStatus, DesiredRevision: pool.DesiredRevision,
		AppliedRevision: hello.AppliedConfigRevision, AgentVersion: hello.AgentVersion, ProtocolVersion: hello.ProtocolVersion,
		AppliedProbeRevision: hello.AppliedProbeRevision,
		HTTP3Supported:       hello.Http3Supported, HTTP3AdvertisedPort: int(hello.Http3AdvertisedPort),
		RendererVersion: hello.RendererVersion, ModelDigest: hello.AppliedModelDigest, RenderedDigest: hello.RenderedDigest,
		LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if gateway.Name == "" {
		gateway.Name = gateway.ID
	}
	if err = s.store.UpsertGateway(stream.Context(), &gateway); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return status.Error(codes.PermissionDenied, err.Error())
		}
		return status.Error(codes.Internal, "register gateway")
	}
	s.logger.Info("gateway connected", "gateway_id", gateway.ID, "pool_id", gateway.PoolID, "agent_version", gateway.AgentVersion)
	defer s.logger.Info("gateway disconnected", "gateway_id", gateway.ID, "pool_id", gateway.PoolID)

	incoming := make(chan received, 1)
	go func() {
		for {
			message, receiveErr := stream.Recv()
			select {
			case incoming <- received{message: message, err: receiveErr}:
			case <-stream.Context().Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()
	changes, unsubscribe := s.broker.Subscribe(identity.PoolID)
	defer unsubscribe()
	poll := time.NewTicker(time.Second)
	heartbeat := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	appliedRevision := hello.AppliedConfigRevision
	appliedModelDigest := hello.AppliedModelDigest
	appliedRenderedDigest := hello.RenderedDigest
	lastSentRevision := int64(0)
	lastSentSHA256 := ""
	defer func() {
		if lastSentRevision == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(stream.Context()), 2*time.Second)
		defer cancel()
		if err := s.core.RecordNginxReleaseResult(ctx, identity.PoolID, identity.GatewayID, lastSentRevision, "UNKNOWN", "", "", "connection ended before a result was confirmed"); err != nil {
			s.logger.Error("record unknown release result", "error", err)
		}
	}()

	failedRevision := int64(0)
	nextRetry := time.Time{}
	retryBackoff := time.Second
	lastProbeEpoch, lastProbeRevision := int64(-1), int64(-1)

	sendState := func() error {
		registered, getErr := s.store.GetGateway(stream.Context(), identity.GatewayID)
		if getErr != nil {
			return getErr
		}
		if !registered.Enabled || registered.PoolID != identity.PoolID || registered.CertificateHash != fingerprint {
			return errGatewayRevoked
		}
		if err := s.sendValidation(stream, identity.GatewayID, identity.PoolID); err != nil {
			return err
		}
		current, getErr := s.store.GetPool(stream.Context(), identity.PoolID)
		if getErr != nil {
			return getErr
		}
		if current.DesiredRevision > appliedRevision && lastSentRevision == 0 {
			if failedRevision != 0 && current.DesiredRevision != failedRevision {
				failedRevision = 0
				nextRetry = time.Time{}
				retryBackoff = time.Second
			}
			if current.DesiredRevision == failedRevision && time.Now().Before(nextRetry) {
				// Continue maintaining probe leadership while the failed Snapshot
				// waits for its bounded retry window.
			} else {
				generationStarted := time.Now()
				claimed, managed, claimErr := s.core.ClaimNginxRelease(stream.Context(), identity.PoolID, identity.GatewayID, current.DesiredRevision)
				if claimErr != nil {
					return claimErr
				}
				if !managed || claimed != nil {
					var snapshot model.Snapshot
					if managed {
						snapshot = *claimed
						lastSentRevision = snapshot.Revision
					} else {
						var buildErr error
						snapshot, buildErr = s.core.BuildGatewaySnapshot(stream.Context(), identity.PoolID)
						if buildErr != nil {
							return buildErr
						}
					}
					encoded, encodeErr := EncodeSnapshot(snapshot, DefaultChunkSize)
					if encodeErr != nil {
						return encodeErr
					}
					if s.metrics != nil {
						s.metrics.SnapshotGeneration.Observe(time.Since(generationStarted).Seconds())
						s.metrics.SnapshotBytes.Observe(float64(encoded.Header.CompressedSize))
					}
					publicPayload, marshalErr := json.Marshal(snapshot)
					if marshalErr != nil {
						return marshalErr
					}
					if recordErr := s.store.RecordSnapshot(stream.Context(), snapshot.PoolID, snapshot.Revision, encoded.Header.ModelDigest, publicPayload, time.Now().UTC()); recordErr != nil {
						return recordErr
					}
					if err := s.core.MarkNginxReleaseSent(stream.Context(), identity.PoolID, identity.GatewayID, snapshot.Revision, encoded.Header.ModelDigest); err != nil {
						return err
					}
					if sendErr := stream.Send(&gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_SnapshotHeader{SnapshotHeader: encoded.Header}}); sendErr != nil {
						return sendErr
					}
					for _, chunk := range encoded.Chunks {
						if sendErr := stream.Send(&gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_SnapshotChunk{SnapshotChunk: chunk}}); sendErr != nil {
							return sendErr
						}
					}
					lastSentRevision = current.DesiredRevision
					lastSentSHA256 = encoded.Header.ModelDigest
				}
			}
		}
		leader, selectErr := s.store.SelectProbeLeader(stream.Context(), identity.PoolID, s.offlineTimeout)
		if selectErr != nil {
			return selectErr
		}
		if leader.ProbeGatewayID == identity.GatewayID && (leader.ProbeEpoch != lastProbeEpoch || leader.ProbeRevision != lastProbeRevision) {
			plan, planErr := s.store.BuildProbePlan(stream.Context(), identity.PoolID)
			if planErr != nil {
				return planErr
			}
			if sendErr := stream.Send(&gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_ProbePlan{ProbePlan: ProbePlanToProto(plan)}}); sendErr != nil {
				return sendErr
			}
			s.logger.Info("probe plan sent", "gateway_id", identity.GatewayID, "pool_id", identity.PoolID,
				"epoch", plan.Epoch, "revision", plan.Revision, "targets", len(plan.Targets))
			lastProbeEpoch, lastProbeRevision = plan.Epoch, plan.Revision
		} else if leader.ProbeGatewayID != identity.GatewayID && lastProbeEpoch >= 0 {
			empty := &gatewayv2.ProbePlan{PoolId: identity.PoolID, Epoch: leader.ProbeEpoch, ProbeRevision: leader.ProbeRevision}
			if sendErr := stream.Send(&gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_ProbePlan{ProbePlan: empty}}); sendErr != nil {
				return sendErr
			}
			s.logger.Info("probe plan withdrawn", "gateway_id", identity.GatewayID, "pool_id", identity.PoolID,
				"epoch", leader.ProbeEpoch, "revision", leader.ProbeRevision)
			lastProbeEpoch, lastProbeRevision = -1, -1
		}
		return nil
	}
	if err = sendState(); err != nil {
		s.logger.Error("prepare initial gateway state", "gateway_id", identity.GatewayID, "error", err)
		return connectStateError(err, "prepare initial gateway state")
	}
	var reconcileTimer *time.Timer
	var reconcileTimerC <-chan time.Time
	var dirtySince time.Time
	scheduleReconcile := func() {
		now := time.Now()
		if reconcileTimer == nil {
			dirtySince = now
			reconcileTimer = time.NewTimer(s.reconcileDelay)
			reconcileTimerC = reconcileTimer.C
			return
		}
		delay := s.reconcileDelay
		if remaining := time.Until(dirtySince.Add(s.reconcileMax)); remaining < delay {
			delay = max(remaining, 0)
		}
		if !reconcileTimer.Stop() {
			select {
			case <-reconcileTimer.C:
			default:
			}
		}
		reconcileTimer.Reset(delay)
	}
	defer func() {
		if reconcileTimer != nil {
			reconcileTimer.Stop()
		}
	}()

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case value := <-incoming:
			if errors.Is(value.err, io.EOF) {
				return nil
			}
			if value.err != nil {
				return value.err
			}
			if value.message.GetHello() != nil {
				return status.Error(codes.InvalidArgument, "duplicate Hello")
			}
			if result := value.message.GetValidationResult(); result != nil {
				_ = s.finishValidation(stream.Context(), identity.PoolID, identity.GatewayID, result)
				continue
			}
			if ack := value.message.GetAck(); ack != nil {
				if err := s.sendValidation(stream, identity.GatewayID, identity.PoolID); err != nil {
					return err
				}
				current, getErr := s.store.GetPool(stream.Context(), identity.PoolID)
				if getErr != nil || ack.ConfigRevision > current.DesiredRevision {
					return status.Error(codes.FailedPrecondition, "ACK revision exceeds desired revision")
				}
				if ack.ConfigRevision != lastSentRevision || ack.ModelDigest != lastSentSHA256 ||
					ack.RendererVersion != nginxx.RendererVersion || !validDigest(ack.RenderedDigest) {
					return status.Error(codes.FailedPrecondition, "ACK does not match the last transmitted snapshot")
				}
				if err = s.store.AckGateway(stream.Context(), identity.GatewayID, ack.ConfigRevision, ack.ModelDigest,
					ack.RendererVersion, ack.RenderedDigest, ""); err != nil {
					return status.Error(codes.Internal, "store gateway ACK")
				}
				if err = s.core.RecordNginxReleaseResult(stream.Context(), identity.PoolID, identity.GatewayID, ack.ConfigRevision, "SUCCEEDED", ack.ModelDigest, ack.RenderedDigest, ""); err != nil {
					return status.Error(codes.Internal, "record release ACK")
				}
				recordAgentApplyMetrics(s.metrics, "", ack.NginxValidateDurationNanos, ack.NginxReloadDurationNanos, true)
				appliedRevision = max(appliedRevision, ack.ConfigRevision)
				appliedModelDigest = ack.ModelDigest
				appliedRenderedDigest = ack.RenderedDigest
				lastSentRevision = 0
				lastSentSHA256 = ""
				failedRevision = 0
				nextRetry = time.Time{}
				retryBackoff = time.Second
				if err = sendState(); err != nil {
					return connectStateError(err, "send state after gateway ACK")
				}
			} else if nack := value.message.GetNack(); nack != nil {
				if nack.ConfigRevision != lastSentRevision || nack.ModelDigest != lastSentSHA256 || nack.RendererVersion != nginxx.RendererVersion {
					return status.Error(codes.FailedPrecondition, "NACK does not match the last transmitted snapshot")
				}
				message := truncate(nack.Error, 2048)
				if err = s.store.AckGateway(stream.Context(), identity.GatewayID, nack.ConfigRevision, nack.ModelDigest,
					nack.RendererVersion, "", message); err != nil {
					return status.Error(codes.Internal, "store gateway NACK")
				}
				resultState := "UNKNOWN"
				switch nack.FailedPhase {
				case "input", "decode", "identity", "render", "validate":
					resultState = "FAILED"
				}
				if err = s.core.RecordNginxReleaseResult(stream.Context(), identity.PoolID, identity.GatewayID, nack.ConfigRevision, resultState, "", "", message); err != nil {
					return status.Error(codes.Internal, "record release NACK")
				}
				recordAgentApplyMetrics(s.metrics, nack.FailedPhase, nack.NginxValidateDurationNanos, nack.NginxReloadDurationNanos, false)
				s.logger.Warn("gateway rejected snapshot", "gateway_id", identity.GatewayID, "revision", nack.ConfigRevision, "error", message)
				lastSentRevision = 0
				lastSentSHA256 = ""
				failedRevision = nack.ConfigRevision
				nextRetry = time.Now().Add(retryBackoff)
				retryBackoff = min(retryBackoff*2, 30*time.Second)
			} else if result := value.message.GetProbeResult(); result != nil {
				if result.PoolId != identity.PoolID {
					return status.Error(codes.PermissionDenied, "probe pool does not match gateway identity")
				}
				if err := s.sendValidation(stream, identity.GatewayID, identity.PoolID); err != nil {
					return err
				}
				current, getErr := s.store.GetPool(stream.Context(), identity.PoolID)
				if getErr != nil {
					return status.Error(codes.Internal, "read probe leader")
				}
				if current.ProbeGatewayID != identity.GatewayID {
					continue
				}
				probe := model.ProbeResult{PoolID: identity.PoolID, Epoch: result.Epoch, Revision: result.ProbeRevision,
					ServiceID: result.ServiceId, Sequence: result.Sequence, Success: result.Success,
					Latency: time.Duration(result.LatencyNanos), Error: truncate(result.Error, 1024),
					ObservedAt: time.Unix(0, result.ObservedAtUnixNano).UTC(), TargetToken: result.TargetToken}
				if probe.ObservedAt.IsZero() || result.ObservedAtUnixNano == 0 {
					probe.ObservedAt = time.Now().UTC()
				}
				changed, updateErr := s.store.UpdateServiceProbe(stream.Context(), probe)
				if updateErr != nil {
					return status.Error(codes.Internal, "store probe result")
				}
				if s.metrics != nil {
					outcome := "failure"
					if probe.Success {
						outcome = "success"
					}
					s.metrics.ProbeResults.WithLabelValues("agent", outcome).Inc()
				}
				if changed {
					updated, _ := s.store.GetPool(stream.Context(), identity.PoolID)
					s.broker.Publish(model.StateChange{PoolID: identity.PoolID, Revision: updated.DesiredRevision, Kind: "probe-result", IDs: []string{probe.ServiceID}})
				}
			} else if agentHeartbeat := value.message.GetHeartbeat(); agentHeartbeat != nil {
				if err := s.sendValidation(stream, identity.GatewayID, identity.PoolID); err != nil {
					return err
				}
				current, getErr := s.store.GetPool(stream.Context(), identity.PoolID)
				if getErr != nil {
					return status.Error(codes.Internal, "read pool for heartbeat")
				}
				if agentHeartbeat.AppliedProbeRevision < 0 || agentHeartbeat.AppliedProbeRevision > current.ProbeRevision {
					return status.Error(codes.FailedPrecondition, "heartbeat applied probe revision is outside the pool probe revision range")
				}
				if agentHeartbeat.AppliedConfigRevision != appliedRevision || agentHeartbeat.RendererVersion != nginxx.RendererVersion ||
					agentHeartbeat.AppliedModelDigest != appliedModelDigest || agentHeartbeat.RenderedDigest != appliedRenderedDigest {
					return status.Error(codes.FailedPrecondition, "heartbeat applied tuple does not match the acknowledged Gateway state")
				}
				if err = s.core.RecordGatewayEvidence(stream.Context(), identity.GatewayID, core.GatewayEvidence{
					ConfirmedRevision: agentHeartbeat.AppliedConfigRevision, LoadingConfirmed: agentHeartbeat.LoadingConfirmed,
					StartupState: agentHeartbeat.StartupState, DiskDigest: agentHeartbeat.DiskDigest,
					ModelDigest: agentHeartbeat.AppliedModelDigest, RenderedDigest: agentHeartbeat.RenderedDigest,
					ObservedAt: time.Unix(0, agentHeartbeat.EvidenceAtUnixNano).UTC(), RefreshIntervalMillis: agentHeartbeat.RefreshIntervalMillis,
				}); err != nil {
					return status.Error(codes.Internal, "store gateway evidence")
				}
				if err = s.store.HeartbeatGateway(stream.Context(), identity.GatewayID, current.DesiredRevision,
					agentHeartbeat.AppliedProbeRevision, time.Now().UTC()); err != nil {
					return status.Error(codes.Internal, "store gateway heartbeat")
				}
			}
		case <-changes:
			scheduleReconcile()
		case <-reconcileTimerC:
			reconcileTimer = nil
			reconcileTimerC = nil
			dirtySince = time.Time{}
			if err = sendState(); err != nil {
				return connectStateError(err, "send changed gateway state")
			}
		case <-poll.C:
			if err = sendState(); err != nil {
				return connectStateError(err, "poll gateway state")
			}
		case <-heartbeat.C:
			if err := s.sendValidation(stream, identity.GatewayID, identity.PoolID); err != nil {
				return err
			}
			current, getErr := s.store.GetPool(stream.Context(), identity.PoolID)
			if getErr != nil {
				return status.Error(codes.Internal, "read heartbeat state")
			}
			if err = stream.Send(&gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_Heartbeat{Heartbeat: &gatewayv2.ControllerHeartbeat{
				ConfigRevision: current.DesiredRevision, ProbeRevision: current.ProbeRevision, SentAtUnixNano: time.Now().UnixNano()}}}); err != nil {
				return err
			}
		}
	}
}

func connectStateError(err error, operation string) error {
	if errors.Is(err, errGatewayRevoked) || errors.Is(err, storage.ErrNotFound) {
		return status.Error(codes.PermissionDenied, errGatewayRevoked.Error())
	}
	return status.Error(codes.Internal, operation)
}

type agentIdentity struct {
	PoolID    string
	GatewayID string
}

func identityFromContext(ctx context.Context) (agentIdentity, string, error) {
	remote, ok := peer.FromContext(ctx)
	if !ok {
		return agentIdentity{}, "", errors.New("missing peer information")
	}
	tlsInfo, ok := remote.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.PeerCertificates) == 0 {
		return agentIdentity{}, "", errors.New("a verified mTLS client certificate is required")
	}
	certificate := tlsInfo.State.PeerCertificates[0]
	identity, err := identityFromCertificate(certificate)
	if err != nil {
		return agentIdentity{}, "", err
	}
	sum := sha256.Sum256(certificate.Raw)
	return identity, hex.EncodeToString(sum[:]), nil
}

func identityFromCertificate(certificate *x509.Certificate) (agentIdentity, error) {
	for _, uri := range certificate.URIs {
		if identity, ok := parseIdentityURI(uri); ok {
			return identity, nil
		}
	}
	return agentIdentity{}, errors.New("client certificate does not contain an NSC gateway identity URI")
}

func parseIdentityURI(uri *url.URL) (agentIdentity, bool) {
	if uri == nil || uri.Scheme != "spiffe" || uri.Host != identityHost {
		return agentIdentity{}, false
	}
	parts := strings.Split(strings.Trim(uri.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "gateway-pools" || parts[2] != "gateways" || parts[1] == "" || parts[3] == "" {
		return agentIdentity{}, false
	}
	if id.Validate(parts[1], id.Pool) != nil || id.Validate(parts[3], id.Gateway) != nil {
		return agentIdentity{}, false
	}
	return agentIdentity{PoolID: parts[1], GatewayID: parts[3]}, true
}

func AgentIdentityURI(poolID, gatewayID string) (*url.URL, error) {
	if err := id.Validate(poolID, id.Pool); err != nil {
		return nil, err
	}
	if err := id.Validate(gatewayID, id.Gateway); err != nil {
		return nil, err
	}
	return url.Parse("spiffe://" + identityHost + "/gateway-pools/" + poolID + "/gateways/" + gatewayID)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validateAppliedTuple(revision int64, modelDigest, renderedDigest string) error {
	if revision == 0 {
		if modelDigest != "" || renderedDigest != "" {
			return errors.New("revision zero must not carry model or rendered digests")
		}
		return nil
	}
	if !validDigest(modelDigest) {
		return errors.New("model digest must be 64 lowercase hexadecimal characters")
	}
	if !validDigest(renderedDigest) {
		return errors.New("rendered digest must be 64 lowercase hexadecimal characters")
	}
	return nil
}

func recordAgentApplyMetrics(metrics *metricsx.Metrics, failedPhase string, validationNanos, reloadNanos int64, success bool) {
	if metrics == nil {
		return
	}
	observe := func(nanos int64, histogram interface{ Observe(float64) }) {
		duration := time.Duration(nanos)
		if duration > 0 && duration <= 5*time.Minute {
			histogram.Observe(duration.Seconds())
		}
	}
	if success {
		metrics.NGINXValidations.WithLabelValues("success").Inc()
		metrics.NGINXReloads.WithLabelValues("success").Inc()
		observe(validationNanos, metrics.NGINXValidateTime)
		observe(reloadNanos, metrics.NGINXReloadTime)
		return
	}
	switch failedPhase {
	case "validate":
		metrics.NGINXValidations.WithLabelValues("failure").Inc()
		observe(validationNanos, metrics.NGINXValidateTime)
	case "activate":
		metrics.NGINXValidations.WithLabelValues("success").Inc()
		observe(validationNanos, metrics.NGINXValidateTime)
	case "reload":
		metrics.NGINXValidations.WithLabelValues("success").Inc()
		metrics.NGINXReloads.WithLabelValues("failure").Inc()
		observe(validationNanos, metrics.NGINXValidateTime)
		observe(reloadNanos, metrics.NGINXReloadTime)
	case "persist", "prune":
		metrics.NGINXValidations.WithLabelValues("success").Inc()
		metrics.NGINXReloads.WithLabelValues("success").Inc()
		observe(validationNanos, metrics.NGINXValidateTime)
		observe(reloadNanos, metrics.NGINXReloadTime)
	}
}
