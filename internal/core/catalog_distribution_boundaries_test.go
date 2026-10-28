package core

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/storage"
	"io/fs"
	"strings"
	"testing"
)

type catalogDistributionFS struct {
	fs.FS
	path             string
	content          []byte
	failure          error
	directoryFailure bool
}

func (f catalogDistributionFS) ReadFile(name string) ([]byte, error) {
	if name == f.path {
		if f.content != nil {
			return f.content, nil
		}
		return nil, f.failure
	}
	return fs.ReadFile(f.FS, name)
}
func (f catalogDistributionFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if f.directoryFailure {
		return nil, f.failure
	}
	return fs.ReadDir(f.FS, name)
}
func TestBuiltinCatalogDistributionFailuresCanBeRetriedWithoutDuplicatingArtifacts(t *testing.T) {
	for _, stage := range []string{"invalid legacy compose", "missing market", "malformed market", "directory unavailable", "missing application file", "malformed application manifest"} {
		t.Run(stage, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			ctx := context.Background()
			legacy := map[string]string{"docker/docker-compose.yml": "services:\n  web:\n    image: nginx:alpine\n", "nsc-app.yaml": "name: NGINX\n", "README.md": "# NGINX\n"}
			goodCompose := legacy["docker/docker-compose.yml"]
			failure := errors.New("isolated distribution unavailable")
			assets := catalogDistributionFS{FS: builtinCatalogFiles, failure: failure}
			wantWrapped := false
			switch stage {
			case "invalid legacy compose":
				legacy["docker/docker-compose.yml"] = "services: ["
			case "missing market":
				assets.path = "builtin/market.yaml"
				wantWrapped = true
			case "malformed market":
				assets.path = "builtin/market.yaml"
				assets.content = []byte("[")
			case "directory unavailable":
				assets.directoryFailure = true
				wantWrapped = true
			case "missing application file":
				assets.path = "builtin/redis/manifest.yaml"
				wantWrapped = true
			case "malformed application manifest":
				assets.path = "builtin/redis/manifest.yaml"
				assets.content = []byte("variables: [")
			}
			e := c.ensureBuiltinCatalog(ctx, assets, legacy)
			if e == nil || wantWrapped && !errors.Is(e, failure) {
				t.Fatalf("distribution failure hidden: %v", e)
			}
			legacy["docker/docker-compose.yml"] = goodCompose
			if e = c.ensureBuiltinCatalog(ctx, builtinCatalogFiles, legacy); e != nil {
				t.Fatalf("corrected distribution cannot resume: %v", e)
			}
			sources, e := c.store.ListCatalogSources(ctx, storage.Page{Limit: 200})
			if e != nil || len(sources) != 1 {
				t.Fatalf("retry duplicated source: %d %v", len(sources), e)
			}
			before, e := c.store.ListCatalogAppVersions(ctx, sources[0].ID, "", storage.Page{Limit: 200})
			if e != nil || len(before) < 7 {
				t.Fatalf("recovery omitted common applications: %d %v", len(before), e)
			}
			if e = c.ensureBuiltinCatalog(ctx, builtinCatalogFiles, legacy); e != nil {
				t.Fatal("idempotent retry failed", e)
			}
			after, e := c.store.ListCatalogAppVersions(ctx, sources[0].ID, "", storage.Page{Limit: 200})
			if e != nil || len(after) != len(before) {
				t.Fatal("repeat initialization duplicated immutable versions", e)
			}
			for i := range before {
				if before[i].ID != after[i].ID || before[i].ComposeSHA256 != after[i].ComposeSHA256 || before[i].ArtifactDigest != after[i].ArtifactDigest || before[i].README != after[i].README {
					t.Fatal("retry rewrote immutable install material")
				}
			}
			if legacy["docker/docker-compose.yml"] != goodCompose || !strings.HasPrefix(legacy["README.md"], "# NGINX") {
				t.Fatal("initialization mutated source material")
			}
		})
	}
}
