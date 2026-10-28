package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/marketplace"
)

func publishForTest(t *testing.T, args ...string) error {
	t.Helper()
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args = oldArgs; flag.CommandLine = oldFlags }()
	flag.CommandLine = flag.NewFlagSet("market-boundary", flag.ContinueOnError)
	os.Args = append([]string{"market-boundary"}, args...)
	return run()
}

func publisherFixture(t *testing.T) (string, marketplace.Manifest) {
	t.Helper()
	dir := t.TempDir()
	for path, body := range map[string]string{"compose.yaml": "services:\n  web:\n    image: nginx:alpine\n", "README.md": "# Start\n[manual](manual.md#restore)\n[web](https://example.org/)\n[here](#start)", "manual.md": "# Restore", ".env.example": "MODE=test\n"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := marketplace.Manifest{SchemaVersion: 1, Marketplace: marketplace.Metadata{ID: "test", Name: marketplace.Text{"en": "Test"}, Endpoint: "https://market.example/", DefaultLocale: "en", Locales: []string{"en"}}, Apps: []marketplace.App{{ID: "web", Name: marketplace.Text{"en": "Web"}, Versions: []marketplace.Version{{Version: "1", Files: marketplace.Files{
		Compose: marketplace.File{Path: "compose.yaml"}, EnvExample: marketplace.File{Path: ".env.example"}, Readme: map[string]marketplace.File{"en": {Path: "README.md"}}, Manuals: map[string]marketplace.Document{"manual": {Files: map[string]marketplace.File{"en": {Path: "manual.md"}}}},
	}}}}}}
	return dir, m
}

func writeMarket(t *testing.T, dir string, m marketplace.Manifest) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "market.yaml")
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPublicationFailuresPreserveVisibleManifest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, string, *marketplace.Manifest)
		want   string
	}{
		{"missing compose", func(_ *testing.T, _ string, m *marketplace.Manifest) {
			m.Apps[0].Versions[0].Files.Compose.Path = "missing.yaml"
		}, "no such file"},
		{"directory instead of file", func(_ *testing.T, dir string, m *marketplace.Manifest) {
			if err := os.Mkdir(filepath.Join(dir, "folder"), 0700); err != nil {
				panic(err)
			}
			m.Apps[0].Versions[0].Files.Compose.Path = "folder"
		}, "invalid or oversized"},
		{"oversized file", func(t *testing.T, dir string, _ *marketplace.Manifest) {
			if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), make([]byte, (4<<20)+1), 0600); err != nil {
				t.Fatal(err)
			}
		}, "oversized"},
		{"symlink", func(t *testing.T, dir string, m *marketplace.Manifest) {
			if err := os.Symlink("compose.yaml", filepath.Join(dir, "link.yaml")); err != nil {
				t.Fatal(err)
			}
			m.Apps[0].Versions[0].Files.Compose.Path = "link.yaml"
		}, "symlinks"},
		{"symlink ancestor", func(t *testing.T, dir string, m *marketplace.Manifest) {
			if err := os.Symlink(dir, filepath.Join(dir, "alias")); err != nil {
				t.Fatal(err)
			}
			m.Apps[0].Versions[0].Files.Compose.Path = "alias/compose.yaml"
		}, "symlinks"},
		{"unbundled document", func(t *testing.T, dir string, _ *marketplace.Manifest) {
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("[lost](missing.md)"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "unbundled"},
		{"insecure document link", func(t *testing.T, dir string, _ *marketplace.Manifest) {
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("[link](http://example.org/)"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "unsupported documentation link"},
		{"malformed document link", func(t *testing.T, dir string, _ *marketplace.Manifest) {
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("[link](%zz)"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "invalid documentation link"},
		{"missing environment", func(_ *testing.T, _ string, m *marketplace.Manifest) {
			m.Apps[0].Versions[0].Files.EnvExample.Path = "missing.env"
		}, "missing.env"},
		{"missing market manual", func(_ *testing.T, _ string, m *marketplace.Manifest) {
			m.Marketplace.Docs = map[string]marketplace.Document{"guide": {Files: map[string]marketplace.File{"en": {Path: "missing.md"}}}}
		}, "missing.md"},
		{"missing asset", func(_ *testing.T, _ string, m *marketplace.Manifest) {
			m.Apps[0].Versions[0].Files.Assets = map[string]marketplace.File{"icon.png": {Path: "missing.png"}}
		}, "missing.png"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, m := publisherFixture(t)
			out := filepath.Join(t.TempDir(), "published")
			input := writeMarket(t, dir, m)
			if err := publishForTest(t, "--manifest", input, "--output", out); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(out, "market.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			tc.change(t, dir, &m)
			writeMarket(t, dir, m)
			err = publishForTest(t, "--manifest", input, "--output", out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			after, err := os.ReadFile(filepath.Join(out, "market.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("failed publication replaced visible manifest")
			}
		})
	}
}

func TestPublisherValidatesArgumentsAndPublicSourcesBeforeFetching(t *testing.T) {
	dir, m := publisherFixture(t)
	input := writeMarket(t, dir, m)
	for _, args := range [][]string{
		{"--manifest", filepath.Join(dir, "missing")},
		{"--manifest", input, "--output", dir},
		{"--manifest", input, "--endpoint", "http://market.example/"},
		{"--manifest", input, "--homepage", "javascript:alert(1)"},
		{"--manifest", input, "--official", "--output", filepath.Join(dir, "out")},
	} {
		if err := publishForTest(t, args...); err == nil {
			t.Fatal("invalid arguments accepted", args)
		}
	}
	for _, source := range []marketplace.Source{
		{Repository: "https://[invalid", Ref: "main"}, {Repository: "https:///missing-host", Ref: "main"},
		{Repository: "https://user:secret@example.org/repo", Ref: "main"}, {Repository: "https://example.org/repo?", Ref: "main"},
		{Repository: "https://example.org/repo?token=secret", Ref: "main"}, {Repository: "https://example.org/repo#fragment", Ref: "main"},
		{Repository: "ssh://example.org/repo", Ref: "main"}, {Repository: "https://example.org/repo", Ref: "--upload-pack=command"},
		{Repository: "https://example.org/repo", Ref: ""}, {Repository: "https://example.org/repo", Ref: " "},
	} {
		m.Apps[0].Versions[0].Source = &source
		writeMarket(t, dir, m)
		err := publishForTest(t, "--manifest", input, "--output", filepath.Join(dir, "out"))
		if err == nil || !strings.Contains(err.Error(), "invalid public Git source") {
			t.Fatalf("source was not rejected before Git: %v", err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("source credentials leaked")
		}
	}
}

func TestPublisherOverridesAndCanonicalDotPrefixedFile(t *testing.T) {
	dir, m := publisherFixture(t)
	if err := os.Rename(filepath.Join(dir, "compose.yaml"), filepath.Join(dir, "..compose.yaml")); err != nil {
		t.Fatal(err)
	}
	m.Apps[0].Versions[0].Files.Compose.Path = "..compose.yaml"
	m.Apps[0].Versions[0].Files.EnvExample = marketplace.File{}
	input := writeMarket(t, dir, m)
	out := filepath.Join(t.TempDir(), "published")
	if err := publishForTest(t, "--manifest", input, "--output", out, "--endpoint", "https://pages.example/nsc/", "--homepage", "https://nsc.example/"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "market.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	published, err := marketplace.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if err = published.Validate(true); err != nil {
		t.Fatal(err)
	}
	if published.Marketplace.Endpoint != "https://pages.example/nsc/" || published.Marketplace.Homepage != "https://nsc.example/" {
		t.Fatal("overrides lost")
	}
	f := published.Apps[0].Versions[0].Files.Compose
	if f.SourcePath != "..compose.yaml" {
		t.Fatal("original path lost")
	}
	data, err := os.ReadFile(filepath.Join(out, f.Path))
	if err != nil || marketplace.Digest(data) != f.SHA256 {
		t.Fatal("published object cannot be verified", err)
	}
}

func TestPublisherSwarmVariantIsIndependentAndDigestVerified(t *testing.T) {
	dir, manifest := publisherFixture(t)
	swarm := "services:\n  web:\n    image: nginx:alpine\n    deploy: {replicas: 1}\n"
	if err := os.WriteFile(filepath.Join(dir, "swarm.yaml"), []byte(swarm), 0600); err != nil {
		t.Fatal(err)
	}
	manifest.Apps[0].Versions[0].Files.Swarm = marketplace.File{Path: "swarm.yaml"}
	input := writeMarket(t, dir, manifest)
	out := filepath.Join(t.TempDir(), "published")
	if err := publishForTest(t, "--manifest", input, "--output", out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "market.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	published, err := marketplace.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	files := published.Apps[0].Versions[0].Files
	bytes, err := os.ReadFile(filepath.Join(out, files.Swarm.Path))
	if err != nil || string(bytes) != swarm || files.Swarm.SHA256 != marketplace.Digest(bytes) || files.Swarm.Path == files.Compose.Path {
		t.Fatal("Swarm variant lost or not verified", err)
	}
	if err := published.Validate(true); err != nil {
		t.Fatal(err)
	}
	files.Swarm.SHA256 = "corrupt"
	published.Apps[0].Versions[0].Files = files
	if err := published.Validate(true); err == nil {
		t.Fatal("invalid Swarm digest accepted")
	}
	manifest.Apps[0].Versions[0].Files.Swarm.Path = "missing.yaml"
	if err := publishForTest(t, "--manifest", writeMarket(t, dir, manifest), "--output", out); err == nil {
		t.Fatal("missing variant silently fell back to Compose")
	}
}
