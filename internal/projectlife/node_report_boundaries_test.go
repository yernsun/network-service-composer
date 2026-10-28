package projectlife

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestProvisionedNodeReaderCarriesAuthenticatedStorageSnapshot(t *testing.T) {
	for _, scenario := range []string{"complete", "storage incomplete", "inventory incomplete", "expired", "unknown node"} {
		t.Run(scenario, func(t *testing.T) {
			store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "node.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			keys, err := security.NewKeyring("test", map[string]string{"test": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{Controller: config.ControllerConfig{Nodes: []config.ControllerNodeConfig{{InstanceID: "controller", NodeID: "worker", EngineID: "engine", ClusterID: "cluster"}}}}
			now := time.Now().UTC()
			if scenario == "expired" {
				now = now.Add(-time.Minute)
			}
			reports := nodecontrol.Reports{Store: store, Keys: keys, Bindings: map[string]nodecontrol.Binding{"controller": {InstanceID: "controller", NodeID: "worker", EngineID: "engine", ClusterID: "cluster"}}, Now: func(context.Context) (time.Time, error) { return now, nil }, CheckOwner: func(context.Context, string, int64) error { return nil }}
			session, err := reports.Begin(t.Context(), "controller", "worker", "engine", "boot", 11)
			if err != nil {
				t.Fatal(err)
			}
			value := nodecontrol.Report{NodeID: "worker", EngineID: "engine", Generation: session.Generation, Sequence: 1, Observation: nodecontrol.Observation{Complete: scenario != "inventory incomplete", StorageComplete: scenario != "storage incomplete", At: now, Info: system.Info{ID: "engine", Swarm: swarm.Info{NodeID: "worker", Cluster: &swarm.ClusterInfo{ID: "cluster"}}}, Volumes: map[string]time.Time{"data": now}, VolumeOwners: map[string]string{"data": "stack"}, ImageVolumes: map[string][]string{"db@sha256:fixed": {"/data"}}, Inspections: map[string]container.InspectResponse{"container": {ID: "container"}}}}
			if err = reports.Accept(t.Context(), "controller", value); err != nil {
				t.Fatal(err)
			}
			docker := NewDocker(cfg, store, keys)
			node := "worker"
			if scenario == "unknown node" {
				node = "unprovisioned"
			}
			got, err := docker.ReadNode(t.Context(), node)
			if scenario == "inventory incomplete" || scenario == "expired" || scenario == "unknown node" {
				if err == nil {
					t.Fatal("untrusted or stale report accepted")
				}
				if scenario == "unknown node" && !errors.Is(err, ErrDataRetention) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || got.Complete != (scenario == "complete") || got.Generation != session.Generation || got.Sequence != 1 || got.NodeID != "worker" || !got.Volumes["data"].Equal(now) || got.VolumeOwners["data"] != "stack" || got.Containers["container"].ID != "container" || len(got.Images) != 1 {
				t.Fatal("snapshot fields or completeness lost", err, got)
			}
		})
	}
}

// Exercise the durable report reader at the destructive executor boundary.
// Accepting a report is separate from accepting it as complete retention proof.
func TestRemoteDeletionRejectsChangedDurableStorageEvidenceBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"complete", "storage incomplete", "inventory incomplete", "expired", "unprovisioned", "new generation", "recreated volume"} {
		t.Run(scenario, func(t *testing.T) {
			d, rt, app, services, fixture := runtimeFixture(t, true)
			fixture.remoteTask, fixture.dataNode = true, "uninspected-worker"
			store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "retention.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			keys, err := security.NewKeyring("test", map[string]string{"test": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			reports := nodecontrol.Reports{Store: store, Keys: keys, Bindings: map[string]nodecontrol.Binding{"collector": {InstanceID: "collector", NodeID: "uninspected-worker", EngineID: "engine", ClusterID: "cluster"}}, Now: func(context.Context) (time.Time, error) { return now, nil }, CheckOwner: func(context.Context, string, int64) error { return nil }}
			session, err := reports.Begin(t.Context(), "collector", "uninspected-worker", "engine", "boot", 11)
			if err != nil {
				t.Fatal(err)
			}
			actual := container.InspectResponse{ID: "remote-one", Config: &container.Config{Image: fixture.image, Labels: map[string]string{"com.docker.swarm.service.id": "service-one", "com.docker.swarm.task.id": "task-one"}}, State: &container.State{Running: true}, Mounts: []container.MountPoint{{Type: "volume", Name: "fixture-data", Destination: "/data"}}}
			report := nodecontrol.Report{NodeID: "uninspected-worker", EngineID: "engine", Generation: session.Generation, Sequence: 1, Observation: nodecontrol.Observation{Complete: true, StorageComplete: true, At: now, Info: system.Info{ID: "engine", Swarm: swarm.Info{NodeID: "uninspected-worker", Cluster: &swarm.ClusterInfo{ID: "cluster"}}}, Volumes: map[string]time.Time{"fixture-data": now}, VolumeOwners: map[string]string{"fixture-data": app.ProjectName}, ImageVolumes: map[string][]string{fixture.image: {"/data"}}, Inspections: map[string]container.InspectResponse{actual.ID: actual}}}
			if err = reports.Accept(t.Context(), "collector", report); err != nil {
				t.Fatal(err)
			}
			cfg := d.Config
			cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "collector", NodeID: "uninspected-worker", EngineID: "engine", ClusterID: "cluster"}}
			d = NewDocker(cfg, store, keys)
			observed, err := d.Observe(t.Context(), rt, app, services)
			if err != nil || len(observed.Retained) != 1 {
				t.Fatal("complete durable Worker proof was not admitted", observed, err)
			}
			if scenario == "complete" {
				if err = VerifyRetainedVolumes(t.Context(), d.ReadNode, observed.Retained); err != nil {
					t.Fatal(err)
				}
				return
			}
			switch scenario {
			case "storage incomplete":
				report.Observation.StorageComplete = false
			case "inventory incomplete":
				report.Observation.Complete = false
			case "expired":
				now = now.Add(-time.Minute)
				report.Observation.At = now
			case "recreated volume":
				report.Observation.Volumes["fixture-data"] = now.Add(time.Second)
			case "new generation":
				if _, err = reports.Begin(t.Context(), "collector", "uninspected-worker", "engine", "replacement-boot", 12); err != nil {
					t.Fatal(err)
				}
			case "unprovisioned":
				cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "other", NodeID: "other-worker", EngineID: "other-engine"}}
				d = NewDocker(cfg, store, keys)
			}
			if scenario != "new generation" && scenario != "unprovisioned" {
				report.Sequence++
				if err = reports.Accept(t.Context(), "collector", report); err != nil {
					t.Fatal(err)
				}
			}
			if err = d.Remove(t.Context(), rt, app, services, observed, true); err == nil {
				t.Fatal("force deletion accepted untrusted or changed data")
			}
			if len(fixture.requests) != 0 || fixture.missing {
				t.Fatal("destructive request preceded retention rejection", fixture.requests)
			}
		})
	}
}

func TestUnprovisionedOrUnsupportedStoreDoesNotPretendToOfferRemoteEvidence(t *testing.T) {
	if d := NewDocker(config.Config{}, nil, nil); d.ReadNode != nil {
		t.Fatal("unconfigured node reader fabricated")
	}
	cfg := config.Config{Controller: config.ControllerConfig{Nodes: []config.ControllerNodeConfig{{InstanceID: "controller", NodeID: "uninspected-worker", EngineID: "engine"}}}}
	if d := NewDocker(cfg, nil, nil); d.ReadNode != nil {
		t.Fatal("unsupported store fabricated durable report reader")
	}
}
