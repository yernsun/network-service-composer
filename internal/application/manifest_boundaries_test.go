package application

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yernsun/network-service-composer/internal/model"
	"gopkg.in/yaml.v3"
)

func TestManifestConstraintsRejectInvalidDefinitionsAndDefaults(t *testing.T) {
	for _, manifest := range []Manifest{
		{Groups: []ManifestGroup{{Name: " "}}}, {Groups: []ManifestGroup{{Name: "group"}, {Name: " group "}}},
		{Variables: []ManifestVariable{{Key: "bad-key"}}}, {Variables: []ManifestVariable{{Key: "KEY"}, {Key: "KEY"}}},
		{Variables: []ManifestVariable{{Key: "KEY", Group: "missing"}}},
		{Variables: []ManifestVariable{{Key: "KEY", MinLength: -1}}}, {Variables: []ManifestVariable{{Key: "KEY", MaxLength: -1}}},
		{Variables: []ManifestVariable{{Key: "KEY", MinLength: 3, MaxLength: 2}}},
		{Variables: []ManifestVariable{{Key: "KEY", Pattern: "["}}}, {Variables: []ManifestVariable{{Key: "KEY", Generator: "shell"}}},
		{Variables: []ManifestVariable{{Key: "KEY", Choices: []string{"same", "same"}}}},
		{Variables: []ManifestVariable{{Key: "KEY", Default: "a", MinLength: 2}}},
		{Variables: []ManifestVariable{{Key: "KEY", Default: "long", MaxLength: 2}}},
		{Variables: []ManifestVariable{{Key: "KEY", Default: "no", Pattern: "^yes$"}}},
		{Variables: []ManifestVariable{{Key: "KEY", Default: "no", Choices: []string{"yes", "maybe"}}}},
	} {
		raw, err := yaml.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		result, err := Preflight(PreflightInput{ComposeYAML: "services:\n  web: {image: 'image:fixed'}", ManifestYAML: string(raw)})
		if err == nil || result.ComposeCompatible || result.ResolvedYAML != "" {
			t.Fatalf("invalid manifest accepted: %#v %v", manifest, err)
		}
	}
	for _, raw := range []string{"unknown: value", "[", "name: first\n---\nname: second", "name: first\n---\n["} {
		if _, err := ParseManifest(raw); err == nil {
			t.Fatalf("invalid manifest accepted: %q", raw)
		}
	}
	if _, err := ParseManifest(" \n"); err != nil {
		t.Fatal(err)
	}
	if err := validateManifestVariable(ManifestVariable{Key: "KEY", Pattern: "["}, "value"); err == nil {
		t.Fatal("invalid pattern accepted by value validation")
	}
}

func TestManifestValuesUnicodeBoundariesAndSafeGenerators(t *testing.T) {
	manifest := Manifest{Groups: []ManifestGroup{{Name: "credentials"}}, Variables: []ManifestVariable{
		{Key: "TEXT", Group: "credentials", Required: true, Default: "你好", MinLength: 2, MaxLength: 2, Pattern: "^你好$", Choices: []string{"你好", "再见", "other"}},
		{Key: "PASSWORD", Secret: true, Generator: " password "}, {Key: "TOKEN", Secret: true, Generator: "TOKEN"}, {Key: "ID", Generator: "uuid"}, {Key: "OPTIONAL"},
	}}
	raw, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := PreflightInput{ComposeYAML: "services:\r\n  web:\r\n    image: image:fixed\r\n    environment:\r\n      VALUE: ${TEXT}\r\n", ManifestYAML: string(raw)}
	first, err := Preflight(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Preflight(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Environment["TEXT"] != "你好" || strings.Contains(first.ComposeYAML, "\r") || first.ComposeSHA256 != second.ComposeSHA256 || first.EnvironmentSHA256 == second.EnvironmentSHA256 {
		t.Fatalf("preflight identity/generation mismatch: %#v", first.EnvironmentKeys)
	}
	for _, key := range []string{"PASSWORD", "TOKEN"} {
		decoded, err := hex.DecodeString(first.Environment[key])
		if err != nil || len(decoded) != 24 || first.Environment[key] == second.Environment[key] {
			t.Fatalf("invalid generated %s", key)
		}
	}
	if _, err := uuid.Parse(first.Environment["ID"]); err != nil {
		t.Fatal(err)
	}
	if _, err := generateValue("unsupported"); err == nil {
		t.Fatal("unknown generator accepted")
	}
	for _, tc := range []struct {
		key, value string
		want       string
	}{
		{"TEXT", "你", "min_length"}, {"TEXT", "你好啊", "max_length"}, {"TEXT", "再见", "pattern"},
	} {
		items := []EnvironmentVariable{{Key: tc.key, Value: &tc.value}}
		input.Environment = EnvironmentInput{Values: &items}
		if _, err := Preflight(input); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("constraint %s: %v", tc.want, err)
		}
	}
}

func TestRecommendedDomainValidationAtPreflightBoundary(t *testing.T) {
	for _, tc := range []struct {
		domain ManifestDomain
		valid  bool
	}{
		{ManifestDomain{Service: "web"}, true}, {ManifestDomain{Service: "web", Path: "/api/", TargetPort: 1, TLSMode: model.TLSDisabled}, true},
		{ManifestDomain{Service: "web", TargetPort: 65535, TLSMode: model.TLSManaged}, true}, {ManifestDomain{Service: "web", TLSMode: model.TLSCustom}, true},
		{ManifestDomain{Service: "missing"}, false}, {ManifestDomain{Service: "web", Path: "relative"}, false}, {ManifestDomain{Service: "web", Path: "/?query"}, false},
		{ManifestDomain{Service: "web", Path: "/#fragment"}, false}, {ManifestDomain{Service: "web", Path: "/\r\n"}, false},
		{ManifestDomain{Service: "web", TargetPort: -1}, false}, {ManifestDomain{Service: "web", TargetPort: 65536}, false}, {ManifestDomain{Service: "web", TLSMode: "unknown"}, false},
	} {
		raw, err := yaml.Marshal(Manifest{RecommendedDomains: []ManifestDomain{tc.domain}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := Preflight(PreflightInput{ComposeYAML: "services:\n  web: {image: 'image:fixed'}", ManifestYAML: string(raw)})
		if (err == nil) != tc.valid {
			t.Fatalf("domain %#v: %v", tc.domain, err)
		}
		if tc.valid && (len(result.RecommendedDomains) != 1 || result.RecommendedDomains[0] != tc.domain) {
			t.Fatal("recommended domain changed")
		}
	}
}

func TestPreflightFailuresAndCatalogAnalysisRemainDistinct(t *testing.T) {
	for _, input := range []PreflightInput{
		{}, {ComposeYAML: "services: {}"}, {ComposeYAML: "services:\n  web: {image: 'image:fixed'}", EnvExample: "invalid"},
		{ComposeYAML: "services:\n  web: {image: '${REQUIRED:?missing}'}"},
		{ComposeYAML: "services:\n  web: {image: 'image:fixed'}", ManifestYAML: "variables:\n  - {key: REQUIRED, required: true}"},
	} {
		if result, err := Preflight(input); err == nil || result.ComposeCompatible {
			t.Fatalf("failed preflight accepted: %#v %v", result, err)
		}
	}
	keys := requiredInterpolationKeys("${VALID:?required} ${OTHER?} ${VALID?again} ${INVALID.KEY?} ${OPTIONAL:-value} tail ${UNFINISHED")
	if strings.Join(keys, ",") != "OTHER,VALID" {
		t.Fatalf("required keys %v", keys)
	}
	result, err := Preflight(PreflightInput{ComposeYAML: "services:\n  web:\n    image: '${IMAGE:?required}'\n    environment: {TOKEN: '${TOKEN}'}", ManifestYAML: "variables:\n  - {key: TOKEN, generator: password, required: true, secret: true, min_length: 48}", CatalogAnalysis: true})
	if err != nil || result.Environment["IMAGE"] != "nsc-catalog-placeholder" || result.Environment["TOKEN"] != "nsc-catalog-placeholder" {
		t.Fatalf("analysis generated or rejected placeholder: %#v %v", result.Environment, err)
	}
}

func TestComposeSectionDigestsExposeOnlyChangedSections(t *testing.T) {
	base := "services:\n  web:\n    image: image:fixed\n    deploy: {replicas: 1}\nvolumes: {data: {}}\nnetworks: {private: {}}"
	first, err := ComposeSectionDigests(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ComposeSectionDigests(strings.Replace(base, "replicas: 1", "replicas: 2", 1))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range first {
		if len(value) != 64 {
			t.Fatal("non-digest content exposed")
		}
		if (value != second[key]) != (key == "services" || key == "deploy") {
			t.Fatalf("wrong change boundary %s", key)
		}
	}
	for _, raw := range []string{"[", "services: {web: {deploy: .nan}}"} {
		if _, err := ComposeSectionDigests(raw); err == nil {
			t.Fatalf("unserializable section accepted: %q", raw)
		}
	}
	for _, raw := range []string{"", "services: []", "services: {web: null, api: {image: fixed}}"} {
		if result, err := ComposeSectionDigests(raw); err != nil || len(result) != 4 {
			t.Fatalf("partial sections: %#v %v", result, err)
		}
	}
}
