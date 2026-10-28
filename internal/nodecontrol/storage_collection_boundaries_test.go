package nodecontrol

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/volume"
	"github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
)

type storageCollectionEngine struct {
	*collectingEngine
	volumes                    []volume.Volume
	images                     []image.Summary
	metadata                   map[string]image.InspectResponse
	inventoryErr, imageListErr error
	inspectMissing             string
	inventoryFailure           string
}

func (c *storageCollectionEngine) VolumeList(context.Context) ([]volume.Volume, error) {
	return c.volumes, c.inventoryErr
}
func (c *storageCollectionEngine) ImageList(context.Context) ([]image.Summary, error) {
	return c.images, c.imageListErr
}
func (c *storageCollectionEngine) ImageInspect(_ context.Context, ref string) (image.InspectResponse, error) {
	v, ok := c.metadata[ref]
	if !ok {
		return v, errors.New("image metadata unavailable")
	}
	return v, nil
}
func (c *storageCollectionEngine) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if id == c.inspectMissing {
		return container.InspectResponse{}, errors.New("container inspection unavailable")
	}
	return c.Client.ContainerInspect(ctx, id)
}
func (c *storageCollectionEngine) ContainerList(ctx context.Context) ([]container.Summary, error) {
	if c.inventoryFailure == "containers" {
		return nil, errors.New("container list unavailable")
	}
	return c.Client.ContainerList(ctx)
}

func TestStorageCollectionKeepsFullEvidenceSeparateFromUsableInventory(t *testing.T) {
	for _, scenario := range []string{"complete", "volume inventory partial", "invalid creation identity", "image inventory partial", "image inspect missing", "image config missing", "container inspect missing", "container config missing", "stats unavailable", "no storage adapter"} {
		t.Run(scenario, func(t *testing.T) {
			_, base := enginetest.Fixture()
			base.Containers = []container.Summary{{ID: "db", State: container.StateRunning}, {ID: "old", State: container.StateExited}}
			base.Inspections = map[string]container.InspectResponse{}
			ref, digest := "db:fixed", "sha256:db"
			for _, id := range []string{"db", "old"} {
				base.Inspections[id] = container.InspectResponse{ID: id, Image: digest, Config: &container.Config{Image: ref}}
			}
			created := time.Now().UTC().Truncate(time.Second)
			local := &storageCollectionEngine{collectingEngine: &collectingEngine{Client: base}, volumes: []volume.Volume{{Name: "stack_data", Driver: "local", CreatedAt: created.Format(time.RFC3339Nano), Labels: map[string]string{"com.docker.stack.namespace": "stack"}}, {Name: "external", Driver: "nfs", CreatedAt: "unknown"}}, images: []image.Summary{{ID: digest, RepoDigests: []string{"db@sha256:db"}}}, metadata: map[string]image.InspectResponse{}}
			for _, ref := range []string{ref, digest, "db@sha256:db"} {
				local.metadata[ref] = image.InspectResponse{ID: digest, RepoDigests: []string{"db@sha256:db"}, Config: &dockerspec.DockerOCIImageConfig{ImageConfig: v1.ImageConfig{Volumes: map[string]struct{}{"/data": {}}}}}
			}
			switch scenario {
			case "volume inventory partial":
				local.inventoryErr = errors.New("volume list unavailable")
			case "invalid creation identity":
				local.volumes[0].CreatedAt = "not an identity"
			case "image inventory partial":
				local.imageListErr = errors.New("image list unavailable")
			case "image inspect missing":
				delete(local.metadata, ref)
			case "image config missing":
				local.metadata[digest] = image.InspectResponse{ID: digest}
			case "container inspect missing":
				local.inspectMissing = "old"
			case "container config missing":
				v := base.Inspections["old"]
				v.Config = nil
				base.Inspections["old"] = v
			case "stats unavailable":
				local.statsFailure = true
			}
			cli := engine.MonitoringClient(local)
			if scenario == "no storage adapter" {
				cli = local.collectingEngine
			}
			collector := Collector{Binding: Binding{NodeID: "node-a", EngineID: "engine-local", ClusterID: "cluster-a"}, Open: func() (engine.MonitoringClient, error) { return cli, nil }}
			observed, err := collector.Collect(t.Context())
			if err != nil || !observed.Complete || !local.closed {
				t.Fatal("ordinary inventory lost", err, observed.Complete, local.closed)
			}
			complete := scenario == "complete" || scenario == "stats unavailable" || scenario == "container config missing"
			if observed.StorageComplete != complete {
				t.Fatalf("storage completeness=%v, want %v", observed.StorageComplete, complete)
			}
			if scenario == "no storage adapter" {
				if observed.Volumes != nil || observed.ImageVolumes != nil {
					t.Fatal("fabricated storage adapter evidence")
				}
				return
			}
			if _, ok := observed.Volumes["external"]; ok {
				t.Fatal("remote driver treated as node-local storage")
			}
			if scenario != "invalid creation identity" && !observed.Volumes["stack_data"].Equal(created) {
				t.Fatal("volume creation identity lost")
			}
			if observed.VolumeOwners["stack_data"] != "stack" {
				t.Fatal("volume owner omitted")
			}
			if complete && !slices.Equal(observed.ImageVolumes[digest], []string{"/data"}) {
				t.Fatal("immutable image volume declarations lost")
			}
			if scenario == "container inspect missing" && observed.Errors["inspect:old"] != "unavailable" {
				t.Fatal("partial container evidence hidden")
			}
			if scenario == "stats unavailable" && len(observed.Stats) != 0 {
				t.Fatal("missing metrics became zero samples")
			}
		})
	}
}

func TestCollectionFailureDoesNotReturnFreshInventory(t *testing.T) {
	sentinel := errors.New("engine unavailable")
	c := Collector{Open: func() (engine.MonitoringClient, error) { return nil, sentinel }}
	if observed, err := c.Collect(t.Context()); !errors.Is(err, sentinel) || observed.Complete {
		t.Fatal("open failure marked complete", err)
	}
	_, base := enginetest.Fixture()
	local := &storageCollectionEngine{collectingEngine: &collectingEngine{Client: base}, inventoryFailure: "containers"}
	c = Collector{Binding: Binding{NodeID: "node-a", EngineID: "engine-local"}, Open: func() (engine.MonitoringClient, error) { return local, nil }}
	if observed, err := c.Collect(t.Context()); err == nil || observed.Complete || !local.closed {
		t.Fatal("partial inventory treated as complete", err)
	}
}
