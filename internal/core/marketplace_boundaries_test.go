package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/marketplace"
	"github.com/yernsun/network-service-composer/internal/model"
)

func analysisFixture() (marketplace.Manifest, model.CatalogSource, map[string][]byte) {
	files := map[string][]byte{"compose.yaml": []byte("services:\n  web:\n    image: nginx:alpine\n    expose: [\"80\"]\n")}
	m := marketplace.Manifest{SchemaVersion: 1, Marketplace: marketplace.Metadata{
		ID: "boundary", Name: marketplace.Text{"en": "Boundary"}, Endpoint: "https://market.example/", DefaultLocale: "en", Locales: []string{"en"},
	}, Apps: []marketplace.App{{ID: "web", Name: marketplace.Text{"en": "Web"}, Versions: []marketplace.Version{{Version: "1", Files: marketplace.Files{
		Compose: marketplace.File{Path: "compose.yaml", SHA256: marketplace.Digest(files["compose.yaml"])},
	}}}}}}
	return m, model.CatalogSource{ID: "boundary", URL: "https://market.example/market.yaml", Type: model.CatalogManifest}, files
}

func TestMarketAnalysisRejectsIncompleteSnapshots(t *testing.T) {
	c, _, _ := controlFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*marketplace.Manifest, *model.CatalogSource, map[string][]byte)
		want   string
	}{
		{"invalid source URL", func(_ *marketplace.Manifest, s *model.CatalogSource, _ map[string][]byte) { s.URL = "https://[invalid" }, "catalog URL"},
		{"private endpoint", func(m *marketplace.Manifest, s *model.CatalogSource, _ map[string][]byte) {
			m.Marketplace.Endpoint = "https://127.0.0.1/"
			s.ApprovedEndpoint = m.Marketplace.Endpoint
		}, "private"},
		{"unapproved endpoint", func(m *marketplace.Manifest, _ *model.CatalogSource, _ map[string][]byte) {
			m.Marketplace.Endpoint = "https://other.example/"
		}, "approved_endpoint"},
		{"missing compose", func(_ *marketplace.Manifest, _ *model.CatalogSource, f map[string][]byte) { delete(f, "compose.yaml") }, "not available"},
		{"changed compose", func(_ *marketplace.Manifest, _ *model.CatalogSource, f map[string][]byte) {
			f["compose.yaml"] = []byte("changed")
		}, "SHA256 mismatch"},
		{"invalid compose", func(m *marketplace.Manifest, _ *model.CatalogSource, f map[string][]byte) {
			f["compose.yaml"] = []byte("services: [")
			m.Apps[0].Versions[0].Files.Compose.SHA256 = marketplace.Digest(f["compose.yaml"])
		}, "YAML_SYNTAX_ERROR"},
		{"missing market manual", func(m *marketplace.Manifest, _ *model.CatalogSource, _ map[string][]byte) {
			m.Marketplace.Docs = map[string]marketplace.Document{"guide": {Files: map[string]marketplace.File{"en": {Path: "guide.md", SHA256: marketplace.Digest(nil)}}}}
		}, "guide.md"},
		{"missing application manual", func(m *marketplace.Manifest, _ *model.CatalogSource, _ map[string][]byte) {
			m.Apps[0].Versions[0].Files.Manuals = map[string]marketplace.Document{"guide": {Files: map[string]marketplace.File{"en": {Path: "guide.md", SHA256: marketplace.Digest(nil)}}}}
		}, "guide.md"},
		{"missing environment", func(m *marketplace.Manifest, _ *model.CatalogSource, _ map[string][]byte) {
			m.Apps[0].Versions[0].Files.EnvExample = marketplace.File{Path: "env", SHA256: marketplace.Digest(nil)}
		}, "env"},
		{"secret in environment", func(m *marketplace.Manifest, _ *model.CatalogSource, f map[string][]byte) {
			f["env"] = []byte("PASSWORD=do-not-publish\n")
			v := &m.Apps[0].Versions[0]
			v.Files.EnvExample = marketplace.File{Path: "env", SHA256: marketplace.Digest(f["env"])}
			v.Variables = []marketplace.Variable{{Key: "PASSWORD", Secret: true}}
		}, "secret variable PASSWORD"},
		{"invalid environment", func(m *marketplace.Manifest, _ *model.CatalogSource, f map[string][]byte) {
			f["env"] = []byte("not an assignment")
			m.Apps[0].Versions[0].Files.EnvExample = marketplace.File{Path: "env", SHA256: marketplace.Digest(f["env"])}
		}, "KEY=VALUE"},
		{"unsupported asset", func(m *marketplace.Manifest, _ *model.CatalogSource, f map[string][]byte) {
			f["icon.svg"] = []byte("<svg></svg>")
			m.Apps[0].Versions[0].Files.Assets = map[string]marketplace.File{"icon.svg": {Path: "icon.svg", SHA256: marketplace.Digest(f["icon.svg"])}}
		}, "unsupported image"},
		{"missing asset", func(m *marketplace.Manifest, _ *model.CatalogSource, _ map[string][]byte) {
			m.Apps[0].Versions[0].Files.Assets = map[string]marketplace.File{"icon.png": {Path: "icon.png", SHA256: marketplace.Digest(nil)}}
		}, "icon.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, source, files := analysisFixture()
			tc.change(&m, &source, files)
			payload, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			versions, snapshot, err := c.analyzeMarket(context.Background(), source, payload, func(_ context.Context, u string) ([]byte, error) {
				if data, ok := files[strings.TrimPrefix(u, m.Marketplace.Endpoint)]; ok {
					return data, nil
				}
				return nil, errors.New("not available")
			})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("want %q error, got %v", tc.want, err)
			}
			if len(versions) != 0 || snapshot.ManifestSHA256 != "" {
				t.Fatal("failed analysis exposed a partial snapshot")
			}
			if strings.Contains(err.Error(), "do-not-publish") {
				t.Fatal("error leaked secret contents")
			}
		})
	}
}

func TestMarketAnalysisReusesContentAndChecksEveryReference(t *testing.T) {
	c, _, _ := controlFixture(t)
	m, source, files := analysisFixture()
	files["readme.md"] = []byte("# Shared document")
	doc := marketplace.Document{Files: map[string]marketplace.File{"en": {Path: "readme.md", SHA256: marketplace.Digest(files["readme.md"])}}}
	m.Marketplace.Docs = map[string]marketplace.Document{"guide": doc}
	m.Apps[0].Versions[0].Files.Readme = doc.Files
	files["env"] = []byte("MODE=old\nUNCHANGED=keep\n")
	v := &m.Apps[0].Versions[0]
	v.Files.EnvExample = marketplace.File{Path: "env", SHA256: marketplace.Digest(files["env"])}
	v.Variables = []marketplace.Variable{{Key: "MODE", Default: "new"}}
	files["pixel.gif"] = []byte("GIF89a\x01\x00\x01\x00")
	v.Files.Assets = map[string]marketplace.File{"pixel.gif": {Path: "pixel.gif", SHA256: marketplace.Digest(files["pixel.gif"])}}
	count := map[string]int{}
	fetch := func(_ context.Context, u string) ([]byte, error) {
		p := strings.TrimPrefix(u, m.Marketplace.Endpoint)
		count[p]++
		return files[p], nil
	}
	b, _ := json.Marshal(m)
	versions, snapshot, err := c.analyzeMarket(context.Background(), source, b, fetch)
	if err != nil {
		t.Fatal(err)
	}
	if count["readme.md"] != 1 || len(versions) != 1 || snapshot.Contents["readme.md"] != "# Shared document" {
		t.Fatal("shared content not reused", count)
	}
	if !strings.Contains(versions[0].EnvExample, `MODE="new"`) || !strings.Contains(versions[0].EnvExample, `UNCHANGED="keep"`) {
		t.Fatal("manifest/environment precedence lost", versions[0].EnvExample)
	}
	var stored marketplace.Snapshot
	if err = json.Unmarshal([]byte(versions[0].MarketJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored.Assets["pixel.gif"], "data:image/gif;base64,") {
		t.Fatal("verified asset missing")
	}
	// Sharing a path must not bypass the checksum of a later reference.
	v.Files.Readme = map[string]marketplace.File{"en": {Path: "readme.md", SHA256: strings.Repeat("0", 64)}}
	b, _ = json.Marshal(m)
	if _, _, err = c.analyzeMarket(context.Background(), source, b, fetch); err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Fatal("cached file bypassed reference checksum", err)
	}
}

func TestMarketAnalysisBoundsTotalFilesAndBytes(t *testing.T) {
	c, _, _ := controlFixture(t)
	for _, test := range []struct {
		name        string
		count, size int
		want        string
	}{{"files", 1001, 1, "1000 files"}, {"bytes", 9, 4 << 20, "32 MiB"}} {
		t.Run(test.name, func(t *testing.T) {
			m, source, _ := analysisFixture()
			m.Apps = nil
			m.Marketplace.Docs = map[string]marketplace.Document{}
			content := []byte(strings.Repeat("x", test.size))
			for i := 0; i < test.count; i++ {
				p := fmt.Sprintf("doc-%d.md", i)
				m.Marketplace.Docs[p] = marketplace.Document{Files: map[string]marketplace.File{"en": {Path: p, SHA256: marketplace.Digest(content)}}}
			}
			b, _ := json.Marshal(m)
			calls := 0
			_, _, err := c.analyzeMarket(context.Background(), source, b, func(context.Context, string) ([]byte, error) { calls++; return content, nil })
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("want %s, got %v", test.want, err)
			}
			if test.name == "files" && calls != 1000 {
				t.Fatal("file bound checked after fetch", calls)
			}
		})
	}
}

func TestConcurrentCatalogWaiterCanCancelWithoutCancellingAcceptedSync(t *testing.T) {
	c, _, _ := controlFixture(t)
	call := &catalogSyncCall{done: make(chan struct{})}
	c.catalogSyncs = map[string]*catalogSyncCall{"active": call}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := c.SyncCatalogSource(ctx, "active"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if c.catalogSyncs["active"] != call {
		t.Fatal("waiter removed accepted sync")
	}
	call.source = model.CatalogSource{ID: "active"}
	call.count = 6
	call.err = errors.New("accepted sync failed")
	close(call.done)
	s, count, err := c.SyncCatalogSource(context.Background(), "active")
	if s.ID != "active" || count != 6 || err != call.err {
		t.Fatal("waiter did not receive original result", s, count, err)
	}
}
