package api

import (
	"encoding/base64"
	"github.com/yernsun/network-service-composer/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDockerResourceCreateQueuesEncryptedIdempotentOperation(t *testing.T) {
	server, handler, runtime, _ := managedRuntimeFixture(t)
	path := "/runtimes/" + runtime.ID + "/docker-resources"
	value := "private-config-value"
	input := map[string]any{"kind": "config", "name": "app.conf", "content_base64": base64.StdEncoding.EncodeToString([]byte(value))}
	queue := func(key string, payload map[string]any) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1"+path, nil)
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		server.queueDockerResource(response, request, runtime, "create", payload)
		return response
	}
	first := queue("resource-create-1", input)
	operation := responseData[model.DeploymentOperation](t, first, http.StatusAccepted)
	if operation.Status != model.OperationQueued || operation.Type != "DOCKER_RESOURCE_CREATE" || first.Header().Get("Location") != "/api/v1/operations/"+operation.ID {
		t.Fatalf("incorrect queued operation: %#v", operation)
	}
	if strings.Contains(first.Body.String(), value) || strings.Contains(first.Body.String(), input["content_base64"].(string)) {
		t.Fatal("resource content leaked in response")
	}
	stored, err := server.store.GetDeploymentOperation(t.Context(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.EncryptedInput) == 0 || strings.Contains(string(stored.EncryptedInput), value) {
		t.Fatal("pending resource content was not encrypted")
	}
	repeated := responseData[model.DeploymentOperation](t, queue("resource-create-1", input), http.StatusAccepted)
	if repeated.ID != operation.ID {
		t.Fatal("idempotent retry queued a second operation")
	}
	input["content_base64"] = base64.StdEncoding.EncodeToString([]byte("other"))
	if got := queue("resource-create-1", input); got.Code != http.StatusConflict {
		t.Fatalf("changed idempotent request returned %d", got.Code)
	}
	if got := queue("", input); got.Code != http.StatusBadRequest {
		t.Fatalf("missing key returned %d", got.Code)
	}
	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list returned %d", anonymous.Code)
	}
}
