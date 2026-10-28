package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/runtime/engine/enginetest"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type collectionBoundaryClient struct {
	*enginetest.Client
	stage      string
	failure    error
	closed     atomic.Int32
	inspection container.InspectResponse
}

func (c *collectionBoundaryClient) Info(ctx context.Context) (system.Info, error) {
	if c.stage == "info" {
		return system.Info{}, c.failure
	}
	return c.Client.Info(ctx)
}
func (c *collectionBoundaryClient) ContainerList(ctx context.Context) ([]container.Summary, error) {
	if c.stage == "containers" {
		return nil, c.failure
	}
	return c.Client.ContainerList(ctx)
}
func (c *collectionBoundaryClient) NodeList(ctx context.Context) ([]swarm.Node, error) {
	if c.stage == "nodes" {
		return nil, c.failure
	}
	return c.Client.NodeList(ctx)
}
func (c *collectionBoundaryClient) ServiceList(ctx context.Context) ([]swarm.Service, error) {
	if c.stage == "services" {
		return nil, c.failure
	}
	return c.Client.ServiceList(ctx)
}
func (c *collectionBoundaryClient) TaskList(ctx context.Context) ([]swarm.Task, error) {
	if c.stage == "tasks" {
		return nil, c.failure
	}
	return c.Client.TaskList(ctx)
}
func (c *collectionBoundaryClient) ContainerStats(ctx context.Context, id string) (container.StatsResponse, error) {
	if c.stage == "stats" {
		return container.StatsResponse{}, c.failure
	}
	return c.Client.ContainerStats(ctx, id)
}
func (c *collectionBoundaryClient) ContainerInspect(context.Context, string) (container.InspectResponse, error) {
	if c.stage == "inspect" {
		return container.InspectResponse{}, c.failure
	}
	return c.inspection, nil
}
func (c *collectionBoundaryClient) Close() error { c.closed.Add(1); return nil }

type collectionBoundaryStore struct {
	*enginetest.Store
	stage   string
	failure error
}

func (s *collectionBoundaryStore) ListRuntimes(ctx context.Context, p storage.Page) ([]model.Runtime, error) {
	if s.stage == "runtimes" {
		return nil, s.failure
	}
	return s.Store.ListRuntimes(ctx, p)
}
func (s *collectionBoundaryStore) ListServicesFiltered(ctx context.Context, f storage.ServiceFilter, p storage.Page) ([]model.Service, error) {
	if s.stage == "references" {
		return nil, s.failure
	}
	return s.Store.ListServicesFiltered(ctx, f, p)
}

func TestCollectionFailuresPreserveEvidenceAndCloseClients(t *testing.T) {
	for _, stage := range []string{"runtimes", "references", "credentials", "factory", "info", "containers", "nodes", "services", "tasks", "stats", "inspect", "empty engine"} {
		t.Run(stage, func(t *testing.T) {
			m, base, cli := fixtureMonitor()
			ctx := context.Background()
			if e := m.Refresh(ctx); e != nil {
				t.Fatal(e)
			}
			before := m.sources[connection(base.Runtimes[0])]
			failure := errors.New("isolated monitoring failed")
			store := &collectionBoundaryStore{Store: base, failure: failure}
			client := &collectionBoundaryClient{Client: cli, failure: failure}
			m.store = store
			client.stage = stage
			store.stage = stage
			m.factory = func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) {
				if stage == "factory" {
					return nil, failure
				}
				return client, nil
			}
			if stage == "credentials" {
				base.Runtimes[0].CredentialRef = "missing"
			}
			if stage == "empty engine" {
				cli.System.ID = ""
			}
			e := m.Refresh(ctx)
			if stage == "runtimes" {
				if !errors.Is(e, failure) || !m.sources[connection(base.Runtimes[0])].At.Equal(before.At) {
					t.Fatal("runtime read failure discarded prior evidence", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			key := connection(base.Runtimes[0])
			after := m.sources[key]
			switch stage {
			case "credentials", "factory", "info", "containers", "nodes", "services", "tasks":
				if after.Error != "collection_failed" {
					t.Fatal("failed source reported current", after)
				}
				if stage != "credentials" && !after.At.Equal(before.At) {
					t.Fatal("failure extended evidence lifetime")
				}
			case "stats":
				v := m.Metrics("engine-local", "container-a")
				if v.UnavailableReason != "stale" || v.Current == nil || !v.Current.Stale || len(v.Samples) == 0 || v.Samples[len(v.Samples)-1].Error != "collection_failed" {
					t.Fatal("failed metrics presented as current", v)
				}
			case "references":
				if len(m.references[base.Runtimes[0].ID]) != 0 {
					t.Fatal("failed reference read reused stale ownership")
				}
			case "empty engine":
				v := m.Metrics("engine-local", "container-a")
				if v.UnavailableReason != "stale" || v.Current == nil || !v.Current.Stale || len(v.Samples) == 0 || v.Samples[len(v.Samples)-1].Error != "collection_failed" {
					t.Fatal("unidentified engine retained current metrics", v)
				}
			}
			expected := int32(1)
			if stage == "factory" || stage == "credentials" {
				expected = 0
			}
			if client.closed.Load() != expected {
				t.Fatal("monitoring client leaked", client.closed.Load())
			}
		})
	}
}

func TestCollectionHealthErrorsAndCacheBounds(t *testing.T) {
	for _, stage := range []string{"no health", "healthy", "unhealthy no log", "unhealthy nil log", "unhealthy output", "unhealthy long output", "explicit error", "inspect failure", "cache limit"} {
		t.Run(stage, func(t *testing.T) {
			m, _, cli := fixtureMonitor()
			cli.Containers = cli.Containers[:1]
			state := &container.State{Error: ""}
			expectedHealth := "none"
			expectedError := ""
			switch stage {
			case "healthy":
				state.Health = &container.Health{Status: container.Healthy}
				expectedHealth = "healthy"
			case "unhealthy no log", "unhealthy nil log", "unhealthy output", "unhealthy long output", "explicit error":
				state.Health = &container.Health{Status: container.Unhealthy}
				expectedHealth = "unhealthy"
				switch stage {
				case "unhealthy nil log":
					state.Health.Log = []*container.HealthcheckResult{nil}
				case "unhealthy output":
					state.Health.Log = []*container.HealthcheckResult{{Output: "readiness refused"}}
					expectedError = "readiness refused"
				case "unhealthy long output":
					state.Health.Log = []*container.HealthcheckResult{{Output: strings.Repeat("x", 5000)}}
					expectedError = strings.Repeat("x", 4096)
				case "explicit error":
					state.Error = "container failed"
					state.Health.Log = []*container.HealthcheckResult{{Output: "secondary"}}
					expectedError = "container failed"
				}
			}
			client := &collectionBoundaryClient{Client: cli, inspection: container.InspectResponse{State: state}, failure: errors.New("inspect failed")}
			if stage == "inspect failure" {
				client.stage = "inspect"
			}
			m.factory = func(model.Runtime, engine.TLSFiles) (engine.MonitoringClient, error) { return client, nil }
			if stage == "cache limit" {
				for i := 0; i < 4096; i++ {
					m.details[fmt.Sprint(i)] = containerDetail{At: m.now()}
				}
			}
			if e := m.Refresh(context.Background()); e != nil {
				t.Fatal(e)
			}
			detail, ok := m.details["engine-local/container-a"]
			if stage == "inspect failure" {
				if ok {
					t.Fatal("failed inspection invented detail")
				}
				return
			}
			if !ok || detail.Health != expectedHealth || detail.Error != expectedError {
				t.Fatal("health evidence changed", detail)
			}
			if len(m.details) > 4096 {
				t.Fatal("inspection cache unbounded")
			}
		})
	}
}

func TestCollectionReadsAllPagesAndPreservesNewerNodeSpec(t *testing.T) {
	m, store, cli := fixtureMonitor()
	base := store.Runtimes[0]
	for i := 0; i < 200; i++ {
		v := base
		v.ID = fmt.Sprintf("runtime-%03d", i)
		v.Enabled = false
		store.Runtimes = append(store.Runtimes, v)
	}
	for i := 0; i < 201; i++ {
		store.Services = append(store.Services, model.Service{ID: fmt.Sprintf("service-%03d", i), RuntimeID: base.ID, ServiceKey: fmt.Sprintf("stack/service-%03d", i), RuntimeObjectID: fmt.Sprintf("docker-%03d", i)})
	}
	cli.Nodes[0].Version.Index = 3
	fresh := cli.Nodes[0]
	fresh.Version.Index = 9
	fresh.Spec.Labels = map[string]string{"zone": "new"}
	m.sources[connection(base)] = source{Nodes: []swarm.Node{fresh}, At: m.now()}
	base.CredentialRef = ""
	if e := m.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(m.runtimes) != 201 || len(m.references[base.ID]) != 402 || m.references[base.ID]["docker-200"] != "service-200" {
		t.Fatal("pagination dropped runtime or service references")
	}
	current := m.sources[connection(base)].Nodes[0]
	if current.Version.Index != 9 || current.Spec.Labels["zone"] != "new" {
		t.Fatal("collection overwrote newer label write", current)
	}
	var files engine.TLSFiles
	m.config.Credentials = map[string]config.DockerCredential{"fixture": {CAFile: "ca", CertFile: "cert", KeyFile: "key"}}
	m.factory = func(_ model.Runtime, f engine.TLSFiles) (engine.MonitoringClient, error) { files = f; return cli, nil }
	base.CredentialRef = "fixture"
	c, e := m.client(base)
	if e != nil || c == nil || files.CA != "ca" || files.Certificate != "cert" || files.Key != "key" {
		t.Fatal("configured TLS identity not used", e, files)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if e := m.Run(ctx); e != nil || time.Since(start) > time.Second {
		t.Fatal("monitor ignored cancellation", e)
	}
}
