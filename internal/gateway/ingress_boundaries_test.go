package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type gatewayIngressStore struct {
	storage.Store
	stage     string
	failure   error
	afterAck  bool
	poolReads int
	probes    []model.ProbeResult
}

func (s *gatewayIngressStore) GetPool(ctx context.Context, key string) (model.GatewayPool, error) {
	s.poolReads++
	if s.stage == "pool" && s.poolReads >= 3 {
		return model.GatewayPool{}, s.failure
	}
	return s.Store.GetPool(ctx, key)
}
func (s *gatewayIngressStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if s.stage == "result" && s.afterAck && strings.HasSuffix(key, ":nginx-release-head") {
		return storage.ControlDocument{}, s.failure
	}
	if s.stage == "evidence" && strings.HasSuffix(key, ":evidence") {
		return storage.ControlDocument{}, s.failure
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s *gatewayIngressStore) AckGateway(ctx context.Context, key string, revision int64, digest, renderer, rendered, message string) error {
	if s.stage == "ack" {
		return s.failure
	}
	err := s.Store.AckGateway(ctx, key, revision, digest, renderer, rendered, message)
	s.afterAck = true
	return err
}
func (s *gatewayIngressStore) HeartbeatGateway(ctx context.Context, key string, revision, probe int64, now time.Time) error {
	if s.stage == "heartbeat" {
		return s.failure
	}
	return s.Store.HeartbeatGateway(ctx, key, revision, probe, now)
}
func (s *gatewayIngressStore) UpdateServiceProbe(_ context.Context, p model.ProbeResult) (bool, error) {
	s.probes = append(s.probes, p)
	if s.stage == "probe" {
		return false, s.failure
	}
	return s.stage == "changed", nil
}

func TestGatewayIncomingPersistenceFailuresDoNotReportSuccess(t *testing.T) {
	for _, kind := range []string{"ACK", "NACK", "heartbeat", "probe"} {
		stages := []string{"pool", "ack", "result"}
		if kind == "NACK" {
			stages = []string{"ack", "result"}
		}
		if kind == "heartbeat" {
			stages = []string{"pool", "evidence", "heartbeat"}
		}
		if kind == "probe" {
			stages = []string{"pool", "probe"}
		}
		for _, stage := range stages {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				server, db, stream, hello := gatewayHandshakeFixture(t)
				snapshot, err := server.core.BuildGatewaySnapshot(context.Background(), hello.PoolId)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := EncodeSnapshot(snapshot, DefaultChunkSize)
				if err != nil {
					t.Fatal(err)
				}
				boundary := &gatewayIngressStore{Store: db, stage: stage, failure: errors.New("private gateway storage failure")}
				server.store = boundary
				server.core = core.New(boundary, config.Defaults(), nil)
				var incoming *gatewayv2.AgentMessage
				switch kind {
				case "ACK":
					incoming = &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{ConfigRevision: 2, ModelDigest: encoded.Header.ModelDigest, RendererVersion: nginxx.RendererVersion, RenderedDigest: strings.Repeat("a", 64)}}}
				case "NACK":
					incoming = &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Nack{Nack: &gatewayv2.ApplyNack{ConfigRevision: 2, ModelDigest: encoded.Header.ModelDigest, RendererVersion: nginxx.RendererVersion, FailedPhase: "validate", Error: "invalid candidate"}}}
				case "heartbeat":
					incoming = &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{RendererVersion: nginxx.RendererVersion, EvidenceAtUnixNano: time.Now().UnixNano(), RefreshIntervalMillis: 1000, StartupState: "UNKNOWN"}}}
				case "probe":
					incoming = &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_ProbeResult{ProbeResult: &gatewayv2.ProbeResult{PoolId: hello.PoolId, ServiceId: "service", Success: true}}}
				}
				stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}, incoming}
				err = server.Connect(stream)
				want := codes.Internal
				if kind == "ACK" && stage == "pool" {
					want = codes.FailedPrecondition
				}
				if status.Code(err) != want || strings.Contains(err.Error(), boundary.failure.Error()) {
					t.Fatalf("persistence error leaked or became success: %v", err)
				}
				g, readErr := db.GetGateway(context.Background(), hello.GatewayId)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if kind != "ACK" && g.AppliedRevision != 0 {
					t.Fatal("non-ACK input confirmed a running revision")
				}
			})
		}
	}
}

func TestGatewayProbeEvidencePreservesTimestampOutcomeAndBounds(t *testing.T) {
	for _, success := range []bool{false, true} {
		for _, timestamp := range []int64{0, time.Now().UnixNano()} {
			server, db, stream, hello := gatewayHandshakeFixture(t)
			boundary := &gatewayIngressStore{Store: db, stage: "changed"}
			server.store = boundary
			server.metrics = metricsx.New()
			stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}, {Payload: &gatewayv2.AgentMessage_ProbeResult{ProbeResult: &gatewayv2.ProbeResult{PoolId: hello.PoolId, ServiceId: "service", Epoch: 3, ProbeRevision: 2, Sequence: 4, TargetToken: "token", Success: success, Error: strings.Repeat("x", 1200), ObservedAtUnixNano: timestamp, LatencyNanos: int64(time.Millisecond)}}}}
			before := time.Now()
			if err := server.Connect(stream); err != nil {
				t.Fatal(err)
			}
			if len(boundary.probes) != 1 {
				t.Fatal("probe evidence was discarded")
			}
			p := boundary.probes[0]
			if p.Success != success || p.ServiceID != "service" || p.TargetToken != "token" || p.Sequence != 4 || p.Epoch != 3 || len(p.Error) != 1024 || p.Latency != time.Millisecond {
				t.Fatalf("probe facts changed: %+v", p)
			}
			if timestamp != 0 && p.ObservedAt.UnixNano() != timestamp {
				t.Fatal("agent timestamp replaced")
			}
			if timestamp == 0 && p.ObservedAt.Before(before) {
				t.Fatal("missing probe timestamp was not bounded to receipt")
			}
		}
	}
}

func TestGatewayRejectsProbeOutsideAuthenticatedPool(t *testing.T) {
	for _, test := range []struct{ name, poolID string }{{"empty pool", ""}, {"foreign pool", "another-pool"}} {
		t.Run(test.name, func(t *testing.T) {
			server, db, stream, hello := gatewayHandshakeFixture(t)
			boundary := &gatewayIngressStore{Store: db}
			server.store = boundary
			stream.input = []*gatewayv2.AgentMessage{
				{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}},
				{Payload: &gatewayv2.AgentMessage_ProbeResult{ProbeResult: &gatewayv2.ProbeResult{
					PoolId: test.poolID, ServiceId: "foreign-service", Sequence: 1<<63 - 1, Success: true,
				}}},
			}
			if err := server.Connect(stream); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("cross-pool probe must be denied: %v", err)
			}
			if len(boundary.probes) != 0 {
				t.Fatal("unauthorized health evidence reached storage")
			}
		})
	}
}
