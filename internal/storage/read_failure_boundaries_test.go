package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

// Every resource list must distinguish an empty database from a failed read.
// Exercise the database boundary directly, including interruption after Query
// returned rows. No runtime or application operation is executed by this suite.
func TestResourceListsDoNotHideDatabaseFailures(t *testing.T) {
	ctx := context.Background()
	page := Page{Limit: 2, After: "previous"}
	reads := []struct {
		name string
		read func(*SQLStore) error
	}{
		{"pools", func(s *SQLStore) error { _, e := s.ListPools(ctx, page); return e }},
		{"runtimes", func(s *SQLStore) error { _, e := s.ListRuntimes(ctx, page); return e }},
		{"services", func(s *SQLStore) error { _, e := s.ListServices(ctx, page); return e }},
		{"filtered services", func(s *SQLStore) error {
			_, e := s.ListServicesFiltered(ctx, ServiceFilter{RuntimeID: "runtime", ApplicationID: "app", Search: "needle", Status: "AVAILABLE"}, page)
			return e
		}},
		{"all filtered services", func(s *SQLStore) error {
			_, e := s.ListAllServicesFiltered(ctx, ServiceFilter{RuntimeID: "runtime"})
			return e
		}},
		{"endpoints", func(s *SQLStore) error { _, e := s.ListEndpoints(ctx, "service"); return e }},
		{"paged endpoints", func(s *SQLStore) error { _, e := s.ListEndpointsPage(ctx, "service", page); return e }},
		{"domains", func(s *SQLStore) error { _, e := s.ListDomains(ctx, page); return e }},
		{"routes", func(s *SQLStore) error { _, e := s.ListRoutes(ctx, page); return e }},
		{"filtered routes", func(s *SQLStore) error {
			_, e := s.ListRoutesFiltered(ctx, ConsoleFilter{DomainID: "domain", ServiceID: "service"}, page)
			return e
		}},
		{"bindings", func(s *SQLStore) error { _, e := s.ListServiceDomainBindings(ctx, "service"); return e }},
		{"catalog sources", func(s *SQLStore) error { _, e := s.ListCatalogSources(ctx, page); return e }},
		{"catalog versions", func(s *SQLStore) error { _, e := s.ListCatalogAppVersions(ctx, "source", "app", page); return e }},
		{"applications", func(s *SQLStore) error { _, e := s.ListApplications(ctx, "runtime", page); return e }},
		{"application revisions", func(s *SQLStore) error { _, e := s.ListApplicationRevisions(ctx, "application", page); return e }},
		{"operations", func(s *SQLStore) error { _, e := s.ListDeploymentOperations(ctx, "application", page); return e }},
		{"certificates", func(s *SQLStore) error { _, e := s.ListCertificates(ctx, page); return e }},
		{"pending certificates", func(s *SQLStore) error { _, e := s.ListPendingCertificates(ctx, time.Now().UTC(), 2); return e }},
		{"gateways", func(s *SQLStore) error { _, e := s.ListGateways(ctx, page); return e }},
		{"gateway DNS targets", func(s *SQLStore) error { _, e := s.ListGatewayDNSTargets(ctx, "pool"); return e }},
		{"audit", func(s *SQLStore) error { _, e := s.ListAuditEvents(ctx, page); return e }},
		{"service groups", func(s *SQLStore) error {
			_, e := s.ListServiceGroups(ctx, "services", ConsoleFilter{}, Page{})
			return e
		}},
		{"route groups", func(s *SQLStore) error { _, e := s.ListServiceGroups(ctx, "routes", ConsoleFilter{}, Page{}); return e }},
	}
	failure := errors.New("database connection lost during read")
	for _, read := range reads {
		for _, fault := range []string{"query", "row stream", "malformed row"} {
			t.Run(read.name+"/"+fault, func(t *testing.T) {
				reply := &queryReply{contains: "SELECT", columns: []string{"invalid_row"}}
				switch fault {
				case "query":
					reply.err = failure
				case "row stream":
					reply.rowErr = failure
				case "malformed row":
					reply.values = [][]driver.Value{{nil}}
				}
				err := read.read(scriptedStore(t, reply))
				if fault == "malformed row" {
					if err == nil || !strings.Contains(err.Error(), "sql:") {
						t.Fatalf("malformed database row accepted: %v", err)
					}
				} else if !errors.Is(err, failure) {
					t.Fatalf("database failure lost: %v", err)
				}
			})
		}
	}
}

func TestResourceLookupPropagatesUnavailableDatabase(t *testing.T) {
	ctx := context.Background()
	failure := errors.New("database unavailable")
	for _, read := range []struct {
		name string
		run  func(*SQLStore) error
	}{
		{"pool", func(s *SQLStore) error { _, e := s.GetPool(ctx, "pool"); return e }},
		{"runtime", func(s *SQLStore) error { _, e := s.GetRuntime(ctx, "runtime"); return e }},
		{"service", func(s *SQLStore) error { _, e := s.GetService(ctx, "service"); return e }},
		{"domain", func(s *SQLStore) error { _, e := s.GetDomain(ctx, "domain"); return e }},
		{"route", func(s *SQLStore) error { _, e := s.GetRoute(ctx, "route"); return e }},
		{"source", func(s *SQLStore) error { _, e := s.GetCatalogSource(ctx, "source"); return e }},
		{"version", func(s *SQLStore) error { _, e := s.GetCatalogAppVersion(ctx, "version"); return e }},
		{"application", func(s *SQLStore) error { _, e := s.GetApplication(ctx, "application"); return e }},
		{"revision", func(s *SQLStore) error { _, e := s.GetApplicationRevision(ctx, "revision"); return e }},
		{"operation", func(s *SQLStore) error { _, e := s.GetDeploymentOperation(ctx, "operation"); return e }},
		{"idempotency", func(s *SQLStore) error {
			_, e := s.GetDeploymentOperationByIdempotency(ctx, "runtime", "key")
			return e
		}},
		{"certificate", func(s *SQLStore) error { _, e := s.GetCertificate(ctx, "certificate"); return e }},
		{"gateway", func(s *SQLStore) error { _, e := s.GetGateway(ctx, "gateway"); return e }},
		{"challenge", func(s *SQLStore) error { _, e := s.GetChallenge(ctx, "token", time.Now().UTC()); return e }},
		{"ACME account", func(s *SQLStore) error { _, e := s.GetACMEAccount(ctx, "directory", "email"); return e }},
	} {
		t.Run(read.name, func(t *testing.T) {
			err := read.run(scriptedStore(t, &queryReply{contains: "SELECT", err: failure}))
			if !errors.Is(err, failure) {
				t.Fatalf("unavailable resource read misreported: %v", err)
			}
		})
	}
}
