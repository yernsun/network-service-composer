package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type boundaryGatewayStream struct {
	grpc.ServerStream
	ctx                     context.Context
	input                   []*gatewayv2.AgentMessage
	output                  []*gatewayv2.ControllerMessage
	receiveError, sendError error
	block                   bool
}

func (s *boundaryGatewayStream) Context() context.Context { return s.ctx }
func (s *boundaryGatewayStream) Recv() (*gatewayv2.AgentMessage, error) {
	if s.block {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	if len(s.input) == 0 {
		if s.receiveError != nil {
			return nil, s.receiveError
		}
		return nil, io.EOF
	}
	message := s.input[0]
	s.input = s.input[1:]
	return message, nil
}
func (s *boundaryGatewayStream) Send(message *gatewayv2.ControllerMessage) error {
	if s.sendError != nil {
		return s.sendError
	}
	s.output = append(s.output, message)
	return nil
}

func gatewayHandshakeFixture(t *testing.T) (*Server, *storage.SQLStore, *boundaryGatewayStream, *gatewayv2.Hello) {
	t.Helper()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "gateway.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "gateway-handshake", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, DesiredRevision: 2, ProbeRevision: 2, Revision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err = store.CreatePool(context.Background(), &pool); err != nil {
		t.Fatal(err)
	}
	application := core.New(store, config.Defaults(), nil)
	server := NewServer(application, nil, ServerOptions{ReconcileDelay: -1, ReconcileMaxWait: time.Millisecond}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	hello := &gatewayv2.Hello{GatewayId: id.New(id.Gateway), PoolId: pool.ID, ProtocolVersion: ProtocolVersion, RendererVersion: nginxx.RendererVersion}
	identity, err := AgentIdentityURI(pool.ID, hello.GatewayId)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{Raw: []byte("verified gateway certificate"), URIs: []*url.URL{identity}}
	// Handshake behavior must not depend on host load during fixture setup.
	// Cancellation cases provide their own trigger; the test runner bounds hangs.
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	ctx = peer.NewContext(ctx, &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{certificate}}, PeerCertificates: []*x509.Certificate{certificate}}}})
	return server, store, &boundaryGatewayStream{ctx: ctx}, hello
}

func TestGatewayHelloRejectsIdentityProtocolAndEvidenceMismatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*gatewayv2.Hello)
		code   codes.Code
	}{
		{"gateway mismatch", func(h *gatewayv2.Hello) { h.GatewayId = id.New(id.Gateway) }, codes.PermissionDenied},
		{"pool mismatch", func(h *gatewayv2.Hello) { h.PoolId = id.New(id.Pool) }, codes.PermissionDenied},
		{"mode mismatch", func(h *gatewayv2.Hello) { h.Mode = "sandbox" }, codes.PermissionDenied},
		{"protocol mismatch", func(h *gatewayv2.Hello) { h.ProtocolVersion-- }, codes.FailedPrecondition},
		{"renderer mismatch", func(h *gatewayv2.Hello) { h.RendererVersion = "old" }, codes.FailedPrecondition},
		{"negative config", func(h *gatewayv2.Hello) { h.AppliedConfigRevision = -1 }, codes.FailedPrecondition},
		{"future config", func(h *gatewayv2.Hello) { h.AppliedConfigRevision = 3 }, codes.FailedPrecondition},
		{"negative probe", func(h *gatewayv2.Hello) { h.AppliedProbeRevision = -1 }, codes.FailedPrecondition},
		{"future probe", func(h *gatewayv2.Hello) { h.AppliedProbeRevision = 3 }, codes.FailedPrecondition},
		{"bootstrap digest", func(h *gatewayv2.Hello) { h.RenderedDigest = strings.Repeat("a", 64) }, codes.FailedPrecondition},
		{"applied tuple missing digest", func(h *gatewayv2.Hello) { h.AppliedConfigRevision = 1 }, codes.FailedPrecondition},
		{"desired revision wrong digest", func(h *gatewayv2.Hello) {
			h.AppliedConfigRevision = 2
			h.AppliedModelDigest = strings.Repeat("a", 64)
			h.RenderedDigest = strings.Repeat("b", 64)
		}, codes.FailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, stream, hello := gatewayHandshakeFixture(t)
			gatewayID := hello.GatewayId
			tc.mutate(hello)
			stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}}
			err := server.Connect(stream)
			if status.Code(err) != tc.code || len(stream.output) != 0 {
				t.Fatalf("invalid Hello accepted: code=%s error=%v messages=%d", status.Code(err), err, len(stream.output))
			}
			if tc.name == "protocol mismatch" || tc.name == "renderer mismatch" {
				gateway, err := store.GetGateway(context.Background(), gatewayID)
				if err != nil || gateway.Status != model.GatewayIncompatible || gateway.LastError == "" {
					t.Fatalf("incompatibility not recorded: %#v %v", gateway, err)
				}
			}
		})
	}
}

func TestGatewayHandshakeMissingHelloDisconnectAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   []*gatewayv2.AgentMessage
		failure error
		code    codes.Code
	}{
		{"empty message", []*gatewayv2.AgentMessage{{}}, nil, codes.InvalidArgument},
		{"EOF", nil, io.EOF, codes.Unknown},
		{"transport error", nil, errors.New("transport interrupted"), codes.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _, stream, _ := gatewayHandshakeFixture(t)
			stream.input = tc.input
			stream.receiveError = tc.failure
			err := server.Connect(stream)
			if status.Code(err) != tc.code {
				t.Fatalf("handshake %v", err)
			}
			if tc.failure != nil && !errors.Is(err, tc.failure) {
				t.Fatalf("transport error hidden: %v", err)
			}
		})
	}
	server, _, stream, _ := gatewayHandshakeFixture(t)
	ctx, cancel := context.WithCancel(stream.ctx)
	stream.ctx = ctx
	stream.block = true
	cancel()
	if err := server.Connect(stream); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Hello: %v", err)
	}
	anonymous := &boundaryGatewayStream{ctx: context.Background()}
	if err := server.Connect(anonymous); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous connection accepted: %v", err)
	}
}

func TestDisabledGatewayPoolNeverReceivesConfiguration(t *testing.T) {
	server, store, stream, hello := gatewayHandshakeFixture(t)
	pool, err := store.GetPool(context.Background(), hello.PoolId)
	if err != nil {
		t.Fatal(err)
	}
	pool.Enabled = false
	if err = store.UpdatePool(context.Background(), &pool, pool.Revision); err != nil {
		t.Fatal(err)
	}
	stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}}
	if err = server.Connect(stream); status.Code(err) != codes.PermissionDenied || len(stream.output) != 0 {
		t.Fatalf("disabled pool sent configuration: %v", err)
	}
}

func TestGatewayRejectsUncorrelatedAcknowledgementsAndHeartbeats(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message func(*gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage
		code    codes.Code
	}{
		{"duplicate Hello", func(_ *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Hello{Hello: &gatewayv2.Hello{}}}
		}, codes.InvalidArgument},
		{"ACK beyond target", func(h *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{ConfigRevision: 3}}}
		}, codes.FailedPrecondition},
		{"ACK wrong revision", func(h *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{ConfigRevision: 1, ModelDigest: h.ModelDigest, RendererVersion: nginxx.RendererVersion, RenderedDigest: strings.Repeat("a", 64)}}}
		}, codes.FailedPrecondition},
		{"ACK wrong model", func(h *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{ConfigRevision: 2, ModelDigest: strings.Repeat("0", 64), RendererVersion: nginxx.RendererVersion, RenderedDigest: strings.Repeat("a", 64)}}}
		}, codes.FailedPrecondition},
		{"ACK wrong renderer", func(h *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{ConfigRevision: 2, ModelDigest: h.ModelDigest, RendererVersion: "wrong", RenderedDigest: strings.Repeat("a", 64)}}}
		}, codes.FailedPrecondition},
		{"ACK malformed rendered digest", func(h *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{ConfigRevision: 2, ModelDigest: h.ModelDigest, RendererVersion: nginxx.RendererVersion, RenderedDigest: "invalid"}}}
		}, codes.FailedPrecondition},
		{"NACK wrong revision", func(h *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Nack{Nack: &gatewayv2.ApplyNack{ConfigRevision: 1, ModelDigest: h.ModelDigest, RendererVersion: nginxx.RendererVersion}}}
		}, codes.FailedPrecondition},
		{"NACK wrong model", func(h *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Nack{Nack: &gatewayv2.ApplyNack{ConfigRevision: 2, ModelDigest: "wrong", RendererVersion: nginxx.RendererVersion}}}
		}, codes.FailedPrecondition},
		{"NACK wrong renderer", func(h *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Nack{Nack: &gatewayv2.ApplyNack{ConfigRevision: 2, ModelDigest: h.ModelDigest, RendererVersion: "wrong"}}}
		}, codes.FailedPrecondition},
		{"heartbeat future probe", func(_ *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{AppliedProbeRevision: 3, RendererVersion: nginxx.RendererVersion}}}
		}, codes.FailedPrecondition},
		{"heartbeat negative probe", func(_ *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{AppliedProbeRevision: -1, RendererVersion: nginxx.RendererVersion}}}
		}, codes.FailedPrecondition},
		{"heartbeat unacknowledged revision", func(_ *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{AppliedConfigRevision: 1, RendererVersion: nginxx.RendererVersion}}}
		}, codes.FailedPrecondition},
		{"heartbeat wrong renderer", func(_ *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{RendererVersion: "wrong"}}}
		}, codes.FailedPrecondition},
		{"heartbeat wrong model", func(_ *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{RendererVersion: nginxx.RendererVersion, AppliedModelDigest: "wrong"}}}
		}, codes.FailedPrecondition},
		{"heartbeat wrong rendered digest", func(_ *gatewayv2.SnapshotHeader) *gatewayv2.AgentMessage {
			return &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{RendererVersion: nginxx.RendererVersion, RenderedDigest: "wrong"}}}
		}, codes.FailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, stream, hello := gatewayHandshakeFixture(t)
			snapshot, err := server.core.BuildGatewaySnapshot(context.Background(), hello.PoolId)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeSnapshot(snapshot, DefaultChunkSize)
			if err != nil {
				t.Fatal(err)
			}
			stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}, tc.message(encoded.Header)}
			err = server.Connect(stream)
			if status.Code(err) != tc.code {
				t.Fatalf("uncorrelated message result: %v", err)
			}
			gateway, err := store.GetGateway(context.Background(), hello.GatewayId)
			if err != nil || gateway.AppliedRevision != 0 || gateway.ModelDigest != "" || gateway.RenderedDigest != "" {
				t.Fatalf("rejected evidence changed confirmed version: %#v %v", gateway, err)
			}
		})
	}
}

func TestGatewayAcknowledgedVersionAndHeartbeatEvidenceRemainCorrelated(t *testing.T) {
	server, store, stream, hello := gatewayHandshakeFixture(t)
	hello.Name = "named-gateway"
	hello.Address = "192.0.2.10"
	snapshot, err := server.core.BuildGatewaySnapshot(context.Background(), hello.PoolId)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeSnapshot(snapshot, DefaultChunkSize)
	if err != nil {
		t.Fatal(err)
	}
	rendered := strings.Repeat("a", 64)
	stream.input = []*gatewayv2.AgentMessage{
		{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}},
		{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{ConfigRevision: 2, ModelDigest: encoded.Header.ModelDigest, RendererVersion: nginxx.RendererVersion, RenderedDigest: rendered}}},
		{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{AppliedConfigRevision: 2, AppliedProbeRevision: 2, AppliedModelDigest: encoded.Header.ModelDigest, RendererVersion: nginxx.RendererVersion, RenderedDigest: rendered, LoadingConfirmed: true, StartupState: "CONFIRMED", DiskDigest: rendered, EvidenceAtUnixNano: time.Now().UnixNano(), RefreshIntervalMillis: 1000}}},
		{},
	}
	if err = server.Connect(stream); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetGateway(context.Background(), hello.GatewayId)
	if err != nil || stored.AppliedRevision != 2 || stored.AppliedProbeRevision != 2 || stored.ModelDigest != encoded.Header.ModelDigest || stored.RenderedDigest != rendered || stored.Name != hello.Name || stored.Address != hello.Address {
		t.Fatalf("acknowledged state lost: %#v %v", stored, err)
	}
	headers := 0
	for _, message := range stream.output {
		if message.GetSnapshotHeader() != nil {
			headers++
		}
	}
	if headers != 1 {
		t.Fatalf("ACK retransmitted completed version: %d snapshots", headers)
	}
}

func TestGatewayNACKPreservesLastConfirmedVersion(t *testing.T) {
	for _, phase := range []string{"input", "decode", "identity", "render", "validate", "activate", "reload", "persist", "prune", "unknown"} {
		t.Run(phase, func(t *testing.T) {
			server, store, stream, hello := gatewayHandshakeFixture(t)
			if phase != "input" {
				hello.AppliedConfigRevision = 1
				hello.AppliedModelDigest = strings.Repeat("c", 64)
				hello.RenderedDigest = strings.Repeat("d", 64)
			}
			snapshot, err := server.core.BuildGatewaySnapshot(context.Background(), hello.PoolId)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeSnapshot(snapshot, DefaultChunkSize)
			if err != nil {
				t.Fatal(err)
			}
			stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}, {Payload: &gatewayv2.AgentMessage_Nack{Nack: &gatewayv2.ApplyNack{ConfigRevision: 2, ModelDigest: encoded.Header.ModelDigest, RendererVersion: nginxx.RendererVersion, FailedPhase: phase, Error: strings.Repeat("x", 2050)}}}}
			if err = server.Connect(stream); err != nil {
				t.Fatal(err)
			}
			stored, err := store.GetGateway(context.Background(), hello.GatewayId)
			if err != nil || stored.AppliedRevision != hello.AppliedConfigRevision || stored.ModelDigest != hello.AppliedModelDigest || stored.RenderedDigest != hello.RenderedDigest || len(stored.LastError) != 2048 {
				t.Fatalf("rejection became success or lost diagnosis: %#v %v", stored, err)
			}
		})
	}
}
