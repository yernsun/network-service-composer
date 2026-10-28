package upstream

import (
	"regexp"
	"testing"
)

func TestNameIsStableAndUsesCanonicalFields(t *testing.T) {
	name, err := Name("rt_1", "hello/web", "hello_web", 80, "http", "")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^nsc_hello_web_[0-9a-f]{8}$`).MatchString(name) {
		t.Fatalf("unexpected upstream name %q", name)
	}
	recreated, err := Name("rt_1", "hello/web", "hello_web", 80, "http", "")
	if err != nil || recreated != name {
		t.Fatalf("upstream identity changed: %q %v", recreated, err)
	}
	changed, err := Name("rt_1", "hello/web", "hello_web", 443, "https", "hello.internal")
	if err != nil {
		t.Fatal(err)
	}
	if changed == name {
		t.Fatal("route-significant fields must change the suffix")
	}
}

func TestNameSanitizesQualifiedName(t *testing.T) {
	name, err := Name("rt", "key", "My Stack/API.v2", 8080, "http", "")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^nsc_my_stack_api_v2_[0-9a-f]{8}$`).MatchString(name) {
		t.Fatalf("unexpected sanitized name %q", name)
	}
}
