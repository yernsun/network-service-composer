package composecheck

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSharedFrontendConformance(t *testing.T) {
	raw, err := os.ReadFile("testdata/conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, YAML, Code string }
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			result := Analyze(tc.YAML, Options{Swarm: true})
			if tc.Code == "" {
				if !result.Deployable {
					t.Fatalf("unexpected diagnostics: %+v", result.Diagnostics)
				}
			} else if !has(result, tc.Code) {
				t.Fatalf("missing %s: %+v", tc.Code, result)
			}
		})
	}
}

func TestScaleOverrideRequiresNewAcknowledgement(t *testing.T) {
	raw := "services:\n  web:\n    image: nginx:1\n    volumes: [/data]\n    deploy:\n      placement:\n        constraints: ['node.hostname == worker-01']\n"
	options := Options{Swarm: true, ClusterChecked: true, Nodes: readyNodes}
	if !Analyze(raw, options).Deployable {
		t.Fatal("single replica rejected")
	}
	options.ReplicasOverride = map[string]int{"web": 3}
	result := Analyze(raw, options)
	if result.Deployable || !has(result, "SINGLE_NODE_STATEFUL_REPLICAS") {
		t.Fatalf("scale bypassed warning: %+v", result)
	}
	for _, d := range result.Diagnostics {
		options.Plan.Acknowledged = append(options.Plan.Acknowledged, d.ID)
	}
	if !Analyze(raw, options).Deployable {
		t.Fatal("reviewed replicas rejected")
	}
	options.ReplicasOverride["web"] = 4
	if Analyze(raw, options).Deployable {
		t.Fatal("old acknowledgement applied to new scale")
	}
}
