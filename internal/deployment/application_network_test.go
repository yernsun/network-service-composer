package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"gopkg.in/yaml.v3"
)

func TestApplicationNetworkOwnershipAndIdempotency(t *testing.T) {
	f := newWorkerFixture(t)
	for _, mode := range []string{"create", "existing", "foreign", "legacy labels", "bridge", "ambiguous", "unavailable", "create failed", "inspect failed", "invalid evidence", "missing locker", "project create", "project existing", "project legacy labels"} {
		t.Run(mode, func(t *testing.T) {
			created := 0
			present := mode != "create" && mode != "create failed" && mode != "project create"
			ownerLabel, ownerID := applicationNetworkOwner, "app-owned"
			var scope *model.ServiceDeploymentScope
			if strings.HasPrefix(mode, "project ") {
				ownerLabel, ownerID = "io.network-service-composer.project", "project-owned"
				scope = &model.ServiceDeploymentScope{ProjectID: ownerID}
			}
			owner, driver := ownerID, "overlay"
			if mode == "foreign" {
				owner = "someone-else"
			}
			if mode == "bridge" {
				driver = "bridge"
			}
			runner := &recordingRunner{run: func(call runnerCall) (string, error) {
				cmd := strings.Join(call.arguments, " ")
				switch {
				case strings.Contains(cmd, "network ls"):
					if mode == "unavailable" {
						return "", errors.New("engine unavailable")
					}
					if mode == "ambiguous" {
						return "id1 demoapp\nid2 demoapp", nil
					}
					if present {
						return "network-id demoapp\nother demoapp-other", nil
					}
					return "", nil
				case strings.Contains(cmd, "network create"):
					created++
					if mode == "create failed" {
						return "", errors.New("creation outcome unknown")
					}
					if !strings.Contains(cmd, ownerLabel+"="+ownerID) || !strings.Contains(cmd, "--driver overlay") {
						t.Fatalf("missing ownership: %s", cmd)
					}
					present = true
					return "network-id", nil
				case strings.Contains(cmd, "network inspect"):
					if mode == "inspect failed" {
						return "", errors.New("inspect unavailable")
					}
					if mode == "invalid evidence" {
						return "not json", nil
					}
					labels := map[string]string{ownerLabel: owner}
					if mode == "legacy labels" || mode == "project legacy labels" {
						labels = map[string]string{strings.Replace(ownerLabel, "network-service", "nginx-service", 1): owner}
					}
					encoded, _ := json.Marshal(map[string]any{"Name": "demoapp", "Driver": driver, "Scope": "swarm", "Labels": labels})
					return string(encoded), nil
				default:
					t.Fatalf("unexpected mutation %s", cmd)
					return "", nil
				}
			}}
			w := NewWorkerWithRunner(f.store, lease.New(f.store.DB(), f.store.Dialect()), f.keyring, f.config, nil, runner)
			if mode == "missing locker" {
				w.locker = nil
			}
			value := material{application: model.Application{ID: "app-owned"}, revision: model.ApplicationRevision{ID: "rev-new", ConfigurationSource: model.DeploymentSource{NetworkName: "demoapp", ServiceScope: scope}}}
			runtime := f.runtime
			runtime.Type = model.RuntimeSwarm
			err := w.ensureApplicationNetwork(context.Background(), runtime, value)
			if success := mode == "create" || mode == "existing" || mode == "project create" || mode == "project existing"; (err == nil) != success {
				t.Fatalf("%s: %v", mode, err)
			}
			if err == nil {
				if err := w.ensureApplicationNetwork(context.Background(), runtime, value); err != nil {
					t.Fatal(err)
				}
			}
			want := 0
			if mode == "create" || mode == "create failed" || mode == "project create" {
				want = 1
			}
			if created != want {
				t.Fatalf("created %d networks, want %d", created, want)
			}
		})
	}
}

func TestApplicationNetworkMaterialPreservesDefaultBeforeIngressMerge(t *testing.T) {
	f := newWorkerFixture(t)
	app, op := installFixtureApplication(t, f, "services:\n  web:\n    image: web:fixed\n  database:\n    image: database:fixed\n")
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	value, err := w.loadMaterial(context.Background(), f.runtime, app, op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	value.revision.ConfigurationSource.NetworkName = "demoapp"
	runtime := f.runtime
	runtime.Type = model.RuntimeSwarm
	_, path, _, _, cleanup, err := w.materialize(context.Background(), runtime, value)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Services map[string]struct{ Networks map[string]any }
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for name, service := range document.Services {
		if _, ok := service.Networks["default"]; !ok {
			t.Fatalf("%s lost default network", name)
		}
		if _, ok := service.Networks["nsc_application"]; !ok {
			t.Fatalf("%s lost application network", name)
		}
		if _, ok := service.Networks["nsc_backend"]; ok {
			t.Fatalf("unbound service %s gained Gateway access", name)
		}
	}
}

func TestFailedInstallationNetworkCleanupRespectsOwnershipAndUsage(t *testing.T) {
	f := newWorkerFixture(t)
	app, op := installFixtureApplication(t, f, "services:\n  web:\n    image: web:fixed\n")
	if _, err := f.store.DB().Exec(`UPDATE application_revisions SET configuration_source=? WHERE id=?`, `{"network_name":"demoapp"}`, op.RevisionID); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"owned empty", "foreign", "previous revision", "legacy labels", "in use", "missing", "ambiguous", "remove refused"} {
		t.Run(mode, func(t *testing.T) {
			removed := false
			runner := &recordingRunner{run: func(call runnerCall) (string, error) {
				cmd := strings.Join(call.arguments, " ")
				switch {
				case strings.Contains(cmd, "network ls"):
					if mode == "missing" {
						return "", nil
					}
					if mode == "ambiguous" {
						return "one demoapp\ntwo demoapp", nil
					}
					return "network-id demoapp\nother demoapp-other", nil
				case strings.Contains(cmd, "network inspect"):
					owner, revision := app.ID, op.RevisionID
					if mode == "foreign" {
						owner = "external"
					}
					if mode == "previous revision" {
						revision = "previous"
					}
					containers := map[string]any{}
					if mode == "in use" {
						containers["running-container"] = map[string]any{}
					}
					labels := map[string]string{applicationNetworkOwner: owner, "io.network-service-composer.revision": revision}
					if mode == "legacy labels" {
						labels = map[string]string{"io.nginx-service-composer.application": owner, "io.nginx-service-composer.revision": revision}
					}
					encoded, _ := json.Marshal(map[string]any{"ID": "network-id", "Labels": labels, "Containers": containers})
					return string(encoded), nil
				case strings.Contains(cmd, "network rm network-id"):
					if mode == "remove refused" {
						return "", errors.New("service still references network")
					}
					removed = true
					return "network-id", nil
				default:
					t.Fatalf("unexpected mutation %s", cmd)
					return "", nil
				}
			}}
			w := NewWorkerWithRunner(f.store, lease.New(f.store.DB(), f.store.Dialect()), f.keyring, f.config, nil, runner)
			runtime := f.runtime
			runtime.Type = model.RuntimeSwarm
			err := w.cleanupInstallationNetwork(context.Background(), runtime, app, op.RevisionID)
			if removed != (mode == "owned empty") {
				t.Fatalf("unsafe cleanup in %s", mode)
			}
			failure := mode == "in use" || mode == "ambiguous" || mode == "remove refused"
			if (err != nil) != failure {
				t.Fatalf("cleanup evidence %s: %v", mode, err)
			}
		})
	}
}

func TestExactNetworkNamesDoNotMatchOtherApplications(t *testing.T) {
	ids, err := exactNetworkIDs("first demoapp-dev\nsecond demoapp\nthird other-demoapp", "demoapp")
	if err != nil || len(ids) != 1 || ids[0] != "second" {
		t.Fatalf("incorrect selection: %v %v", ids, err)
	}
	if _, err := exactNetworkIDs("unparseable", "demoapp"); err == nil {
		t.Fatal("accepted unknown network evidence")
	}
}
