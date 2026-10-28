package application

import (
	"fmt"
	"strings"
	"testing"
)

func TestComposeServiceNameAlphabetAndLengthBoundaries(t *testing.T) {
	for _, name := range []string{"a", "z", "A", "Z", "0", "9", ".", "_", "-", "web.API-2_worker", strings.Repeat("a", 128)} {
		if !validComposeServiceName(name) {
			t.Errorf("supported name rejected %q", name)
		}
	}
	for _, name := range []string{"", strings.Repeat("a", 129), "@", "[", "`", "{", "/", ":", "!", "é", "服务", "has space", "line\nbreak"} {
		if validComposeServiceName(name) {
			t.Errorf("unsupported name accepted %q", name)
		}
	}
	for _, name := range []string{"API.v2_worker-0", "invalid/name"} {
		t.Run(fmt.Sprint(name), func(t *testing.T) {
			raw := "services:\n  " + name + ":\n    image: example.test/web:fixed\n"
			services, _, e := validateCompose(raw, false, nil, nil, nil)
			if name == "invalid/name" {
				if e == nil || len(services) != 0 {
					t.Fatal("invalid identity reached application plan", services, e)
				}
			} else if e != nil || len(services) != 1 || services[0].Name != name {
				t.Fatal("valid identity changed", services, e)
			}
		})
	}
}
