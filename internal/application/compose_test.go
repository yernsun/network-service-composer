package application

import (
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
)

func stringPointer(value string) *string { return &value }

func TestEnvironmentRawAndKeyModesAreEquivalent(t *testing.T) {
	raw := "NAME=web\nPORT=8080\nQUOTED=\"hello world\"\n"
	values := []EnvironmentVariable{
		{Key: "NAME", Value: stringPointer("web")},
		{Key: "PORT", Value: stringPointer("8080")},
		{Key: "QUOTED", Value: stringPointer("hello world")},
	}
	equal, err := EqualEnvironmentModes(raw, values)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("raw and key/value modes normalized differently")
	}
}

func TestDotEnvQuotesCommentsAndDuplicates(t *testing.T) {
	values, err := ParseDotEnv("export A='literal # value'\nB=plain # comment\nC=\"line\\nvalue\" # comment\nMULTI='first\nsecond'\nBASE=example\nEXPANDED=${BASE:-fallback}-value\nLITERAL='${BASE}'\n")
	if err != nil {
		t.Fatal(err)
	}
	if values["A"] != "literal # value" || values["B"] != "plain" || values["C"] != "line\nvalue" ||
		values["MULTI"] != "first\nsecond" || values["EXPANDED"] != "example-value" || values["LITERAL"] != "${BASE}" {
		t.Fatalf("unexpected dotenv values: %#v", values)
	}
	if _, err = ParseDotEnv("A=1\nA=2\n"); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate variable was accepted: %v", err)
	}
	if _, err = ParseDotEnv("BAD-NAME=1\n"); err == nil {
		t.Fatal("invalid environment name was accepted")
	}
}

func TestDotEnvInterpolationUsesDefaultsButNotProcessEnvironment(t *testing.T) {
	t.Setenv("CONTROLLER_SECRET", "must-not-be-used")
	defaults, err := ParseDotEnv("HOST=database\nPORT=5432\n")
	if err != nil {
		t.Fatal(err)
	}
	raw := "URL=postgres://${HOST}:${PORT}/app\nLEAK=$CONTROLLER_SECRET\nPRICE=$$5\n"
	values, _, err := NormalizeEnvironment(EnvironmentInput{Raw: &raw}, defaults, nil)
	if err != nil {
		t.Fatal(err)
	}
	if values["URL"] != "postgres://database:5432/app" || values["LEAK"] != "" || values["PRICE"] != "$5" {
		t.Fatalf("unexpected scoped interpolation: %#v", values)
	}
	roundTrip, err := ParseDotEnv(RenderDotEnv(values))
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip["PRICE"] != "$5" || roundTrip["URL"] != values["URL"] {
		t.Fatalf("canonical dotenv did not round-trip: %#v", roundTrip)
	}
}

func TestSecretRetainAndClear(t *testing.T) {
	previous := map[string]string{"PASSWORD": "old-secret", "OTHER": "old"}
	empty := ""
	values := []EnvironmentVariable{{Key: "PASSWORD", Value: &empty, Secret: true}, {Key: "OTHER", Clear: true}}
	result, secrets, err := NormalizeEnvironment(EnvironmentInput{Values: &values}, nil, previous)
	if err != nil {
		t.Fatal(err)
	}
	if result["PASSWORD"] != "old-secret" {
		t.Fatalf("write-only secret was not retained: %#v", result)
	}
	if _, exists := result["OTHER"]; exists {
		t.Fatalf("explicit clear did not remove value: %#v", result)
	}
	if len(secrets) != 1 || secrets[0] != "PASSWORD" {
		t.Fatalf("secret metadata = %#v", secrets)
	}
}

func TestKeyModePatchesAndRawModeReplacesPreviousEnvironment(t *testing.T) {
	previous := map[string]string{"KEEP": "old", "EMPTY": "old-empty", "REMOVE": "old-remove"}
	defaults := map[string]string{"DEFAULT": "template"}
	empty := ""
	values := []EnvironmentVariable{
		{Key: "EMPTY", Value: &empty},
		{Key: "REMOVE", Clear: true},
		{Key: "NEW", Value: stringPointer("new")},
	}
	patched, _, err := NormalizeEnvironment(EnvironmentInput{Values: &values}, defaults, previous)
	if err != nil {
		t.Fatal(err)
	}
	if patched["KEEP"] != "old" || patched["EMPTY"] != "" || patched["DEFAULT"] != "template" || patched["NEW"] != "new" {
		t.Fatalf("key mode did not patch the previous environment: %#v", patched)
	}
	if _, exists := patched["REMOVE"]; exists {
		t.Fatalf("clear did not remove the previous value: %#v", patched)
	}

	raw := "NEW=raw\n"
	replaced, _, err := NormalizeEnvironment(EnvironmentInput{Raw: &raw}, defaults, previous)
	if err != nil {
		t.Fatal(err)
	}
	if replaced["NEW"] != "raw" || replaced["DEFAULT"] != "template" || len(replaced) != 2 {
		t.Fatalf("raw mode was not a complete replacement: %#v", replaced)
	}
}

func TestPreviousSecretClassificationSurvivesKeyPatch(t *testing.T) {
	compose := "services:\n  app:\n    image: busybox:1.37\n    environment:\n      PASSWORD: ${PASSWORD}\n"
	values := []EnvironmentVariable{}
	result, err := Preflight(PreflightInput{
		ComposeYAML: compose, Backend: model.RuntimeDocker,
		Environment: EnvironmentInput{Values: &values}, Previous: map[string]string{"PASSWORD": "old-secret"},
		PreviousSecretKeys: []string{"PASSWORD"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SecretKeys) != 1 || result.SecretKeys[0] != "PASSWORD" {
		t.Fatalf("previous secret classification was lost: %#v", result.SecretKeys)
	}
	preview := RedactResolvedYAML(result.ComposeYAML, result.Environment, result.SecretKeys)
	if strings.Contains(preview, "old-secret") || !strings.Contains(preview, "[redacted]") {
		t.Fatalf("retained secret leaked through preview: %q", preview)
	}
}

func TestPreviousSecretEmptyValueRetainsWithoutClientEchoingClassification(t *testing.T) {
	empty := ""
	values := []EnvironmentVariable{{Key: "PASSWORD", Value: &empty}}
	result, err := Preflight(PreflightInput{
		ComposeYAML: "services:\n  app:\n    image: busybox:1.37\n    environment:\n      PASSWORD: ${PASSWORD}\n",
		Backend:     model.RuntimeDocker, Environment: EnvironmentInput{Values: &values},
		Previous: map[string]string{"PASSWORD": "old-secret"}, PreviousSecretKeys: []string{"PASSWORD"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Environment["PASSWORD"] != "old-secret" || len(result.SecretKeys) != 1 || result.SecretKeys[0] != "PASSWORD" {
		t.Fatalf("write-only previous secret was not retained: %#v", result)
	}
}

func TestInterpolationNeverReadsControllerEnvironment(t *testing.T) {
	t.Setenv("CONTROLLER_ONLY", "must-not-leak")
	resolved, err := Interpolate("a=${SET:?required}\nb=${MISSING:-fallback}\nc=$CONTROLLER_ONLY\nd=$$HOSTNAME\n", map[string]string{"SET": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resolved, "must-not-leak") || resolved != "a=ok\nb=fallback\nc=\nd=$HOSTNAME\n" {
		t.Fatalf("unexpected interpolation: %q", resolved)
	}
	if escaped := EscapeForComposeCLI(resolved); !strings.Contains(escaped, "d=$$HOSTNAME") {
		t.Fatalf("resolved dollar was not protected for CLI: %q", escaped)
	}
	if _, err = Interpolate("${REQUIRED:?REQUIRED is missing}", nil); err == nil || !strings.Contains(err.Error(), "REQUIRED is missing") {
		t.Fatalf("required expression did not fail: %v", err)
	}
}

func TestPreflightRedactsSecretsAndInfersPort(t *testing.T) {
	raw := "PASSWORD=supersecret\n"
	compose := "services:\n  web:\n    image: nginx:1.29-alpine\n    environment:\n      PASSWORD: \"${PASSWORD:?required}\"\n    expose: [\"8080\"]\n"
	manifest := "variables:\n  - key: PASSWORD\n    required: true\n    secret: true\n"
	result, err := Preflight(PreflightInput{ComposeYAML: compose, ManifestYAML: manifest, Environment: EnvironmentInput{Raw: &raw}, Backend: model.RuntimeDocker})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Services) != 1 || result.Services[0].TargetPort != 8080 || !result.Services[0].PortCertain {
		t.Fatalf("port inference = %#v", result.Services)
	}
	preview := RedactResolvedYAML(result.ComposeYAML, result.Environment, result.SecretKeys)
	if strings.Contains(preview, "supersecret") || !strings.Contains(preview, "[redacted]") {
		t.Fatalf("secret leaked in preview: %q", preview)
	}
}

func TestComposeSafetyPolicy(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "build", body: "build: .", want: "forbidden field build"},
		{name: "privileged", body: "privileged: true", want: "forbidden field privileged"},
		{name: "system bind mount", body: "volumes: [\"/proc:/data\"]", want: "host system interfaces"},
		{name: "invalid volume mapping", body: "volumes: {data: /data}", want: "COMPOSE_SCHEMA_ERROR"},
		{name: "docker socket", body: "environment: {SOCKET: /var/run/docker.sock}", want: "Docker socket"},
		{name: "inherited env", body: "environment: [\"SECRET\"]", want: "cannot inherit controller environment"},
		{name: "capability", body: "cap_add: [SYS_ADMIN]", want: "forbidden field cap_add"},
		{name: "invalid ports mapping", body: "ports: {target: 80}", want: "COMPOSE_SCHEMA_ERROR"},
		{name: "invalid expose port", body: "expose: [\"70000\"]", want: "invalid port"},
		{name: "invalid deploy resources", body: "deploy: {resources: unlimited}", want: "COMPOSE_SCHEMA_ERROR"},
		{name: "undeclared network", body: "networks: [missing]", want: "undeclared network"},
		{name: "undeclared secret", body: "secrets: [missing]", want: "undeclared secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compose := "services:\n  app:\n    image: busybox:1.37\n    " + test.body + "\n"
			_, err := Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeDocker})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestComposeRejectsMultipleDocumentsAndMalformedResourceDefinitions(t *testing.T) {
	values := []struct {
		compose string
		want    string
	}{
		{"services:\n  app: {image: busybox:1.37}\n---\nservices:\n  other: {image: busybox:1.37}\n", "YAML_SYNTAX_ERROR"},
		{"services:\n  app: {image: busybox:1.37}\nvolumes: [data]\n", "COMPOSE_SCHEMA_ERROR"},
		{"services:\n  app: {image: busybox:1.37}\nvolumes:\n  data: local\n", "COMPOSE_SCHEMA_ERROR"},
		{"services:\n  app: {image: busybox:1.37}\nnetworks:\n  edge: []\n", "COMPOSE_SCHEMA_ERROR"},
	}
	for _, value := range values {
		if _, err := Preflight(PreflightInput{ComposeYAML: value.compose, Backend: model.RuntimeDocker}); err == nil || !strings.Contains(err.Error(), value.want) {
			t.Fatalf("expected %q, got %v", value.want, err)
		}
	}
}

func TestApplicationCannotClaimNSCManagedBackendNetwork(t *testing.T) {
	compose := `services:
  app:
    image: busybox:1.37
    networks: [edge]
networks:
  edge:
    external: true
    name: nsc-ingress
`
	if _, err := Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeDocker, ProtectedNetworks: []string{"nsc-ingress"}}); err == nil || !strings.Contains(err.Error(), "managed by NSC") {
		t.Fatalf("protected Gateway network was accepted: %v", err)
	}
}

func TestExternalSecretAndConfigRequireAdministratorAllowlist(t *testing.T) {
	compose := `services:
  app:
    image: busybox:1.37
    secrets: [database_password]
    configs: [application_config]
secrets:
  database_password:
    external: true
    name: production_database_password
configs:
  application_config:
    external: true
`
	if _, err := Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeDocker}); err == nil || !strings.Contains(err.Error(), "not configured by the administrator") {
		t.Fatalf("unconfigured external Docker resource was accepted: %v", err)
	}
	if _, err := Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeDocker,
		AllowedSecrets: []string{"production_database_password"}, AllowedConfigs: []string{"application_config"}}); err != nil {
		t.Fatalf("administrator-configured external resources were rejected: %v", err)
	}
	if _, err := Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeDocker, CatalogAnalysis: true}); err != nil {
		t.Fatalf("generic catalog compatibility analysis rejected external references: %v", err)
	}
}

func TestSwarmCompatibilityBlocksComposeOnlyFields(t *testing.T) {
	compose := "services:\n  app:\n    image: busybox:1.37\n    pull_policy: always\n"
	composeResult, err := Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeDocker})
	if err != nil {
		t.Fatal(err)
	}
	if composeResult.SwarmCompatible || len(composeResult.CompatibilityIssues) == 0 {
		t.Fatalf("Compose-only field was not reported: %#v", composeResult)
	}
	if _, err = Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeSwarm}); err == nil || !strings.Contains(err.Error(), "not Swarm compatible") {
		t.Fatalf("Swarm preflight accepted incompatible Compose: %v", err)
	}
}

func TestSwarmCompatibilityReportsAllKnownComposeOnlyFields(t *testing.T) {
	compose := "name: demo\nservices:\n  db:\n    image: postgres:18-alpine\n  app:\n    image: busybox:1.37\n    depends_on: [db]\n    restart: always\n    shm_size: 64m\nnetworks:\n  local:\n    driver: bridge\n"
	result, err := Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeDocker})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.CompatibilityIssues, "\n")
	for _, expected := range []string{"top-level name", "bridge driver", "depends_on", "restart", "shm_size"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("compatibility report omitted %q: %s", expected, joined)
		}
	}
	if _, err = Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeSwarm}); err == nil {
		t.Fatal("Swarm preflight accepted Compose-only fields")
	}
}

func TestComposeSafetyRejectsDeployDevicesAndInvalidServiceNames(t *testing.T) {
	values := []string{
		"services:\n  app:\n    image: busybox:1.37\n    deploy:\n      resources:\n        reservations:\n          devices:\n            - capabilities: [gpu]\n",
		"services:\n  'bad/service':\n    image: busybox:1.37\n",
	}
	for _, compose := range values {
		if _, err := Preflight(PreflightInput{ComposeYAML: compose, Backend: model.RuntimeDocker}); err == nil {
			t.Fatalf("unsafe Compose was accepted:\n%s", compose)
		}
	}
}

func TestCatalogAnalysisAllowsRequiredPlaceholdersWithoutGeneratingSecrets(t *testing.T) {
	compose := "services:\n  app:\n    image: busybox:1.37\n    environment:\n      PASSWORD: ${PASSWORD:?required}\n"
	manifest := "variables:\n  - key: PASSWORD\n    required: true\n    secret: true\n    generator: password\n"
	if _, err := Preflight(PreflightInput{ComposeYAML: compose, ManifestYAML: manifest, Backend: model.RuntimeDocker}); err != nil {
		t.Fatalf("normal preflight should generate a secret: %v", err)
	}
	result, err := Preflight(PreflightInput{ComposeYAML: compose, ManifestYAML: manifest, Backend: model.RuntimeDocker, CatalogAnalysis: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Environment["PASSWORD"] != "nsc-catalog-placeholder" {
		t.Fatalf("catalog analysis unexpectedly generated a persisted secret: %#v", result.Environment)
	}
}

func TestCatalogAnalysisRejectsInvalidManifestDefinitions(t *testing.T) {
	compose := "services:\n  app:\n    image: busybox:1.37\n"
	for name, manifest := range map[string]string{
		"duplicate variable": "variables:\n  - key: TOKEN\n  - key: TOKEN\n",
		"unknown group":      "variables:\n  - key: TOKEN\n    group: missing\n",
		"invalid generator":  "variables:\n  - key: TOKEN\n    generator: shell\n",
		"invalid pattern":    "variables:\n  - key: TOKEN\n    pattern: '[unterminated'\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Preflight(PreflightInput{ComposeYAML: compose, ManifestYAML: manifest, Backend: model.RuntimeDocker, CatalogAnalysis: true}); err == nil {
				t.Fatalf("invalid manifest was accepted: %s", manifest)
			}
		})
	}
}

func TestManifestDefaultsValidationGroupsAndRecommendedDomains(t *testing.T) {
	compose := "services:\n  web:\n    image: nginx:1.29-alpine\n    expose: [\"8080\"]\n    environment:\n      MODE: ${MODE}\n"
	manifest := `name: Demo
version: 1.0.0
groups:
  - name: general
    label: General
variables:
  - key: MODE
    group: general
    default: production
    choices: [production, development]
    pattern: '^[a-z]+$'
    min_length: 3
    max_length: 20
recommended_domains:
  - service: web
    path: /
    target_port: 8080
    tls_mode: managed
`
	result, err := Preflight(PreflightInput{ComposeYAML: compose, ManifestYAML: manifest, Backend: model.RuntimeDocker})
	if err != nil {
		t.Fatal(err)
	}
	if result.Environment["MODE"] != "production" || len(result.RecommendedDomains) != 1 || result.RecommendedDomains[0].Service != "web" {
		t.Fatalf("manifest metadata was not applied: %#v", result)
	}
	invalidRaw := "MODE=staging\n"
	if _, err = Preflight(PreflightInput{ComposeYAML: compose, ManifestYAML: manifest, Environment: EnvironmentInput{Raw: &invalidRaw}, Backend: model.RuntimeDocker}); err == nil || !strings.Contains(err.Error(), "allowed choices") {
		t.Fatalf("manifest choices were not enforced: %v", err)
	}
}
