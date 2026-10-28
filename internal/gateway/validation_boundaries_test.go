package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func gatewayControlFixture(t *testing.T, s storage.Store, key, kind, parent string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.WriteControlDocuments(context.Background(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: key, Kind: kind, ParentID: parent, Payload: payload}}}); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayValidationTransportPreservesResultsAndBounds(t *testing.T) {
	for _, success := range []bool{false, true} {
		for _, checks := range []string{`{"checks":[]}`, "invalid"} {
			server, db, stream, hello := gatewayHandshakeFixture(t)
			ctx := context.Background()
			cfg := config.Defaults()
			cfg.Management.Enabled = true
			server.core = core.New(db, cfg, nil)
			now := time.Now().UTC()
			g := model.Gateway{ID: hello.GatewayId, PoolID: hello.PoolId, Enabled: true, RendererVersion: nginxx.RendererVersion, LastSeenAt: &now, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if err := db.UpsertGateway(ctx, &g); err != nil {
				t.Fatal(err)
			}
			draft, err := server.core.SaveNginxDraft(ctx, g.PoolID, core.NginxConfiguration{Snippets: []model.ConfigSnippet{{Scope: "http", Content: "gzip on;"}}}, "owner", 0)
			if err != nil {
				t.Fatal(err)
			}
			validation, err := server.core.CreateNginxValidation(ctx, g.PoolID, draft.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if err = server.sendValidation(stream, g.ID, g.PoolID); err != nil {
				t.Fatal(err)
			}
			if len(stream.output) != 1 || stream.output[0].GetValidationRequest().TaskId != validation.ID || stream.output[0].GetValidationRequest().ActivateSandbox {
				t.Fatal("validation sent to wrong mode or task")
			}
			if err = server.finishValidation(ctx, g.PoolID, g.ID, &gatewayv2.ValidationResult{TaskId: validation.ID, Success: success, ChecksJson: checks, Error: strings.Repeat("x", 9000), RenderedDigest: "rendered"}); err != nil {
				t.Fatal(err)
			}
			actual, err := server.core.GetNginxValidation(ctx, g.PoolID, validation.ID)
			if err != nil {
				t.Fatal(err)
			}
			result := actual.Results[g.ID]
			want := "FAILED"
			if success {
				want = "SUCCEEDED"
			}
			if result.Status != want || len(result.Error) != 8192 || result.RenderedDigest != "rendered" {
				t.Fatalf("validation facts lost: %+v", result)
			}
			if json.Valid([]byte(checks)) {
				if string(result.Checks) != checks {
					t.Fatal("valid check evidence lost")
				}
			} else if len(result.Checks) != 0 {
				t.Fatal("invalid JSON accepted as checks")
			}
		}
	}
}

func TestSandboxCancellationTargetsOnlyExpiredOrFailedTasks(t *testing.T) {
	for _, name := range []string{"cancelled", "expired", "failed", "running", "success", "other sandbox", "send failure"} {
		t.Run(name, func(t *testing.T) {
			server, db, stream, hello := gatewayHandshakeFixture(t)
			v := core.NginxValidation{PoolID: hello.PoolId, SandboxID: hello.GatewayId, BaseRevision: 2, ExpiresAt: time.Now().Add(time.Hour), Results: map[string]core.NginxValidationResult{hello.GatewayId: {Status: "SUCCEEDED"}}}
			switch name {
			case "cancelled", "send failure":
				v.Status = "CANCELLED"
			case "expired":
				v.ExpiresAt = time.Now().Add(-time.Minute)
			case "failed":
				v.Results[hello.GatewayId] = core.NginxValidationResult{Status: "FAILED"}
			case "running":
				v.Results[hello.GatewayId] = core.NginxValidationResult{Status: "RUNNING"}
			case "other sandbox":
				v.SandboxID = "other"
				v.Status = "CANCELLED"
			}
			gatewayControlFixture(t, db, "validation", "nginx-validation", hello.PoolId, v)
			gatewayControlFixture(t, db, hello.PoolId+":nginx-validation-head", "nginx-validation-head", hello.PoolId, map[string]string{"task_id": "validation"})
			failure := errors.New("sandbox disconnected")
			if name == "send failure" {
				stream.sendError = failure
			}
			err := server.sendSandboxCancellation(stream, hello.GatewayId, hello.PoolId)
			if name == "send failure" {
				if !errors.Is(err, failure) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := name == "cancelled" || name == "expired" || name == "failed"
			if (len(stream.output) == 1) != want {
				t.Fatalf("cancellation count=%d want=%t", len(stream.output), want)
			}
			if want {
				r := stream.output[0].GetValidationRequest()
				if r.TaskId != "validation" || !r.Cancel {
					t.Fatal("cancellation lost task identity")
				}
			}
		})
	}
}

func TestSandboxConnectionRejectsProductionStateAndRetainsHeartbeat(t *testing.T) {
	for _, name := range []string{"heartbeat", "validation result", "production ACK", "empty message", "receive failure", "cancellation"} {
		t.Run(name, func(t *testing.T) {
			server, db, stream, hello := gatewayHandshakeFixture(t)
			var message *gatewayv2.AgentMessage
			switch name {
			case "heartbeat":
				message = &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Heartbeat{Heartbeat: &gatewayv2.AgentHeartbeat{}}}
			case "validation result":
				message = &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_ValidationResult{ValidationResult: &gatewayv2.ValidationResult{TaskId: "missing", Success: true}}}
			case "production ACK":
				message = &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{}}}
			case "empty message":
				message = &gatewayv2.AgentMessage{}
			case "receive failure":
				stream.receiveError = errors.New("connection ended")
			case "cancellation":
				ctx, cancel := context.WithCancel(stream.ctx)
				cancel()
				stream.ctx = ctx
				stream.block = true
			}
			if message != nil {
				stream.input = []*gatewayv2.AgentMessage{message}
			}
			err := server.connectSandbox(stream, hello.GatewayId, hello.PoolId)
			switch name {
			case "heartbeat", "validation result":
				if !errors.Is(err, io.EOF) {
					t.Fatal(err)
				}
			case "production ACK", "empty message":
				if status.Code(err) != codes.PermissionDenied {
					t.Fatal("sandbox submitted production state", err)
				}
			case "receive failure":
				if !errors.Is(err, stream.receiveError) {
					t.Fatal(err)
				}
			case "cancellation":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if name != "cancellation" {
				d, err := db.GetControlDocument(context.Background(), hello.GatewayId+":sandbox-status")
				if err != nil || d.ParentID != hello.PoolId {
					t.Fatal("sandbox heartbeat lost", err)
				}
			}
			if _, err := db.GetGateway(context.Background(), hello.GatewayId); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("sandbox entered production inventory")
			}
		})
	}
}
