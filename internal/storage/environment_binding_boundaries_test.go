package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
)

func environmentBindingSteps(previous string) []transactionBoundaryStep {
	return []transactionBoundaryStep{
		boundaryRead("kind='image-policy'", []string{"revision"}, int64(2)),
		boundaryRead("SELECT COUNT(*) FROM deployment_operations", []string{"count"}, int64(0)),
		boundaryRead("SELECT payload,parent_id,revision", []string{"payload", "parent", "revision"}, `{"application_id":"`+previous+`"}`, "project", int64(3)),
		boundaryRead("SELECT id FROM control_documents", []string{"id"}, "project"), boundaryRead("SELECT id FROM control_documents", []string{"id"}, "environment"),
		boundaryRead("SELECT revision FROM control_documents", []string{"revision"}, int64(4)), boundaryRead("SELECT revision FROM control_documents", []string{"revision"}, int64(5)),
		boundaryWrite("UPDATE control_documents SET payload"), boundaryWrite("INSERT INTO control_documents"),
	}
}
func environmentBindingRevision() model.ApplicationRevision {
	return model.ApplicationRevision{ID: "candidate", ApplicationID: "application", ConfigurationSource: model.DeploymentSource{EnvironmentID: "environment", EnvironmentRevision: 3, UpdatePolicyRevision: 2, ImageUpdate: true, PublicVariablesVersion: 4, EnvironmentVariablesVersion: 5}}
}

func TestEnvironmentBindingFailuresCannotPartiallyClaimApplication(t *testing.T) {
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		t.Run(string(dialect), func(t *testing.T) {
			steps := environmentBindingSteps("")
			for fault := -1; fault <= len(steps); fault++ {
				t.Run(fmt.Sprint(fault), func(t *testing.T) {
					runTransactionBoundary(t, dialect, steps, fault, func(s *SQLStore) error {
						ctx := context.Background()
						tx, e := s.db.BeginTx(ctx, nil)
						if e != nil {
							return e
						}
						defer tx.Rollback()
						revision := environmentBindingRevision()
						if e = s.bindEnvironmentRevision(ctx, tx, &revision); e != nil {
							return e
						}
						return tx.Commit()
					})
				})
			}
		})
	}
}

func TestEnvironmentBindingRechecksEveryReviewedVersion(t *testing.T) {
	for _, stage := range []string{"policy changed", "active operation", "environment changed", "public variables changed", "environment variables changed", "corrupt environment", "null environment", "array environment", "another application", "already claimed", "missing variable layers", "no environment", "no revision", "concurrent environment write", "affected unavailable"} {
		t.Run(stage, func(t *testing.T) {
			revision := environmentBindingRevision()
			steps := environmentBindingSteps("")
			last := len(steps) - 1
			wantError := true
			wantCause := error(nil)
			failure := errors.New("affected count unavailable")
			switch stage {
			case "policy changed":
				steps[0].query.values[0][0] = int64(9)
				last = 0
				wantCause = ErrPrecondition
			case "active operation":
				steps[1].query.values[0][0] = int64(1)
				last = 1
				wantCause = ErrConflict
			case "environment changed":
				steps[2].query.values[0][2] = int64(9)
				last = 2
				wantCause = ErrPrecondition
			case "public variables changed":
				steps[5].query.values[0][0] = int64(9)
				last = 5
				wantCause = ErrPrecondition
			case "environment variables changed":
				steps[6].query.values[0][0] = int64(9)
				last = 6
				wantCause = ErrPrecondition
			case "corrupt environment":
				steps[2].query.values[0][0] = "{"
				last = 6
			case "null environment":
				steps[2].query.values[0][0] = "null"
				last = 6
				wantCause = ErrConflict
			case "array environment":
				steps[2].query.values[0][0] = "[]"
				last = 6
			case "another application":
				steps[2].query.values[0][0] = `{"application_id":"external"}`
				last = 6
				wantCause = ErrConflict
			case "already claimed":
				steps[2].query.values[0][0] = `{"application_id":"application"}`
				last = 7
				wantError = false
			case "missing variable layers":
				steps[5].query.values = nil
				steps[6].query.values = nil
				revision.ConfigurationSource.PublicVariablesVersion = 0
				revision.ConfigurationSource.EnvironmentVariablesVersion = 0
				wantError = false
			case "no environment":
				revision.ConfigurationSource = model.DeploymentSource{}
				last = -1
				wantError = false
			case "no revision":
				revision.ConfigurationSource = model.DeploymentSource{EnvironmentID: "environment"}
				last = -1
				wantError = false
			case "concurrent environment write":
				steps[7].write.affected = 0
				last = 7
				wantCause = ErrPrecondition
			case "affected unavailable":
				steps[7].write.affectedErr = failure
				last = 7
				wantCause = failure
			}
			script := &queryScript{t: t, transactions: true}
			for i, step := range steps {
				if i > last {
					break
				}
				if step.query != nil {
					script.replies = append(script.replies, step.query)
				} else {
					script.execs = append(script.execs, step.write)
				}
			}
			s := storeWithScript(t, script)
			ctx := context.Background()
			tx, e := s.db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			e = s.bindEnvironmentRevision(ctx, tx, &revision)
			if e != nil {
				tx.Rollback()
			} else {
				e = tx.Commit()
			}
			if (e != nil) != wantError {
				t.Fatal("invalid environment claim result", e)
			}
			if wantCause != nil && !errors.Is(e, wantCause) {
				t.Fatal("version/ownership failure lost", e)
			}
			if wantError {
				if script.commits != 0 || script.rollbacks != 1 {
					t.Fatal("failed claim committed")
				}
			} else if script.commits != 1 {
				t.Fatal("valid claim not committed")
			}
		})
	}
}

func TestEnvironmentBindingMalformedReadNeverCommits(t *testing.T) {
	for index := 0; index < 7; index++ {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			steps := environmentBindingSteps("")
			script := &queryScript{t: t, transactions: true}
			for i := 0; i <= index; i++ {
				q := *steps[i].query
				if i == index {
					q.columns = []string{"invalid"}
					q.values = [][]driver.Value{{nil}}
				}
				script.replies = append(script.replies, &q)
			}
			s := storeWithScript(t, script)
			tx, e := s.db.BeginTx(context.Background(), nil)
			if e != nil {
				t.Fatal(e)
			}
			r := environmentBindingRevision()
			e = s.bindEnvironmentRevision(context.Background(), tx, &r)
			tx.Rollback()
			if e == nil || script.commits != 0 || script.rollbacks != 1 {
				t.Fatal("invalid stored version accepted", e)
			}
		})
	}
}
