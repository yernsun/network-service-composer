package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/images"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type ImagePolicy struct {
	storage.ControlDocument
	Enabled         bool     `json:"enabled"`
	Poll            bool     `json:"poll"`
	Webhook         bool     `json:"webhook"`
	Automatic       bool     `json:"automatic"`
	IntervalSeconds int      `json:"interval_seconds"`
	Services        []string `json:"services"`
}
type ImageCheck struct {
	storage.ControlDocument
	Requested   bool       `json:"requested"`
	Worker      string     `json:"worker,omitempty"`
	LeaseUntil  time.Time  `json:"lease_until,omitempty"`
	NextCheck   time.Time  `json:"next_check,omitempty"`
	LastChecked *time.Time `json:"last_checked,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	Error       string     `json:"error,omitempty"`
	Failures    int        `json:"failures"`
	CandidateID string     `json:"candidate_id,omitempty"`
}
type ImageUpdate struct {
	storage.ControlDocument
	EnvironmentID  string            `json:"environment_id"`
	BaseRevisionID string            `json:"base_revision_id"`
	Images         map[string]string `json:"images"`
	Changed        []string          `json:"changed"`
	Status         string            `json:"status"`
	OperationID    string            `json:"operation_id,omitempty"`
	Error          string            `json:"error,omitempty"`
}
type Hook struct {
	storage.ControlDocument
	EnvironmentID   string `json:"environment_id"`
	HookID          string `json:"hook_id"`
	Secret          []byte `json:"-"`
	EncryptedSecret []byte `json:"encrypted_secret,omitempty"`
}

func (c *Core) GetImagePolicy(ctx context.Context, key string) (ImagePolicy, error) {
	_, e := c.GetEnvironment(ctx, key)
	if e != nil {
		return ImagePolicy{}, e
	}
	p, d, e := readControl[ImagePolicy](ctx, c.store, key+":image-policy", "image-policy")
	if errors.Is(e, storage.ErrNotFound) {
		return ImagePolicy{ControlDocument: storage.ControlDocument{ID: key + ":image-policy"}, IntervalSeconds: 300, Services: []string{}}, nil
	}
	p.ControlDocument = d
	if p.Services == nil {
		p.Services = []string{}
	}
	return p, e
}
func (c *Core) SaveImagePolicy(ctx context.Context, key string, p ImagePolicy, expected int64) (ImagePolicy, error) {
	if err := c.store.CheckProjectResource(ctx, key); err != nil {
		return p, err
	}
	v, e := c.GetEnvironment(ctx, key)
	if e != nil {
		return p, e
	}
	if _, e = c.requireManagement(ctx, v.RuntimeID); e != nil {
		return p, e
	}
	if p.IntervalSeconds == 0 {
		p.IntervalSeconds = 300
	}
	if p.IntervalSeconds < 60 || p.IntervalSeconds > 86400 {
		return p, controlInvalid("interval_seconds", "interval must be between 60 and 86400 seconds")
	}
	if p.Enabled {
		if !p.Poll && !p.Webhook || len(p.Services) == 0 {
			return p, controlInvalid("services", "select services and at least one trigger")
		}
		a, rev, _, err := c.activeEnvironmentRevision(ctx, v)
		if err != nil {
			return p, err
		}
		if !a.Managed {
			return p, invalid("management.disabled", "", "application must be managed")
		}
		seen := map[string]bool{}
		for _, s := range p.Services {
			if seen[s] || rev.ConfigurationSource.TaggedImages[s] == "" {
				return p, controlInvalid("services", "unknown or duplicate service; deploy a digest-pinned baseline first")
			}
			seen[s] = true
		}
	}
	sort.Strings(p.Services)
	p.ControlDocument = storage.ControlDocument{}
	if e = c.saveControl(ctx, key+":image-policy", "image-policy", key, p, expected); e != nil {
		return p, e
	}
	return c.GetImagePolicy(ctx, key)
}
func (c *Core) activeEnvironmentRevision(ctx context.Context, v Environment) (model.Application, model.ApplicationRevision, string, error) {
	a, e := c.store.GetApplication(ctx, v.ApplicationID)
	if e != nil {
		return a, model.ApplicationRevision{}, "", e
	}
	rev, e := c.store.GetApplicationRevision(ctx, a.ActiveRevisionID)
	if e != nil {
		return a, rev, "", e
	}
	raw, _, e := c.keyring.Decrypt(rev.EncryptedResolved, revisionAAD(rev.ID, "resolved"))
	if e != nil {
		return a, rev, "", e
	}
	_, refs, e := images.Pin(string(raw), nil)
	if e != nil {
		return a, rev, "", e
	}
	for name, ref := range refs {
		if !strings.Contains(ref, "@sha256:") || rev.ConfigurationSource.Images[name] != ref {
			return a, rev, "", controlInvalid("baseline", "deploy a digest-pinned baseline before enabling image updates")
		}
	}
	return a, rev, string(raw), nil
}
func (c *Core) imageResolver(credential string) (images.Resolver, error) {
	r := images.Resolver{}
	if credential != "" {
		cfg, ok := c.config.Registries[credential]
		if !ok {
			return r, fmt.Errorf("registry credential is not configured")
		}
		r.ConfigFile = cfg.ConfigFile
		r.Insecure = cfg.Insecure
	}
	return r, nil
}
func (c *Core) lockEnvironmentImages(ctx context.Context, v Environment, input *ApplicationPreflightInput) error {
	preview, e := c.PreflightApplication(ctx, *input)
	if e != nil {
		return e
	}
	// Preflight's public result is redacted, so use the internal parser for the
	// actual image references. No plaintext resolved file leaves the core.
	runtime, e := c.store.GetRuntime(ctx, v.RuntimeID)
	if e != nil {
		return e
	}
	pool, e := c.store.GetPool(ctx, runtime.PoolID)
	if e != nil {
		return e
	}
	allowedSecrets, allowedConfigs, e := c.allowedDockerResourceNames(ctx, runtime)
	if e != nil {
		return e
	}
	result, e := applicationx.Preflight(applicationx.PreflightInput{ComposeYAML: input.ComposeYAML, Environment: input.Environment, Backend: runtime.Type, AllowedSecrets: allowedSecrets, AllowedConfigs: allowedConfigs, ProtectedNetworks: []string{pool.BackendNetwork}, DataNodeID: effectiveDeploymentPlan(input.DeploymentPlan, input.ConfigurationSource.DeploymentPlan).DataNodeID, RuntimeNodeID: applicationx.RuntimeNodeID(runtime.Endpoint)})
	if e != nil {
		return e
	}
	_ = preview
	_, refs, e := images.Pin(result.ResolvedYAML, nil)
	if e != nil {
		return e
	}
	resolver, e := c.imageResolver(v.RegistryCredentialRef)
	if e != nil {
		return e
	}
	locks := map[string]string{}
	for name, ref := range refs {
		locked, e := resolver.Resolve(ctx, ref)
		if e != nil {
			return controlInvalid("images", fmt.Sprintf("%s: %s", name, e))
		}
		locks[name] = locked
	}
	input.ConfigurationSource.Images = locks
	input.ConfigurationSource.TaggedImages = refs
	return nil
}
func pinPreflight(result *applicationx.PreflightResult, source model.DeploymentSource) error {
	if len(source.Images) == 0 {
		return nil
	}
	resolved, _, e := images.Pin(result.ResolvedYAML, source.Images)
	if e != nil {
		return e
	}
	result.ResolvedYAML = resolved
	result.ResolvedSHA256 = digestString(resolved)
	return nil
}
func (c *Core) GetImageCheck(ctx context.Context, key string) (ImageCheck, error) {
	if _, e := c.GetEnvironment(ctx, key); e != nil {
		return ImageCheck{}, e
	}
	s, d, e := readControl[ImageCheck](ctx, c.store, key+":image-check", "image-check")
	if errors.Is(e, storage.ErrNotFound) {
		return ImageCheck{ControlDocument: storage.ControlDocument{ID: key + ":image-check"}}, nil
	}
	s.ControlDocument = d
	return s, e
}
func (c *Core) RequestImageCheck(ctx context.Context, key string) (ImageCheck, error) {
	if e := c.requireControlManagement(); e != nil {
		return ImageCheck{}, e
	}
	for i := 0; i < 4; i++ {
		s, e := c.GetImageCheck(ctx, key)
		if e != nil {
			return s, e
		}
		s.Requested = true
		if e = c.saveControl(ctx, key+":image-check", "image-check", key, s, s.Revision); e == nil {
			return c.GetImageCheck(ctx, key)
		} else if !errors.Is(e, storage.ErrPrecondition) && !errors.Is(e, storage.ErrConflict) {
			return s, e
		}
	}
	return ImageCheck{}, storage.ErrConflict
}
func (c *Core) ListImageUpdates(ctx context.Context, key string, page storage.Page) ([]ImageUpdate, error) {
	if _, e := c.GetEnvironment(ctx, key); e != nil {
		return nil, e
	}
	docs, e := c.store.ListControlDocuments(ctx, "image-update", key, page)
	result := []ImageUpdate{}
	for _, d := range docs {
		var v ImageUpdate
		if err := json.Unmarshal(d.Payload, &v); err != nil {
			return nil, err
		}
		v.ControlDocument = d
		if v.OperationID != "" {
			if op, err := c.store.GetDeploymentOperation(ctx, v.OperationID); err == nil {
				v.Status = string(op.Status)
				v.Error = op.ErrorMessage
			}
		}
		result = append(result, v)
	}
	return result, e
}
func (c *Core) ImageUpdateAction(ctx context.Context, key, candidate, action, actor string, expected int64) (ImageUpdate, error) {
	if err := c.store.CheckProjectResource(ctx, key); err != nil {
		return ImageUpdate{}, err
	}
	v, e := c.GetEnvironment(ctx, key)
	if e != nil {
		return ImageUpdate{}, e
	}
	u, d, e := readControl[ImageUpdate](ctx, c.store, candidate, "image-update")
	u.ControlDocument = d
	if e != nil || d.ParentID != key {
		if e == nil {
			e = storage.ErrNotFound
		}
		return u, e
	}
	if expected != d.Revision {
		return u, storage.ErrPrecondition
	}
	if _, e = c.requireManagement(ctx, v.RuntimeID); e != nil {
		return u, e
	}
	if action == "ignore" {
		if u.OperationID != "" || u.Status == "SUBMITTING" {
			return u, storage.ErrConflict
		}
		u.Status = "IGNORED"
		e = c.saveControl(ctx, u.ID, "image-update", key, u, d.Revision)
		return u, e
	}
	if action != "deploy" && action != "retry" {
		return u, invalid("validation.application_operation_invalid", "action", "action must be deploy, retry, or ignore")
	}
	if u.Status == "SUBMITTING" || (action == "deploy" && u.Status != "AVAILABLE") {
		return u, storage.ErrConflict
	}
	a, rev, _, e := c.activeEnvironmentRevision(ctx, v)
	if e != nil {
		return u, e
	}
	if a.ActiveRevisionID != u.BaseRevisionID || a.DesiredRevisionID != "" && a.DesiredRevisionID != a.ActiveRevisionID {
		return u, storage.ErrPrecondition
	}
	if a.Status != model.ApplicationRunning {
		return u, invalid("validation.application_operation_invalid", "", "environment is not running")
	}
	for _, n := range a.ReplicaOverrides {
		if n == 0 {
			return u, invalid("validation.application_operation_invalid", "", "a service is intentionally scaled to zero")
		}
	}
	operations, e := c.store.ListDeploymentOperations(ctx, a.ID, storage.Page{Limit: 200})
	if e != nil {
		return u, e
	}
	for _, o := range operations {
		if o.Status == model.OperationQueued || o.Status == model.OperationRunning {
			return u, storage.ErrConflict
		}
	}
	if u.OperationID != "" {
		op, e := c.store.GetDeploymentOperation(ctx, u.OperationID)
		if e != nil {
			return u, e
		}
		if action != "retry" || op.Status == model.OperationQueued || op.Status == model.OperationRunning || op.Status == model.OperationSucceeded {
			return u, storage.ErrConflict
		}
	}
	compose, _, e := c.keyring.Decrypt(rev.EncryptedCompose, revisionAAD(rev.ID, "compose"))
	if e != nil {
		return u, e
	}
	envRaw, _, e := c.keyring.Decrypt(rev.EncryptedEnvironment, revisionAAD(rev.ID, "environment"))
	if e != nil {
		return u, e
	}
	env, e := applicationx.ParseDotEnv(string(envRaw))
	if e != nil {
		return u, e
	}
	secret := map[string]bool{}
	for _, k := range rev.SecretKeys {
		secret[k] = true
	}
	values := []applicationx.EnvironmentVariable{}
	for k, value := range env {
		values = append(values, applicationx.EnvironmentVariable{Key: k, Value: &value, Secret: secret[k]})
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Key < values[j].Key })
	source := rev.ConfigurationSource
	source.RestoredFrom = ""
	source.Entrypoint = nil
	if source.ServiceScope != nil {
		names := []string{}
		for name, image := range u.Images {
			if source.Images[name] != image {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return u, storage.ErrPrecondition
		}
		selected, err := applicationx.SelectServices(string(compose), names)
		if err != nil {
			return u, err
		}
		compose = []byte(selected)
		source.NetworkName = ""
		source.ServiceScope = &model.ServiceDeploymentScope{Mode: "update", Names: names, BaseRevisionID: a.ActiveRevisionID, ProjectID: source.ServiceScope.ProjectID}
		target, err := c.GetEnvironment(ctx, key)
		if err != nil {
			return u, err
		}
		if err = c.freezeServiceVersions(ctx, target, source.ServiceScope); err != nil {
			return u, err
		}
	}
	source.Images = u.Images
	source.EnvironmentRevision = 0
	source.ImageUpdate = true
	source.UpdatePolicyRevision = 0
	if actor == "image-updater" {
		policy, err := c.GetImagePolicy(ctx, key)
		if err != nil {
			return u, err
		}
		if !policy.Enabled || !policy.Automatic {
			return u, storage.ErrPrecondition
		}
		source.UpdatePolicyRevision = policy.Revision
	}
	// Persist ownership before enqueueing. A retry of the same attempt reuses
	// its idempotency key; other checkers never submit an in-flight candidate.
	u.Status = "SUBMITTING"
	if e = c.saveControl(ctx, u.ID, "image-update", key, u, d.Revision); e != nil {
		return u, e
	}
	attempt := d.Revision + 1
	_, op, _, e := c.CreateApplicationRevision(ctx, a.ID, CreateRevisionInput{ApplicationPreflightInput: ApplicationPreflightInput{RuntimeID: a.RuntimeID, ComposeYAML: string(compose), Environment: applicationx.EnvironmentInput{Values: &values}, ReplaceEnvironment: true, ConfigurationSource: source}, Actor: actor, IdempotencyKey: "image-update:" + u.ID + ":" + strconv.FormatInt(attempt, 10)}, a.Revision)
	if e != nil {
		u.Status = "FAILED"
		u.Error = e.Error()
	} else {
		u.Status = string(op.Status)
		u.OperationID = op.ID
	}
	saveErr := c.saveControl(ctx, u.ID, "image-update", key, u, attempt)
	if e == nil {
		_ = c.store.AppendAuditEvent(ctx, model.AuditEvent{ID: id.New("audit"), Actor: actor, Action: "image-update.deploy", ResourceType: "environment", ResourceID: key, Details: op.ID, CreatedAt: c.now()})
	}
	if e != nil {
		return u, e
	}
	return u, saveErr
}

// Run performs registry polling independently of browser sessions. Each
// environment check has a durable lease, bounded by the request deadline.
func (c *Core) Run(ctx context.Context) error {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		c.imageTick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Core) imageTick(ctx context.Context) {
	if !c.config.Management.Enabled {
		return
	}
	page := storage.Page{Limit: 200}
	sem := make(chan struct{}, 4)
	var workers sync.WaitGroup
	for {
		envs, e := c.ListEnvironments(ctx, "", page)
		if e != nil {
			return
		}
		for _, v := range envs {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				workers.Wait()
				return
			}
			workers.Add(1)
			go func(v Environment) { defer workers.Done(); defer func() { <-sem }(); c.checkEnvironmentImages(ctx, v) }(v)
		}
		if len(envs) < 200 {
			break
		}
		page.After = envs[len(envs)-1].ID
	}
	workers.Wait()
}
func (c *Core) checkEnvironmentImages(parent context.Context, v Environment) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	c.recoverImageSubmissions(ctx, v)
	c.serviceEntrypointTick(ctx, v)
	policy, e := c.GetImagePolicy(ctx, v.ID)
	if e != nil || !policy.Enabled {
		return
	}
	state, e := c.GetImageCheck(ctx, v.ID)
	if e != nil {
		return
	}
	now := c.now()
	if state.LeaseUntil.After(now) || (!state.Requested && (!policy.Poll || state.NextCheck.After(now))) {
		return
	}
	worker := id.New("check")
	state.Worker = worker
	state.LeaseUntil = now.Add(time.Minute)
	state.Requested = false
	if e = c.saveControl(ctx, v.ID+":image-check", "image-check", v.ID, state, state.Revision); e != nil {
		return
	}
	resultErr := func() error {
		if _, e := c.requireManagement(ctx, v.RuntimeID); e != nil {
			return e
		}
		_, rev, _, e := c.activeEnvironmentRevision(ctx, v)
		if e != nil {
			return e
		}
		resolver, e := c.imageResolver(v.RegistryCredentialRef)
		if e != nil {
			return e
		}
		locks := map[string]string{}
		for k, val := range rev.ConfigurationSource.Images {
			locks[k] = val
		}
		changed := []string{}
		for _, name := range policy.Services {
			ref := rev.ConfigurationSource.TaggedImages[name]
			if ref == "" {
				return fmt.Errorf("tracked service %s no longer exists", name)
			}
			if strings.Contains(ref, "@") {
				continue
			}
			resolved, e := resolver.Resolve(ctx, ref)
			if e != nil {
				return e
			}
			if locks[name] != resolved {
				locks[name] = resolved
				changed = append(changed, name)
			}
		}
		if len(changed) == 0 {
			return nil
		}
		sort.Strings(changed)
		encoded, _ := json.Marshal(locks)
		key := "img_" + digestString(v.ID+rev.ID+string(encoded))
		u, d, e := readControl[ImageUpdate](ctx, c.store, key, "image-update")
		if errors.Is(e, storage.ErrNotFound) {
			u = ImageUpdate{EnvironmentID: v.ID, BaseRevisionID: rev.ID, Images: locks, Changed: changed, Status: "AVAILABLE"}
			if e = c.saveControl(ctx, key, "image-update", v.ID, u, 0); e != nil {
				return e
			}
			u, d, e = readControl[ImageUpdate](ctx, c.store, key, "image-update")
		}
		if e != nil {
			return e
		}
		state.CandidateID = key
		if policy.Automatic && u.Status == "AVAILABLE" {
			_, e = c.ImageUpdateAction(ctx, v.ID, key, "deploy", "image-updater", d.Revision)
			if errors.Is(e, storage.ErrConflict) || errors.Is(e, storage.ErrPrecondition) {
				return nil
			}
			return e
		}
		return nil
	}()
	// Merge concurrent webhook requests rather than overwriting them with the
	// collector's older copy of the state.
	for i := 0; i < 4; i++ {
		current, e := c.GetImageCheck(parent, v.ID)
		if e != nil || current.Worker != worker {
			return
		}
		current.LeaseUntil = time.Time{}
		current.Worker = ""
		current.LastChecked = &now
		current.CandidateID = state.CandidateID
		delay := time.Duration(policy.IntervalSeconds) * time.Second
		if resultErr == nil {
			current.LastSuccess = &now
			current.Error = ""
			current.Failures = 0
		} else {
			current.Error = resultErr.Error()
			current.Failures++
			backoff := time.Minute * time.Duration(1<<min(current.Failures, 6))
			if backoff > delay {
				delay = backoff
			}
			var re *images.RegistryError
			if errors.As(resultErr, &re) && re.RetryAfter > delay {
				delay = re.RetryAfter
			}
		}
		var random [1]byte
		rand.Read(random[:]) // Fills the buffer or terminates; there is no error return path.
		delay += time.Duration(random[0]) * delay / 1280
		current.NextCheck = c.now().Add(delay)
		if e = c.saveControl(parent, v.ID+":image-check", "image-check", v.ID, current, current.Revision); e == nil {
			return
		}
	}
}
func (c *Core) GetHook(ctx context.Context, key string) (Hook, error) {
	if _, e := c.GetEnvironment(ctx, key); e != nil {
		return Hook{}, e
	}
	h, d, e := readControl[Hook](ctx, c.store, key+":hook", "hook")
	if errors.Is(e, storage.ErrNotFound) {
		return Hook{EnvironmentID: key}, nil
	}
	h.ControlDocument = d
	h.EncryptedSecret = nil
	return h, e
}
func (c *Core) RotateHook(ctx context.Context, key string, expected int64) (Hook, string, error) {
	if e := c.requireControlManagement(); e != nil {
		return Hook{}, "", e
	}
	if _, e := c.GetEnvironment(ctx, key); e != nil {
		return Hook{}, "", e
	}
	secret := make([]byte, 32)
	rand.Read(secret) // Fills the buffer or terminates on entropy failure.
	encoded := hex.EncodeToString(secret)
	cipher, e := c.keyring.Encrypt([]byte(encoded), []byte("image-hook:"+key))
	if e != nil {
		return Hook{}, "", e
	}
	h := Hook{EnvironmentID: key, HookID: strings.TrimPrefix(key, "env_"), EncryptedSecret: cipher}
	if e = c.saveControl(ctx, key+":hook", "hook", key, h, expected); e != nil {
		return h, "", e
	}
	h, e = c.GetHook(ctx, key)
	return h, encoded, e
}
func (c *Core) ReceiveImageHook(ctx context.Context, hook, timestamp, event, signature string, body []byte) (ImageCheck, error) {
	key := "env_" + hook
	policy, e := c.GetImagePolicy(ctx, key)
	if e != nil || !policy.Enabled || !policy.Webhook {
		return ImageCheck{}, storage.ErrNotFound
	}
	h, _, e := readControl[Hook](ctx, c.store, key+":hook", "hook")
	if e != nil {
		return ImageCheck{}, e
	}
	seconds, e := strconv.ParseInt(timestamp, 10, 64)
	if e != nil || c.now().Sub(time.Unix(seconds, 0)).Abs() > 5*time.Minute || len(event) == 0 || len(event) > 128 {
		return ImageCheck{}, invalid("auth.required", "", "invalid webhook timestamp or event")
	}
	secret, _, e := c.keyring.Decrypt(h.EncryptedSecret, []byte("image-hook:"+key))
	if e != nil {
		return ImageCheck{}, e
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp + "." + event + "."))
	mac.Write(body)
	supplied, e := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if e != nil || !hmac.Equal(supplied, mac.Sum(nil)) {
		return ImageCheck{}, invalid("auth.required", "", "invalid webhook signature")
	}
	var payload struct {
		ServiceNames []string `json:"services,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if len(body) > 0 && (decoder.Decode(&payload) != nil || decoder.Decode(&struct{}{}) != io.EOF) {
		return ImageCheck{}, controlInvalid("body", "invalid webhook body")
	}
	allowed := map[string]bool{}
	for _, s := range policy.Services {
		allowed[s] = true
	}
	for _, s := range payload.ServiceNames {
		if !allowed[s] {
			return ImageCheck{}, controlInvalid("services", "service is not tracked")
		}
	}
	state, e := c.GetImageCheck(ctx, key)
	if e != nil {
		return state, e
	}
	state.Requested = true
	eventKey := "hook-event:" + digestString(hook+":"+event)
	eventWrite, e := c.controlWrite(eventKey, "hook-event", key, map[string]string{"body_sha256": digestString(string(body))}, 0)
	if e != nil {
		return state, e
	}
	stateWrite, e := c.controlWrite(key+":image-check", "image-check", key, state, state.Revision)
	if e != nil {
		return state, e
	}
	if e = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{eventWrite, stateWrite}); e != nil {
		return state, e
	}
	return c.GetImageCheck(ctx, key)
}

// Recover the crash window after the durable claim. The operation key encodes
// the claim revision, so a committed deployment can always be reattached. A
// claim with no operation is quarantined; only explicit retry starts a new one.
func (c *Core) recoverImageSubmissions(ctx context.Context, env Environment) {
	page := storage.Page{Limit: 200}
	for {
		docs, err := c.store.ListControlDocuments(ctx, "image-update", env.ID, page)
		if err != nil {
			return
		}
		for _, d := range docs {
			var u ImageUpdate
			if json.Unmarshal(d.Payload, &u) != nil || u.Status != "SUBMITTING" || c.now().Sub(d.UpdatedAt) < 2*time.Minute {
				continue
			}
			u.ControlDocument = d
			op, err := c.store.GetDeploymentOperationByIdempotency(ctx, env.RuntimeID, "image-update:"+d.ID+":"+strconv.FormatInt(d.Revision, 10))
			if err == nil {
				u.OperationID = op.ID
				u.Status = string(op.Status)
				u.Error = op.ErrorMessage
			} else if errors.Is(err, storage.ErrNotFound) {
				u.Status = "FAILED"
				u.Error = "Submission interrupted before enqueue; explicit retry required"
			} else {
				continue
			}
			_ = c.saveControl(ctx, d.ID, "image-update", env.ID, u, d.Revision)
		}
		if len(docs) < 200 {
			return
		}
		page.After = docs[len(docs)-1].ID
	}
}
