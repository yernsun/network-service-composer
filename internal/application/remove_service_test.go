package application

import (
	"strings"
	"testing"
)

func TestRemoveServiceRetainsOtherSourceAndVolumes(t *testing.T) {
	source := "services:\n  web:\n    image: nginx:1\n  db:\n    image: postgres:17\n    volumes: [data:/var/lib/postgresql/data]\nvolumes:\n  data: {}\n"
	result, err := RemoveService(source, "web")
	if err != nil || strings.Contains(result, "web:") || !strings.Contains(result, "db:") || !strings.Contains(result, "data:") {
		t.Fatalf("unexpected source: %s %v", result, err)
	}
	if _, err = RemoveService(source, "missing"); err == nil {
		t.Fatal("missing target accepted")
	}
	if _, err = RemoveService("services:\n  web:\n    image: nginx\n", "web"); err == nil {
		t.Fatal("last service deletion accepted")
	}
}
