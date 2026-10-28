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

type transactionBoundaryStep struct {
	query *queryReply
	write *execReply
}

func boundaryRead(query string, columns []string, values ...driver.Value) transactionBoundaryStep {
	return transactionBoundaryStep{query: &queryReply{contains: query, columns: columns, values: [][]driver.Value{values}}}
}
func boundaryWrite(query string) transactionBoundaryStep {
	return transactionBoundaryStep{write: &execReply{contains: query, affected: 1}}
}
func boundaryPoolSteps(dialect Dialect) []transactionBoundaryStep {
	steps := []transactionBoundaryStep{
		boundaryRead("SELECT desired_revision FROM gateway_pools", []string{"desired_revision"}, int64(7)),
		boundaryWrite("UPDATE gateway_pools SET desired_revision"),
		boundaryWrite("UPDATE gateways SET desired_revision"),
	}
	if dialect == Postgres {
		steps = append(steps, boundaryWrite("pg_notify"))
	}
	return steps
}
func boundaryDomainRead(t *testing.T, domain model.Domain) transactionBoundaryStep {
	t.Helper()
	values := []driver.Value{}
	for _, value := range domainArgs(&domain) {
		v, err := driver.DefaultParameterConverter.ConvertValue(value)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, v)
	}
	return boundaryRead("FROM domains WHERE id=", strings.Split(domainColumns, ","), values...)
}

// Fail each database boundary independently. Earlier writes must never commit
// after a later read/write failure, and the original error remains observable.
func runTransactionBoundary(t *testing.T, dialect Dialect, steps []transactionBoundaryStep, fault int, run func(*SQLStore) error) {
	t.Helper()
	failure := errors.New("domain certificate transaction interrupted")
	script := &queryScript{t: t, transactions: true}
	for i, step := range steps {
		if step.query != nil {
			r := *step.query
			if i == fault {
				r.err = failure
			}
			script.replies = append(script.replies, &r)
		} else {
			w := *step.write
			if i == fault {
				w.err = failure
			}
			script.execs = append(script.execs, &w)
		}
		if i == fault {
			break
		}
	}
	if fault == len(steps) {
		script.commitErr = failure
	}
	store := storeWithScript(t, script)
	store.dialect = dialect
	err := run(store)
	if fault < 0 {
		if err != nil || script.commits != 1 || script.rollbacks != 0 {
			t.Fatalf("successful transaction = %v commits=%d rollbacks=%d", err, script.commits, script.rollbacks)
		}
	} else {
		if !errors.Is(err, failure) {
			t.Fatalf("failure %d returned %v", fault, err)
		}
		if fault < len(steps) && (script.commits != 0 || script.rollbacks != 1) {
			t.Fatalf("partial transaction committed: commits=%d rollbacks=%d", script.commits, script.rollbacks)
		}
		if fault == len(steps) && script.commits != 1 {
			t.Fatal("commit failure was not exercised")
		}
	}
}

func TestDomainAndCertificateTransactionsFailAtomically(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, operation := range []string{"create domain", "update domain", "create managed", "provision managed", "install custom", "activate certificate", "create certificate"} {
			t.Run(string(dialect)+"/"+operation, func(t *testing.T) {
				now := time.Now().UTC()
				domain := model.Domain{ID: "domain", PoolID: "pool", Host: "app.example.test", TLSMode: model.TLSManaged, CertificateID: "certificate", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
				certificate := model.Certificate{ID: "certificate", PoolID: "pool", Provider: "acme", Status: model.CertificateActive, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
				var steps []transactionBoundaryStep
				var run func(*SQLStore) error
				switch operation {
				case "create domain":
					steps = []transactionBoundaryStep{boundaryWrite("INSERT INTO domains"), boundaryRead("SELECT pool_id FROM certificates", []string{"pool_id"}, "pool"), boundaryWrite("INSERT INTO certificate_domains")}
					steps = append(steps, boundaryPoolSteps(dialect)...)
					run = func(s *SQLStore) error { v := domain; return s.CreateDomain(context.Background(), &v) }
				case "update domain":
					steps = []transactionBoundaryStep{boundaryRead("SELECT certificate_id FROM domains", []string{"certificate_id"}, "old"), boundaryRead("SELECT pool_id FROM certificates", []string{"pool_id"}, "pool"), boundaryWrite("UPDATE domains SET host"), boundaryWrite("DELETE FROM certificate_domains"), boundaryWrite("UPDATE certificates SET status"), boundaryWrite("INSERT INTO certificate_domains")}
					steps = append(steps, boundaryPoolSteps(dialect)...)
					run = func(s *SQLStore) error { v := domain; return s.UpdateDomain(context.Background(), &v, 1) }
				case "create managed":
					steps = []transactionBoundaryStep{boundaryWrite("INSERT INTO certificates"), boundaryWrite("INSERT INTO domains"), boundaryWrite("INSERT INTO certificate_domains")}
					steps = append(steps, boundaryPoolSteps(dialect)...)
					steps = append(steps, boundaryWrite("UPDATE certificates SET distribution_revision"))
					run = func(s *SQLStore) error {
						d, c := domain, certificate
						return s.CreateManagedDomain(context.Background(), &d, &c)
					}
				case "provision managed":
					domain.CertificateID = ""
					steps = []transactionBoundaryStep{boundaryDomainRead(t, domain), boundaryWrite("INSERT INTO certificates"), boundaryWrite("DELETE FROM certificate_domains"), boundaryWrite("INSERT INTO certificate_domains"), boundaryWrite("UPDATE domains SET certificate_id")}
					steps = append(steps, boundaryPoolSteps(dialect)...)
					steps = append(steps, boundaryWrite("UPDATE certificates SET distribution_revision"))
					run = func(s *SQLStore) error {
						c := certificate
						revision, err := s.ProvisionManagedCertificate(context.Background(), &c, domain.ID, now)
						if err == nil && revision != 8 {
							t.Fatalf("distribution revision=%d", revision)
						}
						return err
					}
				case "install custom":
					steps = []transactionBoundaryStep{boundaryDomainRead(t, domain), boundaryWrite("INSERT INTO certificates"), boundaryWrite("DELETE FROM certificate_domains"), boundaryWrite("INSERT INTO certificate_domains"), boundaryWrite("UPDATE domains SET tls_mode")}
					steps = append(steps, boundaryPoolSteps(dialect)...)
					run = func(s *SQLStore) error {
						c := certificate
						c.Provider = "custom"
						return s.InstallCustomCertificate(context.Background(), &c, []string{domain.ID}, now)
					}
				case "activate certificate":
					steps = []transactionBoundaryStep{boundaryRead("SELECT pool_id,status,certificate_pem,encrypted_private_key,revision FROM certificates", []string{"pool_id", "status", "certificate_pem", "encrypted_private_key", "revision"}, "pool", "PENDING", "", []byte{}, int64(1)), boundaryWrite("UPDATE certificates SET provider"), boundaryWrite("UPDATE domains SET status")}
					steps = append(steps, boundaryPoolSteps(dialect)...)
					run = func(s *SQLStore) error {
						c := certificate
						return s.ActivateCertificate(context.Background(), &c, 1, now)
					}
				case "create certificate":
					steps = []transactionBoundaryStep{boundaryWrite("INSERT INTO certificates")}
					run = func(s *SQLStore) error { c := certificate; return s.CreateCertificate(context.Background(), &c) }
				}
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) { runTransactionBoundary(t, dialect, steps, fault, run) })
				}
			})
		}
	}
}
