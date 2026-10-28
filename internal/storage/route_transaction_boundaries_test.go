package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestRouteUpdateAndDomainDisableStorageFailuresPreserveExistingEntry(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, action := range []string{"update route", "disable domain"} {
			t.Run(string(dialect)+"/"+action, func(t *testing.T) {
				now := time.Now().UTC()
				domain := model.Domain{ID: "domain", PoolID: "pool", Host: "example.test", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
				route := model.Route{ID: "route", DomainID: domain.ID, ServiceID: "service", Path: "/", Enabled: true, Status: model.RouteActive, Source: model.SourceAPI, Revision: 1, CreatedAt: now, UpdatedAt: now}
				service := model.Service{ID: "service", RuntimeID: "runtime", Revision: 1, CreatedAt: now, UpdatedAt: now}
				runtime := model.Runtime{ID: "runtime", PoolID: "pool", Name: "runtime", Type: model.RuntimeDocker, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
				steps := []transactionBoundaryStep{}
				outsideReads := 1
				var run func(*SQLStore) error
				if action == "disable domain" {
					steps = append(steps, boundaryDomainRead(t, domain), boundaryWrite("UPDATE domains SET enabled"), boundaryWrite("UPDATE routes SET enabled"))
					steps = append(steps, boundaryPoolSteps(dialect)...)
					run = func(s *SQLStore) error { return s.DeleteDomain(context.Background(), domain.ID, 1) }
				} else {
					outsideReads = 5
					readRoute := boundaryRead("FROM routes WHERE id", strings.Split(routeColumns, ","), "route", "domain", "service", "", "/", int64(0), false, int64(8080), "http", "", false, true, string(model.RouteActive), string(model.SourceAPI), "", int64(1), now, now)
					steps = append(steps, readRoute, boundaryDomainRead(t, domain), boundaryDomainRead(t, domain), boundaryRead("FROM services WHERE id", strings.Split(serviceColumns, ","), snapshotRow(t, serviceArgs(&service))...), boundaryRead("FROM runtimes WHERE id", strings.Split(runtimeColumns, ","), runtime.ID, runtime.PoolID, runtime.Name, string(runtime.Type), "unix:///fixture", "", "", true, true, "ONLINE", int64(0), int64(0), nil, "", int64(1), now, now), boundaryRead("SELECT id FROM domains", []string{"id"}, domain.ID))
					steps = append(steps, boundaryPlaneSteps(false)...)
					steps = append(steps, boundaryEmpty("SELECT id FROM routes"), boundaryWrite("UPDATE routes SET domain_id"))
					steps = append(steps, boundaryPlaneSteps(true)...)
					steps = append(steps, boundaryPoolSteps(dialect)...)
					run = func(s *SQLStore) error {
						v := route
						v.Path = "/api/"
						return s.UpdateRoute(context.Background(), &v, 1)
					}
				}
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) {
						failure := errors.New("route transaction interrupted")
						script := &queryScript{t: t, transactions: true}
						for i, step := range steps {
							if step.query != nil {
								q := *step.query
								if i == fault {
									q.err = failure
								}
								script.replies = append(script.replies, &q)
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
						s := storeWithScript(t, script)
						s.dialect = dialect
						e := run(s)
						if fault < 0 {
							if e != nil || script.commits != 1 || script.rollbacks != 0 {
								t.Fatal("valid change did not commit", e, script.commits, script.rollbacks)
							}
						} else {
							if !errors.Is(e, failure) {
								t.Fatal("original failure was hidden", e)
							}
							if fault < len(steps) && script.commits != 0 {
								t.Fatal("partial route change committed")
							}
							if fault >= outsideReads && fault < len(steps) && script.rollbacks != 1 {
								t.Fatal("partial route transaction was not rolled back")
							}
						}
					})
				}
			})
		}
	}
}
