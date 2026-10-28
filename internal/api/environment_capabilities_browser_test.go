package api

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"golang.org/x/time/rate"
)

func TestEnvironmentCapabilitiesBrowser(t *testing.T) {
	if os.Getenv("NSC_PRACTICAL_BROWSER") != "1" {
		t.Skip("run with NSC_PRACTICAL_BROWSER=1 and an installed browser")
	}
	s, handler, runtime, _ := managedRuntimeFixture(t)
	s.limiter = rate.NewLimiter(1000, 1000)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	project, err := s.core.CreateProject(ctx, "Capabilities", "capabilities")
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", DisplayName: "production-" + strings.Repeat("long", 12), RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config=playwright.practical.config.ts")
	command.Dir = filepath.Join(root, "frontend")
	command.Env = append(os.Environ(), "E2E_BASE_URL="+server.URL, "NSC_PRACTICAL_ENVIRONMENT_PATH=/projects/"+project.ID+"/environments/"+env.ID+"/overview", "NSC_PRACTICAL_REPORT="+filepath.Join(root, "coverage/practical/p0-a/browser"))
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
