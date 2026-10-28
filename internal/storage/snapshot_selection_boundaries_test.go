package storage

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
)

func snapshotSetField(t *testing.T, reply *queryReply, row int, column string, value driver.Value) {
	t.Helper()
	for i, name := range reply.columns {
		if strings.TrimSpace(name) == column {
			reply.values[row][i] = value
			return
		}
	}
	t.Fatal("fixture column absent", column)
}
func TestSnapshotEligibilityAndUpstreamIdentityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*testing.T, []*queryReply)
		routes  int
		failure bool
	}{
		{"disabled service", func(t *testing.T, q []*queryReply) { snapshotSetField(t, q[3], 0, "enabled_override", false) }, 0, false},
		{"invalid service configuration", func(t *testing.T, q []*queryReply) { snapshotSetField(t, q[3], 0, "configuration_error", "invalid") }, 0, false},
		{"DNS collision", func(t *testing.T, q []*queryReply) { snapshotSetField(t, q[3], 0, "dns_status", "CONFLICT") }, 0, false},
		{"missing domain", func(t *testing.T, q []*queryReply) { snapshotSetField(t, q[4], 0, "domain_id", "missing") }, 0, false},
		{"missing service", func(t *testing.T, q []*queryReply) { snapshotSetField(t, q[4], 0, "service_id", "missing") }, 0, false},
		{"missing DNS identity", func(t *testing.T, q []*queryReply) {
			snapshotSetField(t, q[3], 0, "dns_name", "")
			snapshotSetField(t, q[3], 0, "qualified_name", "")
		}, 0, false},
		{"paused invalid target retained", func(t *testing.T, q []*queryReply) {
			snapshotSetField(t, q[4], 0, "route_status", "PAUSED")
			snapshotSetField(t, q[3], 0, "dns_status", "CONFLICT")
		}, 1, false},
		{"invalid port", func(t *testing.T, q []*queryReply) { snapshotSetField(t, q[4], 0, "target_port", int64(70000)) }, 0, true},
		{"invalid protocol", func(t *testing.T, q []*queryReply) { snapshotSetField(t, q[4], 0, "upstream_protocol", "tcp") }, 0, true},
		{"canonical upstream collision", func(t *testing.T, q []*queryReply) {
			q[3].values = append(q[3].values, append([]driver.Value(nil), q[3].values[0]...))
			snapshotSetField(t, q[3], 1, "id", "second")
			snapshotSetField(t, q[3], 1, "dns_name", "different.internal")
			q[4].values = append(q[4].values, append([]driver.Value(nil), q[4].values[0]...))
			snapshotSetField(t, q[4], 1, "id", "second-route")
			snapshotSetField(t, q[4], 1, "service_id", "second")
			snapshotSetField(t, q[4], 1, "path", "/api/")
		}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := snapshotReadReplies(t)
			tc.change(t, replies)
			if tc.failure {
				replies = replies[:5]
			}
			snapshot, e := scriptedStore(t, replies...).BuildSnapshot(context.Background(), "pool")
			if tc.failure {
				if e == nil || snapshot.PoolID != "" {
					t.Fatal("unsafe upstream identity accepted", snapshot, e)
				}
				return
			}
			if e != nil || len(snapshot.Routes) != tc.routes || len(snapshot.Services) != tc.routes || len(snapshot.Domains) != 1 {
				t.Fatal("ineligible route affected snapshot or removed its domain", snapshot, e)
			}
			if tc.routes == 1 && !snapshot.Routes[0].Paused {
				t.Fatal("paused route became active")
			}
		})
	}
}
func TestSnapshotOrderingSelectsOneDeterministicRoutePerLocation(t *testing.T) {
	replies := snapshotReadReplies(t)
	original := append([]driver.Value(nil), replies[4].values[0]...)
	replies[4].values = nil
	for _, tc := range []struct {
		id, path, source string
		priority         int64
	}{
		{"z-api", "/api/", "API", 10}, {"a-api", "/api/", "API", 10}, {"lower-api", "/api/", "API", 9}, {"discovery", "/api/", "DISCOVERY", 100}, {"root", "/", "API", 0}, {"deep", "/api/v2/", "API", 0},
	} {
		replies[4].values = append(replies[4].values, append([]driver.Value(nil), original...))
		i := len(replies[4].values) - 1
		snapshotSetField(t, replies[4], i, "id", tc.id)
		snapshotSetField(t, replies[4], i, "path", tc.path)
		snapshotSetField(t, replies[4], i, "source", tc.source)
		snapshotSetField(t, replies[4], i, "priority", tc.priority)
	}
	snapshot, e := scriptedStore(t, replies...).BuildSnapshot(context.Background(), "pool")
	if e != nil || len(snapshot.Routes) != 3 || len(snapshot.Services) != 1 {
		t.Fatal("duplicate matches or duplicate services in snapshot", snapshot, e)
	}
	for i, want := range []string{"deep", "a-api", "root"} {
		if snapshot.Routes[i].ID != want {
			t.Fatalf("matching precedence lost: route %d=%s want %s", i, snapshot.Routes[i].ID, want)
		}
	}
}
