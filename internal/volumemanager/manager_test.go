package volumemanager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/volume"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type fixtureStore struct {
	storage.Store
	rt            model.Runtime
	ops           []model.DeploymentOperation
	apps          []model.Application
	docs          map[string]storage.ControlDocument
	revisions     map[string]model.ApplicationRevision
	outage        error
	runtimes      []model.Runtime
	appsByRuntime map[string][]model.Application
}

func (s *fixtureStore) GetRuntime(context.Context, string) (model.Runtime, error) { return s.rt, nil }
func (s *fixtureStore) ListRuntimes(context.Context, storage.Page) ([]model.Runtime, error) {
	if s.runtimes != nil {
		return s.runtimes, s.outage
	}
	return []model.Runtime{s.rt}, s.outage
}
func (s *fixtureStore) GetDeploymentOperation(_ context.Context, id string) (model.DeploymentOperation, error) {
	for _, op := range s.ops {
		if op.ID == id {
			return op, s.outage
		}
	}
	return model.DeploymentOperation{}, storage.ErrNotFound
}
func (s *fixtureStore) ListDeploymentOperations(context.Context, string, storage.Page) ([]model.DeploymentOperation, error) {
	return s.ops, s.outage
}
func (s *fixtureStore) ListApplications(_ context.Context, runtime string, _ storage.Page) ([]model.Application, error) {
	if s.appsByRuntime != nil {
		return s.appsByRuntime[runtime], s.outage
	}
	return s.apps, s.outage
}
func (s *fixtureStore) GetControlDocument(_ context.Context, id string) (storage.ControlDocument, error) {
	if d, ok := s.docs[id]; ok {
		return d, s.outage
	}
	return storage.ControlDocument{}, storage.ErrNotFound
}
func (s *fixtureStore) GetApplicationRevision(_ context.Context, id string) (model.ApplicationRevision, error) {
	if d, ok := s.revisions[id]; ok {
		return d, s.outage
	}
	return model.ApplicationRevision{}, storage.ErrNotFound
}

type fixture struct {
	m                                                       Manager
	store                                                   *fixtureStore
	volumes                                                 []volume.Volume
	containers                                              []map[string]any
	inspections                                             map[string]any
	services                                                []map[string]any
	creates, deletes, inspects                              int
	loseCreate, loseDelete, inspectError, replace, conflict bool
	allContainers                                           bool
	worker                                                  bool
	beforeRead                                              func(string)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	keys, err := security.NewKeyring("fixture", map[string]string{"fixture": base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{store: &fixtureStore{rt: model.Runtime{ID: "rt", Type: model.RuntimeDocker, Enabled: true, ManagementEnabled: true}, docs: map[string]storage.ControlDocument{}, revisions: map[string]model.ApplicationRevision{}}, inspections: map[string]any{}}
	f.m = Manager{Store: f.store, Keys: keys, Config: config.Config{Management: config.ManagementConfig{Enabled: true}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if f.beforeRead != nil {
			f.beforeRead(path)
		}
		if strings.HasPrefix(path, "/v") {
			path = path[strings.Index(path[1:], "/")+1:]
		}
		respond := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		failure := func(code int) { w.WriteHeader(code); respond(map[string]string{"message": "fixture failure"}) }
		switch {
		case path == "/_ping":
			w.Header().Set("API-Version", "1.52")
			w.Write([]byte("OK"))
		case path == "/info":
			if f.worker {
				respond(map[string]any{"ID": "engine", "Name": "fixture", "Swarm": map[string]any{"NodeID": "node", "ControlAvailable": false}})
			} else {
				respond(map[string]any{"ID": "engine", "Name": "fixture", "Swarm": map[string]any{"NodeID": "node", "ControlAvailable": true, "Cluster": map[string]string{"ID": "cluster"}}})
			}
		case path == "/nodes":
			respond([]map[string]any{{"ID": "node", "Status": map[string]string{"State": "ready"}, "Spec": map[string]string{"Availability": "active"}}})
		case path == "/services":
			respond(f.services)
		case path == "/containers/json":
			f.allContainers = r.URL.Query().Get("all") == "1"
			respond(f.containers)
		case strings.HasPrefix(path, "/containers/"):
			if f.inspectError {
				failure(503)
				return
			}
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
			respond(f.inspections[id])
		case path == "/volumes/create":
			var input struct {
				Name   string
				Driver string
				Labels map[string]string
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			f.creates++
			if input.Name == "" {
				input.Name = "anonymous-id"
			}
			v := testVolume(input.Name)
			v.Labels = input.Labels
			f.volumes = append(f.volumes, v)
			if f.loseCreate {
				f.loseCreate = false
				failure(503)
				return
			}
			respond(v)
		case path == "/volumes":
			respond(map[string]any{"Volumes": f.volumes})
		case strings.HasPrefix(path, "/volumes/"):
			name := strings.TrimPrefix(path, "/volumes/")
			i := slices.IndexFunc(f.volumes, func(v volume.Volume) bool { return v.Name == name })
			if i < 0 {
				failure(404)
				return
			}
			if r.Method == "DELETE" {
				if r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true" {
					t.Error("force removal requested")
				}
				if f.conflict {
					failure(409)
					return
				}
				f.deletes++
				f.volumes = append(f.volumes[:i], f.volumes[i+1:]...)
				if f.loseDelete {
					f.loseDelete = false
					failure(503)
					return
				}
				w.WriteHeader(204)
				return
			}
			f.inspects++
			if f.replace && f.inspects > 1 {
				f.volumes[i].CreatedAt = "2026-10-06T12:00:00Z"
			}
			respond(f.volumes[i])
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, path)
			failure(404)
		}
	}))
	t.Cleanup(server.Close)
	f.store.rt.Endpoint = server.URL
	return f
}
func testVolume(name string) volume.Volume {
	return volume.Volume{Name: name, Driver: "local", Scope: "local", CreatedAt: "2026-10-01T00:00:00Z"}
}
func (f *fixture) freeze(t *testing.T, req Request) Request {
	t.Helper()
	req.RuntimeID = "rt"
	req.NodeID = "node"
	req.EngineID = "engine"
	req.OperationID = "op"
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := f.m.Keys.Encrypt(raw, []byte("deployment-operation:op"))
	if err != nil {
		t.Fatal(err)
	}
	kind := "VOLUME_CREATE"
	if req.Action == "delete" {
		kind = "VOLUME_DELETE"
	}
	f.store.ops = []model.DeploymentOperation{{ID: "op", RuntimeID: "rt", Type: kind, Status: model.OperationRunning, WorkerID: "worker", EncryptedInput: encrypted}}
	return req
}
func TestAnonymousCreateLostAcknowledgementReconcilesSameVolume(t *testing.T) {
	f := newFixture(t)
	f.loseCreate = true
	req := f.freeze(t, Request{Action: "create"})
	if _, err := f.m.Do(t.Context(), req); err == nil {
		t.Fatal("lost response treated as success")
	}
	result, err := f.m.Do(t.Context(), req)
	if err != nil || result.Name != "anonymous-id" || f.creates != 1 {
		t.Fatalf("duplicate anonymous create: %+v %v creates=%d", result, err, f.creates)
	}
	if _, err = f.m.Do(t.Context(), Request{RuntimeID: "rt", Action: "list"}); err != nil {
		t.Fatal(err)
	}
}
func TestCreateDoesNotAdoptForeignNamedVolumeOrChangedFrozenRequest(t *testing.T) {
	f := newFixture(t)
	f.volumes = []volume.Volume{testVolume("data")}
	req := f.freeze(t, Request{Action: "create", Name: "data"})
	if _, err := f.m.Do(t.Context(), req); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("foreign volume admitted: %v", err)
	}
	req.Name = "different"
	if _, err := f.m.Do(t.Context(), req); !errors.Is(err, ErrIdentity) || f.creates != 0 {
		t.Fatalf("changed input admitted: %v", err)
	}
}
func TestExternalVolumeDeleteAndLostAcknowledgement(t *testing.T) {
	f := newFixture(t)
	v := testVolume("external")
	f.volumes = []volume.Volume{v}
	f.loseDelete = true
	req := f.freeze(t, Request{Action: "delete", Name: v.Name, Version: nodecontrol.VolumeMetadata(v).Version, AcknowledgeDataLoss: true})
	if _, err := f.m.Do(t.Context(), req); err == nil {
		t.Fatal("unknown result was silently successful")
	}
	result, err := f.m.Do(t.Context(), req)
	if err != nil || result.Name != v.Name || f.deletes != 1 || !f.allContainers {
		t.Fatalf("reconcile=%+v err=%v deletes=%d all=%t", result, err, f.deletes, f.allContainers)
	}
}
func TestDeletionConsumerAndEvidenceBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(*fixture)
		expected error
	}{
		{"stopped container", func(f *fixture) {
			f.containers = []map[string]any{{"Id": "c"}}
			f.inspections["c"] = map[string]any{"Id": "c", "Name": "/stopped", "State": map[string]string{"Status": "exited"}, "Mounts": []map[string]string{{"Type": "volume", "Name": "data", "Destination": "/data"}}}
		}, ErrBlocked},
		{"uninspectable container", func(f *fixture) { f.containers = []map[string]any{{"Id": "c"}}; f.inspectError = true }, ErrBlocked},
		{"zero replica service", func(f *fixture) {
			f.store.rt.Type = model.RuntimeSwarm
			f.store.rt.ClusterID = "cluster"
			f.services = []map[string]any{{"ID": "s", "Spec": map[string]any{"Name": "idle", "Mode": map[string]any{"Replicated": map[string]int{"Replicas": 0}}, "TaskTemplate": map[string]any{"ContainerSpec": map[string]any{"Mounts": []map[string]string{{"Type": "volume", "Source": "data", "Target": "/data"}}}}}}}
		}, ErrBlocked},
		{"retained app", func(f *fixture) {
			f.store.apps = []model.Application{{ID: "app", Name: "stopped"}}
			f.store.docs["application-retention:app"] = storage.ControlDocument{Kind: "application-retention", ParentID: "app", Payload: []byte(`{"application_id":"app","runtime_id":"rt","expected_node":"node","expected_volumes":["data"]}`)}
		}, ErrBlocked},
		{"pending application operation", func(f *fixture) {
			f.store.ops = append(f.store.ops, model.DeploymentOperation{ID: "pending", RuntimeID: "rt", Type: "APPLICATION_START", Status: model.OperationQueued})
		}, ErrBlocked},
		{"driver options", func(f *fixture) { f.volumes[0].Options = map[string]string{"device": "private"} }, storage.ErrPrecondition},
		{"external replacement", func(f *fixture) { f.replace = true }, storage.ErrPrecondition},
		{"management disabled", func(f *fixture) { f.store.rt.ManagementEnabled = false }, ErrIdentity},
		{"database unavailable", func(f *fixture) { f.store.outage = errors.New("database unavailable") }, nil},
		{"Docker reference race", func(f *fixture) { f.conflict = true }, ErrBlocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			v := testVolume("data")
			f.volumes = []volume.Volume{v}
			req := f.freeze(t, Request{Action: "delete", Name: "data", Version: nodecontrol.VolumeMetadata(v).Version, AcknowledgeDataLoss: true})
			tc.setup(f)
			_, err := f.m.Do(t.Context(), req)
			if err == nil || tc.expected != nil && !errors.Is(err, tc.expected) || f.deletes != 0 {
				t.Fatalf("boundary violated: err=%v removals=%d", err, f.deletes)
			}
		})
	}
}
func TestInventoryProtectsExternalRevisionAndAllowsOtherNode(t *testing.T) {
	f := newFixture(t)
	f.store.rt.Type = model.RuntimeSwarm
	f.store.rt.ClusterID = "cluster"
	v := testVolume("data")
	v.Labels = map[string]string{"com.docker.stack.namespace": "site"}
	f.volumes = []volume.Volume{v}
	f.store.apps = []model.Application{{ID: "app", Name: "site", ProjectName: "site", ActiveRevisionID: "rev"}}
	raw := []byte("services: {}\nvolumes:\n  data: {external: true}\n")
	encrypted, err := f.m.Keys.Encrypt(raw, []byte("application-revision:rev:resolved"))
	if err != nil {
		t.Fatal(err)
	}
	rev := model.ApplicationRevision{ID: "rev", ApplicationID: "app", EncryptedResolved: encrypted}
	f.store.revisions["rev"] = rev
	result, err := f.m.Do(t.Context(), Request{RuntimeID: "rt", NodeID: "node", Action: "list"})
	if err != nil || len(result.Items[0].UsedBy) == 0 {
		t.Fatalf("restorable external volume unprotected: %v %+v", err, result)
	}
	rev.ConfigurationSource.DeploymentPlan.DataNodeID = "other-node"
	f.store.revisions["rev"] = rev
	result, err = f.m.Do(t.Context(), Request{RuntimeID: "rt", NodeID: "node", Action: "list"})
	if err != nil || len(result.Items[0].UsedBy) != 0 {
		t.Fatalf("same-name volume on another node protected incorrectly: %v %+v", err, result)
	}
}
func TestVolumeTypeEvidenceAndPrivacy(t *testing.T) {
	f := newFixture(t)
	v := testVolume(strings.Repeat("a", 64))
	v.Labels = map[string]string{"password": "never-return-this"}
	f.volumes = []volume.Volume{v}
	result, err := f.m.Do(t.Context(), Request{RuntimeID: "rt", Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if result.Items[0].Kind != "unknown" || strings.Contains(string(raw), "never-return-this") {
		t.Fatalf("guess or disclosure: %s", raw)
	}
	v.Labels = map[string]string{"com.docker.volume.anonymous": ""}
	if nodecontrol.VolumeMetadata(v).Kind != "anonymous" {
		t.Fatal("Docker anonymous marker ignored")
	}
}

func TestVolumeOwnershipRequiresRecordedNodeAndFingerprint(t *testing.T) {
	f := newFixture(t)
	v := testVolume("data")
	v.Labels = map[string]string{labelRuntime: "rt", labelEngine: "engine", labelOperation: "creation", labelKind: "named"}
	f.volumes = []volume.Volume{v}
	read := func() Item {
		t.Helper()
		result, err := f.m.Do(t.Context(), Request{RuntimeID: "rt", Action: "list"})
		if err != nil {
			t.Fatal(err)
		}
		return result.Items[0]
	}
	if read().Owned {
		t.Fatal("labels alone established ownership")
	}
	raw, _ := json.Marshal(map[string]string{"runtime_id": "rt", "node_id": "node", "engine_id": "engine", "name": "data", "version": nodecontrol.VolumeMetadata(v).Version})
	f.store.docs["volume-operation:creation"] = storage.ControlDocument{Kind: "volume-operation", ParentID: "creation", Payload: raw}
	if !read().Owned {
		t.Fatal("recorded original volume was not recognized")
	}
	f.volumes[0].CreatedAt = "2026-10-06T01:00:00Z"
	if read().Owned {
		t.Fatal("external recreation inherited ownership")
	}
}

func TestVolumeFrozenTargetAndManagementBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*fixture, *Request)
	}{
		{"wrong engine", func(_ *fixture, r *Request) { r.EngineID = "replacement" }},
		{"wrong node", func(_ *fixture, r *Request) { r.NodeID = "other" }},
		{"missing node", func(_ *fixture, r *Request) { r.NodeID = "" }},
		{"missing engine", func(_ *fixture, r *Request) { r.EngineID = "" }},
		{"runtime disabled", func(f *fixture, _ *Request) { f.store.rt.Enabled = false }},
		{"global management disabled", func(f *fixture, _ *Request) { f.m.Config.Management.Enabled = false }},
		{"unsupported runtime", func(f *fixture, _ *Request) { f.store.rt.Type = "agent" }},
		{"operation finished", func(f *fixture, _ *Request) { f.store.ops[0].Status = model.OperationSucceeded }},
		{"operation on another runtime", func(f *fixture, _ *Request) { f.store.ops[0].RuntimeID = "other" }},
		{"operation not claimed", func(f *fixture, _ *Request) { f.store.ops[0].WorkerID = "" }},
		{"wrong operation kind", func(f *fixture, _ *Request) { f.store.ops[0].Type = "VOLUME_DELETE" }},
		{"corrupt frozen input", func(f *fixture, _ *Request) { f.store.ops[0].EncryptedInput = []byte(`{}`) }},
		{"keys unavailable", func(f *fixture, _ *Request) { f.m.Keys = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			req := f.freeze(t, Request{Action: "create", Name: "data"})
			tc.change(f, &req)
			if _, err := f.m.Do(t.Context(), req); err == nil || f.creates != 0 || f.deletes != 0 {
				t.Fatalf("unauthorized mutation %v create=%d delete=%d", err, f.creates, f.deletes)
			}
		})
	}
}
func TestLiveManagerConsumerInventoryAndNonManagerRejection(t *testing.T) {
	f := newFixture(t)
	f.store.rt.Type = model.RuntimeSwarm
	f.store.rt.ClusterID = "cluster"
	f.services = []map[string]any{{"ID": "just-created", "Spec": map[string]string{"Name": "new"}}}
	result, err := f.m.Do(t.Context(), Request{RuntimeID: "rt", NodeID: "node", EngineID: "engine", Action: "consumers"})
	if err != nil || !result.Complete || len(result.Services) != 1 || len(result.Nodes) != 1 || result.Services[0].ID != "just-created" {
		t.Fatalf("live consumer proof=%+v %v", result, err)
	}
	f.store.rt.Type = model.RuntimeDocker
	if _, err = f.m.Do(t.Context(), Request{RuntimeID: "rt", Action: "consumers"}); !errors.Is(err, ErrIdentity) {
		t.Fatalf("non-Swarm consumer query admitted: %v", err)
	}
}
func TestServicePlacementAndMountEvidence(t *testing.T) {
	for _, tc := range []struct {
		constraints []string
		allowed     bool
	}{
		{nil, true}, {[]string{"node.id == node"}, true}, {[]string{"node.id == other"}, false}, {[]string{"node.id != node"}, false}, {[]string{"node.id != other"}, true}, {[]string{"node.role == worker"}, true},
	} {
		s := swarm.Service{Spec: swarm.ServiceSpec{TaskTemplate: swarm.TaskSpec{Placement: &swarm.Placement{Constraints: tc.constraints}}}}
		if serviceCanUseNode(s, "node") != tc.allowed {
			t.Fatalf("node placement mismatch: %v", tc.constraints)
		}
	}
	for _, tc := range []struct {
		bind string
		kind string
	}{{"/data", "anonymous"}, {"data:/data", "named"}, {"another:/data", "unknown"}, {"data:/other", "unknown"}} {
		c := container.InspectResponse{HostConfig: &container.HostConfig{Binds: []string{tc.bind}}}
		if got := mountKind(c, "data", "/data"); got != tc.kind {
			t.Fatalf("mount %s classified %s", tc.bind, got)
		}
	}
	for _, tc := range []struct {
		source string
		kind   string
	}{{"", "anonymous"}, {"data", "named"}, {"other", "unknown"}} {
		c := container.InspectResponse{HostConfig: &container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: tc.source, Target: "/data"}}}}
		if got := mountKind(c, "data", "/data"); got != tc.kind {
			t.Fatalf("mount source %s classified %s", tc.source, got)
		}
	}
}

func TestUnsupportedVolumeIdentityIsReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		volume volume.Volume
		reason string
	}{
		{"plugin", volume.Volume{Name: "data", Driver: "plugin", Scope: "global", CreatedAt: "2026-10-01T00:00:00Z"}, "unsupported_driver"},
		{"driver options", volume.Volume{Name: "data", Driver: "local", Scope: "local", CreatedAt: "2026-10-01T00:00:00Z", Options: map[string]string{"device": "private-storage"}}, "driver_options"},
		{"missing creation identity", volume.Volume{Name: "data", Driver: "local", Scope: "local"}, "identity_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.volumes = []volume.Volume{tc.volume}
			result, err := f.m.Do(t.Context(), Request{RuntimeID: "rt", Action: "list"})
			if err != nil || !slices.Contains(result.Items[0].BlockedReasons, tc.reason) {
				t.Fatalf("read-only volume not explained: %+v %v", result, err)
			}
			req := f.freeze(t, Request{Action: "delete", Name: "data", Version: result.Items[0].Version, AcknowledgeDataLoss: true})
			if _, err = f.m.Do(t.Context(), req); !errors.Is(err, ErrBlocked) || f.deletes != 0 {
				t.Fatalf("read-only volume removed: %v", err)
			}
		})
	}
}

func TestDockerAliasesShareRecoveryProtectionAndMaintenanceLock(t *testing.T) {
	f := newFixture(t)
	alias := f.store.rt
	alias.ID = "alias"
	f.store.runtimes = []model.Runtime{f.store.rt, alias}
	f.store.appsByRuntime = map[string][]model.Application{"alias": {{ID: "retained-app", Name: "stopped", ProjectName: "site"}}}
	f.store.docs["application-retention:retained-app"] = storage.ControlDocument{Kind: "application-retention", ParentID: "retained-app", Payload: []byte(`{"application_id":"retained-app","runtime_id":"alias","expected_node":"node","expected_volumes":["data"]}`)}
	f.volumes = []volume.Volume{testVolume("data")}
	result, err := f.m.Do(t.Context(), Request{RuntimeID: "rt", Action: "list"})
	if err != nil || len(result.Items[0].UsedBy) != 1 || result.Items[0].UsedBy[0].ID != "retained-app" {
		t.Fatalf("alias recovery data unprotected: %+v %v", result, err)
	}
	if LockKey(f.store.rt) != LockKey(alias) {
		t.Fatal("aliases did not share a maintenance lock")
	}
	alias.Endpoint = "unix:///different-engine.sock"
	if LockKey(f.store.rt) == LockKey(alias) {
		t.Fatal("unrelated engines share a lock")
	}
}
func TestDockerRuntimeCannotBypassSwarmConsumerEvidence(t *testing.T) {
	f := newFixture(t)
	f.volumes = []volume.Volume{testVolume("data")}
	f.services = []map[string]any{{"ID": "zero", "Spec": map[string]any{"Name": "zero", "TaskTemplate": map[string]any{"ContainerSpec": map[string]any{"Mounts": []map[string]string{{"Type": "volume", "Source": "data", "Target": "/data"}}}}}}}
	result, err := f.m.Do(t.Context(), Request{RuntimeID: "rt", Action: "list"})
	if err != nil || len(result.Items[0].UsedBy) != 1 || result.Items[0].UsedBy[0].Kind != "service" {
		t.Fatalf("Docker alias ignored real Swarm Service: %+v %v", result, err)
	}
	f.worker = true
	result, err = f.m.Do(t.Context(), Request{RuntimeID: "rt", Action: "list"})
	if err != nil || result.Complete || !slices.Contains(result.Items[0].BlockedReasons, "inventory_unknown") {
		t.Fatalf("unverifiable Swarm worker was writable: %+v %v", result, err)
	}
	if _, err = f.m.Do(t.Context(), Request{RuntimeID: "rt", Action: "target"}); !errors.Is(err, ErrIdentity) {
		t.Fatal("unknown worker target was admitted")
	}
}
