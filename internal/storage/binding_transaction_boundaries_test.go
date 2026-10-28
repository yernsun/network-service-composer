package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func boundaryEmpty(query string) transactionBoundaryStep {
	return transactionBoundaryStep{query: &queryReply{contains: query, columns: []string{"unused"}}}
}

func boundaryPlaneSteps(changed bool) []transactionBoundaryStep {
	domains := boundaryEmpty("FROM domains WHERE pool_id=")
	if changed {
		domains = boundaryRead("FROM domains WHERE pool_id=", []string{"id", "host", "tls_mode", "https_redirect", "http_enabled", "https_enabled", "certificate_id"}, "domain", "app.example.test", "MANAGED", true, true, true, "certificate")
	}
	return []transactionBoundaryStep{domains, boundaryEmpty("FROM routes r JOIN domains d")}
}

func TestServiceDomainBindingTransactionsPreserveAtomicity(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, operation := range []string{"create", "update", "disable"} {
			t.Run(string(dialect)+"/"+operation, func(t *testing.T) {
				now := time.Now().UTC()
				domain := model.Domain{ID: "domain", PoolID: "pool", Host: "app.example.test", TLSMode: model.TLSManaged, CertificateID: "certificate", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
				certificate := model.Certificate{ID: "certificate", PoolID: "pool", Provider: "acme"}
				route := model.Route{ID: "route", ServiceID: "service", DomainID: domain.ID, Enabled: true, Revision: 1, UpdatedAt: now}
				var steps []transactionBoundaryStep
				var run func(*SQLStore) error
				switch operation {
				case "create":
					steps = []transactionBoundaryStep{
						boundaryRead("SELECT rt.pool_id FROM services", []string{"pool_id"}, "pool"),
						boundaryEmpty("FROM services"),
						boundaryWrite("UPDATE services SET target_port_override"),
						boundaryRead("SELECT rt.pool_id FROM services", []string{"pool_id"}, "pool"),
						boundaryEmpty("SELECT pool_id FROM domains"),
						boundaryWrite("INSERT INTO certificates"), boundaryWrite("INSERT INTO domains"), boundaryWrite("INSERT INTO certificate_domains"),
						boundaryRead("SELECT id FROM domains", []string{"id"}, "domain"), boundaryEmpty("SELECT id FROM routes"), boundaryWrite("INSERT INTO routes"), boundaryWrite("INSERT INTO deployment_operations"),
					}
					steps = append(steps, boundaryPoolSteps(dialect)...)
					steps = append(steps, boundaryEmpty("FROM services"))
					run = func(s *SQLStore) error {
						d, c, r := domain, certificate, route
						return s.CreateServiceDomainBinding(context.Background(), &d, &c, &r, &model.DeploymentOperation{ID: "operation"}, &model.Service{ID: "service"}, 1)
					}
				case "update":
					steps = []transactionBoundaryStep{boundaryRead("SELECT rt.pool_id,r.source", []string{"pool_id", "source"}, "pool", string(model.SourceAPI)), boundaryRead("SELECT id FROM domains", []string{"id"}, "domain")}
					steps = append(steps, boundaryPlaneSteps(false)...)
					steps = append(steps,
						boundaryRead("SELECT certificate_id,revision FROM domains", []string{"certificate_id", "revision"}, "old", int64(1)),
						boundaryRead("SELECT revision FROM routes", []string{"revision"}, int64(1)),
						boundaryWrite("INSERT INTO certificates"), boundaryRead("SELECT pool_id FROM certificates", []string{"pool_id"}, "pool"), boundaryWrite("UPDATE domains SET host"), boundaryWrite("DELETE FROM certificate_domains"), boundaryWrite("UPDATE certificates SET status"), boundaryWrite("INSERT INTO certificate_domains"), boundaryEmpty("SELECT id FROM routes"), boundaryWrite("UPDATE routes SET path"))
					steps = append(steps, boundaryPlaneSteps(true)...)
					steps = append(steps, boundaryPoolSteps(dialect)...)
					run = func(s *SQLStore) error {
						d, c, r := domain, certificate, route
						return s.UpdateServiceDomainBinding(context.Background(), "service", &d, &c, &r, 1, 1)
					}
				case "disable":
					steps = []transactionBoundaryStep{boundaryRead("SELECT r.source,rt.pool_id", []string{"source", "pool_id"}, string(model.SourceAPI), "pool")}
					steps = append(steps, boundaryPlaneSteps(true)...)
					steps = append(steps, boundaryWrite("UPDATE routes SET enabled"), boundaryWrite("INSERT INTO deployment_operations"))
					steps = append(steps, boundaryPlaneSteps(false)...)
					steps = append(steps, boundaryPoolSteps(dialect)...)
					run = func(s *SQLStore) error {
						return s.DisableServiceDomainBinding(context.Background(), "service", "route", 1, now, &model.DeploymentOperation{ID: "operation"})
					}
				}
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) { runTransactionBoundary(t, dialect, steps, fault, run) })
				}
			})
		}
	}
}
