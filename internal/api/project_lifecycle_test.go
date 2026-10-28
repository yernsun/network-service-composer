package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestProjectLifecycleAPITransactionsETagsAndDurableDeletion(t *testing.T) {
	s, h, rt, _ := managedRuntimeFixture(t)
	input := map[string]any{"name": "Lifecycle <literal>", "slug": "lifecycle-api", "description": "persistent", "default_runtime_id": rt.ID, "initial_environment": map[string]any{"display_name": "Production", "kind": "production"}}
	headers := map[string]string{"Idempotency-Key": "atomic-create"}
	p := responseData[core.Project](t, managementRequest(t, h, "POST", "/projects", input, headers), 201)
	again := responseData[core.Project](t, managementRequest(t, h, "POST", "/projects", input, headers), 201)
	if p.ID != again.ID || p.InitialEnvironmentID == "" {
		t.Fatal(p, again)
	}
	match := func(revision int64) map[string]string {
		return map[string]string{"If-Match": fmt.Sprintf(`"rev-%d"`, revision)}
	}
	if got := managementRequest(t, h, "PATCH", "/projects/"+p.ID, map[string]string{"name": "Changed"}, nil); got.Code != 428 {
		t.Fatal(got.Code, got.Body)
	}
	p = responseData[core.Project](t, managementRequest(t, h, "PATCH", "/projects/"+p.ID, map[string]string{"name": "Changed", "description": "retained", "default_runtime_id": rt.ID}, match(p.Revision)), 200)
	env := responseData[core.Environment](t, managementRequest(t, h, "POST", "/projects/"+p.ID+"/environments", map[string]string{"name": "qa", "display_name": "QA", "kind": "testing"}, match(p.Revision)), 201)
	env = responseData[core.Environment](t, managementRequest(t, h, "PATCH", "/environments/"+env.ID, map[string]any{"display_name": "Quality", "kind": "development", "gateway_ids": []string{}}, match(env.Revision)), 200)
	if env.Name != "qa" || env.RuntimeID != rt.ID {
		t.Fatal(env)
	}
	if got := managementRequest(t, h, "DELETE", "/environments/"+env.ID, nil, match(env.Revision)); got.Code != 204 {
		t.Fatal(got.Code, got.Body)
	}
	preview := responseData[core.ProjectDeletionPreview](t, managementRequest(t, h, "POST", "/projects/"+p.ID+"/deletion-previews", map[string]bool{"force": false}, nil), 200)
	body := core.ProjectDeletionInput{PreviewID: preview.ID, ConfirmSlug: p.Slug}
	deletionHeaders := match(preview.ProjectRevision)
	deletionHeaders["Idempotency-Key"] = "delete-once"
	accepted := managementRequest(t, h, "DELETE", "/projects/"+p.ID, body, deletionHeaders)
	task := responseData[core.ProjectDeletion](t, accepted, 202)
	if accepted.Header().Get("Location") != "/api/v1/project-deletions/"+task.ID {
		t.Fatal(accepted.Header())
	}
	responseData[core.ProjectDeletion](t, managementRequest(t, h, "GET", "/project-deletions/"+task.ID, nil, nil), 200)
	if got := managementRequest(t, h, "POST", "/project-deletions/"+task.ID+"/resume", body, match(task.Revision)); got.Code != 409 {
		t.Fatal(got.Code, got.Body)
	}
	sqlStore := s.store.(*storage.SQLStore)
	worker := core.ProjectDeletionWorker{Core: s.core, Locker: lease.New(sqlStore.DB(), sqlStore.Dialect())}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := responseData[core.ProjectDeletion](t, managementRequest(t, h, "GET", "/project-deletions/"+task.ID, nil, nil), 200)
	if done.Status != "SUCCEEDED" {
		t.Fatal(done)
	}
	if got := managementRequest(t, h, "GET", "/projects/"+p.ID, nil, nil); got.Code != 404 {
		t.Fatal(got.Code, got.Body)
	}
	retry := responseData[core.ProjectDeletion](t, managementRequest(t, h, "DELETE", "/projects/"+p.ID, body, deletionHeaders), 202)
	if retry.ID != task.ID || retry.Status != "SUCCEEDED" {
		t.Fatal(retry)
	}
}

func TestProjectLifecycleBrowserRequiresAuthenticationAndCSRF(t *testing.T) {
	_, h, _, _ := managedRuntimeFixture(t)
	for _, entry := range []struct{ method, path string }{{"PATCH", "/projects/fixture"}, {"POST", "/projects/fixture/deletion-previews"}, {"DELETE", "/projects/fixture"}, {"POST", "/project-deletions/fixture/resume"}, {"PATCH", "/environments/fixture"}, {"DELETE", "/environments/fixture"}} {
		anonymous := browserRequest(h, entry.method, "/api/v1"+entry.path, `{}`, nil, "")
		if anonymous.Code != http.StatusUnauthorized {
			t.Fatal(entry, anonymous.Code, anonymous.Body)
		}
	}
	cookie, _ := browserLogin(t, h)
	for _, path := range []string{"/projects/fixture/deletion-previews", "/project-deletions/fixture/resume"} {
		missing := browserRequest(h, "POST", "/api/v1"+path, `{}`, cookie, "")
		if missing.Code != http.StatusForbidden {
			t.Fatal(path, missing.Code, missing.Body)
		}
	}
}
