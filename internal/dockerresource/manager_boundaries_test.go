package dockerresource

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// resourceEngine uses the real SDK serialization and HTTP protocol. Its state
// changes only on requests which would mutate a disposable Engine resource.
type resourceEngine struct {
	mu              sync.Mutex
	info            system.Info
	nodes           []swarm.Node
	configs         []swarm.Config
	secrets         []swarm.Secret
	services        []swarm.Service
	mutations       atomic.Int64
	requests        atomic.Int64
	failPath        string
	loseCreateReply bool
	inventoryReads  map[string]int
	onInventoryRead func(string, int)
}

func (e *resourceEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	if strings.HasPrefix(path, "/v") {
		path = path[strings.Index(path[1:], "/")+1:]
	}
	if path == "/_ping" {
		w.Header().Set("API-Version", client.MaxAPIVersion)
		w.WriteHeader(http.StatusOK)
		return
	}
	if path == e.failPath {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "Engine unavailable"})
		return
	}
	encode := func(value any) { _ = json.NewEncoder(w).Encode(value) }
	switch {
	case r.Method == http.MethodGet && path == "/info":
		encode(e.info)
	case r.Method == http.MethodGet && path == "/nodes":
		encode(e.nodes)
	case r.Method == http.MethodGet && path == "/services":
		encode(e.services)
	case r.Method == http.MethodGet && path == "/configs":
		e.inventoryReads["config"]++
		if e.onInventoryRead != nil {
			e.onInventoryRead("config", e.inventoryReads["config"])
		}
		encode(e.configs)
	case r.Method == http.MethodGet && path == "/secrets":
		e.inventoryReads["secret"]++
		if e.onInventoryRead != nil {
			e.onInventoryRead("secret", e.inventoryReads["secret"])
		}
		encode(e.secrets)
	case r.Method == http.MethodPost && path == "/configs/create":
		var spec swarm.ConfigSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		id := fmt.Sprintf("config-%d", e.mutations.Add(1))
		e.configs = append(e.configs, swarm.Config{ID: id, Spec: spec, Meta: swarm.Meta{CreatedAt: time.Now().UTC()}})
		if e.loseCreateReply {
			e.loseCreateReply = false
			w.WriteHeader(http.StatusServiceUnavailable)
			encode(map[string]string{"message": "create acknowledgement lost"})
			return
		}
		w.WriteHeader(http.StatusCreated)
		encode(map[string]string{"ID": id})
	case r.Method == http.MethodPost && path == "/secrets/create":
		var spec swarm.SecretSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		id := fmt.Sprintf("secret-%d", e.mutations.Add(1))
		e.secrets = append(e.secrets, swarm.Secret{ID: id, Spec: spec, Meta: swarm.Meta{CreatedAt: time.Now().UTC()}})
		if e.loseCreateReply {
			e.loseCreateReply = false
			w.WriteHeader(http.StatusServiceUnavailable)
			encode(map[string]string{"message": "create acknowledgement lost"})
			return
		}
		w.WriteHeader(http.StatusCreated)
		encode(map[string]string{"ID": id})
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/configs/"):
		id := strings.TrimPrefix(path, "/configs/")
		for index, value := range e.configs {
			if value.ID == id {
				e.configs = append(e.configs[:index], e.configs[index+1:]...)
				e.mutations.Add(1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/secrets/"):
		id := strings.TrimPrefix(path, "/secrets/")
		for index, value := range e.secrets {
			if value.ID == id {
				e.secrets = append(e.secrets[:index], e.secrets[index+1:]...)
				e.mutations.Add(1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type resourceFixture struct {
	manager Manager
	store   *storage.SQLStore
	runtime model.Runtime
	engine  *resourceEngine
}

func newResourceFixture(t *testing.T) resourceFixture {
	t.Helper()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "resources.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	e := &resourceEngine{info: system.Info{ID: "engine-fixture", Swarm: swarm.Info{NodeID: "node-fixture", ControlAvailable: true, Cluster: &swarm.ClusterInfo{ID: "cluster-fixture"}}}, inventoryReads: map[string]int{}}
	server := httptest.NewServer(e)
	t.Cleanup(server.Close)
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: "pool-fixture", Name: "resource-fixture", BackendNetwork: "fixture", Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := store.CreatePool(t.Context(), &pool); err != nil {
		t.Fatal(err)
	}
	runtime := model.Runtime{ID: "runtime-fixture", PoolID: pool.ID, Name: "resource-fixture", Type: model.RuntimeDocker, Endpoint: server.URL, ClusterID: "cluster-fixture", Enabled: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := store.CreateRuntime(t.Context(), &runtime); err != nil {
		t.Fatal(err)
	}
	// SQLite is only the isolated ownership/operation fixture. Directly seed a
	// Swarm row to exercise the manager without claiming SQLite deployment support.
	runtime.Type = model.RuntimeSwarm
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE runtimes SET runtime_type=? WHERE id=?`, runtime.Type, runtime.ID); err != nil {
		t.Fatal(err)
	}
	return resourceFixture{manager: Manager{Store: store, Config: config.Defaults()}, store: store, runtime: runtime, engine: e}
}

func (f resourceFixture) operation(t *testing.T, id, kind string) model.DeploymentOperation {
	t.Helper()
	now := time.Now().UTC()
	value := model.DeploymentOperation{ID: id, RuntimeID: f.runtime.ID, Type: kind, Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if _, err := f.store.CreateDeploymentOperation(t.Context(), &value); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET status=?,worker_id=? WHERE id=?`, model.OperationRunning, "fixture-worker", id); err != nil {
		t.Fatal(err)
	}
	value.Status, value.WorkerID = model.OperationRunning, "fixture-worker"
	return value
}

func (f resourceFixture) record(t *testing.T, kind, id, name, operation string) {
	t.Helper()
	if err := f.store.RecordDockerResource(t.Context(), storage.DockerResource{ClusterID: f.runtime.ClusterID, Kind: kind, DockerID: id, Name: name, CreateOperationID: operation, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func TestManagedResourceCreateRetryAndDeletePreserveOwnership(t *testing.T) {
	for _, kind := range []string{"config", "secret"} {
		t.Run(kind, func(t *testing.T) {
			f := newResourceFixture(t)
			create := f.operation(t, "create-one", "DOCKER_RESOURCE_CREATE")
			request := Request{RuntimeID: f.runtime.ID, Action: "create", Kind: kind, OperationID: create.ID, Name: "managed-one", Content: []byte("fixture-private-content")}
			f.engine.loseCreateReply = true
			if _, err := f.manager.Do(t.Context(), request); err == nil {
				t.Fatal("lost create acknowledgement became success")
			}
			result, err := f.manager.Do(t.Context(), request)
			if err != nil || result.ID == "" || f.engine.mutations.Load() != 1 {
				t.Fatalf("retry duplicated a Docker object: %#v %v mutations=%d", result, err, f.engine.mutations.Load())
			}
			listed, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list", Kind: kind})
			if err != nil || len(listed.Items) != 1 || listed.Items[0].Owned {
				t.Fatalf("labels without durable evidence claimed ownership: %#v %v", listed, err)
			}
			f.record(t, kind, result.ID, request.Name, create.ID)
			again, err := f.manager.Do(t.Context(), request)
			if err != nil || again.ID != result.ID || f.engine.mutations.Load() != 1 {
				t.Fatal("recorded retry did not preserve the original object", again, err)
			}
			listed, err = f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list"})
			if err != nil || len(listed.Items) != 1 || !listed.Items[0].Owned || listed.Items[0].CreatedAt.IsZero() {
				t.Fatalf("owned resource metadata lost: %#v %v", listed, err)
			}
			body, err := json.Marshal(listed)
			if err != nil || strings.Contains(string(body), string(request.Content)) || strings.Contains(string(body), "Data") {
				t.Fatal("ordinary resource result exposed confidential content")
			}
			remove := f.operation(t, "delete-one", "DOCKER_RESOURCE_DELETE")
			deletion := Request{RuntimeID: f.runtime.ID, Action: "delete", Kind: kind, ID: result.ID, OperationID: remove.ID}
			if _, err := f.manager.Do(t.Context(), deletion); !errors.Is(err, storage.ErrConflict) || f.engine.mutations.Load() != 1 {
				t.Fatal("deletion bypassed restoration-risk acknowledgement", err)
			}
			deletion.AcknowledgeRestoreRisk = true
			if deleted, err := f.manager.Do(t.Context(), deletion); err != nil || deleted.ID != result.ID || f.engine.mutations.Load() != 2 {
				t.Fatal("acknowledged deletion failed", deleted, err)
			}
			if _, err := f.manager.Do(t.Context(), deletion); err != nil || f.engine.mutations.Load() != 2 {
				t.Fatal("lost delete acknowledgement was not idempotent", err)
			}
			if err := f.store.DeleteDockerResource(t.Context(), f.runtime.ClusterID, kind, result.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.Do(t.Context(), deletion); err != nil || f.engine.mutations.Load() != 2 {
				t.Fatal("completed cleanup was not retryable", err)
			}
		})
	}
}

func TestResourceDeletionWaitsForCurrentAndPendingConsumers(t *testing.T) {
	for _, kind := range []string{"config", "secret"} {
		t.Run(kind, func(t *testing.T) {
			f := newResourceFixture(t)
			create := f.operation(t, "create-consumed", "DOCKER_RESOURCE_CREATE")
			created, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "create", Kind: kind, OperationID: create.ID, Name: "in-use", Content: []byte("fixture-data")})
			if err != nil {
				t.Fatal(err)
			}
			f.record(t, kind, created.ID, "in-use", create.ID)
			container := &swarm.ContainerSpec{}
			if kind == "config" {
				container.Configs = []*swarm.ConfigReference{{ConfigID: "unrelated"}, {ConfigID: created.ID}, {ConfigID: created.ID}}
			} else {
				container.Secrets = []*swarm.SecretReference{{SecretID: "unrelated"}, {SecretID: created.ID}, {SecretID: created.ID}}
			}
			f.engine.mu.Lock()
			f.engine.services = []swarm.Service{{Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "consumer"}, TaskTemplate: swarm.TaskSpec{ContainerSpec: container}}}, {Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "empty-service"}}}}
			f.engine.mu.Unlock()
			listed, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list", Kind: kind})
			if err != nil || len(listed.Items) != 1 || len(listed.Items[0].UsedBy) != 1 || listed.Items[0].UsedBy[0] != "consumer" {
				t.Fatal("current consumer evidence was not deduplicated", listed, err)
			}
			remove := f.operation(t, "delete-consumed", "DOCKER_RESOURCE_DELETE")
			request := Request{RuntimeID: f.runtime.ID, Action: "delete", Kind: kind, ID: created.ID, OperationID: remove.ID, AcknowledgeRestoreRisk: true}
			if _, err := f.manager.Do(t.Context(), request); !errors.Is(err, ErrInUse) || f.engine.mutations.Load() != 1 {
				t.Fatal("current consumer did not prevent deletion", err)
			}
			f.engine.mu.Lock()
			f.engine.services = nil
			f.engine.mu.Unlock()
			pending := model.DeploymentOperation{ID: "consumer-pending", RuntimeID: f.runtime.ID, Type: "UPDATE", Status: model.OperationQueued, Metadata: model.Metadata{Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}
			if _, err := f.store.CreateDeploymentOperation(t.Context(), &pending); err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.Do(t.Context(), request); !errors.Is(err, ErrInUse) || f.engine.mutations.Load() != 1 {
				t.Fatal("pending submission did not prevent deletion", err)
			}
			if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET status=? WHERE id=?`, model.OperationSucceeded, pending.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.Do(t.Context(), request); err != nil || f.engine.mutations.Load() != 2 {
				t.Fatal("finished consumer still prevented deletion", err)
			}
		})
	}
}

func TestExternalAndAlteredResourceEvidenceNeverAuthorizesMutation(t *testing.T) {
	for _, kind := range []string{"config", "secret"} {
		for _, altered := range []string{"external", "cluster", "operation", "name", "missing-record"} {
			t.Run(kind+"/"+altered, func(t *testing.T) {
				f := newResourceFixture(t)
				labels := map[string]string{labelOwner: "nsc", labelCluster: f.runtime.ClusterID, labelOperation: "original-create"}
				name := "existing"
				switch altered {
				case "external":
					labels = nil
				case "cluster":
					labels[labelCluster] = "another-cluster"
				case "operation":
					labels[labelOperation] = "another-operation"
				case "name":
					name = "externally-renamed"
				}
				annotation := swarm.Annotations{Name: name, Labels: labels}
				if kind == "config" {
					f.engine.configs = []swarm.Config{{ID: "existing-id", Spec: swarm.ConfigSpec{Annotations: annotation, Data: []byte("external-private-config")}}}
				} else {
					f.engine.secrets = []swarm.Secret{{ID: "existing-id", Spec: swarm.SecretSpec{Annotations: annotation, Data: []byte("external-private-secret")}}}
				}
				if altered != "missing-record" {
					f.record(t, kind, "existing-id", "existing", "original-create")
				}
				listed, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list", Kind: kind})
				if err != nil || len(listed.Items) != 1 || listed.Items[0].Owned {
					t.Fatal("external evidence claimed ownership", listed, err)
				}
				remove := f.operation(t, "remove-external", "DOCKER_RESOURCE_DELETE")
				if _, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "delete", Kind: kind, ID: "existing-id", OperationID: remove.ID, AcknowledgeRestoreRisk: true}); !errors.Is(err, ErrUnowned) {
					t.Fatal("external evidence allowed deletion", err)
				}
				create := f.operation(t, "new-create", "DOCKER_RESOURCE_CREATE")
				if _, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "create", Kind: kind, Name: name, Content: []byte("new-content"), OperationID: create.ID}); !errors.Is(err, storage.ErrConflict) || f.engine.mutations.Load() != 0 {
					t.Fatal("new request replaced or duplicated an existing resource", err)
				}
			})
		}
	}
}

func TestResourceAdmissionRejectsWrongIdentityBeforeWrites(t *testing.T) {
	f := newResourceFixture(t)
	create := f.operation(t, "admission-create", "DOCKER_RESOURCE_CREATE")
	base := Request{RuntimeID: f.runtime.ID, Action: "create", Kind: "config", Name: "admission", Content: []byte("fixture-data"), OperationID: create.ID}
	for _, field := range []string{"wrong-type", "wrong-runtime", "queued", "no-worker", "missing"} {
		t.Run(field, func(t *testing.T) {
			value := create
			switch field {
			case "wrong-type":
				value.Type = "UPDATE"
			case "wrong-runtime":
				value.RuntimeID = "other-runtime"
			case "queued":
				value.Status = model.OperationQueued
			case "no-worker":
				value.WorkerID = ""
			}
			m := f.manager
			m.Store = resourceAdmissionStore{Store: f.store, runtime: f.runtime, operation: value, missing: field == "missing"}
			before := f.engine.requests.Load()
			if _, err := m.Do(t.Context(), base); !errors.Is(err, ErrIdentity) || before != f.engine.requests.Load() {
				t.Fatal("invalid operation reached the Engine", err)
			}
		})
	}
	for _, field := range []string{"compose", "disabled", "missing-cluster", "missing-runtime", "invalid-kind", "unbound-node", "wrong-bound-cluster", "unknown-credential", "incomplete-credential"} {
		t.Run(field, func(t *testing.T) {
			m, request, runtime := f.manager, base, f.runtime
			switch field {
			case "compose":
				runtime.Type = model.RuntimeDocker
			case "disabled":
				runtime.Enabled = false
			case "missing-cluster":
				runtime.ClusterID = ""
			case "missing-runtime":
				request.RuntimeID = "missing"
			case "invalid-kind":
				request.Kind = "volume"
			case "unbound-node", "wrong-bound-cluster":
				runtime.Endpoint = "node://node-fixture"
				if field == "wrong-bound-cluster" {
					m.Config.Controller.Nodes = []config.ControllerNodeConfig{{NodeID: "node-fixture", ClusterID: "another-cluster"}}
				}
			case "unknown-credential", "incomplete-credential":
				runtime.CredentialRef = "fixture-credential"
				if field == "incomplete-credential" {
					m.Config.Credentials = map[string]config.DockerCredential{"fixture-credential": {CAFile: "missing-fixture-ca"}}
				}
			}
			if field != "missing-runtime" {
				m.Store = resourceAdmissionStore{Store: f.store, runtime: runtime, operation: create}
			}
			before := f.engine.requests.Load()
			if _, err := m.Do(t.Context(), request); err == nil || before != f.engine.requests.Load() || f.engine.mutations.Load() != 0 {
				t.Fatal("invalid runtime reached the Engine", err)
			}
		})
	}
}

type resourceAdmissionStore struct {
	storage.Store
	runtime   model.Runtime
	operation model.DeploymentOperation
	missing   bool
}

func (s resourceAdmissionStore) GetRuntime(context.Context, string) (model.Runtime, error) {
	return s.runtime, nil
}
func (s resourceAdmissionStore) GetDeploymentOperation(context.Context, string) (model.DeploymentOperation, error) {
	if s.missing {
		return model.DeploymentOperation{}, storage.ErrNotFound
	}
	return s.operation, nil
}

func TestResourceManagerRequiresLiveManagerClusterProof(t *testing.T) {
	for _, proof := range []string{"worker", "no-cluster", "other-cluster", "unavailable"} {
		t.Run(proof, func(t *testing.T) {
			f := newResourceFixture(t)
			switch proof {
			case "worker":
				f.engine.info.Swarm.ControlAvailable = false
			case "no-cluster":
				f.engine.info.Swarm.Cluster = nil
			case "other-cluster":
				f.engine.info.Swarm.Cluster.ID = "another-cluster"
			case "unavailable":
				f.engine.failPath = "/info"
			}
			_, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list"})
			if err == nil || f.engine.mutations.Load() != 0 {
				t.Fatal("missing live manager proof was accepted", err)
			}
			if (proof != "unavailable") != errors.Is(err, ErrIdentity) {
				t.Fatal("unavailable observation was confused with a proven identity mismatch", err)
			}
		})
	}
}

func TestResourceDeletionRechecksOwnershipAfterDiscovery(t *testing.T) {
	for _, kind := range []string{"config", "secret"} {
		t.Run(kind, func(t *testing.T) {
			f := newResourceFixture(t)
			create := f.operation(t, "changing-create", "DOCKER_RESOURCE_CREATE")
			created, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "create", Kind: kind, Name: "changing", Content: []byte("retained-private-content"), OperationID: create.ID})
			if err != nil {
				t.Fatal(err)
			}
			f.record(t, kind, created.ID, "changing", create.ID)
			remove := f.operation(t, "changing-delete", "DOCKER_RESOURCE_DELETE")
			f.engine.mu.Lock()
			f.engine.inventoryReads[kind] = 0
			f.engine.onInventoryRead = func(readKind string, count int) {
				if readKind == kind && count == 2 {
					if kind == "config" {
						f.engine.configs[0].Spec.Labels[labelOperation] = "external-operation"
					} else {
						f.engine.secrets[0].Spec.Labels[labelOperation] = "external-operation"
					}
				}
			}
			f.engine.mu.Unlock()
			request := Request{RuntimeID: f.runtime.ID, Action: "delete", Kind: kind, ID: created.ID, OperationID: remove.ID, AcknowledgeRestoreRisk: true}
			if _, err := f.manager.Do(t.Context(), request); !errors.Is(err, ErrUnowned) || f.engine.mutations.Load() != 1 {
				t.Fatal("stale discovery authorized deletion after ownership changed", err)
			}
			listed, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list", Kind: kind})
			if err != nil || len(listed.Items) != 1 || listed.Items[0].Owned || listed.Items[0].ID != created.ID {
				t.Fatal("changed external resource was removed or claimed", listed, err)
			}
			if record, err := f.store.GetDockerResource(t.Context(), f.runtime.ClusterID, kind, created.ID); err != nil || record.CreateOperationID != create.ID {
				t.Fatal("ownership conflict erased original operation evidence", record, err)
			}
		})
	}
}

type unavailableResourceEvidenceStore struct {
	storage.Store
	err error
}

func (s unavailableResourceEvidenceStore) GetDockerResource(context.Context, string, string, string) (storage.DockerResource, error) {
	return storage.DockerResource{}, s.err
}

func TestUnavailableResourceOwnershipEvidenceStaysRetryable(t *testing.T) {
	for _, kind := range []string{"config", "secret"} {
		t.Run(kind, func(t *testing.T) {
			f := newResourceFixture(t)
			create := f.operation(t, "outage-create", "DOCKER_RESOURCE_CREATE")
			request := Request{RuntimeID: f.runtime.ID, Action: "create", Kind: kind, Name: "ownership-outage", Content: []byte("fixture-private"), OperationID: create.ID}
			created, err := f.manager.Do(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			f.record(t, kind, created.ID, request.Name, create.ID)
			outage := errors.New("temporary ownership store outage")
			manager := f.manager
			manager.Store = unavailableResourceEvidenceStore{Store: f.store, err: outage}
			if listed, err := manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list", Kind: kind}); !errors.Is(err, outage) || len(listed.Items) != 0 {
				t.Fatal("unknown ownership was published as complete inventory", listed, err)
			}
			if _, err := manager.Do(t.Context(), request); !errors.Is(err, outage) || f.engine.mutations.Load() != 1 {
				t.Fatal("ownership outage duplicated or permanently rejected the resource", err)
			}
			remove := f.operation(t, "outage-delete", "DOCKER_RESOURCE_DELETE")
			deletion := Request{RuntimeID: f.runtime.ID, Action: "delete", Kind: kind, ID: created.ID, OperationID: remove.ID, AcknowledgeRestoreRisk: true}
			if _, err := manager.Do(t.Context(), deletion); !errors.Is(err, outage) || f.engine.mutations.Load() != 1 {
				t.Fatal("unknown ownership allowed deletion or lost retryability", err)
			}
			if _, err := f.manager.Do(t.Context(), deletion); err != nil || f.engine.mutations.Load() != 2 {
				t.Fatal("ownership recovery did not allow the original delete", err)
			}
		})
	}
}

func TestResourceCreateValidationAndUnavailableInventoryPreventWrites(t *testing.T) {
	f := newResourceFixture(t)
	create := f.operation(t, "validate-create", "DOCKER_RESOURCE_CREATE")
	base := Request{RuntimeID: f.runtime.ID, Action: "create", Kind: "config", Name: "validated", Content: []byte("fixture-data"), OperationID: create.ID}
	for _, field := range []string{"empty-name", "empty-content", "oversized", "empty-kind", "unsupported-action"} {
		t.Run(field, func(t *testing.T) {
			request := base
			switch field {
			case "empty-name":
				request.Name = ""
			case "empty-content":
				request.Content = nil
			case "oversized":
				request.Content = make([]byte, maxContent+1)
			case "empty-kind":
				request.Kind = ""
			case "unsupported-action":
				request.Action = "replace"
			}
			if _, err := f.manager.Do(t.Context(), request); !errors.Is(err, storage.ErrConflict) || f.engine.mutations.Load() != 0 {
				t.Fatal("invalid content or operation reached a write", err)
			}
		})
	}
	for _, path := range []string{"/services", "/configs", "/secrets"} {
		t.Run(path, func(t *testing.T) {
			f.engine.mu.Lock()
			f.engine.failPath = path
			f.engine.mu.Unlock()
			if result, err := f.manager.Do(t.Context(), Request{RuntimeID: f.runtime.ID, Action: "list"}); err == nil || len(result.Items) != 0 {
				t.Fatal("incomplete inventory appeared complete", result, err)
			}
			request := base
			request.Kind = strings.TrimSuffix(strings.TrimPrefix(path, "/"), "s")
			if path == "/services" {
				request.Kind = "config"
			}
			if _, err := f.manager.Do(t.Context(), request); err == nil || f.engine.mutations.Load() != 0 {
				t.Fatal("unavailable inventory allowed creation", err)
			}
		})
	}
}

func TestResourceManagerLocalNodeRequiresDurableOwnerBeforeSocketAccess(t *testing.T) {
	f := newResourceFixture(t)
	binding := config.ControllerNodeConfig{InstanceID: "fixture-self", NodeID: "node-fixture", EngineID: "engine-fixture", ClusterID: f.runtime.ClusterID}
	if err := f.manager.checkOwner(t.Context(), binding); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("missing owner session accepted", err)
	}
	for _, scenario := range []struct{ kind, parent, payload string }{
		{"wrong-kind", binding.NodeID, `{"owner":1}`},
		{"node-session", "another-node", `{"owner":1}`},
		{"node-session", binding.NodeID, `{"owner":0}`},
		{"node-session", binding.NodeID, `{"owner":-1}`},
		{"node-session", binding.NodeID, `not-json`},
		{"node-session", binding.NodeID, `{"owner":1}`},
	} {
		if _, err := f.store.DB().ExecContext(t.Context(), `DELETE FROM control_documents WHERE id=?`, "node-session:"+binding.NodeID); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if _, err := f.store.DB().ExecContext(t.Context(), `INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at) VALUES(?,?,?,?,1,?,?)`, "node-session:"+binding.NodeID, scenario.kind, scenario.parent, scenario.payload, now, now); err != nil {
			t.Fatal(err)
		}
		err := f.manager.checkOwner(t.Context(), binding)
		valid := scenario.kind == "node-session" && scenario.parent == binding.NodeID && scenario.payload == `{"owner":1}`
		if valid && err != nil || !valid && !errors.Is(err, ErrIdentity) {
			t.Fatalf("owner evidence boundary failed for %s/%s: %v", scenario.kind, scenario.parent, err)
		}
	}
	manager := f.manager
	manager.Config.Controller.InstanceID = binding.InstanceID
	manager.Config.Controller.Nodes = []config.ControllerNodeConfig{binding}
	runtime := f.runtime
	runtime.Endpoint, runtime.CredentialRef = "node://"+binding.NodeID, "missing-credential"
	manager.Store = resourceAdmissionStore{Store: f.store, runtime: runtime}
	// Wrapped stores cannot establish a database-session owner; no socket is opened.
	if _, err := manager.Do(t.Context(), Request{RuntimeID: runtime.ID, Action: "list"}); !errors.Is(err, ErrIdentity) || f.engine.requests.Load() != 0 {
		t.Fatal("non-SQL owner authority reached a socket", err)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE runtimes SET endpoint=?,credential_ref=? WHERE id=?`, runtime.Endpoint, runtime.CredentialRef, runtime.ID); err != nil {
		t.Fatal(err)
	}
	manager.Store = f.store
	if _, err := manager.Do(t.Context(), Request{RuntimeID: runtime.ID, Action: "list"}); !errors.Is(err, ErrIdentity) || f.engine.requests.Load() != 0 {
		t.Fatal("missing credential reached the local Docker socket", err)
	}
}

func resourcePeer(t *testing.T, dir, ca, caKey, instance string) (config.ControllerInternalConfig, *tls.Config) {
	t.Helper()
	uri, err := controlplane.IdentityURI(instance)
	if err != nil {
		t.Fatal(err)
	}
	cert, key := filepath.Join(dir, instance+".pem"), filepath.Join(dir, instance+".key")
	if err := pki.Issue(ca, caKey, cert, key, pki.CertificateOptions{CommonName: instance, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, URIs: []*url.URL{uri}, Server: true, Client: true, Duration: time.Hour}); err != nil {
		t.Fatal(err)
	}
	cfg, err := controlplane.InternalTLS(ca, cert, key, instance)
	if err != nil {
		t.Fatal(err)
	}
	return config.ControllerInternalConfig{CAFile: ca, CertFile: cert, KeyFile: key}, cfg
}

func TestResourceManagerProxyUsesBoundControllerIdentityAndRedactedResponses(t *testing.T) {
	f := newResourceFixture(t)
	dir := t.TempDir()
	ca, caKey := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := pki.InitCA(ca, caKey, "isolated Docker resource CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	internal, _ := resourcePeer(t, dir, ca, caKey, "fixture-caller")
	_, serverTLS := resourcePeer(t, dir, ca, caKey, "fixture-manager")
	var mu sync.Mutex
	accepted, mode := 0, "ok"
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		peer, err := controlplane.CertificateIdentity(r.TLS.PeerCertificates[0])
		if err != nil || peer != "fixture-caller" || r.Method != http.MethodPost || r.URL.Path != "/internal/v1/docker-resources" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("proxy changed authenticated request routing")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var request Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.OperationID != "proxy-create" || string(request.Content) != "proxy-fixture-private" {
			t.Error("proxy lost encrypted-operation execution parameters")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		accepted++
		switch mode {
		case "rejected":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("proxy-fixture-private must not escape"))
		case "malformed":
			_, _ = w.Write([]byte("not-json"))
		default:
			_ = json.NewEncoder(w).Encode(Result{ID: "proxy-resource"})
		}
	}))
	server.TLS = serverTLS
	server.StartTLS()
	t.Cleanup(server.Close)
	manager := f.manager
	manager.Config.Controller.InstanceID = "fixture-caller"
	manager.Config.Controller.Internal = internal
	binding := config.ControllerNodeConfig{InstanceID: "fixture-manager", NodeID: "remote-node", ClusterID: f.runtime.ClusterID, URL: server.URL}
	manager.Config.Controller.Nodes = []config.ControllerNodeConfig{binding}
	runtime := f.runtime
	runtime.Endpoint = "node://" + binding.NodeID
	create := f.operation(t, "proxy-create", "DOCKER_RESOURCE_CREATE")
	manager.Store = resourceAdmissionStore{Store: f.store, runtime: runtime, operation: create}
	request := Request{RuntimeID: runtime.ID, Action: "create", Kind: "secret", Name: "proxy-secret", Content: []byte("proxy-fixture-private"), OperationID: create.ID}
	if _, err := manager.Do(t.Context(), request); !errors.Is(err, ErrIdentity) {
		t.Fatal("legacy mutation proxy bypassed frozen execution", err)
	}
	forward := func() (Result, error) {
		var result Result
		err := Forward(t.Context(), manager.Config, manager.Config.Controller.Nodes[0], "/internal/v1/docker-resources", request, &result)
		return result, err
	}
	if result, err := forward(); err != nil || result.ID != "proxy-resource" {
		t.Fatal("bound manager proxy failed", result, err)
	}
	for _, response := range []string{"rejected", "malformed"} {
		mu.Lock()
		mode = response
		mu.Unlock()
		if result, err := forward(); err == nil || result.ID != "" || strings.Contains(err.Error(), string(request.Content)) {
			t.Fatal("proxy failure exposed private content or became success", result, err)
		}
	}
	mu.Lock()
	before := accepted
	mu.Unlock()
	manager.Config.Controller.Nodes[0].InstanceID = "different-manager"
	if _, err := forward(); err == nil {
		t.Fatal("same-CA certificate impersonated the bound manager")
	}
	mu.Lock()
	if accepted != before {
		t.Error("wrong manager received confidential request body")
	}
	mu.Unlock()
	manager.Config.Controller.Nodes[0] = binding
	manager.Config.Controller.Nodes[0].URL = "http://127.0.0.1"
	if _, err := forward(); err == nil {
		t.Fatal("cleartext manager accepted")
	}
	manager.Config.Controller.Nodes[0] = binding
	manager.Config.Controller.Internal.CertFile = filepath.Join(dir, "missing.pem")
	if _, err := forward(); err == nil {
		t.Fatal("missing caller certificate accepted")
	}
	if f.engine.requests.Load() != 0 {
		t.Fatal("remote manager operation escaped into local Engine")
	}
}

func TestInternalResourceHandlerRejectsUnverifiedPeersAndMismatchedExecution(t *testing.T) {
	f := newResourceFixture(t)
	manager := f.manager
	manager.Config.Controller.InstanceID = "self"
	manager.Config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "caller", NodeID: "caller-node"}, {InstanceID: "self", NodeID: "local-node", ClusterID: f.runtime.ClusterID}}
	uri, _ := controlplane.IdentityURI("caller")
	cert := &x509.Certificate{URIs: []*url.URL{uri}}
	verified := &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{cert}}
	requestBody := `{"runtime_id":"runtime-fixture","action":"list"}`
	call := func(method, body string, state *tls.ConnectionState) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/internal/v1/docker-resources", strings.NewReader(body))
		r.TLS = state
		w := httptest.NewRecorder()
		manager.InternalHandler().ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "fixture-private") {
			t.Fatal("internal response may cache or expose confidential content")
		}
		return w
	}
	for _, state := range []*tls.ConnectionState{nil, {}, {PeerCertificates: []*x509.Certificate{cert}}, {VerifiedChains: [][]*x509.Certificate{{cert}}}, {VerifiedChains: [][]*x509.Certificate{{cert}}, PeerCertificates: []*x509.Certificate{{}}}} {
		if w := call(http.MethodPost, requestBody, state); w.Code != http.StatusForbidden {
			t.Fatal("unverified peer reached resource management", w.Code)
		}
	}
	unknown, _ := controlplane.IdentityURI("unknown")
	unknownCert := &x509.Certificate{URIs: []*url.URL{unknown}}
	if w := call(http.MethodPost, requestBody, &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{unknownCert}}, PeerCertificates: []*x509.Certificate{unknownCert}}); w.Code != http.StatusForbidden {
		t.Fatal("unconfigured controller gained authority", w.Code)
	}
	if w := call(http.MethodGet, requestBody, verified); w.Code != http.StatusForbidden {
		t.Fatal("wrong method gained authority", w.Code)
	}
	for _, body := range []string{"not-json", `{"runtime_id":"runtime-fixture","unexpected":true}`, strings.Repeat(" ", (1<<20)+1) + requestBody} {
		if w := call(http.MethodPost, body, verified); w.Code != http.StatusBadRequest {
			t.Fatal("invalid or oversized request accepted", w.Code)
		}
	}
	if w := call(http.MethodPost, `{"runtime_id":"missing","action":"list"}`, verified); w.Code != http.StatusForbidden {
		t.Fatal("unknown runtime accepted", w.Code)
	}
	if w := call(http.MethodPost, requestBody, verified); w.Code != http.StatusForbidden {
		t.Fatal("nonlocal runtime accepted", w.Code)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE runtimes SET endpoint=? WHERE id=?`, "node://local-node", f.runtime.ID); err != nil {
		t.Fatal(err)
	}
	f.store.ConfigureOperationNodes(map[string]model.RuntimeExecutionIdentity{
		"node://local-node": {NodeID: "local-node", EngineID: "fixture-engine"},
	})
	create := f.operation(t, "internal-create", "DOCKER_RESOURCE_CREATE")
	internalRequest := Request{RuntimeID: f.runtime.ID, Action: "create", Kind: "secret", OperationID: create.ID, Name: "private", Content: []byte("fixture-private")}
	body, _ := json.Marshal(internalRequest)
	if w := call(http.MethodPost, string(body), verified); w.Code != http.StatusForbidden {
		t.Fatal("peer executed another worker's operation", w.Code)
	}
	if _, err := f.store.DB().ExecContext(t.Context(), `UPDATE deployment_operations SET worker_id=? WHERE id=?`, "caller", create.ID); err != nil {
		t.Fatal(err)
	}
	if w := call(http.MethodPost, string(body), verified); w.Code != http.StatusForbidden {
		t.Fatal("legacy mutation bypassed operation-only execution", w.Code)
	}
	// Owner proof fails before constructing a Docker client: even authenticated
	// requests cannot fall back to the machine's real /var/run/docker.sock.
	if f.engine.requests.Load() != 0 {
		t.Fatal("rejected internal request reached an Engine")
	}
}
