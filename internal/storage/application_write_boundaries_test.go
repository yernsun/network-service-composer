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

func TestReplicaOverrideTransactionFailures(t *testing.T) {
	failure := errors.New("replica storage interrupted")
	for _, dialect := range []Dialect{SQLite, Postgres, MySQL} {
		for _, fault := range []string{"read", "missing", "invalid JSON", "write", "deleted", "affected", "commit", "success"} {
			t.Run(string(dialect)+"/"+fault, func(t *testing.T) {
				query := "SELECT replica_overrides FROM applications WHERE id=?"
				if dialect != SQLite {
					query = "FOR UPDATE"
				}
				read := &queryReply{contains: query, columns: []string{"replica_overrides"}, values: [][]driver.Value{{`{"worker":2}`}}}
				write := &execReply{contains: "UPDATE applications SET replica_overrides", affected: 1}
				script := &queryScript{t: t, transactions: true, replies: []*queryReply{read}}
				want := failure
				switch fault {
				case "read":
					read.err = failure
				case "missing":
					read.values = nil
					want = ErrNotFound
				case "invalid JSON":
					read.values = [][]driver.Value{{`{"worker":`}}
					want = nil
				default:
					script.execs = []*execReply{write}
					switch fault {
					case "write":
						write.err = failure
					case "deleted":
						write.affected = 0
						want = ErrNotFound
					case "affected":
						write.affectedErr = failure
					case "commit":
						script.commitErr = failure
					case "success":
						want = nil
					}
				}
				store := storeWithScript(t, script)
				store.dialect = dialect
				err := store.SetApplicationReplicaOverride(context.Background(), "app", "web", 0, time.Now().UTC())
				if fault == "invalid JSON" {
					if err == nil || !strings.Contains(err.Error(), "decode replica overrides") {
						t.Fatalf("corrupt overrides accepted: %v", err)
					}
				} else if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
				if fault == "success" || fault == "commit" {
					if script.commits != 1 {
						t.Fatalf("commit attempts = %d", script.commits)
					}
				} else if script.commits != 0 || script.rollbacks != 1 {
					t.Fatalf("failed scale was not rolled back: %+v", script)
				}
			})
		}
	}
}

func TestApplicationInstallAndRevisionWriteFailuresRollback(t *testing.T) {
	failure := errors.New("application transaction interrupted")
	for _, mode := range []string{"install", "revision"} {
		stages := []string{"application", "revision", "service", "operation", "commit", "success"}
		if mode == "revision" {
			stages = []string{"application", "precondition", "affected", "revision", "operation", "commit", "success"}
		}
		for _, stage := range stages {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				script := &queryScript{t: t, transactions: true}
				application := &model.Application{ID: "app", ReplicaOverrides: map[string]int{"worker": 2}}
				revision := &model.ApplicationRevision{ID: "revision", ApplicationID: "app"}
				operation := &model.DeploymentOperation{ID: "operation", RuntimeID: "runtime"}
				first := &execReply{contains: "INSERT INTO applications", affected: 1}
				if mode == "revision" {
					first.contains = "UPDATE applications"
				}
				script.execs = append(script.execs, first)
				want := failure
				switch stage {
				case "application":
					first.err = failure
				case "precondition":
					first.affected = 0
					want = ErrPrecondition
				case "affected":
					first.affectedErr = failure
				default:
					for _, next := range []string{"revision", "service", "operation"} {
						if next == "service" && mode == "revision" {
							continue
						}
						table := map[string]string{"revision": "application_revisions", "service": "services", "operation": "deployment_operations"}[next]
						write := &execReply{contains: "INSERT INTO " + table, affected: 1}
						script.execs = append(script.execs, write)
						if stage == next {
							write.err = failure
							break
						}
					}
					if stage == "commit" {
						script.commitErr = failure
					}
					if stage == "success" {
						want = nil
					}
				}
				if stage == "operation" || stage == "commit" || stage == "success" {
					script.replies = append(script.replies, &queryReply{contains: "SELECT pool_id,runtime_type,endpoint,credential_ref,cluster_id FROM runtimes", columns: []string{"pool_id", "runtime_type", "endpoint", "credential_ref", "cluster_id"}, values: [][]driver.Value{{"pool", "docker", "unix:///fixture.sock", "", ""}}})
				}
				store := storeWithScript(t, script)
				var err error
				if mode == "install" {
					err = store.CreateApplicationInstall(context.Background(), application, revision, []model.Service{{ID: "service"}}, nil, nil, nil, operation)
				} else {
					err = store.CreateApplicationRevision(context.Background(), revision, operation, 1, time.Now().UTC())
				}
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
				if stage == "commit" || stage == "success" {
					if script.commits != 1 {
						t.Fatalf("commit attempts = %d", script.commits)
					}
				} else if script.commits != 0 || script.rollbacks != 1 {
					t.Fatalf("partial desired state not rolled back: %+v", script)
				}
			})
		}
	}
}
