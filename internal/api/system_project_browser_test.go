package api

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// This opt-in test runs the production HTTP handler and embedded frontend over
// real loopback HTTP with disposable SQL fixtures. It performs no Docker writes.
func TestSystemProjectBrowser(t *testing.T) {
	if os.Getenv("NSC_SYSTEM_BROWSER") != "1" {
		t.Skip("run with NSC_SYSTEM_BROWSER=1 and an installed browser")
	}
	for _, profile := range []string{"empty", "system"} {
		t.Run(profile, func(t *testing.T) {
			s, h := newTestServer(t)
			if profile == "system" {
				s, h, _, _ = systemApplicationsAPIFixture(t)
			}
			s.limiter = rate.NewLimiter(1000, 1000)
			server := httptest.NewServer(h)
			defer server.Close()
			s.config.Server.PublicURL = server.URL
			root, err := filepath.Abs("../..")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config=playwright.system.config.ts")
			command.Dir = filepath.Join(root, "frontend")
			command.Env = append(os.Environ(), "E2E_BASE_URL="+server.URL, "NSC_SYSTEM_PROFILE="+profile, "NSC_SYSTEM_REPORT="+filepath.Join(root, "coverage/system-applications/browser", profile))
			output, err := command.CombinedOutput()
			t.Log(string(output))
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
