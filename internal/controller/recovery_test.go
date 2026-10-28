package controller

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/recovery"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type recoveryUnexpectedWorker struct{ called bool }

func (w *recoveryUnexpectedWorker) Run(context.Context) error { w.called = true; return nil }

func TestLeaderRechecksPreparationBarrierBeforeStartingAnyWorker(t *testing.T) {
	cfg := controllerConfigFixture(t)
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = recovery.Prepare(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	worker := &recoveryUnexpectedWorker{}
	c := &Controller{store: store, background: []Background{worker}}
	e := controlplane.Election{Identity: controlplane.Identity{ID: "late-start"}, Locker: lease.New(nil, storage.SQLite), Store: controlplane.SQLState{Store: store}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = e.Run(ctx, c.runLeader)
	if err == nil || !strings.Contains(err.Error(), "held for recovery") || worker.called {
		t.Fatal("late leader started restored workers", err, worker.called)
	}
}

func TestRecoveryControllerDoesNotInitializeWorkersOrPublishReadiness(t *testing.T) {
	cfg := controllerConfigFixture(t)
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = recovery.Prepare(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	before, err := storage.RecoveryFingerprint(context.Background(), store.DB())
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	cfg.Controller.RecoveryMode = true
	cfg.Storage.AutoMigrate = false
	cfg.Controller.LocalGateway = true
	cfg.Controller.Internal.Listen = "unusable recovery internal listener"
	cfg.Server.GRPCListen = "unusable recovery gateway listener"
	cfg.Server.HTTPListen = controllerAddress(t)
	cfg.Security.SessionKey = strings.Repeat("s", 32)
	c, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.background) != 0 || c.election != nil || c.gatewayRPC != nil || c.nodeReports != nil || c.localCollector != nil || c.localGateway != nil || c.internalTLS != nil {
		t.Fatal("recovery initialized a mutation-capable subsystem")
	}
	if c.isReady() {
		t.Fatal("recovery reported normal readiness")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get("http://" + cfg.Server.HTTPListen + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("recovery health", response.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("read-only inspection server did not start", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := client.Get("http://" + cfg.Server.HTTPListen + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatal("production LB could admit recovery controller", response.StatusCode)
	}
	client.CloseIdleConnections()
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not stop")
	}
	store, err = storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after, err := storage.RecoveryFingerprint(context.Background(), store.DB())
	if err != nil || after != before {
		t.Fatal("recovery startup seeded or changed the restore", err)
	}
}

func TestRecoveryStartupRejectsUnpreparedOrAutomaticActivation(t *testing.T) {
	cfg := controllerConfigFixture(t)
	cfg.Controller.RecoveryMode = true
	if c, err := New(context.Background(), cfg, nil); err == nil || c != nil {
		t.Fatal("recovery allowed automatic migration")
	}
	cfg.Storage.AutoMigrate = false
	if c, err := New(context.Background(), cfg, nil); err == nil || c != nil {
		t.Fatal("recovery allowed startup without persistent prepare barrier")
	}
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = recovery.Prepare(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	store.Close()
	cfg.Controller.RecoveryMode = false
	cfg.Storage.AutoMigrate = true
	if c, err := New(context.Background(), cfg, nil); err == nil || c != nil {
		t.Fatal("normal restart escaped durable recovery review")
	}
}
