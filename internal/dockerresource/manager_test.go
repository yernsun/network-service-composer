package dockerresource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type interruptedOperationStore struct {
	storage.Store
	readErr error
}

func (s interruptedOperationStore) GetRuntime(context.Context, string) (model.Runtime, error) {
	return model.Runtime{ID: "rt-test", Type: model.RuntimeSwarm, Enabled: true, ClusterID: "cluster-test"}, nil
}
func (s interruptedOperationStore) GetDeploymentOperation(context.Context, string) (model.DeploymentOperation, error) {
	return model.DeploymentOperation{}, s.readErr
}

func TestStorageOutageDoesNotBecomePermanentIdentityRejection(t *testing.T) {
	outage := errors.New("storage temporarily unavailable")
	manager := Manager{Store: interruptedOperationStore{readErr: outage}}
	_, err := manager.Do(t.Context(), Request{RuntimeID: "rt-test", Action: "create", Kind: "config", OperationID: "op-test"})
	if !errors.Is(err, outage) {
		t.Fatalf("operation storage outage was converted into permanent identity error: %v", err)
	}
	manager.Store = interruptedOperationStore{readErr: storage.ErrNotFound}
	_, err = manager.Do(t.Context(), Request{RuntimeID: "rt-test", Action: "create", Kind: "config", OperationID: "op-test"})
	if !errors.Is(err, ErrIdentity) {
		t.Fatalf("missing operation was not rejected: %v", err)
	}
}

func TestLostCreateResponseRetainsRetryWhenDockerVerificationFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected Docker method %s", r.Method)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.41"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	for _, kind := range []string{"config", "secret"} {
		t.Run(kind, func(t *testing.T) {
			same, err := (Manager{}).sameOperation(t.Context(), cli, kind, "resource", "cluster", "operation")
			if same || err == nil {
				t.Fatalf("Docker verification failure became a permanent ownership decision: same=%t err=%v", same, err)
			}
		})
	}
}
