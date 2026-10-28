package core

import (
	"context"
	"errors"
	"reflect"
	"testing"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type applicationCatalogStore struct {
	storage.Store
	version model.CatalogAppVersion
	failure error
}

func (s *applicationCatalogStore) GetCatalogAppVersion(context.Context, string) (model.CatalogAppVersion, error) {
	return s.version, s.failure
}

func TestApplicationCatalogInheritancePreservesImmutableDefaults(t *testing.T) {
	for _, name := range []string{"inherit", "environment override", "manifest override", "explicit compose", "no input", "no catalog", "corrupt envelope", "empty compose", "base digest", "catalog unavailable", "catalog compose digest", "catalog artifact digest", "different base"} {
		t.Run(name, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			compose := "services:\n  web:\n    image: nginx:alpine\n"
			version := model.CatalogAppVersion{ID: "catalog", ComposeYAML: compose, EnvExample: "DEFAULT=value\n", ManifestJSON: "name: Example\nversion: '1'", README: "# Example", ComposeSHA256: digestString(compose)}
			version.ArtifactDigest = catalogArtifactDigest(compose, version.EnvExample, version.ManifestJSON, version.README)
			base := model.ApplicationRevision{ID: "revision", CatalogAppVersionID: version.ID, ComposeSHA256: version.ComposeSHA256}
			var err error
			base.EncryptedCompose, err = c.keyring.Encrypt([]byte(compose), revisionAAD(base.ID, "compose"))
			if err != nil {
				t.Fatal(err)
			}
			in := &ApplicationPreflightInput{}
			s := &applicationCatalogStore{Store: c.store, version: version}
			c.store = s
			valid := true
			switch name {
			case "environment override":
				in.EnvExample = "EXPLICIT=value"
			case "manifest override":
				in.Manifest = "name: Explicit"
			case "explicit compose":
				in.ComposeYAML = "services: {}"
			case "no input":
				in = nil
			case "no catalog":
				base.CatalogAppVersionID = ""
			case "corrupt envelope":
				base.EncryptedCompose = []byte("bad")
				valid = false
			case "empty compose":
				base.EncryptedCompose, err = c.keyring.Encrypt([]byte(" "), revisionAAD(base.ID, "compose"))
				if err != nil {
					t.Fatal(err)
				}
				base.ComposeSHA256 = digestString(" ")
				valid = false
			case "base digest":
				base.ComposeSHA256 = "wrong"
				valid = false
			case "catalog unavailable":
				s.failure = errors.New("unavailable")
				valid = false
			case "catalog compose digest":
				s.version.ComposeSHA256 = "wrong"
				valid = false
			case "catalog artifact digest":
				s.version.ArtifactDigest = "wrong"
				valid = false
			case "different base":
				s.version.ComposeYAML = "services:\n  web:\n    image: nginx:stable\n"
				s.version.ComposeSHA256 = digestString(s.version.ComposeYAML)
				s.version.ArtifactDigest = catalogArtifactDigest(s.version.ComposeYAML, s.version.EnvExample, s.version.ManifestJSON, s.version.README)
				valid = false
			}
			catalog, err := c.inheritRevisionContent(context.Background(), base, in)
			if (err == nil) != valid {
				t.Fatalf("inheritance %s: %v", name, err)
			}
			if name == "inherit" && (catalog != version.ID || in.ComposeYAML != compose || in.EnvExample != version.EnvExample || in.Manifest != version.ManifestJSON) {
				t.Fatal("catalog identity or defaults lost")
			}
			if name == "environment override" && (catalog != "" || in.EnvExample != "EXPLICIT=value" || in.Manifest != version.ManifestJSON) {
				t.Fatal("environment override silently reset")
			}
			if name == "manifest override" && (catalog != "" || in.Manifest != "name: Explicit" || in.EnvExample != version.EnvExample) {
				t.Fatal("manifest override silently reset")
			}
		})
	}
}

func TestCatalogInputRejectsOverridesWithdrawalAndCorruption(t *testing.T) {
	for _, name := range []string{"valid", "compose override", "environment override", "manifest override", "read failure", "withdrawn", "compose digest", "artifact digest"} {
		t.Run(name, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			v := model.CatalogAppVersion{ID: "catalog", ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n", EnvExample: "A=1", ManifestJSON: "name: Web", README: "# Web"}
			v.ComposeSHA256 = digestString(v.ComposeYAML)
			v.ArtifactDigest = catalogArtifactDigest(v.ComposeYAML, v.EnvExample, v.ManifestJSON, v.README)
			s := &applicationCatalogStore{Store: c.store, version: v}
			c.store = s
			in := ApplicationPreflightInput{CatalogAppVersionID: v.ID}
			switch name {
			case "compose override":
				in.ComposeYAML = "services: {}"
			case "environment override":
				in.EnvExample = "A=2"
			case "manifest override":
				in.Manifest = "name: Changed"
			case "read failure":
				s.failure = errors.New("unavailable")
			case "withdrawn":
				s.version.Withdrawn = true
			case "compose digest":
				s.version.ComposeSHA256 = "wrong"
			case "artifact digest":
				s.version.ArtifactDigest = "wrong"
			}
			got, err := c.resolveCatalogInput(context.Background(), in)
			if (err == nil) != (name == "valid") {
				t.Fatalf("catalog input %s: %v", name, err)
			}
			if name == "valid" && (got.ComposeYAML != v.ComposeYAML || got.EnvExample != v.EnvExample || got.Manifest != v.ManifestJSON) {
				t.Fatal("catalog snapshot content lost")
			}
		})
	}
}

func TestApplicationDiffSummarizesChangedServicesAndEnvironment(t *testing.T) {
	before := "services:\n  keep:\n    image: nginx:alpine\n  remove:\n    image: nginx:alpine\n"
	after := "services:\n  keep:\n    image: nginx:stable\n    deploy:\n      replicas: 2\n  add:\n    image: redis:alpine\nvolumes:\n  data: {}\nnetworks:\n  isolated: {}\n"
	candidate, err := applicationx.Preflight(applicationx.PreflightInput{ComposeYAML: after, Backend: model.RuntimeDocker, CatalogAnalysis: true})
	if err != nil {
		t.Fatal(err)
	}
	candidate.Environment = map[string]string{"SAME": "same", "CHANGED": "new", "ADDED": "new"}
	diff := buildApplicationDiff("old", digestString(before), before, before, map[string]string{"SAME": "same", "CHANGED": "old", "REMOVED": "old"}, candidate)
	if diff.FromRevisionID != "old" || !diff.ComposeChanged || !diff.ServiceConfigChanged || !diff.NetworksChanged || !diff.VolumesChanged || !diff.DeployPolicyChanged {
		t.Fatalf("diff omitted impact: %+v", diff)
	}
	for _, pair := range []struct{ got, want []string }{{diff.EnvironmentAdded, []string{"ADDED"}}, {diff.EnvironmentChanged, []string{"CHANGED"}}, {diff.EnvironmentRemoved, []string{"REMOVED"}}, {diff.ServicesAdded, []string{"add"}}, {diff.ServicesRemoved, []string{"remove"}}} {
		if !reflect.DeepEqual(pair.got, pair.want) {
			t.Fatalf("diff=%v want=%v", pair.got, pair.want)
		}
	}
	invalid := buildApplicationDiff("old", "", before, "services: [", nil, candidate)
	if !reflect.DeepEqual(invalid.ServicesAdded, []string{"add", "keep"}) {
		t.Fatal("unreadable historical definition fabricated existing services")
	}
}

func TestApplicationOperationPayloadAndIdentityBoundaries(t *testing.T) {
	c, _, _ := controlFixture(t)
	if _, err := operationRequestSHA256("INSTALL", make(chan int)); err == nil {
		t.Fatal("unserializable idempotency payload accepted")
	}
	if _, err := c.newOperation("op", "app", "runtime", "", "revision", "INSTALL", "key", make(chan int), c.now()); err == nil {
		t.Fatal("unserializable operation queued")
	}
	if op, err := c.newOperation("op", "app", "runtime", "", "revision", "INSTALL", " key ", nil, c.now()); err != nil || op.IdempotencyKey != "key" || len(op.EncryptedInput) != 0 {
		t.Fatal("empty payload not preserved", err)
	}
	c.keyring = &security.Keyring{}
	if _, err := c.newOperation("op", "app", "runtime", "", "revision", "INSTALL", "key", map[string]string{"secret": "value"}, c.now()); err == nil {
		t.Fatal("operation persisted without encryption")
	}
	for _, name := range []string{"type", "missing hash", "different hash", "application", "service", "valid"} {
		v := model.DeploymentOperation{Type: "UPGRADE", ApplicationID: "app", ServiceID: "service", RequestSHA256: "hash"}
		switch name {
		case "type":
			v.Type = "INSTALL"
		case "missing hash":
			v.RequestSHA256 = ""
		case "different hash":
			v.RequestSHA256 = "other"
		case "application":
			v.ApplicationID = "other"
		case "service":
			v.ServiceID = "other"
		}
		if err := validateIdempotentOperation(v, "UPGRADE", "app", "service", "hash"); (err == nil) != (name == "valid") {
			t.Fatalf("operation identity %s: %v", name, err)
		}
	}
}
