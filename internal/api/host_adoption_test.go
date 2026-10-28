package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestHostRestrictedAdoptionHTTPAuthorizationAndOwnership(t *testing.T) {
	s, h, runtime, _ := managedRuntimeFixture(t)
	ctx := context.Background()
	runtime.Endpoint = "node://node-one"
	if err := s.store.UpdateRuntime(ctx, &runtime, runtime.Revision); err != nil {
		t.Fatal(err)
	}
	s.store.(*storage.SQLStore).ConfigureOperationNodes(map[string]model.RuntimeExecutionIdentity{
		runtime.Endpoint: {NodeID: "node-one", EngineID: "engine-one"},
	})
	now := time.Now().UTC()
	existing := model.Service{ID: "svc-host-fixture", RuntimeID: runtime.ID, ServiceKey: "existing/web", RuntimeObjectID: "container-preserved", ExternalID: "container-preserved", Name: "web", Namespace: "existing", DiscoveredProtocol: "http", Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.store.UpsertService(ctx, &existing); err != nil {
		t.Fatal(err)
	}
	source := "services:\n  web:\n    image: nginx:alpine\n    volumes: ['/srv/app/data:/data:ro']\n    deploy:\n      placement:\n        constraints: ['node.id == node-one']\n"
	input := core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: source}, Name: "Existing", ProjectName: "existing", Adopt: true}
	viewer, err := s.core.SaveUser(ctx, "", core.UserInput{Username: "host-viewer", Role: "viewer", Enabled: true, Password: "isolated-viewer-password"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(ctx, viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if w := managementRequest(t, h, http.MethodPost, "/applications", input, map[string]string{"Authorization": "Bearer " + token}); w.Code != 403 {
		t.Fatal("viewer acquired host workload", w.Code)
	}
	bad := input
	bad.ComposeYAML = strings.ReplaceAll(source, "node-one", "node-two")
	if w := managementRequest(t, h, http.MethodPost, "/applications", bad, nil); w.Code != 400 {
		t.Fatal("foreign host accepted", w.Code)
	}
	rows, err := s.store.ListApplications(ctx, runtime.ID, storage.Page{Limit: 100})
	if err != nil || len(rows) != 0 {
		t.Fatal("rejected request left ownership behind", err)
	}
	var accepted struct {
		Application model.Application         `json:"application"`
		Operation   model.DeploymentOperation `json:"operation"`
	}
	headers := map[string]string{"Idempotency-Key": "host-adoption"}
	accepted = responseData[struct {
		Application model.Application         `json:"application"`
		Operation   model.DeploymentOperation `json:"operation"`
	}](t, managementRequest(t, h, http.MethodPost, "/applications", input, headers), 202)
	if accepted.Operation.Type != "ADOPT" {
		t.Fatal("adoption became an installation")
	}
	replay := responseData[struct {
		Application model.Application         `json:"application"`
		Operation   model.DeploymentOperation `json:"operation"`
	}](t, managementRequest(t, h, http.MethodPost, "/applications", input, headers), 202)
	if replay.Operation.ID != accepted.Operation.ID || replay.Application.ID != accepted.Application.ID {
		t.Fatal("duplicate adoption changed ownership")
	}
	stored, err := s.store.GetService(ctx, existing.ID)
	if err != nil || stored.RuntimeObjectID != existing.RuntimeObjectID || stored.Managed || stored.ApplicationID != "" {
		t.Fatal("adoption replaced original service", err)
	}
	conflict := input
	conflict.Name = "Competing owner"
	if w := managementRequest(t, h, http.MethodPost, "/applications", conflict, map[string]string{"Idempotency-Key": "competing-adoption"}); w.Code < 400 {
		t.Fatal("second owner adopted claimed workload")
	}
}
