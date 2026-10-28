package composecheck

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Declaration struct {
	Resource    string `json:"resource"`
	Fingerprint string `json:"fingerprint"`
	Capability  string `json:"capability"`
	Confirmed   bool   `json:"confirmed"`
}
type Plan struct {
	DataNodeID   string        `json:"data_node_id,omitempty"`
	Storage      []Declaration `json:"storage,omitempty"`
	Acknowledged []string      `json:"acknowledged,omitempty"`
}
type Mount struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	Capability  string `json:"capability"`
	Declared    bool   `json:"declared"`
	Index       int    `json:"index"`
}
type Service struct {
	Name         string   `json:"name"`
	Mounts       []Mount  `json:"mounts"`
	Constraints  []string `json:"constraints"`
	MatchedNodes []Node   `json:"matched_nodes"`
	Replicas     int      `json:"replicas"`
}
type Result struct {
	Diagnostics    []Diagnostic `json:"diagnostics"`
	Services       []Service    `json:"services"`
	Nodes          []Node       `json:"nodes"`
	Fingerprint    string       `json:"fingerprint"`
	SyntaxValid    bool         `json:"syntax_valid"`
	SchemaValid    bool         `json:"schema_valid"`
	ClusterChecked bool         `json:"cluster_checked"`
	Deployable     bool         `json:"deployable"`
}
type Options struct {
	Swarm            bool
	Plan             Plan
	Nodes            []Node
	ClusterChecked   bool
	ReplicasOverride map[string]int
}

func mapping(v any) map[string]any { x, _ := v.(map[string]any); return x }
func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func Analyze(raw string, options Options) Result {
	doc, diagnostics := Parse(raw)
	r := Result{Diagnostics: []Diagnostic{}, Services: []Service{}, Nodes: options.Nodes, ClusterChecked: options.ClusterChecked, Fingerprint: Fingerprint(raw)}
	if r.Nodes == nil {
		r.Nodes = []Node{}
	}
	if len(diagnostics) > 0 {
		r.Diagnostics = diagnostics
		r.SyntaxValid = diagnostics[0].Code != "YAML_SYNTAX_ERROR"
		return r
	}
	r.SyntaxValid = true
	r.SchemaValid = true
	r.Diagnostics = append(r.Diagnostics, PortDiagnostics(doc, options.Swarm)...)
	services := mapping(doc["services"])
	if len(services) == 0 {
		r.Diagnostics = append(r.Diagnostics, issue("COMPOSE_SERVICES_REQUIRED", "error", "", "services"))
	}
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	volumes := mapping(doc["volumes"])
	for _, name := range names {
		value := mapping(services[name])
		deploy := mapping(value["deploy"])
		if replicas, ok := options.ReplicasOverride[name]; ok {
			copy := map[string]any{}
			for key, value := range deploy {
				copy[key] = value
			}
			copy["replicas"] = replicas
			deploy = copy
		}
		placement := mapping(deploy["placement"])
		s := Service{Name: name, Mounts: []Mount{}, Constraints: []string{}, MatchedNodes: []Node{}, Replicas: 1}
		add := func(code, severity string, path ...string) {
			d := issue(code, severity, name, append([]string{"services", name}, path...)...)
			d.Actions = []string{"CONFIGURE_PLACEMENT", "LOCATE"}
			if severity == "warning" {
				nodeIDs := []string{}
				for _, node := range s.MatchedNodes {
					nodeIDs = append(nodeIDs, node.ID)
				}
				sort.Strings(nodeIDs)
				d.ID += ":" + Fingerprint(map[string]any{"volumes": value["volumes"], "deploy": deploy, "mounts": s.Mounts, "matched_nodes": nodeIDs})
			}
			r.Diagnostics = append(r.Diagnostics, d)
		}
		if replicas, exists := deploy["replicas"]; exists {
			n, err := strconv.Atoi(text(replicas))
			if err != nil || n < 0 {
				add("COMPOSE_REPLICAS_INVALID", "error", "deploy", "replicas")
			} else {
				s.Replicas = n
			}
		}
		for _, c := range slice(placement["constraints"]) {
			s.Constraints = append(s.Constraints, text(c))
		}
		for i, v := range slice(value["volumes"]) {
			m := Mount{Index: i, Type: "volume", Capability: "local"}
			if mountText, ok := v.(string); ok {
				parts := strings.Split(mountText, ":")
				if len(parts) == 1 {
					m.Target = parts[0]
				} else {
					m.Source = parts[0]
					m.Target = parts[1]
				}
				if strings.HasPrefix(m.Source, "/") || strings.HasPrefix(m.Source, ".") || strings.HasPrefix(m.Source, "~") || (len(m.Source) == 1 && len(parts) > 2 && strings.HasPrefix(parts[1], "\\")) {
					m.Type = "bind"
				}
			} else {
				mount := mapping(v)
				m.Type = text(mount["type"])
				m.Source = text(mount["source"])
				m.Target = text(mount["target"])
			}
			m.Key = "mount:" + name + ":" + strconv.Itoa(i)
			definition := map[string]any{"mount": v}
			if m.Type == "volume" && m.Source != "" {
				// Resource identifiers are opaque: interpolated volume names may
				// contain environment secrets, while declarations must round-trip.
				m.Key = "volume:" + Fingerprint(m.Source)
				consumers := []any{}
				for _, consumerName := range names {
					for _, mount := range slice(mapping(services[consumerName])["volumes"]) {
						source := text(mapping(mount)["source"])
						if short, ok := mount.(string); ok {
							source = strings.Split(short, ":")[0]
						}
						if source == m.Source {
							consumers = append(consumers, map[string]any{"service": consumerName, "mount": mount})
						}
					}
				}
				definition = map[string]any{"name": m.Source, "definition": volumes[m.Source], "consumers": consumers}
				volume := mapping(volumes[m.Source])
				driver := text(volume["driver"])
				if volume["external"] == true || (driver != "" && driver != "local") {
					m.Capability = "unknown"
				}
				if _, ok := volumes[m.Source]; !ok {
					add("COMPOSE_VOLUME_UNDECLARED", "error", "volumes", strconv.Itoa(i))
				}
				if _, ok := volume["driver_opts"]; ok {
					add("NSC_VOLUME_DRIVER_OPTIONS_FORBIDDEN", "error", "volumes", strconv.Itoa(i))
				}
			}
			if m.Type == "tmpfs" {
				m.Capability = "ephemeral"
			}
			m.Fingerprint = Fingerprint(definition)
			for _, d := range options.Plan.Storage {
				if d.Resource == m.Key && d.Fingerprint == m.Fingerprint && (d.Capability == "local" || (d.Capability == "shared" && d.Confirmed)) && m.Type != "tmpfs" {
					m.Capability = d.Capability
					m.Declared = true
					break
				}
			}
			s.Mounts = append(s.Mounts, m)
			if m.Capability == "unknown" && options.Swarm {
				add("STORAGE_CAPABILITY_UNKNOWN", "warning", "volumes", strconv.Itoa(i))
			}
		}
		if options.Swarm {
			local, bind := false, false
			for _, m := range s.Mounts {
				if m.Capability == "local" {
					local = true
					bind = bind || m.Type == "bind"
				}
			}
			if local && len(s.Constraints) == 0 {
				code := "SWARM_LOCAL_VOLUME_WITHOUT_PLACEMENT"
				if bind {
					code = "SWARM_BIND_MOUNT_WITHOUT_PLACEMENT"
				}
				add(code, "required", "deploy", "placement", "constraints")
			}
			if len(s.Constraints) > 0 {
				matched, code := MatchConstraints(s.Constraints, options.Nodes)
				if code != "" {
					add(code, "error", "deploy", "placement", "constraints")
				} else if !options.ClusterChecked {
					add("SWARM_NODE_CHECK_UNAVAILABLE", "error", "deploy", "placement", "constraints")
				} else if len(matched) == 0 {
					add("SWARM_NO_MATCHING_NODE", "error", "deploy", "placement", "constraints")
				} else {
					s.MatchedNodes = matched
					if local && len(matched) > 1 {
						add("LOCAL_STORAGE_MULTIPLE_NODES", "warning", "deploy", "placement", "constraints")
					}
				}
			}
			if local && s.Replicas > 1 {
				add("SINGLE_NODE_STATEFUL_REPLICAS", "warning", "deploy", "replicas")
			}
		}
		image := text(value["image"])
		if strings.HasSuffix(image, ":latest") || (image != "" && !strings.Contains(image, "@") && !strings.Contains(image[strings.LastIndex(image, "/")+1:], ":")) {
			add("IMAGE_TAG_LATEST", "warning", "image")
		}
		r.Services = append(r.Services, s)
	}
	r.Deployable = true
	for _, d := range r.Diagnostics {
		if d.Severity == "error" || d.Severity == "required" || d.Code == "STORAGE_CAPABILITY_UNKNOWN" || (d.Severity == "warning" && !contains(options.Plan.Acknowledged, d.ID)) {
			r.Deployable = false
		}
	}
	return r
}
func slice(value any) []any { s, _ := value.([]any); return s }
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func (r Result) Error() error {
	if r.Deployable {
		return nil
	}
	for _, d := range r.Diagnostics {
		if d.Severity == "error" || d.Severity == "required" {
			return fmt.Errorf("%s at %s", d.Code, d.Pointer)
		}
	}
	return fmt.Errorf("deployment warnings require current confirmation")
}

// NeedsNodes avoids contacting Docker for source-only validation or a service
// with no constraints. Invalid syntax is handled before this function is used.
func NeedsNodes(raw string) bool {
	doc, ds := Parse(raw)
	if len(ds) > 0 {
		return false
	}
	for _, s := range mapping(doc["services"]) {
		if len(slice(mapping(mapping(mapping(s)["deploy"])["placement"])["constraints"])) > 0 {
			return true
		}
	}
	return false
}
