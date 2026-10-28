package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type initialStorageRuntime struct {
	retentionRuntimeFixture
	node      string
	names     []string
	newErr    error
	newChecks int
}

func (f *initialStorageRuntime) VerifyNewStorage(_ context.Context, _ model.Runtime, _ model.Application, node string, names []string) error {
	f.newChecks++
	f.node = node
	f.names = append([]string{}, names...)
	return f.newErr
}

func TestFirstStorageAdmissionFreezesPhysicalNamesAndRejectsUnknownExistingRecord(t *testing.T) {
	f := newWorkerFixture(t)
	app, op := installFixtureApplication(t, f, "services:\n  web: {image: example.test/web:fixed}\n")
	revision, err := f.store.GetApplicationRevision(t.Context(), op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	revision.ConfigurationSource.DeploymentPlan.DataNodeID = "worker"
	life := &initialStorageRuntime{}
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	w.lifecycle = life
	value := material{application: app, revision: revision, resolved: "services: {}\nvolumes:\n  data: {}\n  files:\n    name: fixed-files\n"}
	if err = w.verifyNewStorage(t.Context(), f.runtime, value); err != nil || life.newChecks != 1 || life.node != "worker" || len(life.names) != 2 {
		t.Fatal("new volume admission did not inspect reviewed names", err, life.names)
	}
	expected := map[string]bool{app.ProjectName + "_data": true, "fixed-files": true}
	for _, name := range life.names {
		if !expected[name] {
			t.Fatal("physical storage name was not frozen", name)
		}
	}
	life.newErr = projectlife.ErrDataRetention
	if err = w.verifyNewStorage(t.Context(), f.runtime, value); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("existing unrecorded volume adopted", err)
	}
	life.newErr = nil
	if err = w.markNewStorageMutation(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	record, _, err := w.retainedStorage(t.Context(), app)
	if err != nil || !record.RecoveryPending || record.ExpectedNode != "worker" || len(record.ExpectedVolumes) != 2 {
		t.Fatal("first mutation lacks durable frozen uncertainty", record, err)
	}
	checks := life.newChecks
	if err = w.verifyNewStorage(t.Context(), f.runtime, value); err != nil || life.newChecks != checks {
		t.Fatal("retry overwrote existing record", err)
	}
	for _, scenario := range []string{"missing expected volume", "wrong node", "unexpected name", "original names"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			o := projectlife.Observation{Retained: []projectlife.RetainedVolume{{NodeID: "worker", Name: app.ProjectName + "_data", CreatedAt: now, Owner: app.ProjectName, Target: "/data"}, {NodeID: "worker", Name: "fixed-files", CreatedAt: now, Owner: app.ProjectName, Target: "/files"}}}
			switch scenario {
			case "missing expected volume":
				o.Retained = o.Retained[:1]
			case "wrong node":
				o.Retained[0].NodeID = "other"
			case "unexpected name":
				o.Retained[0].Name = "replacement"
			}
			err := w.recordRetainedStorage(t.Context(), app, &o)
			if scenario == "original names" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, projectlife.ErrDataRetention) {
				t.Fatal("unknown initial result rebound to different storage", err)
			}
			persisted, _, err := w.retainedStorage(t.Context(), app)
			if err != nil || len(persisted.Volumes) != 0 || persisted.ExpectedNode != "worker" || !persisted.RecoveryPending {
				t.Fatal("rejection destroyed initial frozen evidence", persisted, err)
			}
		})
	}
	if err = w.markStorageRecoveryPending(t.Context(), app, false); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedStorageRecordRejectsExternalLayoutChangesWithoutLosingOriginal(t *testing.T) {
	f := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, f, "services:\n  web: {image: example.test/web:fixed}\n")
	original := projectlife.RetainedVolume{NodeID: "worker", Name: "original", CreatedAt: time.Now().UTC(), Owner: app.ProjectName, Target: "/data"}
	life := &retentionRuntimeFixture{stopRuntimeFixture: stopRuntimeFixture{observation: projectlife.Observation{Retained: []projectlife.RetainedVolume{original}}}}
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	w.lifecycle = life
	baseline := life.observation
	if err := w.recordRetainedStorage(t.Context(), app, &baseline); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"changed target", "changed creation identity", "changed owner", "extra volume", "implicit preserved mount", "no actual retention with live object", "changed actual retention", "verified stopped"} {
		t.Run(scenario, func(t *testing.T) {
			o := projectlife.Observation{Retained: []projectlife.RetainedVolume{original}}
			switch scenario {
			case "changed target":
				o.Retained[0].Target = "/other"
			case "changed creation identity":
				o.Retained[0].CreatedAt = original.CreatedAt.Add(time.Second)
			case "changed owner":
				o.Retained[0].Owner = "foreign"
			case "extra volume":
				o.Retained = append(o.Retained, original)
			case "implicit preserved mount":
				o.Preserved = []string{"bind:/unrecorded"}
			case "no actual retention with live object":
				o.Retained = nil
				o.Objects = []projectlife.Object{{ID: "live", Kind: "swarm-service", Active: true}}
			case "changed actual retention":
				o.Retained[0].Name = "other"
			case "verified stopped":
				o.Retained = nil
			}
			var err error
			if scenario == "no actual retention with live object" || scenario == "changed actual retention" || scenario == "verified stopped" {
				life.observation = o
				err = w.verifyRetainedStorage(t.Context(), app, "worker")
			} else {
				err = w.recordRetainedStorage(t.Context(), app, &o)
			}
			if scenario == "verified stopped" {
				if err != nil {
					t.Fatal("verified original storage cannot resume", err)
				}
			} else if !errors.Is(err, projectlife.ErrDataRetention) {
				t.Fatal("changed data scope accepted", err)
			}
			kept, _, readErr := w.retainedStorage(t.Context(), app)
			if readErr != nil || len(kept.Volumes) != 1 || kept.Volumes[0] != original {
				t.Fatal("rejected layout lost original identity", kept, readErr)
			}
		})
	}
}

func TestInvalidRetentionDocumentsNeverAuthorizeLifecycleOrReplaceEvidence(t *testing.T) {
	f := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, f, "services:\n  web: {image: example.test/web:fixed}\n")
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	for _, scenario := range []string{"wrong application", "wrong runtime", "empty confirmed volumes", "broken payload"} {
		t.Run(scenario, func(t *testing.T) {
			value := storageRetention{ApplicationID: app.ID, RuntimeID: app.RuntimeID, Volumes: []projectlife.RetainedVolume{{NodeID: "worker", Name: "data", Owner: app.ProjectName, CreatedAt: time.Now()}}}
			switch scenario {
			case "wrong application":
				value.ApplicationID = "other"
			case "wrong runtime":
				value.RuntimeID = "other"
			case "empty confirmed volumes":
				value.Volumes = nil
			}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "broken payload" {
				raw = []byte(`{`)
			}
			previous, readErr := f.store.GetControlDocument(t.Context(), retentionKey(app))
			if readErr != nil && !errors.Is(readErr, storage.ErrNotFound) {
				t.Fatal(readErr)
			}
			now := time.Now().UTC()
			if err = f.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Expected: previous.Revision, Document: storage.ControlDocument{ID: retentionKey(app), Kind: "application-retention", ParentID: app.ID, Payload: raw, CreatedAt: now, UpdatedAt: now}}}); err != nil {
				t.Fatal(err)
			}
			if err = w.verifyRetainedStorage(t.Context(), app, "worker"); !errors.Is(err, projectlife.ErrDataRetention) {
				t.Fatal("invalid document authorized operation", err)
			}
			o := projectlife.Observation{Retained: value.Volumes}
			if err = w.recordRetainedStorage(t.Context(), app, &o); !errors.Is(err, projectlife.ErrDataRetention) {
				t.Fatal("unknown record overwritten", err)
			}
			if err = w.markStorageRecoveryPending(t.Context(), app, false); !errors.Is(err, projectlife.ErrDataRetention) {
				t.Fatal("invalid recovery barrier cleared", err)
			}
			got, err := f.store.GetControlDocument(t.Context(), retentionKey(app))
			if err != nil || string(got.Payload) != string(raw) {
				t.Fatal("rejected lifecycle altered evidence", err)
			}
		})
	}
}

func TestStatefulMutationRequiresRetentionAdapterAndNonemptyNamedStorage(t *testing.T) {
	f := newWorkerFixture(t)
	app, op := installFixtureApplication(t, f, "services:\n  web: {image: example.test/web:fixed}\n")
	revision, err := f.store.GetApplicationRevision(t.Context(), op.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	revision.ConfigurationSource.DeploymentPlan.DataNodeID = "worker"
	w := NewWorkerWithRunner(f.store, nil, f.keyring, f.config, nil, &recordingRunner{})
	w.lifecycle = &stopRuntimeFixture{}
	value := material{application: app, revision: revision, resolved: "services: {}\nvolumes: {data: {}}\n"}
	if err = w.verifyNewStorage(t.Context(), f.runtime, value); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("missing evidence adapter admitted stateful deployment", err)
	}
	value.resolved = "services: {}\n"
	if err = w.markNewStorageMutation(t.Context(), value); !errors.Is(err, projectlife.ErrDataRetention) {
		t.Fatal("stateful mutation without named storage admitted", err)
	}
	value.resolved = "not: [valid"
	if err = w.markNewStorageMutation(t.Context(), value); err == nil {
		t.Fatal("invalid storage template admitted")
	}
	if _, err = f.store.GetControlDocument(t.Context(), retentionKey(app)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("rejected mutation created false evidence", err)
	}
	revision.ConfigurationSource.DeploymentPlan.DataNodeID = ""
	value.revision = revision
	if err = w.markNewStorageMutation(t.Context(), value); err != nil {
		t.Fatal("stateless operation needs retention record", err)
	}
	if err = w.verifyNewStorage(t.Context(), f.runtime, value); err != nil {
		t.Fatal(err)
	}
}
