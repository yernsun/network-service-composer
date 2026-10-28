package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/yernsun/network-service-composer/internal/marketplace"
	"strings"
	"testing"
)

func TestMarketAnalysisPreservesVerifiedRasterAssets(t *testing.T) {
	c, _, _ := controlFixture(t)
	for _, tc := range []struct {
		media string
		data  []byte
	}{
		{"image/png", []byte("\x89PNG\r\n\x1a\n")},
		{"image/jpeg", []byte("\xff\xd8\xff\xe0")},
		{"image/webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")},
	} {
		t.Run(tc.media, func(t *testing.T) {
			m, source, files := analysisFixture()
			files["asset"] = tc.data
			m.Apps[0].Versions[0].Files.Assets = map[string]marketplace.File{"logo": {Path: "asset", SHA256: marketplace.Digest(tc.data)}}
			payload, e := json.Marshal(m)
			if e != nil {
				t.Fatal(e)
			}
			versions, _, e := c.analyzeMarket(context.Background(), source, payload, func(_ context.Context, u string) ([]byte, error) {
				return files[strings.TrimPrefix(u, m.Marketplace.Endpoint)], nil
			})
			if e != nil || len(versions) != 1 {
				t.Fatalf("raster rejected: %v", e)
			}
			var snapshot marketplace.Snapshot
			if e = json.Unmarshal([]byte(versions[0].MarketJSON), &snapshot); e != nil {
				t.Fatal(e)
			}
			if snapshot.Assets["logo"] != "data:"+tc.media+";base64,"+base64.StdEncoding.EncodeToString(tc.data) {
				t.Fatal("verified bytes or media type changed")
			}
		})
	}
}
func TestMarketAnalysisRejectsMalformedManifestBeforeFetching(t *testing.T) {
	c, _, _ := controlFixture(t)
	_, source, _ := analysisFixture()
	for _, payload := range []string{"[", "schema_version: 99", "schema_version: 1\nunknown: field"} {
		fetched := false
		versions, snapshot, e := c.analyzeMarket(context.Background(), source, []byte(payload), func(context.Context, string) ([]byte, error) { fetched = true; return nil, nil })
		if e == nil || fetched || len(versions) != 0 || snapshot.ManifestSHA256 != "" {
			t.Fatal("invalid manifest fetched or exposed partial data")
		}
	}
	if ValidateApprovedEndpoint("https://market.example") == nil {
		t.Fatal("non-directory approved endpoint accepted")
	}
}

func TestMarketAnalysisValidatesDocumentIdentityBeforeFetching(t *testing.T) {
	c, _, _ := controlFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*marketplace.Manifest)
		want   string
	}{
		{"metadata", func(m *marketplace.Manifest) { m.Marketplace.DefaultLocale = "undeclared" }, "locale"},
		{"reserved README", func(m *marketplace.Manifest) {
			files := &m.Apps[0].Versions[0].Files
			files.Readme = map[string]marketplace.File{"en": {Path: "README.md", SHA256: marketplace.Digest(nil)}}
			files.Manuals = map[string]marketplace.Document{"readme": {Files: files.Readme}}
		}, "reserved"},
		{"unsafe asset identity", func(m *marketplace.Manifest) {
			m.Apps[0].Versions[0].Files.Assets = map[string]marketplace.File{"../secret": {Path: "asset.png", SHA256: marketplace.Digest(nil)}}
		}, "asset key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, source, _ := analysisFixture()
			tc.mutate(&manifest)
			payload, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			fetched := false
			versions, snapshot, err := c.analyzeMarket(context.Background(), source, payload, func(context.Context, string) ([]byte, error) { fetched = true; return nil, nil })
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) || fetched || len(versions) != 0 || snapshot.ManifestSHA256 != "" {
				t.Fatalf("invalid document identity escaped preflight: fetched=%v versions=%d err=%v", fetched, len(versions), err)
			}
		})
	}
}
