package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) sendValidation(stream gatewayv2.GatewayControl_ConnectServer, gateway, pool string) error {
	var v *core.NginxValidation
	var snapshot *model.Snapshot
	for attempt := 0; attempt < 8; attempt++ {
		var e error
		v, snapshot, e = s.core.NextNginxValidation(stream.Context(), pool, gateway)
		if errors.Is(e, storage.ErrPrecondition) {
			continue
		}
		if e != nil {
			return e
		}
		break
	}
	if v == nil {
		return nil
	}
	checks, _ := json.Marshal(v.Checks)
	return stream.Send(&gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_ValidationRequest{ValidationRequest: &gatewayv2.ValidationRequest{TaskId: v.ID, Snapshot: SnapshotToProto(*snapshot), ActivateSandbox: v.SandboxID == gateway, ChecksJson: string(checks), HoldUntilUnix: v.ExpiresAt.Unix()}}})
}
func (s *Server) finishValidation(ctx context.Context, pool, gateway string, result *gatewayv2.ValidationResult) error {
	state := "FAILED"
	if result.Success {
		state = "SUCCEEDED"
	}
	var checks json.RawMessage
	if json.Valid([]byte(result.ChecksJson)) {
		checks = json.RawMessage(result.ChecksJson)
	}
	message := result.Error
	if len(message) > 8192 {
		message = message[:8192]
	}
	return s.core.FinishNginxValidation(ctx, pool, result.TaskId, gateway, core.NginxValidationResult{Status: state, Error: message, Checks: checks, RenderedDigest: result.RenderedDigest})
}
func (s *Server) connectSandbox(stream gatewayv2.GatewayControl_ConnectServer, gateway, pool string) error {
	ctx := stream.Context()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	if e := s.core.SandboxHeartbeat(ctx, gateway, pool); e != nil {
		return e
	}
	type incoming struct {
		message *gatewayv2.AgentMessage
		err     error
	}
	messages := make(chan incoming, 1)
	go func() {
		for {
			m, e := stream.Recv()
			select {
			case messages <- incoming{m, e}:
			case <-ctx.Done():
				return
			}
			if e != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case value := <-messages:
			if value.err != nil {
				return value.err
			}
			if value.message.GetHeartbeat() != nil {
				if e := s.core.SandboxHeartbeat(ctx, gateway, pool); e != nil {
					return e
				}
			} else if r := value.message.GetValidationResult(); r != nil {
				_ = s.finishValidation(ctx, pool, gateway, r)
			} else {
				return status.Error(codes.PermissionDenied, "sandbox cannot submit production state")
			}
		case <-ticker.C:
			mode, e := s.core.GatewayMode(ctx, gateway, pool)
			if e != nil || mode != "sandbox" {
				return status.Error(codes.PermissionDenied, "sandbox identity revoked")
			}
			p, e := s.store.GetPool(ctx, pool)
			if e != nil || !p.Enabled {
				return status.Error(codes.PermissionDenied, "pool disabled")
			}
			if e = s.sendSandboxCancellation(stream, gateway, pool); e != nil {
				return e
			}
			if e = s.sendValidation(stream, gateway, pool); e != nil {
				return e
			}
		}
	}
}

func (s *Server) sendSandboxCancellation(stream gatewayv2.GatewayControl_ConnectServer, gateway, pool string) error {
	docs, e := s.store.ListControlDocuments(stream.Context(), "nginx-validation", pool, storage.Page{Limit: 200})
	if e != nil {
		return e
	}
	for _, d := range docs {
		v, e := s.core.GetNginxValidation(stream.Context(), pool, d.ID)
		if e != nil {
			return e
		}
		if v.SandboxID == gateway && (v.Status == "CANCELLED" || v.Status == "EXPIRED" || v.Status == "FAILED") {
			if e = stream.Send(&gatewayv2.ControllerMessage{Payload: &gatewayv2.ControllerMessage_ValidationRequest{ValidationRequest: &gatewayv2.ValidationRequest{TaskId: v.ID, Cancel: true}}}); e != nil {
				return e
			}
		}
	}
	return nil
}
