package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/model"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestGatewayMonitorReconcilesAllPoolsAndPublishesLeadershipChanges(t *testing.T) {
	_, store, _, hello := gatewayHandshakeFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	// Include more than one page without introducing live Gateway connections.
	tx, e := store.DB().BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 201; i++ {
		_, e = tx.ExecContext(ctx, `INSERT INTO gateway_pools(id,name,backend_network,challenge_upstream,enabled,desired_revision,probe_revision,probe_epoch,probe_gateway_id,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("pool-page-%03d", i), fmt.Sprintf("pool %d", i), "ingress", "http://controller:8080", true, 1, 1, 0, "", 1, now, now)
		if e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	g := model.Gateway{ID: hello.GatewayId, PoolID: hello.PoolId, Enabled: true, Status: model.GatewayReady, LastSeenAt: &now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if e = store.UpsertGateway(ctx, &g); e != nil {
		t.Fatal(e)
	}
	broker := NewBroker()
	events, unsubscribe := broker.Subscribe(hello.PoolId)
	defer unsubscribe()
	monitor := NewMonitor(store, time.Minute, broker, nil)
	if e = monitor.reconcileProbeLeaders(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case event := <-events:
		if event.PoolID != hello.PoolId || event.Kind != "probe-leader" {
			t.Fatal("incorrect leadership notification", event)
		}
	default:
		t.Fatal("leadership change not published")
	}
	pool, e := store.GetPool(ctx, hello.PoolId)
	if e != nil || pool.ProbeGatewayID != g.ID || pool.ProbeEpoch != 1 {
		t.Fatalf("leader not persisted: %+v %v", pool, e)
	}
	if e = monitor.reconcileProbeLeaders(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-events:
		t.Fatal("unchanged leadership repeatedly published")
	default:
	}
	g.Enabled = false
	if e = store.UpdateGateway(ctx, &g, g.Revision); e != nil {
		t.Fatal(e)
	}
	monitor.broker = nil
	if e = monitor.reconcileProbeLeaders(ctx); e != nil {
		t.Fatal(e)
	}
	pool, e = store.GetPool(ctx, pool.ID)
	if e != nil || pool.ProbeGatewayID != "" || pool.ProbeEpoch != 2 {
		t.Fatal("disabled leader retained without broker", e)
	}
	if _, e = store.DB().Exec(`DROP TABLE gateways`); e != nil {
		t.Fatal(e)
	}
	if e = monitor.reconcileProbeLeaders(ctx); e == nil {
		t.Fatal("leader-selection database failure hidden")
	}
	store.Close()
	if e = monitor.reconcileProbeLeaders(ctx); e == nil {
		t.Fatal("pool-list database failure hidden")
	}
}
func TestGatewayMonitorCancellationAndOfflineEvidence(t *testing.T) {
	for _, stage := range []string{"already canceled", "offline", "storage unavailable"} {
		t.Run(stage, func(t *testing.T) {
			_, store, _, hello := gatewayHandshakeFixture(t)
			var logs bytes.Buffer
			monitor := NewMonitor(store, 0, nil, slog.New(slog.NewTextHandler(&logs, nil)))
			if monitor.timeout != 15*time.Second {
				t.Fatal("default offline timeout lost")
			}
			monitor.timeout = 10 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "already canceled" {
				cancel()
			} else if stage == "storage unavailable" {
				store.Close()
				time.AfterFunc(1100*time.Millisecond, cancel)
			} else {
				old := time.Now().Add(-time.Hour)
				g := model.Gateway{ID: hello.GatewayId, PoolID: hello.PoolId, Enabled: true, Status: model.GatewayReady, LastSeenAt: &old, Revision: 1, CreatedAt: old, UpdatedAt: old}
				if e := store.UpsertGateway(ctx, &g); e != nil {
					t.Fatal(e)
				}
				time.AfterFunc(1100*time.Millisecond, cancel)
			}
			if e := monitor.Run(ctx); !errors.Is(e, context.Canceled) {
				t.Fatal("monitor cancellation lost", e)
			}
			if stage == "offline" {
				g, e := store.GetGateway(context.Background(), hello.GatewayId)
				if e != nil || g.Status != model.GatewayOffline || !strings.Contains(logs.String(), "gateways marked offline") {
					t.Fatal("expired gateway stayed ready", e)
				}
			}
			if stage == "storage unavailable" && (!strings.Contains(logs.String(), "mark offline gateways") || !strings.Contains(logs.String(), "reconcile gateway probe leaders")) {
				t.Fatal("independent database failures not reported")
			}
		})
	}
}
func TestBrokerSubscriptionIsolationAndBackpressure(t *testing.T) {
	broker := NewBroker()
	first, stopFirst := broker.Subscribe("pool")
	second, stopSecond := broker.Subscribe("pool")
	foreign, stopForeign := broker.Subscribe("other")
	for i := int64(1); i <= 3; i++ {
		broker.Publish(model.StateChange{PoolID: "pool", Revision: i})
	}
	for _, events := range []<-chan model.StateChange{first, second} {
		select {
		case event := <-events:
			if event.Revision != 1 {
				t.Fatal("queued event overwritten")
			}
		default:
			t.Fatal("subscriber missed event")
		}
	}
	select {
	case <-foreign:
		t.Fatal("cross-pool notification leaked")
	default:
	}
	stopFirst()
	stopFirst()
	broker.Publish(model.StateChange{PoolID: "pool", Revision: 4})
	select {
	case <-first:
		t.Fatal("unsubscribed listener received event")
	default:
	}
	select {
	case e := <-second:
		if e.Revision != 4 {
			t.Fatal(e)
		}
	default:
		t.Fatal("remaining listener removed")
	}
	stopSecond()
	stopForeign()
	stopForeign()
}
