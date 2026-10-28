package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/projectlife"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type recoveryInfoTransport func(*http.Request) (*http.Response, error)

func (f recoveryInfoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRuntimeInfoUsesConfiguredTransportAndOriginalLocalNodeBinding(t *testing.T) {
	for _, scenario := range []string{"remote", "remote TLS", "local", "replacement local engine", "replacement local node", "replacement local cluster", "missing reported local cluster", "missing local binding", "wrong local owner", "wrong local node", "missing local engine", "missing local cluster", "wrong local cluster", "credential on local binding", "missing credential", "incomplete TLS", "offline"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Controller.InstanceID = "recovery-owner"
			rt := model.Runtime{Endpoint: "tcp://fixture.example:2375", ClusterID: "cluster-original"}
			info := system.Info{ID: "engine-original", Swarm: swarm.Info{NodeID: "node-original", Cluster: &swarm.ClusterInfo{ID: "cluster-original"}}}
			local := strings.Contains(scenario, "local")
			if local {
				rt.Endpoint = "node://node-original"
				cfg.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "recovery-owner", NodeID: "node-original", EngineID: "engine-original", ClusterID: "cluster-original"}}
			}
			if scenario == "replacement local engine" {
				info.ID = "engine-replacement"
			}
			if scenario == "replacement local node" {
				info.Swarm.NodeID = "node-replacement"
			}
			if scenario == "replacement local cluster" {
				info.Swarm.Cluster.ID = "cluster-replacement"
			}
			if scenario == "missing reported local cluster" {
				info.Swarm.Cluster = nil
			}
			if scenario == "missing local binding" {
				cfg.Controller.Nodes = nil
			}
			switch scenario {
			case "wrong local owner":
				cfg.Controller.Nodes[0].InstanceID = "another-controller"
			case "wrong local node":
				cfg.Controller.Nodes[0].NodeID = "another-node"
			case "missing local engine":
				cfg.Controller.Nodes[0].EngineID = ""
			case "missing local cluster":
				cfg.Controller.Nodes[0].ClusterID = ""
			case "wrong local cluster":
				cfg.Controller.Nodes[0].ClusterID = "another-cluster"
			}
			if scenario == "remote TLS" || scenario == "credential on local binding" {
				rt.CredentialRef = "restored-tls"
				cfg.Credentials = map[string]config.DockerCredential{"restored-tls": {CAFile: "/private/restored-ca", CertFile: "/private/restored-cert", KeyFile: "/private/restored-key"}}
			}
			if scenario == "missing credential" {
				rt.CredentialRef = "not-restored"
			}
			if scenario == "incomplete TLS" {
				rt.CredentialRef = "tls"
				cfg.Credentials = map[string]config.DockerCredential{"tls": {CAFile: "/fixture/missing-ca"}}
			}
			reader := LiveReconciler{Config: cfg}
			opened, reads := false, 0
			if scenario != "incomplete TLS" {
				reader.Open = func(actual model.Runtime, files engine.TLSFiles) (*client.Client, error) {
					opened = true
					if scenario == "remote TLS" && (files.CA != "/private/restored-ca" || files.Certificate != "/private/restored-cert" || files.Key != "/private/restored-key") {
						t.Fatal("restored remote credentials were replaced")
					}
					if local && actual.Endpoint != "unix:///var/run/docker.sock" {
						t.Fatal("local binding was routed to a different endpoint", actual.Endpoint)
					}
					return client.New(client.WithHost("http://fixture.example"), client.WithVersion("1.53"), client.WithHTTPClient(&http.Client{Transport: recoveryInfoTransport(func(request *http.Request) (*http.Response, error) {
						reads++
						if request.Method != "GET" || !strings.HasSuffix(request.URL.Path, "/info") {
							t.Fatal("recovery transport issued a mutating request", request.Method, request.URL.Path)
						}
						if scenario == "offline" {
							return nil, errors.New("fixture offline")
						}
						raw, err := json.Marshal(info)
						if err != nil {
							t.Fatal(err)
						}
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
					})}))
				}
			}
			got, err := reader.readInfo(context.Background(), rt)
			if scenario == "remote" || scenario == "remote TLS" || scenario == "local" {
				if err != nil || got.ID != info.ID || !opened || reads != 1 {
					t.Fatal("restored read-only transport failed", got, err, reads)
				}
			} else if err == nil {
				t.Fatal("unavailable or changed original node accepted", got)
			}
			if scenario == "missing local binding" || scenario == "missing credential" || strings.HasPrefix(scenario, "wrong local ") || scenario == "missing local engine" || scenario == "missing local cluster" || scenario == "credential on local binding" {
				if opened || reads != 0 {
					t.Fatal("unverified binding reached Docker", reads)
				}
			}
		})
	}
}

func runningApplicationFixture(t *testing.T, setups ...func(*storage.SQLStore, model.DeploymentOperation)) (*storage.SQLStore, Plan, LiveReconciler, *projectlife.Observation) {
	t.Helper()
	store, ring, runtime := recoveryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	app := model.Application{ID: id.New(id.Application), RuntimeID: runtime.ID, Name: "restore application", ProjectName: "restore-app", Status: model.ApplicationRunning, Managed: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	revision := model.ApplicationRevision{ID: id.New(id.ApplicationRevision), ApplicationID: app.ID, Sequence: 1, EncryptionKeyID: "backup", CreatedAt: now}
	var err error
	revision.EncryptedCompose, err = ring.Encrypt([]byte("services: {}"), []byte("application-revision:"+revision.ID+":compose"))
	if err != nil {
		t.Fatal(err)
	}
	revision.EncryptedEnvironment, err = ring.Encrypt([]byte("{}"), []byte("application-revision:"+revision.ID+":environment"))
	if err != nil {
		t.Fatal(err)
	}
	revision.EncryptedResolved, err = ring.Encrypt([]byte("services: {}"), []byte("application-revision:"+revision.ID+":resolved"))
	if err != nil {
		t.Fatal(err)
	}
	op := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: app.ID, RuntimeID: runtime.ID, RevisionID: revision.ID, Type: "REDEPLOY", Status: model.OperationQueued, Revision: 1, CreatedAt: now, UpdatedAt: now}
	op.EncryptedInput, err = ring.Encrypt([]byte(`{"action":"redeploy"}`), []byte("deployment-operation:"+op.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateApplication(ctx, &app, &revision, &op); err != nil {
		t.Fatal(err)
	}
	service := model.Service{ID: id.New(id.Service), ApplicationID: app.ID, RuntimeID: runtime.ID, Name: "web", Namespace: app.ProjectName, ServiceKey: app.ProjectName + "/web", RuntimeObjectID: "owned-container", ExternalID: "owned-container", Managed: true, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimDeploymentOperation(ctx, "interrupted-owner", now, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, setup := range setups {
		current, err := store.GetDeploymentOperation(ctx, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		setup(store, current)
	}
	plan, err := Prepare(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Management.Enabled = true
	state := &projectlife.Observation{ObservedAt: now, Objects: []projectlife.Object{{ID: "owned-container", Kind: "container", Service: "web", Active: true}}, Ready: true}
	r := LiveReconciler{Store: store, Keys: ring, Config: cfg, Now: func() time.Time { return now }, ReadInfo: func(context.Context, model.Runtime) (system.Info, error) {
		return system.Info{ID: "original-engine"}, nil
	}, Observe: func(context.Context, model.Runtime, model.Application, []model.Service) (projectlife.Observation, error) {
		return *state, nil
	}}
	return store, plan, r, state
}

func TestReviewedRuntimeReconciliationReleasesUnknownApplicationOnceWithoutGuessingOutcome(t *testing.T) {
	store, plan, reader, _ := runningApplicationFixture(t)
	ctx := context.Background()
	proof, err := reader.Capture(ctx, plan.Items[0], RuntimeIdentity{EngineID: "original-engine"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.GetDeploymentOperation(ctx, plan.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Items[0].Decision, plan.Items[0].Reconciliation = "reconcile", proof
	plan.ActivationReviewed = true
	if err = Release(ctx, store, plan, reader.Validate); err != nil {
		t.Fatal(err)
	}
	preserved, err := store.GetDeploymentOperation(ctx, before.ID)
	if err != nil || preserved.Status != model.OperationRunning || preserved.Attempt != before.Attempt || preserved.Revision != before.Revision || string(preserved.EncryptedInput) != string(before.EncryptedInput) {
		t.Fatal("reconciliation guessed or rewrote the external result", err)
	}
	if held, err := storage.RecoveryHeld(ctx, store, "deployment", before.ID); err != nil || held {
		t.Fatal("reviewed runtime did not release its hold", err)
	}
	if err = Release(ctx, store, plan, reader.Validate); err == nil {
		t.Fatal("old reconciliation review was reusable")
	}
	claimed, err := store.ClaimDeploymentOperation(ctx, "new-owner", time.Now().Add(2*time.Hour), time.Now().Add(time.Hour))
	if err != nil || claimed.ID != before.ID || claimed.Attempt != before.Attempt+1 {
		t.Fatal("existing stale claim reconciliation was not available", err)
	}
}

func TestRuntimeReconciliationRejectsMissingStaleChangedAndUnavailableEvidence(t *testing.T) {
	for _, scenario := range []string{"missing receipt", "tampered receipt", "damaged encrypted receipt", "future receipt", "expired", "object changed", "runtime identity changed", "runtime offline", "ambiguous observation", "stale observation", "future observation", "object missing identity", "object missing kind", "missing current objects", "missing ownership"} {
		t.Run(scenario, func(t *testing.T) {
			store, plan, reader, state := runningApplicationFixture(t)
			ctx := context.Background()
			proof, err := reader.Capture(ctx, plan.Items[0], RuntimeIdentity{EngineID: "original-engine"})
			if err != nil {
				t.Fatal(err)
			}
			plan.Items[0].Decision, plan.Items[0].Reconciliation = "reconcile", proof
			plan.ActivationReviewed = true
			switch scenario {
			case "missing receipt":
				plan.Items[0].Reconciliation = nil
			case "tampered receipt":
				proof.Evidence.InputDigest = "changed by editor"
			case "damaged encrypted receipt":
				proof.Receipt[0] ^= 0xff
			case "future receipt":
				reader.Now = func() time.Time { return proof.Evidence.ObservedAt.Add(-time.Minute) }
			case "expired":
				reader.Now = func() time.Time { return proof.Evidence.ObservedAt.Add(3 * time.Minute) }
			case "object changed":
				state.Objects = []projectlife.Object{{ID: "replacement-container", Kind: "container", Service: "web", Active: true}}
			case "runtime identity changed":
				reader.ReadInfo = func(context.Context, model.Runtime) (system.Info, error) {
					return system.Info{ID: "replacement-engine"}, nil
				}
			case "runtime offline":
				reader.ReadInfo = func(context.Context, model.Runtime) (system.Info, error) {
					return system.Info{}, errors.New("runtime offline")
				}
			case "ambiguous observation":
				state.ObservedAt = time.Time{}
			case "stale observation":
				state.ObservedAt = state.ObservedAt.Add(-2 * time.Minute)
			case "future observation":
				state.ObservedAt = state.ObservedAt.Add(time.Minute)
			case "object missing identity":
				state.Objects[0].ID = ""
			case "object missing kind":
				state.Objects[0].Kind = ""
			case "missing current objects":
				state.Objects = nil
			case "missing ownership":
				reader.Observe = func(context.Context, model.Runtime, model.Application, []model.Service) (projectlife.Observation, error) {
					return projectlife.Observation{}, projectlife.ErrOwnership
				}
			}
			if err = Release(ctx, store, plan, reader.Validate); err == nil {
				t.Fatal("unknown/changed evidence released work")
			}
			if held, err := storage.RecoveryHeld(ctx, store, "deployment", plan.Items[0].ID); err != nil || !held {
				t.Fatal("failed observation cleared the hold", err)
			}
			preserved, err := store.GetDeploymentOperation(ctx, plan.Items[0].ID)
			if err != nil || preserved.Status != model.OperationRunning || preserved.Revision != plan.Items[0].Revision {
				t.Fatal("failed observation changed history", err)
			}
		})
	}
}

func TestReconciliationRejectsEngineNodeClusterAndManagerMismatchBeforeReadingWorkloads(t *testing.T) {
	for _, scenario := range []string{"engine", "node", "cluster", "manager"} {
		t.Run(scenario, func(t *testing.T) {
			store, plan, reader, _ := runningApplicationFixture(t)
			op, err := store.GetDeploymentOperation(context.Background(), plan.Items[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := store.GetRuntime(context.Background(), op.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			runtime.Type = model.RuntimeSwarm
			runtime.ClusterID = "original-cluster"
			op.ExecutionIdentity = model.RuntimeExecutionIdentity{PoolID: runtime.PoolID, Type: runtime.Type, Endpoint: runtime.Endpoint, CredentialRef: runtime.CredentialRef, ClusterID: runtime.ClusterID}
			reader.Store = &reconciliationRuntimeStore{Store: store, runtime: runtime}
			services, err := store.ListServices(context.Background(), storage.Page{Limit: 200})
			if err != nil {
				t.Fatal(err)
			}
			services[0].ServiceKey = "restore-app_web"
			reader.Store = &reconciliationRuntimeStore{Store: store, runtime: runtime, services: services, operation: &op}
			called := false
			infoRead := false
			reader.Observe = func(context.Context, model.Runtime, model.Application, []model.Service) (projectlife.Observation, error) {
				called = true
				return projectlife.Observation{}, nil
			}
			reader.ReadInfo = func(context.Context, model.Runtime) (system.Info, error) {
				infoRead = true
				info := system.Info{ID: "original-engine"}
				info.Swarm.NodeID = "original-node"
				info.Swarm.ControlAvailable = true
				info.Swarm.Cluster = &swarm.ClusterInfo{ID: "original-cluster"}
				switch scenario {
				case "engine":
					info.ID = "other-engine"
				case "node":
					info.Swarm.NodeID = "other-node"
				case "cluster":
					info.Swarm.Cluster.ID = "other-cluster"
				case "manager":
					info.Swarm.ControlAvailable = false
				}
				return info, nil
			}
			if _, err = reader.Capture(context.Background(), plan.Items[0], RuntimeIdentity{EngineID: "original-engine", NodeID: "original-node", ClusterID: "original-cluster"}); err == nil || called || !infoRead {
				t.Fatal("unverified manager target reached workload reads", err)
			}
		})
	}
}

// The fake shared-store observation exercises Swarm identity guards while the
// test database remains an isolated Compose-only SQLite fixture.
type reconciliationRuntimeStore struct {
	storage.Store
	runtime   model.Runtime
	services  []model.Service
	operation *model.DeploymentOperation
}

func (s *reconciliationRuntimeStore) GetDeploymentOperation(ctx context.Context, key string) (model.DeploymentOperation, error) {
	if s.operation != nil {
		return *s.operation, nil
	}
	return s.Store.GetDeploymentOperation(ctx, key)
}

func (s *reconciliationRuntimeStore) GetRuntime(context.Context, string) (model.Runtime, error) {
	return s.runtime, nil
}

func (s *reconciliationRuntimeStore) ListServices(context.Context, storage.Page) ([]model.Service, error) {
	return s.services, nil
}
