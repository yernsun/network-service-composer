package application

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func composeFixture(t *testing.T, service map[string]any, extra map[string]any) string {
	t.Helper()
	document := map[string]any{"services": map[string]any{"web": service}}
	for key, value := range extra {
		document[key] = value
	}
	raw, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func TestLongSyntaxPortsUseDefaultTCPAndNeverGuessAmbiguousTarget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ports, expose []any
		want          int
		certain       bool
	}{
		{"default protocol", []any{map[string]any{"target": 8080, "published": 80}}, nil, 8080, true},
		{"explicit empty protocol", []any{map[string]any{"target": 8080, "protocol": ""}}, nil, 8080, true},
		{"explicit TCP", []any{map[string]any{"target": 8080, "protocol": "tcp"}}, nil, 8080, true},
		{"UDP excluded", []any{map[string]any{"target": 53, "protocol": "udp"}}, nil, 0, false},
		{"string UDP excluded", []any{"53:53/udp"}, nil, 0, false},
		{"duplicate target", []any{"80:8080/tcp"}, []any{8080}, 8080, true},
		{"multiple targets", []any{8080, 8443}, nil, 0, false},
		{"port range", []any{"8000-8002"}, nil, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := map[string]any{"image": "example.test/web:fixed"}
			if tc.ports != nil {
				service["ports"] = tc.ports
			}
			if tc.expose != nil {
				service["expose"] = tc.expose
			}
			services, _, err := validateCompose(composeFixture(t, service, nil), false, nil, nil, nil)
			if err != nil || len(services) != 1 || services[0].TargetPort != tc.want || services[0].PortCertain != tc.certain {
				t.Fatalf("port inference=%#v %v, expected %d/certain=%t", services, err, tc.want, tc.certain)
			}
		})
	}
}

func TestComposeRejectsMalformedAndForbiddenServiceFields(t *testing.T) {
	for _, tc := range []struct {
		field string
		value any
		want  string
	}{
		{"image", 12, "must use image"}, {"image", " ", "must use image"}, {"image", "image\nother", "must use image"},
		{"unknown", true, "unsupported field"}, {"command", "cat /var/run/docker.sock", "Docker socket"}, {"command", "//./pipe/docker_engine", "Docker socket"},
		{"volumes", "volume:/data", "volumes must be a list"}, {"volumes", []any{42}, "invalid volume"},
		{"volumes", []any{"/proc:/data"}, "host system interfaces"}, {"volumes", []any{"undeclared:/data"}, "declared named volume"},
		{"volumes", []any{map[string]any{"type": "bind", "source": "/host"}}, "absolute paths"},
		{"volumes", []any{map[string]any{"type": "volume", "source": "undeclared"}}, "declared named volume"},
		{"environment", 42, "mapping or list"}, {"environment", map[string]any{"BAD-KEY": "value"}, "invalid environment key"},
		{"environment", map[string]any{"KEY": nil}, "explicit value"}, {"environment", []any{42}, "cannot inherit"},
		{"environment", []any{"INHERITED"}, "cannot inherit"}, {"environment", []any{"BAD-KEY=value"}, "invalid environment key"},
		{"ports", 80, "must be a list"}, {"ports", []any{0}, "invalid port"}, {"ports", []any{65536}, "invalid port"},
		{"ports", []any{"invalid"}, "invalid port"}, {"ports", []any{map[string]any{}}, "valid target"},
		{"ports", []any{map[string]any{"target": 80, "protocol": "sctp"}}, "invalid protocol"}, {"ports", []any{false}, "invalid entry"},
		{"expose", []any{map[string]any{"target": 80}}, "mapping syntax"},
		{"networks", 42, "list or mapping"}, {"networks", []any{42}, "entries must be names"}, {"networks", []any{" "}, "entries must be names"},
		{"networks", map[string]any{"default": 42}, "options must be a mapping"}, {"networks", []any{"unknown"}, "undeclared network"},
		{"secrets", "name", "must be a list"}, {"secrets", []any{42}, "invalid secrets entry"}, {"secrets", []any{" "}, "requires source"},
		{"secrets", []any{map[string]any{}}, "requires source"}, {"secrets", []any{"missing"}, "undeclared secret"},
		{"configs", []any{"missing"}, "undeclared config"},
		{"deploy", 42, "must be a mapping"}, {"deploy", map[string]any{"unknown": true}, "unsupported field"},
		{"deploy", map[string]any{"replicas": "one"}, "non-negative integer"}, {"deploy", map[string]any{"replicas": -1}, "non-negative integer"},
		{"deploy", map[string]any{"placement": 42}, "placement must be a mapping"}, {"deploy", map[string]any{"restart_policy": 42}, "restart_policy must be a mapping"},
		{"deploy", map[string]any{"rollback_config": 42}, "rollback_config must be a mapping"}, {"deploy", map[string]any{"update_config": 42}, "update_config must be a mapping"},
		{"deploy", map[string]any{"resources": 42}, "resources must be a mapping"},
		{"deploy", map[string]any{"resources": map[string]any{"reservations": 42}}, "reservations must be a mapping"},
		{"deploy", map[string]any{"resources": map[string]any{"limits": 42}}, "limits must be a mapping"},
		{"deploy", map[string]any{"resources": map[string]any{"reservations": map[string]any{"devices": []any{}}}}, "forbidden devices"},
	} {
		t.Run(tc.field+"/"+tc.want, func(t *testing.T) {
			service := map[string]any{"image": "example.test/web:fixed", tc.field: tc.value}
			_, _, err := validateCompose(composeFixture(t, service, nil), false, nil, nil, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsafe Compose accepted or misdiagnosed: %v", err)
			}
		})
	}
	for _, field := range []string{"build", "container_name", "privileged", "devices", "device_cgroup_rules", "cap_add", "security_opt", "sysctls", "network_mode", "pid", "ipc", "uts", "userns_mode", "cgroup"} {
		_, _, err := validateCompose(composeFixture(t, map[string]any{"image": "example.test/web:fixed", field: true}, nil), false, nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "forbidden field "+field) {
			t.Fatalf("forbidden %s accepted: %v", field, err)
		}
	}
}

func TestComposeResourceDeclarationsAndSupportedForms(t *testing.T) {
	for _, field := range []string{"volumes", "networks", "secrets", "configs"} {
		for _, value := range []any{42, map[string]any{"resource": 42}} {
			_, _, err := validateCompose(composeFixture(t, map[string]any{"image": "image:fixed"}, map[string]any{field: value}), false, nil, nil, nil)
			if err == nil || !strings.Contains(err.Error(), "mapping") {
				t.Fatalf("invalid %s definition accepted: %v", field, err)
			}
		}
	}
	for _, tc := range []struct {
		field      string
		definition any
		want       string
	}{
		{"volumes", map[string]any{"driver_opts": map[string]any{}}, "driver_opts"}, {"volumes", map[string]any{"driver_opts": map[string]any{"type": "nfs"}}, "driver_opts"},
		{"networks", map[string]any{"driver_opts": map[string]any{}}, "driver_opts"}, {"networks", map[string]any{"driver": "host"}, "unsupported driver"},
		{"secrets", map[string]any{"file": "/host/secret"}, "file"}, {"configs", map[string]any{"file": "/host/config"}, "file"},
		{"secrets", nil, "external resource"}, {"configs", map[string]any{}, "external resource"},
	} {
		_, _, err := validateCompose(composeFixture(t, map[string]any{"image": "image:fixed"}, map[string]any{tc.field: map[string]any{"resource": tc.definition}}), false, nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("unsafe resource accepted: %v", err)
		}
	}
	service := map[string]any{"image": "image:fixed", "environment": []any{"KEY=value"}, "volumes": []any{"/anonymous", "data:/data", map[string]any{"type": "volume", "source": "data"}, map[string]any{"type": "volume", "source": ""}}, "networks": map[string]any{"default": nil, "private": map[string]any{}}, "secrets": []any{"secret", map[string]any{"source": "secret"}}, "configs": []any{map[string]any{"source": "config"}}, "deploy": map[string]any{"replicas": 0, "resources": map[string]any{"limits": map[string]any{}, "reservations": map[string]any{}}, "placement": map[string]any{}, "restart_policy": map[string]any{}, "update_config": map[string]any{}, "rollback_config": map[string]any{}}}
	extra := map[string]any{"volumes": map[string]any{"data": map[string]any{"driver": "local"}}, "networks": map[string]any{"private": map[string]any{"driver": "overlay"}}, "secrets": map[string]any{"secret": map[string]any{"external": true}}, "configs": map[string]any{"config": map[string]any{"external": true, "name": "external-config"}}}
	services, issues, err := validateCompose(composeFixture(t, service, extra), false, []string{"secret"}, []string{"external-config"}, nil)
	if err != nil || len(services) != 1 || len(issues) != 0 {
		t.Fatalf("supported explicit resources rejected: %#v %#v %v", services, issues, err)
	}
	service["environment"] = map[string]any{"KEY": ""}
	service["networks"] = []any{"default", "private"}
	extra["volumes"] = map[string]any{"data": nil}
	extra["networks"] = map[string]any{"private": nil}
	if _, _, err := validateCompose(composeFixture(t, service, extra), false, []string{"secret"}, []string{"external-config"}, nil); err != nil {
		t.Fatalf("supported shorthand resources rejected: %v", err)
	}
	for _, raw := range []string{"services: {}", "services: []", "services:\n  web: null", "services:\n  web: {}", "services:\n  web: {image: image:fixed}\nunknown: true", "services:\n  web: {image: image:fixed}\n---\n["} {
		if _, _, err := validateCompose(raw, false, nil, nil, nil); err == nil {
			t.Fatalf("malformed document accepted: %s", raw)
		}
	}
}

func TestPortRangeAndHostPathBoundaries(t *testing.T) {
	for _, port := range []string{"1", "65535", "8000-8002", "8000-8000", "127.0.0.1:8080:80/tcp", "53/udp"} {
		if !validPortTarget(port) {
			t.Errorf("valid port rejected: %s", port)
		}
	}
	for _, port := range []string{"", "<nil>", "80/sctp", "1-2-3", "bad", "0", "65536", "1-bad", "2-1", "1-65536"} {
		if validPortTarget(port) {
			t.Errorf("invalid port accepted: %s", port)
		}
	}
	for _, path := range []string{"/host", "./host", "~/host", "name..escape", `C:\host`, "C:/host"} {
		if !isHostPath(path) {
			t.Errorf("host path not detected: %s", path)
		}
	}
	for _, path := range []string{"data", "x", "a:b", "abc"} {
		if isHostPath(path) {
			t.Errorf("named volume treated as bind mount: %s", path)
		}
	}
	got := inferPorts(map[string]any{"ports": []any{0, 65536, 42, true, "not-a-port", map[string]any{"target": 43, "protocol": "tcp"}}})
	if !reflect.DeepEqual(got, []int{42, 43}) {
		t.Fatalf("port inference included invalid entries: %v", got)
	}
	for _, name := range []string{"", "bad/name", strings.Repeat("a", 129)} {
		if validComposeServiceName(name) {
			t.Fatalf("invalid service name accepted: %q", name)
		}
	}
}
