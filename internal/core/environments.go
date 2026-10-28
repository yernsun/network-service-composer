package core

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Project struct {
	LifecycleOperations []string `json:"lifecycle_operations"`
	System              bool     `json:"system"`
	storage.ControlDocument
	Name                 string `json:"name"`
	Slug                 string `json:"slug"`
	Description          string `json:"description"`
	DefaultRuntimeID     string `json:"default_runtime_id"`
	InitialEnvironmentID string `json:"initial_environment_id,omitempty"`
	DeletionID           string `json:"deletion_id,omitempty"`
}
type Environment struct {
	LifecycleOperations []string `json:"lifecycle_operations"`
	System              bool     `json:"system"`
	storage.ControlDocument
	ProjectID                   string   `json:"project_id"`
	Name                        string   `json:"name"`
	DisplayName                 string   `json:"display_name"`
	Kind                        string   `json:"kind"`
	GatewayIDs                  []string `json:"gateway_ids"`
	RuntimeID                   string   `json:"runtime_id"`
	ProjectName                 string   `json:"project_name"`
	ApplicationID               string   `json:"application_id,omitempty"`
	RegistryCredentialRef       string   `json:"registry_credential_ref,omitempty"`
	PublicVariablesVersion      int64    `json:"public_variables_version"`
	EnvironmentVariablesVersion int64    `json:"environment_variables_version"`
}
type EnvironmentInput struct {
	Name                  string   `json:"name"`
	DisplayName           string   `json:"display_name,omitempty"`
	Kind                  string   `json:"kind,omitempty"`
	GatewayIDs            []string `json:"gateway_ids,omitempty"`
	RuntimeID             string   `json:"runtime_id"`
	ApplicationID         string   `json:"application_id,omitempty"`
	CopyEnvironmentID     string   `json:"copy_environment_id,omitempty"`
	RegistryCredentialRef string   `json:"registry_credential_ref,omitempty"`
}
type Variable struct {
	Key    string  `json:"key"`
	Value  *string `json:"value,omitempty"`
	Secret bool    `json:"secret"`
	Mask   bool    `json:"mask"`
	Source string  `json:"source,omitempty"`
}
type VariableInput struct {
	Values []Variable `json:"values"`
	Raw    *string    `json:"raw,omitempty"`
}
type VariableSet struct {
	storage.ControlDocument
	Values []Variable `json:"values"`
}
type encryptedControl struct {
	Ciphertext []byte `json:"ciphertext"`
	KeyID      string `json:"key_id"`
}

var environmentName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func readControl[T any](ctx context.Context, s storage.Store, key, kind string) (T, storage.ControlDocument, error) {
	var v T
	d, e := s.GetControlDocument(ctx, key)
	if e != nil {
		return v, d, e
	}
	if d.Kind != kind {
		return v, d, storage.ErrNotFound
	}
	e = json.Unmarshal(d.Payload, &v)
	return v, d, e
}
func (c *Core) controlWrite(key, kind, parent string, v any, expected int64) (storage.ControlWrite, error) {
	payload, e := json.Marshal(v)
	now := c.now()
	return storage.ControlWrite{Document: storage.ControlDocument{ID: key, Kind: kind, ParentID: parent, Payload: payload, CreatedAt: now, UpdatedAt: now}, Expected: expected}, e
}
func (c *Core) saveControl(ctx context.Context, key, kind, parent string, v any, expected int64) error {
	w, e := c.controlWrite(key, kind, parent, v, expected)
	if e != nil {
		return e
	}
	return c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w})
}
func (c *Core) requireControlManagement() error {
	if !c.config.Management.Enabled {
		return invalid("management.control_disabled", "", "management is disabled")
	}
	return nil
}
func (c *Core) CreateProject(ctx context.Context, name, slug string) (Project, error) {
	return c.CreateProjectWithEnvironment(ctx, ProjectInput{Name: name, Slug: slug}, "")
}
func (c *Core) GetProject(ctx context.Context, key string) (Project, error) {
	p, d, e := readControl[Project](ctx, c.store, key, "project")
	p.ControlDocument = d
	p.LifecycleOperations = c.projectOperations(p.System)
	return p, e
}
func (c *Core) ListProjects(ctx context.Context, page storage.Page) ([]Project, error) {
	ds, e := c.store.ListControlDocuments(ctx, "project", "", page)
	result := []Project{}
	for _, d := range ds {
		var p Project
		if err := json.Unmarshal(d.Payload, &p); err != nil {
			return nil, err
		}
		p.ControlDocument = d
		p.LifecycleOperations = c.projectOperations(p.System)
		result = append(result, p)
	}
	return result, e
}
func (c *Core) GetEnvironment(ctx context.Context, key string) (Environment, error) {
	v, d, e := readControl[Environment](ctx, c.store, key, "environment")
	v.ControlDocument = d
	normalizeEnvironment(&v)
	v.LifecycleOperations = c.projectOperations(v.System)
	return v, e
}
func (c *Core) ListEnvironments(ctx context.Context, parent string, page storage.Page) ([]Environment, error) {
	ds, e := c.store.ListControlDocuments(ctx, "environment", parent, page)
	result := []Environment{}
	for _, d := range ds {
		var p Environment
		if err := json.Unmarshal(d.Payload, &p); err != nil {
			return nil, err
		}
		p.ControlDocument = d
		normalizeEnvironment(&p)
		p.LifecycleOperations = c.projectOperations(p.System)
		result = append(result, p)
	}
	return result, e
}
func (c *Core) CreateEnvironment(ctx context.Context, project string, in EnvironmentInput) (Environment, error) {
	return c.CreateEnvironmentChecked(ctx, project, in, 0)
}
func (c *Core) CreateEnvironmentChecked(ctx context.Context, project string, in EnvironmentInput, expected int64) (Environment, error) {
	p, e := c.GetProject(ctx, project)
	if e != nil {
		return Environment{}, e
	}
	if p.System {
		return Environment{}, storage.ErrSystemProtected
	}
	if expected != 0 && expected != p.Revision {
		return Environment{}, storage.ErrPrecondition
	}
	if in.RuntimeID == "" {
		in.RuntimeID, e = c.environmentRuntime(ctx, p)
		if e != nil {
			return Environment{}, e
		}
	}
	if !environmentSlug.MatchString(in.Name) {
		return Environment{}, invalid("validation.application_identity_invalid", "name", "environment name must be lowercase letters, digits and hyphens")
	}
	if _, e = c.requireManagement(ctx, in.RuntimeID); e != nil {
		return Environment{}, e
	}
	v := Environment{ProjectID: project, Name: in.Name, DisplayName: in.DisplayName, Kind: in.Kind, GatewayIDs: in.GatewayIDs, RuntimeID: in.RuntimeID, RegistryCredentialRef: in.RegistryCredentialRef}
	normalizeEnvironment(&v)
	if e = c.validateEnvironmentMetadata(ctx, v); e != nil {
		return Environment{}, e
	}
	v.ID = id.New("env")
	prefix := p.Slug + "-" + in.Name
	if len(prefix) > 54 {
		prefix = strings.TrimRight(prefix[:54], "-")
	}
	v.ProjectName = prefix + "-" + v.ID[len(v.ID)-8:]
	parentGuard, e := c.controlWrite(p.ID, "project", "", p, p.Revision)
	if e != nil {
		return v, e
	}
	writes := []storage.ControlWrite{parentGuard}
	if in.ApplicationID != "" {
		a, err := c.store.GetApplication(ctx, in.ApplicationID)
		if err != nil {
			return v, err
		}
		if !a.Managed || a.RuntimeID != in.RuntimeID {
			return v, invalid("management.disabled", "application_id", "application must be managed by the selected runtime")
		}
		v.ApplicationID = a.ID
		v.ProjectName = a.ProjectName
		if v.RegistryCredentialRef == "" {
			v.RegistryCredentialRef = a.RegistryCredentialRef
		}
		v.RegistryCredentialRef = a.RegistryCredentialRef
		claim, err := c.controlWrite("application-environment:"+a.ID, "application-environment", a.ID, map[string]string{"environment_id": v.ID}, 0)
		if err != nil {
			return v, err
		}
		writes = append(writes, claim)
	}
	if in.CopyEnvironmentID != "" {
		source, err := c.GetEnvironment(ctx, in.CopyEnvironmentID)
		if err != nil {
			return v, err
		}
		if source.ProjectID != project || in.ApplicationID != "" {
			return v, invalid("validation.application_identity_invalid", "copy_environment_id", "copy must belong to the same project and cannot replace an existing application")
		}
		content, err := c.environmentCompose(ctx, source)
		if err != nil {
			return v, err
		}
		w, err := c.encryptedWrite(v.ID+":compose", "environment-compose", v.ID, content, 0)
		if err != nil {
			return v, err
		}
		writes = append(writes, w)
	}
	w, e := c.controlWrite(v.ID, "environment", project, v, 0)
	if e != nil {
		return v, e
	}
	writes = append(writes, w)
	claim, e := c.controlWrite("environment-name:"+project+":"+in.Name, "environment-name", project, map[string]string{"environment_id": v.ID}, 0)
	if e != nil {
		return v, e
	}
	writes = append(writes, claim)
	if e = c.store.WriteControlDocuments(ctx, writes); e != nil {
		return v, e
	}
	return c.GetEnvironment(ctx, v.ID)
}
func (c *Core) encryptedWrite(key, kind, parent string, v any, expected int64) (storage.ControlWrite, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return storage.ControlWrite{}, e
	}
	cipher, e := c.keyring.Encrypt(raw, []byte("control:"+key))
	if e != nil {
		return storage.ControlWrite{}, e
	}
	return c.controlWrite(key, kind, parent, encryptedControl{cipher, c.keyring.PrimaryID()}, expected)
}
func (c *Core) readEncrypted(ctx context.Context, key, kind string, target any) (storage.ControlDocument, error) {
	enc, d, e := readControl[encryptedControl](ctx, c.store, key, kind)
	if e != nil {
		return d, e
	}
	raw, _, e := c.keyring.Decrypt(enc.Ciphertext, []byte("control:"+key))
	if e != nil {
		return d, e
	}
	return d, json.Unmarshal(raw, target)
}
func (c *Core) variableOwner(ctx context.Context, owner string) error {
	if strings.HasPrefix(owner, "prj_") {
		_, e := c.GetProject(ctx, owner)
		return e
	}
	_, e := c.GetEnvironment(ctx, owner)
	return e
}
func (c *Core) variables(ctx context.Context, owner string) (VariableSet, error) {
	v := VariableSet{Values: []Variable{}}
	d, e := c.readEncrypted(ctx, owner+":variables", "variables", &v.Values)
	v.ControlDocument = d
	if errors.Is(e, storage.ErrNotFound) {
		v.ID = owner + ":variables"
		return v, nil
	}
	return v, e
}
func (c *Core) GetVariables(ctx context.Context, owner string) (VariableSet, error) {
	if e := c.variableOwner(ctx, owner); e != nil {
		return VariableSet{}, e
	}
	v, e := c.variables(ctx, owner)
	if strings.HasPrefix(owner, "env_") {
		env, err := c.GetEnvironment(ctx, owner)
		if err != nil {
			return v, err
		}
		common, err := c.variables(ctx, env.ProjectID)
		if err != nil {
			return v, err
		}
		secrets := map[string]bool{}
		for _, item := range common.Values {
			secrets[item.Key] = item.Secret
		}
		for i := range v.Values {
			v.Values[i].Secret = v.Values[i].Secret || secrets[v.Values[i].Key]
		}
	}
	for i := range v.Values {
		if v.Values[i].Secret {
			v.Values[i].Value = nil
		}
	}
	return v, e
}
func (c *Core) SaveVariables(ctx context.Context, owner string, in VariableInput, expected int64) (VariableSet, error) {
	if e := c.requireControlManagement(); e != nil {
		return VariableSet{}, e
	}
	if e := c.variableOwner(ctx, owner); e != nil {
		return VariableSet{}, e
	}
	old, e := c.variables(ctx, owner)
	if e != nil {
		return old, e
	}
	if old.Revision != expected {
		return old, storage.ErrPrecondition
	}
	if in.Raw != nil {
		if in.Values != nil {
			return old, controlInvalid("values", "choose raw or values")
		}
		values, e := applicationx.ParseDotEnv(*in.Raw)
		if e != nil {
			return old, controlInvalid("raw", e.Error())
		}
		for key, value := range values {
			in.Values = append(in.Values, Variable{Key: key, Value: &value})
		}
	}
	inheritedSecrets := map[string]bool{}
	if strings.HasPrefix(owner, "env_") {
		v, err := c.GetEnvironment(ctx, owner)
		if err != nil {
			return old, err
		}
		common, err := c.variables(ctx, v.ProjectID)
		if err != nil {
			return old, err
		}
		for _, item := range common.Values {
			inheritedSecrets[item.Key] = item.Secret
		}
	}
	previous := map[string]Variable{}
	for _, v := range old.Values {
		previous[v.Key] = v
	}
	seen := map[string]bool{}
	for i := range in.Values {
		v := &in.Values[i]
		if !variableName.MatchString(v.Key) || seen[v.Key] {
			return old, controlInvalid("values", "invalid or duplicate variable key")
		}
		seen[v.Key] = true
		v.Source = ""
		p, exists := previous[v.Key]
		v.Secret = v.Secret || p.Secret || inheritedSecrets[v.Key]
		if v.Mask {
			v.Value = nil
		} else if v.Value == nil {
			if !exists || p.Value == nil {
				return old, controlInvalid("values", "a new variable requires a value")
			}
			v.Value = p.Value
		}
	}
	sort.Slice(in.Values, func(i, j int) bool { return in.Values[i].Key < in.Values[j].Key })
	if in.Values == nil {
		in.Values = []Variable{}
	}
	w, e := c.encryptedWrite(owner+":variables", "variables", owner, in.Values, expected)
	if e != nil {
		return old, e
	}
	history, e := c.encryptedWrite(id.New("varrev"), "variable-version", owner, in.Values, 0)
	if e != nil {
		return old, e
	}
	if e = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{w, history}); e != nil {
		return old, e
	}
	return c.GetVariables(ctx, owner)
}

// MergeVariables keeps masking distinct from an empty value. The clear patch
// prevents a masked key from being reintroduced by template defaults.
func MergeVariables(common, overrides []Variable) ([]applicationx.EnvironmentVariable, []Variable) {
	merged := map[string]Variable{}
	for _, layer := range []struct {
		values []Variable
		source string
	}{{common, "project"}, {overrides, "environment"}} {
		for _, v := range layer.values {
			v.Secret = v.Secret || merged[v.Key].Secret
			v.Source = layer.source
			merged[v.Key] = v
		}
	}
	keys := []string{}
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := []applicationx.EnvironmentVariable{}
	preview := []Variable{}
	for _, key := range keys {
		v := merged[key]
		result = append(result, applicationx.EnvironmentVariable{Key: key, Value: v.Value, Secret: v.Secret, Clear: v.Mask})
		if v.Secret {
			v.Value = nil
		}
		preview = append(preview, v)
	}
	return result, preview
}
func (c *Core) EffectiveVariables(ctx context.Context, key string) (map[string]any, error) {
	v, e := c.GetEnvironment(ctx, key)
	if e != nil {
		return nil, e
	}
	common, e := c.variables(ctx, v.ProjectID)
	if e != nil {
		return nil, e
	}
	local, e := c.variables(ctx, key)
	if e != nil {
		return nil, e
	}
	_, preview := MergeVariables(common.Values, local.Values)
	if v.ApplicationID != "" {
		if a, err := c.store.GetApplication(ctx, v.ApplicationID); err == nil && a.ActiveRevisionID != "" {
			if r, err := c.store.GetApplicationRevision(ctx, a.ActiveRevisionID); err == nil {
				v.PublicVariablesVersion = r.ConfigurationSource.PublicVariablesVersion
				v.EnvironmentVariablesVersion = r.ConfigurationSource.EnvironmentVariablesVersion
			}
		}
	}
	return map[string]any{"values": preview, "public_version": common.Revision, "environment_version": local.Revision, "pending": common.Revision != v.PublicVariablesVersion || local.Revision != v.EnvironmentVariablesVersion}, nil
}
func (c *Core) environmentCompose(ctx context.Context, v Environment) (string, error) {
	if v.ApplicationID != "" {
		a, e := c.store.GetApplication(ctx, v.ApplicationID)
		if e != nil {
			return "", e
		}
		rev := a.ActiveRevisionID
		if rev == "" {
			rev = a.DesiredRevisionID
		}
		r, e := c.store.GetApplicationRevision(ctx, rev)
		if e != nil {
			return "", e
		}
		raw, _, e := c.keyring.Decrypt(r.EncryptedCompose, revisionAAD(r.ID, "compose"))
		return string(raw), e
	}
	var content string
	_, e := c.readEncrypted(ctx, v.ID+":compose", "environment-compose", &content)
	return content, e
}
func (c *Core) EnvironmentPreflightInput(ctx context.Context, key, compose string) (Environment, ApplicationPreflightInput, error) {
	v, e := c.GetEnvironment(ctx, key)
	if e != nil {
		return v, ApplicationPreflightInput{}, e
	}
	if v.System {
		return v, ApplicationPreflightInput{}, storage.ErrSystemProtected
	}
	if compose == "" {
		compose, e = c.environmentCompose(ctx, v)
		if e != nil {
			return v, ApplicationPreflightInput{}, e
		}
	}
	common, e := c.variables(ctx, v.ProjectID)
	if e != nil {
		return v, ApplicationPreflightInput{}, e
	}
	local, e := c.variables(ctx, key)
	if e != nil {
		return v, ApplicationPreflightInput{}, e
	}
	values, _ := MergeVariables(common.Values, local.Values)

	return v, ApplicationPreflightInput{ProjectName: v.ProjectName, ReplaceEnvironment: true, ConfigurationSource: model.DeploymentSource{EnvironmentID: v.ID, PublicVariablesVersion: common.Revision, EnvironmentVariablesVersion: local.Revision}, RuntimeID: v.RuntimeID, ComposeYAML: compose, Environment: applicationx.EnvironmentInput{Values: &values}}, nil
}
func (c *Core) DeployEnvironment(ctx context.Context, key, compose, actor, idem string, expected int64) (model.DeploymentOperation, error) {
	v, input, e := c.EnvironmentPreflightInput(ctx, key, compose)
	if e != nil {
		return model.DeploymentOperation{}, e
	}
	if expected != v.Revision {
		return model.DeploymentOperation{}, storage.ErrPrecondition
	}
	if idem == "" {
		return model.DeploymentOperation{}, controlInvalid("idempotency_key", "an idempotency key is required")
	}
	input.ConfigurationSource.EnvironmentRevision = expected
	if e = c.lockEnvironmentImages(ctx, v, &input); e != nil {
		return model.DeploymentOperation{}, e
	}
	return c.deployPreparedEnvironment(ctx, v, input, actor, idem)
}
func (c *Core) deployPreparedEnvironment(ctx context.Context, v Environment, input ApplicationPreflightInput, actor, idem string) (model.DeploymentOperation, error) {
	var op model.DeploymentOperation
	if v.ApplicationID == "" {
		a, o, _, err := c.InstallApplication(ctx, InstallApplicationInput{ApplicationPreflightInput: input, Name: v.Name, ProjectName: v.ProjectName, RegistryCredentialRef: v.RegistryCredentialRef, Actor: actor, IdempotencyKey: idem})
		if err != nil {
			return op, err
		}
		v.ApplicationID = a.ID
		op = o
	} else {
		a, err := c.store.GetApplication(ctx, v.ApplicationID)
		if err != nil {
			return op, err
		}
		_, o, _, err := c.CreateApplicationRevision(ctx, a.ID, CreateRevisionInput{ApplicationPreflightInput: input, Actor: actor, IdempotencyKey: idem}, a.Revision)
		if err != nil {
			return op, err
		}
		op = o
	}
	return op, nil
}

func controlInvalid(field, message string) error {
	return invalidParams("validation.control_invalid", field, message, map[string]any{"reason": message})
}

// These operations describe resource restrictions; authentication and runtime
// management checks still apply independently on every request.
func (c *Core) projectOperations(system bool) []string {
	if system || !c.config.Management.Enabled {
		return []string{}
	}
	return []string{"update", "delete"}
}
