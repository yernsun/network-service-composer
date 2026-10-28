package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/composecheck"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestComposeValidationHTTPIsReadOnlyAndAuthenticated(t *testing.T) {
	s, h, rt, _ := managedRuntimeFixture(t)
	project := responseData[core.Project](t, managementRequest(t, h, "POST", "/projects", map[string]string{"name": "compose", "slug": "compose"}, nil), 201)
	env := responseData[core.Environment](t, managementRequest(t, h, "POST", "/projects/"+project.ID+"/environments", core.EnvironmentInput{Name: "dev", RuntimeID: rt.ID}, nil), 201)
	path := "/environments/" + env.ID + "/compose-validation"
	for _, tc := range []struct{ raw, code string }{
		{"services:\n  web:\n    image: nginx:1\n    volumes: [/data]\n", ""},
		{"services: [", "YAML_SYNTAX_ERROR"},
		{"services:\n  web:\n    image: [wrong]\n", "COMPOSE_SCHEMA_ERROR"},
	} {
		result := responseData[composecheck.Result](t, managementRequest(t, h, "POST", path, map[string]string{"compose_yaml": tc.raw}, nil), 200)
		if tc.code == "" {
			if !result.Deployable {
				t.Fatalf("standalone blocked: %+v", result)
			}
		} else if len(result.Diagnostics) == 0 || result.Diagnostics[0].Code != tc.code {
			t.Fatalf("wrong diagnostics: %+v", result)
		}
	}
	current, err := s.core.GetEnvironment(context.Background(), env.ID)
	if err != nil || current.Revision != env.Revision || current.ApplicationID != "" {
		t.Fatalf("analysis mutated environment: %+v %v", current, err)
	}
	if operations, err := s.store.ListDeploymentOperations(context.Background(), "", storage.Page{Limit: 20}); err != nil || len(operations) != 0 {
		t.Fatalf("analysis queued execution: %+v %v", operations, err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1"+path, strings.NewReader(`{"compose_yaml":"services: {}"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated analysis: %d %s", w.Code, w.Body.String())
	}
}

func TestComposePreviewMasksInheritedAndEditedVariablesWithoutPersistence(t *testing.T) {
	s, h, rt, _ := managedRuntimeFixture(t)
	ctx := context.Background()
	project := responseData[core.Project](t, managementRequest(t, h, "POST", "/projects", map[string]string{"name": "preview", "slug": "preview"}, nil), 201)
	env := responseData[core.Environment](t, managementRequest(t, h, "POST", "/projects/"+project.ID+"/environments", core.EnvironmentInput{Name: "dev", RuntimeID: rt.ID}, nil), 201)
	projectSecret, localSecret, ordinary, namedSecret := "LongSecret123", "short", "east", "NamedSecret789"
	_, err := s.core.SaveVariables(ctx, project.ID, core.VariableInput{Values: []core.Variable{{Key: "PRIVATE", Value: &projectSecret, Secret: true}, {Key: "REGION", Value: &ordinary}, {Key: "API_KEY", Value: &namedSecret}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.core.SaveVariables(ctx, env.ID, core.VariableInput{Values: []core.Variable{{Key: "SHORT", Value: &localSecret, Secret: true}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var reviewsBefore int
	if err = s.store.(*storage.SQLStore).DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM control_documents WHERE kind IN ('environment-review','service-review')").Scan(&reviewsBefore); err != nil {
		t.Fatal(err)
	}
	path := "/environments/" + env.ID + "/compose-preview"
	compose := "services:\n  web:\n    image: nginx:1\n    environment:\n      PRIVATE: ${PRIVATE}\n      API_KEY: ${API_KEY}\n      SHORT: ${SHORT}\n      REGION: ${REGION}\n"
	response := managementRequest(t, h, "POST", path, core.ComposeValidationInput{Compose: compose, RuntimeEnv: "REGION=west\n"}, nil)
	preview := responseData[core.ComposePreview](t, response, 200)
	if response.Header().Get("Cache-Control") != "no-store" || preview.RenderedYAML == nil || !preview.Validation.Deployable {
		t.Fatalf("preview unavailable: %s", response.Body)
	}
	for _, expected := range []string{"Lo**23", "Na**89", "SHORT:", "west"} {
		if !strings.Contains(*preview.RenderedYAML, expected) {
			t.Fatalf("missing %q: %s", expected, *preview.RenderedYAML)
		}
	}
	for _, secret := range []string{projectSecret, localSecret, namedSecret} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("raw secret in response: %s", response.Body)
		}
	}
	overridden := managementRequest(t, h, "POST", path, core.ComposeValidationInput{Compose: compose, RuntimeEnv: "REGION=west\nPRIVATE=Override1234\n"}, nil)
	overridePreview := responseData[core.ComposePreview](t, overridden, 200)
	if overridePreview.RenderedYAML == nil || !strings.Contains(*overridePreview.RenderedYAML, "Ov**34") || strings.Contains(overridden.Body.String(), "Override1234") {
		t.Fatalf("edited secret override leaked or was ignored: %s", overridden.Body)
	}
	for _, tc := range []struct{ raw, code string }{
		{"services: [", "YAML_SYNTAX_ERROR"},
		{"services:\n  web: {image: nginx:1}\n  web: {image: nginx:2}\n", "YAML_SYNTAX_ERROR"},
		{"services:\n  web:\n    image: [wrong]\n", "COMPOSE_SCHEMA_ERROR"},
		{"services:\n  web:\n    image: nginx:1\n    environment: [\"MISSING=${MISSING:?required}\"]\n", "COMPOSE_ENVIRONMENT_UNRESOLVED"},
	} {
		got := responseData[core.ComposePreview](t, managementRequest(t, h, "POST", path, core.ComposeValidationInput{Compose: tc.raw}, nil), 200)
		if got.RenderedYAML != nil || len(got.Validation.Diagnostics) == 0 || got.Validation.Diagnostics[0].Code != tc.code {
			t.Fatalf("unexpected diagnostic for %q: %+v", tc.raw, got)
		}
	}
	policy := responseData[core.ComposePreview](t, managementRequest(t, h, "POST", path, core.ComposeValidationInput{Compose: "services:\n  web:\n    image: nginx:1\n    privileged: true\n"}, nil), 200)
	if policy.Validation.Deployable || policy.RenderedYAML == nil || len(policy.Validation.Diagnostics) == 0 || policy.Validation.Diagnostics[len(policy.Validation.Diagnostics)-1].Code != "NSC_EXECUTION_POLICY" {
		t.Fatalf("policy diagnostic missing: %+v", policy)
	}
	viewer, err := s.core.SaveUser(ctx, "", core.UserInput{Username: "preview-reader", Role: "viewer", Enabled: true, Password: "reader-password-123456"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(ctx, viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if denied := managementRequest(t, h, "POST", path, core.ComposeValidationInput{Compose: compose}, map[string]string{"Authorization": "Bearer " + token}); denied.Code != 403 {
		t.Fatalf("viewer preview: %d %s", denied.Code, denied.Body)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1"+path, strings.NewReader(`{"compose_yaml":"services: {}"}`))
	request.Header.Set("Content-Type", "application/json")
	anonymous := httptest.NewRecorder()
	h.ServeHTTP(anonymous, request)
	if anonymous.Code != 401 {
		t.Fatalf("anonymous preview: %d %s", anonymous.Code, anonymous.Body)
	}
	malformed := managementRequest(t, h, "POST", path, "{", nil)
	if malformed.Code != http.StatusBadRequest || malformed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("malformed preview request: %d %s", malformed.Code, malformed.Body)
	}
	current, err := s.core.GetEnvironment(ctx, env.ID)
	if err != nil || current.Revision != env.Revision || current.ApplicationID != "" {
		t.Fatalf("preview changed environment: %+v %v", current, err)
	}
	if operations, err := s.store.ListDeploymentOperations(ctx, "", storage.Page{Limit: 20}); err != nil || len(operations) != 0 {
		t.Fatalf("preview queued execution: %+v %v", operations, err)
	}
	var reviewsAfter int
	if err = s.store.(*storage.SQLStore).DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM control_documents WHERE kind IN ('environment-review','service-review')").Scan(&reviewsAfter); err != nil || reviewsAfter != reviewsBefore {
		t.Fatalf("preview stored a deployment review: before=%d after=%d error=%v", reviewsBefore, reviewsAfter, err)
	}
}
