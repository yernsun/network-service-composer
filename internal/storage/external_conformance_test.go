package storage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestExternalStoreConformance(t *testing.T) {
	tests := []struct {
		name, driver, environment string
	}{
		{name: "postgres", driver: "postgres", environment: "NSC_TEST_POSTGRES_DSN"},
		{name: "mysql", driver: "mysql", environment: "NSC_TEST_MYSQL_DSN"},
	}
	ran := false
	for _, test := range tests {
		dsn := os.Getenv(test.environment)
		if dsn == "" {
			continue
		}
		ran = true
		t.Run(test.name, func(t *testing.T) { runStoreConformance(t, test.driver, dsn) })
	}
	if !ran {
		t.Skip("set NSC_TEST_POSTGRES_DSN and/or NSC_TEST_MYSQL_DSN")
	}
}

func runStoreConformance(t *testing.T, driver, dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := storage.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	suffix := id.New(id.Pool)
	pool := model.GatewayPool{ID: suffix, Name: "contract-" + suffix, BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "contract-" + id.New(id.Runtime), Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}

	const observations = 12
	values := make(chan int64, observations)
	errorsCh := make(chan error, observations)
	var group sync.WaitGroup
	for range observations {
		group.Add(1)
		go func() {
			defer group.Done()
			value, observationErr := store.NextObservation(ctx, runtimeValue.ID)
			if observationErr != nil {
				errorsCh <- observationErr
				return
			}
			values <- value
		}()
	}
	group.Wait()
	close(values)
	close(errorsCh)
	for observationErr := range errorsCh {
		t.Fatal(observationErr)
	}
	got := make([]int, 0, observations)
	for value := range values {
		got = append(got, int(value))
	}
	sort.Ints(got)
	for index, value := range got {
		if value != index+1 {
			t.Fatalf("observation epochs are not gap-free: %v", got)
		}
	}
	observation, err := store.NextObservation(ctx, runtimeValue.ID)
	if err != nil {
		t.Fatal(err)
	}
	readiness := model.ReadinessPolicy{Mode: model.ReadinessTCP, Interval: 2 * time.Second, Timeout: time.Second, SuccessThreshold: 1, FailureThreshold: 1, StartupTimeout: time.Minute, TCPFallback: true}
	discovered := model.RuntimeSnapshot{RuntimeID: runtimeValue.ID, Observation: observation, ObservedAt: now, Services: []model.DiscoveredService{{
		ServiceKey: "contract_service", ExternalID: "contract_service", Name: "contract_service", QualifiedName: "contract_service",
		DNSName: "contract_service", DNSStatus: model.DNSReady, RoutingEligible: true,
		Protocol: "http", TargetPort: 8080, Enabled: true, DesiredInstances: 1, Readiness: readiness,
		Endpoints: []model.DiscoveredEndpoint{{ExternalID: "contract-endpoint", Address: "10.250.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthUnknown, Weight: 1}},
		Routes:    []model.DiscoveredRoute{{ExternalKey: "contract-route", Host: "contract.example.com", Path: "/", TLSMode: model.TLSDisabled, HTTPEnabled: true, UpstreamProtocol: "http"}},
	}}}
	stale := discovered
	stale.Observation--
	if changed, _, applyErr := store.ApplyRuntimeSnapshot(ctx, stale); applyErr != nil || changed {
		t.Fatalf("superseded discovery observation applied: changed=%t err=%v", changed, applyErr)
	}
	if changed, _, applyErr := store.ApplyRuntimeSnapshot(ctx, discovered); applyErr != nil || !changed {
		t.Fatalf("apply discovery snapshot: changed=%t err=%v", changed, applyErr)
	}
	gatewaySnapshot, err := store.BuildSnapshot(ctx, pool.ID)
	if err != nil || len(gatewaySnapshot.Routes) != 1 || len(gatewaySnapshot.Services) != 1 {
		t.Fatalf("build gateway snapshot: routes=%d services=%d err=%v", len(gatewaySnapshot.Routes), len(gatewaySnapshot.Services), err)
	}
	probePlan, err := store.BuildProbePlan(ctx, pool.ID)
	if err != nil || len(probePlan.Targets) != 1 {
		t.Fatalf("build probe plan: targets=%d err=%v", len(probePlan.Targets), err)
	}
	target := probePlan.Targets[0]
	if changed, probeErr := store.UpdateServiceProbe(ctx, model.ProbeResult{PoolID: pool.ID, Epoch: probePlan.Epoch, Revision: probePlan.Revision,
		ServiceID: target.ServiceID, Sequence: 1, Success: false, Error: "resolver timeout",
		ObservedAt: now.Add(time.Second), TargetToken: target.TargetToken}); probeErr != nil || !changed {
		t.Fatalf("apply fenced probe result: changed=%t err=%v", changed, probeErr)
	}
	gatewaySnapshot, err = store.BuildSnapshot(ctx, pool.ID)
	if err != nil || len(gatewaySnapshot.Services) != 1 || gatewaySnapshot.Services[gatewaySnapshot.Routes[0].ServiceID].DNSName != "contract_service" {
		t.Fatalf("DNS health changed the immutable Gateway target: snapshot=%#v err=%v", gatewaySnapshot, err)
	}

	lockerA := lease.New(store.DB(), store.Dialect())
	lockerB := lease.New(store.DB(), store.Dialect())
	key := fmt.Sprintf("contract/%s", suffix)
	first, err := lockerA.TryLock(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Check(ctx); err != nil {
		t.Fatalf("fresh distributed lease was not held: %v", err)
	}
	if _, err = lockerB.TryLock(ctx, key); !errors.Is(err, lease.ErrBusy) {
		t.Fatalf("second distributed lock was not rejected: %v", err)
	}
	if err = first.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err = first.Check(ctx); !errors.Is(err, lease.ErrLost) {
		t.Fatalf("released distributed lease check = %v, want ErrLost", err)
	}
	second, err := lockerB.TryLock(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Release(ctx); err != nil {
		t.Fatal(err)
	}

	pool, err = store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	pool.Name += "-updated"
	pool.UpdatedAt = now.Add(time.Second)
	if err = store.UpdatePool(ctx, &pool, pool.Revision); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdatePool(ctx, &pool, pool.Revision); !errors.Is(err, storage.ErrPrecondition) {
		t.Fatalf("stale ETag update returned %v", err)
	}
	for revision := int64(1); revision <= 4; revision++ {
		if err = store.RecordSnapshot(ctx, pool.ID, revision, fmt.Sprintf("contract-digest-%d", revision), []byte(fmt.Sprintf(`{"revision":%d}`, revision)), now.Add(time.Duration(revision)*time.Second)); err != nil {
			t.Fatalf("record snapshot revision %d: %v", revision, err)
		}
	}
	if err = store.RecordSnapshot(ctx, pool.ID, 4, "different-contract-digest", []byte(`{"revision":4}`), now.Add(5*time.Second)); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("non-deterministic snapshot returned %v", err)
	}
	var retainedSnapshots int
	if err = store.DB().QueryRowContext(ctx, storeQuery(driver, `SELECT COUNT(*) FROM pool_snapshots WHERE pool_id=?`), pool.ID).Scan(&retainedSnapshots); err != nil || retainedSnapshots != 3 {
		t.Fatalf("snapshot retention count=%d err=%v", retainedSnapshots, err)
	}
	testExternalCertificateTransaction(t, ctx, store, pool.ID, now)
	operational, err := store.ReadOperationalMetrics(ctx, now)
	if err != nil {
		t.Fatalf("read operational metrics: %v", err)
	}
	if _, exists := operational.PoolDesired[pool.ID]; !exists {
		t.Fatalf("operational metrics omitted pool %s", pool.ID)
	}
	if driver == "postgres" {
		testPostgresPoolNotification(t, store, pool.ID)
	}
}

func storeQuery(driver, query string) string {
	if driver != "postgres" {
		return query
	}
	index := 1
	for strings.Contains(query, "?") {
		query = strings.Replace(query, "?", fmt.Sprintf("$%d", index), 1)
		index++
	}
	return query
}

func testExternalCertificateTransaction(t *testing.T, ctx context.Context, store *storage.SQLStore, poolID string, now time.Time) {
	t.Helper()
	domain := model.Domain{ID: id.New(id.Domain), PoolID: poolID, Host: "contract-" + id.New(id.Domain) + ".example.com", TLSMode: model.TLSManaged, HTTPSRedirect: true, Status: model.DomainCertPending, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: poolID, Provider: "acme", Status: model.CertificatePending, PrimaryDomain: domain.Host, Domains: []string{domain.Host}, Revision: 1, CreatedAt: now, UpdatedAt: now}
	domain.CertificateID = certificate.ID
	if err := store.CreateManagedDomain(ctx, &domain, &certificate); err != nil {
		t.Fatal(err)
	}
	certificate, err := store.GetCertificate(ctx, certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	certificate.Status = model.CertificateActive
	certificate.CertificatePEM = "contract-certificate"
	certificate.EncryptedPrivateKey = []byte("contract-key")
	certificate.EncryptionKeyID = "test"
	certificate.UpdatedAt = now.Add(time.Second)
	if err = store.ActivateCertificate(ctx, &certificate, certificate.Revision, certificate.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	domain, err = store.GetDomain(ctx, domain.ID)
	if err != nil || domain.Status != model.DomainReady {
		t.Fatalf("atomic certificate activation failed: domain=%#v err=%v", domain, err)
	}
	domain.TLSMode = model.TLSDisabled
	domain.HTTPSRedirect = false
	domain.Status = model.DomainReady
	domain.CertificateID = ""
	domain.UpdatedAt = now.Add(2 * time.Second)
	if err = store.UpdateDomain(ctx, &domain, domain.Revision); err != nil {
		t.Fatal(err)
	}
	challenge := storage.Challenge{ID: id.New(id.Challenge), CertificateID: certificate.ID, Domain: domain.Host, Type: "http-01", Token: id.New(id.Challenge), KeyAuthorization: "contract-key-authorization", Status: "PRESENTED", ExpiresAt: now, CreatedAt: now, UpdatedAt: now}
	if err = store.PutChallenge(ctx, challenge); err != nil {
		t.Fatal(err)
	}
	result, err := store.GarbageCollect(ctx, now.Add(2*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExpiredChallenges < 1 || result.Certificates < 1 {
		t.Fatalf("external GC did not remove terminal ACME state: %#v", result)
	}
}

func testPostgresPoolNotification(t *testing.T, store *storage.SQLStore, poolID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	changes := make(chan model.StateChange, 8)
	done := make(chan error, 1)
	go func() {
		done <- store.ListenPoolChanges(ctx, func(change model.StateChange) {
			select {
			case changes <- change:
			default:
			}
		})
	}()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case change := <-changes:
			if change.PoolID == poolID && change.Revision > 0 {
				cancel()
				select {
				case err := <-done:
					if err != nil && !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
				}
				return
			}
		case <-ticker.C:
			if _, err := store.BumpPoolRevision(ctx, poolID); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for PostgreSQL pool change notification")
		}
	}
}
