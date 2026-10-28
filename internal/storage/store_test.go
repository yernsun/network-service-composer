package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestBindNamedSQLAcrossDialects(t *testing.T) {
	query := "SELECT ':literal', \"quoted:column\", value::text FROM samples " +
		"WHERE id=:id AND parent_id=:id AND note='don''t :bind' AND state=:state " +
		"/* :ignored */ AND retry=? -- :comment\nLIMIT :limit"
	for _, test := range []struct {
		dialect Dialect
		want    string
	}{
		{Postgres, "SELECT ':literal', \"quoted:column\", value::text FROM samples WHERE id=$1 AND parent_id=$2 AND note='don''t :bind' AND state=$3 /* :ignored */ AND retry=$4 -- :comment\nLIMIT $5"},
		{MySQL, "SELECT ':literal', \"quoted:column\", value::text FROM samples WHERE id=? AND parent_id=? AND note='don''t :bind' AND state=? /* :ignored */ AND retry=? -- :comment\nLIMIT ?"},
		{SQLite, "SELECT ':literal', \"quoted:column\", value::text FROM samples WHERE id=? AND parent_id=? AND note='don''t :bind' AND state=? /* :ignored */ AND retry=? -- :comment\nLIMIT ?"},
	} {
		t.Run(string(test.dialect), func(t *testing.T) {
			if got := (&SQLStore{dialect: test.dialect}).bind(query); got != test.want {
				t.Fatalf("bound query = %q; want %q", got, test.want)
			}
		})
	}
}

func newSQLiteStore(t *testing.T) *SQLStore {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "nsc.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"
	store, err := Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func currentPoolRevision(t *testing.T, store *SQLStore, poolID string) int64 {
	t.Helper()
	pool, err := store.GetPool(context.Background(), poolID)
	if err != nil {
		t.Fatal(err)
	}
	return pool.DesiredRevision
}

func currentProbeRevision(t *testing.T, store *SQLStore, poolID string) int64 {
	t.Helper()
	pool, err := store.GetPool(context.Background(), poolID)
	if err != nil {
		t.Fatal(err)
	}
	return pool.ProbeRevision
}

func currentProbeTarget(t *testing.T, store *SQLStore, poolID, serviceID string) model.ProbeTarget {
	t.Helper()
	plan, err := store.BuildProbePlan(context.Background(), poolID)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range plan.Targets {
		if target.ServiceID == serviceID {
			return target
		}
	}
	t.Fatalf("service %s is missing from the probe plan: %#v", serviceID, plan)
	return model.ProbeTarget{}
}

func TestDNSChallengeMetadataRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "dns-metadata", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "dns.example.com", TLSMode: model.TLSManaged, ACMEChallenge: model.ACMEDNS01, DNSProviderRef: "cloudflare-prod", HTTPSRedirect: true, Status: model.DomainCertPending, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", ACMEChallenge: model.ACMEDNS01, DNSProviderRef: "cloudflare-prod", Status: model.CertificatePending, PrimaryDomain: domain.Host, Domains: []string{domain.Host}, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateManagedDomain(ctx, &domain, &certificate); err != nil {
		t.Fatal(err)
	}
	storedDomain, err := store.GetDomain(ctx, domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedCertificate, err := store.GetCertificate(ctx, certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedDomain.ACMEChallenge != model.ACMEDNS01 || storedDomain.DNSProviderRef != "cloudflare-prod" {
		t.Fatalf("domain DNS metadata was not persisted: %#v", storedDomain)
	}
	if storedCertificate.ACMEChallenge != model.ACMEDNS01 || storedCertificate.DNSProviderRef != "cloudflare-prod" {
		t.Fatalf("certificate DNS metadata was not persisted: %#v", storedCertificate)
	}
}

func TestSQLiteLifecycleAndSnapshot(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "local", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtime); err != nil {
		t.Fatal(err)
	}
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtime.ID, ServiceKey: "demo/api", ExternalID: "demo/api", Name: "api", Namespace: "demo",
		QualifiedName: "demo_api", DNSName: "demo_api", DNSStatus: model.DNSReady, RoutingEligible: true,
		DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true,
		Readiness: model.ReadinessPolicy{Mode: model.ReadinessTCP, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupGracePeriod: time.Second, StartupTimeout: time.Minute, TCPFallback: true}, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	endpoint := model.Endpoint{ID: id.New(id.Endpoint), ServiceID: service.ID, ExternalID: "container-1", Address: "10.0.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthHealthy, ReadyState: model.Ready, ReadinessSource: model.ReadinessTCP, Weight: 1, FirstSeenAt: now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertEndpoint(ctx, &endpoint); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "api.example.com", TLSMode: model.TLSDisabled,
		HTTPEnabled: true, Status: model.DomainReady, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateDomain(ctx, &domain); err != nil {
		t.Fatal(err)
	}
	route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: service.ID, Path: "/", UpstreamProtocol: "http", Enabled: true, Source: model.SourceAPI, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRoute(ctx, &route); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Routes) != 1 || snapshot.Services[service.ID].DNSName != "demo_api" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	pool, err = store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	pool.Name = "edge-updated"
	pool.UpdatedAt = now.Add(time.Second)
	if err := store.UpdatePool(ctx, &pool, pool.Revision); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePool(ctx, &pool, pool.Revision); err == nil {
		t.Fatal("expected stale revision conflict")
	}
}

func TestSQLiteStoreRejectsSwarmRuntime(t *testing.T) {
	store := newSQLiteStore(t)
	now := time.Now().UTC()
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "sqlite", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(context.Background(), &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "swarm", Type: model.RuntimeSwarm, Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(context.Background(), &runtimeValue); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateRuntime() error = %v, want ErrConflict", err)
	}
}

func TestRuntimePoolMoveRequiresASeparateDisabledUpdate(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := model.GatewayPool{ID: id.New(id.Pool), Name: "first", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	second := model.GatewayPool{ID: id.New(id.Pool), Name: "second", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &first); err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePool(ctx, &second); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: first.ID, Name: "movable", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: false, Status: model.RuntimeDisabled, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	runtimeValue.PoolID = second.ID
	runtimeValue.Enabled = true
	runtimeValue.Status = model.RuntimeInitializing
	if err := store.UpdateRuntime(ctx, &runtimeValue, runtimeValue.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("move-and-enable error=%v, want ErrConflict", err)
	}
	runtimeValue.Enabled = false
	runtimeValue.Status = model.RuntimeDisabled
	if err := store.UpdateRuntime(ctx, &runtimeValue, runtimeValue.Revision); err != nil {
		t.Fatalf("disabled pool move failed: %v", err)
	}
	moved, err := store.GetRuntime(ctx, runtimeValue.ID)
	if err != nil || moved.PoolID != second.ID || moved.Enabled {
		t.Fatalf("moved runtime=%#v err=%v", moved, err)
	}
}

func TestServiceDNSProbeIsIndependentFromRuntimeTaskReadiness(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "dns-edge", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "dns-runtime", Type: model.RuntimeDocker, Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessTCP, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{
		ServiceKey: "demo_service", ExternalID: "demo_service", Name: "demo_service", QualifiedName: "demo_service",
		DNSName: "demo_service", DNSStatus: model.DNSReady, RoutingEligible: true,
		Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 3, RunningInstances: 3, ReadyInstances: 1, Readiness: policy,
		Endpoints: []model.DiscoveredEndpoint{{ExternalID: "task-1", Address: "10.80.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}},
		Routes:    []model.DiscoveredRoute{{ExternalKey: "public", Host: "dns-probe.example.com", Path: "/", HTTPEnabled: true, TLSMode: model.TLSDisabled, UpstreamProtocol: "http"}},
	}}}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	services, err := store.ListServices(ctx, Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("services=%#v err=%v", services, err)
	}
	service := services[0]
	plan, err := store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 1 || plan.Targets[0].ServiceID != service.ID || plan.Targets[0].DNSName != "demo_service" {
		t.Fatalf("service DNS probe plan=%#v service=%#v err=%v", plan, service, err)
	}
	configRevision := currentPoolRevision(t, store, pool.ID)
	beforeFailure, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil || len(beforeFailure.Routes) != 1 {
		t.Fatalf("initial DNS route snapshot=%#v err=%v", beforeFailure, err)
	}
	if changed, err := store.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, Epoch: plan.Epoch, Revision: plan.Revision,
		ServiceID: service.ID, Sequence: 1, Success: false, Error: "lookup demo_service: no such host",
		ObservedAt: now.Add(time.Second), TargetToken: plan.Targets[0].TargetToken}); err != nil || !changed {
		t.Fatalf("DNS failure changed=%v err=%v", changed, err)
	}
	service, err = store.GetService(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if service.DNSStatus != model.DNSUnreachable || service.RunningInstances != 3 || service.RuntimeReadyInstances != 1 || service.ReadyInstances != 1 {
		t.Fatalf("DNS status and task status were not kept separate: %+v", service)
	}
	afterFailure, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil || len(afterFailure.Routes) != 1 || len(afterFailure.Services) != 1 ||
		afterFailure.Services[service.ID].DNSName != "demo_service" || currentPoolRevision(t, store, pool.ID) != configRevision {
		t.Fatalf("transient DNS failure withdrew or revised the route: snapshot=%#v revision=%d err=%v", afterFailure, currentPoolRevision(t, store, pool.ID), err)
	}

	snapshot.Observation = 2
	snapshot.ObservedAt = now.Add(2 * time.Second)
	snapshot.Services[0].ReadyInstances = 0
	snapshot.Services[0].Endpoints[0].RuntimeState = model.EndpointStarting
	if _, _, err = store.ApplyRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	service, err = store.GetService(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if service.RuntimeReadyInstances != 0 || service.ReadyInstances != 0 || service.Status != model.ServiceUnavailable || service.DNSStatus != model.DNSUnreachable {
		t.Fatalf("runtime reconcile corrupted DNS probe status: %+v", service)
	}
	if currentPoolRevision(t, store, pool.ID) != configRevision {
		t.Fatalf("runtime instance state advanced config revision: got %d want %d", currentPoolRevision(t, store, pool.ID), configRevision)
	}
}

func TestStoppingEndpointPreservesRunningCountAndEntersDrain(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "draining-edge", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "draining-runtime", Type: model.RuntimeDocker, Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{
		ExternalID: "service", Name: "service", Protocol: "http", TargetPort: 8080, Enabled: true,
		DesiredInstances: 1, RunningInstances: 1, Readiness: policy,
		Endpoints: []model.DiscoveredEndpoint{{ExternalID: "old-task", Address: "10.83.0.2", Port: 8080, RuntimeState: model.EndpointStopping, HealthState: model.HealthUnknown, Weight: 1}},
	}}}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	services, err := store.ListServices(ctx, Page{Limit: 10})
	if err != nil || len(services) != 1 || services[0].RunningInstances != 1 || services[0].ReadyInstances != 0 {
		t.Fatalf("service runtime counts=%#v err=%v", services, err)
	}
	endpoints, err := store.ListEndpoints(ctx, services[0].ID)
	if err != nil || len(endpoints) != 1 || endpoints[0].ReadyState != model.Draining || endpoints[0].DrainSince == nil {
		t.Fatalf("stopping endpoint did not enter drain: %#v err=%v", endpoints, err)
	}
	if _, _, err = store.SweepEndpointLifecycle(ctx, runtimeValue.ID, snapshot.Observation, "retain-ready", now.Add(time.Second), time.Minute, 0, time.Hour); err != nil {
		t.Fatal(err)
	}
	endpoints, err = store.ListEndpoints(ctx, services[0].ID)
	if err != nil || len(endpoints) != 1 || endpoints[0].ReadyState != model.Removed {
		t.Fatalf("drained endpoint was not removed: %#v err=%v", endpoints, err)
	}
	service, err := store.GetService(ctx, services[0].ID)
	if err != nil || service.RunningInstances != 1 || service.ReadyInstances != 0 {
		t.Fatalf("endpoint GC corrupted the last observed runtime counts: %#v err=%v", service, err)
	}
}

func TestRuntimeSnapshotObservationAndPoolRevision(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "local", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtime); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupGracePeriod: 0, StartupTimeout: time.Minute}
	observed := model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{
		ServiceKey: "demo/api", ExternalID: "demo/api", Name: "api", Namespace: "demo", QualifiedName: "demo_api",
		DNSName: "demo_api", DNSStatus: model.DNSReady, RoutingEligible: true,
		Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: policy,
		Endpoints: []model.DiscoveredEndpoint{{ExternalID: "container-a", Address: "10.0.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}},
		Routes:    []model.DiscoveredRoute{{ExternalKey: "main", Host: "api.example.com", Path: "/", TLSMode: model.TLSDisabled, HTTPEnabled: true, UpstreamProtocol: "http"}},
	}}}
	changed, firstRevision, err := store.ApplyRuntimeSnapshot(ctx, observed)
	if err != nil || !changed {
		t.Fatalf("first reconcile: changed=%v revision=%d err=%v", changed, firstRevision, err)
	}
	stale := observed
	stale.Services[0].Endpoints[0].Address = "10.0.0.99"
	if changed, revision, err := store.ApplyRuntimeSnapshot(ctx, stale); err != nil || changed || revision != firstRevision {
		t.Fatalf("stale observation was not ignored: changed=%v revision=%d err=%v", changed, revision, err)
	}
	observed.Services[0].Endpoints[0].Address = "10.0.0.2"
	observed.Observation = 2
	observed.ObservedAt = now.Add(time.Second)
	if changed, revision, err := store.ApplyRuntimeSnapshot(ctx, observed); err != nil || changed || revision != firstRevision {
		t.Fatalf("identical reconcile changed effective state: changed=%v revision=%d err=%v", changed, revision, err)
	}
	observed.Observation = 3
	observed.ObservedAt = now.Add(2 * time.Second)
	observed.Services[0].Endpoints[0].Address = "10.0.0.3"
	changed, thirdRevision, err := store.ApplyRuntimeSnapshot(ctx, observed)
	if err != nil || changed || thirdRevision != firstRevision {
		t.Fatalf("instance address churn changed Gateway config: changed=%v revision=%d err=%v", changed, thirdRevision, err)
	}
	snapshot, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Routes) != 1 || snapshot.Services[snapshot.Routes[0].ServiceID].DNSName != "demo_api" {
		t.Fatalf("unexpected reconciled snapshot: %#v", snapshot)
	}
}

func TestObservationEpochFencesSupersededOnlineAndOfflineUpdates(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "observation-fence", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "observation-fence", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	older, err := store.NextObservation(ctx, runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := store.NextObservation(ctx, runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed, _, applyErr := store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: older, ObservedAt: now}); applyErr != nil || changed {
		t.Fatalf("superseded online observation applied: changed=%v err=%v", changed, applyErr)
	}
	if applied, markErr := store.MarkRuntimeOffline(ctx, runtimeValue.ID, older, "stale owner", now); markErr != nil || applied {
		t.Fatalf("superseded offline observation applied: applied=%v err=%v", applied, markErr)
	}
	current, err := store.GetRuntime(ctx, runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.AppliedObservation != 0 || current.Status != model.RuntimeInitializing {
		t.Fatalf("superseded observation mutated runtime: %#v", current)
	}
	if changed, _, applyErr := store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: newer, ObservedAt: now.Add(time.Second)}); applyErr != nil || changed {
		t.Fatalf("current empty observation failed: changed=%v err=%v", changed, applyErr)
	}
	current, err = store.GetRuntime(ctx, runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.AppliedObservation != newer || current.Status != model.RuntimeOnline {
		t.Fatalf("current observation was not committed: %#v", current)
	}
	offline, err := store.NextObservation(ctx, runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if applied, markErr := store.MarkRuntimeOffline(ctx, runtimeValue.ID, offline, "current owner", now.Add(2*time.Second)); markErr != nil || !applied {
		t.Fatalf("current offline observation failed: applied=%v err=%v", applied, markErr)
	}
	if changed, _, applyErr := store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: newer, ObservedAt: now.Add(3 * time.Second)}); applyErr != nil || changed {
		t.Fatalf("old online snapshot was not fenced after offline transition: changed=%v err=%v", changed, applyErr)
	}
	current, err = store.GetRuntime(ctx, runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.AppliedObservation != offline || current.Status != model.RuntimeOffline {
		t.Fatalf("old online snapshot overwrote newer outage: %#v", current)
	}
}

func TestUnroutedDiscoveryStateDoesNotBumpPoolRevision(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "quiet-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "quiet-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{ServiceKey: "demo/internal", ExternalID: "demo/internal", Name: "internal", Namespace: "demo", QualifiedName: "demo_internal", DNSName: "demo_internal", DNSStatus: model.DNSReady, RoutingEligible: true, Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: policy, Endpoints: []model.DiscoveredEndpoint{{ExternalID: "container", Address: "10.0.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}}}}}
	changed, revision, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || changed || revision != 0 {
		t.Fatalf("unrouted service created gateway revision: changed=%v revision=%d err=%v", changed, revision, err)
	}
	snapshot.Observation = 2
	snapshot.ObservedAt = now.Add(time.Second)
	snapshot.Services[0].Endpoints[0].Address = "10.0.0.3"
	changed, revision, err = store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || changed || revision != 0 {
		t.Fatalf("unrouted endpoint update created gateway revision: changed=%v revision=%d err=%v", changed, revision, err)
	}
	snapshot.Observation = 3
	snapshot.ObservedAt = now.Add(2 * time.Second)
	snapshot.Services[0].Routes = []model.DiscoveredRoute{{ExternalKey: "public", Host: "quiet.example.com", Path: "/", TLSMode: model.TLSDisabled, UpstreamProtocol: "http"}}
	changed, revision, err = store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || !changed || revision != 1 {
		t.Fatalf("new routed state did not create one gateway revision: changed=%v revision=%d err=%v", changed, revision, err)
	}
}

func TestProbePlanHasIndependentRevisionAndUsesServiceDNS(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "probe-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "probe-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready", Scheme: "http", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 2, FailureThreshold: 3, StartupTimeout: time.Minute}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{
		ServiceKey: "demo_api", ExternalID: "demo_api", Name: "demo_api", QualifiedName: "demo_api",
		DNSName: "demo_api", DNSStatus: model.DNSReady, RoutingEligible: true,
		Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: policy,
		Endpoints: []model.DiscoveredEndpoint{{ExternalID: "container", Address: "10.70.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}},
	}}}
	changed, firstRevision, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || changed || firstRevision != 0 {
		t.Fatalf("initial probe plan: changed=%v revision=%d err=%v", changed, firstRevision, err)
	}
	plan, err := store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 1 || plan.Targets[0].DNSName != "demo_api" || plan.Revision != 1 {
		services, _ := store.ListServices(ctx, Page{Limit: 10})
		t.Fatalf("unexpected initial probe plan: %#v services=%#v err=%v", plan, services, err)
	}
	snapshot.Observation = 2
	snapshot.ObservedAt = now.Add(time.Second)
	if changed, revision, applyErr := store.ApplyRuntimeSnapshot(ctx, snapshot); applyErr != nil || changed || revision != firstRevision {
		t.Fatalf("stable probe plan changed revision: changed=%v revision=%d err=%v", changed, revision, applyErr)
	}
	snapshot.Observation = 3
	snapshot.ObservedAt = now.Add(2 * time.Second)
	snapshot.Services[0].Endpoints[0].Address = "10.70.0.3"
	changed, addressRevision, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || changed || addressRevision != firstRevision {
		t.Fatalf("probe address change: changed=%v revision=%d err=%v", changed, addressRevision, err)
	}
	if probeRevision := currentProbeRevision(t, store, pool.ID); probeRevision != plan.Revision {
		t.Fatalf("instance IP churn advanced probe revision: got %d want %d", probeRevision, plan.Revision)
	}
	snapshot.Observation = 4
	snapshot.ObservedAt = now.Add(3 * time.Second)
	snapshot.Services[0].Readiness = model.ReadinessPolicy{Mode: model.ReadinessGrace, StartupTimeout: time.Minute}
	changed, policyRevision, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || changed || policyRevision != addressRevision {
		t.Fatalf("probe policy removal: changed=%v revision=%d err=%v", changed, policyRevision, err)
	}
	plan, err = store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 0 || plan.Revision != 2 {
		t.Fatalf("removed probe remained in plan: %#v err=%v", plan, err)
	}
}

func TestInvalidDiscoveredConfigurationCannotBeEnabledByAPIOverride(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "invalid-config-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "invalid-config-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready", Scheme: "http", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	discovered := model.DiscoveredService{ServiceKey: "demo/api", ExternalID: "demo/api", Name: "api", Namespace: "demo", QualifiedName: "demo_api", DNSName: "demo_api", DNSStatus: model.DNSReady, RoutingEligible: true, Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: policy,
		Endpoints: []model.DiscoveredEndpoint{{ExternalID: "container", Address: "10.73.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}},
		Routes:    []model.DiscoveredRoute{{ExternalKey: "public", Host: "invalid.example.com", Path: "/", TLSMode: model.TLSDisabled, UpstreamProtocol: "http"}}}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{discovered}}
	changed, validRevision, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || !changed {
		t.Fatalf("valid discovery changed=%v revision=%d err=%v", changed, validRevision, err)
	}
	services, err := store.ListServices(ctx, Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("services=%#v err=%v", services, err)
	}
	service := services[0]
	enabled := true
	service.EnabledOverride = &enabled
	service.UpdatedAt = now.Add(time.Second)
	if err = store.UpdateService(ctx, &service, service.Revision); err != nil {
		t.Fatal(err)
	}

	snapshot.Observation = 2
	snapshot.ObservedAt = now.Add(2 * time.Second)
	snapshot.Services[0].ConfigurationError = "replicas have inconsistent NSC labels"
	snapshot.Services[0].Enabled = false
	changed, invalidRevision, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || !changed || invalidRevision != validRevision+1 {
		t.Fatalf("invalid transition changed=%v revision=%d valid=%d err=%v", changed, invalidRevision, validRevision, err)
	}
	gatewaySnapshot, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gatewaySnapshot.Routes) != 0 || len(gatewaySnapshot.Services) != 0 {
		t.Fatalf("invalid service leaked into gateway snapshot: %#v", gatewaySnapshot)
	}
	plan, err := store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 0 {
		t.Fatalf("invalid service leaked into probe plan: %#v err=%v", plan, err)
	}

	snapshot.Observation = 3
	snapshot.ObservedAt = now.Add(3 * time.Second)
	snapshot.Services[0].Endpoints[0].Address = "10.73.0.3"
	changed, unchangedRevision, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || changed || unchangedRevision != invalidRevision {
		t.Fatalf("invalid metadata caused data-plane churn: changed=%v revision=%d err=%v", changed, unchangedRevision, err)
	}
}

func TestReadinessOverrideSurvivesDiscoveryReconcile(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "override-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "override-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	discoveredReadiness := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/discovered", Scheme: "http", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 2, FailureThreshold: 2, StartupTimeout: time.Minute}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{
		ServiceKey: "demo_api", ExternalID: "demo_api", Name: "demo_api", QualifiedName: "demo_api",
		DNSName: "demo_api", DNSStatus: model.DNSReady, RoutingEligible: true,
		Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: discoveredReadiness,
		Endpoints: []model.DiscoveredEndpoint{{ExternalID: "container", Address: "10.71.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}},
	}}}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	services, err := store.ListServices(ctx, Page{Limit: 10})
	if err != nil || len(services) != 1 {
		t.Fatalf("services=%#v err=%v", services, err)
	}
	service := services[0]
	override := model.ReadinessPolicy{Mode: model.ReadinessTCP, Interval: 2 * time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 3, StartupTimeout: time.Minute}
	service.ReadinessOverride = &override
	service.UpdatedAt = now.Add(time.Second)
	if err = store.UpdateService(ctx, &service, service.Revision); err != nil {
		t.Fatal(err)
	}
	plan, err := store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 1 || plan.Targets[0].Policy.Mode != model.ReadinessTCP {
		t.Fatalf("override probe plan=%#v err=%v", plan, err)
	}
	endpoints, err := store.ListEndpoints(ctx, service.ID)
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("override endpoints=%#v err=%v", endpoints, err)
	}
	target := currentProbeTarget(t, store, pool.ID, service.ID)
	if changed, probeErr := store.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, Epoch: 0, Revision: currentProbeRevision(t, store, pool.ID),
		ServiceID: service.ID, Sequence: 1, Success: false, Error: "connection refused",
		ObservedAt: now.Add(1500 * time.Millisecond), TargetToken: target.TargetToken}); probeErr != nil || changed {
		t.Fatalf("override success threshold was not applied: changed=%v err=%v", changed, probeErr)
	}

	snapshot.Observation = 2
	snapshot.ObservedAt = now.Add(2 * time.Second)
	snapshot.Services[0].Readiness.Path = "/changed"
	changed, _, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || changed {
		t.Fatalf("hidden discovery readiness changed gateway state: changed=%v err=%v", changed, err)
	}
	service, err = store.GetService(ctx, service.ID)
	if err != nil || service.Readiness.Path != "/changed" || service.ReadinessOverride == nil || service.ReadinessOverride.Mode != model.ReadinessTCP {
		t.Fatalf("readiness override did not survive reconcile: %#v err=%v", service, err)
	}
	plan, err = store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 1 || plan.Targets[0].Policy.Mode != model.ReadinessTCP {
		t.Fatalf("reconciled override probe plan=%#v err=%v", plan, err)
	}

	service.ReadinessOverride = nil
	service.UpdatedAt = now.Add(3 * time.Second)
	if err = store.UpdateService(ctx, &service, service.Revision); err != nil {
		t.Fatal(err)
	}
	plan, err = store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 1 || plan.Targets[0].Policy.Mode != model.ReadinessHTTP || plan.Targets[0].Policy.Path != "/changed" {
		t.Fatalf("cleared override did not restore discovery: %#v err=%v", plan, err)
	}
}

func TestProbeLeaderLossFencesResultsWithoutMutatingInstancesOrConfig(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "leader-loss-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "leader-loss-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready", Scheme: "http", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtimeValue.ID, ServiceKey: "demo_api", ExternalID: "demo_api", Name: "demo_api",
		QualifiedName: "demo_api", DNSName: "demo_api", DNSStatus: model.DNSReady, RoutingEligible: true,
		DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, Readiness: policy,
		DesiredInstances: 1, RunningInstances: 1, ReadyInstances: 1, Status: model.ServiceAvailable,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	endpoint := model.Endpoint{ID: id.New(id.Endpoint), ServiceID: service.ID, ExternalID: "container", Address: "10.72.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, ReadyState: model.Ready, ReadinessSource: model.ReadinessHTTP, Weight: 1, FirstSeenAt: now, ReadySince: &now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertEndpoint(ctx, &endpoint); err != nil {
		t.Fatal(err)
	}
	gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "leader", Enabled: true, Status: model.GatewayReady, LastSeenAt: &now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertGateway(ctx, &gateway); err != nil {
		t.Fatal(err)
	}
	selected, err := store.SelectProbeLeader(ctx, pool.ID, time.Minute)
	if err != nil || selected.ProbeGatewayID != gateway.ID {
		t.Fatalf("initial leader=%#v err=%v", selected, err)
	}
	target := currentProbeTarget(t, store, pool.ID, service.ID)
	probeChanged, err := store.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, Epoch: selected.ProbeEpoch,
		Revision: currentProbeRevision(t, store, pool.ID), ServiceID: service.ID, Sequence: 1, Success: false,
		Error: "DNS timeout", ObservedAt: now.Add(time.Second), TargetToken: target.TargetToken})
	if err != nil || !probeChanged {
		t.Fatalf("leader probe did not update DNS status: changed=%v err=%v", probeChanged, err)
	}
	selected, err = store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * time.Minute)
	if _, err = store.DB().ExecContext(ctx, `UPDATE gateways SET last_seen_at=? WHERE id=?`, old, gateway.ID); err != nil {
		t.Fatal(err)
	}
	cleared, err := store.SelectProbeLeader(ctx, pool.ID, time.Minute)
	if err != nil || cleared.ProbeGatewayID != "" || cleared.ProbeEpoch <= selected.ProbeEpoch || cleared.DesiredRevision != selected.DesiredRevision {
		t.Fatalf("cleared leader=%#v previous=%#v err=%v", cleared, selected, err)
	}
	endpoints, err := store.ListEndpoints(ctx, service.ID)
	if err != nil || len(endpoints) != 1 || endpoints[0].ReadyState != model.Ready {
		t.Fatalf("leader loss mutated instance health: %#v err=%v", endpoints, err)
	}
	service, err = store.GetService(ctx, service.ID)
	if err != nil || service.DNSStatus != model.DNSUnreachable {
		t.Fatalf("leader loss discarded the last DNS status: %#v err=%v", service, err)
	}
}

func TestRouteSourcePrecedenceOnlyPublishesEffectiveChanges(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "precedence-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "precedence-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	makeService := func(name, address string) model.Service {
		service := model.Service{ID: id.New(id.Service), RuntimeID: runtimeValue.ID, ServiceKey: "demo/" + name, ExternalID: "demo/" + name, Name: name, Namespace: "demo", QualifiedName: "demo_" + name, DNSName: "demo_" + name, DNSStatus: model.DNSReady, RoutingEligible: true, DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, Readiness: policy, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.UpsertService(ctx, &service); err != nil {
			t.Fatal(err)
		}
		endpoint := model.Endpoint{ID: id.New(id.Endpoint), ServiceID: service.ID, ExternalID: name, Address: address, Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthHealthy, ReadyState: model.Ready, ReadinessSource: model.ReadinessDocker, Weight: 1, FirstSeenAt: now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.UpsertEndpoint(ctx, &endpoint); err != nil {
			t.Fatal(err)
		}
		return service
	}
	discoveredService := makeService("discovered", "10.60.0.2")
	apiService := makeService("api", "10.60.0.3")
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "precedence.example.com", TLSMode: model.TLSDisabled, Status: model.DomainReady, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateDomain(ctx, &domain); err != nil {
		t.Fatal(err)
	}
	discoveredRoute := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: discoveredService.ID, Path: "/", Priority: 10_000, UpstreamProtocol: "http", Enabled: true, Source: model.SourceDiscovery, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRoute(ctx, &discoveredRoute); err != nil {
		t.Fatal(err)
	}
	apiRoute := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: apiService.ID, Path: "/", Priority: -10_000, UpstreamProtocol: "http", Enabled: true, Source: model.SourceAPI, Revision: 1, CreatedAt: now, UpdatedAt: now.Add(time.Second)}
	if err := store.CreateRoute(ctx, &apiRoute); err != nil {
		t.Fatal(err)
	}
	pool, err := store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	revisionWithAPIWinner := pool.DesiredRevision

	shadowedStatic := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: discoveredService.ID, Path: "/", Priority: 20_000, TargetPort: 9000, UpstreamProtocol: "http", Enabled: true, Source: model.SourceStatic, Revision: 1, CreatedAt: now, UpdatedAt: now.Add(2 * time.Second)}
	if err = store.CreateRoute(ctx, &shadowedStatic); err != nil {
		t.Fatal(err)
	}
	pool, _ = store.GetPool(ctx, pool.ID)
	if pool.DesiredRevision != revisionWithAPIWinner {
		t.Fatalf("shadowed route bumped desired revision: got %d want %d", pool.DesiredRevision, revisionWithAPIWinner)
	}
	shadowedStatic.TargetPort = 9001
	shadowedStatic.UpdatedAt = now.Add(3 * time.Second)
	if err = store.UpdateRoute(ctx, &shadowedStatic, shadowedStatic.Revision); err != nil {
		t.Fatal(err)
	}
	shadowedStatic.Revision++
	pool, _ = store.GetPool(ctx, pool.ID)
	if pool.DesiredRevision != revisionWithAPIWinner {
		t.Fatalf("shadowed route update bumped desired revision: got %d want %d", pool.DesiredRevision, revisionWithAPIWinner)
	}
	if err = store.DeleteRoute(ctx, shadowedStatic.ID, shadowedStatic.Revision); err != nil {
		t.Fatal(err)
	}
	pool, _ = store.GetPool(ctx, pool.ID)
	if pool.DesiredRevision != revisionWithAPIWinner {
		t.Fatalf("shadowed route deletion bumped desired revision: got %d want %d", pool.DesiredRevision, revisionWithAPIWinner)
	}
	conflictingAPI := apiRoute
	conflictingAPI.ID = id.New(id.Route)
	conflictingAPI.ServiceID = discoveredService.ID
	if err = store.CreateRoute(ctx, &conflictingAPI); !errors.Is(err, ErrConflict) {
		t.Fatalf("equal API precedence returned %v, want conflict", err)
	}
	snapshot, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].ID != apiRoute.ID || snapshot.Routes[0].ServiceID != apiService.ID {
		t.Fatalf("source precedence selected the wrong route: %#v", snapshot.Routes)
	}
}

func TestRouteMovePublishesOldAndNewPoolSnapshots(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	type resources struct {
		pool    model.GatewayPool
		service model.Service
		domain  model.Domain
	}
	create := func(suffix, address string) resources {
		value := resources{}
		value.pool = model.GatewayPool{ID: id.New(id.Pool), Name: "move-" + suffix, BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.CreatePool(ctx, &value.pool); err != nil {
			t.Fatal(err)
		}
		runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: value.pool.ID, Name: "move-runtime-" + suffix, Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
			t.Fatal(err)
		}
		value.service = model.Service{ID: id.New(id.Service), RuntimeID: runtimeValue.ID, ServiceKey: suffix + "/service", ExternalID: suffix + "/service", Name: "service", Namespace: suffix, QualifiedName: suffix + "_service", DNSName: suffix + "_service", DNSStatus: model.DNSReady, RoutingEligible: true, DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, Readiness: model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.UpsertService(ctx, &value.service); err != nil {
			t.Fatal(err)
		}
		endpoint := model.Endpoint{ID: id.New(id.Endpoint), ServiceID: value.service.ID, ExternalID: "endpoint-" + suffix, Address: address, Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthHealthy, ReadyState: model.Ready, ReadinessSource: model.ReadinessDocker, Weight: 1, FirstSeenAt: now, ReadySince: &now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.UpsertEndpoint(ctx, &endpoint); err != nil {
			t.Fatal(err)
		}
		value.domain = model.Domain{ID: id.New(id.Domain), PoolID: value.pool.ID, Host: suffix + ".example.com", TLSMode: model.TLSDisabled, Status: model.DomainReady, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateDomain(ctx, &value.domain); err != nil {
			t.Fatal(err)
		}
		return value
	}
	a := create("old", "10.81.0.2")
	b := create("new", "10.82.0.2")
	route := model.Route{ID: id.New(id.Route), DomainID: a.domain.ID, ServiceID: a.service.ID, Path: "/", UpstreamProtocol: "http", Enabled: true, Source: model.SourceAPI, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRoute(ctx, &route); err != nil {
		t.Fatal(err)
	}
	a.pool, _ = store.GetPool(ctx, a.pool.ID)
	b.pool, _ = store.GetPool(ctx, b.pool.ID)
	oldRevision, newRevision := a.pool.DesiredRevision, b.pool.DesiredRevision

	route.DomainID = b.domain.ID
	route.ServiceID = b.service.ID
	route.UpdatedAt = now.Add(time.Second)
	if err := store.UpdateRoute(ctx, &route, route.Revision); err != nil {
		t.Fatal(err)
	}
	a.pool, _ = store.GetPool(ctx, a.pool.ID)
	b.pool, _ = store.GetPool(ctx, b.pool.ID)
	if a.pool.DesiredRevision != oldRevision+1 || b.pool.DesiredRevision != newRevision+1 {
		t.Fatalf("route move revisions old=%d want=%d new=%d want=%d", a.pool.DesiredRevision, oldRevision+1, b.pool.DesiredRevision, newRevision+1)
	}
	oldSnapshot, err := store.BuildSnapshot(ctx, a.pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	newSnapshot, err := store.BuildSnapshot(ctx, b.pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldSnapshot.Routes) != 0 || len(newSnapshot.Routes) != 1 || newSnapshot.Routes[0].ID != route.ID {
		t.Fatalf("route move leaked across pools: old=%#v new=%#v", oldSnapshot.Routes, newSnapshot.Routes)
	}
}

func TestReadOperationalMetrics(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "metrics-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, DesiredRevision: 7, ProbeRevision: 9, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "metrics-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtimeValue.ID, ExternalID: "metrics", Name: "metrics", DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, Readiness: model.ReadinessPolicy{Mode: model.ReadinessDocker, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}, Status: model.ServiceDegraded, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	for index, readyState := range []model.ReadyState{model.Ready, model.NotReady} {
		endpoint := model.Endpoint{ID: id.New(id.Endpoint), ServiceID: service.ID, ExternalID: fmt.Sprintf("endpoint-%d", index), Address: fmt.Sprintf("10.70.0.%d", index+2), Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthHealthy, ReadyState: readyState, ReadinessSource: model.ReadinessDocker, Weight: 1, FirstSeenAt: now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.UpsertEndpoint(ctx, &endpoint); err != nil {
			t.Fatal(err)
		}
	}
	gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "metrics-gateway", Enabled: true, Status: model.GatewayReady, DesiredRevision: 7, AppliedRevision: 5, AppliedProbeRevision: 6, LastSeenAt: &now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertGateway(ctx, &gateway); err != nil {
		t.Fatal(err)
	}
	if selected, err := store.SelectProbeLeader(ctx, pool.ID, time.Minute); err != nil || selected.ProbeGatewayID != gateway.ID {
		t.Fatalf("probe leader=%#v err=%v", selected, err)
	}
	notAfter := now.Add(48 * time.Hour)
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "custom", Status: model.CertificateActive, PrimaryDomain: "metrics.example.com", NotAfter: &notAfter, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateCertificate(ctx, &certificate); err != nil {
		t.Fatal(err)
	}
	state, err := store.ReadOperationalMetrics(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if state.Services != 1 || state.Endpoints != 2 || state.ReadyEndpoints != 1 || state.RuntimeOnline[model.RuntimeDocker] != 1 {
		t.Fatalf("unexpected aggregate counts: %#v", state)
	}
	if state.PoolDesired[pool.ID] != 7 || state.PoolApplied[pool.ID] != 5 || state.PoolProbeDesired[pool.ID] != 9 ||
		state.PoolProbeApplied[pool.ID] != 6 || state.Certificates[model.CertificateActive] != 1 {
		t.Fatalf("unexpected pool/certificate metrics: %#v", state)
	}
	if got := state.CertificateExpiry[pool.ID]; got != (48 * time.Hour).Seconds() {
		t.Fatalf("certificate expiry = %f", got)
	}
}

func TestGatewayHeartbeatDoesNotMaskRevisionLagOrNACK(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "heartbeat-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, DesiredRevision: 3, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "gateway", Enabled: true, Status: model.GatewayStale, DesiredRevision: 3, AppliedRevision: 2, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertGateway(ctx, &gateway); err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatGateway(ctx, gateway.ID, 3, 0, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetGateway(ctx, gateway.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.GatewayStale {
		t.Fatalf("lagging heartbeat changed status to %s", updated.Status)
	}
	if err = store.AckGateway(ctx, gateway.ID, 3, strings.Repeat("a", 64), "test-renderer", strings.Repeat("b", 64), ""); err != nil {
		t.Fatal(err)
	}
	updated, _ = store.GetGateway(ctx, gateway.ID)
	if updated.Status != model.GatewayReady || updated.AppliedRevision != 3 {
		t.Fatalf("successful ACK was not persisted: %#v", updated)
	}
	if err = store.AckGateway(ctx, gateway.ID, 4, strings.Repeat("c", 64), "test-renderer", "", "nginx -t failed"); err != nil {
		t.Fatal(err)
	}
	if err = store.HeartbeatGateway(ctx, gateway.ID, 4, 0, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	updated, _ = store.GetGateway(ctx, gateway.ID)
	if updated.Status != model.GatewayApplyFailed || updated.AppliedRevision != 3 {
		t.Fatalf("heartbeat masked NACK state: %#v", updated)
	}
}

func TestRecordSnapshotRejectsNondeterminismAndKeepsThree(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "snapshot-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, DesiredRevision: 4, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	for revision := int64(1); revision <= 4; revision++ {
		digest := fmt.Sprintf("digest-%d", revision)
		payload := []byte(fmt.Sprintf(`{"revision":%d}`, revision))
		if err := store.RecordSnapshot(ctx, pool.ID, revision, digest, payload, now.Add(time.Duration(revision)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordSnapshot(ctx, pool.ID, 4, "digest-4", []byte(`{"revision":4}`), now.Add(5*time.Second)); err != nil {
		t.Fatalf("idempotent snapshot record failed: %v", err)
	}
	if err := store.RecordSnapshot(ctx, pool.ID, 4, "different-digest", []byte(`{"revision":4}`), now.Add(6*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-deterministic snapshot returned %v", err)
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pool_snapshots WHERE pool_id=?`, pool.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("retained snapshots = %d, want 3", count)
	}
	var oldest int64
	if err := store.DB().QueryRowContext(ctx, `SELECT MIN(revision) FROM pool_snapshots WHERE pool_id=?`, pool.ID).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if oldest != 2 {
		t.Fatalf("oldest retained revision = %d, want 2", oldest)
	}
}

func TestProbeEpochAndTargetTokenRejectStaleResults(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtime := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "local", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtime); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready", Scheme: "http", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 2, FailureThreshold: 2, StartupTimeout: time.Minute}
	observed := model.RuntimeSnapshot{RuntimeID: runtime.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{
		ServiceKey: "demo_api", ExternalID: "demo_api", Name: "demo_api", QualifiedName: "demo_api",
		DNSName: "demo_api", DNSStatus: model.DNSReady, RoutingEligible: true,
		Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: policy,
		Endpoints: []model.DiscoveredEndpoint{{ExternalID: "a", Address: "10.0.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}},
	}}}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, observed); err != nil {
		t.Fatal(err)
	}
	services, _ := store.ListServices(ctx, Page{Limit: 10})
	gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "gw", Enabled: true, Status: model.GatewayReady, LastSeenAt: &now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertGateway(ctx, &gateway); err != nil {
		t.Fatal(err)
	}
	leader, err := store.SelectProbeLeader(ctx, pool.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	target := currentProbeTarget(t, store, pool.ID, services[0].ID)
	oldEpoch := leader.ProbeEpoch - 1
	if changed, err := store.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, Epoch: oldEpoch, Revision: currentProbeRevision(t, store, pool.ID), ServiceID: services[0].ID, Sequence: 1, Success: false, ObservedAt: now, TargetToken: target.TargetToken}); err != nil || changed {
		t.Fatalf("old epoch result was accepted: changed=%v err=%v", changed, err)
	}
	currentRevision := currentProbeRevision(t, store, pool.ID)
	if changed, err := store.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, Epoch: leader.ProbeEpoch, Revision: currentRevision, ServiceID: services[0].ID, Sequence: 1, Success: false, ObservedAt: now, TargetToken: "stale-target"}); err != nil || changed {
		t.Fatalf("stale target result was accepted: changed=%v err=%v", changed, err)
	}
	if changed, err := store.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, Epoch: leader.ProbeEpoch, Revision: currentProbeRevision(t, store, pool.ID), ServiceID: services[0].ID, Sequence: 1, Success: false, Error: "timeout", ObservedAt: now, TargetToken: target.TargetToken}); err != nil || changed {
		t.Fatalf("first threshold result changed readiness: changed=%v err=%v", changed, err)
	}
	if changed, err := store.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, Epoch: leader.ProbeEpoch, Revision: currentProbeRevision(t, store, pool.ID), ServiceID: services[0].ID, Sequence: 2, Success: false, Error: "timeout", ObservedAt: now, TargetToken: target.TargetToken}); err != nil || !changed {
		t.Fatalf("second threshold result did not change readiness: changed=%v err=%v", changed, err)
	}
}

func TestProbePlanContainsOneTargetForManyInstances(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "concurrent-probe-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "concurrent-probe-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessTCP, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	observed := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{
		ServiceKey: "demo_api", ExternalID: "demo_api", Name: "demo_api", QualifiedName: "demo_api",
		DNSName: "demo_api", DNSStatus: model.DNSReady, RoutingEligible: true,
		Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 2, RunningInstances: 2, Readiness: policy,
		Endpoints: []model.DiscoveredEndpoint{
			{ExternalID: "a", Address: "10.74.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1},
			{ExternalID: "b", Address: "10.74.0.3", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1},
		},
		Routes: []model.DiscoveredRoute{{ExternalKey: "public", Host: "concurrent.example.com", Path: "/", TLSMode: model.TLSDisabled, HTTPEnabled: true, UpstreamProtocol: "http"}},
	}}}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, observed); err != nil {
		t.Fatal(err)
	}
	plan, err := store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 1 || plan.Targets[0].DNSName != "demo_api" {
		t.Fatalf("probe plan=%#v err=%v", plan, err)
	}
	target := plan.Targets[0]
	changed, probeErr := store.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, Epoch: plan.Epoch, Revision: plan.Revision,
		ServiceID: target.ServiceID, Sequence: 1, Success: false, Error: "DNS timeout",
		ObservedAt: now.Add(time.Second), TargetToken: target.TargetToken})
	if probeErr != nil || !changed {
		t.Fatalf("Service DNS result was not accepted: changed=%v err=%v", changed, probeErr)
	}
	services, err := store.ListServices(ctx, Page{Limit: 10})
	if err != nil || len(services) != 1 || services[0].DNSStatus != model.DNSUnreachable || services[0].RunningInstances != 2 {
		t.Fatalf("Service DNS result corrupted instance state: services=%#v err=%v", services, err)
	}
}

func TestDiscoveryRouteWaitsForDNSAndKeepsActiveRouteDuringRecreate(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "dns-gated-edge", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "dns-gated-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessTCP, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	service := model.DiscoveredService{
		ServiceKey: "demo/web", ExternalID: "demo/web", Name: "web", Namespace: "demo", QualifiedName: "demo_web",
		DNSName: "demo_web", DNSStatus: model.DNSMissing, RoutingEligible: false, RoutingIssues: []string{"service is not attached to nsc-ingress"},
		Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: policy,
		Routes: []model.DiscoveredRoute{{ExternalKey: "public", Host: "dns-gated.example.com", Path: "/", HTTPEnabled: true, TLSMode: model.TLSDisabled, UpstreamProtocol: "http"}},
	}
	observed := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{service}}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, observed); err != nil {
		t.Fatal(err)
	}
	routes, err := store.ListRoutes(ctx, Page{Limit: 10})
	if err != nil || len(routes) != 1 {
		t.Fatalf("routes=%#v err=%v", routes, err)
	}
	if routes[0].Enabled || routes[0].Status != model.RouteError || !strings.HasPrefix(routes[0].LastError, discoveryDNSNotReadyPrefix) {
		t.Fatalf("DNS-ineligible discovery route was not gated: %#v", routes[0])
	}
	config, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil || len(config.Routes) != 0 {
		t.Fatalf("DNS-ineligible route leaked into snapshot: %#v err=%v", config.Routes, err)
	}
	plan, err := store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 0 {
		t.Fatalf("DNS-ineligible service leaked into probe plan: %#v err=%v", plan, err)
	}

	observed.Observation = 2
	observed.ObservedAt = now.Add(time.Second)
	observed.Services[0].DNSStatus = model.DNSReady
	observed.Services[0].RoutingEligible = true
	observed.Services[0].RoutingIssues = nil
	if _, _, err = store.ApplyRuntimeSnapshot(ctx, observed); err != nil {
		t.Fatal(err)
	}
	routes, err = store.ListRoutes(ctx, Page{Limit: 10})
	if err != nil || len(routes) != 1 || !routes[0].Enabled || routes[0].Status != model.RouteActive || routes[0].LastError != "" {
		t.Fatalf("route did not activate after DNS became ready: routes=%#v err=%v", routes, err)
	}
	config, err = store.BuildSnapshot(ctx, pool.ID)
	if err != nil || len(config.Routes) != 1 || config.Services[config.Routes[0].ServiceID].DNSName != "demo_web" {
		t.Fatalf("ready DNS route snapshot=%#v err=%v", config, err)
	}
	plan, err = store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 1 || plan.Targets[0].DNSName != "demo_web" {
		t.Fatalf("ready DNS probe plan=%#v err=%v", plan, err)
	}
	configRevision := currentPoolRevision(t, store, pool.ID)
	probeRevision := currentProbeRevision(t, store, pool.ID)

	observed.Observation = 3
	observed.ObservedAt = now.Add(2 * time.Second)
	observed.Services[0].DNSStatus = model.DNSMissing
	observed.Services[0].RoutingEligible = false
	observed.Services[0].RoutingIssues = []string{"replacement container is starting"}
	if changed, revision, applyErr := store.ApplyRuntimeSnapshot(ctx, observed); applyErr != nil || changed || revision != configRevision {
		t.Fatalf("transient DNS loss changed config: changed=%v revision=%d want=%d err=%v", changed, revision, configRevision, applyErr)
	}
	routes, err = store.ListRoutes(ctx, Page{Limit: 10})
	if err != nil || len(routes) != 1 || !routes[0].Enabled || routes[0].Status != model.RouteActive || routes[0].LastError != "" {
		t.Fatalf("active route was withdrawn during recreate: routes=%#v err=%v", routes, err)
	}
	config, err = store.BuildSnapshot(ctx, pool.ID)
	if err != nil || len(config.Routes) != 1 || config.Services[config.Routes[0].ServiceID].DNSName != "demo_web" {
		t.Fatalf("recreate withdrew DNS route snapshot=%#v err=%v", config, err)
	}
	plan, err = store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 1 || currentProbeRevision(t, store, pool.ID) != probeRevision {
		t.Fatalf("recreate changed stable DNS probe plan: plan=%#v revision=%d want=%d err=%v", plan, currentProbeRevision(t, store, pool.ID), probeRevision, err)
	}
}

func TestDiscoveryRouteConflictIsDiagnosedAndRecovers(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "edge-conflict", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "conflicting-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	service := func(externalID, routeKey string) model.DiscoveredService {
		return model.DiscoveredService{ServiceKey: "demo/" + externalID, ExternalID: "demo/" + externalID, Name: externalID, Namespace: "demo", QualifiedName: "demo_" + externalID, DNSName: "demo_" + externalID, DNSStatus: model.DNSReady, RoutingEligible: true, Protocol: "http", TargetPort: 8080, Enabled: true, Readiness: policy,
			Routes: []model.DiscoveredRoute{{ExternalKey: routeKey, Host: "conflict.example.com", Path: "/", Priority: 10, TLSMode: model.TLSDisabled, UpstreamProtocol: "http"}}}
	}
	observed := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{service("one", "one"), service("two", "two")}}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, observed); err != nil {
		t.Fatal(err)
	}
	routes, err := store.ListRoutes(ctx, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected both route claims for diagnostics, got %d", len(routes))
	}
	for _, route := range routes {
		if route.Enabled || route.LastError == "" {
			t.Fatalf("conflicting discovery route was not disabled and diagnosed: %#v", route)
		}
	}
	snapshot, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Routes) != 0 {
		t.Fatalf("conflicting routes leaked into snapshot: %#v", snapshot.Routes)
	}

	observed.Observation = 2
	observed.ObservedAt = now.Add(time.Second)
	observed.Services = observed.Services[:1]
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, observed); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.BuildSnapshot(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].ServiceID == "" {
		t.Fatalf("remaining route did not recover after conflict removal: %#v", snapshot.Routes)
	}
}

func TestPoolDNSNameConflictFailsClosedAndRecovers(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "dns-conflict", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeA := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "docker-a", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	runtimeB := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "docker-b", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeA); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRuntime(ctx, &runtimeB); err != nil {
		t.Fatal(err)
	}
	service := func(key, host string) model.DiscoveredService {
		return model.DiscoveredService{ServiceKey: key, ExternalID: key, Name: "web", Namespace: key, QualifiedName: "shared_web", DNSName: "shared_web", DNSStatus: model.DNSReady,
			Protocol: "http", TargetPort: 80, Enabled: true, RoutingEligible: true, Readiness: model.ReadinessPolicy{Mode: model.ReadinessTCP, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1},
			Routes: []model.DiscoveredRoute{{ExternalKey: "main", Host: host, Path: "/", HTTPEnabled: true, TLSMode: model.TLSDisabled, UpstreamProtocol: "http"}}}
	}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtimeA.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{service("project-a/web", "a.example.com")}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtimeB.ID, Observation: 1, ObservedAt: now.Add(time.Second), Services: []model.DiscoveredService{service("project-b/web", "b.example.com")}}); err != nil {
		t.Fatal(err)
	}
	services, err := store.ListServices(ctx, Page{Limit: 10})
	if err != nil || len(services) != 2 {
		t.Fatalf("services=%#v err=%v", services, err)
	}
	for _, value := range services {
		if value.DNSStatus != model.DNSConflict || value.RoutingEligible || !strings.HasPrefix(value.DNSLastError, serviceDNSConflictPrefix) {
			t.Fatalf("duplicate DNS owner did not fail closed: %#v", value)
		}
	}
	snapshot, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil || len(snapshot.Routes) != 0 || len(snapshot.Services) != 0 {
		t.Fatalf("conflicting DNS leaked into snapshot: %#v err=%v", snapshot, err)
	}
	plan, err := store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(plan.Targets) != 0 {
		t.Fatalf("conflicting DNS leaked into probe plan: %#v err=%v", plan, err)
	}
	if _, _, err = store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtimeB.ID, Observation: 2, ObservedAt: now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	services, err = store.ListServices(ctx, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var recovered model.Service
	for _, value := range services {
		if value.RuntimeID == runtimeA.ID {
			recovered = value
		}
	}
	if recovered.DNSStatus != model.DNSReady || !recovered.RoutingEligible || recovered.DNSLastError != "" {
		t.Fatalf("sole DNS owner did not recover: %#v", recovered)
	}
	snapshot, err = store.BuildSnapshot(ctx, pool.ID)
	if err != nil || len(snapshot.Routes) != 1 || snapshot.Routes[0].Host != "a.example.com" {
		t.Fatalf("recovered DNS route snapshot=%#v err=%v", snapshot, err)
	}
}

func TestDiscoveryDomainTLSConflictIsStableAndRecovers(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "edge-tls-conflict", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeA := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "runtime-a", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	runtimeB := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "runtime-b", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeA); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRuntime(ctx, &runtimeB); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	snapshot := func(runtimeID, externalID, routeKey, routePath string, tlsMode model.TLSMode, observation int64, observedAt time.Time) model.RuntimeSnapshot {
		return model.RuntimeSnapshot{RuntimeID: runtimeID, Observation: observation, ObservedAt: observedAt, Services: []model.DiscoveredService{{
			ServiceKey: "demo/" + externalID, ExternalID: "demo/" + externalID, Name: externalID, Namespace: "demo", QualifiedName: "demo_" + externalID, DNSName: "demo_" + externalID, DNSStatus: model.DNSReady, RoutingEligible: true, Protocol: "http", TargetPort: 8080, Enabled: true, Readiness: policy,
			Routes: []model.DiscoveredRoute{{ExternalKey: routeKey, Host: "shared.example.com", Path: routePath, TLSMode: tlsMode, UpstreamProtocol: "http"}},
		}}}
	}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, snapshot(runtimeA.ID, "plain", "plain", "/plain/", model.TLSDisabled, 1, now)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, snapshot(runtimeB.ID, "secure", "secure", "/secure/", model.TLSManaged, 1, now.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	domains, err := store.ListDomains(ctx, Page{Limit: 10})
	if err != nil || len(domains) != 1 {
		t.Fatalf("domains=%#v err=%v", domains, err)
	}
	if domains[0].TLSMode != model.TLSDisabled {
		t.Fatalf("conflicting observation changed the active TLS mode: %#v", domains[0])
	}
	routes, err := store.ListRoutes(ctx, Page{Limit: 10})
	if err != nil || len(routes) != 2 {
		t.Fatalf("routes=%#v err=%v", routes, err)
	}
	var conflicted bool
	for _, route := range routes {
		if strings.HasPrefix(route.LastError, discoveryDomainTLSConflictPrefix) {
			conflicted = true
			if route.Enabled {
				t.Fatalf("TLS-conflicting route remained enabled: %#v", route)
			}
		}
	}
	if !conflicted {
		t.Fatalf("TLS conflict was not retained as a route diagnostic: %#v", routes)
	}
	config, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Routes) != 1 || config.Routes[0].Path != "/plain/" {
		t.Fatalf("TLS-conflicting route leaked into the snapshot: %#v", config.Routes)
	}

	if _, _, err := store.ApplyRuntimeSnapshot(ctx, model.RuntimeSnapshot{RuntimeID: runtimeA.ID, Observation: 2, ObservedAt: now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, snapshot(runtimeB.ID, "secure", "secure", "/secure/", model.TLSManaged, 2, now.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	domains, err = store.ListDomains(ctx, Page{Limit: 10})
	if err != nil || len(domains) != 1 || domains[0].TLSMode != model.TLSManaged || domains[0].CertificateID == "" {
		t.Fatalf("remaining TLS claim did not take ownership: domains=%#v err=%v", domains, err)
	}
	routes, err = store.ListRoutes(ctx, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var recovered bool
	for _, route := range routes {
		if route.Path == "/secure/" {
			recovered = route.Enabled && route.LastError == ""
		}
	}
	if !recovered {
		t.Fatalf("remaining TLS route did not recover: %#v", routes)
	}
}

func TestDiscoveryManagedDomainCreatesACKGatedCertificate(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "managed-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "managed-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute}
	observed := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{ExternalID: "managed", Name: "managed", Protocol: "http", TargetPort: 8080, Enabled: true, Readiness: policy,
		Routes: []model.DiscoveredRoute{{ExternalKey: "public", Host: "managed.example.com", Path: "/", TLSMode: model.TLSManaged, UpstreamProtocol: "http"}}}}}
	_, revision, err := store.ApplyRuntimeSnapshot(ctx, observed)
	if err != nil {
		t.Fatal(err)
	}
	domains, err := store.ListDomains(ctx, Page{Limit: 10})
	if err != nil || len(domains) != 1 || domains[0].CertificateID == "" || domains[0].Status != model.DomainCertPending {
		t.Fatalf("managed domain was not linked to a pending certificate: domains=%#v err=%v", domains, err)
	}
	certificate, err := store.GetCertificate(ctx, domains[0].CertificateID)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Provider != "acme" || certificate.Status != model.CertificatePending || certificate.DistributionRevision != revision {
		t.Fatalf("certificate is not gated on the discovery snapshot revision: %#v, revision=%d", certificate, revision)
	}
}

func TestManagedDomainWithoutRouteIsPresentForHTTP01(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "challenge-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "challenge.example.com", TLSMode: model.TLSManaged, HTTPSRedirect: true, Status: model.DomainCertPending, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", Status: model.CertificatePending, PrimaryDomain: domain.Host, Domains: []string{domain.Host}, Revision: 1, CreatedAt: now, UpdatedAt: now}
	domain.CertificateID = certificate.ID
	if err := store.CreateManagedDomain(ctx, &domain, &certificate); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Routes) != 0 || len(snapshot.Domains) != 1 || snapshot.Domains[0].Host != domain.Host || snapshot.Domains[0].CertificateID != certificate.ID {
		t.Fatalf("managed domain was not represented in challenge snapshot: %#v", snapshot)
	}
}

func TestExplicitServiceDNSProbeDoesNotPretendToClassifyRuntimeInstances(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "startup-timeout", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "local", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	started := now
	policy := model.ReadinessPolicy{Mode: model.ReadinessHTTP, Path: "/ready", Scheme: "http", Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: 10 * time.Second}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{ExternalID: "api", Name: "api", Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: policy, Endpoints: []model.DiscoveredEndpoint{{ExternalID: "a", Address: "10.0.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, StartedAt: &started, Weight: 1}}}}}
	if _, _, err := store.ApplyRuntimeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	services, err := store.ListServices(ctx, Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	endpoints, err := store.ListEndpoints(ctx, services[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if endpoints[0].ReadyState != model.Ready || endpoints[0].ReadinessSource != model.ReadinessHTTP {
		t.Fatalf("running instance state=%s source=%s, want runtime READY with service-level HTTP probe", endpoints[0].ReadyState, endpoints[0].ReadinessSource)
	}
}

func TestRuntimeOfflineRetainDrainAndEndpointGC(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "offline-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "offline-runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	policy := model.ReadinessPolicy{Mode: model.ReadinessGrace, Interval: time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupGracePeriod: 0, StartupTimeout: time.Minute}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: 1, ObservedAt: now, Services: []model.DiscoveredService{{ServiceKey: "demo/api", ExternalID: "demo/api", Name: "api", Namespace: "demo", QualifiedName: "demo_api", DNSName: "demo_api", DNSStatus: model.DNSReady, RoutingEligible: true, Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: policy, Endpoints: []model.DiscoveredEndpoint{{ExternalID: "container", Address: "10.80.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}}, Routes: []model.DiscoveredRoute{{ExternalKey: "public", Host: "offline.example.com", Path: "/", TLSMode: model.TLSDisabled, UpstreamProtocol: "http"}}}}}
	changed, activeRevision, err := store.ApplyRuntimeSnapshot(ctx, snapshot)
	if err != nil || !changed {
		t.Fatalf("initial snapshot changed=%v revision=%d err=%v", changed, activeRevision, err)
	}
	offlineAt := now.Add(time.Second)
	offlineObservation, err := store.NextObservation(ctx, runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if applied, markErr := store.MarkRuntimeOffline(ctx, runtimeValue.ID, offlineObservation, "Docker unavailable", offlineAt); markErr != nil || !applied {
		t.Fatalf("mark offline: applied=%v err=%v", applied, markErr)
	}
	services, _ := store.ListServices(ctx, Page{Limit: 10})
	endpoints, _ := store.ListEndpoints(ctx, services[0].ID)
	if endpoints[0].ReadyState != model.Ready || endpoints[0].StaleSince == nil {
		t.Fatalf("runtime outage altered retained readiness: %#v", endpoints[0])
	}
	changed, revision, err := store.SweepEndpointLifecycle(ctx, runtimeValue.ID, offlineObservation, "retain-ready", offlineAt.Add(time.Minute), 10*time.Second, 5*time.Second, time.Hour)
	if err != nil || changed || revision != activeRevision {
		t.Fatalf("retain-ready sweep changed data plane: changed=%v revision=%d err=%v", changed, revision, err)
	}
	changed, drainedRevision, err := store.SweepEndpointLifecycle(ctx, runtimeValue.ID, offlineObservation, "drain-after-timeout", offlineAt.Add(time.Minute), 10*time.Second, 5*time.Second, time.Hour)
	if err != nil || changed || drainedRevision != activeRevision {
		t.Fatalf("drain sweep changed=%v revision=%d err=%v", changed, drainedRevision, err)
	}
	endpoints, _ = store.ListEndpoints(ctx, services[0].ID)
	if endpoints[0].ReadyState != model.Draining || endpoints[0].DrainSince == nil {
		t.Fatalf("stale endpoint did not enter draining: %#v", endpoints[0])
	}
	changed, revision, err = store.SweepEndpointLifecycle(ctx, runtimeValue.ID, offlineObservation, "drain-after-timeout", offlineAt.Add(2*time.Minute), 10*time.Second, 5*time.Second, time.Hour)
	if err != nil || changed || revision != drainedRevision {
		t.Fatalf("draining-to-removed changed effective data plane: changed=%v revision=%d err=%v", changed, revision, err)
	}
	endpoints, _ = store.ListEndpoints(ctx, services[0].ID)
	if endpoints[0].ReadyState != model.Removed {
		t.Fatalf("endpoint state = %s, want REMOVED", endpoints[0].ReadyState)
	}
	if _, _, err = store.SweepEndpointLifecycle(ctx, runtimeValue.ID, offlineObservation, "drain-after-timeout", now.Add(2*time.Hour), 10*time.Second, 5*time.Second, time.Hour); err != nil {
		t.Fatal(err)
	}
	endpoints, _ = store.ListEndpoints(ctx, services[0].ID)
	if len(endpoints) != 0 {
		t.Fatalf("retained endpoint was not garbage collected: %#v", endpoints)
	}
}

func TestReencryptACMEAccounts(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	account := model.ACMEAccount{ID: id.New(id.Account), DirectoryURL: "https://acme.invalid/directory", Email: "ops@example.com", Registration: `{}`, EncryptedPrivateKey: []byte("old-envelope"), EncryptionKeyID: "old", CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertACMEAccount(ctx, &account); err != nil {
		t.Fatal(err)
	}
	count, err := store.ReencryptACMEAccounts(ctx, func(value model.ACMEAccount) ([]byte, string, error) {
		if value.ID != account.ID || string(value.EncryptedPrivateKey) != "old-envelope" {
			t.Fatalf("unexpected account passed to transform: %#v", value)
		}
		return []byte("new-envelope"), "new", nil
	})
	if err != nil || count != 1 {
		t.Fatalf("reencrypt count=%d err=%v", count, err)
	}
	updated, err := store.GetACMEAccount(ctx, account.DirectoryURL, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if updated.EncryptionKeyID != "new" || string(updated.EncryptedPrivateKey) != "new-envelope" {
		t.Fatalf("account was not re-encrypted: %#v", updated)
	}
}

func TestActivateCertificateAndDomainsIsAtomic(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "tls-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "secure.example.com", TLSMode: model.TLSManaged, HTTPSRedirect: true, Status: model.DomainCertPending, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", Status: model.CertificatePending, PrimaryDomain: domain.Host, Domains: []string{domain.Host}, Revision: 1, CreatedAt: now, UpdatedAt: now}
	domain.CertificateID = certificate.ID
	if err := store.CreateManagedDomain(ctx, &domain, &certificate); err != nil {
		t.Fatal(err)
	}
	certificate, err := store.GetCertificate(ctx, certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	certificate.Status = model.CertificateActive
	certificate.CertificatePEM = "certificate-pem"
	certificate.EncryptedPrivateKey = []byte("encrypted-key")
	certificate.EncryptionKeyID = "v1"
	certificate.UpdatedAt = now.Add(time.Second)
	if err = store.ActivateCertificate(ctx, &certificate, certificate.Revision, certificate.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	domain, err = store.GetDomain(ctx, domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if domain.Status != model.DomainReady || domain.LastError != "" {
		t.Fatalf("domain was not activated with certificate: %#v", domain)
	}
	pool, err = store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pool.DesiredRevision != 2 {
		t.Fatalf("desired revision=%d, want 2", pool.DesiredRevision)
	}
}

func TestCertificateReencryptionDoesNotChangeGatewayRevision(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "rotation-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "custom", Status: model.CertificateActive, PrimaryDomain: "rotate.example.com", CertificatePEM: "certificate-pem", EncryptedPrivateKey: []byte("old-envelope"), EncryptionKeyID: "old", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateCertificate(ctx, &certificate); err != nil {
		t.Fatal(err)
	}
	count, err := store.ReencryptCertificates(ctx, func(model.Certificate) ([]byte, string, error) {
		return []byte("new-envelope"), "new", nil
	})
	if err != nil || count != 1 {
		t.Fatalf("reencrypt count=%d err=%v", count, err)
	}
	pool, err = store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pool.DesiredRevision != 0 {
		t.Fatalf("key envelope rotation changed desired revision to %d", pool.DesiredRevision)
	}
	updated, err := store.GetCertificate(ctx, certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.EncryptionKeyID != "new" || string(updated.EncryptedPrivateKey) != "new-envelope" {
		t.Fatalf("certificate was not re-encrypted: %#v", updated)
	}
}

func TestGarbageCollectsExpiredACMEState(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "gc-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-48 * time.Hour)
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", Status: model.CertificateError, PrimaryDomain: "failed.example.com", LastError: "failed", Revision: 1, CreatedAt: old, UpdatedAt: old}
	if err := store.CreateCertificate(ctx, &certificate); err != nil {
		t.Fatal(err)
	}
	challenge := Challenge{ID: id.New(id.Challenge), CertificateID: certificate.ID, Domain: certificate.PrimaryDomain, Type: "http-01", Token: "expired-token", KeyAuthorization: "expired-key", Status: "PRESENTED", ExpiresAt: now.Add(-time.Minute), CreatedAt: old, UpdatedAt: old}
	if err := store.PutChallenge(ctx, challenge); err != nil {
		t.Fatal(err)
	}
	result, err := store.GarbageCollect(ctx, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExpiredChallenges != 1 || result.Certificates != 1 {
		t.Fatalf("unexpected GC result: %#v", result)
	}
	if _, err = store.GetCertificate(ctx, certificate.ID); err != ErrNotFound {
		t.Fatalf("collected certificate lookup returned %v", err)
	}
}

func TestUnlinkedManagedCertificateIsQueuedForRevocation(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "revoke-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "old.example.com", TLSMode: model.TLSManaged, HTTPSRedirect: true, Status: model.DomainCertPending, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", Status: model.CertificatePending, PrimaryDomain: domain.Host, Domains: []string{domain.Host}, Revision: 1, CreatedAt: now, UpdatedAt: now}
	domain.CertificateID = certificate.ID
	if err := store.CreateManagedDomain(ctx, &domain, &certificate); err != nil {
		t.Fatal(err)
	}
	domain.TLSMode = model.TLSDisabled
	domain.HTTPSRedirect = false
	domain.Status = model.DomainReady
	domain.CertificateID = ""
	domain.UpdatedAt = now.Add(time.Second)
	if err := store.UpdateDomain(ctx, &domain, domain.Revision); err != nil {
		t.Fatal(err)
	}
	certificate, err := store.GetCertificate(ctx, certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Status != model.CertificateRevoked || certificate.NextAttemptAt == nil {
		t.Fatalf("unlinked certificate was not queued for revocation: %#v", certificate)
	}
}
