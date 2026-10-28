package api

import (
	"context"
	"encoding/json"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"golang.org/x/time/rate"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImageUpdatesBrowser(t *testing.T) {
	if os.Getenv("NSC_PRACTICAL_IMAGE_BROWSER") != "1" {
		t.Skip("run with NSC_PRACTICAL_IMAGE_BROWSER=1 and an installed browser")
	}
	s, handler, runtime, _ := managedRuntimeFixture(t)
	s.limiter = rate.NewLimiter(1000, 1000)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	project, err := s.core.CreateProject(ctx, "Image browser", "image-browser")
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.core.DeployEnvironment(ctx, env.ID, "services:\n  web:\n    image: registry.example.test/web@sha256:"+strings.Repeat("a", 64)+"\n", "fixture", "fixture", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.store.GetApplication(ctx, op.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	// Seed confirmed executor evidence in this isolated SQL fixture. No worker or Docker deployment runs.
	app.ActiveRevisionID, app.DesiredRevisionID, app.Status = op.RevisionID, op.RevisionID, model.ApplicationRunning
	if err := s.store.UpdateApplication(ctx, &app, app.Revision); err != nil {
		t.Fatal(err)
	}
	revision, err := s.store.GetApplicationRevision(ctx, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	revision.ConfigurationSource.TaggedImages = map[string]string{"web": "registry.example.test/web:stable"}
	raw, err := json.Marshal(revision.ConfigurationSource)
	if err != nil {
		t.Fatal(err)
	}
	sql := s.store.(*storage.SQLStore).DB()
	if _, err := sql.ExecContext(ctx, `UPDATE application_revisions SET configuration_source=? WHERE id=?`, string(raw), revision.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sql.ExecContext(ctx, `UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?`, op.ID); err != nil {
		t.Fatal(err)
	}
	candidate := core.ImageUpdate{EnvironmentID: env.ID, BaseRevisionID: revision.ID, Status: "AVAILABLE", Images: map[string]string{"web": "registry.example.test/web@sha256:" + strings.Repeat("b", 64)}, Changed: []string{"web"}}
	raw, err = json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: "candidate-browser", Kind: "image-update", ParentID: env.ID, Payload: raw}}}); err != nil {
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
	command.Env = append(os.Environ(), "E2E_BASE_URL="+server.URL, "NSC_PRACTICAL_ENVIRONMENT_PATH=/projects/"+project.ID+"/environments/"+env.ID+"/overview", "NSC_PRACTICAL_REPORT="+filepath.Join(root, "coverage/practical/p0-b/browser"))
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
