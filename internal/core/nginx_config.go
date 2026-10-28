package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type NginxConfiguration struct {
	AccessControls []model.IPAccessControl `json:"access_controls,omitempty"`
	storage.ControlDocument
	Snippets      []model.ConfigSnippet `json:"snippets"`
	Routes        []storage.RouteWrite  `json:"routes,omitempty"`
	Description   string                `json:"description"`
	Actor         string                `json:"actor,omitempty"`
	ParentVersion string                `json:"parent_version,omitempty"`
	VersionID     string                `json:"version_id,omitempty"`
}
type SandboxCheck struct {
	RouteID        string            `json:"route_id"`
	Path           string            `json:"path"`
	Method         string            `json:"method"`
	Scheme         string            `json:"scheme"`
	ExpectedStatus int               `json:"expected_status"`
	Headers        map[string]string `json:"headers,omitempty"`
	Host           string            `json:"host,omitempty"`
}
type SandboxSettings struct {
	storage.ControlDocument
	GatewayID string         `json:"gateway_id"`
	Checks    []SandboxCheck `json:"checks"`
}
type NginxValidationResult struct {
	Status         string          `json:"status"`
	Error          string          `json:"error,omitempty"`
	Checks         json.RawMessage `json:"checks,omitempty"`
	RenderedDigest string          `json:"rendered_digest,omitempty"`
}
type NginxValidation struct {
	HeadRevision int64 `json:"-"`
	storage.ControlDocument
	PoolID           string                           `json:"pool_id"`
	DraftRevision    int64                            `json:"draft_revision"`
	BaseRevision     int64                            `json:"base_revision"`
	Configuration    NginxConfiguration               `json:"configuration"`
	SandboxID        string                           `json:"sandbox_id,omitempty"`
	SandboxRevision  int64                            `json:"sandbox_revision"`
	Checks           []SandboxCheck                   `json:"checks"`
	Results          map[string]NginxValidationResult `json:"results"`
	Status           string                           `json:"status"`
	ExpiresAt        time.Time                        `json:"expires_at"`
	RendererVersion  string                           `json:"renderer_version"`
	SettingsRevision int64                            `json:"settings_revision"`
}
type NginxReleaseTarget struct {
	GatewayID         string     `json:"gateway_id"`
	Name              string     `json:"name"`
	BeforeRevision    int64      `json:"before_revision"`
	Status            string     `json:"status"`
	ConfirmedRevision int64      `json:"confirmed_revision"`
	ModelDigest       string     `json:"model_digest,omitempty"`
	RenderedDigest    string     `json:"rendered_digest,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	Error             string     `json:"error,omitempty"`
}

type NginxRelease struct {
	Targets    []NginxReleaseTarget `json:"targets"`
	FinishedAt *time.Time           `json:"finished_at,omitempty"`
	Stopped    bool                 `json:"stopped"`
	storage.ControlDocument
	PoolID         string          `json:"pool_id"`
	VersionID      string          `json:"version_id"`
	ValidationID   string          `json:"validation_id"`
	ConfigRevision int64           `json:"config_revision"`
	Status         string          `json:"status"`
	Gateways       []model.Gateway `json:"gateways"`
	Actor          string          `json:"actor"`
}

func (c *Core) NginxConfiguration(ctx context.Context, pool, kind string) (NginxConfiguration, error) {
	if _, e := c.store.GetPool(ctx, pool); e != nil {
		return NginxConfiguration{}, e
	}
	v, d, e := readControl[NginxConfiguration](ctx, c.store, pool+":"+kind, kind)
	if errors.Is(e, storage.ErrNotFound) {
		return NginxConfiguration{ControlDocument: storage.ControlDocument{ID: pool + ":" + kind}, Snippets: []model.ConfigSnippet{}}, nil
	}
	v.ControlDocument = d
	return v, e
}
func (c *Core) validateNginxConfiguration(ctx context.Context, pool string, in NginxConfiguration) error {
	if e := c.requireControlManagement(); e != nil {
		return e
	}
	p, e := c.store.GetPool(ctx, pool)
	if e != nil {
		return e
	}
	if !p.Enabled {
		return invalid("management.disabled", "", "gateway pool is disabled")
	}
	if len(in.Snippets) > 1000 {
		return controlInvalid("snippets", "too many snippets")
	}
	if len(in.Routes) > 1000 {
		return controlInvalid("routes", "too many route changes")
	}
	if e := c.validateNginxRoutes(ctx, pool, in.Routes); e != nil {
		return e
	}
	if e := c.validateIPAccessControls(ctx, pool, in); e != nil {
		return e
	}
	seen := map[string]bool{}
	for i, s := range in.Snippets {
		if s.ResourceID != "" {
			if err := c.store.CheckProjectResource(ctx, s.ResourceID); err != nil {
				return err
			}
		}
		key := s.Scope + ":" + s.ResourceID
		if seen[key] {
			return controlInvalid("snippets", "duplicate scope and resource")
		}
		seen[key] = true
		if _, e = nginxx.ParseSnippet(s.Scope, s.Content); e != nil {
			return controlInvalid(fmt.Sprintf("snippets/%d/content", i), e.Error())
		}
		switch s.Scope {
		case "http":
			if s.ResourceID != "" {
				return controlInvalid("resource_id", "HTTP snippets belong to the pool")
			}
		case "server":
			d, e := c.store.GetDomain(ctx, s.ResourceID)
			if e != nil {
				return e
			}
			if d.PoolID != pool {
				return storage.ErrNotFound
			}
		case "location":
			r, e := c.store.GetRoute(ctx, s.ResourceID)
			if errors.Is(e, storage.ErrNotFound) {
				for _, change := range in.Routes {
					if change.Route.ID == s.ResourceID && !change.Delete {
						r = change.Route
						e = nil
						break
					}
				}
			}
			if e != nil {
				return e
			}
			d, e := c.store.GetDomain(ctx, r.DomainID)
			if e != nil {
				return e
			}
			if d.PoolID != pool {
				return storage.ErrNotFound
			}
		}
	}
	return nil
}
func (c *Core) SaveNginxDraft(ctx context.Context, pool string, in NginxConfiguration, actor string, expected int64) (NginxConfiguration, error) {
	in.AccessControls = append([]model.IPAccessControl(nil), in.AccessControls...)
	for i := range in.AccessControls {
		if in.AccessControls[i].Access == nil {
			in.AccessControls[i].Access = []string{}
		}
		if in.AccessControls[i].Forbidden == nil {
			in.AccessControls[i].Forbidden = []string{}
		}
	}
	for i := range in.Routes {
		route := &in.Routes[i].Route
		if route.ID == "" {
			route.ID = id.New(id.Route)
		}
		if route.Kind == "" {
			route.Kind = "proxy"
		}
		if route.Match == "" {
			route.Match = "prefer"
		}
		if route.ReadTimeoutSeconds == 0 {
			route.ReadTimeoutSeconds = 60
		}
		if route.ConnectTimeoutSeconds == 0 {
			route.ConnectTimeoutSeconds = 5
		}
		route.Source = model.SourceAPI
		if route.Enabled {
			route.Status = model.RouteActive
		} else {
			route.Status = model.RouteDisabled
		}
	}
	if e := c.validateNginxConfiguration(ctx, pool, in); e != nil {
		return in, e
	}
	sort.Slice(in.Snippets, func(i, j int) bool {
		return in.Snippets[i].Scope+in.Snippets[i].ResourceID < in.Snippets[j].Scope+in.Snippets[j].ResourceID
	})
	in.ControlDocument = storage.ControlDocument{}
	in.Actor = actor
	draft, e := c.controlWrite(pool+":nginx-draft", "nginx-draft", pool, in, expected)
	if e != nil {
		return in, e
	}
	history, e := c.controlWrite(id.New("ngdraftver"), "nginx-draft-version", pool, in, 0)
	if e != nil {
		return in, e
	}
	if e = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{draft, history}); e != nil {
		return in, e
	}
	_ = c.store.PruneControlHistory(ctx, "nginx-draft-version", pool, 100, nil)
	return c.NginxConfiguration(ctx, pool, "nginx-draft")
}
func (c *Core) ListNginxDraftVersions(ctx context.Context, pool string, page storage.Page) ([]NginxConfiguration, error) {
	return c.listNginxConfigurations(ctx, pool, "nginx-draft-version", page)
}
func (c *Core) ListNginxVersions(ctx context.Context, pool string, page storage.Page) ([]NginxConfiguration, error) {
	return c.listNginxConfigurations(ctx, pool, "nginx-version", page)
}
func (c *Core) listNginxConfigurations(ctx context.Context, pool, kind string, page storage.Page) ([]NginxConfiguration, error) {
	if _, e := c.store.GetPool(ctx, pool); e != nil {
		return nil, e
	}
	docs, e := c.store.ListControlDocuments(ctx, kind, pool, page)
	result := []NginxConfiguration{}
	for _, d := range docs {
		var v NginxConfiguration
		if err := json.Unmarshal(d.Payload, &v); err != nil {
			return nil, err
		}
		v.ControlDocument = d
		result = append(result, v)
	}
	return result, e
}
func (c *Core) RestoreNginxVersion(ctx context.Context, pool, version, actor string, expected int64) (NginxConfiguration, error) {
	v, d, e := readControl[NginxConfiguration](ctx, c.store, version, "nginx-version")
	if e != nil {
		return v, e
	}
	if d.ParentID != pool {
		return v, storage.ErrNotFound
	}
	v.ParentVersion = version
	return c.SaveNginxDraft(ctx, pool, v, actor, expected)
}
func (c *Core) GetSandbox(ctx context.Context, pool string) (SandboxSettings, error) {
	if _, e := c.store.GetPool(ctx, pool); e != nil {
		return SandboxSettings{}, e
	}
	v, d, e := readControl[SandboxSettings](ctx, c.store, pool+":sandbox", "sandbox")
	if errors.Is(e, storage.ErrNotFound) {
		return SandboxSettings{Checks: []SandboxCheck{}}, nil
	}
	v.ControlDocument = d
	return v, e
}
func (c *Core) SaveSandbox(ctx context.Context, pool string, v SandboxSettings, expected int64) (SandboxSettings, error) {
	if e := c.requireControlManagement(); e != nil {
		return v, e
	}
	if _, e := c.store.GetPool(ctx, pool); e != nil {
		return v, e
	}
	writes := []storage.ControlWrite{}
	if v.GatewayID != "" {
		if e := id.Validate(v.GatewayID, id.Gateway); e != nil {
			return v, invalid("validation.application_identity_invalid", "gateway_id", e.Error())
		}
		if _, e := c.store.GetGateway(ctx, v.GatewayID); e == nil {
			return v, invalid("validation.application_identity_invalid", "gateway_id", "use a dedicated gateway identity; production gateways cannot be converted")
		} else if !errors.Is(e, storage.ErrNotFound) {
			return v, e
		}
		role, d, e := readControl[map[string]string](ctx, c.store, v.GatewayID+":role", "gateway-role")
		if errors.Is(e, storage.ErrNotFound) {
			w, e := c.controlWrite(v.GatewayID+":role", "gateway-role", pool, map[string]string{"mode": "sandbox", "pool_id": pool}, 0)
			if e != nil {
				return v, e
			}
			writes = append(writes, w)
		} else if e != nil {
			return v, e
		} else if role["mode"] != "sandbox" || d.ParentID != pool {
			return v, storage.ErrConflict
		}
	}
	if len(v.Checks) > 50 {
		return v, controlInvalid("checks", "at most 50 checks are allowed")
	}
	for i := range v.Checks {
		check := &v.Checks[i]
		r, e := c.store.GetRoute(ctx, check.RouteID)
		if e != nil {
			return v, e
		}
		d, e := c.store.GetDomain(ctx, r.DomainID)
		if e != nil {
			return v, e
		}
		if d.PoolID != pool {
			return v, storage.ErrNotFound
		}
		check.Host = d.Host
		if strings.Contains(d.Host, "*") {
			return v, controlInvalid("checks", "wildcard hosts require a concrete route test and are not supported")
		}
		if check.Method == "" {
			check.Method = "GET"
		}
		if check.Method != "GET" && check.Method != "HEAD" {
			return v, controlInvalid("method", "checks support GET and HEAD only")
		}
		if check.Scheme == "" {
			check.Scheme = "http"
		}
		if check.Scheme != "http" && check.Scheme != "https" && check.Scheme != "http3" {
			return v, controlInvalid("scheme", "expected http, https or http3")
		}
		if !strings.HasPrefix(check.Path, "/") || strings.HasPrefix(check.Path, "//") || strings.ContainsAny(check.Path, "\r\n\\") {
			return v, controlInvalid("path", "expected a local request path")
		}
		if check.ExpectedStatus < 100 || check.ExpectedStatus > 599 {
			return v, controlInvalid("expected_status", "expected an HTTP status")
		}
	}
	v.ControlDocument = storage.ControlDocument{}
	w, e := c.controlWrite(pool+":sandbox", "sandbox", pool, v, expected)
	if e != nil {
		return v, e
	}
	writes = append(writes, w)
	if e = c.store.WriteControlDocuments(ctx, writes); e != nil {
		return v, e
	}
	return c.GetSandbox(ctx, pool)
}
func (c *Core) GatewayMode(ctx context.Context, gateway, pool string) (string, error) {
	v, d, e := readControl[map[string]string](ctx, c.store, gateway+":role", "gateway-role")
	if errors.Is(e, storage.ErrNotFound) {
		return "production", nil
	}
	if e != nil {
		return "", e
	}
	if d.ParentID != pool {
		return "", storage.ErrNotFound
	}
	return v["mode"], nil
}
func (c *Core) SandboxHeartbeat(ctx context.Context, gateway, pool string) error {
	d, e := c.store.GetControlDocument(ctx, gateway+":sandbox-status")
	if e != nil && !errors.Is(e, storage.ErrNotFound) {
		return e
	}
	return c.saveControl(ctx, gateway+":sandbox-status", "sandbox-status", pool, map[string]any{"last_seen_at": c.now()}, d.Revision)
}
func (c *Core) onlineProductionGateways(ctx context.Context, pool string) ([]model.Gateway, error) {
	result := []model.Gateway{}
	page := storage.Page{Limit: 200}
	for {
		values, e := c.store.ListGateways(ctx, page)
		if e != nil {
			return nil, e
		}
		for _, v := range values {
			if v.PoolID == pool && v.Enabled && v.LastSeenAt != nil && c.now().Sub(*v.LastSeenAt) < c.config.Controller.Gateway.OfflineTimeout.Std() {
				if v.RendererVersion != nginxx.RendererVersion {
					return nil, controlInvalid("gateways", "an online gateway requires a renderer upgrade")
				}
				result = append(result, v)
			}
		}
		if len(values) < 200 {
			break
		}
		page.After = values[len(values)-1].ID
	}
	return result, nil
}
func (c *Core) CreateNginxValidation(ctx context.Context, pool string, expected int64) (NginxValidation, error) {
	draft, e := c.NginxConfiguration(ctx, pool, "nginx-draft")
	if e != nil {
		return NginxValidation{}, e
	}
	if draft.Revision != expected || expected == 0 {
		return NginxValidation{}, storage.ErrPrecondition
	}
	if e = c.validateNginxConfiguration(ctx, pool, draft); e != nil {
		return NginxValidation{}, e
	}
	if _, e = c.nginxCandidateSnapshot(ctx, pool, draft); e != nil {
		return NginxValidation{}, e
	}
	p, e := c.store.GetPool(ctx, pool)
	if e != nil {
		return NginxValidation{}, e
	}
	settings, e := c.GetSandbox(ctx, pool)
	if e != nil {
		return NginxValidation{}, e
	}
	gateways, e := c.onlineProductionGateways(ctx, pool)
	if e != nil {
		return NginxValidation{}, e
	}
	if len(gateways) == 0 {
		return NginxValidation{}, controlInvalid("gateways", "no compatible production gateway is online")
	}
	v := NginxValidation{PoolID: pool, DraftRevision: draft.Revision, BaseRevision: p.DesiredRevision, Configuration: draft, SandboxID: settings.GatewayID, SandboxRevision: c.now().UnixNano(), Checks: settings.Checks, Results: map[string]NginxValidationResult{}, Status: "PENDING", ExpiresAt: c.now().Add(5 * time.Minute), RendererVersion: nginxx.RendererVersion, SettingsRevision: settings.Revision}
	v.ID = id.New("ngval")
	for _, g := range gateways {
		v.Results[g.ID] = NginxValidationResult{Status: "PENDING"}
	}
	if settings.GatewayID != "" {
		status, e := c.store.GetControlDocument(ctx, settings.GatewayID+":sandbox-status")
		if e != nil || c.now().Sub(status.UpdatedAt) > 15*time.Second {
			return v, controlInvalid("sandbox", "configured sandbox is offline")
		}
		v.Results[settings.GatewayID] = NginxValidationResult{Status: "PENDING"}
	}
	head, err := c.store.GetControlDocument(ctx, pool+":nginx-validation-head")
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return v, err
	}
	taskWrite, e := c.controlWrite(v.ID, "nginx-validation", pool, v, 0)
	if e != nil {
		return v, e
	}
	headWrite, e := c.controlWrite(pool+":nginx-validation-head", "nginx-validation-head", pool, map[string]string{"task_id": v.ID}, head.Revision)
	if e != nil {
		return v, e
	}
	if e = c.store.WriteControlDocuments(ctx, []storage.ControlWrite{taskWrite, headWrite}); e != nil {
		return v, e
	}
	return c.GetNginxValidation(ctx, pool, v.ID)
}
func (c *Core) GetNginxValidation(ctx context.Context, pool, key string) (NginxValidation, error) {
	v, d, e := readControl[NginxValidation](ctx, c.store, key, "nginx-validation")
	v.ControlDocument = d
	if e != nil {
		return v, e
	}
	if d.ParentID != pool {
		return v, storage.ErrNotFound
	}
	head, hd, err := readControl[map[string]string](ctx, c.store, pool+":nginx-validation-head", "nginx-validation-head")
	if err != nil {
		return v, err
	}
	v.HeadRevision = hd.Revision
	if head["task_id"] != key {
		v.Status = "EXPIRED"
		return v, nil
	}
	p, err := c.store.GetPool(ctx, pool)
	if err != nil {
		return v, err
	}
	if p.DesiredRevision != v.BaseRevision {
		v.Status = "EXPIRED"
		return v, nil
	}
	if v.Status == "CANCELLED" {
		return v, nil
	}
	if !v.ExpiresAt.After(c.now()) {
		v.Status = "EXPIRED"
		return v, nil
	}
	v.Status = "SUCCEEDED"
	for _, r := range v.Results {
		if r.Status == "FAILED" {
			v.Status = "FAILED"
			break
		}
		if r.Status != "SUCCEEDED" {
			v.Status = "RUNNING"
		}
	}
	return v, nil
}
func (c *Core) CancelNginxValidation(ctx context.Context, pool, key string, expected int64) error {
	if e := c.requireControlManagement(); e != nil {
		return e
	}
	v, e := c.GetNginxValidation(ctx, pool, key)
	if e != nil {
		return e
	}
	v.Status = "CANCELLED"
	return c.saveControl(ctx, key, "nginx-validation", pool, v, expected)
}
func (c *Core) NextNginxValidation(ctx context.Context, pool, gateway string) (*NginxValidation, *model.Snapshot, error) {
	page := storage.Page{Limit: 200}
	for {
		docs, e := c.store.ListControlDocuments(ctx, "nginx-validation", pool, page)
		if e != nil {
			return nil, nil, e
		}
		for _, d := range docs {
			v, e := c.GetNginxValidation(ctx, pool, d.ID)
			if e != nil {
				return nil, nil, e
			}
			if v.Status != "RUNNING" || v.Results[gateway].Status != "PENDING" {
				continue
			}
			if v.SandboxID != "" && gateway != v.SandboxID && v.Results[v.SandboxID].Status != "SUCCEEDED" {
				continue
			}
			snapshot, e := c.nginxCandidateSnapshot(ctx, pool, v.Configuration)
			if e != nil {
				return nil, nil, e
			}
			if snapshot.Revision != v.BaseRevision {
				continue
			}
			snapshot.Revision = v.BaseRevision + 1
			if gateway == v.SandboxID {
				snapshot.Revision = v.SandboxRevision
			}
			v.Results[gateway] = NginxValidationResult{Status: "RUNNING"}
			if e = c.saveControl(ctx, v.ID, "nginx-validation", pool, v, d.Revision); e != nil {
				return nil, nil, e
			}
			return &v, &snapshot, nil
		}
		if len(docs) < 200 {
			break
		}
		page.After = docs[len(docs)-1].ID
	}
	return nil, nil, nil
}
func (c *Core) FinishNginxValidation(ctx context.Context, pool, key, gateway string, result NginxValidationResult) error {
	for i := 0; i < 4; i++ {
		v, e := c.GetNginxValidation(ctx, pool, key)
		if e != nil {
			return e
		}
		if v.Status == "CANCELLED" || v.Status == "EXPIRED" || v.Results[gateway].Status != "RUNNING" {
			return storage.ErrPrecondition
		}
		if result.Status != "SUCCEEDED" && result.Status != "FAILED" {
			return storage.ErrConflict
		}
		v.Results[gateway] = result
		if e = c.saveControl(ctx, key, "nginx-validation", pool, v, v.Revision); e == nil {
			return nil
		} else if !errors.Is(e, storage.ErrPrecondition) {
			return e
		}
	}
	return storage.ErrConflict
}
func (c *Core) PublishNginx(ctx context.Context, pool, validation, actor, idem string, expected int64) (NginxRelease, error) {
	if idem == "" {
		return NginxRelease{}, controlInvalid("idempotency_key", "an idempotency key is required")
	}
	releaseID := "ngrel_" + digestString(pool+":"+idem)
	if r, d, e := readControl[NginxRelease](ctx, c.store, releaseID, "nginx-release"); e == nil {
		if r.ValidationID != validation {
			return r, storage.ErrConflict
		}
		r.ControlDocument = d
		return r, nil
	} else if !errors.Is(e, storage.ErrNotFound) {
		return r, e
	}
	v, e := c.GetNginxValidation(ctx, pool, validation)
	if e != nil {
		return NginxRelease{}, e
	}
	if v.Status != "SUCCEEDED" {
		return NginxRelease{}, controlInvalid("validation_id", "validation has not succeeded or has expired")
	}
	draft, e := c.NginxConfiguration(ctx, pool, "nginx-draft")
	if e != nil {
		return NginxRelease{}, e
	}
	if expected != draft.Revision || draft.Revision != v.DraftRevision {
		return NginxRelease{}, storage.ErrPrecondition
	}
	if e = c.validateNginxConfiguration(ctx, pool, draft); e != nil {
		return NginxRelease{}, e
	}
	settings, e := c.GetSandbox(ctx, pool)
	if e != nil {
		return NginxRelease{}, e
	}
	if settings.Revision != v.SettingsRevision {
		return NginxRelease{}, storage.ErrPrecondition
	}
	gateways, e := c.onlineProductionGateways(ctx, pool)
	if e != nil {
		return NginxRelease{}, e
	}
	expectedCount := len(v.Results)
	if v.SandboxID != "" {
		expectedCount--
	}
	if len(gateways) != expectedCount {
		return NginxRelease{}, storage.ErrPrecondition
	}
	for _, g := range gateways {
		evidence, err := c.GatewayEvidence(ctx, g.ID)
		if err != nil {
			return NginxRelease{}, err
		}
		if evidence.StartupState == "RISK" || evidence.ConfigurationState == "EXTERNAL_CHANGE" {
			return NginxRelease{}, controlInvalid("gateways", "resolve startup configuration risk or external changes before publishing")
		}
		if v.Results[g.ID].Status != "SUCCEEDED" {
			return NginxRelease{}, storage.ErrPrecondition
		}
	}
	active, e := c.NginxConfiguration(ctx, pool, "nginx-active")
	if e != nil {
		return NginxRelease{}, e
	}
	routeChanges := draft.Routes
	version := draft
	draft.Routes = nil
	version.ID = id.New("ngver")
	version.ParentVersion = active.VersionID
	version.VersionID = version.ID
	version.Actor = actor
	release := NginxRelease{PoolID: pool, VersionID: version.ID, ValidationID: validation, ConfigRevision: v.BaseRevision + 1, Status: "PUBLISHING", Actor: actor}
	release.ID = releaseID
	for _, g := range gateways {
		release.Targets = append(release.Targets, NginxReleaseTarget{GatewayID: g.ID, Name: g.Name, BeforeRevision: g.AppliedRevision, Status: "PENDING"})
	}
	all, err := c.productionGateways(ctx, pool)
	if err != nil {
		return release, err
	}
	if len(all) != len(gateways) || len(all) == 0 {
		return release, controlInvalid("gateways", "all selected gateways must be online and verified before publication")
	}
	snapshot, err := c.nginxCandidateSnapshot(ctx, pool, version)
	if err != nil {
		return release, err
	}
	if snapshot.Revision != v.BaseRevision {
		return release, storage.ErrPrecondition
	}
	snapshot.Revision = release.ConfigRevision
	frozen, err := c.freezeReleaseSnapshot(releaseID, pool, snapshot)
	if err != nil {
		return release, err
	}
	head, err := c.store.GetControlDocument(ctx, pool+":nginx-release-head")
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return release, err
	}
	if head.Revision > 0 {
		var old map[string]string
		if err = json.Unmarshal(head.Payload, &old); err != nil {
			return release, err
		}
		previous, err := c.GetNginxRelease(ctx, pool, old["release_id"])
		if err != nil {
			return release, err
		}
		for _, target := range previous.Targets {
			if target.Status == "RUNNING" || target.Status == "UNKNOWN" {
				return release, controlInvalid("gateways", "verify the previous in-flight operation before publishing")
			}
		}
	}
	writes := []storage.ControlWrite{frozen}
	for _, item := range []struct {
		key, kind string
		value     any
		expected  int64
	}{{pool + ":nginx-release-head", "nginx-release-head", map[string]string{"release_id": releaseID}, head.Revision}, {version.ID, "nginx-version", version, 0}, {pool + ":nginx-active", "nginx-active", version, active.Revision}, {releaseID, "nginx-release", release, 0}, {pool + ":nginx-draft", "nginx-draft", draft, draft.Revision}, {validation, "nginx-validation", v, v.Revision}, {pool + ":nginx-validation-head", "nginx-validation-head", map[string]string{"task_id": validation}, v.HeadRevision}, {pool + ":sandbox", "sandbox", settings, settings.Revision}} {
		w, e := c.controlWrite(item.key, item.kind, pool, item.value, item.expected)
		if e != nil {
			return release, e
		}
		writes = append(writes, w)
	}
	if len(routeChanges) == 0 {
		e = c.store.PublishNginxDocuments(ctx, pool, v.BaseRevision, writes)
	} else {
		e = c.store.PublishNginxChanges(ctx, pool, v.BaseRevision, writes, routeChanges)
	}
	if e != nil {
		return release, e
	}
	_ = c.store.PruneControlHistory(ctx, "nginx-version", pool, 100, []string{version.ID, active.VersionID})
	return c.GetNginxRelease(ctx, pool, releaseID)
}
func (c *Core) GetNginxRelease(ctx context.Context, pool, key string) (NginxRelease, error) {
	v, d, e := readControl[NginxRelease](ctx, c.store, key, "nginx-release")
	v.ControlDocument = d
	if e != nil {
		return v, e
	}
	if d.ParentID != pool {
		return v, storage.ErrNotFound
	}
	// Historical outcomes belong to the fixed operation, never to today's
	// pool membership or a numerically newer applied revision.
	v.Gateways = []model.Gateway{}
	for _, target := range v.Targets {
		g, err := c.store.GetGateway(ctx, target.GatewayID)
		if err == nil {
			v.Gateways = append(v.Gateways, g)
		} else if !errors.Is(err, storage.ErrNotFound) {
			return v, err
		}
	}
	summarizeRelease(&v, c.now())
	return v, nil
}

// Every transport reconnect (including either process restarting) invalidates
// previous tests. A new candidate also supersedes the previous validation head.
func (c *Core) InvalidateNginxValidation(ctx context.Context, pool string) error {
	d, e := c.store.GetControlDocument(ctx, pool+":nginx-validation-head")
	if errors.Is(e, storage.ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	return c.saveControl(ctx, d.ID, "nginx-validation-head", pool, map[string]string{"task_id": ""}, d.Revision)
}
