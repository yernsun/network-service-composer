// Package composecheck validates Compose structure and deployment storage policy.
// It has no runtime or persistence dependencies; callers provide fresh node evidence.
package composecheck

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

//go:embed compose-spec.json
var schemaSource []byte

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	var value any
	if err := json.Unmarshal(schemaSource, &value); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("compose_spec.json", value); err != nil {
		return nil, err
	}
	return c.Compile("compose_spec.json")
})

type Diagnostic struct {
	ID       string   `json:"id"`
	Code     string   `json:"code"`
	Severity string   `json:"severity"`
	Path     []string `json:"path"`
	Pointer  string   `json:"pointer"`
	Service  string   `json:"service,omitempty"`
	Resource string   `json:"resource,omitempty"`
	Line     int      `json:"line,omitempty"`
	Column   int      `json:"column,omitempty"`
	Actions  []string `json:"actions"`
	Reason   string   `json:"reason,omitempty"`
}

func Fingerprint(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func issue(code, severity, service string, path ...string) Diagnostic {
	if path == nil {
		path = []string{}
	}
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~", "~0"), "/", "~1")
	}
	pointer := ""
	if len(parts) > 0 {
		pointer = "/" + strings.Join(parts, "/")
	}
	return Diagnostic{ID: code + ":" + pointer, Code: code, Severity: severity, Service: service, Path: path, Pointer: pointer, Actions: []string{"LOCATE"}}
}

// Parse uses the unmodified official schema. Semantic restrictions are separate.
func Parse(raw string) (map[string]any, []Diagnostic) {
	var root yaml.Node
	d := yaml.NewDecoder(strings.NewReader(raw))
	err := d.Decode(&root)
	var document any
	if err == nil {
		err = root.Decode(&document)
	}
	var trailing any
	if err == nil && d.Decode(&trailing) != io.EOF {
		err = fmt.Errorf("multiple documents")
	}
	if err != nil && err != io.EOF {
		diagnostic := issue("YAML_SYNTAX_ERROR", "error", "")
		// Parser messages can include source values. Only expose the position.
		if err != nil {
			if match := regexp.MustCompile(`line (\d+):`).FindStringSubmatch(err.Error()); len(match) > 1 {
				diagnostic.Line, _ = strconv.Atoi(match[1])
				diagnostic.Column = 1
			}
		}
		return nil, []Diagnostic{diagnostic}
	}
	schema, err := compiledSchema()
	if err != nil {
		return nil, []Diagnostic{issue("COMPOSE_SCHEMA_UNAVAILABLE", "error", "")}
	}
	// JSON-compatible decoding also rejects non-string YAML map keys.
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, []Diagnostic{issue("COMPOSE_SCHEMA_ERROR", "error", "")}
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err = decoder.Decode(&value); err != nil {
		return nil, []Diagnostic{issue("COMPOSE_SCHEMA_ERROR", "error", "")}
	}
	if err = schema.Validate(value); err != nil {
		var diagnostics []Diagnostic
		var walk func(*jsonschema.ValidationError)
		walk = func(e *jsonschema.ValidationError) {
			if len(e.Causes) > 0 {
				for _, cause := range e.Causes {
					walk(cause)
				}
				return
			}
			service := ""
			if len(e.InstanceLocation) > 1 && e.InstanceLocation[0] == "services" {
				service = e.InstanceLocation[1]
			}
			x := issue("COMPOSE_SCHEMA_ERROR", "error", service, e.InstanceLocation...)
			x.Reason = strings.Join(e.ErrorKind.KeywordPath(), "/")
			locate(&root, &x)
			diagnostics = append(diagnostics, x)
		}
		if validation, ok := err.(*jsonschema.ValidationError); ok {
			walk(validation)
		}
		if len(diagnostics) == 0 {
			diagnostics = append(diagnostics, issue("COMPOSE_SCHEMA_ERROR", "error", ""))
		}
		return nil, diagnostics
	}
	doc, _ := document.(map[string]any)
	return doc, nil
}

func locate(root *yaml.Node, d *Diagnostic) {
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, key := range d.Path {
		if n.Kind == yaml.AliasNode {
			n = n.Alias
		}
		if n.Kind == yaml.SequenceNode {
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(n.Content) {
				break
			}
			n = n.Content[index]
			continue
		}
		if n.Kind != yaml.MappingNode {
			break
		}
		found := false
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				n = n.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	d.Line = n.Line
	d.Column = n.Column
}
