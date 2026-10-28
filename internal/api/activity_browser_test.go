package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/storage"
	"golang.org/x/time/rate"
)

func TestActivityWorkflowBrowser(t *testing.T) {
	if os.Getenv("NSC_ACTIVITY_BROWSER") != "1" {
		t.Skip("run with NSC_ACTIVITY_BROWSER=1 and an installed browser")
	}
	s, handler, runtime, _ := managedRuntimeFixture(t)
	s.limiter = rate.NewLimiter(1000, 1000)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	project, err := s.core.CreateProject(ctx, "Workflow", "workflow")
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	scopes := []core.AlertScope{{ProjectID: project.ID, EnvironmentID: env.ID}}
	issue := core.Issue{Code: "runtime_unavailable", ResourceType: "runtime", ResourceID: runtime.ID, Severity: "critical", Active: true, Occurrences: 2, ObservationState: "UNKNOWN", Scopes: scopes, FirstSeenAt: now, LastSeenAt: now}
	delivery := core.Notification{Status: "DELIVERED", ReceiverID: "receiver_fixture", DeliveredAt: &now, Event: core.NotificationEvent{ID: "event_fixture", Type: "runtime.problem", ResourceID: runtime.ID, IssueID: "issue_browser", Occurrence: 2, Severity: "critical", OccurredAt: now, Scopes: scopes}}
	for _, row := range []struct {
		id, kind string
		value    any
	}{{"issue_browser", "issue", issue}, {"notification_browser", "notification-delivery", delivery}} {
		raw, err := json.Marshal(row.value)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: row.id, Kind: row.kind, Payload: raw}}}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", "node_modules/@playwright/test/cli.js", "test", "--config=playwright.activity.config.ts")
	command.Dir = filepath.Join(root, "frontend")
	command.Env = append(os.Environ(), "E2E_BASE_URL="+server.URL, "NSC_ACTIVITY_ENVIRONMENT_PATH=/projects/"+project.ID+"/environments/"+env.ID+"/overview", "NSC_ACTIVITY_PATH=/operations/activity?environment_id="+env.ID, "NSC_ACTIVITY_REPORT="+filepath.Join(root, "coverage/alerts-ha/p05-browser"))
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
