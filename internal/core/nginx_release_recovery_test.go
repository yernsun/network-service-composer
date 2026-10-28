package core

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestCompletedNginxReleaseResendsFrozenConfigurationWithoutRewritingHistory(t *testing.T) {
	c, release := releaseFixture(t, 2)
	ctx := context.Background()
	var expected *model.Snapshot
	for _, target := range release.Targets {
		snapshot, managed, err := c.ClaimNginxRelease(ctx, release.PoolID, target.GatewayID, release.ConfigRevision)
		if err != nil || !managed || snapshot == nil {
			t.Fatal("initial publication was not claimed", err)
		}
		expected = snapshot
		if err = c.MarkNginxReleaseSent(ctx, release.PoolID, target.GatewayID, release.ConfigRevision, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if err = c.RecordNginxReleaseResult(ctx, release.PoolID, target.GatewayID, release.ConfigRevision, "SUCCEEDED", strings.Repeat("a", 64), strings.Repeat("b", 64), ""); err != nil {
			t.Fatal(err)
		}
	}
	completed, err := c.GetNginxRelease(ctx, release.PoolID, release.ID)
	if err != nil || completed.Status != "SUCCEEDED" || completed.FinishedAt == nil {
		t.Fatal("publication did not finish", completed, err)
	}
	before, err := c.store.GetControlDocument(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, gateway := range []string{release.Targets[0].GatewayID, release.Targets[1].GatewayID, "new-gateway"} {
		snapshot, managed, err := c.ClaimNginxRelease(ctx, release.PoolID, gateway, release.ConfigRevision)
		if err != nil || !managed || snapshot == nil || !reflect.DeepEqual(snapshot, expected) {
			t.Fatal("reconnecting Gateway did not receive the published frozen configuration", gateway, snapshot, err)
		}
		if err = c.MarkNginxReleaseSent(ctx, release.PoolID, gateway, release.ConfigRevision, strings.Repeat("a", 64)); err != nil {
			t.Fatal("completed configuration could not be sent", err)
		}
		for _, result := range []string{"SUCCEEDED", "FAILED", "UNKNOWN"} {
			if err = c.RecordNginxReleaseResult(ctx, release.PoolID, gateway, release.ConfigRevision, result, strings.Repeat("a", 64), strings.Repeat("c", 64), "recovery result"); err != nil {
				t.Fatal(err)
			}
		}
	}
	after, err := c.store.GetControlDocument(ctx, release.ID)
	if err != nil || before.Revision != after.Revision || !bytes.Equal(before.Payload, after.Payload) {
		t.Fatal("resynchronization rewrote historical publication evidence", err)
	}
	pool, err := c.store.GetPool(ctx, release.PoolID)
	if err != nil || pool.DesiredRevision != release.ConfigRevision {
		t.Fatal("recovery changed the desired configuration revision", err)
	}

	// Losing the frozen snapshot must fail recovery rather than rebuilding a
	// potentially different model from today's discovery and draft state.
	c.store = &nginxBoundaryStore{Store: c.store, method: "document", key: release.ID + ":snapshot", failure: storage.ErrNotFound}
	snapshot, managed, err := c.ClaimNginxRelease(ctx, release.PoolID, release.Targets[0].GatewayID, release.ConfigRevision)
	if snapshot != nil || !managed || !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("missing published snapshot bypassed recovery protection", snapshot, managed, err)
	}
}
