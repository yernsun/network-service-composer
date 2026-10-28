package marketplace

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func validManifest() Manifest {
	return Manifest{SchemaVersion: 1, Marketplace: Metadata{ID: "demo", Name: Text{"en": "Demo"}, Endpoint: "https://example.test/market/", DefaultLocale: "en", Locales: []string{"en", "ru"}}, Apps: []App{{ID: "demo", Name: Text{"en": "Demo"}, Versions: []Version{{Version: "1", Files: Files{Compose: File{Path: "compose.yaml", SHA256: strings.Repeat("a", 64)}}}}}}}
}
func TestManifestValidationBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Manifest)
		errorText string
	}{
		{"identity", func(m *Manifest) { m.Marketplace.ID = "" }, "identity"},
		{"empty name", func(m *Manifest) { m.Marketplace.Name = Text{"en": "  "} }, "identity"},
		{"no locales", func(m *Manifest) { m.Marketplace.Locales = nil }, "identity"},
		{"empty locale", func(m *Manifest) { m.Marketplace.Locales = []string{"en", ""} }, "locale"},
		{"duplicate locale", func(m *Manifest) { m.Marketplace.Locales = []string{"en", "en"} }, "locale"},
		{"invalid locale", func(m *Manifest) { m.Marketplace.Locales = []string{"en", "not a language"} }, "locale"},
		{"missing default", func(m *Manifest) { m.Marketplace.DefaultLocale = "fr" }, "default_locale"},
		{"bad URL", func(m *Manifest) { m.Marketplace.Homepage = "https://%zz/" }, "URLs"},
		{"http URL", func(m *Manifest) { m.Marketplace.Homepage = "http://example.test/" }, "URLs"},
		{"relative URL", func(m *Manifest) { m.Marketplace.Homepage = "/website" }, "URLs"},
		{"credentials", func(m *Manifest) { m.Marketplace.Homepage = "https://user:password@example.test/" }, "URLs"},
		{"fragment", func(m *Manifest) { m.Marketplace.Homepage = "https://example.test/#fragment" }, "URLs"},
		{"query", func(m *Manifest) { m.Marketplace.Endpoint = "https://example.test/?foo=1/" }, "URLs"},
		{"empty query", func(m *Manifest) { m.Marketplace.Homepage = "https://example.test/?" }, "URLs"},
		{"missing endpoint", func(m *Manifest) { m.Marketplace.Endpoint = "" }, "endpoint"},
		{"directory endpoint", func(m *Manifest) { m.Marketplace.Endpoint = "https://example.test" }, "endpoint"},
		{"missing compose", func(m *Manifest) { m.Apps[0].Versions[0].Files.Compose = File{} }, "file path"},
		{"missing digest", func(m *Manifest) { m.Apps[0].Versions[0].Files.Compose.SHA256 = "" }, "SHA256"},
		{"invalid digest", func(m *Manifest) { m.Apps[0].Versions[0].Files.Compose.SHA256 = strings.Repeat("z", 64) }, "SHA256"},
		{"original path traversal", func(m *Manifest) { m.Apps[0].Versions[0].Files.Compose.SourcePath = "../outside" }, "original"},
		{"orphan file metadata", func(m *Manifest) { m.Apps[0].Versions[0].Files.EnvExample = File{SHA256: strings.Repeat("a", 64)} }, "requires a path"},
		{"env traversal", func(m *Manifest) { m.Apps[0].Versions[0].Files.EnvExample = File{Path: "../.env"} }, "file path"},
		{"duplicate app", func(m *Manifest) { m.Apps = append(m.Apps, m.Apps[0]) }, "duplicate app"},
		{"app traversal", func(m *Manifest) { m.Apps[0].ID = "../app" }, "app"},
		{"empty version", func(m *Manifest) { m.Apps[0].Versions[0].Version = " " }, "version"},
		{"duplicate version", func(m *Manifest) { m.Apps[0].Versions = append(m.Apps[0].Versions, m.Apps[0].Versions[0]) }, "version"},
		{"readme undeclared", func(m *Manifest) { m.Apps[0].Versions[0].Files.Readme = map[string]File{"fr": {Path: "readme.md"}} }, "undeclared"},
		{"readme path", func(m *Manifest) { m.Apps[0].Versions[0].Files.Readme = map[string]File{"en": {Path: "../readme.md"}} }, "file path"},
		{"readme missing default", func(m *Manifest) {
			m.Apps[0].Versions[0].Files.Readme = map[string]File{"ru": {Path: "ru.md", SHA256: strings.Repeat("b", 64)}}
		}, "default language"},
		{"readme collision", func(m *Manifest) {
			m.Apps[0].Versions[0].Files.Readme = map[string]File{"en": {Path: "en.md", SHA256: strings.Repeat("b", 64)}}
			m.Apps[0].Versions[0].Files.Manuals = map[string]Document{"readme": {}}
		}, "reserved"},
		{"invalid asset key", func(m *Manifest) { m.Apps[0].Versions[0].Files.Assets = map[string]File{"../logo.png": {}} }, "asset key"},
		{"invalid asset path", func(m *Manifest) {
			m.Apps[0].Versions[0].Files.Assets = map[string]File{"logo.png": {Path: "../logo.png"}}
		}, "file path"},
		{"empty variable", func(m *Manifest) { m.Apps[0].Versions[0].Variables = []Variable{{Key: " "}} }, "variable"},
		{"duplicate variable", func(m *Manifest) { m.Apps[0].Versions[0].Variables = []Variable{{Key: "A"}, {Key: "A"}} }, "variable"},
		{"secret default", func(m *Manifest) {
			m.Apps[0].Versions[0].Variables = []Variable{{Key: "PASSWORD", Secret: true, Default: "secret"}}
		}, "default"},
	}
	for _, scope := range []string{"market", "app"} {
		for _, entry := range []struct {
			name     string
			doc      Document
			expected string
		}{
			{"empty", Document{}, "default-language"},
			{"undeclared", Document{Files: map[string]File{"en": {Path: "en.md", SHA256: strings.Repeat("a", 64)}, "fr": {Path: "fr.md"}}}, "undeclared"},
			{"bad path", Document{Files: map[string]File{"en": {Path: "../en.md"}}}, "file path"},
			{"stale", Document{Files: map[string]File{"en": {Path: "en.md", SHA256: strings.Repeat("a", 64)}}, Revision: "2", Translations: map[string]string{"en": "1"}}, "stale"},
		} {
			scope, entry := scope, entry
			tests = append(tests, struct {
				name      string
				mutate    func(*Manifest)
				errorText string
			}{scope + " docs " + entry.name, func(m *Manifest) {
				if scope == "market" {
					m.Marketplace.Docs = map[string]Document{"guide": entry.doc}
				} else {
					m.Apps[0].Versions[0].Files.Manuals = map[string]Document{"guide": entry.doc}
				}
			}, entry.expected})
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := validManifest()
			test.mutate(&m)
			if err := m.Validate(true); err == nil || !strings.Contains(err.Error(), test.errorText) {
				t.Fatalf("want %s, got %v", test.errorText, err)
			}
		})
	}
	for _, published := range []bool{false, true} {
		m := validManifest()
		m.Marketplace.Homepage = "https://example.test/"
		f := File{Path: "docs/readme.md", SHA256: strings.Repeat("a", 64), SourcePath: "docs/readme.md"}
		m.Marketplace.Docs = map[string]Document{"guide": {Files: map[string]File{"en": f}, Revision: "1", Translations: map[string]string{"en": "1"}}}
		v := &m.Apps[0].Versions[0]
		v.Files.EnvExample = f
		v.Files.Readme = map[string]File{"en": f}
		v.Files.Assets = map[string]File{"logo.png": f}
		v.Variables = []Variable{{Key: "HOST", Default: "localhost"}}
		if err := m.Validate(published); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRecommendedDomainsRejectUnknownAndNonJSONValues(t *testing.T) {
	for _, fields := range []string{
		"        unknown_field: surprise\n",
		"        target_port: .nan\n",
		"        target_port: [80]\n",
		"        service: {nested: value}\n",
	} {
		data := "schema_version: 1\napps:\n  - id: web\n    versions:\n      - version: one\n        recommended_domains:\n          - service: web\n" + strings.ReplaceAll(fields, "        ", "            ")
		if _, err := Decode([]byte(data)); err == nil {
			t.Fatalf("invalid recommendation accepted: %s", fields)
		}
	}
	data := "schema_version: 1\napps:\n  - id: web\n    versions:\n      - version: one\n        recommended_domains:\n          - service: web\n            target_port: 8080\n            path: /\n            tls_mode: managed\n"
	m, err := Decode([]byte(data))
	if err != nil || m.Apps[0].Versions[0].RecommendedDomains[0].TargetPort != 8080 {
		t.Fatal("valid typed recommendation rejected", err)
	}
}

func TestStrictYAMLFilesAndLocalizedText(t *testing.T) {
	for _, input := range []string{"[one,two]", "42", "true", "{en: 42}", "{en: [one]}", "{en: value, en: other}", "{42: text}"} {
		var text Text
		if err := yaml.Unmarshal([]byte(input), &text); err == nil {
			t.Errorf("accepted text %s", input)
		}
	}
	for _, input := range []string{"[one,two]", "42", "true", "{path: file, unknown: value}", "{path: [one]}", "{path: file, path: other}", "{42: file}"} {
		var file File
		if err := yaml.Unmarshal([]byte(input), &file); err == nil {
			t.Errorf("accepted descriptor %s", input)
		}
	}
	var text Text
	if err := yaml.Unmarshal([]byte("hello"), &text); err != nil {
		t.Fatal(err)
	}
	if text.Default("fr") != "hello" {
		t.Fatal("missing default")
	}
	if err := yaml.Unmarshal([]byte("en: English\nru: Русский"), &text); err != nil {
		t.Fatal(err)
	}
	if text.Default("ru") != "Русский" {
		t.Fatal("localization lost")
	}
	file := File{SHA256: "stale", SourcePath: "stale"}
	if err := yaml.Unmarshal([]byte("new.yaml"), &file); err != nil {
		t.Fatal(err)
	}
	if file.SHA256 != "" || file.SourcePath != "" {
		t.Fatal("stale descriptor fields retained")
	}
	if err := yaml.Unmarshal([]byte("path: file\nsha256: digest\nsource_path: original"), &file); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode([]byte("schema_version: 99")); err == nil {
		t.Fatal("unknown schema")
	}
	if _, err := Decode([]byte("[")); err == nil {
		t.Fatal("malformed YAML")
	}
	if _, err := Decode([]byte("schema_version: 1\n---\n[")); err == nil {
		t.Fatal("malformed trailing document")
	}
	for _, p := range []string{".", "name?", "name\nfile", "%zz", "//host/file", "file#anchor"} {
		if ValidPath(p) {
			t.Errorf("accepted %q", p)
		}
	}
	if Digest([]byte("abc")) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("digest mismatch")
	}
	if JSON(map[string]string{"a": "b"}) != `{"a":"b"}` {
		t.Fatal("JSON mismatch")
	}
}
