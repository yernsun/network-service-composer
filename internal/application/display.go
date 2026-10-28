package application

import (
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var credentialKey = regexp.MustCompile(`(?i)password|passwd|secret|token|credential|private.?key|api.?key`)
var credentialURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:]+:)[^@\s]+@`)

func IsCredentialKey(key string) bool { return credentialKey.MatchString(key) }

// MaskCredential keeps enough of a long value to identify it in an editor
// preview without returning the value itself. Short values reveal no characters.
func MaskCredential(value string) string {
	if value == "" {
		return ""
	}
	characters := []rune(value)
	if len(characters) < 8 {
		return "**"
	}
	return string(characters[:2]) + "**" + string(characters[len(characters)-2:])
}

// PreviewResolvedYAML is a display-only render. Validation and deployment must
// use the original values; this output must never become executable input.
func PreviewResolvedYAML(source string, values map[string]string, secretKeys []string) (string, error) {
	secrets := make(map[string]bool, len(secretKeys))
	for _, key := range secretKeys {
		secrets[key] = true
	}
	maskedTokens := make(map[string]bool, len(secretKeys))
	secretValues := make([]string, 0, len(secretKeys))
	for key, value := range values {
		if secrets[key] || credentialKey.MatchString(key) {
			maskedTokens[MaskCredential(value)] = true
			if value != "" {
				secretValues = append(secretValues, value)
			}
			secrets[key] = true
		}
	}
	sort.Slice(secretValues, func(i, j int) bool { return len(secretValues[i]) > len(secretValues[j]) })
	resolved, err := Interpolate(source, values)
	if err != nil {
		return "", err
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(resolved), &document); err != nil {
		return "", err
	}
	var sanitize func(*yaml.Node)
	sanitize = func(node *yaml.Node) {
		node.HeadComment, node.LineComment, node.FootComment = "", "", ""
		if node.Kind == yaml.ScalarNode || node.Kind == yaml.AliasNode {
			for _, secret := range secretValues {
				if strings.Contains(node.Value, secret) {
					node.Value = strings.ReplaceAll(node.Value, secret, MaskCredential(secret))
					if node.Kind == yaml.ScalarNode {
						node.Tag = "!!str"
					}
				}
			}
		}
		for _, secret := range secretValues {
			node.Anchor = strings.ReplaceAll(node.Anchor, secret, MaskCredential(secret))
			if !strings.HasPrefix(node.Tag, "!!") {
				node.Tag = strings.ReplaceAll(node.Tag, secret, MaskCredential(secret))
			}
		}
		if node.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(node.Content); index += 2 {
				key, value := node.Content[index], node.Content[index+1]
				sanitize(key)
				if key.Value == "environment" {
					sanitize(value)
					maskEnvironmentEntries(value, secrets, maskedTokens)
				} else if credentialKey.MatchString(key.Value) && value.Kind == yaml.ScalarNode {
					sanitize(value)
					if !maskedTokens[value.Value] {
						value.Value, value.Tag = MaskCredential(value.Value), "!!str"
						maskedTokens[value.Value] = true
					}
				} else {
					sanitize(value)
				}
			}
			return
		}
		if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
			node.Value = credentialURL.ReplaceAllStringFunc(node.Value, func(value string) string {
				at := strings.LastIndexByte(value, '@')
				colon := strings.LastIndexByte(value[:at], ':')
				password := value[colon+1 : at]
				if maskedTokens[password] {
					return value
				}
				return value[:colon+1] + MaskCredential(password) + "@"
			})
		}
		for _, child := range node.Content {
			sanitize(child)
		}
	}
	sanitize(&document)
	result, err := yaml.Marshal(&document)
	return string(result), err
}

func maskEnvironmentEntries(node *yaml.Node, secrets, maskedTokens map[string]bool) {
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if (secrets[key.Value] || credentialKey.MatchString(key.Value)) && value.Kind == yaml.ScalarNode && !maskedTokens[value.Value] {
				value.Value, value.Tag = MaskCredential(value.Value), "!!str"
			}
		}
	}
	if node.Kind == yaml.SequenceNode {
		for _, value := range node.Content {
			key, raw, found := strings.Cut(value.Value, "=")
			if found && (secrets[key] || credentialKey.MatchString(key)) && !maskedTokens[raw] {
				value.Value, value.Tag = key+"="+MaskCredential(raw), "!!str"
			}
		}
	}
}

// RedactComposeDisplay is for ordinary review only. Source downloads use the
// separate confidential permission. Never feed this text into execution.
func RedactComposeDisplay(source string, secretValues ...string) string {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(source), &node); err != nil {
		return "[redacted preview unavailable]"
	}
	var visit func(*yaml.Node)
	visit = func(n *yaml.Node) {
		// Ordinary diffs never expose comments, extension payloads or command
		// literals, which may carry credentials outside environment mappings.
		n.HeadComment, n.LineComment, n.FootComment = "", "", ""
		if n.Kind == yaml.AliasNode {
			var value any
			if n.Decode(&value) != nil || n.Encode(value) != nil {
				_ = n.Encode("[REDACTED]")
				return
			}
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				n.Content[i].HeadComment, n.Content[i].LineComment, n.Content[i].FootComment = "", "", ""
				key, value := n.Content[i].Value, n.Content[i+1]
				value.HeadComment, value.LineComment, value.FootComment = "", "", ""
				if key == "environment" {
					var values map[string]any
					if value.Kind == yaml.MappingNode {
						values = map[string]any{}
						for j := 0; j < len(value.Content); j += 2 {
							values[value.Content[j].Value] = "[REDACTED]"
						}
					} else if value.Kind == yaml.SequenceNode {
						values = map[string]any{}
						for _, entry := range value.Content {
							name, _, _ := strings.Cut(entry.Value, "=")
							values[name] = "[REDACTED]"
						}
					}
					if values != nil {
						_ = value.Encode(values)
						continue
					}
				}
				if credentialKey.MatchString(key) || strings.HasPrefix(key, "x-") || key == "command" || key == "entrypoint" {
					_ = value.Encode("[REDACTED]")
					continue
				}
				visit(value)
				visit(n.Content[i])
			}
			return
		}
		if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
			n.Value = credentialURL.ReplaceAllString(n.Value, "${1}[REDACTED]@")
		}
		if n.Kind == yaml.ScalarNode {
			for _, secret := range secretValues {
				if secret != "" && strings.Contains(n.Value, secret) {
					// Mask the entire scalar: interpolation can put credentials in
					// arbitrary fields, including numeric values and mapping keys.
					n.Value, n.Tag = "[REDACTED]", "!!str"
					break
				}
			}
		}
		for _, child := range n.Content {
			visit(child)
		}
	}
	visit(&node)
	out, err := yaml.Marshal(&node)
	if err != nil {
		return "[redacted preview unavailable]"
	}
	return string(out)
}
