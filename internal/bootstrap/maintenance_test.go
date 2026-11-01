package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"
)

func validRelease() Release {
	image := "registry.example.test/nsc@sha256:" + strings.Repeat("a", 64)
	return Release{Version: "v0.1.0", Images: Images{Controller: image, Gateway: image, PostgreSQL: image}, Protocol: 2, Schema: 18, Configuration: 1, PostgreSQLMajor: 18, Architectures: []string{"amd64", "arm64"}}
}
func TestReleaseManifestRetainsStrictCompatibilityAndImmutableImages(t *testing.T) {
	for _, scenario := range []string{"valid", "version", "long-version", "controller", "gateway", "postgresql", "protocol", "schema", "configuration", "database-major", "architecture", "duplicate", "missing-arm"} {
		t.Run(scenario, func(t *testing.T) {
			value := validRelease()
			switch scenario {
			case "version":
				value.Version = ""
			case "long-version":
				value.Version = strings.Repeat("a", 65)
			case "controller":
				value.Images.Controller = "nsc:latest"
			case "gateway":
				value.Images.Gateway = "nsc:latest"
			case "postgresql":
				value.Images.PostgreSQL = "nsc:latest"
			case "protocol":
				value.Protocol++
			case "schema":
				value.Schema++
			case "configuration":
				value.Configuration++
			case "database-major":
				value.PostgreSQLMajor++
			case "architecture":
				value.Architectures = []string{"amd64", "unsupported"}
			case "duplicate":
				value.Architectures = []string{"amd64", "arm64", "amd64"}
			case "missing-arm":
				value.Architectures = []string{"amd64"}
			}
			data, _ := json.Marshal(value)
			_, err := DecodeRelease(data)
			if (err == nil) != (scenario == "valid") {
				t.Fatal(scenario, err)
			}
		})
	}
	for _, document := range []string{`{`, `{"extra":true}`, `{} {}`} {
		if _, err := DecodeRelease([]byte(document)); err == nil {
			t.Fatal("untyped or multiple release documents accepted")
		}
	}
}
func TestInstallationRegistrationRequiresFourUniqueOwnedReceipts(t *testing.T) {
	value := Installation{ID: "installation", Revision: 1, RuntimeID: "runtime", PoolID: "pool", ClusterID: "cluster", MaintenanceAnchor: "anchor", Components: map[string]Resource{}, Database: DatabasePin{SinglePointOfFailure: true, ServiceID: "postgresql", NodeID: "node", EngineID: "engine", Volume: "volume"}}
	for _, key := range []string{"controller", "nginx", "nginx-sandbox", "postgresql"} {
		value.Components[key] = Resource{ID: key, Kind: "service", SpecDigest: "owned"}
	}
	if err := ValidateInstallation(value); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"id", "revision", "runtime", "pool", "cluster", "anchor", "components", "kind", "digest", "duplicate", "node", "engine", "volume", "database-id", "database-ha"} {
		t.Run(scenario, func(t *testing.T) {
			copy := value
			copy.Components = map[string]Resource{}
			for key, resource := range value.Components {
				copy.Components[key] = resource
			}
			switch scenario {
			case "id":
				copy.ID = ""
			case "revision":
				copy.Revision = 0
			case "runtime":
				copy.RuntimeID = ""
			case "pool":
				copy.PoolID = ""
			case "cluster":
				copy.ClusterID = ""
			case "anchor":
				copy.MaintenanceAnchor = ""
			case "components":
				delete(copy.Components, "nginx")
			case "kind":
				resource := copy.Components["nginx"]
				resource.Kind = "container"
				copy.Components["nginx"] = resource
			case "digest":
				resource := copy.Components["nginx"]
				resource.SpecDigest = ""
				copy.Components["nginx"] = resource
			case "duplicate":
				copy.Components["nginx"] = copy.Components["controller"]
			case "node":
				copy.Database.NodeID = ""
			case "engine":
				copy.Database.EngineID = ""
			case "volume":
				copy.Database.Volume = ""
			case "database-id":
				copy.Database.ServiceID = "other"
			case "database-ha":
				copy.Database.SinglePointOfFailure = false
			}
			if err := ValidateInstallation(copy); err == nil {
				t.Fatal("invalid registration accepted")
			}
		})
	}
}
