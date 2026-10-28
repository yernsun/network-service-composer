package marketplace

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestManifestRejectsIncompleteDescriptorsAndAmbiguousIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{"locale whitespace", func(m *Manifest) { m.Marketplace.Locales = []string{"en", " ru"} }, "locale"},
		{"https without authority", func(m *Manifest) { m.Marketplace.Homepage = "https:/website" }, "URLs"},
		{"orphan source path", func(m *Manifest) { m.Apps[0].Versions[0].Files.EnvExample = File{SourcePath: "env.example"} }, "requires a path"},
		{"version whitespace", func(m *Manifest) { m.Apps[0].Versions[0].Version = " 1" }, "version"},
		{"document missing default", func(m *Manifest) {
			m.Marketplace.Docs = map[string]Document{"guide": {Files: map[string]File{"ru": {Path: "ru.md"}}}}
		}, "default-language"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.mutate(&m)
			if err := m.Validate(true); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %s: %v", tc.want, err)
			}
		})
	}
	for _, raw := range []string{"{path: 42}", "{sha256: true}", "{source_path: null}"} {
		var f File
		if err := yaml.Unmarshal([]byte(raw), &f); err == nil {
			t.Fatalf("non-string descriptor accepted: %s", raw)
		}
	}
	for _, p := range []string{"file?value=1", "..", "https:/file"} {
		if ValidPath(p) {
			t.Fatalf("unsafe relative artifact path accepted: %q", p)
		}
	}
}

func TestArtifactPathRejectsUnicodeControlCharacters(t *testing.T) {
	for _, path := range []string{"docs/\u0085readme.md", "docs/\u009freadme.md"} {
		if ValidPath(path) {
			t.Fatalf("control character accepted in artifact identity: %q", path)
		}
	}
}
