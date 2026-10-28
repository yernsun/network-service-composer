package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type gatewayFailureStore struct {
	storage.Store
	resource      string
	failure       error
	poolCalls     int
	failPoolAt    int
	changeGateway func(*model.Gateway)
}

func (s *gatewayFailureStore) GetPool(ctx context.Context, key string) (model.GatewayPool, error) {
	s.poolCalls++
	if s.resource == "pool" && s.poolCalls == s.failPoolAt {
		return model.GatewayPool{}, s.failure
	}
	return s.Store.GetPool(ctx, key)
}
func (s *gatewayFailureStore) GetGateway(ctx context.Context, key string) (model.Gateway, error) {
	if s.resource == "gateway" {
		return model.Gateway{}, s.failure
	}
	value, err := s.Store.GetGateway(ctx, key)
	if err == nil && s.changeGateway != nil {
		s.changeGateway(&value)
	}
	return value, err
}
func (s *gatewayFailureStore) UpsertGateway(ctx context.Context, value *model.Gateway) error {
	if s.resource == "register" {
		return s.failure
	}
	return s.Store.UpsertGateway(ctx, value)
}
func (s *gatewayFailureStore) SelectProbeLeader(ctx context.Context, key string, timeout time.Duration) (model.GatewayPool, error) {
	if s.resource == "leader" {
		return model.GatewayPool{}, s.failure
	}
	return s.Store.SelectProbeLeader(ctx, key, timeout)
}
func (s *gatewayFailureStore) BuildProbePlan(ctx context.Context, key string) (model.ProbePlan, error) {
	if s.resource == "probe plan" {
		return model.ProbePlan{}, s.failure
	}
	return s.Store.BuildProbePlan(ctx, key)
}

func TestGatewayInitialStateFailuresNeverConfirmAppliedVersion(t *testing.T) {
	failure := errors.New("private database error")
	for _, tc := range []struct {
		name, resource string
		failure        error
		poolAt         int
		code           codes.Code
		change         func(*model.Gateway)
	}{
		{"pool unavailable", "pool", failure, 1, codes.PermissionDenied, nil},
		{"pool lost after registration", "pool", failure, 2, codes.Internal, nil},
		{"enrollment conflict", "register", storage.ErrConflict, 0, codes.PermissionDenied, nil},
		{"registration failure", "register", failure, 0, codes.Internal, nil},
		{"gateway revoked", "gateway", storage.ErrNotFound, 0, codes.PermissionDenied, nil},
		{"gateway read failed", "gateway", failure, 0, codes.Internal, nil},
		{"disabled after registration", "", nil, 0, codes.PermissionDenied, func(g *model.Gateway) { g.Enabled = false }},
		{"pool changed after registration", "", nil, 0, codes.PermissionDenied, func(g *model.Gateway) { g.PoolID = "other" }},
		{"certificate changed after registration", "", nil, 0, codes.PermissionDenied, func(g *model.Gateway) { g.CertificateHash = "different" }},
		{"leader read failed", "leader", failure, 0, codes.Internal, nil},
		{"probe plan failed", "probe plan", failure, 0, codes.Internal, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, stream, hello := gatewayHandshakeFixture(t)
			server.store = &gatewayFailureStore{Store: store, resource: tc.resource, failure: tc.failure, failPoolAt: tc.poolAt, changeGateway: tc.change}
			stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}}
			err := server.Connect(stream)
			if status.Code(err) != tc.code {
				t.Fatalf("state failure status: %v", err)
			}
			value, err := store.GetGateway(context.Background(), hello.GatewayId)
			if err == nil && value.AppliedRevision != 0 {
				t.Fatal("failed connection confirmed new version")
			}
		})
	}
}

func TestGatewayTransportFailurePreservesUnknownDelivery(t *testing.T) {
	for _, sendFailure := range []bool{false, true} {
		server, store, stream, hello := gatewayHandshakeFixture(t)
		failure := errors.New("connection interrupted")
		stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}}
		if sendFailure {
			stream.sendError = failure
		} else {
			stream.receiveError = failure
		}
		err := server.Connect(stream)
		if sendFailure {
			if status.Code(err) != codes.Internal {
				t.Fatalf("send failure: %v", err)
			}
		} else if !errors.Is(err, failure) {
			t.Fatalf("receive failure: %v", err)
		}
		gateway, err := store.GetGateway(context.Background(), hello.GatewayId)
		if err != nil || gateway.AppliedRevision != 0 {
			t.Fatalf("transport failure marked delivery applied: %#v %v", gateway, err)
		}
	}
}
