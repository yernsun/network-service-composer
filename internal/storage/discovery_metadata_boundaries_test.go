package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestDiscoveryRefreshPreservesManagementAndUserOverrides(t *testing.T) {
	for _, mode := range []string{"unchanged", "runtime object", "external identity", "display name", "namespace", "qualified name", "DNS name", "DNS status", "clear DNS error", "protocol", "port", "enabled", "readiness", "desired replicas", "ready replicas", "configuration error", "routing eligibility", "routing issues", "runtime created", "runtime updated", "removed metadata", "removed timestamp", "preserve failed probe"} {
		t.Run(mode, func(t *testing.T) {
			store := newSQLiteStore(t)
			ctx, runtime, now := managedRuntimeFixture(t, store)
			port := 9090
			enabled := true
			protocol := "https"
			original := model.Service{ID: "service", RuntimeID: runtime.ID, ServiceKey: "project/web", ExternalID: "old", RuntimeObjectID: "object", Name: "web", Namespace: "project", QualifiedName: "project_web", DNSName: "project_web", DNSStatus: model.DNSReady, DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true, RoutingEligible: true, Readiness: model.ReadinessPolicy{Mode: model.ReadinessDocker}, DesiredInstances: 1, RuntimeReadyInstances: 1, Status: model.ServiceAvailable, TargetPortOverride: &port, EnabledOverride: &enabled, ProtocolOverride: &protocol, Revision: 1, CreatedAt: now, UpdatedAt: now}
			observed := model.DiscoveredService{ServiceKey: original.ServiceKey, ExternalID: original.ExternalID, RuntimeObjectID: original.RuntimeObjectID, Name: original.Name, Namespace: original.Namespace, QualifiedName: original.QualifiedName, DNSName: original.DNSName, DNSStatus: original.DNSStatus, Protocol: original.DiscoveredProtocol, TargetPort: original.DiscoveredPort, Enabled: original.DiscoveredEnabled, RoutingEligible: original.RoutingEligible, Readiness: original.Readiness, DesiredInstances: 1, ReadyInstances: 1}
			switch mode {
			case "runtime object":
				observed.RuntimeObjectID = "new-object"
			case "external identity":
				observed.ExternalID = "new-external"
			case "display name":
				observed.Name = "renamed"
			case "namespace":
				observed.Namespace = "renamed"
			case "qualified name":
				observed.QualifiedName = "renamed_web"
			case "DNS name":
				observed.DNSName = "new.internal"
			case "DNS status":
				observed.DNSStatus = model.DNSMissing
			case "clear DNS error":
				original.DNSLastError = "old metadata failure"
			case "protocol":
				observed.Protocol = "https"
			case "port":
				observed.TargetPort = 8081
			case "enabled":
				observed.Enabled = false
			case "readiness":
				observed.Readiness.Mode = model.ReadinessTCP
			case "desired replicas":
				observed.DesiredInstances = 3
			case "ready replicas":
				observed.ReadyInstances = 2
			case "configuration error":
				observed.ConfigurationError = "invalid network"
			case "routing eligibility":
				observed.RoutingEligible = false
			case "routing issues":
				observed.RoutingIssues = []string{"network not attached"}
			case "runtime created":
				created := now.Add(-time.Hour)
				observed.RuntimeCreatedAt = &created
			case "runtime updated":
				updated := now.Add(-time.Minute)
				observed.RuntimeUpdatedAt = &updated
			case "removed metadata":
				original.Status = model.ServiceRemoved
			case "removed timestamp":
				removed := now.Add(-time.Minute)
				original.RemovedAt = &removed
			case "preserve failed probe":
				original.DNSStatus = model.DNSUnreachable
				original.DNSLastError = "Gateway cannot reach target"
			}
			if e := store.UpsertService(ctx, &original); e != nil {
				t.Fatal(e)
			}
			tx, e := store.db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			value, _, e := upsertDiscoveredServiceTx(ctx, store, tx, model.RuntimeSnapshot{RuntimeID: runtime.ID, ObservedAt: now.Add(time.Second)}, observed, map[string]model.Service{original.ServiceKey: original})
			if e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			current, e := store.GetService(ctx, original.ID)
			if e != nil {
				t.Fatal(e)
			}
			if current.ID != original.ID || current.RuntimeID != original.RuntimeID || current.Managed != original.Managed || current.ApplicationID != original.ApplicationID || current.TargetPortOverride == nil || *current.TargetPortOverride != port || current.EnabledOverride == nil || !*current.EnabledOverride || current.ProtocolOverride == nil || *current.ProtocolOverride != protocol || !current.CreatedAt.Equal(original.CreatedAt) {
				t.Fatal("discovery overwrote explicit management or overrides", current)
			}
			if current.RuntimeObjectID != observed.RuntimeObjectID || current.ExternalID != observed.ExternalID || current.Name != observed.Name || current.Namespace != observed.Namespace || current.QualifiedName != observed.QualifiedName || current.DNSName != observed.DNSName || current.DiscoveredProtocol != observed.Protocol || current.DiscoveredPort != observed.TargetPort || current.DiscoveredEnabled != observed.Enabled || current.Readiness != observed.Readiness || current.DesiredInstances != observed.DesiredInstances || current.RuntimeReadyInstances != observed.ReadyInstances || current.ConfigurationError != observed.ConfigurationError || current.RoutingEligible != observed.RoutingEligible || !reflect.DeepEqual(current.RoutingIssues, observed.RoutingIssues) || !equalOptionalTime(current.RuntimeCreatedAt, observed.RuntimeCreatedAt) || !equalOptionalTime(current.RuntimeUpdatedAt, observed.RuntimeUpdatedAt) || !equalOptionalTime(current.RemovedAt, original.RemovedAt) {
				t.Fatal("discovery failed to refresh authoritative metadata", current)
			}
			if mode == "preserve failed probe" {
				if value.DNSStatus != model.DNSUnreachable || value.DNSLastError != original.DNSLastError || current.DNSStatus != model.DNSUnreachable {
					t.Fatal("runtime discovery falsely confirmed Gateway reachability")
				}
			} else if current.DNSStatus != observed.DNSStatus || current.DNSLastError != "" {
				t.Fatal("stale DNS metadata retained")
			}
			revisionChanged := map[string]bool{"runtime object": true, "external identity": true, "display name": true,
				"namespace": true, "qualified name": true, "DNS name": true, "protocol": true, "port": true,
				"enabled": true, "readiness": true, "desired replicas": true, "runtime created": true,
				"runtime updated": true, "removed metadata": true}[mode]
			if current.LastSeenAt == nil || !current.LastSeenAt.Equal(now.Add(time.Second)) {
				t.Fatal("accepted unchanged observation lost its freshness", current.LastSeenAt)
			}
			if !revisionChanged && current.Revision != original.Revision || revisionChanged && current.Revision != original.Revision+1 {
				t.Fatal("metadata revision did not distinguish actual changes", current.Revision, original.Revision)
			}
		})
	}
}

func TestEndpointHealthChangesDoNotExpireServiceETag(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	service := model.Service{ID: "health-etag", RuntimeID: runtime.ID, ServiceKey: "project/web", RuntimeObjectID: "object",
		ExternalID: "object", Name: "web", Namespace: "project", DiscoveredProtocol: "http", DiscoveredPort: 80,
		DesiredInstances: 1, RunningInstances: 1, ReadyInstances: 1, Status: model.ServiceAvailable,
		Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, _, _, err = reconcileEndpointsTx(ctx, store, tx, service,
		model.DiscoveredService{DesiredInstances: 1}, map[string]model.Endpoint{}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetService(ctx, service.ID)
	if err != nil || stored.Status != model.ServiceUnavailable || stored.RunningInstances != 0 || stored.Revision != service.Revision {
		t.Fatalf("endpoint observation changed configuration ETag: %#v, %v", stored, err)
	}
}

func TestReadinessRecomputeKeepsServiceETag(t *testing.T) {
	store := newSQLiteStore(t)
	ctx, runtime, now := managedRuntimeFixture(t, store)
	service := model.Service{ID: "recompute-etag", RuntimeID: runtime.ID, ServiceKey: "project/web", RuntimeObjectID: "object",
		ExternalID: "object", Name: "web", Namespace: "project", DiscoveredProtocol: "http", DiscoveredPort: 80,
		DesiredInstances: 1, RunningInstances: 1, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = recomputeServiceTx(ctx, store, tx, service.ID, now.Add(time.Duration(i+1)*time.Second)); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := store.GetService(ctx, service.ID)
	if err != nil || stored.Status != model.ServiceUnavailable || stored.Revision != service.Revision {
		t.Fatalf("readiness recompute changed configuration ETag: %#v, %v", stored, err)
	}
}

func TestDiscoveryServiceWriteFailureDoesNotClaimPersistedChange(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "insert", true: "refresh"}[existing], func(t *testing.T) {
			failure := errors.New("service persistence unavailable")
			query := "INSERT INTO services"
			current := map[string]model.Service{}
			if existing {
				query = "UPDATE services SET service_key"
				current["web"] = model.Service{ID: "retained", ServiceKey: "web", RuntimeID: "runtime", DiscoveredPort: 8080}
			}
			script := &queryScript{t: t, transactions: true, execs: []*execReply{{contains: query, err: failure}}}
			store := storeWithScript(t, script)
			tx, e := store.db.BeginTx(context.Background(), nil)
			if e != nil {
				t.Fatal(e)
			}
			_, _, e = upsertDiscoveredServiceTx(context.Background(), store, tx, model.RuntimeSnapshot{RuntimeID: "runtime", ObservedAt: time.Now()}, model.DiscoveredService{ServiceKey: "web", Name: "updated", TargetPort: 8080}, current)
			if !errors.Is(e, failure) {
				t.Fatal("discovery persistence failure hidden", e)
			}
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
			if script.commits != 0 || script.rollbacks != 1 {
				t.Fatal("failed refresh committed")
			}
		})
	}
}
