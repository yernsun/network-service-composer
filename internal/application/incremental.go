package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"reflect"
	"sort"
	"strings"
)

type ServiceChange struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Fields []string `json:"fields,omitempty"`
}

// ServiceDelta selects whole service definitions; omission is never deletion.
// Shared resource changes include all candidate services in the reviewed scope.
func ServiceDelta(base, candidate, mode string, force ...bool) (string, []ServiceChange, []string, error) {
	var previous, next map[string]any
	if base != "" {
		if err := yaml.Unmarshal([]byte(base), &previous); err != nil {
			return "", nil, nil, err
		}
	}
	if err := yaml.Unmarshal([]byte(candidate), &next); err != nil {
		return "", nil, nil, err
	}
	before, _ := previous["services"].(map[string]any)
	after, _ := next["services"].(map[string]any)
	if len(after) == 0 {
		return "", nil, nil, fmt.Errorf("services must not be empty")
	}
	shared := len(force) > 0 && force[0]
	expanded := false
	if shared && mode == "update" {
		// A changed runtime environment is shared by the Stack. Re-review all
		// existing complete definitions rather than changing omitted services
		// only on their next lifecycle operation.
		for name, value := range before {
			if _, exists := after[name]; !exists {
				after[name] = value
				expanded = true
			}
		}
	}
	for _, key := range []string{"networks", "volumes", "configs", "secrets"} {
		// A partial source need not repeat resources it does not use.
		old, _ := previous[key].(map[string]any)
		current, _ := next[key].(map[string]any)
		for name, definition := range current {
			if !reflect.DeepEqual(old[name], definition) {
				if _, exists := old[name]; exists {
					if mode == "create" {
						return "", nil, nil, fmt.Errorf("resource %q already exists with a different definition", name)
					}
					// Include complete deployed definitions for services affected by
					// a shared-resource edit so they appear in the frozen review.
					for service, value := range before {
						if _, submitted := after[service]; !submitted && serviceUsesResource(value, key, name) {
							after[service] = value
							expanded = true
						}
					}
				}
				shared = true
			}
		}
	}
	names := map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	changes := []ServiceChange{}
	selected := []string{}
	for _, name := range ordered {
		old, exists := before[name]
		value, submitted := after[name]
		kind := "retained"
		if submitted {
			if exists && mode == "create" {
				return "", nil, nil, fmt.Errorf("service %q already exists; use the update workflow", name)
			}
			kind = "unchanged"
			if !exists {
				kind = "added"
			} else if shared || !reflect.DeepEqual(old, value) {
				kind = "updated"
			}
			if kind == "added" || kind == "updated" {
				selected = append(selected, name)
			}
		}
		fields := []string{}
		if kind == "updated" {
			oldMap, _ := old.(map[string]any)
			newMap, _ := value.(map[string]any)
			keys := map[string]bool{}
			for key := range oldMap {
				keys[key] = true
			}
			for key := range newMap {
				keys[key] = true
			}
			for key := range keys {
				if !reflect.DeepEqual(oldMap[key], newMap[key]) {
					fields = append(fields, key)
				}
			}
			if shared {
				fields = append(fields, "shared_resources_or_variables")
			}
			sort.Strings(fields)
		}
		changes = append(changes, ServiceChange{Name: name, Kind: kind, Fields: fields})
	}
	if len(selected) == 0 {
		return "", changes, selected, nil
	}
	if expanded {
		merged, err := MergeServices(base, candidate)
		if err != nil {
			return "", nil, nil, err
		}
		candidate = merged
	}
	result, err := SelectServices(candidate, selected)
	return result, changes, selected, err
}

// SelectServices expands aliases before pruning; execution cannot retain an
// alias to a service which was deliberately omitted from this submission.
func SelectServices(source string, names []string) (string, error) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(source), &document); err != nil {
		return "", err
	}
	services, ok := document["services"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("services must be a mapping")
	}
	selected := map[string]any{}
	for _, name := range names {
		value, ok := services[name]
		if !ok {
			return "", fmt.Errorf("service %q is absent from the revision", name)
		}
		selected[name] = value
	}
	document["services"] = selected
	// Keep only resource definitions referenced by the submitted services.
	for _, section := range []string{"networks", "volumes", "configs", "secrets"} {
		definitions, _ := document[section].(map[string]any)
		used := map[string]bool{}
		for _, value := range selected {
			service, _ := value.(map[string]any)
			switch refs := service[section].(type) {
			case map[string]any:
				for key := range refs {
					used[key] = true
				}
			case []any:
				for _, ref := range refs {
					if m, ok := ref.(map[string]any); ok {
						key, _ := m["source"].(string)
						used[key] = true
					} else if key, ok := ref.(string); ok {
						if section == "volumes" {
							for i, c := range key {
								if c == ':' {
									key = key[:i]
									break
								}
							}
						}
						used[key] = true
					}
				}
			}
			if section == "networks" && service["networks"] == nil {
				used["default"] = true
			}
		}
		for key := range definitions {
			if !used[key] {
				delete(definitions, key)
			}
		}
		if len(definitions) == 0 {
			delete(document, section)
		}
	}
	var ast yaml.Node
	if err := yaml.Unmarshal([]byte(source), &ast); err != nil {
		return "", err
	}
	root := ast.Content[0]
	// Expand aliases before pruning their defining services, retaining comments
	// and ordering on every ordinary node.
	var expand func(*yaml.Node) error
	expand = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode {
			var value any
			if err := n.Decode(&value); err != nil {
				return err
			}
			return n.Encode(value)
		}
		for _, child := range n.Content {
			if err := expand(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := expand(root); err != nil {
		return "", err
	}
	for i := len(root.Content) - 2; i >= 0; i -= 2 {
		key := root.Content[i].Value
		if key != "services" && key != "networks" && key != "volumes" && key != "configs" && key != "secrets" {
			continue
		}
		keep, ok := document[key].(map[string]any)
		if !ok {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			continue
		}
		node := root.Content[i+1]
		for j := len(node.Content) - 2; j >= 0; j -= 2 {
			if _, ok := keep[node.Content[j].Value]; !ok {
				node.Content = append(node.Content[:j], node.Content[j+2:]...)
			}
		}
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&ast); err != nil {
		return "", err
	}
	return output.String(), nil
}

// MergeServices preserves untouched YAML nodes and historical sources. Each
// candidate service is a complete replacement, never a field-level patch.
func MergeServices(base, delta string) (string, error) {
	if base == "" {
		return delta, nil
	}
	var a, b yaml.Node
	if err := yaml.Unmarshal([]byte(base), &a); err != nil {
		return "", err
	}
	if err := yaml.Unmarshal([]byte(delta), &b); err != nil {
		return "", err
	}
	if len(a.Content) != 1 || len(b.Content) != 1 || a.Content[0].Kind != yaml.MappingNode || b.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("Compose must be a mapping")
	}
	// Detach candidate aliases: anchor names from independently authored sources
	// must not shadow anchors still used by an untouched service.
	var detach func(*yaml.Node) error
	detach = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode {
			var v any
			if err := n.Decode(&v); err != nil {
				return err
			}
			if err := n.Encode(v); err != nil {
				return err
			}
		}
		n.Anchor = ""
		for _, c := range n.Content {
			if err := detach(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := detach(b.Content[0]); err != nil {
		return "", err
	}
	root := a.Content[0]
	if json.Valid([]byte(base)) {
		var blockStyle func(*yaml.Node)
		blockStyle = func(n *yaml.Node) {
			if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
				n.Style = 0
			}
			if n.Kind == yaml.MappingNode {
				for i := 0; i < len(n.Content); i += 2 {
					n.Content[i].Style = 0
				}
			}
			for _, child := range n.Content {
				blockStyle(child)
			}
		}
		blockStyle(root)
	}
	// If a replacement removes an anchor definition, materialize only aliases
	// pointing into that replaced subtree. Unrelated anchors remain untouched.
	removed := map[*yaml.Node]bool{}
	var mark func(*yaml.Node)
	mark = func(n *yaml.Node) {
		removed[n] = true
		for _, child := range n.Content {
			mark(child)
		}
	}
	for i := 0; i < len(b.Content[0].Content); i += 2 {
		key, value := b.Content[0].Content[i], b.Content[0].Content[i+1]
		pos := nodeKey(root, key.Value)
		if pos < 0 {
			continue
		}
		target := root.Content[pos+1]
		if value.Kind == yaml.MappingNode && target.Kind == yaml.MappingNode {
			for j := 0; j < len(value.Content); j += 2 {
				if p := nodeKey(target, value.Content[j].Value); p >= 0 {
					mark(target.Content[p+1])
				}
			}
		} else {
			mark(target)
		}
	}
	var preserve func(*yaml.Node) error
	preserve = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode && removed[n.Alias] {
			return detach(n)
		}
		for _, child := range n.Content {
			if err := preserve(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := preserve(root); err != nil {
		return "", err
	}
	for i := 0; i < len(b.Content[0].Content); i += 2 {
		key, value := b.Content[0].Content[i], b.Content[0].Content[i+1]
		pos := nodeKey(root, key.Value)
		if pos < 0 {
			root.Content = append(root.Content, key, value)
			continue
		}
		if value.Kind == yaml.MappingNode && root.Content[pos+1].Kind == yaml.MappingNode {
			target := root.Content[pos+1]
			for j := 0; j < len(value.Content); j += 2 {
				p := nodeKey(target, value.Content[j].Value)
				if p < 0 {
					target.Content = append(target.Content, value.Content[j], value.Content[j+1])
				} else {
					target.Content[p+1] = value.Content[j+1]
				}
			}
		} else {
			root.Content[pos+1] = value
		}
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&a); err != nil {
		return "", err
	}
	return out.String(), nil
}

func serviceUsesResource(value any, section, name string) bool {
	service, _ := value.(map[string]any)
	refs := service[section]
	if refs == nil {
		return section == "networks" && name == "default"
	}
	if mapping, ok := refs.(map[string]any); ok {
		_, found := mapping[name]
		return found
	}
	if list, ok := refs.([]any); ok {
		for _, ref := range list {
			if mapping, ok := ref.(map[string]any); ok {
				if mapping["source"] == name {
					return true
				}
			}
			if source, ok := ref.(string); ok {
				if section == "volumes" {
					source, _, _ = strings.Cut(source, ":")
				}
				if source == name {
					return true
				}
			}
		}
	}
	return false
}
func nodeKey(node *yaml.Node, key string) int {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// An explicit replica edit replaces a previous scale override for that service.
// Image/environment edits keep scale overrides; other services are untouched.
func UpdatedReplicaOverrides(base, candidate string, existing map[string]int) (map[string]int, error) {
	var a, b map[string]any
	if err := yaml.Unmarshal([]byte(base), &a); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal([]byte(candidate), &b); err != nil {
		return nil, err
	}
	before, _ := a["services"].(map[string]any)
	after, _ := b["services"].(map[string]any)
	result := map[string]int{}
	for key, value := range existing {
		result[key] = value
	}
	replicas := func(value any) any {
		service, _ := value.(map[string]any)
		deploy, _ := service["deploy"].(map[string]any)
		return deploy["replicas"]
	}
	for name, value := range after {
		if !reflect.DeepEqual(replicas(before[name]), replicas(value)) {
			delete(result, name)
		}
	}
	return result, nil
}

func WithReplicaOverrides(raw string, overrides map[string]int) (string, error) {
	if len(overrides) == 0 {
		return raw, nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return "", err
	}
	services, _ := doc["services"].(map[string]any)
	for name, replicas := range overrides {
		service, ok := services[name].(map[string]any)
		if !ok {
			continue
		}
		deploy, _ := service["deploy"].(map[string]any)
		if deploy == nil {
			deploy = map[string]any{}
			service["deploy"] = deploy
		}
		deploy["replicas"] = replicas
	}
	out, err := yaml.Marshal(doc)
	return string(out), err
}

// ReplicaOverridesMatch detects even a stopped service's deferred scale changes,
// which do not change its current Docker service version.
func ReplicaOverridesMatch(names []string, current, observed map[string]int) bool {
	for _, name := range names {
		a, present := current[name]
		b, recorded := observed[name]
		if present != recorded || a != b {
			return false
		}
	}
	return true
}

// MergeScopedReplicaOverrides retains current values for unsubmitted services.
func MergeScopedReplicaOverrides(names []string, current, desired map[string]int) map[string]int {
	result := map[string]int{}
	for name, count := range current {
		result[name] = count
	}
	for _, name := range names {
		if count, exists := desired[name]; exists {
			result[name] = count
		} else {
			delete(result, name)
		}
	}
	return result
}
