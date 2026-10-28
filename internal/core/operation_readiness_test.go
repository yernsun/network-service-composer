package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type readinessFixture struct {
	c           *Core
	app         model.Application
	op          model.DeploymentOperation
	runtime     model.Runtime
	pool        model.GatewayPool
	service     model.Service
	domain      model.Domain
	route       model.Route
	certificate model.Certificate
	gateway     model.Gateway
	now         time.Time
}

// These persisted contract fixtures are not Docker, ACME or NGINX acceptance.
func newReadinessFixture(t *testing.T, composeSource ...string) *readinessFixture {
	t.Helper()
	c, runtime, pool := controlFixture(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	c.now = func() time.Time { return now }
	if _, _, err := c.store.ApplyRuntimeSnapshot(t.Context(), model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: 1, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	runtime, err := c.store.GetRuntime(t.Context(), runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]\n"
	if len(composeSource) > 0 {
		compose = composeSource[0]
	}
	app, op, _, err := c.InstallApplication(t.Context(), InstallApplicationInput{ApplicationPreflightInput: ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: compose}, Name: "Readiness", ProjectName: "readiness", IdempotencyKey: "readiness-install"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := c.store.ClaimDeploymentOperation(t.Context(), "fixture-worker", now, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claimed.Status = model.OperationSucceeded
	if err = c.store.FinishDeploymentOperation(t.Context(), &claimed, now); err != nil {
		t.Fatal(err)
	}
	app.ActiveRevisionID, app.Status = op.RevisionID, model.ApplicationRunning
	if err = c.store.UpdateApplication(t.Context(), &app, app.Revision); err != nil {
		t.Fatal(err)
	}
	runtime.LastSeenAt = &now
	if err = c.store.UpdateRuntime(t.Context(), &runtime, runtime.Revision); err != nil {
		t.Fatal(err)
	}
	services, err := c.store.ListAllServicesFiltered(t.Context(), storage.ServiceFilter{ApplicationID: app.ID})
	if err != nil || len(services) != 1 {
		t.Fatal(services, err)
	}
	service := services[0]
	service.DesiredInstances, service.RunningInstances, service.RuntimeReadyInstances = 1, 1, 1
	service.LastSeenAt, service.RuntimeObjectID = &now, "immutable-runtime-object"
	if err = c.store.UpsertService(t.Context(), &service); err != nil {
		t.Fatal(err)
	}
	if _, _, err = c.store.ApplyRuntimeSnapshot(t.Context(), model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: runtime.AppliedObservation + 1, ObservedAt: now, Services: []model.DiscoveredService{readinessDiscoveredService(service)}}); err != nil {
		t.Fatal(err)
	}
	service, err = c.store.GetService(t.Context(), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = c.store.GetRuntime(t.Context(), runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, after := now.Add(-time.Hour), now.Add(time.Hour)
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Status: model.CertificateIssued, Fingerprint: strings.Repeat("c", 64), PrimaryDomain: "ready.example.test", Domains: []string{"ready.example.test"}, NotBefore: &before, NotAfter: &after, EncryptedPrivateKey: []byte("not-serialized-private-material"), Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = c.store.CreateCertificate(t.Context(), &certificate); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "ready.example.test", Enabled: true, HTTPEnabled: true, HTTPSEnabled: true, TLSMode: model.TLSCustom, CertificateID: certificate.ID, Source: model.SourceAPI, Status: model.DomainReady, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = c.store.CreateDomain(t.Context(), &domain); err != nil {
		t.Fatal(err)
	}
	route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: service.ID, Path: "/app", Kind: "proxy", Enabled: true, Status: model.RouteActive, Source: model.SourceAPI, UpstreamProtocol: "http", Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = c.store.CreateRoute(t.Context(), &route); err != nil {
		t.Fatal(err)
	}
	pool, err = c.store.GetPool(t.Context(), pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Enabled: true, Status: model.GatewayReady, DesiredRevision: pool.DesiredRevision, AppliedRevision: pool.DesiredRevision, ModelDigest: strings.Repeat("a", 64), RenderedDigest: strings.Repeat("b", 64), LastSeenAt: &now, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err = c.store.UpsertGateway(t.Context(), &gateway); err != nil {
		t.Fatal(err)
	}
	f := &readinessFixture{c: c, app: app, op: op, runtime: runtime, pool: pool, service: service, domain: domain, route: route, certificate: certificate, gateway: gateway, now: now}
	f.evidence(t, GatewayEvidence{ConfirmedRevision: pool.DesiredRevision, LoadingConfirmed: true, StartupState: "CONFIRMED", ModelDigest: gateway.ModelDigest, RenderedDigest: gateway.RenderedDigest, DiskDigest: gateway.RenderedDigest, RefreshIntervalMillis: 5000, ObservedAt: now})
	return f
}

func readinessDiscoveredService(service model.Service) model.DiscoveredService {
	return model.DiscoveredService{ServiceKey: service.ServiceKey, RuntimeObjectID: service.RuntimeObjectID, ExternalID: service.ExternalID,
		Name: service.Name, Namespace: service.Namespace, QualifiedName: service.QualifiedName, DNSName: service.DNSName, DNSStatus: service.DNSStatus,
		Protocol: service.DiscoveredProtocol, TargetPort: service.DiscoveredPort, Enabled: service.DiscoveredEnabled,
		DesiredInstances: service.DesiredInstances, RunningInstances: service.RunningInstances, ReadyInstances: service.RuntimeReadyInstances,
		Readiness: service.Readiness, RoutingEligible: service.RoutingEligible, RuntimeCreatedAt: service.RuntimeCreatedAt, RuntimeUpdatedAt: service.RuntimeUpdatedAt}
}

func TestReadinessRetainsPersistedReplicaIntentAfterPartialComposeDisappearance(t *testing.T) {
	f := newReadinessFixture(t)
	op, err := f.c.QueueServiceOperation(t.Context(), f.service.ID, "SCALE", 3, "persist-three", ServiceOperationOptions{Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := f.c.store.GetApplicationRevision(t.Context(), op.RevisionID)
	if err != nil || revision.ConfigurationSource.ServiceScope == nil || revision.ConfigurationSource.ServiceScope.RequestedReplicas == nil || *revision.ConfigurationSource.ServiceScope.RequestedReplicas != 3 {
		t.Fatal("production scale did not freeze three replicas", revision, err)
	}
	claimed, err := f.c.store.ClaimDeploymentOperation(t.Context(), "scale-fixture", f.now, f.now.Add(-time.Minute))
	if err != nil || claimed.ID != op.ID {
		t.Fatal(claimed, err)
	}
	claimed.Status = model.OperationSucceeded
	if err = f.c.store.FinishDeploymentOperation(t.Context(), &claimed, f.now); err != nil {
		t.Fatal(err)
	}
	app, err := f.c.store.GetApplication(t.Context(), f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	app.ActiveRevisionID = revision.ID
	if err = f.c.store.UpdateApplication(t.Context(), &app, app.Revision); err != nil {
		t.Fatal(err)
	}
	observed := readinessDiscoveredService(f.service)
	// The real Compose provider counts only currently observed containers, so
	// losing one of three leaves equal observed desired/running/ready counts.
	observed.DesiredInstances, observed.RunningInstances, observed.ReadyInstances = 2, 2, 2
	if _, _, err = f.c.store.ApplyRuntimeSnapshot(t.Context(), model.RuntimeSnapshot{RuntimeID: f.runtime.ID, Observation: f.runtime.AppliedObservation + 1, ObservedAt: f.now, Services: []model.DiscoveredService{observed}}); err != nil {
		t.Fatal(err)
	}
	before, err := f.c.store.GetDeploymentOperation(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.c.DeploymentReadiness(t.Context(), op.ID)
	if err != nil || v.stage("workload").State != ReadinessPending || v.stage("workload").ErrorCode != "workload_converging" {
		t.Fatal("observed two healthy containers concealed requested three", v.stage("workload"), err)
	}
	after, err := f.c.store.GetDeploymentOperation(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("readiness rewrote succeeded scale history", err)
	}
	observed.DesiredInstances, observed.RunningInstances, observed.ReadyInstances = 3, 3, 3
	if _, _, err = f.c.store.ApplyRuntimeSnapshot(t.Context(), model.RuntimeSnapshot{RuntimeID: f.runtime.ID, Observation: f.runtime.AppliedObservation + 2, ObservedAt: f.now, Services: []model.DiscoveredService{observed}}); err != nil {
		t.Fatal(err)
	}
	converged, err := f.c.DeploymentReadiness(t.Context(), op.ID)
	if err != nil || converged.stage("workload").State != ReadinessConfirmed || converged.BaselineDigest != v.BaselineDigest {
		t.Fatal("satisfied same immutable intent did not converge with stable baseline", converged, err)
	}
}

func TestReadinessManagedComposeReplicaTargets(t *testing.T) {
	for _, scenario := range []string{"full requested three short", "full requested three satisfied", "scale down extra container", "default one", "override short", "override satisfied", "native health pending", "unknown negative override", "unknown missing revision", "unknown missing intent", "unknown decrypted malformed intent", "unknown unmatched service", "unknown missing image", "unknown digest", "unknown nonreplicated mode", "unknown ambiguous scope", "zero present", "zero absent", "stopped no intent", "superseded revision", "unmanaged native counts"} {
		t.Run(scenario, func(t *testing.T) {
			count, observedCount := 3, 2
			want := ReadinessPending
			switch scenario {
			case "full requested three satisfied", "unmanaged native counts":
				observedCount, want = 3, ReadinessConfirmed
			case "scale down extra container":
				count, observedCount = 2, 3
			case "default one":
				count, observedCount, want = 1, 1, ReadinessConfirmed
			case "override short":
				count, observedCount = 1, 2
			case "override satisfied":
				count, observedCount, want = 1, 3, ReadinessConfirmed
			case "zero present":
				count, observedCount, want = 0, 0, ReadinessConfirmed
			case "zero absent", "stopped no intent":
				count, observedCount, want = 0, 0, ReadinessUnknown
			}
			compose := fmt.Sprintf("services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]\n    deploy: {replicas: %d}\n", count)
			if scenario == "default one" {
				compose = "services:\n  web:\n    image: example.test/web:fixed\n    expose: [8080]\n"
			}
			f := newReadinessFixture(t, compose)
			op, err := f.c.store.GetDeploymentOperation(t.Context(), f.op.ID)
			if err != nil {
				t.Fatal(err)
			}
			app, err := f.c.store.GetApplication(t.Context(), f.app.ID)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := f.c.store.GetApplicationRevision(t.Context(), op.RevisionID)
			if err != nil {
				t.Fatal(err)
			}
			observed := readinessDiscoveredService(f.service)
			observed.DesiredInstances, observed.RunningInstances, observed.ReadyInstances = observedCount, observedCount, observedCount
			if scenario == "native health pending" {
				observed.DesiredInstances, observed.RunningInstances, observed.ReadyInstances = 3, 3, 2
			}
			snapshot := model.RuntimeSnapshot{RuntimeID: f.runtime.ID, Observation: f.runtime.AppliedObservation + 1, ObservedAt: f.now, Services: []model.DiscoveredService{observed}}
			if scenario == "zero absent" || scenario == "stopped no intent" {
				snapshot.Services = nil
			}
			if _, _, err = f.c.store.ApplyRuntimeSnapshot(t.Context(), snapshot); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "override short", "override satisfied":
				app.ReplicaOverrides = map[string]int{"web": 3}
			case "unknown negative override":
				app.ReplicaOverrides = map[string]int{"web": -1}
			case "unknown missing revision":
				op.RevisionID = ""
			case "unknown missing intent", "stopped no intent":
				revision.EncryptedResolved = nil
			case "unknown decrypted malformed intent", "unknown unmatched service", "unknown missing image", "unknown nonreplicated mode":
				invalid := "services: ["
				if scenario == "unknown unmatched service" {
					invalid = "services: {other: {image: example.test/other:fixed}}"
				} else if scenario == "unknown missing image" {
					invalid = "services: {web: {deploy: {replicas: 3}}}"
				} else if scenario == "unknown nonreplicated mode" {
					invalid = "services: {web: {image: example.test/web:fixed, deploy: {mode: global}}}"
				}
				revision.EncryptedResolved, err = f.c.keyring.Encrypt([]byte(invalid), revisionAAD(revision.ID, "resolved"))
				if err != nil {
					t.Fatal(err)
				}
				revision.ResolvedSHA256 = digestString(invalid)
			case "unknown digest":
				revision.ResolvedSHA256 = strings.Repeat("0", 64)
			case "unknown ambiguous scope":
				requested := 3
				op.Type = "UPGRADE"
				revision.ConfigurationSource.ServiceScope = &model.ServiceDeploymentScope{Names: []string{"web", "other"}, RequestedReplicas: &requested}
			case "superseded revision":
				app.ActiveRevisionID = "a-newer-immutable-revision"
				want = ReadinessUnknown
			case "unmanaged native counts":
				service, e := f.c.store.GetService(t.Context(), f.service.ID)
				if e != nil {
					t.Fatal(e)
				}
				service.Managed = false
				revision.EncryptedResolved = nil
				f.c.store = readinessOverrideStore{Store: f.c.store, services: []model.Service{service}}
			}
			if strings.HasPrefix(scenario, "unknown") {
				want = ReadinessUnknown
			}
			if scenario == "stopped no intent" {
				op.Type, want = "STOP", ReadinessConfirmed
			}
			f.c.store = readinessOverrideStore{Store: f.c.store, application: &app, operation: &op, revision: &revision}
			v, err := f.c.DeploymentReadiness(t.Context(), op.ID)
			if err != nil || v.stage("workload").State != want {
				t.Fatal("managed replica target was inferred from surviving containers", v.stage("workload"), err)
			}
		})
	}
}

func TestReadinessTemporaryScaleUsesValidatedInputForComposeAndSwarm(t *testing.T) {
	for _, runtimeType := range []model.RuntimeType{model.RuntimeDocker, model.RuntimeSwarm} {
		for _, scenario := range []string{"short", "satisfied", "extra", "zero", "input missing", "input malformed", "input null", "input negative", "input out of range", "input foreign service"} {
			t.Run(string(runtimeType)+"/"+scenario, func(t *testing.T) {
				f := newReadinessFixture(t)
				count, observedCount, want := 3, 2, ReadinessPending
				if scenario == "satisfied" {
					observedCount, want = 3, ReadinessConfirmed
				} else if scenario == "extra" {
					count, observedCount = 2, 3
				} else if scenario == "zero" {
					count, observedCount, want = 0, 0, ReadinessConfirmed
				}
				op, err := f.c.QueueServiceOperation(t.Context(), f.service.ID, "SCALE", count, "temporary-scale")
				if err != nil {
					t.Fatal(err)
				}
				op.Status = model.OperationSucceeded
				observed := readinessDiscoveredService(f.service)
				observed.DesiredInstances, observed.RunningInstances, observed.ReadyInstances = observedCount, observedCount, observedCount
				if _, _, err = f.c.store.ApplyRuntimeSnapshot(t.Context(), model.RuntimeSnapshot{RuntimeID: f.runtime.ID, Observation: f.runtime.AppliedObservation + 1, ObservedAt: f.now, Services: []model.DiscoveredService{observed}}); err != nil {
					t.Fatal(err)
				}
				runtime, err := f.c.store.GetRuntime(t.Context(), f.runtime.ID)
				if err != nil {
					t.Fatal(err)
				}
				runtime.Type = runtimeType // A dialect-independent read contract, not Swarm startup acceptance.
				if strings.HasPrefix(scenario, "input") {
					want = ReadinessUnknown
					input := `{"replicas":3,"service_key":"` + f.service.ServiceKey + `"}`
					switch scenario {
					case "input missing":
						op.EncryptedInput = nil
					case "input malformed":
						input = `{"replicas":`
					case "input null":
						input = `{"replicas":null}`
					case "input negative":
						input = `{"replicas":-1}`
					case "input out of range":
						input = `{"replicas":10001}`
					case "input foreign service":
						input = `{"replicas":3,"service_key":"foreign/web"}`
					}
					if scenario != "input missing" {
						op.EncryptedInput, err = f.c.keyring.Encrypt([]byte(input), operationAAD(op.ID))
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				f.c.store = readinessOverrideStore{Store: f.c.store, operation: &op, runtime: &runtime}
				v, err := f.c.DeploymentReadiness(t.Context(), op.ID)
				if err != nil || v.stage("workload").State != want {
					t.Fatal("temporary scale lost exact immutable request", v.stage("workload"), err)
				}
			})
		}
	}
}

func TestReadinessReplicaTargetChangesBaselineButObservationDoesNot(t *testing.T) {
	f := newReadinessFixture(t)
	before, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.c.store.GetApplication(t.Context(), f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	app.ReplicaOverrides = map[string]int{"web": 2}
	f.c.store = readinessOverrideStore{Store: f.c.store, application: &app}
	changed, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
	if err != nil || changed.BaselineDigest == before.BaselineDigest || changed.stage("workload").State != ReadinessPending {
		t.Fatal("changed effective target reused former client baseline", changed, err)
	}
	runtime := f.runtime
	runtime.Status = model.RuntimeOffline
	f.c.store = readinessOverrideStore{Store: f.c.store, runtime: &runtime}
	stale, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
	if err != nil || stale.BaselineDigest != changed.BaselineDigest || stale.stage("workload").State != ReadinessUnknown {
		t.Fatal("transient Runtime state changed effective replica target", stale, err)
	}
}

func (f *readinessFixture) evidence(t *testing.T, evidence GatewayEvidence) {
	t.Helper()
	if err := f.c.RecordGatewayEvidence(t.Context(), f.gateway.ID, evidence); err != nil {
		t.Fatal(err)
	}
}

type readinessOverrideStore struct {
	storage.Store
	operation       *model.DeploymentOperation
	application     *model.Application
	runtime         *model.Runtime
	services        []model.Service
	certificate     *model.Certificate
	gateway         *model.Gateway
	revision        *model.ApplicationRevision
	pool            *model.GatewayPool
	failure         string
	bindings        []model.DomainBinding
	useBindings     bool
	recoveryReviews []storage.ControlDocument
}

func (s readinessOverrideStore) GetApplicationRevision(ctx context.Context, key string) (model.ApplicationRevision, error) {
	if s.failure == "revision" {
		return model.ApplicationRevision{}, storage.ErrNotFound
	}
	if s.revision != nil {
		return *s.revision, nil
	}
	return s.Store.GetApplicationRevision(ctx, key)
}
func (s readinessOverrideStore) GetPool(ctx context.Context, key string) (model.GatewayPool, error) {
	if s.failure == "pool" {
		return model.GatewayPool{}, errors.New("private-storage-diagnostic")
	}
	if s.pool != nil {
		return *s.pool, nil
	}
	return s.Store.GetPool(ctx, key)
}
func (s readinessOverrideStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if (s.failure == "recovery-barrier" && key == storage.RecoveryBarrierID) || (s.failure == "recovery-hold" && strings.HasPrefix(key, "recovery-hold:")) {
		return storage.ControlDocument{}, errors.New("private-recovery-storage-diagnostic")
	}
	if (s.failure == "sandbox" && strings.HasSuffix(key, ":sandbox")) || (s.failure == "evidence" && strings.HasSuffix(key, ":evidence")) || (s.failure == "entrypoint" && strings.HasSuffix(key, ":entrypoint")) {
		return storage.ControlDocument{}, errors.New("private-storage-diagnostic")
	}
	if s.failure == "presence" && strings.HasSuffix(key, ":presence") {
		return storage.ControlDocument{}, errors.New("private-inventory-storage-diagnostic")
	}
	return s.Store.GetControlDocument(ctx, key)
}

func (s readinessOverrideStore) ListControlDocuments(ctx context.Context, kind, parent string, page storage.Page) ([]storage.ControlDocument, error) {
	if kind == "recovery-review" {
		if s.failure == "recovery-history" {
			return nil, errors.New("private-recovery-storage-diagnostic")
		}
		if s.recoveryReviews != nil {
			return s.recoveryReviews, nil
		}
	}
	return s.Store.ListControlDocuments(ctx, kind, parent, page)
}

func (s readinessOverrideStore) GetDeploymentOperation(ctx context.Context, key string) (model.DeploymentOperation, error) {
	if s.failure == "operation" {
		return model.DeploymentOperation{}, errors.New("private-storage-diagnostic")
	}
	if s.operation != nil {
		return *s.operation, nil
	}
	return s.Store.GetDeploymentOperation(ctx, key)
}
func (s readinessOverrideStore) GetApplication(ctx context.Context, key string) (model.Application, error) {
	if s.failure == "application" {
		return model.Application{}, errors.New("private-storage-diagnostic")
	}
	if s.application != nil {
		return *s.application, nil
	}
	return s.Store.GetApplication(ctx, key)
}
func (s readinessOverrideStore) GetRuntime(ctx context.Context, key string) (model.Runtime, error) {
	if s.failure == "runtime" {
		return model.Runtime{}, errors.New("private-storage-diagnostic")
	}
	if s.runtime != nil {
		return *s.runtime, nil
	}
	return s.Store.GetRuntime(ctx, key)
}
func (s readinessOverrideStore) ListAllServicesFiltered(ctx context.Context, filter storage.ServiceFilter) ([]model.Service, error) {
	if s.failure == "services" {
		return nil, errors.New("private-storage-diagnostic")
	}
	if s.services != nil {
		return s.services, nil
	}
	return s.Store.ListAllServicesFiltered(ctx, filter)
}
func (s readinessOverrideStore) ListServiceDomainBindings(ctx context.Context, key string) ([]model.DomainBinding, error) {
	if s.failure == "bindings" {
		return nil, errors.New("private-storage-diagnostic")
	}
	if s.useBindings {
		return s.bindings, nil
	}
	return s.Store.ListServiceDomainBindings(ctx, key)
}
func (s readinessOverrideStore) GetCertificate(ctx context.Context, key string) (model.Certificate, error) {
	if s.failure == "certificate" {
		return model.Certificate{}, storage.ErrNotFound
	}
	if s.certificate != nil {
		return *s.certificate, nil
	}
	return s.Store.GetCertificate(ctx, key)
}
func (s readinessOverrideStore) GetGateway(ctx context.Context, key string) (model.Gateway, error) {
	if s.gateway != nil && key == s.gateway.ID {
		return *s.gateway, nil
	}
	return s.Store.GetGateway(ctx, key)
}
func (s readinessOverrideStore) ListGateways(ctx context.Context, page storage.Page) ([]model.Gateway, error) {
	if s.failure == "gateways" {
		return nil, errors.New("private-storage-diagnostic")
	}
	values, err := s.Store.ListGateways(ctx, page)
	for i := range values {
		if s.gateway != nil && values[i].ID == s.gateway.ID {
			values[i] = *s.gateway
		}
	}
	return values, err
}

func TestReadinessSeparatesStoredResultIssuanceLoadingAndClientAccess(t *testing.T) {
	f := newReadinessFixture(t)
	v, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"accepted", "workload", "certificate", "gateway"} {
		if v.stage(name).State != ReadinessConfirmed {
			t.Fatalf("%s: %#v", name, v.stage(name))
		}
	}
	if v.stage("client_access").State != ReadinessUnknown || v.stage("client_access").ErrorCode != "client_not_verified" || len(v.GatewayTargets) != 1 || len(v.AccessTargets) != 1 {
		t.Fatal("loading was confused with client access", v)
	}
	if v.AccessTargets[0].Host != f.domain.Host || v.AccessTargets[0].Scheme != "https" || v.AccessTargets[0].Port != 443 || v.AccessTargets[0].Path != "/app" || v.AccessTargets[0].CertificateFingerprint != f.certificate.Fingerprint || !readinessDigest(v.BaselineDigest) {
		t.Fatal(v)
	}
	before, _ := f.c.store.GetDeploymentOperation(t.Context(), f.op.ID)
	f.now = f.now.Add(time.Second)
	f.c.now = func() time.Time { return f.now }
	f.evidence(t, GatewayEvidence{ConfirmedRevision: f.pool.DesiredRevision, LoadingConfirmed: true, StartupState: "CONFIRMED", ModelDigest: f.gateway.ModelDigest, RenderedDigest: f.gateway.RenderedDigest, DiskDigest: f.gateway.RenderedDigest, RefreshIntervalMillis: 5000, ObservedAt: f.now})
	current, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
	if err != nil || current.BaselineDigest != v.BaselineDigest || current.ObservedAt.Equal(v.ObservedAt) {
		t.Fatal("heartbeat changed configuration identity", current, err)
	}
	after, _ := f.c.store.GetDeploymentOperation(t.Context(), f.op.ID)
	if before.Status != after.Status || before.Revision != after.Revision {
		t.Fatal("GET changed operation history")
	}
	raw, _ := json.Marshal(current)
	if strings.Contains(string(raw), "private-material") || strings.Contains(string(raw), "unix:") {
		t.Fatal("readiness exposed confidential source")
	}
}

func TestReadinessRecoveryHoldAndReleasePreserveExecutionAndCertificateHistory(t *testing.T) {
	for _, scope := range []string{"global", "operation", "inspection mode"} {
		t.Run(scope, func(t *testing.T) {
			f := newReadinessFixture(t)
			before, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil {
				t.Fatal(err)
			}
			operation, err := f.c.store.GetDeploymentOperation(t.Context(), f.op.ID)
			if err != nil {
				t.Fatal(err)
			}
			certificate, err := f.c.store.GetCertificate(t.Context(), f.certificate.ID)
			if err != nil {
				t.Fatal(err)
			}
			hold := storage.ControlDocument{ID: storage.RecoveryHoldID("deployment", f.op.ID), Kind: "recovery-hold", Payload: json.RawMessage(`{}`), CreatedAt: f.now, UpdatedAt: f.now}
			if scope == "global" {
				hold.ID, hold.Kind = storage.RecoveryBarrierID, "recovery-barrier"
			}
			if scope == "inspection mode" {
				f.c.config.Controller.RecoveryMode = true
			} else if err := f.c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: hold}}); err != nil {
				t.Fatal(err)
			}
			held, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil || held.stage("accepted").State != ReadinessConfirmed || held.BaselineDigest == before.BaselineDigest {
				t.Fatal("recovery hold reused a normal baseline or changed durable acceptance", err)
			}
			for _, name := range []string{"workload", "certificate", "gateway", "client_access"} {
				if held.stage(name).State != ReadinessUnknown || held.stage(name).ErrorCode != "recovery_held" {
					t.Fatalf("held %s reused inherited evidence: %#v", name, held.stage(name))
				}
			}
			if len(held.GatewayTargets) != 1 || held.GatewayTargets[0].State != ReadinessUnknown || len(held.AccessTargets) != 1 || held.AccessTargets[0].CertificateFingerprint != certificate.Fingerprint {
				t.Fatal("held readiness lost target identity or retained a Gateway confirmation", held)
			}
			cutover := f.now.Add(250 * time.Millisecond)
			writes := []storage.ControlWrite{{Document: storage.ControlDocument{ID: "recovery:review:released", Kind: "recovery-review", Payload: json.RawMessage(`{}`), CreatedAt: cutover, UpdatedAt: cutover}}}
			if scope == "inspection mode" {
				f.c.config.Controller.RecoveryMode = false
			} else {
				document, err := f.c.store.GetControlDocument(t.Context(), hold.ID)
				if err != nil {
					t.Fatal(err)
				}
				writes = append(writes, storage.ControlWrite{Document: document, Expected: document.Revision, Remove: true})
			}
			if err := f.c.store.WriteControlDocuments(t.Context(), writes); err != nil {
				t.Fatal(err)
			}
			f.now = f.now.Add(time.Second)
			released, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil || released.BaselineDigest == held.BaselineDigest || released.BaselineDigest == before.BaselineDigest || released.stage("workload").State != ReadinessUnknown || released.stage("gateway").State != ReadinessUnknown || released.stage("certificate").State != ReadinessConfirmed {
				t.Fatal("release reused historical observations or required certificate reissuance", released, err)
			}
			afterOperation, err := f.c.store.GetDeploymentOperation(t.Context(), f.op.ID)
			if err != nil || !reflect.DeepEqual(afterOperation, operation) {
				t.Fatal("readiness rewrote historic execution state", err)
			}
			afterCertificate, err := f.c.store.GetCertificate(t.Context(), certificate.ID)
			if err != nil || !reflect.DeepEqual(afterCertificate, certificate) {
				t.Fatal("readiness changed the valid certificate or private material", err)
			}
		})
	}
}

func TestReadinessRequiresObservationsAfterTheRecoveryReviewCutover(t *testing.T) {
	for _, scenario := range []string{"all inherited", "service inherited", "Gateway inherited", "Gateway receipt inherited", "Gateway observation inherited", "Gateway heartbeat inherited", "Gateway confirmation inherited", "equal cutover", "all observed after release", "running heartbeat inherited", "running heartbeat after release"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReadinessFixture(t)
			cutover := f.now.Add(250 * time.Millisecond)
			if err := f.c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: "recovery:review:cutover", Kind: "recovery-review", Payload: json.RawMessage(`{}`), CreatedAt: cutover, UpdatedAt: cutover}}}); err != nil {
				t.Fatal(err)
			}
			inherited := f.now
			f.now = f.now.Add(time.Second)
			service, gateway := f.service, f.gateway
			if scenario != "all inherited" {
				// Persist a current authoritative Runtime observation. Restore the
				// selected fixture service counts separately to isolate its timestamp.
				if _, _, err := f.c.store.ApplyRuntimeSnapshot(t.Context(), model.RuntimeSnapshot{RuntimeID: f.runtime.ID, Observation: f.runtime.ObservationEpoch + 1, ObservedAt: f.now, Services: []model.DiscoveredService{readinessDiscoveredService(service)}}); err != nil {
					t.Fatal(err)
				}
				if scenario != "service inherited" {
					service.LastSeenAt = &f.now
				}
				if err := f.c.store.UpsertService(t.Context(), &service); err != nil {
					t.Fatal(err)
				}
			}
			pool, err := f.c.store.GetPool(t.Context(), f.pool.ID)
			if err != nil {
				t.Fatal(err)
			}
			gateway.DesiredRevision, gateway.AppliedRevision = pool.DesiredRevision, pool.DesiredRevision
			document, err := f.c.store.GetControlDocument(t.Context(), gateway.ID+":evidence")
			if err != nil {
				t.Fatal(err)
			}
			var evidence GatewayEvidence
			if err := json.Unmarshal(document.Payload, &evidence); err != nil {
				t.Fatal(err)
			}
			if scenario != "all inherited" && scenario != "Gateway inherited" {
				gateway.LastSeenAt = &f.now
				evidence.ObservedAt, evidence.ReceivedAt, evidence.LastConfirmedAt = f.now, f.now, &f.now
			}
			evidence.ConfirmedRevision, evidence.LastConfirmedRevision = pool.DesiredRevision, pool.DesiredRevision
			switch scenario {
			case "Gateway receipt inherited":
				evidence.ReceivedAt = inherited
			case "Gateway observation inherited":
				evidence.ObservedAt = inherited
			case "Gateway heartbeat inherited":
				gateway.LastSeenAt = &inherited
			case "Gateway confirmation inherited":
				evidence.LastConfirmedAt = &inherited
			case "equal cutover":
				service.LastSeenAt, gateway.LastSeenAt = &cutover, &cutover
				evidence.ObservedAt, evidence.ReceivedAt, evidence.LastConfirmedAt = cutover, cutover, &cutover
				if err := f.c.store.UpsertService(t.Context(), &service); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.c.store.UpsertGateway(t.Context(), &gateway); err != nil {
				t.Fatal(err)
			}
			document.Payload, err = json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: document, Expected: document.Revision}}); err != nil {
				t.Fatal(err)
			}
			wantWorkload, wantGateway := ReadinessConfirmed, ReadinessConfirmed
			if scenario == "all inherited" || scenario == "service inherited" || scenario == "equal cutover" {
				wantWorkload = ReadinessUnknown
			}
			if strings.HasPrefix(scenario, "Gateway ") || scenario == "all inherited" || scenario == "equal cutover" {
				wantGateway = ReadinessUnknown
			}
			if strings.HasPrefix(scenario, "running heartbeat") {
				op, err := f.c.store.GetDeploymentOperation(t.Context(), f.op.ID)
				if err != nil {
					t.Fatal(err)
				}
				op.Status, op.HeartbeatAt = model.OperationRunning, &inherited
				wantWorkload = ReadinessUnknown
				if scenario == "running heartbeat after release" {
					op.HeartbeatAt, wantWorkload = &f.now, ReadinessPending
				}
				f.c.store = readinessOverrideStore{Store: f.c.store, operation: &op}
			}
			v, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil || v.stage("workload").State != wantWorkload || v.stage("gateway").State != wantGateway || v.stage("certificate").State != ReadinessConfirmed || v.stage("client_access").State != ReadinessUnknown {
				t.Fatal("cutover mixed inherited and current facts", v, err)
			}
			if len(v.GatewayTargets) != 1 || v.GatewayTargets[0].State != wantGateway {
				t.Fatal("individual target concealed its recovery observation boundary", v.GatewayTargets)
			}
		})
	}
}

func TestReadinessRecoveryObservationFailureCannotConfirmInheritedState(t *testing.T) {
	for _, failure := range []string{"recovery-barrier", "recovery-hold", "recovery-history", "incomplete review"} {
		t.Run(failure, func(t *testing.T) {
			f := newReadinessFixture(t)
			store := readinessOverrideStore{Store: f.c.store, failure: failure}
			if failure == "incomplete review" {
				store.recoveryReviews = []storage.ControlDocument{{ID: "recovery:review:incomplete", Kind: "recovery-review"}}
			}
			f.c.store = store
			v, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil || v.stage("accepted").State != ReadinessConfirmed {
				t.Fatal("recovery observation failure erased acceptance", err)
			}
			for _, name := range []string{"workload", "certificate", "gateway", "client_access"} {
				if v.stage(name).State != ReadinessUnknown || v.stage(name).ErrorCode != "recovery_state_unavailable" {
					t.Fatal("unavailable recovery state reused historical confirmation", name, v)
				}
			}
			if len(v.GatewayTargets) != 1 || v.GatewayTargets[0].State != ReadinessUnknown {
				t.Fatal("unavailable recovery state retained a confirmed target")
			}
			raw, _ := json.Marshal(v)
			if strings.Contains(string(raw), "private-recovery-storage-diagnostic") {
				t.Fatal("recovery storage diagnostics escaped through a viewer response")
			}
		})
	}
}

func TestReadinessRecoveryCutoverUsesLatestCreatedAtAcrossHistoryPages(t *testing.T) {
	f := newReadinessFixture(t)
	writes := make([]storage.ControlWrite, 0, 201)
	for i := 0; i <= 200; i++ {
		created := f.now.Add(-time.Hour)
		if i == 200 {
			created = f.now.Add(250 * time.Millisecond)
		}
		writes = append(writes, storage.ControlWrite{Document: storage.ControlDocument{ID: fmt.Sprintf("recovery:review:%03d", i), Kind: "recovery-review", Payload: json.RawMessage(`{}`), CreatedAt: created, UpdatedAt: created}})
	}
	if err := f.c.store.WriteControlDocuments(t.Context(), writes); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Second)
	v, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
	if err != nil || v.stage("workload").State != ReadinessUnknown || v.stage("gateway").State != ReadinessUnknown {
		t.Fatal("a review beyond the first history page was ignored", v, err)
	}
	// ID order alone cannot determine the release boundary.
	newest := f.now.Add(250 * time.Millisecond)
	if err := f.c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: "recovery:review:000-before-id", Kind: "recovery-review", Payload: json.RawMessage(`{}`), CreatedAt: newest, UpdatedAt: newest}}}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Second)
	after, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
	if err != nil || after.BaselineDigest == v.BaselineDigest {
		t.Fatal("latest CreatedAt did not change the cutover baseline", err)
	}
}

func TestReadinessEvidenceBoundaries(t *testing.T) {
	for _, scenario := range []string{"queued", "running", "stalled", "failed", "rolled back", "rollback failed", "unsupported status", "superseded", "foreign application", "offline runtime", "stale runtime", "unapplied inventory", "stale service", "unknown service", "unhealthy service", "converging service", "pending certificate", "missing certificate", "expired certificate", "revoked certificate", "certificate error", "missing validity", "not yet valid", "invalid fingerprint", "gateway offline", "gateway unconfirmed", "gateway disk drift", "gateway mismatch", "gateway risk", "gateway incompatible", "stale gateway", "no gateways", "pending binding", "disabled binding", "no binding", "ordinary HTTP", "operation unavailable", "application unavailable", "runtime unavailable", "services unavailable", "bindings unavailable", "gateways unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReadinessFixture(t)
			op, app, rt, service, certificate, gateway := f.op, f.app, f.runtime, f.service, f.certificate, f.gateway
			actualOp, err := f.c.store.GetDeploymentOperation(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			op = actualOp
			store := readinessOverrideStore{Store: f.c.store}
			wantName, wantState := "workload", ReadinessUnknown
			past := f.now.Add(-time.Hour)
			switch scenario {
			case "queued":
				op.Status = model.OperationQueued
				wantState = ReadinessPending
			case "running":
				op.Status = model.OperationRunning
				op.HeartbeatAt = &f.now
				wantState = ReadinessPending
			case "stalled":
				op.Status = model.OperationRunning
				op.HeartbeatAt = &past
			case "failed":
				op.Status = model.OperationFailed
				wantState = ReadinessFailed
			case "rolled back":
				op.Status = model.OperationRolledBack
				wantState = ReadinessFailed
			case "rollback failed":
				op.Status = model.OperationRollbackFailed
				wantState = ReadinessFailed
			case "unsupported status":
				op.Status = "UNKNOWN"
			case "superseded":
				app.ActiveRevisionID = "new-revision"
				store.application = &app
			case "foreign application":
				app.RuntimeID = "foreign-runtime"
				store.application = &app
			case "offline runtime":
				rt.Status = model.RuntimeOffline
				store.runtime = &rt
			case "stale runtime":
				rt.LastSeenAt = &past
				store.runtime = &rt
			case "unapplied inventory":
				rt.ObservationEpoch = rt.AppliedObservation + 1
				store.runtime = &rt
			case "stale service":
				service.LastSeenAt = &past
				store.services = []model.Service{service}
			case "unknown service":
				store.services = []model.Service{}
			case "unhealthy service":
				service.ConfigurationError = "private-configuration-value"
				store.services = []model.Service{service}
				wantState = ReadinessFailed
			case "converging service":
				service.RuntimeReadyInstances = 0
				store.services = []model.Service{service}
				wantState = ReadinessPending
			case "pending certificate":
				certificate.Status = model.CertificatePending
				store.certificate = &certificate
				wantName, wantState = "certificate", ReadinessPending
			case "missing certificate":
				store.failure = "certificate"
				wantName = "certificate"
			case "expired certificate":
				certificate.NotAfter = &past
				store.certificate = &certificate
				wantName, wantState = "certificate", ReadinessFailed
			case "revoked certificate":
				certificate.Status = model.CertificateRevoked
				store.certificate = &certificate
				wantName, wantState = "certificate", ReadinessFailed
			case "certificate error":
				certificate.Status = model.CertificateError
				store.certificate = &certificate
				wantName, wantState = "certificate", ReadinessFailed
			case "missing validity":
				certificate.NotAfter = nil
				store.certificate = &certificate
				wantName = "certificate"
			case "not yet valid":
				future := f.now.Add(time.Hour)
				certificate.NotBefore = &future
				store.certificate = &certificate
				wantName, wantState = "certificate", ReadinessFailed
			case "invalid fingerprint":
				certificate.Fingerprint = "not-a-fingerprint"
				store.certificate = &certificate
				wantName = "certificate"
			case "gateway offline":
				gateway.Status = model.GatewayOffline
				store.gateway = &gateway
				wantName = "gateway"
			case "gateway incompatible":
				gateway.Status = model.GatewayIncompatible
				store.gateway = &gateway
				wantName, wantState = "gateway", ReadinessFailed
			case "stale gateway":
				gateway.LastSeenAt = &past
				store.gateway = &gateway
				wantName = "gateway"
			case "no gateways":
				gateway.Enabled = false
				store.gateway = &gateway
				wantName = "gateway"
			case "pending binding":
				route := f.route
				route.Status = model.RoutePending
				store.bindings = []model.DomainBinding{{ID: route.ID, Service: service, Domain: f.domain, Route: route}}
				store.useBindings = true
				wantName, wantState = "gateway", ReadinessPending
			case "disabled binding":
				route := f.route
				route.Enabled = false
				store.bindings = []model.DomainBinding{{ID: route.ID, Service: service, Domain: f.domain, Route: route}}
				store.useBindings = true
				wantName, wantState = "gateway", ReadinessNotApplicable
			case "no binding":
				store.useBindings = true
				wantName, wantState = "gateway", ReadinessNotApplicable
			case "ordinary HTTP":
				domain := f.domain
				domain.HTTPSEnabled = false
				store.bindings = []model.DomainBinding{{ID: f.route.ID, Service: service, Domain: domain, Route: f.route}}
				store.useBindings = true
				wantName, wantState = "certificate", ReadinessNotApplicable
			case "operation unavailable":
				store.failure = "operation"
			case "application unavailable":
				store.failure = "application"
			case "runtime unavailable":
				store.failure = "runtime"
			case "services unavailable":
				store.failure = "services"
			case "bindings unavailable":
				store.failure = "bindings"
				wantName = "gateway"
			case "gateways unavailable":
				store.failure = "gateways"
				wantName = "gateway"
			case "gateway unconfirmed", "gateway disk drift", "gateway mismatch", "gateway risk":
				f.now = f.now.Add(time.Second)
				f.c.now = func() time.Time { return f.now }
				e := GatewayEvidence{ConfirmedRevision: f.pool.DesiredRevision, LoadingConfirmed: true, StartupState: "CONFIRMED", ModelDigest: gateway.ModelDigest, RenderedDigest: gateway.RenderedDigest, DiskDigest: gateway.RenderedDigest, RefreshIntervalMillis: 5000, ObservedAt: f.now}
				switch scenario {
				case "gateway unconfirmed":
					e.LoadingConfirmed = false
				case "gateway disk drift":
					e.DiskDigest = strings.Repeat("d", 64)
				case "gateway mismatch":
					e.ConfirmedRevision++
				case "gateway risk":
					e.StartupState = "RISK"
				}
				f.evidence(t, e)
				wantName, wantState = "gateway", ReadinessPending
				if scenario == "gateway disk drift" || scenario == "gateway risk" {
					wantState = ReadinessFailed
				}
			}
			store.operation = &op
			f.c.store = store
			v, err := f.c.DeploymentReadiness(t.Context(), op.ID)
			if scenario == "operation unavailable" {
				if err == nil {
					t.Fatal("storage failure hidden")
				}
				return
			}
			if err != nil || v.stage(wantName).State != wantState {
				t.Fatalf("%s: want %s, got %#v error %v", scenario, wantState, v, err)
			}
			if v.stage("accepted").State != ReadinessConfirmed {
				t.Fatal("observation changed durable acceptance")
			}
			if v.stage("client_access").State == ReadinessConfirmed {
				t.Fatal("platform claimed actual client access")
			}
			raw, _ := json.Marshal(v)
			if strings.Contains(string(raw), "private-configuration-value") || strings.Contains(string(raw), "private-storage-diagnostic") {
				t.Fatal("raw diagnostic leaked")
			}
		})
	}
}

func TestReadinessReportsPartialGatewayResultsAndDetectsNewTargetBaseline(t *testing.T) {
	f := newReadinessFixture(t)
	before, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
	if err != nil {
		t.Fatal(err)
	}
	other := f.gateway
	other.ID = id.New(id.Gateway)
	other.Status = model.GatewayOffline
	if err = f.c.store.UpsertGateway(t.Context(), &other); err != nil {
		t.Fatal(err)
	}
	v, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
	if err != nil || len(v.GatewayTargets) != 2 || v.stage("gateway").State != ReadinessUnknown || v.BaselineDigest == before.BaselineDigest {
		t.Fatal("partial load concealed or baseline reused", v, err)
	}
	confirmed := 0
	for _, target := range v.GatewayTargets {
		if target.State == ReadinessConfirmed {
			confirmed++
		}
	}
	if confirmed != 1 {
		t.Fatal("independent target result lost", v.GatewayTargets)
	}
}

func TestReadinessScopesLifecycleAndDeferredEntrypointEvidence(t *testing.T) {
	for _, scenario := range []string{"non-workload", "stopped", "stop still exiting", "removed", "detach", "service selected", "foreign service", "scoped selected", "scoped absent", "revision deleted", "foreign revision", "entrypoint pending", "entrypoint failed", "entrypoint unknown", "explicit gateway", "explicit gateway absent", "domain attach", "domain attach unknown", "foreign binding", "certificate pending without ID", "certificate renewal", "HTTP-only path", "pool unavailable", "pool disabled", "unpublished pool", "sandbox unavailable", "gateway evidence unavailable", "certificate replacement", "runtime replacement", "multiple routes"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReadinessFixture(t)
			op, err := f.c.store.GetDeploymentOperation(t.Context(), f.op.ID)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := f.c.store.GetApplicationRevision(t.Context(), op.RevisionID)
			if err != nil {
				t.Fatal(err)
			}
			before, err := f.c.DeploymentReadiness(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			store := readinessOverrideStore{Store: f.c.store, operation: &op}
			wantName, wantState := "gateway", ReadinessConfirmed
			switch scenario {
			case "non-workload":
				op.ApplicationID = ""
				wantName, wantState = "workload", ReadinessNotApplicable
			case "stopped":
				op.Type = "STOP"
				store.services = []model.Service{}
				wantName, wantState = "workload", ReadinessConfirmed
			case "stop still exiting":
				op.Type = "STOP"
				wantName, wantState = "workload", ReadinessPending
			case "removed":
				op.Type = "REMOVE_RUNTIME"
				service := f.service
				service.RunningInstances = 0
				store.services = []model.Service{service}
				wantName, wantState = "workload", ReadinessConfirmed
			case "detach":
				op.Type = "DOMAIN_DETACH"
				wantState = ReadinessNotApplicable
			case "service selected":
				op.ServiceID = f.service.ID
				other := f.service
				other.ID = "other-service"
				store.services = []model.Service{other, f.service}
			case "foreign service":
				other := f.service
				other.RuntimeID = "foreign"
				store.services = []model.Service{other}
				wantName, wantState = "workload", ReadinessUnknown
			case "scoped selected":
				revision.ConfigurationSource.ServiceScope = &model.ServiceDeploymentScope{Names: []string{"web"}}
				store.revision = &revision
			case "scoped absent":
				revision.ConfigurationSource.ServiceScope = &model.ServiceDeploymentScope{Names: []string{"missing"}}
				store.revision = &revision
				wantName, wantState = "workload", ReadinessUnknown
			case "revision deleted":
				store.failure = "revision"
				wantName, wantState = "workload", ReadinessUnknown
			case "foreign revision":
				revision.ApplicationID = "foreign"
				store.revision = &revision
				wantName, wantState = "workload", ReadinessUnknown
			case "entrypoint pending", "entrypoint failed", "entrypoint unknown":
				revision.ConfigurationSource.Entrypoint = &model.ServiceEntrypoint{Host: f.domain.Host, Service: "web", GatewayID: f.gateway.ID, CertificateID: f.certificate.ID, Port: 8080}
				store.revision = &revision
				store.useBindings = true
				wantState = ReadinessPending
				if scenario == "entrypoint failed" {
					if err = f.c.saveControl(t.Context(), op.ID+":entrypoint", "service-entrypoint", op.ID, ServiceEntrypointStatus{Status: "FAILED"}, 0); err != nil {
						t.Fatal(err)
					}
					wantState = ReadinessFailed
				}
				if scenario == "entrypoint unknown" {
					store.failure = "entrypoint"
					wantState = ReadinessUnknown
				}
			case "explicit gateway", "explicit gateway absent":
				revision.ConfigurationSource.Entrypoint = &model.ServiceEntrypoint{Host: f.domain.Host, Service: "web", GatewayID: f.gateway.ID, Port: 8080}
				store.revision = &revision
				if scenario == "explicit gateway absent" {
					revision.ConfigurationSource.Entrypoint.GatewayID = "missing-gateway"
					wantState = ReadinessUnknown
				}
			case "domain attach", "domain attach unknown":
				op.Type, op.ServiceID, op.RevisionID = "DOMAIN_ATTACH", f.service.ID, ""
				routeID := f.route.ID
				if scenario == "domain attach unknown" {
					routeID = "missing-route"
					wantState = ReadinessUnknown
				}
				op.EncryptedInput, err = f.c.keyring.Encrypt([]byte(`{"route_id":"`+routeID+`"}`), operationAAD(op.ID))
				if err != nil {
					t.Fatal(err)
				}
			case "foreign binding":
				route := f.route
				route.ServiceID = "foreign"
				store.useBindings = true
				store.bindings = []model.DomainBinding{{Service: f.service, Domain: f.domain, Route: route}}
				wantState = ReadinessUnknown
			case "certificate pending without ID":
				domain := f.domain
				domain.CertificateID = ""
				store.useBindings = true
				store.bindings = []model.DomainBinding{{Service: f.service, Domain: domain, Route: f.route}}
				wantName, wantState = "certificate", ReadinessPending
			case "certificate renewal":
				certificate := f.certificate
				certificate.Status = model.CertificateRenewing
				store.certificate = &certificate
				wantName = "certificate"
			case "HTTP-only path":
				domain := f.domain
				domain.HTTPSEnabled = false
				route := f.route
				route.Path = ""
				store.useBindings = true
				store.bindings = []model.DomainBinding{{Service: f.service, Domain: domain, Route: route}}
				wantName, wantState = "certificate", ReadinessNotApplicable
			case "pool unavailable":
				store.failure = "pool"
				wantState = ReadinessUnknown
			case "pool disabled":
				pool := f.pool
				pool.Enabled = false
				store.pool = &pool
				wantState = ReadinessUnknown
			case "unpublished pool":
				pool := f.pool
				pool.DesiredRevision = 0
				store.pool = &pool
				wantState = ReadinessUnknown
			case "sandbox unavailable":
				store.failure = "sandbox"
				wantState = ReadinessUnknown
			case "gateway evidence unavailable":
				store.failure = "evidence"
				wantState = ReadinessUnknown
			case "certificate replacement":
				certificate := f.certificate
				certificate.Fingerprint = strings.Repeat("d", 64)
				certificate.Revision++
				store.certificate = &certificate
			case "runtime replacement":
				service := f.service
				service.RuntimeObjectID = "different-runtime-object"
				store.services = []model.Service{service}
			case "multiple routes":
				route := f.route
				route.ID = "another-route"
				route.Path = "/second"
				store.useBindings = true
				store.bindings = []model.DomainBinding{{Service: f.service, Domain: f.domain, Route: route}, {Service: f.service, Domain: f.domain, Route: f.route}}
			}
			f.c.store = store
			v, err := f.c.DeploymentReadiness(t.Context(), op.ID)
			if err != nil || v.stage(wantName).State != wantState {
				t.Fatalf("%s: want %s, got %#v error %v", scenario, wantState, v, err)
			}
			if scenario == "certificate replacement" || scenario == "runtime replacement" {
				if v.BaselineDigest == before.BaselineDigest {
					t.Fatal("changed target identity retained old baseline")
				}
			}
			if scenario == "multiple routes" && len(v.AccessTargets) != 2 {
				t.Fatal("independent access targets lost")
			}
			if scenario == "HTTP-only path" && (v.AccessTargets[0].Scheme != "http" || v.AccessTargets[0].Port != 80 || v.AccessTargets[0].Path != "/") {
				t.Fatal(v.AccessTargets)
			}
		})
	}
}

func TestReadinessRequiresExactFreshGatewayLoadingTuple(t *testing.T) {
	for _, scenario := range []string{"desired revision", "applied revision", "model disagreement", "rendered disagreement", "missing model", "missing rendered", "missing evidence model", "missing evidence rendered", "missing disk", "confirmation revision", "confirmation history", "confirmation time", "unknown startup", "expired evidence", "future evidence", "future gateway", "apply failure", "disabled target"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReadinessFixture(t)
			gateway := f.gateway
			doc, err := f.c.store.GetControlDocument(t.Context(), gateway.ID+":evidence")
			if err != nil {
				t.Fatal(err)
			}
			var evidence GatewayEvidence
			if err = json.Unmarshal(doc.Payload, &evidence); err != nil {
				t.Fatal(err)
			}
			want := ReadinessPending
			switch scenario {
			case "desired revision":
				gateway.DesiredRevision++
			case "applied revision":
				gateway.AppliedRevision--
			case "model disagreement":
				gateway.ModelDigest = strings.Repeat("d", 64)
				want = ReadinessUnknown
			case "rendered disagreement":
				gateway.RenderedDigest = strings.Repeat("d", 64)
				want = ReadinessUnknown
			case "missing model":
				gateway.ModelDigest = ""
				want = ReadinessUnknown
			case "missing rendered":
				gateway.RenderedDigest = ""
				want = ReadinessUnknown
			case "missing evidence model":
				evidence.ModelDigest = ""
				want = ReadinessUnknown
			case "missing evidence rendered":
				evidence.RenderedDigest = ""
				want = ReadinessFailed
			case "missing disk":
				evidence.DiskDigest = ""
				want = ReadinessUnknown
			case "confirmation revision":
				evidence.ConfirmedRevision++
			case "confirmation history":
				evidence.LastConfirmedRevision++
				want = ReadinessUnknown
			case "confirmation time":
				evidence.LastConfirmedAt = nil
				want = ReadinessUnknown
			case "unknown startup":
				evidence.StartupState = "UNKNOWN"
				want = ReadinessUnknown
			case "expired evidence":
				evidence.ReceivedAt = f.now.Add(-time.Minute)
				want = ReadinessUnknown
			case "future evidence":
				evidence.ReceivedAt = f.now.Add(time.Minute)
				want = ReadinessUnknown
			case "future gateway":
				future := f.now.Add(time.Minute)
				gateway.LastSeenAt = &future
				want = ReadinessUnknown
			case "apply failure":
				gateway.Status = model.GatewayApplyFailed
				want = ReadinessFailed
			case "disabled target":
				gateway.Enabled = false
				want = ReadinessUnknown
			}
			doc.Payload, err = json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: doc, Expected: doc.Revision}}); err != nil {
				t.Fatal(err)
			}
			f.c.store = readinessOverrideStore{Store: f.c.store, gateway: &gateway}
			v, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil || v.stage("gateway").State != want {
				t.Fatalf("%s: got %#v expected %s error %v", scenario, v, want, err)
			}
			if v.stage("workload").State != ReadinessConfirmed || v.stage("certificate").State != ReadinessConfirmed {
				t.Fatal("loading failure erased independent confirmed stages")
			}
		})
	}
}

func TestReadinessSeparatesAuthoritativeAbsenceFromPresentSwarmZeroReplicas(t *testing.T) {
	for _, scenario := range []string{"Compose disappeared", "Swarm disappeared", "Swarm present zero", "STOP disappeared", "DOWN disappeared", "REMOVE_RUNTIME disappeared"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReadinessFixture(t)
			before, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(scenario, "Swarm") {
				// This persisted contract fixture exercises dialect-independent
				// readiness policy; real Swarm startup uses the shared-store E2E.
				if _, err = f.c.store.(*storage.SQLStore).DB().ExecContext(t.Context(), "UPDATE runtimes SET runtime_type=? WHERE id=?", model.RuntimeSwarm, f.runtime.ID); err != nil {
					t.Fatal(err)
				}
			}
			f.now = f.now.Add(time.Second)
			f.c.now = func() time.Time { return f.now }
			snapshot := model.RuntimeSnapshot{RuntimeID: f.runtime.ID, Observation: f.runtime.AppliedObservation + 1, ObservedAt: f.now}
			if scenario == "Swarm present zero" {
				observed := readinessDiscoveredService(f.service)
				observed.DesiredInstances, observed.RunningInstances, observed.ReadyInstances = 0, 0, 0
				snapshot.Services = []model.DiscoveredService{observed}
			}
			if _, _, err = f.c.store.ApplyRuntimeSnapshot(t.Context(), snapshot); err != nil {
				t.Fatal(err)
			}
			service, err := f.c.store.GetService(t.Context(), f.service.ID)
			if err != nil || service.Status != model.ServiceUnavailable || service.DesiredInstances != 0 || service.RunningInstances != 0 || service.RuntimeReadyInstances != 0 || service.RuntimeObjectID != f.service.RuntimeObjectID || !service.CreatedAt.Equal(f.service.CreatedAt) {
				t.Fatal("fixture did not preserve history while observing zero replicas", err)
			}
			want := ReadinessUnknown
			if scenario == "Swarm present zero" {
				want = ReadinessConfirmed
			} else if strings.HasPrefix(scenario, "STOP") || strings.HasPrefix(scenario, "DOWN") || strings.HasPrefix(scenario, "REMOVE_RUNTIME") {
				op, err := f.c.store.GetDeploymentOperation(t.Context(), f.op.ID)
				if err != nil {
					t.Fatal(err)
				}
				op.Type = strings.Fields(scenario)[0]
				f.c.store = readinessOverrideStore{Store: f.c.store, operation: &op}
				want = ReadinessConfirmed
			}
			v, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil || v.stage("workload").State != want {
				t.Fatal("fresh zero counts concealed authoritative membership", v.stage("workload"), err)
			}
			if strings.HasSuffix(scenario, "disappeared") && strings.HasPrefix(scenario, "Compose") && v.BaselineDigest == before.BaselineDigest {
				t.Fatal("disappeared workload retained the former target baseline")
			}
		})
	}
}

func TestReadinessPresenceMustBeCurrentAvailableAndStructurallyValid(t *testing.T) {
	for _, scenario := range []string{"missing", "unavailable", "malformed", "wrong Runtime", "zero epoch", "older epoch", "different timestamp", "future timestamp", "absent membership", "removed marker", "removed status", "nonmanaged service", "new polling epoch", "stale service", "future service", "service configuration error"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReadinessFixture(t)
			before, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := f.c.store.GetControlDocument(t.Context(), storage.RuntimePresenceID(f.runtime.ID))
			if err != nil {
				t.Fatal(err)
			}
			var presence storage.RuntimePresence
			if err = json.Unmarshal(doc.Payload, &presence); err != nil {
				t.Fatal(err)
			}
			want := ReadinessUnknown
			write := storage.ControlWrite{Document: doc, Expected: doc.Revision}
			switch scenario {
			case "missing":
				write.Remove = true
			case "unavailable":
				f.c.store = readinessOverrideStore{Store: f.c.store, failure: "presence"}
			case "wrong Runtime":
				presence.RuntimeID = "replacement-runtime"
			case "zero epoch":
				presence.Observation = 0
			case "older epoch":
				presence.Observation--
			case "different timestamp":
				presence.ObservedAt = f.now.Add(-time.Second)
			case "future timestamp":
				presence.ObservedAt = f.now.Add(time.Minute)
			case "absent membership":
				presence.ServiceIDs = []string{}
			case "removed marker", "removed status", "nonmanaged service":
				service := f.service
				service.RemovedAt = &f.now
				if scenario == "removed status" {
					service.RemovedAt, service.Status = nil, model.ServiceRemoved
				}
				if scenario == "nonmanaged service" {
					service.RemovedAt, service.Managed = nil, false
					want = ReadinessConfirmed
				}
				f.c.store = readinessOverrideStore{Store: f.c.store, services: []model.Service{service}}
			case "new polling epoch":
				f.now = f.now.Add(time.Second)
				f.c.now = func() time.Time { return f.now }
				if _, _, err = f.c.store.ApplyRuntimeSnapshot(t.Context(), model.RuntimeSnapshot{RuntimeID: f.runtime.ID, Observation: f.runtime.AppliedObservation + 1, ObservedAt: f.now, Services: []model.DiscoveredService{readinessDiscoveredService(f.service)}}); err != nil {
					t.Fatal(err)
				}
				want = ReadinessConfirmed
			case "stale service", "future service", "service configuration error":
				service := f.service
				stale, future := f.now.Add(-time.Hour), f.now.Add(time.Minute)
				service.LastSeenAt = &stale
				if scenario == "future service" {
					service.LastSeenAt = &future
				} else if scenario == "service configuration error" {
					service.LastSeenAt, service.ConfigurationError = f.service.LastSeenAt, "private configuration diagnostic"
					want = ReadinessFailed
				}
				f.c.store = readinessOverrideStore{Store: f.c.store, services: []model.Service{service}}
			}
			if scenario != "unavailable" && scenario != "new polling epoch" {
				write.Document.Payload, err = json.Marshal(presence)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "malformed" {
					write.Document.Payload = json.RawMessage(`true`)
				}
				if err = f.c.store.WriteControlDocuments(t.Context(), []storage.ControlWrite{write}); err != nil {
					t.Fatal(err)
				}
			}
			v, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil || v.stage("workload").State != want {
				t.Fatal("invalid presence evidence confirmed a managed workload", v.stage("workload"), err)
			}
			if (scenario == "new polling epoch" || scenario == "stale service" || scenario == "future service" || scenario == "service configuration error") && v.BaselineDigest != before.BaselineDigest {
				t.Fatal("transient inventory observations invalidated a stable target baseline")
			}
			if scenario == "absent membership" && v.BaselineDigest == before.BaselineDigest {
				t.Fatal("changed inventory membership retained an old target baseline")
			}
		})
	}
}

func TestReadinessBaselineIgnoresTransientOperationAndRuntimeState(t *testing.T) {
	for _, scenario := range []string{"runtime stale", "runtime offline", "runtime unapplied", "queued", "running current", "running stale", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newReadinessFixture(t)
			before, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil {
				t.Fatal(err)
			}
			store := readinessOverrideStore{Store: f.c.store}
			runtime := f.runtime
			op, err := f.c.store.GetDeploymentOperation(t.Context(), f.op.ID)
			if err != nil {
				t.Fatal(err)
			}
			stale := f.now.Add(-time.Hour)
			want := ReadinessUnknown
			switch scenario {
			case "runtime stale":
				runtime.LastSeenAt = &stale
				store.runtime = &runtime
			case "runtime offline":
				runtime.Status = model.RuntimeOffline
				store.runtime = &runtime
			case "runtime unapplied":
				runtime.ObservationEpoch++
				store.runtime = &runtime
			case "queued":
				op.Status = model.OperationQueued
				store.operation, want = &op, ReadinessPending
			case "running current":
				op.Status, op.HeartbeatAt = model.OperationRunning, &f.now
				store.operation, want = &op, ReadinessPending
			case "running stale":
				op.Status, op.HeartbeatAt = model.OperationRunning, &stale
				store.operation = &op
			case "failed":
				op.Status = model.OperationFailed
				store.operation, want = &op, ReadinessFailed
			}
			f.c.store = store
			v, err := f.c.DeploymentReadiness(t.Context(), f.op.ID)
			if err != nil || v.stage("workload").State != want || v.BaselineDigest != before.BaselineDigest {
				t.Fatal("transient execution/runtime observations changed configuration identity", v.stage("workload"), err)
			}
		})
	}
}
