package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestOperationHTTPUsesSameBusinessActionForAcceptanceDetailAndCollections(t *testing.T) {
	s, h, rt, _ := managedRuntimeFixture(t)
	ctx := t.Context()
	app, op, _, err := s.core.InstallApplication(ctx, core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: example.test/web:fixed\n"}, Name: "Actions", ProjectName: "actions", IdempotencyKey: "actions"})
	if err != nil {
		t.Fatal(err)
	}
	services, err := s.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil || len(services) != 1 {
		t.Fatal(services, err)
	}
	count := 2
	for _, test := range []struct {
		source        model.DeploymentSource
		service, want string
	}{
		{source: model.DeploymentSource{ServiceScope: &model.ServiceDeploymentScope{Mode: "delete"}}, service: "web", want: "DELETE_SERVICE"},
		{source: model.DeploymentSource{ServiceScope: &model.ServiceDeploymentScope{Mode: "update", RequestedReplicas: &count}}, service: "web", want: "SCALE"},
		{source: model.DeploymentSource{RestoredFrom: "historical", ServiceScope: &model.ServiceDeploymentScope{Mode: "update", RequestedReplicas: &count}}, service: "web", want: "RESTORE_SERVICE"},
		{source: model.DeploymentSource{RestoredFrom: "historical"}, want: "RESTORE_APPLICATION"},
		{source: model.DeploymentSource{ServiceScope: &model.ServiceDeploymentScope{Mode: "create"}}, want: "CREATE_SERVICES"},
		{source: model.DeploymentSource{ServiceScope: &model.ServiceDeploymentScope{Mode: "update"}}, want: "UPDATE_SERVICES"},
		{service: "web", want: "UPGRADE"},
	} {
		t.Run(test.want, func(t *testing.T) {
			raw, err := json.Marshal(test.source)
			if err != nil {
				t.Fatal(err)
			}
			sql := s.store.(*storage.SQLStore)
			if _, err = sql.DB().ExecContext(ctx, "UPDATE application_revisions SET configuration_source=? WHERE id=?", string(raw), op.RevisionID); err != nil {
				t.Fatal(err)
			}
			// Seed accepted immutable operation metadata; no runtime execution is
			// implied by these HTTP presentation fixtures.
			serviceID := ""
			if test.service != "" {
				serviceID = services[0].ID
			}
			if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET operation_type='UPGRADE',service_id=? WHERE id=?", serviceID, op.ID); err != nil {
				t.Fatal(err)
			}
			before, err := s.store.GetDeploymentOperation(ctx, op.ID)
			if err != nil {
				t.Fatal(err)
			}
			detail := responseData[model.DeploymentOperation](t, managementRequest(t, h, http.MethodGet, "/operations/"+op.ID, nil, nil), 200)
			if detail.BusinessAction != test.want || detail.Type != "UPGRADE" {
				t.Fatal(detail)
			}
			for _, path := range []string{"/operations?application_id=" + app.ID + "&limit=1", "/applications/" + app.ID + "/operations?limit=1"} {
				values := responseData[[]model.DeploymentOperation](t, managementRequest(t, h, http.MethodGet, path, nil, nil), 200)
				if len(values) != 1 || values[0].BusinessAction != detail.BusinessAction || values[0].Type != detail.Type {
					t.Fatal(path, values)
				}
			}
			accepted := httptest.NewRecorder()
			s.controlResponse(accepted, httptest.NewRequest(http.MethodPost, "/api/v1/fixture", nil), before, before.Revision, nil, 202)
			value := responseData[model.DeploymentOperation](t, accepted, 202)
			if value.BusinessAction != detail.BusinessAction {
				t.Fatal(value, detail)
			}
			after, err := s.store.GetDeploymentOperation(ctx, op.ID)
			if err != nil || !reflect.DeepEqual(before, after) || after.BusinessAction != "" {
				t.Fatal("presentation persisted on operation history", err)
			}
		})
	}
}

func TestOperationAcceptedResponsesCoverApplicationAndBindingActions(t *testing.T) {
	s, h, rt, _ := managedRuntimeFixture(t)
	ctx := t.Context()
	app, install, _, err := s.core.InstallApplication(ctx, core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: example.test/web:fixed\n    expose: [80]\n"}, Name: "Accepted actions", ProjectName: "accepted-actions", IdempotencyKey: "accepted-actions"})
	if err != nil {
		t.Fatal(err)
	}
	sql := s.store.(*storage.SQLStore)
	if err = sql.ActivateApplicationRevision(ctx, rt.ID, app.ID, install.RevisionID, install.RevisionID, []string{app.ProjectName + "/web"}, nil, "", app.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", install.ID); err != nil {
		t.Fatal(err)
	}
	queued := responseData[model.DeploymentOperation](t, managementRequest(t, h, "POST", "/applications/"+app.ID+"/operations", map[string]string{"action": "REDEPLOY"}, map[string]string{"Idempotency-Key": "accepted-redeploy"}), 202)
	if queued.BusinessAction != "REDEPLOY" || queued.Type != "REDEPLOY" {
		t.Fatal(queued)
	}
	if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE id=?", queued.ID); err != nil {
		t.Fatal(err)
	}
	services, err := s.store.ListAllServicesFiltered(ctx, storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil || len(services) != 1 {
		t.Fatal(services, err)
	}
	response := managementRequest(t, h, "POST", "/services/"+services[0].ID+"/domain-bindings", map[string]any{"domain": map[string]any{"host": "accepted.example.test", "entrypoints": []string{"http"}}, "route": map[string]any{"path": "/", "backend": map[string]any{"port": 80, "protocol": "http"}}}, map[string]string{"Idempotency-Key": "accepted-binding"})
	bound := responseData[struct {
		Binding   model.DomainBinding       `json:"binding"`
		Operation model.DeploymentOperation `json:"operation"`
	}](t, response, 202)
	if bound.Operation.BusinessAction != "DOMAIN_ATTACH" {
		t.Fatal(bound)
	}
	detach := responseData[model.DeploymentOperation](t, managementRequest(t, h, "DELETE", "/services/"+services[0].ID+"/domain-bindings/"+bound.Binding.ID, nil, map[string]string{"If-Match": `"rev-1"`, "Idempotency-Key": "accepted-detach"}), 202)
	if detach.BusinessAction != "DOMAIN_DETACH" {
		t.Fatal(detach)
	}
	app, err = s.store.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sql.DB().ExecContext(ctx, "UPDATE deployment_operations SET status='SUCCEEDED' WHERE application_id=?", app.ID); err != nil {
		t.Fatal(err)
	}
	updated := responseData[struct {
		Operation model.DeploymentOperation `json:"operation"`
	}](t, managementRequest(t, h, "POST", "/applications/"+app.ID+"/revisions", map[string]string{"compose_yaml": "services:\n  web:\n    image: example.test/web:updated\n    expose: [80]\n"}, map[string]string{"If-Match": `"rev-` + strconv.FormatInt(app.Revision, 10) + `"`, "Idempotency-Key": "accepted-upgrade"}), 202)
	if updated.Operation.BusinessAction != "UPGRADE" || updated.Operation.Type != "UPGRADE" {
		t.Fatal(updated)
	}
}

type operationRevisionUnavailable struct {
	storage.Store
	foreign bool
}

func (s operationRevisionUnavailable) GetApplicationRevision(ctx context.Context, key string) (model.ApplicationRevision, error) {
	if !s.foreign {
		return model.ApplicationRevision{}, errors.New("private revision diagnostic")
	}
	return model.ApplicationRevision{ID: key, ApplicationID: "foreign", ConfigurationSource: model.DeploymentSource{RestoredFrom: "foreign-private-source"}}, nil
}
func TestOperationPresentationFallsBackWithoutExposingUnavailableOrForeignSource(t *testing.T) {
	s, _, _, _ := managedRuntimeFixture(t)
	op := model.DeploymentOperation{ID: "op", Type: "UPGRADE", ApplicationID: "app", ServiceID: "service", RevisionID: "historical"}
	original := s.store
	for _, foreign := range []bool{false, true} {
		s.store = operationRevisionUnavailable{Store: original, foreign: foreign}
		if got := s.operationResponse(t.Context(), op); got.BusinessAction != "UPGRADE" || got.Type != op.Type {
			t.Fatal(got)
		}
	}
	s.store = original
	op.RevisionID = ""
	if got := s.operationResponse(t.Context(), op); got.BusinessAction != "UPGRADE" {
		t.Fatal(got)
	}
	var absent *model.DeploymentOperation
	for _, v := range []any{op, &op, absent, map[string]any{"operation": op, "nested": map[string]any{"operation": &op}, "label": "unchanged"}, map[string]any{}, "unchanged"} {
		result := s.operationResponseValue(t.Context(), v)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		switch v.(type) {
		case model.DeploymentOperation:
			if result.(model.DeploymentOperation).BusinessAction != "UPGRADE" {
				t.Fatal(result)
			}
		case *model.DeploymentOperation:
			if v == absent {
				if string(encoded) != "null" {
					t.Fatal(string(encoded))
				}
			} else if result.(model.DeploymentOperation).BusinessAction != "UPGRADE" {
				t.Fatal(result)
			}
		case map[string]any:
			before, _ := json.Marshal(v)
			if strings.Contains(string(before), "business_action") {
				t.Fatal("response mutated input map")
			}
			if strings.Contains(string(before), "operation") && !strings.Contains(string(encoded), `"business_action":"UPGRADE"`) {
				t.Fatal(string(encoded))
			}
			if strings.Contains(string(before), "label") && !strings.Contains(string(encoded), `"label":"unchanged"`) {
				t.Fatal(string(encoded))
			}
		case string:
			if result != v {
				t.Fatal(result)
			}
		}
	}
	if values := s.operationResponses(t.Context(), nil); len(values) != 0 {
		t.Fatal(values)
	}
}
