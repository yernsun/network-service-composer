package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/marketplace"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/networkpolicy"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const builtinCatalogName = "NSC Built-in"

//go:embed all:builtin
var builtinCatalogFiles embed.FS

type CreateCatalogSourceInput struct {
	ApprovedEndpoint string                  `json:"approved_endpoint,omitempty"`
	Name             string                  `json:"name"`
	Type             model.CatalogSourceType `json:"type"`
	URL              string                  `json:"url,omitempty"`
	PinnedRef        string                  `json:"pinned_ref,omitempty"`
	Trusted          bool                    `json:"trusted"`
}

func (c *Core) CreateCatalogSource(ctx context.Context, input CreateCatalogSourceInput) (model.CatalogSource, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.URL = strings.TrimSpace(input.URL)
	input.PinnedRef = normalizeDigest(input.PinnedRef)
	if input.Name == "" {
		return model.CatalogSource{}, invalid("validation.catalog_name_required", "name", "catalog source name is required")
	}
	if input.Type != model.CatalogGit && input.Type != model.CatalogManifest {
		return model.CatalogSource{}, invalid("validation.catalog_source_type", "type", "API catalog sources must use GIT type")
	}
	if !input.Trusted {
		return model.CatalogSource{}, invalid("validation.catalog_trust_required", "trusted", "remote catalogs must be explicitly trusted")
	}
	if err := validateCatalogURL(input.URL); err != nil {
		return model.CatalogSource{}, invalid("validation.catalog_url_invalid", "url", err.Error())
	}
	if err := ValidateApprovedEndpoint(input.ApprovedEndpoint); err != nil {
		return model.CatalogSource{}, err
	}
	if input.Type == model.CatalogGit && !isDigest(input.PinnedRef) {
		return model.CatalogSource{}, invalid("validation.catalog_pin_required", "pinned_ref", "remote catalog pinned_ref must be a full Git commit or SHA256 digest")
	}
	if input.Type == model.CatalogGit && len(input.PinnedRef) == 40 && !catalogURLContainsCommit(input.URL, input.PinnedRef) {
		return model.CatalogSource{}, invalid("validation.catalog_pin_required", "pinned_ref", "a Git pinned_ref must appear as an immutable URL path segment; otherwise use the index SHA256")
	}
	now := c.now()
	value := model.CatalogSource{ID: id.New(id.CatalogSource), Name: input.Name, Type: input.Type, URL: input.URL,
		ApprovedEndpoint: input.ApprovedEndpoint, PinnedRef: input.PinnedRef, Trusted: true, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	return value, c.store.CreateCatalogSource(ctx, &value)
}

// ValidateCatalogSourceUpdate keeps mutation validation in the domain layer;
// transport handlers may expose a partial patch but cannot bypass URL or pin
// constraints before persisting it.
func (c *Core) ValidateCatalogSourceUpdate(value model.CatalogSource) error {
	if value.Type == model.CatalogManifest {
		if err := validateCatalogURL(value.URL); err != nil {
			return err
		}
		return ValidateApprovedEndpoint(value.ApprovedEndpoint)
	}
	if value.Type != model.CatalogGit {
		return invalid("validation.catalog_source_type", "type", "only GIT catalog sources can be updated")
	}
	if err := validateCatalogURL(value.URL); err != nil {
		return invalid("validation.catalog_url_invalid", "url", err.Error())
	}
	if !isDigest(value.PinnedRef) {
		return invalid("validation.catalog_pin_required", "pinned_ref", "pinned_ref must be an index SHA256 or full Git commit")
	}
	if len(value.PinnedRef) == 40 && !catalogURLContainsCommit(value.URL, value.PinnedRef) {
		return invalid("validation.catalog_pin_required", "pinned_ref", "a Git pinned_ref must appear as an immutable URL path segment; otherwise use the index SHA256")
	}
	return nil
}

type remoteCatalogIndex struct {
	SchemaVersion int                    `json:"schema_version"`
	Versions      []remoteCatalogVersion `json:"versions"`
}

type remoteCatalogVersion struct {
	AppKey         string            `json:"app_key"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Version        string            `json:"version"`
	Commit         string            `json:"commit"`
	ArtifactDigest string            `json:"artifact_digest"`
	ComposeSHA256  string            `json:"compose_sha256"`
	IconURL        string            `json:"icon_url"`
	Files          map[string]string `json:"files"`
}

func (c *Core) syncCatalogSource(ctx context.Context, sourceID string) (model.CatalogSource, int, error) {
	source, err := c.store.GetCatalogSource(ctx, sourceID)
	if err != nil {
		return source, 0, err
	}
	if source.Type == model.CatalogBuiltin {
		return source, 0, nil
	}
	if !source.Enabled || !source.Trusted {
		return source, 0, invalid("validation.catalog_source_disabled", "", "catalog source is disabled or untrusted")
	}
	if source.Type == model.CatalogManifest {
		return c.syncManifestSource(ctx, source)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return source, 0, err
	}
	client := catalogHTTPClient()
	response, err := client.Do(request)
	if err != nil {
		return c.catalogSyncFailure(ctx, source, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return c.catalogSyncFailure(ctx, source, fmt.Errorf("catalog returned HTTP %d", response.StatusCode))
	}
	const maxCatalogIndexBytes = 4 << 20
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogIndexBytes+1))
	if err != nil {
		return c.catalogSyncFailure(ctx, source, fmt.Errorf("read catalog index: %w", err))
	}
	if len(payload) > maxCatalogIndexBytes {
		return c.catalogSyncFailure(ctx, source, fmt.Errorf("catalog index exceeds %d bytes", maxCatalogIndexBytes))
	}
	if len(source.PinnedRef) == 64 {
		if digestBytes(payload) != source.PinnedRef {
			return c.catalogSyncFailure(ctx, source, fmt.Errorf("catalog index SHA256 does not match pinned_ref"))
		}
	} else if !catalogURLContainsCommit(source.URL, source.PinnedRef) {
		return c.catalogSyncFailure(ctx, source, fmt.Errorf("Git-pinned catalog URL must contain pinned_ref as an immutable path segment"))
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var index remoteCatalogIndex
	if err = decoder.Decode(&index); err != nil {
		return c.catalogSyncFailure(ctx, source, fmt.Errorf("decode catalog index: %w", err))
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		return c.catalogSyncFailure(ctx, source, fmt.Errorf("catalog index must contain one JSON document"))
	}
	if index.SchemaVersion != 1 {
		return c.catalogSyncFailure(ctx, source, fmt.Errorf("unsupported catalog schema_version %d", index.SchemaVersion))
	}
	versions := make([]model.CatalogAppVersion, 0, len(index.Versions))
	seenVersions := make(map[string]struct{}, len(index.Versions))
	for _, remote := range index.Versions {
		versionKey := strings.TrimSpace(remote.AppKey) + "\x00" + strings.TrimSpace(remote.Version)
		if _, duplicate := seenVersions[versionKey]; duplicate {
			return c.catalogSyncFailure(ctx, source, fmt.Errorf("catalog contains duplicate version %s/%s", remote.AppKey, remote.Version))
		}
		seenVersions[versionKey] = struct{}{}
		compose, exists := remote.Files["docker/docker-compose.yml"]
		if !exists || strings.TrimSpace(compose) == "" {
			return c.catalogSyncFailure(ctx, source, fmt.Errorf("%s %s is missing docker/docker-compose.yml", remote.AppKey, remote.Version))
		}
		if !isDigest(remote.Commit) {
			return c.catalogSyncFailure(ctx, source, fmt.Errorf("%s %s has an invalid immutable commit", remote.AppKey, remote.Version))
		}
		if len(source.PinnedRef) == 40 && normalizeDigest(remote.Commit) != source.PinnedRef {
			return c.catalogSyncFailure(ctx, source, fmt.Errorf("%s %s is not pinned to source Git commit", remote.AppKey, remote.Version))
		}
		if normalizeDigest(remote.ComposeSHA256) != digestString(compose) {
			return c.catalogSyncFailure(ctx, source, fmt.Errorf("%s %s Compose SHA256 mismatch", remote.AppKey, remote.Version))
		}
		artifact := catalogArtifactDigest(compose, remote.Files["docker/.env.example"], remote.Files["nsc-app.yaml"], remote.Files["README.md"])
		if normalizeDigest(remote.ArtifactDigest) != artifact {
			return c.catalogSyncFailure(ctx, source, fmt.Errorf("%s %s artifact SHA256 mismatch", remote.AppKey, remote.Version))
		}
		value, buildErr := c.catalogVersionFromFiles(source.ID, remote.AppKey, remote.Name, remote.Description, remote.Version,
			normalizeDigest(remote.Commit), artifact, remote.IconURL, remote.Files, c.now())
		if buildErr != nil {
			return c.catalogSyncFailure(ctx, source, buildErr)
		}
		versions = append(versions, value)
	}
	previousSyncedAt := source.LastSyncedAt
	now := c.now()
	source.LastSyncedAt = &now
	source.LastError = ""
	source.UpdatedAt = now
	err = c.store.SyncCatalogAppVersions(ctx, &source, versions, source.Revision)
	if err != nil {
		source.LastSyncedAt = previousSyncedAt
		return c.catalogSyncFailure(ctx, source, err)
	}
	source, err = c.store.GetCatalogSource(ctx, source.ID)
	return source, len(versions), err
}

func (c *Core) catalogSyncFailure(ctx context.Context, source model.CatalogSource, syncErr error) (model.CatalogSource, int, error) {
	source.LastError = syncErr.Error()
	source.UpdatedAt = c.now()
	_ = c.store.UpdateCatalogSource(ctx, &source, source.Revision)
	return source, 0, syncErr
}

func (c *Core) EnsureBuiltinCatalog(ctx context.Context) error {
	legacy := map[string]string{
		"docker/docker-compose.yml": "services:\n  web:\n    image: nginx:1.31.5-alpine3.24@sha256:72ba65eb42c10344912a84ff42408db7d34f2feb642204570ab8fc5ffd29f1d3\n    expose:\n      - \"80\"\n",
		"docker/.env.example":       "",
		"nsc-app.yaml":              "name: NGINX\nversion: 1.31.5\ndescription: Minimal NGINX web server\n",
		"README.md":                 "# NGINX\n\nA minimal built-in application for validating managed deployments.\n",
	}
	return c.ensureBuiltinCatalog(ctx, builtinCatalogFiles, legacy)
}

// Keep distribution reads replaceable so corrupt packaged material can be
// verified without rewriting immutable embedded artifacts.
func (c *Core) ensureBuiltinCatalog(ctx context.Context, assets fs.FS, legacy map[string]string) error {
	sources, err := c.listAllCatalogSources(ctx)
	if err != nil {
		return err
	}
	var source model.CatalogSource
	for _, value := range sources {
		if value.Type == model.CatalogBuiltin && value.Name == builtinCatalogName {
			source = value
			break
		}
	}
	now := c.now()
	if source.ID == "" {
		source = model.CatalogSource{ID: id.New(id.CatalogSource), Name: builtinCatalogName, Type: model.CatalogBuiltin,
			Trusted: true, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err = c.store.CreateCatalogSource(ctx, &source); err != nil {
			if !errors.Is(err, storage.ErrConflict) {
				return err
			}
			concurrent, readErr := c.listAllCatalogSources(ctx)
			if readErr != nil {
				return readErr
			}
			found := false
			for _, existing := range concurrent {
				if existing.Type == model.CatalogBuiltin && existing.Name == builtinCatalogName {
					source, found = existing, true
					break
				}
			}
			if !found {
				return err
			}
		}
	}
	market, sourceSnapshot, err := builtinMarketSnapshotsFrom(assets)
	if err != nil {
		return err
	}
	files := legacy
	artifact := catalogArtifactDigest(files["docker/docker-compose.yml"], "", files["nsc-app.yaml"], files["README.md"])
	version, err := c.catalogVersionFromFiles(source.ID, "nginx", "NGINX", "Minimal NGINX web server", "1.31.5",
		"builtin", artifact, "", files, now)
	if err != nil {
		return err
	}
	// Previous Controllers replay this legacy version without market metadata.
	// Keep its immutable snapshot unchanged so an image rollback can restart.
	// The static market publishes NGINX documentation independently.
	if err = c.store.UpsertCatalogAppVersion(ctx, &version); err != nil {
		return err
	}
	if source.MarketJSON != sourceSnapshot {
		source.MarketJSON = sourceSnapshot
		source.UpdatedAt = now
		if err = c.store.UpdateCatalogSource(ctx, &source, source.Revision); err != nil {
			if !errors.Is(err, storage.ErrPrecondition) {
				return err
			}
			current, readErr := c.store.GetCatalogSource(ctx, source.ID)
			if readErr != nil {
				return readErr
			}
			if current.Type != model.CatalogBuiltin || current.Name != builtinCatalogName || current.MarketJSON != sourceSnapshot {
				return err
			}
			source = current
		}
	}
	entries, err := fs.ReadDir(assets, "builtin")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "docs" || entry.Name() == "nginx" {
			continue
		}
		files := make(map[string]string)
		for sourceName, targetName := range map[string]string{"compose.yaml": "docker/docker-compose.yml", "manifest.yaml": "nsc-app.yaml", "README.md": "README.md"} {
			content, err := fs.ReadFile(assets, "builtin/"+entry.Name()+"/"+sourceName)
			if err != nil {
				return err
			}
			files[targetName] = string(content)
		}
		artifact := catalogArtifactDigest(files["docker/docker-compose.yml"], "", files["nsc-app.yaml"], files["README.md"])
		version, err := c.catalogVersionFromFiles(source.ID, entry.Name(), "", "", "", "builtin", artifact, "", files, now)
		if err != nil {
			return err
		}
		version.MarketJSON = market[entry.Name()]
		if err = c.store.UpsertCatalogAppVersion(ctx, &version); err != nil {
			return err
		}
	}

	data, err := fs.ReadFile(assets, "builtin/market.yaml")
	if err != nil {
		return err
	}
	manifest, err := marketplace.Decode(data)
	if err != nil {
		return err
	}
	for _, app := range manifest.Apps {
		for _, entry := range app.Versions {
			if entry.Files.Swarm.Path == "" {
				continue
			}
			content := map[string]string{}
			for file, target := range map[string]string{entry.Files.Compose.Path: "docker/docker-compose.yml", entry.Files.Swarm.Path: "docker/swarm.yml", entry.Files.EnvExample.Path: "docker/.env.example", entry.Files.Readme[manifest.Marketplace.DefaultLocale].Path: "README.md"} {
				if file == "" {
					continue
				}
				raw, e := fs.ReadFile(assets, "builtin/"+file)
				if e != nil {
					return e
				}
				content[target] = string(raw)
			}
			vars := []map[string]any{}
			for _, x := range entry.Variables {
				var item map[string]any
				if err := json.Unmarshal([]byte(marketplace.JSON(x)), &item); err != nil {
					return err
				}
				item["description"] = x.Description.Default(manifest.Marketplace.DefaultLocale)
				vars = append(vars, item)
			}
			content["nsc-app.yaml"] = marketplace.JSON(map[string]any{"name": app.Name.Default(manifest.Marketplace.DefaultLocale), "description": app.Description.Default(manifest.Marketplace.DefaultLocale), "version": entry.Version, "variables": vars, "recommended_domains": entry.RecommendedDomains})
			digest := catalogArtifactDigest(content["docker/docker-compose.yml"], content["docker/.env.example"], content["nsc-app.yaml"], content["README.md"], content["docker/swarm.yml"])
			version, err := c.catalogVersionFromFiles(source.ID, app.ID, "", "", entry.Version, "builtin", digest, "", content, now)
			if err != nil {
				return err
			}
			version.MarketJSON = market[app.ID+"@"+entry.Version]
			if err := c.store.UpsertCatalogAppVersion(ctx, &version); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Core) catalogVersionFromFiles(sourceID, appKey, name, description, version, commit, artifact, icon string, files map[string]string, now time.Time) (model.CatalogAppVersion, error) {
	compose := files["docker/docker-compose.yml"]
	if strings.TrimSpace(appKey) == "" || strings.TrimSpace(compose) == "" {
		return model.CatalogAppVersion{}, fmt.Errorf("catalog app_key and docker/docker-compose.yml are required")
	}
	preflight, err := applicationx.Preflight(applicationx.PreflightInput{ComposeYAML: compose, EnvExample: files["docker/.env.example"], ManifestYAML: files["nsc-app.yaml"], Backend: model.RuntimeDocker, CatalogAnalysis: true})
	if err != nil {
		return model.CatalogAppVersion{}, fmt.Errorf("catalog %s %s: %w", appKey, version, err)
	}
	swarm := files["docker/swarm.yml"]
	if swarm != "" {
		swarmFlight, swarmErr := applicationx.Preflight(applicationx.PreflightInput{ComposeYAML: swarm, EnvExample: files["docker/.env.example"], ManifestYAML: files["nsc-app.yaml"], Backend: model.RuntimeSwarm, CatalogAnalysis: true})
		if swarmErr != nil {
			return model.CatalogAppVersion{}, fmt.Errorf("catalog %s Swarm: %w", appKey, swarmErr)
		}
		preflight.SwarmCompatible = swarmFlight.SwarmCompatible
		preflight.CompatibilityIssues = swarmFlight.CompatibilityIssues
	}
	manifest, manifestErr := applicationx.ParseManifest(files["nsc-app.yaml"])
	if manifestErr != nil {
		return model.CatalogAppVersion{}, manifestErr
	}
	if name == "" {
		name = strings.TrimSpace(manifest.Name)
	}
	readmeName, readmeDescription := inferREADMEMetadata(files["README.md"])
	if name == "" {
		name = readmeName
	}
	if name == "" {
		name = appKey
	}
	if description == "" {
		description = strings.TrimSpace(manifest.Description)
	}
	if description == "" {
		description = readmeDescription
	}
	if version == "" {
		version = strings.TrimSpace(manifest.Version)
	}
	if version == "" {
		return model.CatalogAppVersion{}, fmt.Errorf("catalog %s version is required in the index or nsc-app.yaml", appKey)
	}
	services := make([]string, 0, len(preflight.Services))
	for _, service := range preflight.Services {
		services = append(services, service.Name)
	}
	return model.CatalogAppVersion{ID: id.New(id.CatalogVersion), SourceID: sourceID, AppKey: appKey, Name: name,
		Description: description, Version: version, Commit: commit, ArtifactDigest: artifact,
		ComposeSHA256: digestString(compose), ManifestJSON: files["nsc-app.yaml"], ComposeYAML: compose,
		EnvExample: files["docker/.env.example"], README: files["README.md"], IconURL: icon, Services: services,
		SwarmYAML: swarm, SwarmSHA256: optionalDigest(swarm),
		ComposeCompatible: true, SwarmCompatible: preflight.SwarmCompatible, CompatibilityIssues: preflight.CompatibilityIssues,
		Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}, nil
}

func inferREADMEMetadata(value string) (string, string) {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"), "\n")
	name, description := "", ""
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if name == "" && strings.HasPrefix(line, "# ") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			continue
		}
		if !strings.HasPrefix(line, "#") {
			description = line
			break
		}
	}
	return name, description
}

func validateCatalogURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("catalog URL must be an absolute HTTPS URL without credentials or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("catalog URL cannot target localhost")
	}
	if address := net.ParseIP(host); address != nil && (address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified()) {
		return fmt.Errorf("catalog URL cannot target a private address")
	}
	return nil
}

func catalogHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:               nil,
		TLSHandshakeTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(addresses) == 0 {
				return nil, fmt.Errorf("catalog host %q did not resolve", host)
			}
			for _, resolved := range addresses {
				if !publicCatalogAddress(resolved.IP) {
					return nil, fmt.Errorf("catalog host %q resolves to a private or non-public address", host)
				}
			}
			var lastErr error
			for _, resolved := range addresses {
				connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return fmt.Errorf("catalog redirects are not allowed")
	}}
}

func publicCatalogAddress(address net.IP) bool {
	parsed, ok := netip.AddrFromSlice(address)
	if !ok {
		return false
	}
	return networkpolicy.PublicAddress(parsed)
}

func isDigest(value string) bool {
	value = normalizeDigest(value)
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func normalizeDigest(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func catalogURLContainsCommit(rawURL, commit string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || len(commit) != 40 {
		return false
	}
	for _, segment := range strings.Split(parsed.EscapedPath(), "/") {
		decoded, decodeErr := url.PathUnescape(segment)
		if decodeErr == nil && strings.EqualFold(decoded, commit) {
			return true
		}
	}
	return false
}

func catalogArtifactDigest(compose, env, manifest, readme string, swarm ...string) string {
	hash := sha256.New()
	values := []string{compose, env, manifest, readme}
	if len(swarm) > 0 && swarm[0] != "" {
		values = append(values, swarm[0])
	}
	for _, value := range values {
		_, _ = hash.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (c *Core) listAllCatalogSources(ctx context.Context) ([]model.CatalogSource, error) {
	var result []model.CatalogSource
	after := ""
	for {
		page, err := c.store.ListCatalogSources(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		result = append(result, page...)
		if len(page) < 200 {
			return result, nil
		}
		after = page[len(page)-1].ID
	}
}

func optionalDigest(value string) string {
	if value == "" {
		return ""
	}
	return digestString(value)
}
