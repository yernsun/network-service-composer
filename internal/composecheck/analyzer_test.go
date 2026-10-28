package composecheck

import (
	"strings"
	"testing"
)

const localCompose = "services:\n  mysql:\n    image: mysql:8.4\n    volumes:\n      - data:/var/lib/mysql\nvolumes:\n  data:\n"

var readyNodes = []Node{{ID: "one", Name: "worker-01", Role: "worker", State: "ready", Availability: "active", Labels: map[string]string{"storage.mysql": "true"}}, {ID: "two", Name: "worker-02", Role: "worker", State: "ready", Availability: "active", Labels: map[string]string{"storage.mysql": "true"}}}

func has(r Result, code string) bool {
	for _, d := range r.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestStorageAdmissionAndExplicitSharedDeclaration(t *testing.T) {
	if r := Analyze(localCompose, Options{}); !r.Deployable {
		t.Fatalf("standalone local storage blocked: %+v", r)
	}
	r := Analyze(localCompose, Options{Swarm: true})
	if r.Deployable || !has(r, "SWARM_LOCAL_VOLUME_WITHOUT_PLACEMENT") {
		t.Fatalf("local storage admitted: %+v", r)
	}
	m := r.Services[0].Mounts[0]
	p := Plan{Storage: []Declaration{{Resource: m.Key, Fingerprint: m.Fingerprint, Capability: "shared", Confirmed: true}}}
	if r = Analyze(localCompose, Options{Swarm: true, Plan: p}); !r.Deployable || !r.Services[0].Mounts[0].Declared {
		t.Fatalf("explicit shared declaration rejected: %+v", r)
	}
	p.Storage[0].Confirmed = false
	if Analyze(localCompose, Options{Swarm: true, Plan: p}).Deployable {
		t.Fatal("unchecked declaration bypassed admission")
	}
	p.Storage[0].Confirmed = true
	if Analyze(strings.ReplaceAll(localCompose, "data:/", "other:/"), Options{Swarm: true, Plan: p}).Deployable {
		t.Fatal("stale declaration bypassed admission")
	}
}

func TestUnknownMountsAndMixedServiceStorage(t *testing.T) {
	raw := strings.Replace(localCompose, "  data:\n", "  data:\n    external: true\n", 1)
	r := Analyze(raw, Options{Swarm: true})
	if !has(r, "STORAGE_CAPABILITY_UNKNOWN") || r.Deployable {
		t.Fatalf("external inferred shared: %+v", r)
	}
	p := Plan{Acknowledged: []string{r.Diagnostics[0].ID}}
	if Analyze(raw, Options{Swarm: true, Plan: p}).Deployable {
		t.Fatal("generic warning acknowledgement bypassed classification")
	}
	m := r.Services[0].Mounts[0]
	p.Storage = []Declaration{{Resource: m.Key, Fingerprint: m.Fingerprint, Capability: "shared", Confirmed: true}}
	raw = strings.Replace(raw, "      - data:/var/lib/mysql", "      - data:/var/lib/mysql\n      - type: bind\n        source: /backup/mysql\n        target: /backup\n      - type: tmpfs\n        target: /tmp", 1)
	r = Analyze(raw, Options{Swarm: true, Plan: p})
	if !has(r, "SWARM_BIND_MOUNT_WITHOUT_PLACEMENT") || len(r.Services[0].Mounts) != 3 {
		t.Fatalf("mixed mounts incorrectly admitted: %+v", r)
	}
}

func TestPlacementAndWarningsAreRechecked(t *testing.T) {
	raw := strings.Replace(localCompose, "    volumes:", "    deploy:\n      replicas: 3\n      placement:\n        constraints: ['node.labels.storage.mysql == true']\n    volumes:", 1)
	o := Options{Swarm: true, ClusterChecked: true, Nodes: readyNodes}
	r := Analyze(raw, o)
	if !has(r, "LOCAL_STORAGE_MULTIPLE_NODES") || !has(r, "SINGLE_NODE_STATEFUL_REPLICAS") || r.Deployable {
		t.Fatalf("missing advanced warnings: %+v", r)
	}
	for _, d := range r.Diagnostics {
		o.Plan.Acknowledged = append(o.Plan.Acknowledged, d.ID)
	}
	if r = Analyze(raw, o); !r.Deployable {
		t.Fatalf("current confirmation rejected: %+v", r)
	}
	o.Nodes = []Node{}
	if r = Analyze(raw, o); r.Deployable || !has(r, "SWARM_NO_MATCHING_NODE") {
		t.Fatalf("stale ready evidence admitted: %+v", r)
	}
	o.ClusterChecked = false
	if r = Analyze(raw, o); !has(r, "SWARM_NODE_CHECK_UNAVAILABLE") {
		t.Fatal("unknown evidence treated as zero/current")
	}
}

func TestSyntaxSchemaAndSemanticStages(t *testing.T) {
	for _, tc := range []struct{ raw, code string }{
		{"services:\n  web:\n    image: a:1\n    image: b:2\n", "YAML_SYNTAX_ERROR"},
		{localCompose + "---\nservices: {}\n", "YAML_SYNTAX_ERROR"},
		{"services:\n  web:\n    image: [wrong]\n", "COMPOSE_SCHEMA_ERROR"},
		{"services:\n  web:\n    image: a:1\n    deploy:\n      replicas: abc\n", "COMPOSE_REPLICAS_INVALID"},
		{strings.Replace(localCompose, "  data:\n", "  data:\n    driver_opts: {type: nfs}\n", 1), "NSC_VOLUME_DRIVER_OPTIONS_FORBIDDEN"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := Analyze(tc.raw, Options{Swarm: true})
			if r.Deployable || !has(r, tc.code) {
				t.Fatalf("stage mismatch: %+v", r)
			}
		})
	}
	r := Analyze("x-common: &common\n  image: nginx:1.29\nservices:\n  web:\n    <<: *common\n", Options{})
	if !r.Deployable {
		t.Fatalf("valid merged Compose rejected: %+v", r)
	}
}

func TestConstraintSemantics(t *testing.T) {
	for _, tc := range []struct {
		constraints []string
		count       int
		code        string
	}{
		{[]string{"node.hostname == worker-01", "node.role == worker"}, 1, ""},
		{[]string{"node.hostname != worker-01"}, 1, ""},
		{[]string{"node.labels.storage.mysql == true"}, 2, ""},
		{[]string{"node.hostname == worker-01", "node.hostname == worker-02"}, 0, "PLACEMENT_CONSTRAINT_CONFLICT"},
		{[]string{"node.hostname == worker-01", "node.hostname != worker-01"}, 0, "PLACEMENT_CONSTRAINT_CONFLICT"},
		{[]string{"node.hostname > worker-01"}, 0, "PLACEMENT_CONSTRAINT_INVALID"},
		{[]string{"arbitrary.key == x"}, 0, "PLACEMENT_CONSTRAINT_INVALID"},
	} {
		nodes, code := MatchConstraints(tc.constraints, readyNodes)
		if len(nodes) != tc.count || code != tc.code {
			t.Fatalf("%v: %v %s", tc.constraints, nodes, code)
		}
	}
	nodes := append([]Node{}, readyNodes...)
	nodes[0].Availability = "drain"
	nodes[1].State = "down"
	if matched, _ := MatchConstraints(nil, nodes); len(matched) != 0 {
		t.Fatal("unavailable node counted")
	}
}
