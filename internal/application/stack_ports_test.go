package application

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStackPortNumbersPreservesResolvedMeaning(t *testing.T) {
	raw := `version: "3.8"
x-port: &port {target: '3306', published: '13306', protocol: tcp, mode: ingress}
services:
  mysql:
    image: mysql:8.4
    ports:
      - *port
      - {target: '53', published: '', protocol: udp}
      - {target: 80, published: 0}
      - '9000-9002:90-92'
    environment: {PORT_TEXT: '13306'}
`
	out, err := StackPortNumbers(raw)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	service := doc["services"].(map[string]any)["mysql"].(map[string]any)
	want := []any{map[string]any{"target": 3306, "published": 13306, "protocol": "tcp", "mode": "ingress"}, map[string]any{"target": 53, "protocol": "udp"}, map[string]any{"target": 80, "published": 0}, "9000-9002:90-92"}
	if !reflect.DeepEqual(service["ports"], want) || service["environment"].(map[string]any)["PORT_TEXT"] != "13306" || doc["x-port"].(map[string]any)["published"] != "13306" {
		t.Fatalf("changed unrelated values or failed Stack numeric conversion: %s", out)
	}
	for _, invalid := range []string{"[", "services: {db: {ports: [{target: '0'}]}}", "services: {db: {ports: [{target: '80', published: '65536'}]}}", "services: {db: {ports: [{target: 80, published: '-1'}]}}", "services: {db: {ports: [{target: 80, published: '8000-8010'}]}}"} {
		if _, err := StackPortNumbers(invalid); err == nil {
			t.Fatalf("invalid material converted: %s", invalid)
		}
	}
}
