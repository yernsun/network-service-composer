package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

func TestSandboxWithRealNGINX(t *testing.T) {
	binary := os.Getenv("NSC_TEST_NGINX_BINARY")
	if binary == "" {
		t.Skip("set NSC_TEST_NGINX_BINARY for isolated NGINX integration")
	}
	root := t.TempDir()
	m := NewGenerationManager(config.NGINXConfig{Binary: binary, GenerationDir: root, WorkerProcesses: "1", KeepGenerations: 3})
	if _, e := m.Prepare(); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := m.Stop(ctx); e != nil {
			t.Error(e)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	request := &gatewayv2.ValidationRequest{TaskId: "sandbox-test", Snapshot: &gatewayv2.Snapshot{PoolId: "pool_test", ConfigRevision: 1, ChallengeUpstream: "http://127.0.0.1:1"}, ActivateSandbox: true, ChecksJson: "[]"}
	if r := m.Validate(ctx, request, false); r.Success {
		t.Fatal("production accepted sandbox activation")
	}
	r := m.ValidateSandbox(ctx, request)
	if !r.Success {
		t.Fatalf("sandbox: %s; checks: %s", r.Error, r.ChecksJson)
	}
	if !strings.Contains(r.ChecksJson, `"restore_passed":true`) {
		t.Fatal(r.ChecksJson)
	}
	if m.State().ConfigRevision != 0 {
		t.Fatal("sandbox advanced production revision")
	}
	if e := waitForGenerationHealth(ctx, 0); e != nil {
		t.Fatal("sandbox did not restore baseline", e)
	}
	request.HoldUntilUnix = time.Now().Add(10 * time.Second).Unix()
	held := m.Validate(ctx, request, true)
	if !held.Success {
		t.Fatal(held.Error)
	}
	if e := waitForGenerationHealth(ctx, 1); e != nil {
		t.Fatal("held candidate not reachable", e)
	}
	if e := m.CancelValidation(ctx, request.TaskId); e != nil {
		t.Fatal(e)
	}
	if e := waitForGenerationHealth(ctx, 0); e != nil {
		t.Fatal("cancel did not restore baseline", e)
	}
}
