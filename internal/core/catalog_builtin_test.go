package core

import (
	"context"
	"strings"
	"testing"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

func TestBuiltinMarketTemplates(t *testing.T) {
	c, _, _ := controlFixture(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := c.EnsureBuiltinCatalog(ctx); err != nil {
			t.Fatal(err)
		}
	}
	versions, err := c.store.ListCatalogAppVersions(ctx, "", "", storage.Page{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 13 {
		t.Fatalf("expected thirteen immutable versions after repeated bootstrap, got %d", len(versions))
	}
	for _, v := range versions {
		if v.AppKey == "nginx" {
			continue
		}
		t.Run(v.AppKey, func(t *testing.T) {
			if !v.ComposeCompatible || v.SwarmCompatible != (v.SwarmYAML != "") {
				t.Fatal("stateful templates must explicitly gate Swarm")
			}
			manifest, err := applicationx.ParseManifest(v.ManifestJSON)
			if err != nil {
				t.Fatal(err)
			}
			values := []applicationx.EnvironmentVariable{}
			for _, variable := range manifest.Variables {
				if !variable.Required {
					continue
				}
				value := "catalog-test-value-123456"
				switch variable.Key {
				case "PGADMIN_DEFAULT_EMAIL":
					value = "admin@example.com"
				case "PMA_HOST":
					value = "mysql"
				case "DATABASE_NETWORK":
					value = "catalog_test_default"
				}
				values = append(values, applicationx.EnvironmentVariable{Key: variable.Key, Value: &value, Secret: variable.Secret})
			}
			input := applicationx.PreflightInput{ComposeYAML: v.ComposeYAML, ManifestYAML: v.ManifestJSON, Backend: model.RuntimeDocker, Environment: applicationx.EnvironmentInput{Values: &values}}
			preview, err := applicationx.Preflight(input)
			if err != nil {
				t.Fatal(err)
			}
			var compose struct {
				Services map[string]struct {
					Image   string `yaml:"image"`
					Ports   []any  `yaml:"ports"`
					Volumes []any  `yaml:"volumes"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal([]byte(preview.ResolvedYAML), &compose); err != nil {
				t.Fatal(err)
			}
			for name, service := range compose.Services {
				if !strings.Contains(service.Image, "@sha256:") || len(service.Ports) > 0 || len(service.Volumes) == 0 {
					t.Fatalf("%s lacks image pin/storage or exposes a host port", name)
				}
			}
			if v.AppKey == "redis" || v.AppKey == "mysql" || v.AppKey == "postgresql" {
				if len(preview.RecommendedDomains) != 0 {
					t.Fatal("database must not advertise an HTTP Route")
				}
			}
			if v.AppKey == "wordpress" && len(preview.Services) != 2 {
				t.Fatal("WordPress database dependency missing")
			}
			input.Backend = model.RuntimeSwarm
			if _, err := applicationx.Preflight(input); err == nil {
				t.Fatal("unsupported Swarm install accepted")
			}
			if v.SwarmYAML != "" {
				pinned, e := applicationx.PinDataNode(v.SwarmYAML, "fixture-node")
				if e != nil {
					t.Fatal(e)
				}
				input.ComposeYAML = pinned
				if _, e := applicationx.Preflight(input); e != nil {
					t.Fatal(e)
				}
				if e := applicationx.ValidateDataNodePolicy(pinned, "fixture-node", nil); e != nil {
					t.Fatal(e)
				}
			}

			input.Backend = model.RuntimeDocker
			input.Environment = applicationx.EnvironmentInput{}
			if _, err := applicationx.Preflight(input); err == nil {
				t.Fatal("missing credentials/connection information accepted")
			}
		})
	}
}
