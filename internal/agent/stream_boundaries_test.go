package agent

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/gateway"
	"google.golang.org/grpc"
)

type agentBoundaryStream struct {
	grpc.ClientStream
	ctx       context.Context
	input     chan receivedControllerMessage
	output    chan *gatewayv2.AgentMessage
	sendError error
	failSend  int64
	sends     atomic.Int64
}

func (s *agentBoundaryStream) Context() context.Context { return s.ctx }
func (s *agentBoundaryStream) Send(m *gatewayv2.AgentMessage) error {
	call := s.sends.Add(1)
	if s.sendError != nil && (s.failSend == 0 || call == s.failSend) {
		return s.sendError
	}
	select {
	case s.output <- m:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
func (s *agentBoundaryStream) Recv() (*gatewayv2.ControllerMessage, error) {
	select {
	case value := <-s.input:
		return value.message, value.err
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

type agentBoundaryClient struct {
	stream  *agentBoundaryStream
	failure error
}

func (c agentBoundaryClient) Connect(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[gatewayv2.AgentMessage, gatewayv2.ControllerMessage], error) {
	if c.failure != nil {
		return nil, c.failure
	}
	c.stream.ctx = ctx
	return c.stream, nil
}

func agentStreamFixture(t *testing.T) (*Agent, *agentBoundaryStream) {
	t.Helper()
	m := validationManagerFixture(t, "exit 0")
	cfg := config.Defaults()
	cfg.NGINX = m.config
	cfg.Agent.PoolID = "pool_test"
	cfg.Agent.GatewayID = "gateway_test"
	cfg.Agent.Mode = "production"
	cfg.Agent.HeartbeatInterval = config.Duration(time.Hour)
	a := New(cfg, "test", nil)
	a.generations = m
	s := &agentBoundaryStream{input: make(chan receivedControllerMessage, 32), output: make(chan *gatewayv2.AgentMessage, 32)}
	return a, s
}
func headerMessage(h *gatewayv2.SnapshotHeader) *gatewayv2.ControllerMessage {
	return &gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_SnapshotHeader{SnapshotHeader: h}}
}
func chunkMessage(c *gatewayv2.SnapshotChunk) *gatewayv2.ControllerMessage {
	return &gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_SnapshotChunk{SnapshotChunk: c}}
}
func planMessage(p *gatewayv2.ProbePlan) *gatewayv2.ControllerMessage {
	return &gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_ProbePlan{ProbePlan: p}}
}
func validationMessage(v *gatewayv2.ValidationRequest) *gatewayv2.ControllerMessage {
	return &gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_ValidationRequest{ValidationRequest: v}}
}

func TestAgentStreamRejectsInvalidSnapshotBoundaries(t *testing.T) {
	for _, test := range []struct {
		name     string
		header   func(*gatewayv2.SnapshotHeader)
		messages func() *gatewayv2.ControllerMessage
		want     string
	}{
		{"zero chunks", func(h *gatewayv2.SnapshotHeader) { h.ChunkCount = 0 }, nil, "invalid snapshot header"},
		{"too many chunks", func(h *gatewayv2.SnapshotHeader) { h.ChunkCount = 4097 }, nil, "invalid snapshot header"},
		{"negative compressed", func(h *gatewayv2.SnapshotHeader) { h.CompressedSize = -1 }, nil, "invalid snapshot header"},
		{"large compressed", func(h *gatewayv2.SnapshotHeader) { h.CompressedSize = gateway.MaxCompressedSize + 1 }, nil, "invalid snapshot header"},
		{"negative plain", func(h *gatewayv2.SnapshotHeader) { h.UncompressedSize = -1 }, nil, "invalid snapshot header"},
		{"large plain", func(h *gatewayv2.SnapshotHeader) { h.UncompressedSize = gateway.MaxUncompressedSize + 1 }, nil, "invalid snapshot header"},
		{"overlapping header", nil, func() *gatewayv2.ControllerMessage {
			return headerMessage(&gatewayv2.SnapshotHeader{ConfigRevision: 2, ChunkCount: 1})
		}, "before completing revision"},
		{"wrong revision", nil, func() *gatewayv2.ControllerMessage { return chunkMessage(&gatewayv2.SnapshotChunk{ConfigRevision: 2}) }, "unexpected snapshot chunk"},
		{"out of order", nil, func() *gatewayv2.ControllerMessage {
			return chunkMessage(&gatewayv2.SnapshotChunk{ConfigRevision: 1, Index: 1})
		}, "out-of-order"},
		{"oversized chunk", nil, func() *gatewayv2.ControllerMessage {
			return chunkMessage(&gatewayv2.SnapshotChunk{ConfigRevision: 1, Data: []byte("too much data")})
		}, "beyond the declared size"},
		{"short body", nil, func() *gatewayv2.ControllerMessage {
			return chunkMessage(&gatewayv2.SnapshotChunk{ConfigRevision: 1, Data: []byte("x")})
		}, "does not match the declared size"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, s := agentStreamFixture(t)
			h := &gatewayv2.SnapshotHeader{ConfigRevision: 1, ChunkCount: 1, CompressedSize: 3, UncompressedSize: 3}
			if test.header != nil {
				test.header(h)
			}
			s.input <- receivedControllerMessage{message: headerMessage(h)}
			if test.messages != nil {
				s.input <- receivedControllerMessage{message: test.messages()}
			}
			s.input <- receivedControllerMessage{err: io.EOF}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := a.runStream(ctx, agentBoundaryClient{stream: s}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("bad frame accepted: %v", err)
			}
			if a.applied.Load() != 0 || a.generations.State().ConfigRevision != 0 {
				t.Fatal("invalid stream changed confirmed revision")
			}
			hello := <-s.output
			if hello.GetHello() == nil || hello.GetHello().PoolId != "pool_test" || hello.GetHello().AppliedConfigRevision != 0 {
				t.Fatalf("missing identity handshake: %v", hello)
			}
		})
	}
	for _, mode := range []string{"production", "sandbox"} {
		t.Run("unheaded/"+mode, func(t *testing.T) {
			a, s := agentStreamFixture(t)
			a.config.Agent.Mode = mode
			s.input <- receivedControllerMessage{message: chunkMessage(&gatewayv2.SnapshotChunk{ConfigRevision: 1})}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := a.runStream(ctx, agentBoundaryClient{stream: s})
			if err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unheaded chunk not rejected: %v", err)
			}
		})
	}
}

func TestAgentStreamPoolRoleAndProbeFencing(t *testing.T) {
	for _, kind := range []string{"sandbox header", "sandbox chunk", "sandbox probes", "missing validation snapshot", "foreign validation", "foreign probe", "old epoch", "old probe revision", "invalid plan"} {
		t.Run(kind, func(t *testing.T) {
			a, s := agentStreamFixture(t)
			var message *gatewayv2.ControllerMessage
			switch kind {
			case "sandbox header":
				a.config.Agent.Mode = "sandbox"
				message = headerMessage(&gatewayv2.SnapshotHeader{ChunkCount: 1})
			case "sandbox chunk":
				a.config.Agent.Mode = "sandbox"
				message = chunkMessage(&gatewayv2.SnapshotChunk{})
			case "sandbox probes":
				a.config.Agent.Mode = "sandbox"
				message = planMessage(&gatewayv2.ProbePlan{PoolId: "pool_test"})
			case "missing validation snapshot":
				message = validationMessage(&gatewayv2.ValidationRequest{})
			case "foreign validation":
				message = validationMessage(&gatewayv2.ValidationRequest{Snapshot: &gatewayv2.Snapshot{PoolId: "foreign"}})
			case "foreign probe":
				message = planMessage(&gatewayv2.ProbePlan{PoolId: "foreign"})
			case "old epoch":
				a.probeEpoch.Store(2)
				message = planMessage(&gatewayv2.ProbePlan{PoolId: "pool_test", Epoch: 1})
			case "old probe revision":
				a.appliedProbe.Store(2)
				message = planMessage(&gatewayv2.ProbePlan{PoolId: "pool_test", Epoch: 3, ProbeRevision: 1})
			case "invalid plan":
				message = planMessage(&gatewayv2.ProbePlan{PoolId: "pool_test", ProbeRevision: -1})
			}
			beforeEpoch, beforeRevision := a.probeEpoch.Load(), a.appliedProbe.Load()
			s.input <- receivedControllerMessage{message: message}
			s.input <- receivedControllerMessage{err: io.EOF}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := a.runStream(ctx, agentBoundaryClient{stream: s})
			if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unsafe command accepted: %v", err)
			}
			if a.probeEpoch.Load() != beforeEpoch || a.appliedProbe.Load() != beforeRevision {
				t.Fatal("rejected plan changed probe fence")
			}
		})
	}
}

func TestAgentStreamTransportErrorsAndCancellation(t *testing.T) {
	failure := errors.New("controller transport interrupted")
	for _, kind := range []string{"connect", "hello", "receive", "EOF", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			a, s := agentStreamFixture(t)
			client := agentBoundaryClient{stream: s}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := failure
			switch kind {
			case "connect":
				client.failure = failure
			case "hello":
				s.sendError = failure
			case "receive":
				s.input <- receivedControllerMessage{err: failure}
			case "EOF":
				want = io.EOF
				s.input <- receivedControllerMessage{err: io.EOF}
			case "cancel":
				want = context.Canceled
				cancel()
			}
			if err := a.runStream(ctx, client); !errors.Is(err, want) {
				t.Fatalf("transport error hidden: %v want %v", err, want)
			}
			if a.applied.Load() != 0 {
				t.Fatal("transport success invented")
			}
		})
	}
}

func TestAgentStreamValidationResultAndProbeRevision(t *testing.T) {
	for _, sendFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "send failure"}[sendFails], func(t *testing.T) {
			a, s := agentStreamFixture(t)
			failure := errors.New("result send failed")
			if sendFails {
				s.sendError = failure
				s.failSend = 2
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- a.runStream(ctx, agentBoundaryClient{stream: s}) }()
			select {
			case m := <-s.output:
				if m.GetHello() == nil {
					t.Fatal("first message not hello")
				}
			case <-ctx.Done():
				t.Fatal("hello missing")
			}
			s.input <- receivedControllerMessage{message: planMessage(&gatewayv2.ProbePlan{PoolId: "pool_test", Epoch: 4, ProbeRevision: 7})}
			s.input <- receivedControllerMessage{message: validationMessage(&gatewayv2.ValidationRequest{TaskId: "task", Snapshot: validationSnapshot()})}
			if sendFails {
				select {
				case err := <-done:
					if !errors.Is(err, failure) {
						t.Fatalf("send failure hidden: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("send failure did not stop stream")
				}
			} else {
				select {
				case m := <-s.output:
					r := m.GetValidationResult()
					if r == nil || r.TaskId != "task" || !r.Success {
						t.Fatalf("validation result: %v", m)
					}
				case <-ctx.Done():
					t.Fatal("validation result missing")
				}
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel: %v", err)
				}
			}
			if a.probeEpoch.Load() != 4 || a.appliedProbe.Load() != 7 || a.applied.Load() != 0 {
				t.Fatal("probe/config state conflated")
			}
		})
	}
}
