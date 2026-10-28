package projectlife

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
)

// This engine protocol fixture executes the production SDK transport. It records
// destructive requests and models lingering tasks independently of service removal.
type engineFixture struct {
	actualContainer, containerRunning, containerLingers       bool
	mu                                                        sync.Mutex
	active, foreign, missing, lingering, failRead, failRemove bool
	swarm                                                     bool
	version                                                   int
	requests                                                  []string
	image                                                     string
	mounts                                                    []map[string]string
	imageVolumes                                              []string
	missingImage, nilImageConfig, oldAnonymousTask            bool
	remoteTask                                                bool
	dataNode                                                  string
	healthcheck                                               bool
	taskMounts                                                []map[string]string
	containerMounts                                           []map[string]string
}

func (f *engineFixture) spec() map[string]any {
	spec := map[string]any{"Image": f.image, "Mounts": f.mounts}
	if f.healthcheck {
		spec["Healthcheck"] = map[string]any{"Test": []string{"CMD", "true"}}
	}
	return spec
}

func (f *engineFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("API-Version", "1.54")
	if r.URL.Path == "/_ping" {
		w.Write([]byte("OK"))
		return
	}
	path := r.URL.Path[strings.Index(r.URL.Path[1:], "/")+1:]
	w.Header().Set("Content-Type", "application/json")
	encode := func(v any) {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			panic(err)
		}
	}
	if f.failRead && r.Method == "GET" {
		w.WriteHeader(503)
		encode(map[string]string{"message": "engine offline"})
		return
	}
	if r.Method == "DELETE" || r.Method == "POST" {
		f.requests = append(f.requests, r.Method+" "+path+"?"+r.URL.RawQuery)
		if f.failRemove {
			w.WriteHeader(500)
			encode(map[string]string{"message": "outcome unknown"})
			return
		}
	}
	switch {
	case path == "/info":
		encode(map[string]any{"Swarm": map[string]string{"NodeID": "fixture-node"}})
	case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		if f.missingImage {
			w.WriteHeader(404)
			encode(map[string]string{"message": "image metadata unavailable"})
			return
		}
		if f.nilImageConfig {
			encode(map[string]any{"Id": "sha256:fixture"})
			return
		}
		volumes := map[string]any{}
		for _, target := range f.imageVolumes {
			volumes[target] = map[string]any{}
		}
		encode(map[string]any{"Id": "sha256:fixture", "Config": map[string]any{"Volumes": volumes}})
	case path == "/containers/json":
		if f.swarm {
			if r.URL.Query().Get("all") != "1" || !strings.Contains(r.URL.Query().Get("filters"), "com.docker.swarm.service.id") {
				w.WriteHeader(400)
				encode(map[string]string{"message": "missing Swarm container inventory"})
				return
			}
			if f.actualContainer && (!f.missing || f.containerLingers) {
				encode([]any{map[string]any{"Id": "container-one", "Labels": map[string]string{"com.docker.swarm.service.id": "service-one"}}})
			} else {
				encode([]any{})
			}
			return
		}
		if r.URL.Query().Get("all") != "1" || !strings.Contains(r.URL.Query().Get("filters"), "com.docker.compose.project=fixture") {
			w.WriteHeader(400)
			encode(map[string]string{"message": "unbounded container selection"})
			return
		}
		if f.missing {
			encode([]any{})
		} else {
			encode([]any{map[string]any{"Id": "container-one"}})
		}
	case path == "/containers/container-one/json":
		if f.swarm {
			encode(map[string]any{"Id": "container-one", "Config": map[string]any{"Labels": map[string]string{"com.docker.swarm.service.id": "service-one"}}, "State": map[string]any{"Running": f.containerRunning}, "Mounts": f.containerMounts})
			return
		}
		project := "fixture"
		if f.foreign {
			project = "another-project"
		}
		encode(map[string]any{"Id": "container-one", "Config": map[string]any{"Labels": map[string]string{"com.docker.compose.project": project, "com.docker.compose.service": "web"}}, "State": map[string]any{"Running": f.active}, "Mounts": []any{map[string]string{"Type": "volume", "Name": "fixture-data", "Source": "/var/lib/docker/volumes/fixture-data/_data"}, map[string]string{"Type": "bind", "Source": "/fixture/uploads"}}})
	case path == "/containers/container-one/stop":
		f.active = false
		w.WriteHeader(204)
	case path == "/containers/container-one" && r.Method == "DELETE":
		if f.active || r.URL.Query().Get("v") == "1" || r.URL.Query().Get("force") == "1" {
			w.WriteHeader(409)
			encode(map[string]string{"message": "unsafe removal"})
			return
		}
		f.missing = true
		w.WriteHeader(204)
	case path == "/services" && r.Method == "GET":
		if !strings.Contains(r.URL.Query().Get("filters"), "com.docker.stack.namespace=fixture") {
			w.WriteHeader(400)
			encode(map[string]string{"message": "unbounded stack selection"})
			return
		}
		if f.missing {
			encode([]any{})
			return
		}
		replicas := 0
		if f.active {
			replicas = 1
		}
		name := "fixture_web"
		if f.foreign {
			name = "another_web"
		}
		encode([]any{map[string]any{"ID": "service-one", "Version": map[string]int{"Index": f.version}, "Spec": map[string]any{"Name": name, "Labels": map[string]string{"com.docker.stack.namespace": "fixture"}, "Mode": map[string]any{"Replicated": map[string]int{"Replicas": replicas}}, "TaskTemplate": f.taskTemplate()}}})
	case path == "/tasks":
		if f.lingering || (f.active && !f.missing) {
			node := "fixture-node"
			if f.remoteTask {
				node = "uninspected-worker"
			}
			spec := f.spec()
			if f.taskMounts != nil {
				spec["Mounts"] = f.taskMounts
			}
			if f.oldAnonymousTask {
				spec["Mounts"] = []any{map[string]string{"Type": "volume", "Target": "/old-data"}}
			}
			encode([]any{map[string]any{"ID": "task-one", "DesiredState": "running", "NodeID": node, "ServiceID": "service-one", "Spec": map[string]any{"ContainerSpec": spec}, "Status": map[string]any{"State": "running", "ContainerStatus": map[string]string{"ContainerID": "remote-one"}}}})
		} else {
			encode([]any{})
		}
	case path == "/services/service-one" && r.Method == "DELETE":
		f.missing = true
		w.WriteHeader(200)
		encode(map[string]any{})
	default:
		w.WriteHeader(404)
		encode(map[string]string{"message": "unexpected endpoint: " + path})
	}
}

func TestSwarmObservationsIncludeOldTaskAndActualContainerMounts(t *testing.T) {
	d, rt, app, services, f := runtimeFixture(t, true)
	f.mounts = nil
	f.taskMounts = []map[string]string{{"Type": "volume", "Source": "old-data", "Target": "/data"}}
	f.actualContainer = true
	f.containerMounts = []map[string]string{{"Type": "bind", "Source": "/retained", "Destination": "/files"}}
	observed, err := d.Observe(context.Background(), rt, app, services)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(observed.Preserved, ",") != "bind:/retained,volume:old-data" {
		t.Fatal("lost previous task or actual container data", observed.Preserved)
	}
	previous := observed
	previous.Preserved = nil
	if ReviewedSubset(observed, previous) {
		t.Fatal("new data dependencies did not invalidate the reviewed scope")
	}
	if err := d.Remove(context.Background(), rt, app, services, previous, true); !errors.Is(err, ErrChanged) || len(f.requests) != 0 {
		t.Fatal("removed workload after its data scope changed", err, f.requests)
	}
}
func runtimeFixture(t *testing.T, swarm bool) (Docker, model.Runtime, model.Application, []model.Service, *engineFixture) {
	t.Helper()
	f := &engineFixture{active: true, swarm: swarm, version: 7, image: "fixture@sha256:" + strings.Repeat("a", 64), mounts: []map[string]string{{"Type": "volume", "Source": "fixture-data", "Target": "/data"}}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	cfg := config.Defaults()
	cfg.Management.Enabled = true
	rt := model.Runtime{ID: "runtime", Type: model.RuntimeDocker, Endpoint: "tcp://" + strings.TrimPrefix(server.URL, "http://"), Enabled: true, ManagementEnabled: true}
	key := "fixture/web"
	if swarm {
		rt.Type = model.RuntimeSwarm
		key = "fixture_web"
	}
	app := model.Application{ID: "app", RuntimeID: rt.ID, ProjectName: "fixture", Managed: true}
	services := []model.Service{{ID: "service", RuntimeID: rt.ID, ApplicationID: app.ID, ServiceKey: key, Managed: true, RuntimeObjectID: "service-one"}}
	return Docker{Config: cfg}, rt, app, services, f
}
func TestComposeRemovalRequiresObservedStoppedAndRetainsData(t *testing.T) {
	d, rt, app, services, f := runtimeFixture(t, false)
	ctx := context.Background()
	before, err := d.Observe(ctx, rt, app, services)
	if err != nil {
		t.Fatal(err)
	}
	if Stopped(before) || len(before.Preserved) != 2 {
		t.Fatal(before)
	}
	if err = d.Remove(ctx, rt, app, services, before, false); !errors.Is(err, ErrRunning) || len(f.requests) != 0 {
		t.Fatal(err, f.requests)
	}
	f.active = false
	if err = d.Remove(ctx, rt, app, services, before, false); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 || !strings.HasPrefix(f.requests[0], "DELETE /containers/") {
		t.Fatal("ordinary removal stopped workload", f.requests)
	}
	after, err := d.Observe(ctx, rt, app, services)
	if err != nil || len(after.Objects) != 0 {
		t.Fatal(after, err)
	}
}
func TestComposeForceStopsBeforeRemovingAndRefusesChangedOwnership(t *testing.T) {
	d, rt, app, services, f := runtimeFixture(t, false)
	ctx := context.Background()
	before, err := d.Observe(ctx, rt, app, services)
	if err != nil {
		t.Fatal(err)
	}
	f.foreign = true
	if err = d.Remove(ctx, rt, app, services, before, true); !errors.Is(err, ErrOwnership) || len(f.requests) != 0 {
		t.Fatal(err, f.requests)
	}
	f.foreign = false
	wrong := before
	wrong.Objects = []Object{{ID: "old-container", Kind: "container", Service: "web"}}
	if err = d.Remove(ctx, rt, app, services, wrong, true); !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
	if err = d.Remove(ctx, rt, app, services, before, true); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 2 || !strings.HasPrefix(f.requests[0], "POST /containers/container-one/stop?t=30") || !strings.HasPrefix(f.requests[1], "DELETE /containers/container-one?") {
		t.Fatal(f.requests)
	}
}
func TestRuntimeErrorsRemainUnknownAndCannotAuthorizeRemoval(t *testing.T) {
	d, rt, app, services, f := runtimeFixture(t, false)
	ctx := context.Background()
	before, err := d.Observe(ctx, rt, app, services)
	if err != nil {
		t.Fatal(err)
	}
	f.failRead = true
	if _, err = d.Observe(ctx, rt, app, services); err == nil {
		t.Fatal("offline accepted as stopped")
	}
	if err = d.Remove(ctx, rt, app, services, before, true); err == nil || len(f.requests) != 0 {
		t.Fatal(err, f.requests)
	}
	f.failRead = false
	f.failRemove = true
	if err = d.Remove(ctx, rt, app, services, before, true); err == nil || !strings.Contains(err.Error(), "outcome unknown") {
		t.Fatal(err)
	}
	if len(f.requests) != 1 {
		t.Fatal("retried destructive request", f.requests)
	}
	for _, candidate := range []model.Application{{ID: "app", RuntimeID: rt.ID, ProjectName: "fixture"}, {ID: "app", RuntimeID: "foreign", ProjectName: "fixture", Managed: true}, {ID: "app", RuntimeID: rt.ID, Managed: true}} {
		if _, err = d.Observe(ctx, rt, candidate, services); !errors.Is(err, ErrOwnership) {
			t.Fatal(err)
		}
	}
	rt.CredentialRef = "missing"
	if _, err = d.Observe(ctx, rt, app, services); !errors.Is(err, ErrOwnership) {
		t.Fatal(err)
	}
}
func TestSwarmRemovalWaitsForTaskExitAndChecksReviewedVersion(t *testing.T) {
	d, rt, app, services, f := runtimeFixture(t, true)
	ctx := context.Background()
	before, err := d.Observe(ctx, rt, app, services)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Objects) != 2 || Stopped(before) {
		t.Fatal(before)
	}
	if err = d.Remove(ctx, rt, app, services, before, false); !errors.Is(err, ErrRunning) {
		t.Fatal(err)
	}
	f.version++
	if err = d.Remove(ctx, rt, app, services, before, true); !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
	f.version--
	f.lingering = true
	bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if err = d.Remove(bounded, rt, app, services, before, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("service absence concealed a running task", err)
	}
	if !f.missing || len(f.requests) != 1 {
		t.Fatal(f.requests)
	}
	f.lingering = false
	// Resume observes absence and never repeats ServiceRemove.
	if err = d.Remove(ctx, rt, app, services, before, true); err != nil || len(f.requests) != 1 {
		t.Fatal(err, f.requests)
	}
}

func TestSwarmRemovalRefusesUnprovenVolumeRetention(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*engineFixture)
	}{
		{"anonymous mount", func(f *engineFixture) { f.mounts[0]["Source"] = "" }},
		{"implicit image volume", func(f *engineFixture) { f.imageVolumes = []string{"/unmounted-data"} }},
		{"parent mount does not override image volume", func(f *engineFixture) { f.imageVolumes = []string{"/data/nested"} }},
		{"old task has anonymous data", func(f *engineFixture) { f.oldAnonymousTask = true }},
		{"image absent on manager", func(f *engineFixture) { f.missingImage = true }},
		{"image config unavailable", func(f *engineFixture) { f.nilImageConfig = true }},
		{"mutable image tag", func(f *engineFixture) { f.image = "fixture:latest" }},
		{"remote worker volume evidence unavailable", func(f *engineFixture) { f.remoteTask = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, rt, app, services, f := runtimeFixture(t, true)
			ctx := context.Background()
			reviewed, err := d.Observe(ctx, rt, app, services)
			if err != nil {
				t.Fatal(err)
			}
			test.change(f)
			if _, err = d.Observe(ctx, rt, app, services); !errors.Is(err, ErrDataRetention) {
				t.Fatal("preview must explain data retention blocker", err)
			}
			for _, force := range []bool{false, true} {
				if err = d.Remove(ctx, rt, app, services, reviewed, force); !errors.Is(err, ErrDataRetention) {
					t.Fatal("execution must recheck volume retention", force, err)
				}
			}
			if len(f.requests) != 0 || f.missing || !f.active {
				t.Fatal("unsafe volume check changed the workload", f.requests)
			}
		})
	}
}

func TestSwarmTerminalTasksDoNotProveContainerExitOrRemoval(t *testing.T) {
	d, rt, app, services, f := runtimeFixture(t, true)
	ctx := context.Background()
	f.active = false // zero desired replicas and no active task records
	f.actualContainer, f.containerRunning, f.containerLingers = true, true, true
	reviewed, err := d.Observe(ctx, rt, app, services)
	if err != nil || Stopped(reviewed) {
		t.Fatal("manager state concealed an actual running container", reviewed, err)
	}
	if err = d.Remove(ctx, rt, app, services, reviewed, false); !errors.Is(err, ErrRunning) || len(f.requests) != 0 {
		t.Fatal("ordinary deletion stopped an actual running workload", err, f.requests)
	}
	// Stopped containers can still await Swarm garbage collection.
	f.containerRunning = false
	bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	completed := map[string]bool{}
	err = d.RemoveWithProgress(bounded, rt, app, services, reviewed, true, func(o Object) error { completed[o.ID] = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || completed["service-one"] || completed["container-one"] {
		t.Fatal("declared removal while container still existed", err, completed)
	}
	if len(f.requests) != 1 || !strings.HasPrefix(f.requests[0], "DELETE /services/") {
		t.Fatal("must let Swarm own task-container removal", f.requests)
	}
	f.containerLingers = false
	if err = d.Remove(ctx, rt, app, services, reviewed, true); err != nil || len(f.requests) != 1 {
		t.Fatal("resume should only verify actual disappearance", err, f.requests)
	}
}

func TestSwarmImageVolumesRequireExplicitRetainedMounts(t *testing.T) {
	for _, kind := range []string{"volume", "bind"} {
		t.Run(kind, func(t *testing.T) {
			d, rt, app, services, f := runtimeFixture(t, true)
			f.mounts[0]["Type"] = kind
			f.mounts[0]["Source"] = "/fixture/data"
			if kind == "volume" {
				f.mounts[0]["Source"] = "fixture-data"
			}
			f.imageVolumes = []string{"/data/"}
			ctx := context.Background()
			reviewed, err := d.Observe(ctx, rt, app, services)
			if err != nil || len(reviewed.Preserved) != 1 {
				t.Fatal(reviewed, err)
			}
			if err = d.Remove(ctx, rt, app, services, reviewed, true); err != nil {
				t.Fatal(err)
			}
			if !f.missing || len(f.requests) != 1 || !strings.HasPrefix(f.requests[0], "DELETE /services/") {
				t.Fatal("expected only service removal", f.requests)
			}
		})
	}
}
func TestSwarmZeroReplicasIsInsufficientWithLiveTask(t *testing.T) {
	d, rt, app, services, f := runtimeFixture(t, true)
	ctx := context.Background()
	f.active = false
	f.lingering = true
	before, err := d.Observe(ctx, rt, app, services)
	if err != nil || Stopped(before) {
		t.Fatal(before, err)
	}
	if err = d.Remove(ctx, rt, app, services, before, false); !errors.Is(err, ErrRunning) {
		t.Fatal(err)
	}
	f.lingering = false
	if err = d.Remove(ctx, rt, app, services, before, false); err != nil {
		t.Fatal(err)
	}
}

func (f *engineFixture) taskTemplate() map[string]any {
	value := map[string]any{"ContainerSpec": f.spec()}
	if f.dataNode != "" {
		value["Placement"] = map[string]any{"Constraints": []string{"node.id == " + f.dataNode}}
	}
	return value
}
