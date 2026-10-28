package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
)

// BenchmarkApplyLargeRuntimeSignal measures the hot path behind a discovery
// signal after the DNS-only target data set is already present. Setup is
// deliberately outside the timer because initial import and steady-state
// propagation have different operational budgets. Instance address churn must
// update status without changing the stable Gateway configuration revision.
func BenchmarkApplyLargeRuntimeSignal(b *testing.B) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(b.TempDir(), "nsc-benchmark.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"
	store, err := Open("sqlite", dsn)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "capacity", BackendNetwork: "nsc-ingress", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreatePool(ctx, &pool); err != nil {
		b.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "capacity", Type: model.RuntimeDocker, Enabled: true, Status: model.RuntimeInitializing, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtimeValue); err != nil {
		b.Fatal(err)
	}
	snapshot := largeRuntimeSnapshot(runtimeValue.ID, now)
	if changed, _, applyErr := store.ApplyRuntimeSnapshot(ctx, snapshot); applyErr != nil || !changed {
		b.Fatalf("seed capacity snapshot: changed=%v err=%v", changed, applyErr)
	}
	seededPool, err := store.GetPool(ctx, pool.ID)
	if err != nil {
		b.Fatal(err)
	}
	configRevision := seededPool.DesiredRevision

	b.ReportAllocs()
	durations := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for iteration := range b.N {
		snapshot.Observation = int64(iteration + 2)
		snapshot.ObservedAt = now.Add(time.Duration(iteration+1) * time.Second)
		if iteration%2 == 0 {
			snapshot.Services[0].Endpoints[0].Address = "10.251.0.1"
		} else {
			snapshot.Services[0].Endpoints[0].Address = "10.252.0.1"
		}
		started := time.Now()
		changed, revision, applyErr := store.ApplyRuntimeSnapshot(ctx, snapshot)
		durations = append(durations, time.Since(started))
		if applyErr != nil || changed || revision != configRevision {
			b.Fatalf("apply capacity signal %d: changed=%v revision=%d want=%d err=%v", iteration, changed, revision, configRevision, applyErr)
		}
	}
	b.StopTimer()
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(95*len(durations)+99)/100-1]
	b.ReportMetric(p95.Seconds(), "p95-signal-seconds")
	if p95 >= 2*time.Second {
		b.Fatalf("P95 state signal latency %s exceeds the 2s DNS-only capacity gate", p95)
	}
}

func largeRuntimeSnapshot(runtimeID string, observedAt time.Time) model.RuntimeSnapshot {
	const (
		serviceCount     = 10_000
		endpointCount    = 50_000
		domainCount      = 20_000
		perService       = endpointCount / serviceCount
		routesPerService = domainCount / serviceCount
	)
	policy := model.ReadinessPolicy{
		Mode:               model.ReadinessGrace,
		Interval:           time.Second,
		Timeout:            time.Second,
		SuccessThreshold:   1,
		FailureThreshold:   1,
		StartupGracePeriod: 0,
		StartupTimeout:     time.Minute,
	}
	snapshot := model.RuntimeSnapshot{RuntimeID: runtimeID, Observation: 1, ObservedAt: observedAt, Services: make([]model.DiscoveredService, 0, serviceCount)}
	for serviceIndex := range serviceCount {
		serviceName := "service-" + strconv.Itoa(serviceIndex)
		service := model.DiscoveredService{
			ServiceKey:      "capacity/" + serviceName,
			ExternalID:      "capacity/" + serviceName,
			Name:            serviceName,
			Namespace:       "capacity",
			QualifiedName:   "capacity_" + serviceName,
			DNSName:         "capacity_" + serviceName,
			DNSStatus:       model.DNSReady,
			RoutingEligible: true,
			Protocol:        "http",
			TargetPort:      8_080,
			Enabled:         true,

			DesiredInstances: perService,
			Readiness:        policy,
			Endpoints:        make([]model.DiscoveredEndpoint, 0, perService),
			Routes:           make([]model.DiscoveredRoute, 0, routesPerService),
		}
		for endpointIndex := range perService {
			ordinal := serviceIndex*perService + endpointIndex
			service.Endpoints = append(service.Endpoints, model.DiscoveredEndpoint{
				ExternalID:   "endpoint-" + strconv.Itoa(endpointIndex),
				Address:      fmt.Sprintf("10.%d.%d.%d", (ordinal>>16)%250, (ordinal>>8)%250, ordinal%250+1),
				Port:         8_080,
				RuntimeState: model.EndpointRunning,
				HealthState:  model.HealthUnknown,
				Weight:       1,
			})
		}
		for routeIndex := range routesPerService {
			domainIndex := serviceIndex*routesPerService + routeIndex
			service.Routes = append(service.Routes, model.DiscoveredRoute{
				ExternalKey:      "route-" + strconv.Itoa(routeIndex),
				Host:             "app-" + strconv.Itoa(domainIndex) + ".example.test",
				Path:             "/",
				TLSMode:          model.TLSDisabled,
				HTTPEnabled:      true,
				UpstreamProtocol: "http",
			})
		}
		snapshot.Services = append(snapshot.Services, service)
	}
	return snapshot
}
