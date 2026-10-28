package storage

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func controlRecord(key, kind, parent, payload string, at time.Time) ControlDocument {
	return ControlDocument{ID: key, Kind: kind, ParentID: parent, Payload: json.RawMessage(payload), CreatedAt: at, UpdatedAt: at}
}

func TestControlHistoryRetentionPreservesRecoveryDependencies(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, d := range []ControlDocument{
		controlRecord("a", "revision", "gateway", `{}`, now),
		controlRecord("b", "revision", "gateway", `{}`, now),
		controlRecord("c", "revision", "gateway", `{}`, now),
		controlRecord("d", "revision", "gateway", `{}`, now),
		controlRecord("foreign-parent", "revision", "another", `{}`, now),
		controlRecord("foreign-kind", "issue", "gateway", `{}`, now),
	} {
		if err := s.WriteControlDocuments(ctx, []ControlWrite{{Document: d}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, keep := range []int{-1, 0} {
		if err := s.PruneControlHistory(ctx, "revision", "gateway", keep, nil); !errors.Is(err, ErrConflict) {
			t.Fatalf("invalid retention %d: %v", keep, err)
		}
	}
	if err := s.PruneControlHistory(ctx, "revision", "gateway", 1, []string{"b", "b", "missing"}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"b", "d", "foreign-parent", "foreign-kind"} {
		if _, err := s.GetControlDocument(ctx, key); err != nil {
			t.Fatalf("required recovery record %s lost: %v", key, err)
		}
	}
	for _, key := range []string{"a", "c"} {
		if _, err := s.GetControlDocument(ctx, key); !errors.Is(err, ErrNotFound) {
			t.Fatalf("stale record %s survived: %v", key, err)
		}
	}
	if err := s.PruneControlHistory(ctx, "revision", "gateway", 20, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneControlHistory(ctx, "revision", "empty", 1, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListControlDocuments(ctx, "revision", "gateway", Page{After: "b", Limit: 1})
	if err != nil || len(rows) != 1 || rows[0].ID != "d" {
		t.Fatalf("cursor crossed retention scope: %#v, %v", rows, err)
	}
	rows, err = s.ListControlDocuments(ctx, "revision", "", Page{Limit: 999})
	if err != nil || len(rows) != 3 {
		t.Fatalf("all-parent listing=%#v, %v", rows, err)
	}
}

func TestControlDocumentIdentityAndEncryptionConcurrency(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	owner := controlRecord("application", "application", "", `{}`, now)
	variable := controlRecord("variables", "variables", owner.ID, `{"sealed":"old"}`, now)
	if err := s.WriteControlDocuments(ctx, []ControlWrite{{Document: variable}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan variables accepted: %v", err)
	}
	if _, err := s.GetControlDocument(ctx, variable.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan persisted: %v", err)
	}
	if err := s.WriteControlDocuments(ctx, []ControlWrite{{Document: owner}, {Document: variable}}); err != nil {
		t.Fatal(err)
	}
	old, err := s.GetControlDocument(ctx, variable.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ControlDocument){func(d *ControlDocument) { d.Kind = "route" }, func(d *ControlDocument) { d.ParentID = "another" }} {
		changed := old
		mutate(&changed)
		if err := s.WriteControlDocuments(ctx, []ControlWrite{{Document: changed, Expected: 1}}); err == nil {
			t.Fatal("identity change accepted")
		}
	}
	newPayload := json.RawMessage(`{"sealed":"rotated"}`)
	if err := s.ReencryptControlDocument(ctx, old, newPayload); err != nil {
		t.Fatal(err)
	}
	rotated, err := s.GetControlDocument(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Revision != old.Revision || !rotated.UpdatedAt.Equal(old.UpdatedAt) || string(rotated.Payload) != string(newPayload) {
		t.Fatalf("rotation changed business revision: %#v", rotated)
	}
	if err := s.ReencryptControlDocument(ctx, old, json.RawMessage(`{"sealed":"stale"}`)); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale ciphertext overwritten: %v", err)
	}
	updated := rotated
	updated.Payload = json.RawMessage(`{"sealed":"user edit"}`)
	updated.UpdatedAt = now.Add(time.Second)
	if err := s.WriteControlDocuments(ctx, []ControlWrite{{Document: updated, Expected: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReencryptControlDocument(ctx, rotated, newPayload); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale revision rotation accepted: %v", err)
	}
	actual, err := s.GetControlDocument(ctx, old.ID)
	if err != nil || actual.Revision != 2 || string(actual.Payload) != string(updated.Payload) {
		t.Fatalf("new edit lost: %#v, %v", actual, err)
	}
	rollback := controlRecord("rollback", "revision", owner.ID, `{}`, now)
	if err := s.WriteControlDocuments(ctx, []ControlWrite{{Document: rollback}, {Document: owner}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate owner=%v", err)
	}
	if _, err := s.GetControlDocument(ctx, rollback.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed transaction left history: %v", err)
	}
	if err := s.WriteControlDocuments(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestControlReadsPropagateDatabaseFailures(t *testing.T) {
	failure := errors.New("database read interrupted")
	for _, operation := range []string{"list", "prune"} {
		for _, fault := range []string{"query", "scan", "stream"} {
			t.Run(operation+"/"+fault, func(t *testing.T) {
				r := &queryReply{contains: "FROM control_documents", columns: []string{"id"}}
				switch fault {
				case "query":
					r.err = failure
				case "scan":
					r.values = [][]driver.Value{{nil}}
				case "stream":
					r.rowErr = failure
				}
				s := scriptedStore(t, r)
				var err error
				if operation == "prune" {
					err = s.PruneControlHistory(context.Background(), "revision", "gateway", 1, nil)
				} else {
					_, err = s.ListControlDocuments(context.Background(), "revision", "", Page{})
				}
				if fault == "scan" {
					if err == nil || !strings.Contains(err.Error(), "sql:") {
						t.Fatalf("scan failure=%v", err)
					}
				} else if !errors.Is(err, failure) {
					t.Fatalf("read error lost: %v", err)
				}
			})
		}
	}
	t.Run("closed database cannot accept writes", func(t *testing.T) {
		s := newSQLiteStore(t)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		d := controlRecord("doc", "revision", "gateway", `{}`, time.Now().UTC())
		for _, operation := range []func() error{
			func() error { return s.WriteControlDocuments(context.Background(), []ControlWrite{{Document: d}}) },
			func() error { return s.ReencryptControlDocument(context.Background(), d, json.RawMessage(`{}`)) },
		} {
			if err := operation(); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatalf("unavailable database reported write success: %v", err)
			}
		}
	})
}

func TestGroupKeyRejectsMalformedAndForeignRuntimeIdentity(t *testing.T) {
	for _, mode := range []string{"docker", "swarm"} {
		for _, namespace := range []string{"", "项目/space", "same-name"} {
			parts, err := ParseGroupKey(GroupKey("runtime", mode, namespace))
			if err != nil || !reflect.DeepEqual(parts, []string{"runtime", mode, namespace}) {
				t.Fatalf("identity roundtrip=%#v, %v", parts, err)
			}
		}
	}
	for _, key := range []string{"!", "bm90LWpzb24", "bnVsbA", "W10", GroupKey("", "docker", ""), GroupKey("runtime", "kubernetes", "")} {
		if parts, err := ParseGroupKey(key); err == nil || parts != nil {
			t.Fatalf("invalid key %q accepted: %#v, %v", key, parts, err)
		}
	}
}

func TestControlWriteFailureRollsBackWithoutClaimingCommit(t *testing.T) {
	failure := errors.New("database write acknowledgement failed")
	for _, tc := range []struct {
		name         string
		pool         bool
		reply        *execReply
		commitErr    error
		wantRollback bool
	}{
		{"pool write", true, &execReply{contains: "UPDATE gateway_pools", err: failure}, nil, true},
		{"pool affected count", true, &execReply{contains: "UPDATE gateway_pools", affectedErr: failure}, nil, true},
		{"document affected count", false, &execReply{contains: "UPDATE control_documents", affectedErr: failure}, nil, true},
		{"document write", false, &execReply{contains: "UPDATE control_documents", err: failure}, nil, true},
		{"commit", false, &execReply{contains: "UPDATE control_documents", affected: 1}, failure, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &queryScript{t: t, transactions: true, execs: []*execReply{tc.reply}, commitErr: tc.commitErr}
			s := storeWithScript(t, script)
			d := controlRecord("draft", "draft", "gateway", `{}`, time.Now().UTC())
			var err error
			if tc.pool {
				err = s.PublishNginxDocuments(context.Background(), "pool", 1, []ControlWrite{{Document: d}})
			} else {
				err = s.WriteControlDocuments(context.Background(), []ControlWrite{{Document: d, Expected: 1}})
			}
			if !errors.Is(err, failure) {
				t.Fatalf("write failure lost: %v", err)
			}
			if tc.wantRollback {
				if script.rollbacks != 1 || script.commits != 0 {
					t.Fatalf("failed write committed or leaked transaction: %#v", script)
				}
			} else if script.commits != 1 {
				t.Fatal("commit result was not observed")
			}
		})
	}
	t.Run("history delete error", func(t *testing.T) {
		script := &queryScript{t: t, replies: []*queryReply{{contains: "SELECT id", columns: []string{"id"}, values: [][]driver.Value{{"new"}, {"old"}}}}, execs: []*execReply{{contains: "DELETE FROM control_documents", err: failure}}}
		if err := storeWithScript(t, script).PruneControlHistory(context.Background(), "revision", "gateway", 1, nil); !errors.Is(err, failure) {
			t.Fatalf("cleanup failure hidden: %v", err)
		}
	})
	for _, dialect := range []Dialect{Postgres, MySQL} {
		t.Run(string(dialect)+" serializes variable owner", func(t *testing.T) {
			script := &queryScript{t: t, transactions: true, replies: []*queryReply{{contains: "FOR UPDATE", columns: []string{"id"}, values: [][]driver.Value{{"application"}}}}, execs: []*execReply{{contains: "INSERT INTO control_documents", affected: 1}}}
			s := storeWithScript(t, script)
			s.dialect = dialect
			d := controlRecord("variables", "variables", "application", `{}`, time.Now().UTC())
			if err := s.WriteControlDocuments(context.Background(), []ControlWrite{{Document: d}}); err != nil {
				t.Fatal(err)
			}
			if script.commits != 1 || script.rollbacks != 0 {
				t.Fatal("successful owner-locked write did not commit")
			}
		})
	}
}
