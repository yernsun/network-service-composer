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
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sandboxLifecycleStore struct {
	storage.Store
	stage      string
	failure    error
	heartbeats int
	lists      int
}

func (s *sandboxLifecycleStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if strings.HasSuffix(key, ":sandbox-status") {
		s.heartbeats++
		if s.stage == "heartbeat" && s.heartbeats == 2 {
			return storage.ControlDocument{}, s.failure
		}
	}
	if s.stage == "identity" && strings.HasSuffix(key, ":role") || s.stage == "task" && key == "task" || s.stage == "validation" && strings.HasSuffix(key, ":nginx-validation-head") {
		return storage.ControlDocument{}, s.failure
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s *sandboxLifecycleStore) GetPool(ctx context.Context, key string) (model.GatewayPool, error) {
	if s.stage == "pool" {
		return model.GatewayPool{}, s.failure
	}
	p, err := s.Store.GetPool(ctx, key)
	if s.stage == "disabled" {
		p.Enabled = false
	}
	return p, err
}
func (s *sandboxLifecycleStore) ListControlDocuments(ctx context.Context, kind, parent string, page storage.Page) ([]storage.ControlDocument, error) {
	s.lists++
	if s.stage == "list" || s.stage == "validation" && s.lists == 2 {
		return nil, s.failure
	}
	return s.Store.ListControlDocuments(ctx, kind, parent, page)
}

func TestSandboxLifecycleStopsAfterRevocationAndStorageFailure(t *testing.T) {
	for _, stage := range []string{"identity", "revoked", "pool", "disabled", "list", "task", "validation", "heartbeat", "cancel after idle"} {
		t.Run(stage, func(t *testing.T) {
			server, db, stream, hello := gatewayHandshakeFixture(t)
			ctx, cancel := context.WithTimeout(stream.ctx, 1500*time.Millisecond)
			defer cancel()
			stream.ctx, stream.block = ctx, true
			mode := "sandbox"
			if stage == "revoked" {
				mode = "production"
			}
			gatewayControlFixture(t, db, hello.GatewayId+":role", "gateway-role", hello.PoolId, map[string]string{"mode": mode})
			if stage == "task" {
				gatewayControlFixture(t, db, "task", "nginx-validation", hello.PoolId, core.NginxValidation{})
			}
			if stage == "heartbeat" {
				stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{}}}}
				stream.block = false
			}
			failure := errors.New("sandbox state unavailable")
			wrapped := &sandboxLifecycleStore{Store: db, stage: stage, failure: failure}
			server.store, server.core = wrapped, core.New(wrapped, config.Defaults(), nil)
			err := server.connectSandbox(stream, hello.GatewayId, hello.PoolId)
			switch stage {
			case "identity", "revoked", "pool", "disabled":
				if status.Code(err) != codes.PermissionDenied {
					t.Fatalf("revoked identity retained access: %v", err)
				}
			case "cancel after idle":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			default:
				if !errors.Is(err, failure) {
					t.Fatalf("lost storage failure: %v", err)
				}
			}
			if len(stream.output) != 0 {
				t.Fatal("sandbox received work after failed authorization or state read")
			}
			if _, err := db.GetGateway(context.Background(), hello.GatewayId); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("sandbox registered production gateway")
			}
		})
	}
}
