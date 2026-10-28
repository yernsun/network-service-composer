package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func keyRotationReply(t *testing.T, kind string, count int, empty bool) *queryReply {
	t.Helper()
	now := time.Now()
	q := &queryReply{contains: "FROM " + kind}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("secret-%03d", i)
		encrypted := []byte("encrypted old secret")
		if empty {
			encrypted = nil
		}
		var row []driver.Value
		switch kind {
		case "certificates":
			q.columns = strings.Split(certificateColumns, ",")
			v := model.Certificate{ID: id, PoolID: "pool", EncryptedPrivateKey: encrypted, EncryptionKeyID: "old", CreatedAt: now, UpdatedAt: now}
			row = snapshotRow(t, certificateArgs(&v))
		case "acme_accounts":
			q.columns = []string{"id", "directory", "email", "registration", "cipher", "key", "created", "updated"}
			row = []driver.Value{id, "https://acme.test/directory", "owner@example.test", []byte("{}"), encrypted, "old", now, now}
		case "application_revisions":
			q.columns = strings.Split(revisionColumns, ",")
			v := model.ApplicationRevision{ID: id, ApplicationID: "application", EncryptedCompose: encrypted, EncryptedEnvironment: encrypted, EncryptedResolved: encrypted, EncryptionKeyID: "old", CreatedAt: now}
			row = snapshotRow(t, revisionArgs(&v))
		case "deployment_operations":
			q.columns = strings.Split(operationColumns, ",")
			v := model.DeploymentOperation{ID: id, ApplicationID: "application", EncryptedInput: encrypted, EncryptionKeyID: "old", CreatedAt: now, UpdatedAt: now}
			row = snapshotRow(t, operationArgs(&v))
		}
		q.values = append(q.values, row)
	}
	return q
}
func TestSecretReencryptionReportsOnlyPersistedProgress(t *testing.T) {
	for _, kind := range []string{"certificates", "acme_accounts", "application_revisions", "deployment_operations"} {
		for _, stage := range []string{"success", "already current", "no secret", "query", "scan", "iteration", "transform", "write", "concurrent write", "affected unavailable", "second write fails", "second page"} {
			if stage == "no secret" && (kind == "acme_accounts" || kind == "application_revisions") {
				continue
			}
			t.Run(kind+"/"+stage, func(t *testing.T) {
				failure := errors.New("secret rotation interrupted")
				count := 1
				if stage == "second write fails" {
					count = 2
				}
				if stage == "second page" {
					count = 200
				}
				q := keyRotationReply(t, kind, count, stage == "no secret")
				script := &queryScript{t: t, replies: []*queryReply{q}}
				expected := count
				wantError := false
				wantCause := failure
				switch stage {
				case "query":
					q.err = failure
					wantError = true
				case "scan":
					q.columns = []string{"invalid"}
					q.values = [][]driver.Value{{nil}}
					wantError = true
					wantCause = nil
				case "iteration":
					q.rowErr = failure
					wantError = true
				case "transform", "write", "concurrent write", "affected unavailable", "second write fails":
					wantError = true
				case "already current", "no secret":
					expected = 0
				}
				if wantError {
					expected = 0
				}
				if stage == "second write fails" {
					expected = 1
				}
				if stage == "concurrent write" {
					wantCause = ErrPrecondition
				}
				readFailed := stage == "query" || stage == "scan" || stage == "iteration"
				if !readFailed && kind == "certificates" {
					for i := 0; i < count; i++ {
						script.replies = append(script.replies, boundaryEmpty("FROM certificate_domains").query)
					}
				}
				if !readFailed && stage != "transform" && stage != "already current" && stage != "no secret" {
					for i := 0; i < count; i++ {
						write := &execReply{contains: "UPDATE " + kind, affected: 1}
						switch {
						case stage == "write" || stage == "second write fails" && i == 1:
							write.err = failure
						case stage == "concurrent write":
							write.affected = 0
						case stage == "affected unavailable":
							write.affectedErr = failure
						}
						script.execs = append(script.execs, write)
					}
				}
				if stage == "second page" {
					script.replies = append(script.replies, &queryReply{contains: "FROM " + kind, columns: q.columns, args: []any{"secret-199", int64(200)}})
				}
				calls := 0
				transform := func(id, key string, cipher []byte) ([]byte, string, error) {
					calls++
					if id == "" || key != "old" || string(cipher) != "encrypted old secret" {
						t.Fatal("incorrect secret selected for rotation", id, key)
					}
					if stage == "transform" {
						return nil, "", failure
					}
					if stage == "already current" {
						return cipher, "old", nil
					}
					return []byte("encrypted new secret"), "new", nil
				}
				store := storeWithScript(t, script)
				var actual int
				var e error
				ctx := context.Background()
				switch kind {
				case "certificates":
					actual, e = store.ReencryptCertificates(ctx, func(v model.Certificate) ([]byte, string, error) {
						return transform(v.ID, v.EncryptionKeyID, v.EncryptedPrivateKey)
					})
				case "acme_accounts":
					actual, e = store.ReencryptACMEAccounts(ctx, func(v model.ACMEAccount) ([]byte, string, error) {
						return transform(v.ID, v.EncryptionKeyID, v.EncryptedPrivateKey)
					})
				case "application_revisions":
					actual, e = store.ReencryptApplicationRevisions(ctx, func(v model.ApplicationRevision) ([]byte, []byte, []byte, string, error) {
						cipher, key, e := transform(v.ID, v.EncryptionKeyID, v.EncryptedCompose)
						return cipher, cipher, cipher, key, e
					})
				case "deployment_operations":
					actual, e = store.ReencryptDeploymentOperations(ctx, func(v model.DeploymentOperation) ([]byte, string, error) {
						return transform(v.ID, v.EncryptionKeyID, v.EncryptedInput)
					})
				}
				if actual != expected || (e != nil) != wantError {
					t.Fatal("rotation progress includes failed writes", actual, expected, e)
				}
				if wantError && wantCause != nil && !errors.Is(e, wantCause) {
					t.Fatal("rotation error cause hidden", e)
				}
				if (readFailed || stage == "no secret") && calls != 0 {
					t.Fatal("rotation attempted without readable secret")
				}
			})
		}
	}
}

func TestCertificateRelinkingFailuresPreserveExistingReferences(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		t.Run(string(dialect), func(t *testing.T) {
			q := keyRotationReply(t, "certificates", 1, false)
			steps := []transactionBoundaryStep{{query: q}, boundaryEmpty("FROM certificate_domains"), boundaryWrite("DELETE FROM certificate_domains"), boundaryRead("SELECT pool_id FROM domains", []string{"pool"}, "pool"), boundaryWrite("INSERT INTO certificate_domains"), boundaryWrite("UPDATE domains SET certificate_id")}
			steps = append(steps, boundaryPoolSteps(dialect)...)
			// Lookup happens before BeginTx, so test failures inside the transaction separately.
			for fault := 2; fault <= len(steps); fault++ {
				t.Run(fmt.Sprint(fault), func(t *testing.T) {
					runTransactionBoundary(t, dialect, steps, fault, func(s *SQLStore) error {
						return s.LinkCertificateDomains(context.Background(), "secret-000", []string{"domain"})
					})
				})
			}
			runTransactionBoundary(t, dialect, steps, -1, func(s *SQLStore) error {
				return s.LinkCertificateDomains(context.Background(), "secret-000", []string{"domain"})
			})
		})
	}
}
