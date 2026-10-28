package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/marketplace"
	"github.com/yernsun/network-service-composer/internal/model"
)

// fetchMarket is deliberately injectable only through the internal analysis helper.
// Production requests always use the catalog SSRF-protected HTTP client.
type marketFetch func(context.Context, string) ([]byte, error)

func fetchMarketFile(ctx context.Context, address string) ([]byte, error) {
	if err := validateCatalogURL(address); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := catalogHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("market returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 4<<20 {
		return nil, fmt.Errorf("market file exceeds 4 MiB")
	}
	return data, nil
}
func (c *Core) syncManifestSource(ctx context.Context, source model.CatalogSource) (model.CatalogSource, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	payload, err := fetchMarketFile(ctx, source.URL)
	if err != nil {
		return c.catalogSyncFailure(ctx, source, err)
	}
	versions, snapshot, err := c.analyzeMarket(ctx, source, payload, fetchMarketFile)
	if err != nil {
		return c.catalogSyncFailure(ctx, source, err)
	}
	source.MarketJSON = marketplace.JSON(snapshot)
	now := c.now()
	source.LastSyncedAt = &now
	source.UpdatedAt = now
	if err = c.store.SyncCatalogAppVersions(ctx, &source, versions, source.Revision); err != nil {
		return c.catalogSyncFailure(ctx, source, err)
	}
	source, err = c.store.GetCatalogSource(ctx, source.ID)
	return source, len(versions), err
}
func (c *Core) analyzeMarket(ctx context.Context, source model.CatalogSource, payload []byte, fetch marketFetch) ([]model.CatalogAppVersion, marketplace.Snapshot, error) {
	m, err := marketplace.Decode(payload)
	if err != nil {
		return nil, marketplace.Snapshot{}, err
	}
	if err = m.Validate(true); err != nil {
		return nil, marketplace.Snapshot{}, err
	}
	fail := func(err error) ([]model.CatalogAppVersion, marketplace.Snapshot, error) {
		return nil, marketplace.Snapshot{}, err
	}
	if err = validateCatalogURL(source.URL); err != nil {
		return fail(err)
	}
	base, _ := url.Parse(source.URL)
	expected := base.ResolveReference(&url.URL{Path: "./"}).String()
	if source.ApprovedEndpoint != "" {
		expected = source.ApprovedEndpoint
	}
	if m.Marketplace.Endpoint != expected {
		return fail(fmt.Errorf("market endpoint changed or differs from manifest location; confirm approved_endpoint %s before synchronizing", m.Marketplace.Endpoint))
	}
	if err = validateCatalogURL(m.Marketplace.Endpoint); err != nil {
		return fail(err)
	}
	endpoint, _ := url.Parse(m.Marketplace.Endpoint)
	cache := map[string][]byte{}
	total := len(payload)
	fileCount := 0
	read := func(f marketplace.File) (string, error) {
		if f.Path == "" {
			return "", nil
		}
		data, ok := cache[f.Path]
		if !ok {
			fileCount++
			if fileCount > 1000 {
				return "", fmt.Errorf("market exceeds 1000 files")
			}
			var e error
			data, e = fetch(ctx, endpoint.ResolveReference(&url.URL{Path: f.Path}).String())
			if e != nil {
				return "", fmt.Errorf("%s: %w", f.Path, e)
			}
			total += len(data)
			if total > 32<<20 {
				return "", fmt.Errorf("market exceeds 32 MiB")
			}
			cache[f.Path] = data
		}
		if marketplace.Digest(data) != f.SHA256 {
			return "", fmt.Errorf("%s SHA256 mismatch", f.Path)
		}
		return string(data), nil
	}
	documents := func(docs map[string]marketplace.Document) (map[string]string, error) {
		out := map[string]string{}
		for _, d := range docs {
			for _, f := range d.Files {
				v, e := read(f)
				if e != nil {
					return nil, e
				}
				out[f.Path] = v
			}
		}
		return out, nil
	}
	contents, err := documents(m.Marketplace.Docs)
	if err != nil {
		return fail(err)
	}
	snapshot := marketplace.Snapshot{Metadata: m.Marketplace, Documents: m.Marketplace.Docs, Contents: contents, ManifestSHA256: marketplace.Digest(payload)}
	versions := []model.CatalogAppVersion{}
	for _, a := range m.Apps {
		for _, v := range a.Versions {
			compose, e := read(v.Files.Compose)
			if e != nil {
				return fail(e)
			}
			swarm, e := read(v.Files.Swarm)
			if e != nil {
				return fail(e)
			}
			env, e := read(v.Files.EnvExample)
			if e != nil {
				return fail(e)
			}
			docs := map[string]marketplace.Document{}
			for k, d := range v.Files.Manuals {
				docs[k] = d
			}
			if len(v.Files.Readme) > 0 {
				// Manifest validation reserves this ID before any files are fetched.
				docs["readme"] = marketplace.Document{Title: marketplace.Text{"en": "README"}, Files: v.Files.Readme}
			}
			texts, e := documents(docs)
			if e != nil {
				return fail(e)
			}
			assets := map[string]string{}
			for logical, f := range v.Files.Assets {
				data, e := read(f)
				if e != nil {
					return fail(e)
				}
				media := http.DetectContentType([]byte(data))
				if media != "image/png" && media != "image/jpeg" && media != "image/gif" && media != "image/webp" {
					return fail(fmt.Errorf("unsupported image %s", logical))
				}
				assets[logical] = "data:" + media + ";base64," + base64.StdEncoding.EncodeToString([]byte(data))
			}
			defaults, e := applicationx.ParseDotEnv(env)
			if e != nil {
				return fail(e)
			}
			for _, x := range v.Variables {
				if x.Secret && defaults[x.Key] != "" {
					return fail(fmt.Errorf("secret variable %s has a value in env_example", x.Key))
				}
				if x.Default != "" {
					defaults[x.Key] = x.Default
				}
			}
			// Materialize the documented precedence before the existing installation preflight.
			if len(defaults) > 0 {
				keys := make([]string, 0, len(defaults))
				for k := range defaults {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				var merged strings.Builder
				for _, k := range keys {
					b, _ := json.Marshal(defaults[k])
					fmt.Fprintf(&merged, "%s=%s\n", k, b)
				}
				env = merged.String()
			}
			variables := []map[string]any{}
			for _, x := range v.Variables {
				var item map[string]any
				_ = json.Unmarshal([]byte(marketplace.JSON(x)), &item)
				item["description"] = x.Description.Default(m.Marketplace.DefaultLocale)
				variables = append(variables, item)
			}
			manifest := marketplace.JSON(map[string]any{"name": a.Name.Default(m.Marketplace.DefaultLocale), "description": a.Description.Default(m.Marketplace.DefaultLocale), "version": v.Version, "variables": variables, "recommended_domains": v.RecommendedDomains})
			readme := texts[v.Files.Readme[m.Marketplace.DefaultLocale].Path]
			files := map[string]string{"docker/docker-compose.yml": compose, "docker/swarm.yml": swarm, "docker/.env.example": env, "nsc-app.yaml": manifest, "README.md": readme}
			value, e := c.catalogVersionFromFiles(source.ID, a.ID, a.Name.Default(m.Marketplace.DefaultLocale), a.Description.Default(m.Marketplace.DefaultLocale), v.Version, v.Commit, catalogArtifactDigest(compose, env, manifest, readme, swarm), "", files, c.now())
			if e != nil {
				return fail(e)
			}
			meta := m.Marketplace
			meta.Docs = nil
			meta.Endpoint = "" // A relocated market does not change an application version.
			value.MarketJSON = marketplace.JSON(marketplace.Snapshot{Metadata: meta, Name: a.Name, Description: a.Description, Variables: v.Variables, Documents: docs, Contents: texts, Assets: assets, Commit: v.Commit})
			versions = append(versions, value)
		}
	}
	return versions, snapshot, nil
}

func ValidateApprovedEndpoint(value string) error {
	if value == "" {
		return nil
	}
	if !strings.HasSuffix(value, "/") {
		return fmt.Errorf("approved_endpoint must end with /")
	}
	return validateCatalogURL(value)
}

type catalogSyncCall struct {
	done   chan struct{}
	source model.CatalogSource
	count  int
	err    error
}

func (c *Core) SyncCatalogSource(ctx context.Context, sourceID string) (model.CatalogSource, int, error) {
	c.catalogMu.Lock()
	if c.catalogSyncs == nil {
		c.catalogSyncs = map[string]*catalogSyncCall{}
	}
	if call, ok := c.catalogSyncs[sourceID]; ok {
		c.catalogMu.Unlock()
		select {
		case <-ctx.Done():
			return model.CatalogSource{}, 0, ctx.Err()
		case <-call.done:
			return call.source, call.count, call.err
		}
	}
	call := &catalogSyncCall{done: make(chan struct{})}
	c.catalogSyncs[sourceID] = call
	c.catalogMu.Unlock()
	call.source, call.count, call.err = c.syncCatalogSource(ctx, sourceID)
	c.catalogMu.Lock()
	delete(c.catalogSyncs, sourceID)
	close(call.done)
	c.catalogMu.Unlock()
	return call.source, call.count, call.err
}
