package application

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestServiceDeltaPreservesOmittedServicesAndWholeDefinitions(t *testing.T) {
	base := "# original\nservices:\n  web: # keep\n    image: web:1\n    environment: {KEEP: yes}\n  api:\n    image: api:1\nvolumes:\n  unused: {}\n"
	next := "services:\n  hello:\n    image: hello:1\n    deploy: {replicas: 2}\n"
	delta, changes, names, err := ServiceDelta(base, next, "create")
	if err != nil || len(names) != 1 || names[0] != "hello" || len(changes) != 3 || strings.Contains(delta, "web:") {
		t.Fatalf("bad incremental scope: %v %v %v", names, changes, err)
	}
	merged, err := MergeServices(base, delta)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(merged, "# original") || !strings.Contains(merged, "# keep") || !strings.Contains(merged, "KEEP:") {
		t.Fatal("untouched source lost")
	}
	if _, _, _, err = ServiceDelta(base, "services: {web: {image: web:2}}", "create"); err == nil {
		t.Fatal("create overwrote existing service")
	}
	replacement, err := MergeServices(base, "services: {web: {image: web:2}}")
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err = yaml.Unmarshal([]byte(replacement), &tree); err != nil {
		t.Fatal(err)
	}
	services := tree["services"].(map[string]any)
	if services["web"].(map[string]any)["environment"] != nil || services["api"] == nil {
		t.Fatal("service replacement became field patch or removed another service")
	}
}
func TestSelectServicesExpandsAliasesAndPrunesResources(t *testing.T) {
	raw := "services:\n  old: &base\n    image: web:1\n    networks: [shared]\n  next:\n    <<: *base\nnetworks:\n  shared: {external: true}\n  unused: {}\n"
	selected, err := SelectServices(raw, []string{"next"})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err = yaml.Unmarshal([]byte(selected), &v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(selected, "old:") || strings.Contains(selected, "unused:") || !strings.Contains(selected, "image: web:1") {
		t.Fatal("invalid partial document")
	}
	if _, err = SelectServices(raw, []string{"missing"}); err == nil {
		t.Fatal("missing service accepted")
	}
}
func TestNetworkFreezeDoesNotMoveExistingServices(t *testing.T) {
	old, _ := DeploymentNetworks("services: {web: {image: web:1}}", "old-network")
	next, _ := DeploymentNetworks("services: {hello: {image: hello:1}}", "project")
	next, err := NamespaceApplicationNetwork(next, "project")
	if err != nil {
		t.Fatal(err)
	}
	merged, err := MergeServices(old, next)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	_ = yaml.Unmarshal([]byte(merged), &v)
	networks := v["networks"].(map[string]any)
	if networks[ApplicationNetworkKey].(map[string]any)["name"] != "old-network" || networks[ApplicationNetworkKey+"_project"].(map[string]any)["name"] != "project" {
		t.Fatal("incremental addition moved old network")
	}
	updated, _ := DeploymentNetworks("services: {web: {image: web:2}}", "")
	updated, err = PreserveManagedNetworks(merged, updated)
	if err != nil || !strings.Contains(updated, "old-network") || strings.Contains(updated, "name: project") {
		t.Fatalf("update inherited wrong network: %v", err)
	}
}

func TestIncrementalSharedResourcesAndAnchorReplacement(t *testing.T) {
	base := "services:\n  web: &web\n    image: web:1\n    networks: [shared]\n  api:\n    <<: *web\nnetworks:\n  shared: {external: true, name: old}\n"
	replaced, err := MergeServices(base, "services: {web: {image: web:2}}")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = yaml.Unmarshal([]byte(replaced), &document); err != nil {
		t.Fatal(err)
	}
	services := document["services"].(map[string]any)
	if services["api"].(map[string]any)["image"] != "web:1" {
		t.Fatal("retained alias changed meaning")
	}
	delta, changes, names, err := ServiceDelta(base, "services: {web: {image: web:2, networks: [shared]}}\nnetworks: {shared: {external: true, name: new}}", "update")
	if err != nil || len(names) != 2 || len(changes) != 2 || !strings.Contains(delta, "api:") {
		t.Fatal("shared-resource impact missing from review", names, err)
	}
	var expanded map[string]any
	if err = yaml.Unmarshal([]byte(delta), &expanded); err != nil {
		t.Fatal(err)
	}
	if expanded["services"].(map[string]any)["api"].(map[string]any)["image"] != "web:1" {
		t.Fatal("affected service was not expanded from its complete deployed definition")
	}

}
func TestIncrementalReplicaEditPreservesOtherScaleOverrides(t *testing.T) {
	base := "services: {web: {image: web:1, deploy: {replicas: 1}}, other: {image: other:1}}"
	imageOnly, err := UpdatedReplicaOverrides(base, "services: {web: {image: web:2, deploy: {replicas: 1}}}", map[string]int{"web": 2, "other": 4})
	if err != nil || imageOnly["web"] != 2 || imageOnly["other"] != 4 {
		t.Fatal("image edit reset scale")
	}
	changed, err := UpdatedReplicaOverrides(base, "services: {web: {image: web:1, deploy: {replicas: 3}}}", imageOnly)
	if err != nil || changed["web"] != 0 || changed["other"] != 4 {
		t.Fatal("replica edit did not replace only its override")
	}
	rendered, err := WithReplicaOverrides(base, imageOnly)
	if err != nil || !strings.Contains(rendered, "replicas: 2") || !strings.Contains(rendered, "replicas: 4") {
		t.Fatal("preview differs from execution overrides")
	}
}

func TestReviewDiffMasksCredentialsOutsideNamedSecretFields(t *testing.T) {
	raw := "# credential-in-comment\nx-env: &env {VALUE: extension-credential}\nservices:\n  web:\n    image: nginx:alpine\n    environment: *env\n    command: [serve, --password, command-credential]\n    labels: {url: 'https://user:url-credential@example.test'}\n"
	result := RedactComposeDisplay(raw)
	for _, secret := range []string{"credential-in-comment", "extension-credential", "command-credential", "url-credential"} {
		if strings.Contains(result, secret) {
			t.Fatal("ordinary diff leaked", secret)
		}
	}
	if !strings.Contains(result, "nginx:alpine") || !strings.Contains(result, "[REDACTED]") {
		t.Fatal("preview lost non-secret service context")
	}
}

func TestReviewDiffMasksResolvedSecretsInArbitraryScalars(t *testing.T) {
	raw := "services:\n  web:\n    image: nginx:alpine\n    labels:\n      ordinary: prefix-confidential-value-suffix\n      confidential-value: key\n      number: 724938\n"
	result := RedactComposeDisplay(raw, "confidential-value", "724938", "")
	for _, secret := range []string{"confidential-value", "724938"} {
		if strings.Contains(result, secret) {
			t.Fatal("resolved secret leaked outside environment")
		}
	}
	if !strings.Contains(result, "nginx:alpine") {
		t.Fatal("empty secret masked unrelated fields")
	}
}

func TestScopedScaleSnapshotPreservesConcurrentOtherServiceChanges(t *testing.T) {
	names := []string{"hello"}
	observed := map[string]int{"hello": 2}
	current := map[string]int{"hello": 2, "old": 7}
	if !ReplicaOverridesMatch(names, current, observed) {
		t.Fatal("unsubmitted scale change blocked review")
	}
	changed := MergeScopedReplicaOverrides(names, current, map[string]int{"old": 1})
	if _, exists := changed["hello"]; exists || changed["old"] != 7 {
		t.Fatal("review overwrote another service's scale")
	}
	current["hello"] = 3
	if ReplicaOverridesMatch(names, current, observed) {
		t.Fatal("stale deferred scale accepted")
	}
	if ReplicaOverridesMatch(names, map[string]int{"hello": 0}, nil) {
		t.Fatal("explicit stop confused with absent override")
	}
}
