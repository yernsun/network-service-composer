package api

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIProblemI18NContract(t *testing.T) {
	data, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI    string `yaml:"openapi"`
		Components struct {
			Schemas map[string]struct {
				Required   []string               `yaml:"required"`
				Properties map[string]interface{} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("OpenAPI document is invalid YAML: %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q", document.OpenAPI)
	}
	problem, ok := document.Components.Schemas["Problem"]
	if !ok {
		t.Fatal("Problem schema is missing")
	}
	required := make(map[string]bool, len(problem.Required))
	for _, field := range problem.Required {
		required[field] = true
	}
	for _, field := range []string{"type", "title", "status", "detail", "code"} {
		if !required[field] {
			t.Errorf("Problem.%s is not required", field)
		}
	}
	for _, field := range []string{"params", "errors"} {
		if _, ok := problem.Properties[field]; !ok {
			t.Errorf("Problem.%s is missing", field)
		}
	}
	if _, ok := document.Components.Schemas["ProblemFieldError"]; !ok {
		t.Fatal("ProblemFieldError schema is missing")
	}
	if _, ok := document.Components.Schemas["SettingsEnvelope"]; !ok {
		t.Fatal("SettingsEnvelope schema is missing")
	}
}

func TestOpenAPIApplicationManagementContract(t *testing.T) {
	data, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err = yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	requiredPaths := map[string][]string{
		"/catalog/sources":                           {"get", "post"},
		"/catalog/sources/{id}/sync":                 {"post"},
		"/catalog/apps":                              {"get"},
		"/applications/preflight":                    {"post"},
		"/applications":                              {"get", "post"},
		"/applications/{id}/revisions/preflight":     {"post"},
		"/applications/{id}/revisions":               {"get", "post"},
		"/applications/{id}/operations":              {"get", "post"},
		"/operations/{id}":                           {"get"},
		"/services/{id}/domain-bindings":             {"get", "post"},
		"/services/{id}/domain-bindings/{bindingID}": {"patch", "delete"},
		"/services/{id}/restart":                     {"post"},
		"/services/{id}/scale":                       {"post"},
	}
	for path, methods := range requiredPaths {
		item, exists := document.Paths[path]
		if !exists {
			t.Errorf("OpenAPI path %s is missing", path)
			continue
		}
		for _, method := range methods {
			if _, exists = item[method]; !exists {
				t.Errorf("OpenAPI operation %s %s is missing", method, path)
			}
		}
	}
	for _, schema := range []string{"CatalogSource", "CatalogAppVersion", "Application", "ApplicationRevision", "ApplicationRevisionPreview", "DeploymentOperation", "DomainBinding", "EnvironmentInput"} {
		if _, exists := document.Components.Schemas[schema]; !exists {
			t.Errorf("OpenAPI schema %s is missing", schema)
		}
	}
}

func TestOpenAPIDeploymentContractUsesScopedKeyAndExistingManagementAPI(t *testing.T) {
	deploymentSource, err := os.ReadFile("../../api/openapi-deployments.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var deployment struct {
		OpenAPI string `yaml:"openapi"`
		Servers []struct {
			URL string `yaml:"url"`
		} `yaml:"servers"`
		Security   []map[string]any          `yaml:"security"`
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			SecuritySchemes map[string]struct {
				Type string `yaml:"type"`
				In   string `yaml:"in"`
				Name string `yaml:"name"`
			} `yaml:"securitySchemes"`
		} `yaml:"components"`
	}
	if err = yaml.Unmarshal(deploymentSource, &deployment); err != nil {
		t.Fatal(err)
	}
	if deployment.OpenAPI != "3.1.0" || len(deployment.Servers) != 1 || deployment.Servers[0].URL != "/openapi/v1" {
		t.Fatal("deployment server contract is missing")
	}
	scheme := deployment.Components.SecuritySchemes["deploymentAPIKey"]
	if scheme.Type != "apiKey" || scheme.In != "header" || scheme.Name != "X-NSC-API-Key" || len(deployment.Security) != 1 {
		t.Fatal("deployment API key security contract is missing")
	}
	if _, ok := deployment.Security[0]["deploymentAPIKey"]; !ok {
		t.Fatal("deployment API does not require the independent key")
	}
	for path, methods := range map[string][]string{
		"/environments/{id}":                     {"get"},
		"/environments/{id}/preflight":           {"post"},
		"/environments/{id}/deployments":         {"post"},
		"/environments/{id}/service-preflight":   {"post"},
		"/environments/{id}/service-deployments": {"post"},
		"/operations/{id}":                       {"get"},
	} {
		for _, method := range methods {
			if _, ok := deployment.Paths[path][method]; !ok {
				t.Errorf("deployment contract missing %s %s", method, path)
			}
		}
	}
	controlSource, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var control struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err = yaml.Unmarshal(controlSource, &control); err != nil {
		t.Fatal(err)
	}
	for path, methods := range map[string][]string{"/openapi-keys": {"get", "post"}, "/openapi-keys/{id}": {"delete"}} {
		for _, method := range methods {
			if _, ok := control.Paths[path][method]; !ok {
				t.Errorf("key management contract missing %s %s", method, path)
			}
		}
	}
}

func TestOpenAPIDNSOnlyBreakingContract(t *testing.T) {
	data, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err = yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/gateway-pools/{id}/config-preview", "/gateway-pools/{id}/route-trace", "/services/{id}/instances"} {
		if _, ok := document.Paths[path]; !ok {
			t.Errorf("DNS-only API path %s is missing", path)
		}
	}
	for _, removed := range []string{"/services/{id}/endpoints", "/services/{id}/domains"} {
		if _, ok := document.Paths[removed]; ok {
			t.Errorf("removed compatibility path %s is still published", removed)
		}
	}
	text := string(data)
	for _, removed := range []string{"backend_mode", "discovery_mode"} {
		if strings.Contains(text, removed) {
			t.Errorf("removed compatibility field %q is still published", removed)
		}
	}
	for _, required := range []string{"qualified_name", "dns_name", "dns_status", "entrypoints", "redirect_http_to_https", "renderer_version", "rendered_digest"} {
		if !strings.Contains(text, required) {
			t.Errorf("v2 field %q is missing", required)
		}
	}
}
