package marketplace

import "testing"

func TestPathsAndStrictManifest(t *testing.T) {
	for _, p := range []string{"../secret", "/etc/passwd", "https://example.com/x", "foo/../secret", "%2e%2e/file", "a\\b", "file?query"} {
		if ValidPath(p) {
			t.Errorf("accepted %s", p)
		}
	}
	for _, p := range []string{"apps/redis/compose.yaml", "docs/ru/readme.md"} {
		if !ValidPath(p) {
			t.Errorf("rejected %s", p)
		}
	}
	if _, e := Decode([]byte("schema_version: 1\nunknown: value\n")); e == nil {
		t.Fatal("unknown field accepted")
	}
	if _, e := Decode([]byte("schema_version: 1\n---\nschema_version: 1\n")); e == nil {
		t.Fatal("multiple documents accepted")
	}
}
