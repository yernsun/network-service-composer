package cluster

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	"testing"
	"time"
)

func TestSummaryDeduplicatesPhysicalNodes(t *testing.T) {
	m, store, client := fixtureMonitor()
	rt := store.Runtimes[0]
	rt.ID = "rt-docker"
	rt.Type = model.RuntimeDocker
	store.Runtimes = append(store.Runtimes, rt)
	duplicate := rt
	duplicate.ID = "rt-alias"
	duplicate.Endpoint = "tcp://alias:2376"
	store.Runtimes = append(store.Runtimes, duplicate)
	unknown := rt
	unknown.ID = "rt-unknown"
	unknown.Endpoint = "tcp://unknown:2376"
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.runtimes[unknown.ID] = unknown
	summary, rows := m.Summary()
	if summary.Nodes != 2 || summary.Managers != 1 || summary.ConnectedNodes != 1 || summary.UnknownRuntimes != 1 || summary.Complete || len(rows) != 4 {
		t.Fatalf("summary=%+v rows=%+v", summary, rows)
	}
	before := client.StatsCalls
	_, _ = m.Summary()
	if client.StatsCalls != before {
		t.Fatal("summary contacted Docker")
	}
	client.Failure = errors.New("offline")
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	summary, _ = m.Summary()
	if summary.Nodes != 2 || summary.StaleNodes != 2 || summary.ConnectedNodes != 0 || summary.Complete {
		t.Fatalf("stale=%+v", summary)
	}
	m.now = func() time.Time { return time.Now().Add(time.Hour) }
	summary, _ = m.Summary()
	if summary.StaleNodes != 2 {
		t.Fatal(summary)
	}
}
