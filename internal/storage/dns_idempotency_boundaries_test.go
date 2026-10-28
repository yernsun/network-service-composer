package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestGatewayDNSAddressBoundaries(t *testing.T) {
	for _, tc := range []struct{ address, kind, target string }{
		{"192.0.2.1", "A", "192.0.2.1"}, {"::ffff:192.0.2.1", "A", "192.0.2.1"}, {"2001:db8::1", "AAAA", "2001:db8::1"},
		{strings.Repeat("a.", 127), "CNAME", strings.TrimSuffix(strings.Repeat("a.", 127), ".")},
		{"EDGE-09.Example.TEST.", "CNAME", "edge-09.example.test"}, {strings.Repeat("a", 63) + ".test", "CNAME", strings.Repeat("a", 63) + ".test"},
	} {
		kind, target, ok := gatewayDNSRecord(tc.address)
		if !ok || kind != tc.kind || target != tc.target {
			t.Fatalf("DNS address %q: %s %q %v", tc.address, kind, target, ok)
		}
	}
	for _, address := range []string{"", ".", "a..test", strings.Repeat("a", 64) + ".test", strings.Repeat("a.", 128), "-edge.test", "edge-.test", "é.test", "edge_1.test", "edge{.test", "edge!.test", "1:80", "edge.test ", "edge/test"} {
		kind, target, ok := gatewayDNSRecord(address)
		if ok || kind != "" || target != "" {
			t.Fatalf("invalid DNS address %q yielded a usable record: %s %q", address, kind, target)
		}
	}
}

func TestDeploymentIdempotencyPreservesRequestIdentity(t *testing.T) {
	now := time.Now().UTC()
	original := model.DeploymentOperation{ID: "accepted", RuntimeID: "runtime", ApplicationID: "app", ServiceID: "service", RevisionID: "revision", Type: "UPDATE", IdempotencyKey: "request", Status: model.OperationQueued, Revision: 1, CreatedAt: now, UpdatedAt: now}
	for _, tc := range []struct {
		name    string
		change  func(*model.DeploymentOperation)
		oldHash string
		same    bool
	}{
		{"legacy retry", func(*model.DeploymentOperation) {}, "", true},
		{"legacy type conflict", func(v *model.DeploymentOperation) { v.Type = "DELETE" }, "", false},
		{"legacy application conflict", func(v *model.DeploymentOperation) { v.ApplicationID = "other" }, "", false},
		{"legacy service conflict", func(v *model.DeploymentOperation) { v.ServiceID = "other" }, "", false},
		{"legacy revision conflict", func(v *model.DeploymentOperation) { v.RevisionID = "other" }, "", false},
		{"hash retry", func(v *model.DeploymentOperation) { v.RequestSHA256 = "hash" }, "hash", true},
		{"changed hash", func(v *model.DeploymentOperation) { v.RequestSHA256 = "other" }, "hash", false},
		{"missing retry hash", func(v *model.DeploymentOperation) { v.RequestSHA256 = "" }, "hash", false},
		{"new hash on legacy request", func(v *model.DeploymentOperation) { v.RequestSHA256 = "hash" }, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := original
			existing.RequestSHA256 = tc.oldHash
			candidate := existing
			candidate.ID = "retry"
			tc.change(&candidate)
			script := &queryScript{t: t, replies: []*queryReply{{contains: "FROM deployment_operations WHERE runtime_id", columns: strings.Split(strings.ReplaceAll(operationColumns, "\n", ""), ","), values: [][]driver.Value{snapshotRow(t, operationArgs(&existing))}}}}
			store := storeWithScript(t, script)
			got, e := store.CreateDeploymentOperation(context.Background(), &candidate)
			if tc.same {
				if e != nil || got.ID != existing.ID || got.Revision != existing.Revision {
					t.Fatal("retry did not retain the accepted operation", got, e)
				}
			} else if !errors.Is(e, ErrConflict) || got.ID != "" {
				t.Fatal("conflicting request was accepted", got, e)
			}
			if script.execCalls != 0 || candidate.ID != "retry" {
				t.Fatal("idempotency lookup wrote data or changed the candidate")
			}
		})
	}
	foreign := original
	foreign.RuntimeID = "other"
	if sameIdempotentOperation(original, foreign) {
		t.Fatal("identical requests across runtimes share ownership")
	}
}
