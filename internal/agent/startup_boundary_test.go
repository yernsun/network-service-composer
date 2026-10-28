package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
)

func TestStartupBarrierPreservesGenerationOnCanceledApplyAndValidation(t *testing.T) {
	for _, pid := range []string{"", "1"} {
		t.Run("pid="+pid, func(t *testing.T) {
			m := validationManagerFixture(t, "trap 'exit 0' QUIT; while :; do sleep 0.01; done")
			before, err := os.Readlink(filepath.Join(m.root, "current"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(m.root, "nginx.pid"), []byte(pid), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = m.Stop(ctx)
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result, err := m.Apply(ctx, validationSnapshot(), strings.Repeat("a", 64))
			if !errors.Is(err, context.Canceled) || result.FailedPhase != "startup" {
				t.Fatalf("unready process accepted application: %+v, %v", result, err)
			}
			validation := m.Validate(ctx, &gatewayv2.ValidationRequest{TaskId: "startup", Snapshot: validationSnapshot(), ActivateSandbox: true}, true)
			if validation.Success || validation.Error == "" {
				t.Fatal("unready process accepted sandbox activation")
			}
			after, err := os.Readlink(filepath.Join(m.root, "current"))
			if err != nil || after != before || m.State().ConfigRevision != 0 {
				t.Fatal("startup cancellation replaced the last known good generation")
			}
		})
	}
}
