package api

import (
	"context"
	"golang.org/x/time/rate"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNotificationSettingsBrowser(t *testing.T) {
	if os.Getenv("NSC_NOTIFICATION_BROWSER") != "1" {
		t.Skip("run with NSC_NOTIFICATION_BROWSER=1 and an installed browser")
	}
	s, h := newTestServer(t)
	s.limiter = rate.NewLimiter(1000, 1000)
	server := httptest.NewServer(h)
	defer server.Close()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config=playwright.notifications.config.ts")
	command.Dir = filepath.Join(root, "frontend")
	command.Env = append(os.Environ(), "E2E_BASE_URL="+server.URL, "NSC_NOTIFICATION_REPORT="+filepath.Join(root, "coverage/notifications/browser"))
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
