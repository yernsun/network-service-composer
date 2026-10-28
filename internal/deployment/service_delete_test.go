package deployment

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestDeleteManagedServicePublishesOnlyOmissionAndPreservesVolume(t *testing.T) {
	fixture := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n  db:\n    image: postgres:17\n    volumes: [data:/var/lib/postgresql/data]\nvolumes:\n  data: {}\n")
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		if strings.Contains(strings.Join(call.arguments, " "), " ps --all --quiet web") {
			return "container-web", nil
		}
		return "ok", nil
	}}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: app.ID}, storage.Page{Limit: 10})
	if err != nil || len(services) != 2 {
		t.Fatalf("services: %#v %v", services, err)
	}
	var web model.Service
	for _, service := range services {
		if service.Name == "web" {
			web = service
		}
	}
	if web.ID == "" {
		t.Fatal("web service missing")
	}
	if _, err = fixture.core.QueueDeleteService(context.Background(), web.ID, "test", "no-ack", web.Revision, false); err == nil {
		t.Fatal("missing acknowledgement accepted")
	}
	if _, err = fixture.core.QueueDeleteService(context.Background(), web.ID, "test", "stale-delete", web.Revision+1, true); err == nil {
		t.Fatal("stale deletion accepted")
	}
	op, err := fixture.core.QueueDeleteService(context.Background(), web.ID, "test", "delete-web", web.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.store.GetDeploymentOperation(context.Background(), op.ID)
	if err != nil || stored.Status != model.OperationSucceeded {
		t.Fatalf("deletion: %#v %v", stored, err)
	}
	removed, err := fixture.store.GetService(context.Background(), web.ID)
	if err != nil || removed.Status != model.ServiceRemoved || removed.RemovedAt == nil {
		t.Fatalf("removed: %#v %v", removed, err)
	}
	source, err := fixture.core.ApplicationSource(context.Background(), app.ID, "")
	if err != nil || strings.Contains(source.ComposeYAML, "web:") || !strings.Contains(source.ComposeYAML, "db:") || !strings.Contains(source.ComposeYAML, "data:") {
		t.Fatalf("saved source: %s %v", source.ComposeYAML, err)
	}
	for _, call := range runner.calls[1:] {
		args := strings.Join(call.arguments, " ")
		if strings.Contains(args, "--volumes") || strings.Contains(args, " down ") || strings.Contains(args, "up -d") {
			t.Fatalf("deletion touched retained workload/data: %s", args)
		}
	}
	replayed, err := fixture.core.QueueDeleteService(context.Background(), web.ID, "test", "delete-web", web.Revision, true)
	if err != nil || replayed.ID != op.ID {
		t.Fatalf("idempotent replay: %#v %v", replayed, err)
	}
}

func TestDeleteManagedServiceRuntimeFailureKeepsActiveRevision(t *testing.T) {
	fixture := newWorkerFixture(t)
	app, _ := installFixtureApplication(t, fixture, "services:\n  web:\n    image: nginx:1.29-alpine\n  db:\n    image: postgres:17\n    volumes: [data:/var/lib/postgresql/data]\nvolumes:\n  data: {}\n")
	runner := &recordingRunner{run: func(call runnerCall) (string, error) {
		command := strings.Join(call.arguments, " ")
		if strings.Contains(command, " ps --all --quiet web") {
			return "container-web", nil
		}
		if strings.Contains(command, " rm --stop --force web") {
			return "Docker unavailable", errors.New("Docker unavailable")
		}
		return "ok", nil
	}}
	worker := NewWorkerWithRunner(fixture.store, lease.New(fixture.store.DB(), fixture.store.Dialect()), fixture.keyring, fixture.config, slog.New(slog.NewTextHandler(io.Discard, nil)), runner)
	if err := worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := fixture.store.GetApplication(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	services, err := fixture.store.ListServicesFiltered(context.Background(), storage.ServiceFilter{ApplicationID: app.ID}, storage.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var web model.Service
	for _, item := range services {
		if item.Name == "web" {
			web = item
		}
	}
	op, err := fixture.core.QueueDeleteService(context.Background(), web.ID, "test", "delete-failure", web.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.claimAndRun(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed, err := fixture.store.GetDeploymentOperation(context.Background(), op.ID)
	if err != nil || failed.Status != model.OperationFailed {
		t.Fatalf("failed operation: %#v %v", failed, err)
	}
	after, err := fixture.store.GetApplication(context.Background(), app.ID)
	if err != nil || after.ActiveRevisionID != before.ActiveRevisionID {
		t.Fatalf("active revision changed after Docker failure: %#v %v", after, err)
	}
	source, err := fixture.core.ApplicationSource(context.Background(), app.ID, "")
	if err != nil || !strings.Contains(source.ComposeYAML, "web:") || !strings.Contains(source.ComposeYAML, "data:") {
		t.Fatalf("active source lost after Docker failure: %s %v", source.ComposeYAML, err)
	}
}
