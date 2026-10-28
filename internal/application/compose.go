package application

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yernsun/network-service-composer/internal/composecheck"
	"github.com/yernsun/network-service-composer/internal/model"
	"gopkg.in/yaml.v3"
)

type EnvironmentVariable struct {
	Key    string  `json:"key"`
	Value  *string `json:"value,omitempty"`
	Secret bool    `json:"secret,omitempty"`
	Clear  bool    `json:"clear,omitempty"`
}

// EnvironmentInput represents the two mutually exclusive public input modes.
// Pointers preserve the distinction between an omitted mode and an explicitly
// supplied empty raw file/list.
type EnvironmentInput struct {
	Raw    *string                `json:"raw,omitempty"`
	Values *[]EnvironmentVariable `json:"values,omitempty"`
}

type ManifestVariable struct {
	Key         string   `yaml:"key" json:"key"`
	Group       string   `yaml:"group" json:"group,omitempty"`
	Required    bool     `yaml:"required" json:"required"`
	Secret      bool     `yaml:"secret" json:"secret"`
	Generator   string   `yaml:"generator" json:"generator"`
	Default     string   `yaml:"default" json:"default,omitempty"`
	Pattern     string   `yaml:"pattern" json:"pattern,omitempty"`
	MinLength   int      `yaml:"min_length" json:"min_length,omitempty"`
	MaxLength   int      `yaml:"max_length" json:"max_length,omitempty"`
	Choices     []string `yaml:"choices" json:"choices,omitempty"`
	Description string   `yaml:"description" json:"description"`
}

type ManifestGroup struct {
	Name        string `yaml:"name" json:"name"`
	Label       string `yaml:"label" json:"label,omitempty"`
	Description string `yaml:"description" json:"description,omitempty"`
}

type ManifestDomain struct {
	Service    string        `yaml:"service" json:"service"`
	Host       string        `yaml:"host" json:"host,omitempty"`
	Path       string        `yaml:"path" json:"path,omitempty"`
	TargetPort int           `yaml:"target_port" json:"target_port,omitempty"`
	TLSMode    model.TLSMode `yaml:"tls_mode" json:"tls_mode,omitempty"`
}

type Manifest struct {
	Name               string             `yaml:"name" json:"name"`
	Description        string             `yaml:"description" json:"description"`
	Version            string             `yaml:"version" json:"version"`
	Groups             []ManifestGroup    `yaml:"groups" json:"groups,omitempty"`
	Variables          []ManifestVariable `yaml:"variables" json:"variables"`
	RecommendedDomains []ManifestDomain   `yaml:"recommended_domains" json:"recommended_domains,omitempty"`
}

type ServiceSpec struct {
	Name        string `json:"name"`
	TargetPort  int    `json:"target_port,omitempty"`
	PortCertain bool   `json:"port_certain"`
}

type PreflightInput struct {
	RestrictedHostAccess bool              `json:"-"`
	ComposeYAML          string            `json:"compose_yaml"`
	EnvExample           string            `json:"env_example,omitempty"`
	ManifestYAML         string            `json:"manifest,omitempty"`
	Environment          EnvironmentInput  `json:"environment"`
	Backend              model.RuntimeType `json:"backend"`
	Previous             map[string]string `json:"-"`
	PreviousSecretKeys   []string          `json:"-"`
	CatalogAnalysis      bool              `json:"-"`
	AllowedSecrets       []string          `json:"-"`
	AllowedConfigs       []string          `json:"-"`
	ProtectedNetworks    []string          `json:"-"`
	DataNodeID           string            `json:"-"`
	RuntimeNodeID        string            `json:"-"`
}

type PreflightResult struct {
	ComposeYAML         string            `json:"compose_yaml"`
	ResolvedYAML        string            `json:"resolved_yaml"`
	Environment         map[string]string `json:"environment"`
	EnvironmentKeys     []string          `json:"environment_keys"`
	SecretKeys          []string          `json:"secret_keys,omitempty"`
	Services            []ServiceSpec     `json:"services"`
	ComposeSHA256       string            `json:"compose_sha256"`
	EnvironmentSHA256   string            `json:"environment_sha256"`
	ResolvedSHA256      string            `json:"resolved_sha256"`
	ComposeCompatible   bool              `json:"compose_compatible"`
	SwarmCompatible     bool              `json:"swarm_compatible"`
	CompatibilityIssues []string          `json:"compatibility_issues,omitempty"`
	RecommendedDomains  []ManifestDomain  `json:"recommended_domains,omitempty"`
}

func Preflight(input PreflightInput) (PreflightResult, error) {
	composeText := normalizeNewlines(input.ComposeYAML)
	if strings.TrimSpace(composeText) == "" {
		return PreflightResult{}, fmt.Errorf("compose_yaml is required")
	}
	defaults, err := ParseDotEnv(input.EnvExample)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("parse .env.example: %w", err)
	}
	environment, inputSecrets, err := normalizeEnvironment(input.Environment, defaults, input.Previous, input.PreviousSecretKeys)
	if err != nil {
		return PreflightResult{}, err
	}
	manifest, err := parseManifest(input.ManifestYAML)
	if err != nil {
		return PreflightResult{}, err
	}
	if err = validateManifest(manifest); err != nil {
		return PreflightResult{}, err
	}
	secretSet := make(map[string]struct{}, len(inputSecrets)+len(manifest.Variables))
	for _, key := range inputSecrets {
		secretSet[key] = struct{}{}
	}
	for _, key := range input.PreviousSecretKeys {
		if _, exists := environment[key]; exists {
			secretSet[key] = struct{}{}
		}
	}
	for _, variable := range manifest.Variables {
		if !validEnvironmentKey(variable.Key) {
			return PreflightResult{}, fmt.Errorf("manifest variable %q has an invalid name", variable.Key)
		}
		value, exists := environment[variable.Key]
		if (!exists || value == "") && variable.Default != "" {
			environment[variable.Key] = variable.Default
			value, exists = variable.Default, true
		}
		if input.CatalogAnalysis && (!exists || value == "") && (variable.Required || variable.Generator != "") {
			environment[variable.Key] = "nsc-catalog-placeholder"
			value, exists = environment[variable.Key], true
		} else if (!exists || value == "") && variable.Generator != "" {
			generated, generateErr := generateValue(variable.Generator)
			if generateErr != nil {
				return PreflightResult{}, fmt.Errorf("generate %s: %w", variable.Key, generateErr)
			}
			environment[variable.Key] = generated
			value, exists = generated, true
		}
		if variable.Required && (!exists || value == "") {
			return PreflightResult{}, fmt.Errorf("required environment variable %s is missing", variable.Key)
		}
		if variable.Secret {
			secretSet[variable.Key] = struct{}{}
		}
		if !input.CatalogAnalysis && (value != "" || variable.Required) {
			if err := validateManifestVariable(variable, value); err != nil {
				return PreflightResult{}, err
			}
		}
	}
	if input.CatalogAnalysis {
		for _, key := range requiredInterpolationKeys(composeText) {
			if environment[key] == "" {
				environment[key] = "nsc-catalog-placeholder"
			}
		}
	}
	resolved, err := Interpolate(composeText, environment)
	if err != nil {
		return PreflightResult{}, err
	}
	if _, diagnostics := composecheck.Parse(resolved); len(diagnostics) > 0 {
		return PreflightResult{}, fmt.Errorf("%s at %s", diagnostics[0].Code, diagnostics[0].Pointer)
	}
	services, swarmIssues, err := validateComposeForNode(resolved, input.CatalogAnalysis, input.AllowedSecrets, input.AllowedConfigs, input.ProtectedNetworks, input.RuntimeNodeID, input.DataNodeID)
	if err != nil {
		return PreflightResult{}, err
	}
	if input.RestrictedHostAccess {
		if err := validateScopedStorage(resolved); err != nil {
			return PreflightResult{}, err
		}
	}
	if input.Backend == model.RuntimeSwarm && len(swarmIssues) > 0 {
		return PreflightResult{}, fmt.Errorf("compose file is not Swarm compatible: %s", strings.Join(swarmIssues, "; "))
	}
	if err = validateRecommendedDomains(manifest.RecommendedDomains, services); err != nil {
		return PreflightResult{}, err
	}
	keys := sortedKeys(environment)
	secretKeys := make([]string, 0, len(secretSet))
	for key := range secretSet {
		secretKeys = append(secretKeys, key)
	}
	sort.Strings(secretKeys)
	canonicalEnv := RenderDotEnv(environment)
	result := PreflightResult{
		ComposeYAML: composeText, ResolvedYAML: resolved, Environment: environment, EnvironmentKeys: keys,
		SecretKeys: secretKeys, Services: services, ComposeSHA256: digest(composeText),
		EnvironmentSHA256: digest(canonicalEnv), ResolvedSHA256: digest(resolved), ComposeCompatible: true,
		SwarmCompatible: len(swarmIssues) == 0, CompatibilityIssues: swarmIssues,
		RecommendedDomains: manifest.RecommendedDomains,
	}
	return result, nil
}

func RedactEnvironment(values map[string]string, secretKeys []string) map[string]string {
	secret := make(map[string]struct{}, len(secretKeys))
	for _, key := range secretKeys {
		secret[key] = struct{}{}
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		if _, ok := secret[key]; ok {
			result[key] = "[redacted]"
		} else {
			result[key] = value
		}
	}
	return result
}

// RedactResolvedYAML rebuilds the preview with write-only variables replaced
// before interpolation. This avoids leaking a secret through the resolved
// Compose preview even though the environment map itself is redacted.
func RedactResolvedYAML(composeYAML string, values map[string]string, secretKeys []string) string {
	redacted := cloneMap(values)
	for _, key := range secretKeys {
		if _, exists := redacted[key]; exists {
			redacted[key] = "[redacted]"
		}
	}
	value, err := Interpolate(composeYAML, redacted)
	if err != nil {
		return "[redacted preview unavailable]"
	}
	return value
}

func ParseDotEnv(raw string) (map[string]string, error) {
	return parseDotEnvWithLookup(raw, nil)
}

// parseDotEnvWithLookup applies Compose dotenv interpolation against only the
// supplied values and variables defined earlier in the same file. It never
// consults the Controller process environment.
func parseDotEnvWithLookup(raw string, initial map[string]string) (map[string]string, error) {
	result := make(map[string]string)
	lookup := cloneMap(initial)
	scanner := bufio.NewScanner(strings.NewReader(normalizeNewlines(raw)))
	// Environment files can contain generated certificates or JSON values.
	scanner.Buffer(make([]byte, 4096), 4<<20)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		index := strings.IndexByte(line, '=')
		if index < 1 {
			return nil, fmt.Errorf("line %d must use KEY=VALUE", lineNumber)
		}
		key := strings.TrimSpace(line[:index])
		if !validEnvironmentKey(key) {
			return nil, fmt.Errorf("line %d contains invalid variable name %q", lineNumber, key)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("line %d duplicates variable %s", lineNumber, key)
		}
		rawValue := strings.TrimSpace(line[index+1:])
		for strings.HasPrefix(rawValue, "'") && findClosingQuote(rawValue, '\'') < 0 {
			if !scanner.Scan() {
				return nil, fmt.Errorf("line %d: unterminated single-quoted value", lineNumber)
			}
			lineNumber++
			rawValue += "\n" + scanner.Text()
		}
		value, interpolate, err := parseDotEnvValue(rawValue)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if interpolate {
			value, err = Interpolate(value, lookup)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNumber, err)
			}
		}
		result[key] = value
		lookup[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func parseDotEnvValue(value string) (string, bool, error) {
	if value == "" {
		return "", true, nil
	}
	if value[0] == '\'' {
		end := findClosingQuote(value, '\'')
		if end < 0 {
			return "", false, fmt.Errorf("unterminated single-quoted value")
		}
		if err := validateDotEnvQuoteSuffix(value[end+1:]); err != nil {
			return "", false, err
		}
		return strings.ReplaceAll(value[1:end], `\'`, `'`), false, nil
	}
	if value[0] == '"' {
		end := findClosingQuote(value, '"')
		if end < 0 {
			return "", false, fmt.Errorf("unterminated double-quoted value")
		}
		if err := validateDotEnvQuoteSuffix(value[end+1:]); err != nil {
			return "", false, err
		}
		decoded, err := strconv.Unquote(value[:end+1])
		if err != nil {
			return "", false, fmt.Errorf("invalid quoted value: %w", err)
		}
		return decoded, true, nil
	}
	// Unquoted comments require preceding whitespace, matching Docker dotenv.
	for i := 1; i < len(value); i++ {
		if value[i] == '#' && unicode.IsSpace(rune(value[i-1])) {
			return strings.TrimSpace(value[:i]), true, nil
		}
	}
	return value, true, nil
}

func findClosingQuote(value string, quote byte) int {
	for index := 1; index < len(value); index++ {
		if value[index] != quote {
			continue
		}
		backslashes := 0
		for previous := index - 1; previous >= 0 && value[previous] == '\\'; previous-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return index
		}
	}
	return -1
}

func validateDotEnvQuoteSuffix(value string) error {
	value = strings.TrimSpace(value)
	if value != "" && !strings.HasPrefix(value, "#") {
		return fmt.Errorf("unexpected text after quoted value")
	}
	return nil
}

func NormalizeEnvironment(input EnvironmentInput, defaults, previous map[string]string) (map[string]string, []string, error) {
	return normalizeEnvironment(input, defaults, previous, nil)
}

func normalizeEnvironment(input EnvironmentInput, defaults, previous map[string]string, previousSecretKeys []string) (map[string]string, []string, error) {
	if input.Raw != nil && input.Values != nil {
		return nil, nil, fmt.Errorf("raw and values environment modes are mutually exclusive")
	}
	result := cloneMap(defaults)
	var secrets []string
	if input.Raw != nil {
		values, err := parseDotEnvWithLookup(*input.Raw, defaults)
		if err != nil {
			return nil, nil, fmt.Errorf("parse raw environment: %w", err)
		}
		for key, value := range values {
			result[key] = value
		}
		return result, secrets, nil
	}
	if input.Values == nil {
		for key, value := range previous {
			result[key] = value
		}
		return result, secrets, nil
	}
	// Key/value mode is a patch. Values not represented by the editor must not
	// disappear, especially write-only secrets that the client cannot echo.
	for key, value := range previous {
		result[key] = value
	}
	seen := make(map[string]struct{}, len(*input.Values))
	previousSecrets := make(map[string]struct{}, len(previousSecretKeys))
	for _, key := range previousSecretKeys {
		previousSecrets[key] = struct{}{}
	}
	for _, item := range *input.Values {
		item.Key = strings.TrimSpace(item.Key)
		if !validEnvironmentKey(item.Key) {
			return nil, nil, fmt.Errorf("invalid environment variable name %q", item.Key)
		}
		if _, duplicate := seen[item.Key]; duplicate {
			return nil, nil, fmt.Errorf("duplicate environment variable %s", item.Key)
		}
		seen[item.Key] = struct{}{}
		if item.Clear {
			delete(result, item.Key)
			continue
		}
		if item.Secret {
			secrets = append(secrets, item.Key)
		}
		if item.Value == nil {
			if existing, ok := previous[item.Key]; ok {
				result[item.Key] = existing
				continue
			}
			return nil, nil, fmt.Errorf("environment variable %s requires a value", item.Key)
		}
		_, wasSecret := previousSecrets[item.Key]
		if *item.Value == "" && (item.Secret || wasSecret) {
			if existing, ok := previous[item.Key]; ok {
				result[item.Key] = existing
				continue
			}
		}
		result[item.Key] = *item.Value
	}
	return result, secrets, nil
}

func RenderDotEnv(values map[string]string) string {
	var result strings.Builder
	for _, key := range sortedKeys(values) {
		result.WriteString(key)
		result.WriteByte('=')
		// Canonical encrypted dotenv is parsed again during deployment. Escape
		// dollar signs so that pass restores the literal normalized value.
		result.WriteString(strconv.Quote(strings.ReplaceAll(values[key], "$", "$$")))
		result.WriteByte('\n')
	}
	return result.String()
}

func Interpolate(input string, environment map[string]string) (string, error) {
	var output strings.Builder
	for i := 0; i < len(input); {
		if input[i] != '$' {
			output.WriteByte(input[i])
			i++
			continue
		}
		if i+1 < len(input) && input[i+1] == '$' {
			// Resolve Compose's escape to the literal value. The deployment
			// materializer escapes the fully resolved document again before it is
			// handed to the CLI.
			output.WriteByte('$')
			i += 2
			continue
		}
		if i+1 >= len(input) || input[i+1] != '{' {
			end := i + 1
			if end < len(input) && ((input[end] >= 'A' && input[end] <= 'Z') || (input[end] >= 'a' && input[end] <= 'z') || input[end] == '_') {
				end++
				for end < len(input) && ((input[end] >= 'A' && input[end] <= 'Z') || (input[end] >= 'a' && input[end] <= 'z') || (input[end] >= '0' && input[end] <= '9') || input[end] == '_') {
					end++
				}
				output.WriteString(environment[input[i+1:end]])
				i = end
				continue
			}
			output.WriteByte('$')
			i++
			continue
		}
		end, depth := i+2, 1
		for end < len(input) && depth > 0 {
			if input[end] == '$' && end+1 < len(input) && input[end+1] == '{' {
				depth++
				end += 2
				continue
			}
			if input[end] == '}' {
				depth--
				if depth == 0 {
					break
				}
			}
			end++
		}
		if end >= len(input) || depth != 0 {
			return "", fmt.Errorf("unterminated environment interpolation at byte %d", i)
		}
		expression := input[i+2 : end]
		value, err := interpolateExpression(expression, environment)
		if err != nil {
			return "", err
		}
		output.WriteString(value)
		i = end + 1
	}
	return output.String(), nil
}

// EscapeForComposeCLI makes an already-resolved document inert to the second
// interpolation pass performed by docker compose / docker stack deploy.
func EscapeForComposeCLI(value string) string {
	return strings.ReplaceAll(value, "$", "$$")
}

// ComposeSectionDigests produces value-only hashes for upgrade previews. It
// intentionally returns no configuration values, so it is safe to expose next
// to a redacted resolved preview.
func ComposeSectionDigests(value string) (map[string]string, error) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(value), &document); err != nil {
		return nil, err
	}
	sections := map[string]any{
		"networks": document["networks"],
		"volumes":  document["volumes"],
		"services": document["services"],
	}
	deploy := make(map[string]any)
	if services, ok := stringMap(document["services"]); ok {
		for name, raw := range services {
			if service, serviceOK := stringMap(raw); serviceOK {
				if value, exists := service["deploy"]; exists {
					deploy[name] = value
				}
			}
		}
	}
	sections["deploy"] = deploy
	result := make(map[string]string, len(sections))
	for name, section := range sections {
		encoded, err := json.Marshal(section)
		if err != nil {
			return nil, err
		}
		result[name] = digest(string(encoded))
	}
	return result, nil
}

func interpolateExpression(expression string, environment map[string]string) (string, error) {
	key, operator, operand := expression, "", ""
	for _, candidate := range []string{":-", ":+", ":?", "-", "+", "?"} {
		if index := strings.Index(expression, candidate); index >= 0 {
			key, operator, operand = expression[:index], candidate, expression[index+len(candidate):]
			break
		}
	}
	if !validEnvironmentKey(key) {
		return "", fmt.Errorf("invalid interpolation variable %q", key)
	}
	value, set := environment[key]
	resolveOperand := func() (string, error) { return Interpolate(operand, environment) }
	switch operator {
	case "":
		if !set {
			return "", nil
		}
		return value, nil
	case ":-":
		if !set || value == "" {
			return resolveOperand()
		}
		return value, nil
	case "-":
		if !set {
			return resolveOperand()
		}
		return value, nil
	case ":+":
		if set && value != "" {
			return resolveOperand()
		}
		return "", nil
	case "+":
		if set {
			return resolveOperand()
		}
		return "", nil
	case ":?":
		if !set || value == "" {
			if operand == "" {
				operand = key + " is required"
			}
			return "", fmt.Errorf("%s", operand)
		}
		return value, nil
	case "?":
		if !set {
			if operand == "" {
				operand = key + " is required"
			}
			return "", fmt.Errorf("%s", operand)
		}
		return value, nil
	default:
		return "", fmt.Errorf("unsupported interpolation expression %q", expression)
	}
}

func validateCompose(raw string, catalogAnalysis bool, allowedSecrets, allowedConfigs, protectedNetworks []string) ([]ServiceSpec, []string, error) {
	return validateComposeForNode(raw, catalogAnalysis, allowedSecrets, allowedConfigs, protectedNetworks, "")
}

func validateComposeForNode(raw string, catalogAnalysis bool, allowedSecrets, allowedConfigs, protectedNetworks []string, nodeID string, dataNodes ...string) ([]ServiceSpec, []string, error) {
	var document map[string]any
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(&document); err != nil {
		return nil, nil, fmt.Errorf("decode Compose YAML: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, nil, fmt.Errorf("decode trailing Compose YAML: %w", err)
		}
		return nil, nil, fmt.Errorf("Compose YAML must contain exactly one document")
	}
	for key := range document {
		switch key {
		case "version", "name", "services", "volumes", "networks", "secrets", "configs":
		default:
			if strings.HasPrefix(key, "x-") {
				continue
			}
			return nil, nil, fmt.Errorf("Compose YAML uses unsupported top-level field %s", key)
		}
	}
	var swarmIssues []string
	if _, exists := document["name"]; exists {
		swarmIssues = append(swarmIssues, "top-level name is Compose-only")
	}
	servicesMap, ok := stringMap(document["services"])
	if !ok || len(servicesMap) == 0 {
		return nil, nil, fmt.Errorf("Compose YAML must define at least one service")
	}
	declaredVolumes, err := optionalMapping(document, "volumes")
	if err != nil {
		return nil, nil, err
	}
	for name, rawDefinition := range declaredVolumes {
		definition, definitionErr := composeResourceDefinition("volume", name, rawDefinition)
		if definitionErr != nil {
			return nil, nil, definitionErr
		}
		if _, exists := definition["driver_opts"]; exists {
			return nil, nil, fmt.Errorf("volume %q driver_opts are forbidden", name)
		}

	}
	networks, err := optionalMapping(document, "networks")
	if err != nil {
		return nil, nil, err
	}
	for name, rawDefinition := range networks {
		if name == "nsc_backend" || name == ApplicationNetworkKey {
			return nil, nil, fmt.Errorf("network key %q is reserved for managed networking", name)
		}
		definition, definitionErr := composeResourceDefinition("network", name, rawDefinition)
		if definitionErr != nil {
			return nil, nil, definitionErr
		}
		if _, exists := definition["driver_opts"]; exists {
			return nil, nil, fmt.Errorf("network %q driver_opts are forbidden", name)
		}
		if driver := strings.TrimSpace(fmt.Sprint(definition["driver"])); driver != "" && driver != "<nil>" && driver != "bridge" && driver != "overlay" {
			return nil, nil, fmt.Errorf("network %q uses unsupported driver %q", name, driver)
		} else if driver == "bridge" {
			swarmIssues = append(swarmIssues, fmt.Sprintf("network %s: bridge driver is not supported by Swarm stacks", name))
		}
		externalName := name
		if configuredName := strings.TrimSpace(fmt.Sprint(definition["name"])); configuredName != "" && configuredName != "<nil>" {
			externalName = configuredName
		}
		if external, _ := definition["external"].(bool); external && slices.Contains(protectedNetworks, externalName) {
			return nil, nil, fmt.Errorf("network %q is managed by NSC and cannot be declared by an application", externalName)
		}
	}
	allowedResources := map[string][]string{"secrets": allowedSecrets, "configs": allowedConfigs}
	for _, resource := range []string{"secrets", "configs"} {
		definitions, mappingErr := optionalMapping(document, resource)
		if mappingErr != nil {
			return nil, nil, mappingErr
		}
		for name, rawDefinition := range definitions {
			definition, definitionErr := composeResourceDefinition(strings.TrimSuffix(resource, "s"), name, rawDefinition)
			if definitionErr != nil {
				return nil, nil, definitionErr
			}
			if file, exists := definition["file"]; exists && fmt.Sprint(file) != "" {
				return nil, nil, fmt.Errorf("local %s file %q is forbidden", resource, name)
			}
			if external, _ := definition["external"].(bool); !external {
				return nil, nil, fmt.Errorf("%s %q must reference an administrator-provisioned external resource", resource, name)
			}
			externalName := name
			if configuredName := strings.TrimSpace(fmt.Sprint(definition["name"])); configuredName != "" && configuredName != "<nil>" {
				externalName = configuredName
			}
			if !catalogAnalysis && !slices.Contains(allowedResources[resource], externalName) {
				return nil, nil, fmt.Errorf("external %s %q is not configured by the administrator", resource, externalName)
			}
		}
	}
	allowed := map[string]bool{
		"image": true, "environment": true, "healthcheck": true, "volumes": true, "networks": true,
		"ports": true, "expose": true, "depends_on": true, "deploy": true, "command": true, "entrypoint": true,
		"labels": true, "restart": true, "stop_grace_period": true, "working_dir": true, "user": true,
		"hostname": true, "init": true, "read_only": true, "shm_size": true, "secrets": true,
		"configs": true, "pull_policy": true, "logging": true,
	}
	dangerous := map[string]bool{"build": true, "container_name": true, "privileged": true, "devices": true,
		"device_cgroup_rules": true, "cap_add": true, "security_opt": true, "sysctls": true, "network_mode": true,
		"pid": true, "ipc": true, "uts": true, "userns_mode": true, "cgroup": true}
	var services []ServiceSpec
	names := make([]string, 0, len(servicesMap))
	for name := range servicesMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !validComposeServiceName(name) {
			return nil, nil, fmt.Errorf("service name %q must use letters, numbers, dot, underscore, or dash", name)
		}
		definition, ok := stringMap(servicesMap[name])
		if !ok {
			return nil, nil, fmt.Errorf("service %s must be a mapping", name)
		}
		image, imageExists := definition["image"].(string)
		if !imageExists || strings.TrimSpace(image) == "" || strings.ContainsAny(image, "\r\n") {
			return nil, nil, fmt.Errorf("service %s must use image; source builds are not supported", name)
		}
		boundNode := nodeID
		if len(dataNodes) > 0 && dataNodes[0] != "" && dataNodes[0] != nodeID {
			if err := ValidateDataNodePolicy(raw, dataNodes[0], nil); err != nil {
				return nil, nil, err
			}
			boundNode = ""
		}
		restricted, err := restrictedToNode(definition["deploy"], boundNode)
		if err != nil {
			return nil, nil, fmt.Errorf("service %s: %w", name, err)
		}
		for key, value := range definition {
			if strings.HasPrefix(key, "x-") {
				continue
			}
			localField := restricted && (key == "container_name" || key == "extra_hosts")
			if dangerous[key] && !localField {
				return nil, nil, fmt.Errorf("service %s uses forbidden field %s", name, key)
			}
			if !allowed[key] && !localField {
				return nil, nil, fmt.Errorf("service %s uses unsupported field %s", name, key)
			}
			if containsDockerSocket(value) {
				return nil, nil, fmt.Errorf("service %s references the Docker socket", name)
			}
		}
		if err := validateLocalFields(definition, restricted); err != nil {
			return nil, nil, fmt.Errorf("service %s: %w", name, err)
		}
		if err := validateVolumesForNode(name, definition["volumes"], declaredVolumes, true); err != nil {
			return nil, nil, err
		}
		if err := validateServiceEnvironment(name, definition["environment"]); err != nil {
			return nil, nil, err
		}
		if err := validatePorts(name, "ports", definition["ports"], true); err != nil {
			return nil, nil, err
		}
		if err := validatePorts(name, "expose", definition["expose"], false); err != nil {
			return nil, nil, err
		}
		if err := validateServiceNetworks(name, definition["networks"], networks); err != nil {
			return nil, nil, err
		}
		for _, resource := range []string{"secrets", "configs"} {
			declared, _ := optionalMapping(document, resource)
			if err := validateServiceResources(name, resource, definition[resource], declared); err != nil {
				return nil, nil, err
			}
		}
		if err := validateDeploy(name, definition["deploy"]); err != nil {
			return nil, nil, err
		}
		if _, exists := definition["pull_policy"]; exists {
			swarmIssues = append(swarmIssues, fmt.Sprintf("service %s: pull_policy is Compose-only", name))
		}
		if _, exists := definition["depends_on"]; exists {
			swarmIssues = append(swarmIssues, fmt.Sprintf("service %s: depends_on startup ordering is not supported by Swarm", name))
		}
		if _, exists := definition["restart"]; exists {
			swarmIssues = append(swarmIssues, fmt.Sprintf("service %s: restart is Compose-only; use deploy.restart_policy", name))
		}
		if _, exists := definition["shm_size"]; exists {
			swarmIssues = append(swarmIssues, fmt.Sprintf("service %s: shm_size is not supported by Swarm services", name))
		}
		if _, exists := definition["container_name"]; exists {
			swarmIssues = append(swarmIssues, fmt.Sprintf("service %s: container_name is Compose-only", name))
		}
		ports := inferPorts(definition)
		service := ServiceSpec{Name: name}
		if len(ports) == 1 {
			service.TargetPort, service.PortCertain = ports[0], true
		}
		services = append(services, service)
	}
	return services, swarmIssues, nil
}

func validComposeServiceName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func validateDeploy(service string, raw any) error {
	if raw == nil {
		return nil
	}
	deploy, ok := stringMap(raw)
	if !ok {
		return fmt.Errorf("service %s deploy must be a mapping", service)
	}
	allowed := map[string]bool{
		"endpoint_mode": true, "labels": true, "mode": true, "placement": true, "replicas": true,
		"resources": true, "restart_policy": true, "rollback_config": true, "update_config": true,
	}
	for key := range deploy {
		if !allowed[key] {
			return fmt.Errorf("service %s deploy uses unsupported field %s", service, key)
		}
	}
	if replicas, exists := deploy["replicas"]; exists {
		value, parseErr := strconv.Atoi(fmt.Sprint(replicas))
		if parseErr != nil || value < 0 {
			return fmt.Errorf("service %s deploy replicas must be a non-negative integer", service)
		}
	}
	for _, field := range []string{"placement", "restart_policy", "rollback_config", "update_config"} {
		if value, exists := deploy[field]; exists {
			if _, ok := stringMap(value); !ok {
				return fmt.Errorf("service %s deploy %s must be a mapping", service, field)
			}
		}
	}
	resources := map[string]any{}
	if value, exists := deploy["resources"]; exists {
		var ok bool
		resources, ok = stringMap(value)
		if !ok {
			return fmt.Errorf("service %s deploy resources must be a mapping", service)
		}
	}
	reservations := map[string]any{}
	if value, exists := resources["reservations"]; exists {
		var ok bool
		reservations, ok = stringMap(value)
		if !ok {
			return fmt.Errorf("service %s deploy resources reservations must be a mapping", service)
		}
	}
	if value, exists := resources["limits"]; exists {
		if _, ok := stringMap(value); !ok {
			return fmt.Errorf("service %s deploy resources limits must be a mapping", service)
		}
	}
	if _, exists := reservations["devices"]; exists {
		return fmt.Errorf("service %s deploy requests forbidden devices", service)
	}
	return nil
}

func optionalMapping(document map[string]any, key string) (map[string]any, error) {
	raw, exists := document[key]
	if !exists || raw == nil {
		return map[string]any{}, nil
	}
	value, ok := stringMap(raw)
	if !ok {
		return nil, fmt.Errorf("Compose top-level %s must be a mapping", key)
	}
	return value, nil
}

func composeResourceDefinition(kind, name string, raw any) (map[string]any, error) {
	if raw == nil {
		return map[string]any{}, nil
	}
	value, ok := stringMap(raw)
	if !ok {
		return nil, fmt.Errorf("%s %q definition must be a mapping", kind, name)
	}
	return value, nil
}

func validateServiceNetworks(service string, raw any, declared map[string]any) error {
	if raw == nil {
		return nil
	}
	names := make([]string, 0)
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			name, ok := item.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return fmt.Errorf("service %s networks entries must be names", service)
			}
			names = append(names, name)
		}
	case map[string]any:
		for name, options := range value {
			if options != nil {
				if _, ok := stringMap(options); !ok {
					return fmt.Errorf("service %s network %q options must be a mapping", service, name)
				}
			}
			names = append(names, name)
		}
	default:
		return fmt.Errorf("service %s networks must be a list or mapping", service)
	}
	for _, name := range names {
		if name == "default" {
			continue
		}
		if _, exists := declared[name]; !exists {
			return fmt.Errorf("service %s references undeclared network %q", service, name)
		}
	}
	return nil
}

func validateServiceResources(service, resource string, raw any, declared map[string]any) error {
	if raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("service %s %s must be a list", service, resource)
	}
	for _, item := range items {
		name := ""
		switch value := item.(type) {
		case string:
			name = strings.TrimSpace(value)
		case map[string]any:
			name = strings.TrimSpace(fmt.Sprint(value["source"]))
		default:
			return fmt.Errorf("service %s has an invalid %s entry", service, resource)
		}
		if name == "" || name == "<nil>" {
			return fmt.Errorf("service %s %s entry requires source", service, resource)
		}
		if _, exists := declared[name]; !exists {
			return fmt.Errorf("service %s references undeclared %s %q", service, strings.TrimSuffix(resource, "s"), name)
		}
	}
	return nil
}

func validatePorts(service, field string, raw any, published bool) error {
	if raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("service %s %s must be a list", service, field)
	}
	for _, item := range items {
		switch value := item.(type) {
		case int:
			if value < 1 || value > 65535 {
				return fmt.Errorf("service %s %s contains invalid port %d", service, field, value)
			}
		case string:
			if !validPortTarget(value) {
				return fmt.Errorf("service %s %s contains invalid port %q", service, field, value)
			}
		case map[string]any:
			if !published {
				return fmt.Errorf("service %s expose entries cannot use mapping syntax", service)
			}
			if !validPortTarget(fmt.Sprint(value["target"])) {
				return fmt.Errorf("service %s ports mapping requires a valid target", service)
			}
			if protocol := strings.TrimSpace(fmt.Sprint(value["protocol"])); protocol != "" && protocol != "<nil>" && protocol != "tcp" && protocol != "udp" {
				return fmt.Errorf("service %s ports mapping has invalid protocol %q", service, protocol)
			}
		default:
			return fmt.Errorf("service %s %s contains an invalid entry", service, field)
		}
	}
	return nil
}

func validPortTarget(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" || value == "<nil>" {
		return false
	}
	if slash := strings.LastIndexByte(value, '/'); slash >= 0 {
		protocol := value[slash+1:]
		if protocol != "tcp" && protocol != "udp" {
			return false
		}
		value = value[:slash]
	}
	if colon := strings.LastIndexByte(value, ':'); colon >= 0 {
		value = value[colon+1:]
	}
	parts := strings.Split(value, "-")
	if len(parts) > 2 {
		return false
	}
	first, err := strconv.Atoi(parts[0])
	if err != nil || first < 1 || first > 65535 {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	last, err := strconv.Atoi(parts[1])
	return err == nil && last >= first && last <= 65535
}

func validateServiceEnvironment(service string, raw any) error {
	if raw == nil {
		return nil
	}
	switch values := raw.(type) {
	case map[string]any:
		for key, value := range values {
			if !validEnvironmentKey(key) {
				return fmt.Errorf("service %s has invalid environment key %q", service, key)
			}
			if value == nil {
				return fmt.Errorf("service %s environment %s must have an explicit value", service, key)
			}
		}
	case []any:
		for _, rawItem := range values {
			item, ok := rawItem.(string)
			if !ok || !strings.Contains(item, "=") {
				return fmt.Errorf("service %s environment entries must use KEY=value and cannot inherit controller environment", service)
			}
			key := strings.SplitN(item, "=", 2)[0]
			if !validEnvironmentKey(key) {
				return fmt.Errorf("service %s has invalid environment key %q", service, key)
			}
		}
	default:
		return fmt.Errorf("service %s environment must be a mapping or list", service)
	}
	return nil
}

func validateVolumes(service string, raw any, declared map[string]any) error {
	return validateVolumesForNode(service, raw, declared, false)
}

func validateVolumesForNode(service string, raw any, declared map[string]any, restricted bool) error {
	if raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("service %s volumes must be a list", service)
	}
	for _, item := range items {
		switch value := item.(type) {
		case string:
			parts := strings.Split(value, ":")
			if len(parts) < 2 {
				continue // anonymous volume
			}
			source := parts[0]
			if isHostPath(source) {
				if !restricted {
					return fmt.Errorf("service %s uses forbidden bind mount without an exact runtime node constraint", service)
				}
				if err := validateBindPath(source, parts[1]); err != nil {
					return fmt.Errorf("service %s: %w", service, err)
				}
				if len(parts) > 3 || (len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw") {
					return fmt.Errorf("service %s uses unsupported bind mount options", service)
				}
				continue
			}
			if _, ok := declared[source]; !ok {
				return fmt.Errorf("service %s volume %q must be a declared named volume", service, source)
			}
		case map[string]any:
			typeName := fmt.Sprint(value["type"])
			if typeName == "tmpfs" && restricted {
				if err := validateTmpfsMount(value); err != nil {
					return fmt.Errorf("service %s: %w", service, err)
				}
				continue
			}
			if typeName == "bind" && restricted {
				if err := validateBindMount(value); err != nil {
					return fmt.Errorf("service %s: %w", service, err)
				}
				continue
			}
			if typeName != "volume" {
				return fmt.Errorf("service %s volume type %q is forbidden", service, typeName)
			}
			source, _ := value["source"].(string)
			if source != "" {
				if _, ok := declared[source]; !ok {
					return fmt.Errorf("service %s volume %q must be a declared named volume", service, source)
				}
			}
		default:
			return fmt.Errorf("service %s has an invalid volume entry", service)
		}
	}
	return nil
}

func inferPorts(definition map[string]any) []int {
	seen := make(map[int]struct{})
	for _, field := range []string{"expose", "ports"} {
		items, _ := definition[field].([]any)
		for _, item := range items {
			port := 0
			switch value := item.(type) {
			case int:
				port = value
			case string:
				text := strings.TrimSuffix(value, "/tcp")
				if strings.HasSuffix(text, "/udp") {
					continue
				}
				parts := strings.Split(text, ":")
				port, _ = strconv.Atoi(parts[len(parts)-1])
			case map[string]any:
				if protocol := fmt.Sprint(value["protocol"]); protocol != "<nil>" && protocol != "" && protocol != "tcp" {
					continue
				}
				port, _ = strconv.Atoi(fmt.Sprint(value["target"]))
			}
			if port > 0 && port <= 65535 {
				seen[port] = struct{}{}
			}
		}
	}
	ports := make([]int, 0, len(seen))
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func stringMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case nil:
		return nil, false
	default:
		return nil, false
	}
}

func containsDockerSocket(value any) bool {
	return strings.Contains(strings.ToLower(fmt.Sprint(value)), "docker.sock") || strings.Contains(strings.ToLower(fmt.Sprint(value)), "//./pipe/docker_engine")
}

func isHostPath(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "~") ||
		strings.Contains(value, "..") || (len(value) > 2 && value[1] == ':' && (value[2] == '\\' || value[2] == '/'))
}

func parseManifest(raw string) (Manifest, error) {
	var value Manifest
	if strings.TrimSpace(raw) == "" {
		return value, nil
	}
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode nsc-app.yaml: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return value, fmt.Errorf("decode trailing nsc-app.yaml: %w", err)
		}
		return value, fmt.Errorf("nsc-app.yaml must contain exactly one document")
	}
	return value, nil
}

func ParseManifest(raw string) (Manifest, error) { return parseManifest(raw) }

func validateManifest(manifest Manifest) error {
	groups := make(map[string]struct{}, len(manifest.Groups))
	for index, group := range manifest.Groups {
		group.Name = strings.TrimSpace(group.Name)
		if group.Name == "" {
			return fmt.Errorf("manifest groups[%d] requires a name", index)
		}
		if _, duplicate := groups[group.Name]; duplicate {
			return fmt.Errorf("manifest duplicates group %q", group.Name)
		}
		groups[group.Name] = struct{}{}
	}
	variables := make(map[string]struct{}, len(manifest.Variables))
	for _, variable := range manifest.Variables {
		if !validEnvironmentKey(variable.Key) {
			return fmt.Errorf("manifest variable %q has an invalid name", variable.Key)
		}
		if _, duplicate := variables[variable.Key]; duplicate {
			return fmt.Errorf("manifest duplicates variable %s", variable.Key)
		}
		variables[variable.Key] = struct{}{}
		if variable.Group != "" {
			if _, exists := groups[variable.Group]; !exists {
				return fmt.Errorf("manifest variable %s references unknown group %q", variable.Key, variable.Group)
			}
		}
		if err := validateManifestVariableDefinition(variable); err != nil {
			return err
		}
		if variable.Default != "" {
			if err := validateManifestVariable(variable, variable.Default); err != nil {
				return fmt.Errorf("manifest variable %s has an invalid default: %w", variable.Key, err)
			}
		}
	}
	return nil
}

func validateManifestVariableDefinition(variable ManifestVariable) error {
	if variable.MinLength < 0 || variable.MaxLength < 0 || (variable.MaxLength > 0 && variable.MinLength > variable.MaxLength) {
		return fmt.Errorf("manifest variable %s has invalid length constraints", variable.Key)
	}
	if variable.Pattern != "" {
		if _, err := regexp.Compile(variable.Pattern); err != nil {
			return fmt.Errorf("manifest variable %s has invalid pattern: %w", variable.Key, err)
		}
	}
	switch strings.ToLower(strings.TrimSpace(variable.Generator)) {
	case "", "uuid", "password", "token":
	default:
		return fmt.Errorf("manifest variable %s uses unsupported generator %q", variable.Key, variable.Generator)
	}
	seenChoices := make(map[string]struct{}, len(variable.Choices))
	for _, choice := range variable.Choices {
		if _, duplicate := seenChoices[choice]; duplicate {
			return fmt.Errorf("manifest variable %s duplicates choice %q", variable.Key, choice)
		}
		seenChoices[choice] = struct{}{}
	}
	return nil
}

func validateManifestVariable(variable ManifestVariable, value string) error {
	length := utf8.RuneCountInString(value)
	if variable.MinLength > 0 && length < variable.MinLength {
		return fmt.Errorf("environment variable %s is shorter than min_length", variable.Key)
	}
	if variable.MaxLength > 0 && length > variable.MaxLength {
		return fmt.Errorf("environment variable %s is longer than max_length", variable.Key)
	}
	if variable.Pattern != "" {
		pattern, err := regexp.Compile(variable.Pattern)
		if err != nil {
			return fmt.Errorf("manifest variable %s has invalid pattern: %w", variable.Key, err)
		}
		if !pattern.MatchString(value) {
			return fmt.Errorf("environment variable %s does not match its required pattern", variable.Key)
		}
	}
	if len(variable.Choices) > 0 {
		matched := false
		for _, choice := range variable.Choices {
			matched = matched || value == choice
		}
		if !matched {
			return fmt.Errorf("environment variable %s is not one of the allowed choices", variable.Key)
		}
	}
	return nil
}

func validateRecommendedDomains(domains []ManifestDomain, services []ServiceSpec) error {
	available := make(map[string]struct{}, len(services))
	for _, service := range services {
		available[service.Name] = struct{}{}
	}
	for index, domain := range domains {
		if _, exists := available[domain.Service]; !exists {
			return fmt.Errorf("recommended_domains[%d] references unknown service %q", index, domain.Service)
		}
		if domain.Path != "" && (!strings.HasPrefix(domain.Path, "/") || strings.ContainsAny(domain.Path, "?#\r\n")) {
			return fmt.Errorf("recommended_domains[%d] has an invalid path", index)
		}
		if domain.TargetPort < 0 || domain.TargetPort > 65535 {
			return fmt.Errorf("recommended_domains[%d] has an invalid target_port", index)
		}
		if domain.TLSMode != "" && domain.TLSMode != model.TLSDisabled && domain.TLSMode != model.TLSManaged && domain.TLSMode != model.TLSCustom {
			return fmt.Errorf("recommended_domains[%d] has an invalid tls_mode", index)
		}
	}
	return nil
}

func requiredInterpolationKeys(value string) []string {
	set := make(map[string]struct{})
	for offset := 0; offset < len(value); {
		start := strings.Index(value[offset:], "${")
		if start < 0 {
			break
		}
		start += offset
		end := strings.IndexByte(value[start+2:], '}')
		if end < 0 {
			break
		}
		end += start + 2
		expression := value[start+2 : end]
		for _, operator := range []string{":?", "?"} {
			if index := strings.Index(expression, operator); index > 0 {
				key := expression[:index]
				if validEnvironmentKey(key) {
					set[key] = struct{}{}
				}
				break
			}
		}
		offset = end + 1
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func generateValue(generator string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(generator)) {
	case "uuid":
		return uuid.NewString(), nil
	case "password", "token":
		buffer := make([]byte, 24)
		rand.Read(buffer) // Fills the buffer or terminates on entropy failure.
		return hex.EncodeToString(buffer), nil
	default:
		return "", fmt.Errorf("unsupported generator %q", generator)
	}
}

func validEnvironmentKey(value string) bool {
	if value == "" || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for i := 1; i < len(value); i++ {
		character := value[i]
		if !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

// EqualEnvironmentModes is useful to contract tests and UI conversions.
func EqualEnvironmentModes(raw string, values []EnvironmentVariable) (bool, error) {
	left, _, err := NormalizeEnvironment(EnvironmentInput{Raw: &raw}, nil, nil)
	if err != nil {
		return false, err
	}
	right, _, err := NormalizeEnvironment(EnvironmentInput{Values: &values}, nil, nil)
	if err != nil {
		return false, err
	}
	return bytes.Equal([]byte(RenderDotEnv(left)), []byte(RenderDotEnv(right))), nil
}
