package api

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"golang.org/x/time/rate"
)

func TestSelfSignedCertificateBrowser(t *testing.T) {
	if os.Getenv("NSC_SELF_SIGNED_BROWSER") != "1" {
		t.Skip("run with NSC_SELF_SIGNED_BROWSER=1 and an installed browser")
	}
	s, h := newTestServer(t)
	s.limiter = rate.NewLimiter(1000, 1000)
	if _, err := s.core.CreatePool(t.Context(), core.CreatePoolInput{Name: "Self-signed fixture"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config=playwright.self-signed.config.ts")
	command.Dir = filepath.Join(root, "frontend")
	command.Env = append(os.Environ(), "E2E_BASE_URL="+server.URL)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
