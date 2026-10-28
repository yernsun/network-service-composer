package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func activationTransactionSteps(dialect Dialect, remove bool) []transactionBoundaryStep {
	steps := []transactionBoundaryStep{
		boundaryRead("SELECT a.runtime_id,rt.pool_id,a.desired_revision_id", []string{"runtime", "pool", "desired", "catalog"}, "runtime", "pool", "candidate", nil),
		boundaryRead("SELECT application_id,catalog_app_version_id", []string{"application", "catalog"}, "application", nil),
		boundaryRead("SELECT application_id FROM application_revisions", []string{"application"}, "application"),
	}
	steps = append(steps, boundaryPlaneSteps(false)...)
	steps = append(steps, boundaryEmpty("FROM services"), boundaryRead("SELECT service_key,application_id,managed", []string{"key", "owner", "managed"}, "demo/web", "application", true), boundaryWrite("UPDATE services SET application_id"), boundaryWrite("UPDATE services SET routing_eligible"))
	removed := boundaryEmpty("SELECT id FROM services WHERE runtime_id=")
	if remove {
		removed = boundaryRead("SELECT id FROM services WHERE runtime_id=", []string{"id"}, "removed")
	}
	steps = append(steps, removed)
	if remove {
		steps = append(steps, boundaryWrite("UPDATE routes SET enabled"), boundaryWrite("UPDATE services SET application_id"))
	}
	steps = append(steps, boundaryEmpty("FROM services"), boundaryEmpty("FROM routes WHERE source="), boundaryWrite("UPDATE applications SET active_revision_id"))
	steps = append(steps, boundaryPlaneSteps(true)...)
	steps = append(steps, boundaryEmpty("FROM services"))
	steps = append(steps, boundaryPoolSteps(dialect)...)
	return steps
}

func TestApplicationActivationRollsBackEveryStorageBoundary(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, remove := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/remove=%t", dialect, remove), func(t *testing.T) {
				steps := activationTransactionSteps(dialect, remove)
				for fault := -1; fault <= len(steps); fault++ {
					t.Run(fmt.Sprint(fault), func(t *testing.T) {
						runTransactionBoundary(t, dialect, steps, fault, func(s *SQLStore) error {
							return s.ActivateApplicationRevision(context.Background(), "runtime", "application", "active", "candidate", []string{"demo/web", "demo/web"}, nil, "", time.Now())
						})
					})
				}
			})
		}
	}
}
