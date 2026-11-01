package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestDatabaseRelayRejectsMissingForeignDuplicateAndUnreadyTasks(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	original := i.Hosts["one"]
	service := i.Record.Resources["service:postgresql"]
	valid := swarm.Task{ID: "recorded-task", ServiceID: service.ID, NodeID: i.Record.Nodes["one"].NodeID, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, ContainerStatus: &swarm.ContainerStatus{ContainerID: "original-container"}}}
	for _, scenario := range []string{"unavailable", "foreign-service", "foreign-node", "starting", "no-container", "duplicate", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			task := valid
			tasks := []swarm.Task{}
			switch scenario {
			case "foreign-service":
				task.ServiceID = "other"
			case "foreign-node":
				task.NodeID = "other"
			case "starting":
				task.Status.State = swarm.TaskStateStarting
			case "no-container":
				task.Status.ContainerStatus = nil
			}
			tasks = append(tasks, task)
			if scenario == "duplicate" {
				tasks = append(tasks, task)
			}
			i.Hosts["one"] = interceptedHost{base: original, intercept: func(ctx context.Context, m, p string, in, out any) (bool, error) {
				if len(p) >= 6 && p[:6] == "/tasks" {
					if scenario == "unavailable" {
						return true, errors.New("database task evidence unavailable")
					}
					return true, encode(tasks, out)
				}
				return false, nil
			}}
			id, err := i.databaseContainer(t.Context())
			if scenario == "valid" {
				if err != nil || id != "original-container" {
					t.Fatal("valid pinned target refused", err)
				}
			} else if err == nil {
				t.Fatal("unsafe database target accepted")
			}
		})
	}
	i.Hosts["one"] = original
	delete(i.Record.Resources, "service:postgresql")
	if _, err := i.databaseContainer(t.Context()); err == nil {
		t.Fatal("missing database service receipt accepted")
	}
	i.Record.Resources["service:postgresql"] = service
	i.Database = nil
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := i.withStore(ctx, true, func(storage.Store, config.Config) error { return nil }); err == nil {
		t.Fatal("missing authenticated SSH database relay accepted")
	}
	if len(c.services) != 5 || len(original.(*fakeHost).volumes) != 1 {
		t.Fatal("database failure created replacement resources")
	}
}

func TestPrivateOfflineConfigurationLossCannotResetRuntimeSecrets(t *testing.T) {
	for _, name := range []string{"postgres-password", "admin-password", "session-key", "encryption-key"} {
		t.Run(name, func(t *testing.T) {
			i, _ := installerFixture(t, 1)
			infrastructure(t, i)
			os.Remove(i.material(name))
			if _, err := i.localConfig(t.Context()); err == nil {
				t.Fatal("incomplete offline credentials accepted")
			}
			if err := i.withStore(t.Context(), true, func(storage.Store, config.Config) error { return nil }); err == nil {
				t.Fatal("database opened without original private material")
			}
			if _, err := os.Stat(i.material(name)); !os.IsNotExist(err) {
				t.Fatal("missing secret regenerated")
			}
		})
	}
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	i.Config.Adoption = &bootstrap.Adoption{ControllerConfigFile: "/nonexistent-controller.yaml", ServiceIDs: map[string]string{"postgresql": i.Record.Resources["service:postgresql"].ID}}
	if _, err := i.localConfig(t.Context()); err == nil {
		t.Fatal("missing adoption configuration accepted")
	}
	i.Config.Adoption = nil
	delete(i.Record.Resources, "reserve:controller")
	if _, err := i.localConfig(t.Context()); err == nil {
		t.Fatal("offline configuration omitted actual service identity")
	}
}

func TestFinalizationAndResumeRejectCorruptOrIncompleteInstallationMarkers(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	if err := i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error { return store.Migrate(t.Context()) }); err != nil {
		t.Fatal(err)
	}
	if err := i.finalizeDatabase(t.Context()); err == nil {
		t.Fatal("missing database marker finalized")
	}
	if _, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "seed"}); err == nil {
		t.Fatal("missing marker reconciled")
	}
	if err := i.initializeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, state := range []bootstrap.State{bootstrap.Pending, bootstrap.Running, bootstrap.Succeeded, bootstrap.Failed} {
		if err := i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
			old, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
			if err != nil {
				return err
			}
			r := i.seedRecord()
			r.State = state
			return writeInstallation(t.Context(), store, r, old.Revision)
		}); err != nil {
			t.Fatal(err)
		}
		receipt, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "seed"})
		if state == bootstrap.Running || state == bootstrap.Succeeded {
			if err != nil || receipt == nil {
				t.Fatal("recorded initialization lost", err)
			}
		} else if receipt != nil {
			t.Fatal("incomplete initialization falsely completed")
		}
		receipt, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "finalize"})
		if state == bootstrap.Succeeded {
			if err != nil || receipt == nil {
				t.Fatal(err)
			}
		} else if receipt != nil {
			t.Fatal("unfinished handoff falsely finalized")
		}
	}
	if err := i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
		old, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
		if err != nil {
			return err
		}
		old.Payload = json.RawMessage(`{"id":true}`)
		return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: old, Expected: old.Revision}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := i.finalizeDatabase(t.Context()); err == nil {
		t.Fatal("corrupt marker finalized")
	}
	if _, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "seed"}); err == nil {
		t.Fatal("corrupt marker reconciled")
	}
}

func TestOfflineInitializationDoesNotAdvanceWithoutDatabaseOrMatchingKeyring(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	i.Database = sqliteConnector{fail: context.DeadlineExceeded}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err := i.initializeDatabase(ctx); err == nil {
		t.Fatal("unavailable database initialized")
	}
	if _, err := i.Apply(ctx, bootstrap.Step{Key: "seed"}); err == nil {
		t.Fatal("failed offline initialization acknowledged")
	}
	i.Database = sqliteConnector{dsn: filepath.Join(i.Directory, "fixture.sqlite")}
	os.WriteFile(i.material("encryption-key"), []byte("invalid-keyring-material"), 0600)
	if err := i.initializeDatabase(t.Context()); err == nil {
		t.Fatal("invalid original encryption key accepted")
	}
}

func TestFinalizationRefusesChangedClusterAndConfiguration(t *testing.T) {
	for _, field := range []string{"cluster", "configuration"} {
		i, _ := installerFixture(t, 1)
		infrastructure(t, i)
		if err := i.initializeDatabase(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := i.withStore(t.Context(), false, func(store storage.Store, _ config.Config) error {
			old, err := store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
			if err != nil {
				return err
			}
			var value bootstrap.Installation
			json.Unmarshal(old.Payload, &value)
			if field == "cluster" {
				value.ClusterID = "foreign"
			} else {
				value.ConfigDigest = "foreign"
			}
			old.Payload, _ = json.Marshal(value)
			return store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: old, Expected: old.Revision}})
		}); err != nil {
			t.Fatal(err)
		}
		if err := i.finalizeDatabase(t.Context()); err == nil {
			t.Fatal("changed database identity finalized", field)
		}
		if _, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "finalize"}); err == nil {
			t.Fatal("changed database identity reconciled", field)
		}
	}
}
