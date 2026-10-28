package gateway

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type reconciliationStream struct {
	grpc.ServerStream
	ctx            context.Context
	input          chan *gatewayv2.AgentMessage
	output         chan *gatewayv2.ControllerMessage
	heartbeatError error
}

func (s *reconciliationStream) Context() context.Context { return s.ctx }
func (s *reconciliationStream) Recv() (*gatewayv2.AgentMessage, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case m := <-s.input:
		return m, nil
	}
}
func (s *reconciliationStream) Send(m *gatewayv2.ControllerMessage) error {
	if m.GetHeartbeat() != nil && s.heartbeatError != nil {
		return s.heartbeatError
	}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.output <- m:
		return nil
	}
}
func (s *reconciliationStream) await(t *testing.T, predicate func(*gatewayv2.ControllerMessage) bool) *gatewayv2.ControllerMessage {
	t.Helper()
	for {
		select {
		case m := <-s.output:
			if predicate(m) {
				return m
			}
		case <-s.ctx.Done():
			t.Fatal("gateway did not produce the expected state before its deadline", s.ctx.Err())
			return nil
		}
	}
}

type reconciliationStore struct {
	storage.Store
	revoked     atomic.Bool
	unavailable atomic.Bool
	reads       atomic.Int64
}

func (s *reconciliationStore) GetGateway(ctx context.Context, key string) (model.Gateway, error) {
	s.reads.Add(1)
	if s.unavailable.Load() {
		return model.Gateway{}, errors.New("private gateway database diagnostic")
	}
	g, e := s.Store.GetGateway(ctx, key)
	if s.revoked.Load() {
		g.Enabled = false
	}
	return g, e
}
func TestGatewayReconcilesNotificationsPollingAndControllerHeartbeat(t *testing.T) {
	for _, mode := range []string{"changed plan", "revoked on notification", "revoked on poll", "storage lost on notification", "heartbeat", "heartbeat send failure"} {
		t.Run(mode, func(t *testing.T) {
			server, base, fixture, hello := gatewayHandshakeFixture(t)
			server.reconcileDelay = 40 * time.Millisecond
			server.reconcileMax = 100 * time.Millisecond
			ctx, cancel := context.WithTimeout(context.WithoutCancel(fixture.ctx), 8*time.Second)
			defer cancel()
			stream := &reconciliationStream{ctx: ctx, input: make(chan *gatewayv2.AgentMessage, 1), output: make(chan *gatewayv2.ControllerMessage, 32)}
			failure := errors.New("heartbeat transport interrupted")
			if mode == "heartbeat send failure" {
				stream.heartbeatError = failure
			}
			store := &reconciliationStore{Store: base}
			server.store = store
			stream.input <- &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}
			done := make(chan error, 1)
			go func() { done <- server.Connect(stream) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("stream did not stop after cancellation")
				}
			}()
			initial := stream.await(t, func(m *gatewayv2.ControllerMessage) bool { return m.GetProbePlan() != nil }).GetProbePlan()
			switch mode {
			case "changed plan":
				if _, e := base.DB().ExecContext(ctx, "UPDATE gateway_pools SET probe_revision=probe_revision+1 WHERE id=?", hello.PoolId); e != nil {
					t.Fatal(e)
				}
				for range 4 {
					server.broker.Publish(model.StateChange{PoolID: hello.PoolId, Kind: "probe"})
					time.Sleep(10 * time.Millisecond)
				}
				updated := stream.await(t, func(m *gatewayv2.ControllerMessage) bool { return m.GetProbePlan() != nil }).GetProbePlan()
				if updated.ProbeRevision != initial.ProbeRevision+1 || updated.PoolId != hello.PoolId {
					t.Fatal("notification did not reconcile the latest pool probe plan", updated)
				}
			case "heartbeat":
				heartbeat := stream.await(t, func(m *gatewayv2.ControllerMessage) bool { return m.GetHeartbeat() != nil }).GetHeartbeat()
				pool, e := base.GetPool(ctx, hello.PoolId)
				if e != nil || heartbeat.ConfigRevision != pool.DesiredRevision || heartbeat.ProbeRevision != pool.ProbeRevision || heartbeat.SentAtUnixNano <= 0 || store.reads.Load() < 4 {
					t.Fatal("heartbeat lost the current target or periodic revocation checks", heartbeat, e)
				}
			default:
				if mode == "storage lost on notification" {
					store.unavailable.Store(true)
				} else if mode != "heartbeat send failure" {
					store.revoked.Store(true)
				}
				if strings.HasSuffix(mode, "notification") {
					server.broker.Publish(model.StateChange{PoolID: hello.PoolId, Kind: "gateway"})
				}
				select {
				case e := <-done:
					done <- e // Preserve completion for cleanup.
					if mode == "heartbeat send failure" {
						if !errors.Is(e, failure) {
							t.Fatal("heartbeat transport failure was hidden", e)
						}
					} else if mode == "storage lost on notification" {
						if status.Code(e) != codes.Internal || strings.Contains(e.Error(), "private") {
							t.Fatal("reconcile failure was hidden or leaked", e)
						}
					} else if status.Code(e) != codes.PermissionDenied {
						t.Fatal("revoked stream was not terminated", e)
					}
				case <-ctx.Done():
					t.Fatal("stream ignored revoked or unavailable state", ctx.Err())
				}
			}
			gateway, e := base.GetGateway(context.Background(), hello.GatewayId)
			if e != nil || gateway.AppliedRevision != 0 || gateway.RenderedDigest != "" {
				t.Fatal("background reconciliation confirmed an unacknowledged version", gateway, e)
			}
		})
	}
}
