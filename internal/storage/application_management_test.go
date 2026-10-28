package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func managedRuntimeFixture(t *testing.T, store *SQLStore) (context.Context, model.Runtime, time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: id.New(id.Pool), BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: id.New(id.Runtime), Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtime); err != nil {
		t.Fatal(err)
	}
	return ctx, runtime, now
}

func TestServiceStableKeySurvivesRuntimeObjectReplacement(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	firstID := id.New(id.Service)
	first := model.Service{ID: firstID, RuntimeID: runtime.ID, ServiceKey: "demo_api", RuntimeObjectID: "old-object", ExternalID: "old-object", Name: "api", Namespace: "demo", DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, RoutingEligible: true, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &first); err != nil {
		t.Fatal(err)
	}
	replacement := first
	replacement.ID = id.New(id.Service)
	replacement.RuntimeObjectID = "new-object"
	replacement.ExternalID = "new-object"
	replacement.UpdatedAt = now.Add(time.Second)
	if err := store.UpsertService(ctx, &replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.ID != firstID {
		t.Fatalf("stable upsert changed service id: got %s want %s", replacement.ID, firstID)
	}
	stored, err := store.GetService(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RuntimeObjectID != "new-object" || stored.ServiceKey != "demo_api" {
		t.Fatalf("replacement was not associated with stable service: %#v", stored)
	}
}

func TestServiceObservationKeepsETagUntilRuntimeIdentityChanges(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "demo/web", RuntimeObjectID: "original",
		ExternalID: "original", Name: "web", Namespace: "demo", DiscoveredProtocol: "http", DiscoveredPort: 80,
		Managed: true, DesiredInstances: 1, RunningInstances: 1, ReadyInstances: 1,
		Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	observed := service
	observed.RunningInstances = 0
	observed.ReadyInstances = 0
	observed.Status = model.ServiceUnavailable
	observed.UpdatedAt = now.Add(time.Second)
	if err := store.UpsertService(ctx, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Revision != service.Revision {
		t.Fatalf("health observation expired the Service ETag: %d -> %d", service.Revision, observed.Revision)
	}
	if err := store.MarkServiceRuntimeRemoved(ctx, service.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	removed, err := store.GetService(ctx, service.ID)
	if err != nil || removed.RemovedAt == nil {
		t.Fatalf("removal marker missing: %#v, %v", removed, err)
	}
	observed.UpdatedAt = now.Add(3 * time.Second)
	if err := store.UpsertService(ctx, &observed); err != nil {
		t.Fatal(err)
	}
	retained, err := store.GetService(ctx, service.ID)
	if err != nil || retained.RemovedAt == nil {
		t.Fatalf("old runtime heartbeat cleared removal marker: %#v, %v", retained, err)
	}
	observed.RuntimeObjectID = "redeployed"
	observed.ExternalID = "redeployed"
	observed.UpdatedAt = now.Add(4 * time.Second)
	if err := store.UpsertService(ctx, &observed); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.GetService(ctx, service.ID)
	if err != nil || recovered.RemovedAt != nil || recovered.Revision != retained.Revision+1 {
		t.Fatalf("redeployment did not clear removal marker and advance identity ETag: %#v, %v", recovered, err)
	}
}

func TestManagedSwarmServiceKeepsRevisionTargetPort(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	runtime.Type = model.RuntimeSwarm
	if _, err := store.DB().ExecContext(ctx, `UPDATE runtimes SET runtime_type=? WHERE id=?`, model.RuntimeSwarm, runtime.ID); err != nil {
		t.Fatal(err)
	}
	readiness := model.ReadinessPolicy{Mode: model.ReadinessAuto, Interval: time.Second, Timeout: time.Second,
		SuccessThreshold: 2, FailureThreshold: 3, StartupTimeout: time.Minute, TCPFallback: true}
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "demo_web", ExternalID: "demo_web", Managed: true,
		Name: "web", Namespace: "demo", QualifiedName: "demo_web", DNSName: "demo_web", DNSStatus: model.DNSReady,
		DiscoveredProtocol: "http", DiscoveredPort: 8080,
		Readiness:     readiness,
		RoutingIssues: []string{"awaiting runtime discovery"}, Status: model.ServiceUnavailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	observation, err := store.NextObservation(ctx, runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: observation, ObservedAt: now.Add(time.Second), Services: []model.DiscoveredService{{
		ServiceKey: "demo_web", RuntimeObjectID: "swarm-object", ExternalID: "demo_web", Name: "demo_web", Namespace: "swarm",
		QualifiedName: "demo_web", DNSName: "demo_web", DNSStatus: model.DNSReady,
		Protocol: "http", TargetPort: 0, Enabled: false, RoutingEligible: false,
		RoutingIssues: []string{"nsc.port is required when the service does not expose exactly one TCP target port"},
		Readiness:     readiness, DesiredInstances: 1, RunningInstances: 1, ReadyInstances: 1,
		Endpoints: []model.DiscoveredEndpoint{{ExternalID: "swarm-object:vip", Address: "10.50.0.2", Port: 0, RuntimeState: model.EndpointStarting, HealthState: model.HealthUnknown, Weight: 1}},
	}}}
	changed, revision, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if changed || revision != 0 {
		t.Fatalf("restoring the saved Swarm port changed Gateway config: changed=%t revision=%d", changed, revision)
	}
	stored, err := store.GetService(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := store.ListEndpoints(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DiscoveredPort != 8080 || !stored.RoutingEligible || len(stored.RoutingIssues) != 0 || stored.ReadyInstances != 0 ||
		len(endpoints) != 1 || endpoints[0].Port != 8080 || endpoints[0].RuntimeState != model.EndpointStarting {
		t.Fatalf("saved Swarm port was lost: service=%#v endpoints=%#v", stored, endpoints)
	}
	plan, err := store.BuildProbePlan(ctx, runtime.PoolID)
	if err != nil || len(plan.Targets) != 1 || plan.Targets[0].Port != 8080 || plan.Revision != 0 {
		t.Fatalf("restored Swarm endpoint missing from probe plan: plan=%#v err=%v", plan, err)
	}
}

func TestManagedServiceDisappearanceKeepsDNSRouteAndConfigRevision(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "hello/web", ExternalID: "hello/web",
		ApplicationID: "", Managed: true, Name: "web", Namespace: "hello", QualifiedName: "hello_web", DNSName: "hello_web",
		DNSStatus: model.DNSReady, RoutingEligible: true, DiscoveredProtocol: "http", DiscoveredPort: 80, DiscoveredEnabled: true,
		Readiness:        model.ReadinessPolicy{Mode: model.ReadinessTCP, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute},
		DesiredInstances: 2, RunningInstances: 2, ReadyInstances: 2, Status: model.ServiceAvailable,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: runtime.PoolID, Host: "hello.example.com", HTTPEnabled: true,
		TLSMode: model.TLSDisabled, Status: model.DomainReady, Source: model.SourceAPI, Enabled: true,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: service.ID, Path: "/", TargetPort: 80,
		UpstreamProtocol: "http", Enabled: true, Status: model.RouteActive, Source: model.SourceAPI,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateServiceDomainBinding(ctx, &domain, nil, &route, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	configRevision := currentPoolRevision(t, store, runtime.PoolID)
	before, err := store.BuildSnapshot(ctx, runtime.PoolID)
	if err != nil || len(before.Routes) != 1 || before.Services[service.ID].DNSName != "hello_web" {
		t.Fatalf("initial managed route snapshot=%#v err=%v", before, err)
	}
	beforeService, err := store.GetService(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}

	observation, err := store.NextObservation(ctx, runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed, revision, err := store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: observation, ObservedAt: now.Add(time.Second)})
	if err != nil || changed || revision != configRevision {
		t.Fatalf("managed service disappearance changed config: changed=%t revision=%d want=%d err=%v", changed, revision, configRevision, err)
	}
	stored, err := store.GetService(ctx, service.ID)
	if err != nil || stored.ID != service.ID || stored.Status != model.ServiceUnavailable || stored.DNSName != "hello_web" || stored.Revision != beforeService.Revision {
		t.Fatalf("managed placeholder was not retained: service=%#v err=%v", stored, err)
	}
	observation, err = store.NextObservation(ctx, runtime.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed, revision, err = store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: observation, ObservedAt: now.Add(2 * time.Second)}); err != nil || changed || revision != configRevision {
		t.Fatalf("repeated missing observation changed config: changed=%t revision=%d err=%v", changed, revision, err)
	}
	again, err := store.GetService(ctx, service.ID)
	if err != nil || again.Revision != beforeService.Revision {
		t.Fatalf("repeated missing observation expired Service ETag: %#v, %v", again, err)
	}
	after, err := store.BuildSnapshot(ctx, runtime.PoolID)
	if err != nil || len(after.Routes) != 1 || after.Routes[0].ID != route.ID || after.Services[service.ID].DNSName != "hello_web" {
		t.Fatalf("stop/down withdrew the stable DNS route: snapshot=%#v err=%v", after, err)
	}
}

func TestCatalogVersionCannotBeMutatedByResync(t *testing.T) {
	store := newSQLiteStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := model.CatalogSource{ID: id.New(id.CatalogSource), Name: "immutable-catalog", Type: model.CatalogGit, URL: "https://catalog.example/index.json", PinnedRef: "0123456789abcdef0123456789abcdef01234567", Trusted: true, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateCatalogSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	version := model.CatalogAppVersion{ID: id.New(id.CatalogVersion), SourceID: source.ID, AppKey: "demo", Name: "Demo", Version: "1.0.0", Commit: source.PinnedRef,
		ArtifactDigest: "artifact", ComposeSHA256: "compose", ComposeYAML: "services: {}", Services: []string{"web"}, ComposeCompatible: true, SwarmCompatible: true,
		Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := store.UpsertCatalogAppVersion(ctx, &version); err != nil {
		t.Fatal(err)
	}
	same := version
	same.ID = id.New(id.CatalogVersion)
	if err := store.UpsertCatalogAppVersion(ctx, &same); err != nil || same.ID != version.ID {
		t.Fatalf("identical catalog resync was not idempotent: id=%s err=%v", same.ID, err)
	}
	changed := version
	changed.ID = id.New(id.CatalogVersion)
	changed.ComposeYAML = "services:\n  replaced: {}\n"
	if err := store.UpsertCatalogAppVersion(ctx, &changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("catalog version was mutable: %v", err)
	}
}

func TestCatalogIndexSyncRollsBackEveryVersionOnConflict(t *testing.T) {
	store := newSQLiteStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := model.CatalogSource{ID: id.New(id.CatalogSource), Name: "atomic-catalog", Type: model.CatalogGit,
		URL: "https://catalog.example/index.json", PinnedRef: "0123456789abcdef0123456789abcdef01234567",
		Trusted: true, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateCatalogSource(ctx, &source); err != nil {
		t.Fatal(err)
	}
	existing := model.CatalogAppVersion{ID: id.New(id.CatalogVersion), SourceID: source.ID, AppKey: "demo", Name: "Demo", Version: "1.0.0",
		Commit: source.PinnedRef, ArtifactDigest: "artifact-v1", ComposeSHA256: "compose-v1", ComposeYAML: "services: {}",
		ComposeCompatible: true, SwarmCompatible: true, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := store.UpsertCatalogAppVersion(ctx, &existing); err != nil {
		t.Fatal(err)
	}
	insertedBeforeConflict := existing
	insertedBeforeConflict.ID = id.New(id.CatalogVersion)
	insertedBeforeConflict.Version = "2.0.0"
	insertedBeforeConflict.ArtifactDigest = "artifact-v2"
	insertedBeforeConflict.ComposeSHA256 = "compose-v2"
	conflicting := existing
	conflicting.ID = id.New(id.CatalogVersion)
	conflicting.ComposeYAML = "services:\n  mutated: {}\n"
	syncedAt := now.Add(time.Minute)
	update := source
	update.LastSyncedAt = &syncedAt
	update.UpdatedAt = syncedAt
	if err := store.SyncCatalogAppVersions(ctx, &update, []model.CatalogAppVersion{insertedBeforeConflict, conflicting}, source.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("immutable conflict was accepted: %v", err)
	}
	if _, err := store.GetCatalogAppVersion(ctx, insertedBeforeConflict.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial catalog version survived rollback: %v", err)
	}
	storedSource, err := store.GetCatalogSource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedSource.Revision != source.Revision || storedSource.LastSyncedAt != nil {
		t.Fatalf("failed index was published on the source: %#v", storedSource)
	}
}

func TestDeploymentOperationIdempotencySerializationAndRecovery(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	applicationID := id.New(id.Application)
	first := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: applicationID, RuntimeID: runtime.ID, Type: "INSTALL", Status: model.OperationQueued, IdempotencyKey: "request-1", RequestSHA256: "same-request", Revision: 1, CreatedAt: now, UpdatedAt: now}
	created, err := store.CreateDeploymentOperation(ctx, &first)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.ID = id.New(id.Operation)
	duplicate.Type = "REDEPLOY" // generated fields do not affect a matching request digest
	got, err := store.CreateDeploymentOperation(ctx, &duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.Type != "INSTALL" {
		t.Fatalf("idempotency key created another operation: %#v", got)
	}
	conflicting := duplicate
	conflicting.ID = id.New(id.Operation)
	conflicting.RequestSHA256 = "different-request"
	if _, err = store.CreateDeploymentOperation(ctx, &conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("idempotency key accepted different request: %v", err)
	}
	second := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: applicationID, RuntimeID: runtime.ID, Type: "REDEPLOY", Status: model.OperationQueued, Revision: 1, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	if _, err = store.CreateDeploymentOperation(ctx, &second); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDeploymentOperation(ctx, "worker-a", now.Add(2*time.Second), now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != first.ID || claimed.Status != model.OperationRunning || claimed.Attempt != 1 {
		t.Fatalf("unexpected first claim: %#v", claimed)
	}
	if _, err = store.ClaimDeploymentOperation(ctx, "worker-b", now.Add(3*time.Second), now.Add(-time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("parallel application operation was not serialized: %v", err)
	}
	recovered, err := store.ClaimDeploymentOperation(ctx, "worker-b", now.Add(5*time.Minute), now.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ID != first.ID || recovered.WorkerID != "worker-b" || recovered.Attempt != 2 {
		t.Fatalf("stale operation was not reclaimed: %#v", recovered)
	}
}

func TestApplicationRevisionIsAppendOnlyAndETagProtected(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	application := model.Application{ID: id.New(id.Application), RuntimeID: runtime.ID, Name: "demo", ProjectName: "demo", DesiredRevisionID: "pending", Status: model.ApplicationPending, Managed: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	revision := model.ApplicationRevision{ID: id.New(id.ApplicationRevision), ApplicationID: application.ID, Sequence: 1, ComposeSHA256: "compose", EnvironmentSHA256: "env", ResolvedSHA256: "resolved", EncryptedCompose: []byte("a"), EncryptedEnvironment: []byte("b"), EncryptedResolved: []byte("c"), EncryptionKeyID: "v1", CreatedAt: now}
	application.DesiredRevisionID = revision.ID
	operation := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: application.ID, RuntimeID: runtime.ID, RevisionID: revision.ID, Type: "INSTALL", Status: model.OperationQueued, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateApplication(ctx, &application, &revision, &operation); err != nil {
		t.Fatal(err)
	}
	secondRevision := revision
	secondRevision.ID = id.New(id.ApplicationRevision)
	secondRevision.Sequence = 2
	secondRevision.ParentRevisionID = revision.ID
	secondRevision.CreatedAt = now.Add(time.Second)
	secondOperation := operation
	secondOperation.ID = id.New(id.Operation)
	secondOperation.RevisionID = secondRevision.ID
	secondOperation.Type = "UPGRADE"
	secondOperation.CreatedAt = now.Add(time.Second)
	secondOperation.UpdatedAt = now.Add(time.Second)
	if err := store.CreateApplicationRevision(ctx, &secondRevision, &secondOperation, application.Revision, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	third := secondRevision
	third.ID = id.New(id.ApplicationRevision)
	third.Sequence = 3
	thirdOperation := secondOperation
	thirdOperation.ID = id.New(id.Operation)
	if err := store.CreateApplicationRevision(ctx, &third, &thirdOperation, application.Revision, now.Add(2*time.Second)); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale application ETag was accepted: %v", err)
	}
	values, err := store.ListApplicationRevisions(ctx, application.ID, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].ID != revision.ID || values[1].ParentRevisionID != revision.ID {
		t.Fatalf("revision history was not append-only: %#v", values)
	}
}

func TestApplicationInstallAggregateIsAtomic(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	for name, address := range map[string]string{
		"edge-v4":   "203.0.113.10",
		"edge-v6":   "2001:db8::10",
		"edge-name": "Edge.Example.COM.",
		"peer-only": "10.0.0.2:40232",
	} {
		gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: runtime.PoolID, Name: name, Address: address, Enabled: true, Status: model.GatewayReady, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.UpsertGateway(ctx, &gateway); err != nil {
			t.Fatal(err)
		}
	}
	application := model.Application{ID: id.New(id.Application), RuntimeID: runtime.ID, Name: "atomic", ProjectName: "atomic", Status: model.ApplicationPending, Managed: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	revision := model.ApplicationRevision{ID: id.New(id.ApplicationRevision), ApplicationID: application.ID, Sequence: 1, ComposeSHA256: "compose", EnvironmentSHA256: "env", ResolvedSHA256: "resolved", EncryptedCompose: []byte("a"), EncryptedEnvironment: []byte("b"), EncryptedResolved: []byte("c"), EncryptionKeyID: "v1", CreatedAt: now}
	application.DesiredRevisionID = revision.ID
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "atomic/web", ExternalID: "atomic/web", ApplicationID: application.ID, Managed: true, Name: "web", Namespace: "atomic", DiscoveredProtocol: "http", DiscoveredPort: 8080, Readiness: model.ReadinessPolicy{Mode: model.ReadinessAuto}, Status: model.ServiceUnavailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: runtime.PoolID, Host: "atomic.example.com", TLSMode: model.TLSDisabled, Status: model.DomainReady, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: service.ID, Path: "/", TargetPort: 8080, UpstreamProtocol: "http", Enabled: true, Status: model.RoutePending, Source: model.SourceAPI, Revision: 1, CreatedAt: now, UpdatedAt: now}
	operation := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: application.ID, RuntimeID: runtime.ID, RevisionID: revision.ID, Type: "INSTALL", Status: model.OperationQueued, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateApplicationInstall(ctx, &application, &revision, []model.Service{service}, &domain, nil, &route, &operation); err != nil {
		t.Fatal(err)
	}
	storedService, err := store.GetService(ctx, service.ID)
	if err != nil || storedService.ApplicationID != application.ID || !storedService.Managed {
		t.Fatalf("placeholder Service was not committed with Application: service=%#v err=%v", storedService, err)
	}
	bindings, err := store.ListServiceDomainBindings(ctx, service.ID)
	if err != nil || len(bindings) != 1 || bindings[0].Status != model.RoutePending {
		t.Fatalf("initial pending binding was not committed: bindings=%#v err=%v", bindings, err)
	}
	targets := make(map[string]string)
	for _, target := range bindings[0].DNSTargets {
		targets[target.RecordType] = target.Value
	}
	if len(targets) != 3 || targets["A"] != "203.0.113.10" || targets["AAAA"] != "2001:db8::10" || targets["CNAME"] != "edge.example.com" {
		t.Fatalf("unexpected public DNS targets: %#v", bindings[0].DNSTargets)
	}
	revisionBeforeActivation := currentPoolRevision(t, store, runtime.PoolID)
	if err = store.SetApplicationRouteStatus(ctx, application.ID, model.RouteActive, "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if revisionAfterActivation := currentPoolRevision(t, store, runtime.PoolID); revisionAfterActivation != revisionBeforeActivation+1 {
		t.Fatalf("Route activation did not create a new Pool revision: before=%d after=%d", revisionBeforeActivation, revisionAfterActivation)
	}
	bindings, err = store.ListServiceDomainBindings(ctx, service.ID)
	if err != nil || len(bindings) != 1 || bindings[0].Status != model.RouteActive {
		t.Fatalf("initial binding was not activated: bindings=%#v err=%v", bindings, err)
	}

	failedApplication := application
	failedApplication.ID = id.New(id.Application)
	failedApplication.ProjectName = "rollback"
	failedApplication.Name = "rollback"
	failedRevision := revision
	failedRevision.ID = id.New(id.ApplicationRevision)
	failedRevision.ApplicationID = failedApplication.ID
	failedApplication.DesiredRevisionID = failedRevision.ID
	failedService := service
	failedService.ID = id.New(id.Service)
	failedService.ServiceKey = "rollback/web"
	failedService.ExternalID = failedService.ServiceKey
	failedService.ApplicationID = failedApplication.ID
	failedRoute := route
	failedRoute.ID = id.New(id.Route)
	failedRoute.ServiceID = "svc_missing"
	failedDomain := domain
	failedDomain.ID = id.New(id.Domain)
	failedDomain.Host = "rollback.example.com"
	failedRoute.DomainID = failedDomain.ID
	failedOperation := operation
	failedOperation.ID = id.New(id.Operation)
	failedOperation.ApplicationID = failedApplication.ID
	failedOperation.RevisionID = failedRevision.ID
	if err = store.CreateApplicationInstall(ctx, &failedApplication, &failedRevision, []model.Service{failedService}, &failedDomain, nil, &failedRoute, &failedOperation); err == nil {
		t.Fatal("invalid aggregate install unexpectedly succeeded")
	}
	if _, err = store.GetApplication(ctx, failedApplication.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed aggregate left an Application behind: %v", err)
	}
	if _, err = store.GetService(ctx, failedService.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed aggregate left a Service behind: %v", err)
	}
}

func TestGatewayDNSRecordRejectsControlConnectionAddresses(t *testing.T) {
	for _, value := range []string{"10.0.0.2:40232", "[2001:db8::1]:40232", "https://edge.example.com", "bad_name.example.com", " edge.example.com"} {
		if recordType, target, ok := gatewayDNSRecord(value); ok {
			t.Errorf("unsafe address %q became %s %q", value, recordType, target)
		}
	}
}

func TestDomainBindingTLSAndRouteUpdateIsAtomic(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "demo/web", RuntimeObjectID: "container", ExternalID: "container", Name: "web", Namespace: "demo", DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, RoutingEligible: true, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: runtime.PoolID, Host: "web.example.com", TLSMode: model.TLSDisabled, Status: model.DomainReady, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: service.ID, Path: "/", TargetPort: 8080, UpstreamProtocol: "http", Enabled: true, Status: model.RouteActive, Source: model.SourceAPI, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateServiceDomainBinding(ctx, &domain, nil, &route, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	updatedDomain := domain
	updatedDomain.Host = "renamed.example.com"
	updatedDomain.TLSMode = model.TLSManaged
	updatedDomain.ACMEChallenge = model.ACMEHTTP01
	updatedDomain.HTTPSRedirect = true
	updatedDomain.Status = model.DomainCertPending
	updatedDomain.UpdatedAt = now.Add(time.Second)
	updatedDomain.Enabled = false
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: domain.PoolID, Provider: "acme", ACMEChallenge: model.ACMEHTTP01, Status: model.CertificatePending, PrimaryDomain: updatedDomain.Host, Domains: []string{updatedDomain.Host}, Revision: 1, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	updatedDomain.CertificateID = certificate.ID
	updatedRoute := route
	updatedRoute.Path = "/api/"
	updatedRoute.UpdatedAt = now.Add(time.Second)
	if err := store.UpdateServiceDomainBinding(ctx, service.ID, &updatedDomain, &certificate, &updatedRoute, 99, route.Revision); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale Domain revision was accepted: %v", err)
	}
	unchanged, err := store.GetRoute(ctx, route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Path != "/" {
		t.Fatalf("Route changed despite failed TLS update: %#v", unchanged)
	}
	if err = store.UpdateServiceDomainBinding(ctx, service.ID, &updatedDomain, &certificate, &updatedRoute, domain.Revision, route.Revision); err != nil {
		t.Fatal(err)
	}
	storedDomain, err := store.GetDomain(ctx, domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedRoute, err := store.GetRoute(ctx, route.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedDomain.Host != updatedDomain.Host || storedDomain.Enabled || storedDomain.TLSMode != model.TLSManaged || storedDomain.CertificateID != certificate.ID || storedRoute.Path != "/api/" {
		t.Fatalf("binding update was not committed together: domain=%#v route=%#v", storedDomain, storedRoute)
	}
}

func TestApplicationAndOperationKeyRotation(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	application := model.Application{ID: id.New(id.Application), RuntimeID: runtime.ID, Name: "rotate", ProjectName: "rotate", Status: model.ApplicationPending, Managed: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	revision := model.ApplicationRevision{ID: id.New(id.ApplicationRevision), ApplicationID: application.ID, Sequence: 1,
		ComposeSHA256: "compose", EnvironmentSHA256: "environment", ResolvedSHA256: "resolved",
		EncryptedCompose: []byte("old-compose"), EncryptedEnvironment: []byte("old-environment"), EncryptedResolved: []byte("old-resolved"), EncryptionKeyID: "old", CreatedAt: now}
	application.DesiredRevisionID = revision.ID
	operation := model.DeploymentOperation{ID: id.New(id.Operation), ApplicationID: application.ID, RuntimeID: runtime.ID, RevisionID: revision.ID,
		Type: "INSTALL", Status: model.OperationQueued, EncryptedInput: []byte("old-input"), EncryptionKeyID: "old", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateApplication(ctx, &application, &revision, &operation); err != nil {
		t.Fatal(err)
	}
	revisionCount, err := store.ReencryptApplicationRevisions(ctx, func(value model.ApplicationRevision) ([]byte, []byte, []byte, string, error) {
		if string(value.EncryptedCompose) != "old-compose" || string(value.EncryptedEnvironment) != "old-environment" || string(value.EncryptedResolved) != "old-resolved" {
			t.Fatalf("unexpected revision envelopes: %#v", value)
		}
		return []byte("new-compose"), []byte("new-environment"), []byte("new-resolved"), "new", nil
	})
	if err != nil || revisionCount != 1 {
		t.Fatalf("revision rotation count=%d err=%v", revisionCount, err)
	}
	operationCount, err := store.ReencryptDeploymentOperations(ctx, func(value model.DeploymentOperation) ([]byte, string, error) {
		if string(value.EncryptedInput) != "old-input" {
			t.Fatalf("unexpected operation envelope: %#v", value)
		}
		return []byte("new-input"), "new", nil
	})
	if err != nil || operationCount != 1 {
		t.Fatalf("operation rotation count=%d err=%v", operationCount, err)
	}
	storedRevision, err := store.GetApplicationRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedOperation, err := store.GetDeploymentOperation(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRevision.EncryptionKeyID != "new" || string(storedRevision.EncryptedEnvironment) != "new-environment" || storedOperation.EncryptionKeyID != "new" || string(storedOperation.EncryptedInput) != "new-input" {
		t.Fatalf("encrypted application state was not rotated: revision=%#v operation=%#v", storedRevision, storedOperation)
	}
}
