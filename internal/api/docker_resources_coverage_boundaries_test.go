package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/dockerresource"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestCoverageDockerResourceHTTPQueuesValidatedEncryptedContent(t *testing.T) {
	for _, kind := range []string{"config", "secret"} {
		t.Run(kind, func(t *testing.T) {
			server, handler, runtime, _ := managedRuntimeFixture(t)
			runtime.Type, runtime.ClusterID = model.RuntimeSwarm, "fixture-cluster"
			coverageRuntimeSnapshot(t, server, runtime)
			path := "/runtimes/" + runtime.ID + "/docker-resources"
			content := []byte("private resource content\nwith multiple lines")
			encoded := base64.StdEncoding.EncodeToString(content)
			input := map[string]string{"kind": kind, "name": "  Web_Config-1.0  ", "content_base64": encoded}
			headers := map[string]string{"Idempotency-Key": "coverage-" + kind}
			firstResponse := managementRequest(t, handler, http.MethodPost, path, input, headers)
			first := responseData[model.DeploymentOperation](t, firstResponse, http.StatusAccepted)
			stored, err := server.store.GetDeploymentOperation(t.Context(), first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if first.Status != model.OperationQueued || first.Type != "DOCKER_RESOURCE_CREATE" || first.RuntimeID != runtime.ID || len(stored.EncryptedInput) == 0 || stored.EncryptionKeyID != "v1" {
				t.Fatalf("resource queue did not freeze encrypted work: %#v", first)
			}
			if strings.Contains(firstResponse.Body.String(), encoded) || strings.Contains(firstResponse.Body.String(), "private resource") || bytes.Contains(stored.EncryptedInput, content) {
				t.Fatal("resource plaintext escaped encrypted queued input")
			}
			ring, err := security.NewKeyring("v1", map[string]string{"v1": base64.StdEncoding.EncodeToString(make([]byte, 32))})
			if err != nil {
				t.Fatal(err)
			}
			plain, keyID, err := ring.Decrypt(stored.EncryptedInput, []byte("deployment-operation:"+first.ID))
			if err != nil || keyID != "v1" {
				t.Fatalf("worker cannot decrypt queued input: %v", err)
			}
			var frozen struct {
				Kind, Name string
				Content    []byte
			}
			if err := json.Unmarshal(plain, &frozen); err != nil || frozen.Kind != kind || frozen.Name != "Web_Config-1.0" || !bytes.Equal(frozen.Content, content) {
				t.Fatal("validated name/content changed before execution")
			}
			if _, _, err := ring.Decrypt(stored.EncryptedInput, []byte("deployment-operation:other")); err == nil {
				t.Fatal("encrypted input was transferable to another operation")
			}
			repeated := responseData[model.DeploymentOperation](t, managementRequest(t, handler, http.MethodPost, path, input, headers), http.StatusAccepted)
			if repeated.ID != first.ID {
				t.Fatal("same resource request duplicated queued work")
			}
			input["content_base64"] = base64.StdEncoding.EncodeToString([]byte("changed-content"))
			if response := managementRequest(t, handler, http.MethodPost, path, input, headers); response.Code != http.StatusConflict {
				t.Fatalf("idempotency key accepted different content: %d", response.Code)
			}
			operations, err := server.store.ListDeploymentOperations(t.Context(), "", storage.Page{Limit: 200})
			if err != nil || len(operations) != 1 {
				t.Fatalf("conflicting retry mutated queue: %d %v", len(operations), err)
			}
		})
	}
}

func TestCoverageDockerResourceValidationNeverQueuesRejectedRequests(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	runtime.Type, runtime.ClusterID = model.RuntimeSwarm, "fixture-cluster"
	coverageRuntimeSnapshot(t, server, runtime)
	path := "/runtimes/" + runtime.ID + "/docker-resources"
	validContent := base64.StdEncoding.EncodeToString([]byte("private-value"))
	for _, scenario := range []struct {
		name, kind, resource, content, key string
		status                             int
	}{
		{"unsupported-kind", "volume", "config", validContent, "key", 422},
		{"empty-name", "config", " ", validContent, "key", 422},
		{"name-too-long", "config", strings.Repeat("a", 256), validContent, "key", 422},
		{"name-leading-separator", "config", "-config", validContent, "key", 422},
		{"name-space", "config", "two words", validContent, "key", 422},
		{"name-non-ascii", "config", "café", validContent, "key", 422},
		{"invalid-base64", "secret", "config", "%%%", "key", 422},
		{"empty-content", "secret", "config", "", "key", 422},
		{"content-too-large", "secret", "config", base64.StdEncoding.EncodeToString(make([]byte, (500<<10)+1)), "key", 422},
		{"missing-idempotency", "secret", "config", validContent, "", 400},
		{"long-idempotency", "config", "config", validContent, strings.Repeat("x", 201), 400},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			response := managementRequest(t, handler, http.MethodPost, path,
				map[string]string{"kind": scenario.kind, "name": scenario.resource, "content_base64": scenario.content},
				map[string]string{"Idempotency-Key": scenario.key})
			if response.Code != scenario.status || strings.Contains(response.Body.String(), validContent) {
				t.Fatalf("invalid resource request admitted or content leaked: %d", response.Code)
			}
		})
	}
	if response := managementRequest(t, handler, http.MethodPost, path, `{`, map[string]string{"Idempotency-Key": "json"}); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON accepted: %d", response.Code)
	}
	operations, err := server.store.ListDeploymentOperations(t.Context(), "", storage.Page{Limit: 200})
	if err != nil || len(operations) != 0 {
		t.Fatalf("invalid input left pending resource mutations: %d %v", len(operations), err)
	}
}

func TestCoverageDockerResourceRuntimeAuthorizationAndManagementBoundaries(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	path := "/runtimes/" + runtime.ID + "/docker-resources"
	for _, scenario := range []struct {
		name    string
		runtime model.Runtime
	}{
		{"compose", runtime},
		{"disabled", func() model.Runtime {
			r := runtime
			r.Type, r.ClusterID, r.Enabled = model.RuntimeSwarm, "cluster", false
			return r
		}()},
		{"unmanaged", func() model.Runtime {
			r := runtime
			r.Type, r.ClusterID, r.ManagementEnabled = model.RuntimeSwarm, "cluster", false
			return r
		}()},
		{"unknown-cluster", func() model.Runtime { r := runtime; r.Type = model.RuntimeSwarm; return r }()},
	} {
		coverageRuntimeSnapshot(t, server, scenario.runtime)
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			endpoint := path
			if method == http.MethodDelete {
				endpoint += "/config?kind=config"
			}
			response := managementRequest(t, handler, method, endpoint, `{}`, map[string]string{"Idempotency-Key": "rejected"})
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s %s crossed runtime boundary: %d", scenario.name, method, response.Code)
			}
		}
	}
	if response := managementRequest(t, handler, http.MethodGet, "/runtimes/missing/docker-resources", nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("unknown runtime admitted: %d", response.Code)
	}
	router := chi.NewRouter()
	router.Get("/runtimes/{id}/docker-resources", server.listDockerResources)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request = request.WithContext(context.WithValue(request.Context(), identityContextKey{}, core.User{Role: "viewer", Enabled: true}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer gained resource inventory: %d", response.Code)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated direct handler gained inventory: %d", response.Code)
	}
}

func TestCoverageDockerResourceListPaginatesRedactedOwnershipAndUsage(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	clusterID := "fixture-cluster"
	labels := map[string]string{"com.nsc.docker-resource.owner": "nsc", "com.nsc.docker-resource.cluster": clusterID, "com.nsc.docker-resource.operation": "create-config"}
	configs := []swarm.Config{
		{ID: "c", Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: "external.conf"}, Data: []byte("private-config-content")}},
		{ID: "a", Spec: swarm.ConfigSpec{Annotations: swarm.Annotations{Name: "owned.conf", Labels: labels}, Data: []byte("private-owned-content")}},
	}
	secrets := []swarm.Secret{{ID: "b", Spec: swarm.SecretSpec{Annotations: swarm.Annotations{Name: "external-password"}, Data: []byte("private-secret-content")}}}
	services := []swarm.Service{
		{ID: "nil-spec", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "empty-service"}}},
		{ID: "consumer", Spec: swarm.ServiceSpec{Annotations: swarm.Annotations{Name: "web"}, TaskTemplate: swarm.TaskSpec{ContainerSpec: &swarm.ContainerSpec{
			Configs: []*swarm.ConfigReference{{ConfigID: "a"}}, Secrets: []*swarm.SecretReference{{SecretID: "b"}},
		}}}},
	}
	var writes atomic.Int32
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if coverageDockerPing(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			writes.Add(1)
			http.Error(w, "unexpected mutation", 500)
			return
		}
		switch coverageDockerPath(r) {
		case "/info":
			_ = json.NewEncoder(w).Encode(map[string]any{"Swarm": map[string]any{"ControlAvailable": true, "Cluster": map[string]string{"ID": clusterID}}})
		case "/services":
			_ = json.NewEncoder(w).Encode(services)
		case "/configs":
			_ = json.NewEncoder(w).Encode(configs)
		case "/secrets":
			_ = json.NewEncoder(w).Encode(secrets)
		default:
			t.Errorf("unexpected SDK inventory path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer docker.Close()
	runtime.Type, runtime.ClusterID, runtime.Endpoint = model.RuntimeSwarm, clusterID, docker.URL
	coverageRuntimeSnapshot(t, server, runtime)
	if err := server.store.RecordDockerResource(t.Context(), storage.DockerResource{ClusterID: clusterID, Kind: "config", DockerID: "a", Name: "owned.conf", CreateOperationID: "create-config", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	path := "/runtimes/" + runtime.ID + "/docker-resources"
	decode := func(response *httptest.ResponseRecorder) ([]dockerresource.Item, string, int) {
		t.Helper()
		var envelope struct {
			Data       []dockerresource.Item
			Pagination struct {
				Next  string `json:"next_cursor"`
				Limit int
			}
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &envelope) != nil {
			t.Fatalf("inventory failed: %d %s", response.Code, response.Body)
		}
		if strings.Contains(response.Body.String(), "private-") || writes.Load() != 0 {
			t.Fatal("inventory leaked content or mutated Docker")
		}
		return envelope.Data, envelope.Pagination.Next, envelope.Pagination.Limit
	}
	first, cursor, limit := decode(managementRequest(t, handler, http.MethodGet, path+"?limit=2", nil, nil))
	if len(first) != 2 || first[0].ID != "a" || !first[0].Owned || len(first[0].UsedBy) != 1 || first[0].UsedBy[0] != "web" || first[1].ID != "b" || first[1].Owned || len(first[1].UsedBy) != 1 || cursor != "b" || limit != 2 {
		t.Fatalf("inventory ownership/order/usage lost: %#v %q", first, cursor)
	}
	last, next, _ := decode(managementRequest(t, handler, http.MethodGet, path+"?limit=2&cursor="+cursor, nil, nil))
	if len(last) != 1 || last[0].ID != "c" || last[0].Owned || next != "" {
		t.Fatalf("inventory pagination duplicated data: %#v %q", last, next)
	}
	for _, kind := range []string{"config", "secret"} {
		items, _, _ := decode(managementRequest(t, handler, http.MethodGet, path+"?kind="+kind, nil, nil))
		for _, item := range items {
			if item.Kind != kind {
				t.Fatal("kind filter leaked other resources")
			}
		}
		if len(items) != map[string]int{"config": 2, "secret": 1}[kind] {
			t.Fatal("kind filter dropped resources")
		}
	}
	_, _, limit = decode(managementRequest(t, handler, http.MethodGet, path+"?limit=201", nil, nil))
	if limit != 50 {
		t.Fatal("excessive inventory page did not use bounded default")
	}
	if response := managementRequest(t, handler, http.MethodGet, path+"?kind=volume", nil, nil); response.Code != http.StatusBadRequest {
		t.Fatal("unsupported resource kind accepted")
	}
}

func TestCoverageDockerResourceDeleteRequiresAcknowledgementAndClusterOwnership(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	runtime.Type, runtime.ClusterID = model.RuntimeSwarm, "fixture-cluster"
	coverageRuntimeSnapshot(t, server, runtime)
	now := time.Now().UTC()
	for _, record := range []storage.DockerResource{
		{ClusterID: runtime.ClusterID, Kind: "config", DockerID: "owned", Name: "owned.conf", CreateOperationID: "create-owned", CreatedAt: now},
		{ClusterID: "another-cluster", Kind: "config", DockerID: "foreign", Name: "foreign.conf", CreateOperationID: "create-foreign", CreatedAt: now},
	} {
		if err := server.store.RecordDockerResource(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	path := "/runtimes/" + runtime.ID + "/docker-resources/"
	headers := map[string]string{"Idempotency-Key": "remove-owned"}
	for _, scenario := range []struct {
		resource, query, body string
		status                int
	}{
		{"owned", "kind=volume", `{"acknowledge_restore_risk":true}`, 400},
		{strings.Repeat("x", 256), "kind=config", `{"acknowledge_restore_risk":true}`, 400},
		{"owned", "kind=config", `{`, 400},
		{"owned", "kind=config", `{"acknowledge_restore_risk":false}`, 428},
		{"foreign", "kind=config", `{"acknowledge_restore_risk":true}`, 404},
		{"missing", "kind=config", `{"acknowledge_restore_risk":true}`, 404},
	} {
		response := managementRequest(t, handler, http.MethodDelete, path+scenario.resource+"?"+scenario.query, scenario.body, headers)
		if response.Code != scenario.status {
			t.Fatalf("delete resource safety boundary: %s got %d want %d", scenario.resource, response.Code, scenario.status)
		}
	}
	operation := responseData[model.DeploymentOperation](t, managementRequest(t, handler, http.MethodDelete, path+"owned?kind=config", `{"acknowledge_restore_risk":true}`, headers), http.StatusAccepted)
	if operation.Type != "DOCKER_RESOURCE_DELETE" || operation.Status != model.OperationQueued {
		t.Fatal("delete executed synchronously or queued wrong operation")
	}
	repeated := responseData[model.DeploymentOperation](t, managementRequest(t, handler, http.MethodDelete, path+"owned?kind=config", `{"acknowledge_restore_risk":true}`, headers), http.StatusAccepted)
	if repeated.ID != operation.ID {
		t.Fatal("delete retry queued duplicate work")
	}
	if _, err := server.store.GetDockerResource(t.Context(), runtime.ClusterID, "config", "owned"); err != nil {
		t.Fatal("API removed ownership before executor proved Docker removal")
	}
}
