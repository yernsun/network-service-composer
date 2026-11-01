package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func installationOwnerFixture(t *testing.T, c *Core, runtime, pool string) bootstrap.Installation {
	t.Helper()
	value := bootstrap.Installation{ID: "installation-fixture", Revision: 2, ClusterID: "cluster", PoolID: pool, RuntimeID: runtime, MaintenanceAnchor: "anchor", Database: bootstrap.DatabasePin{NodeID: "node", EngineID: "engine", Volume: "volume", ServiceID: "postgresql-object", SinglePointOfFailure: true}, Components: map[string]bootstrap.Resource{}, Nodes: map[string]bootstrap.Identity{}, AllowedOperations: []bootstrap.Action{}, Drift: []string{}, History: []bootstrap.ReleaseReceipt{}}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		value.Components[key] = bootstrap.Resource{Kind: "service", ID: key + "-object", SpecDigest: "owned", Version: 1}
	}
	if err := c.saveControl(t.Context(), bootstrap.InstallationDocumentID, bootstrap.InstallationDocumentKind, value.ID, value, 0); err != nil {
		t.Fatal(err)
	}
	return value
}
func TestManagementModesRequireExplicitInstallationAndObjectRegistration(t *testing.T) {
	c, rt, pool := controlFixture(t)
	applications := []model.Application{{ID: "observed", RuntimeID: rt.ID}, {ID: "application", RuntimeID: rt.ID, Managed: true}, {ID: "legacy", RuntimeID: rt.ID, System: true, SystemKey: "controller"}, {ID: "owned", RuntimeID: rt.ID, System: true, SystemKey: "controller"}, {ID: "foreign-runtime", RuntimeID: "other", System: true, SystemKey: "controller"}}
	if err := c.ApplicationManagement(t.Context(), applications); err != nil || applications[2].ManagementMode != "observed" || applications[1].ManagementMode != "application" {
		t.Fatal(applications, err)
	}
	owner := installationOwnerFixture(t, c, rt.ID, pool.ID)
	if err := c.saveControl(t.Context(), "owned", "system-application", "", map[string]any{"runtime_id": rt.ID, "cluster_id": owner.ClusterID, "object_ids": []string{"controller-object"}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplicationManagement(t.Context(), applications); err != nil || applications[3].ManagementMode != "system" || applications[3].InstallationID != owner.ID || applications[2].ManagementMode != "observed" || applications[4].ManagementMode != "observed" {
		t.Fatal(applications, err)
	}
	services := []model.Service{{Managed: true}, {System: true, RuntimeID: rt.ID, RuntimeObjectID: "controller-object"}, {System: true, RuntimeID: "other", RuntimeObjectID: "controller-object"}, {System: true, RuntimeID: rt.ID, RuntimeObjectID: "same-name-but-foreign"}}
	if err := c.ServiceManagement(t.Context(), services); err != nil || services[0].ManagementMode != "application" || services[1].ManagementMode != "system" || services[2].ManagementMode != "observed" || services[3].ManagementMode != "observed" {
		t.Fatal(services, err)
	}
	view, err := c.SystemInstallation(t.Context())
	if err != nil || len(view.AllowedOperations) != 0 || len(view.Drift) == 0 {
		t.Fatal(view, err)
	}
	for _, objects := range [][]string{{"controller", "executor"}, {"controller", "controller"}, {"controller"}, {"foreign", "executor"}} {
		valid := registeredComponent(objects, "controller", "executor", "controller")
		if valid != (len(objects) == 2 && objects[0] == "controller" && objects[1] == "executor") {
			t.Fatal(objects, valid)
		}
	}
}
func TestCorruptInstallationCannotGrantManagement(t *testing.T) {
	for _, scenario := range []string{"kind", "parent", "payload", "duplicate", "database", "read"} {
		t.Run(scenario, func(t *testing.T) {
			c, rt, pool := controlFixture(t)
			owner := installationOwnerFixture(t, c, rt.ID, pool.ID)
			document, err := c.store.GetControlDocument(t.Context(), bootstrap.InstallationDocumentID)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "kind":
				document.Kind = "ordinary"
			case "parent":
				document.ParentID = "other"
			case "payload":
				document.Payload = []byte(`{`)
			case "duplicate":
				owner.Components["nginx"] = owner.Components["controller"]
				document.Payload, _ = json.Marshal(owner)
			case "database":
				owner.Database.NodeID = ""
				document.Payload, _ = json.Marshal(owner)
			}
			failed := error(nil)
			if scenario == "read" {
				failed = errors.New("storage unavailable")
			}
			c.store = installationReadStore{Store: c.store, document: document, err: failed}
			if _, err = c.SystemInstallation(t.Context()); err == nil {
				t.Fatal("invalid ownership displayed as managed")
			}
			if err = c.ApplicationManagement(t.Context(), []model.Application{{System: true}}); err == nil {
				t.Fatal("invalid ownership granted application management")
			}
			if err = c.ServiceManagement(t.Context(), []model.Service{{System: true}}); err == nil {
				t.Fatal("invalid ownership granted service management")
			}
		})
	}
}

type installationReadStore struct {
	storage.Store
	document storage.ControlDocument
	err      error
}

func (s installationReadStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if key == bootstrap.InstallationDocumentID {
		return s.document, s.err
	}
	return s.Store.GetControlDocument(ctx, key)
}
func TestInstallationNotificationsAreGlobalDeduplicatedAndRetainFailures(t *testing.T) {
	c, settings := notificationFixture(t, "deployments")
	runtimes, err := c.store.ListRuntimes(t.Context(), storage.Page{Limit: 1})
	if err != nil || len(runtimes) != 1 {
		t.Fatal(err)
	}
	owner := installationOwnerFixture(t, c, runtimes[0].ID, runtimes[0].PoolID)
	for _, state := range []bootstrap.State{bootstrap.Pending, bootstrap.Running, bootstrap.Unknown, bootstrap.Failed, bootstrap.Blocked, bootstrap.Succeeded} {
		job := bootstrap.MaintenanceJob{ID: "maintenance-" + string(state), InstallationID: owner.ID, State: state, Plan: bootstrap.Plan{Action: bootstrap.Upgrade}, CreatedAt: c.now(), UpdatedAt: c.now()}
		if err = c.saveControl(t.Context(), job.ID, "system-installation-job", owner.ID, job, 0); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err = c.installationNotifications(t.Context(), c.now().Add(-time.Second), settings.Revision); err != nil {
			t.Fatal(err)
		}
	}
	rows := notices(t, c)
	if len(rows) != 4 {
		t.Fatal("state transitions were lost or polls duplicated notifications", len(rows))
	}
	for _, row := range rows {
		if row.Event.OperationID == "" || len(row.Event.Scopes) != 0 {
			t.Fatal("system maintenance leaked into a business environment", row)
		}
	}
	if err = c.installationNotifications(t.Context(), c.now().Add(time.Second), settings.Revision); err != nil {
		t.Fatal(err)
	}
}

type installationNotificationStore struct {
	storage.Store
	documents []storage.ControlDocument
	listErr   error
}

func (s installationNotificationStore) ListControlDocuments(ctx context.Context, kind, parent string, page storage.Page) ([]storage.ControlDocument, error) {
	if kind == "system-installation-job" {
		return s.documents, s.listErr
	}
	return s.Store.ListControlDocuments(ctx, kind, parent, page)
}
func TestInstallationNotificationsRejectForeignCorruptAndUnavailableSources(t *testing.T) {
	for _, scenario := range []string{"unmanaged", "read", "payload", "installation", "parent"} {
		t.Run(scenario, func(t *testing.T) {
			c, settings := notificationFixture(t, "deployments")
			if scenario == "unmanaged" {
				if err := c.installationNotifications(t.Context(), c.now(), settings.Revision); err != nil || len(notices(t, c)) != 0 {
					t.Fatal("unmanaged resources emitted system events", err)
				}
				return
			}
			runtimes, err := c.store.ListRuntimes(t.Context(), storage.Page{Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			owner := installationOwnerFixture(t, c, runtimes[0].ID, runtimes[0].PoolID)
			job := bootstrap.MaintenanceJob{ID: "isolated-job", InstallationID: owner.ID, State: bootstrap.Failed}
			document := storage.ControlDocument{ID: "isolated-job", Kind: "system-installation-job", ParentID: owner.ID, UpdatedAt: c.now()}
			if scenario == "installation" {
				job.InstallationID = "foreign"
			}
			document.Payload, _ = json.Marshal(job)
			if scenario == "payload" {
				document.Payload = []byte("{")
			}
			if scenario == "parent" {
				document.ParentID = "foreign"
			}
			var unavailable error
			if scenario == "read" {
				unavailable = errors.New("notification source unavailable")
			}
			c.store = installationNotificationStore{Store: c.store, documents: []storage.ControlDocument{document}, listErr: unavailable}
			if err = c.installationNotifications(t.Context(), c.now().Add(-time.Second), settings.Revision); err == nil {
				t.Fatal("unverified source was accepted", scenario)
			}
			if len(notices(t, c)) != 0 {
				t.Fatal("foreign or malformed job emitted an event")
			}
		})
	}
}
func TestInstallationNotificationsPaginateWithoutLosingOrDuplicatingCompletions(t *testing.T) {
	c, settings := notificationFixture(t, "deployments")
	runtimes, err := c.store.ListRuntimes(t.Context(), storage.Page{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	owner := installationOwnerFixture(t, c, runtimes[0].ID, runtimes[0].PoolID)
	writes := []storage.ControlWrite{}
	for n := range 201 {
		key := fmt.Sprintf("maintenance-page-%03d", n)
		job := bootstrap.MaintenanceJob{ID: key, InstallationID: owner.ID, State: bootstrap.Succeeded, Plan: bootstrap.Plan{Action: bootstrap.Upgrade}, UpdatedAt: c.now()}
		raw, _ := json.Marshal(job)
		writes = append(writes, storage.ControlWrite{Document: storage.ControlDocument{ID: key, Kind: "system-installation-job", ParentID: owner.ID, Payload: raw, CreatedAt: c.now(), UpdatedAt: c.now()}})
	}
	if err = c.store.WriteControlDocuments(t.Context(), writes); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = c.installationNotifications(t.Context(), c.now().Add(-time.Second), settings.Revision); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	page := storage.Page{Limit: 200}
	for range 3 {
		rows, err := c.ListNotifications(t.Context(), page)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if seen[row.ID] {
				t.Fatal("notification repeated across pages")
			}
			seen[row.ID] = true
		}
		if len(rows) < page.Limit {
			break
		}
		page.After = rows[len(rows)-1].ID
	}
	if len(seen) != 201 {
		t.Fatal("pagination lost or duplicated maintenance outcomes", len(seen))
	}
}
