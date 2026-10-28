package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
)

// A database boundary fixture, rather than a replacement for store behavior.
// It makes query, conversion and mid-stream failures deterministic.
type queryReply struct {
	contains string
	args     []any
	columns  []string
	values   [][]driver.Value
	err      error
	rowErr   error
	closed   bool
}
type queryScript struct {
	t               *testing.T
	replies         []*queryReply
	calls           int
	execs           []*execReply
	execCalls       int
	transactions    bool
	commitErr       error
	commits         int
	rollbacks       int
	lifecycleLookup int
}

type execReply struct {
	contains    string
	err         error
	affected    int64
	affectedErr error
}
type scriptedResult struct{ reply *execReply }

func (r scriptedResult) LastInsertId() (int64, error) {
	return 0, errors.New("unexpected insert ID request")
}
func (r scriptedResult) RowsAffected() (int64, error) { return r.reply.affected, r.reply.affectedErr }
func (s *queryScript) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	s.t.Helper()
	if s.execCalls >= len(s.execs) {
		return nil, fmt.Errorf("unexpected write: %s", query)
	}
	r := s.execs[s.execCalls]
	s.execCalls++
	if !strings.Contains(query, r.contains) {
		return nil, fmt.Errorf("write %q does not address %q", query, r.contains)
	}
	if r.err != nil {
		return nil, r.err
	}
	return scriptedResult{r}, nil
}

type scriptedTransaction struct{ script *queryScript }

func (tx scriptedTransaction) Commit() error   { tx.script.commits++; return tx.script.commitErr }
func (tx scriptedTransaction) Rollback() error { tx.script.rollbacks++; return nil }

func (s *queryScript) Connect(context.Context) (driver.Conn, error) { return s, nil }
func (s *queryScript) Driver() driver.Driver                        { return scriptDriver{s} }

type scriptDriver struct{ script *queryScript }

func (d scriptDriver) Open(string) (driver.Conn, error) { return d.script, nil }
func (s *queryScript) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (s *queryScript) Close() error { return nil }
func (s *queryScript) Begin() (driver.Tx, error) {
	if s.transactions {
		return scriptedTransaction{s}, nil
	}
	return nil, errors.New("unexpected transaction")
}
func (s *queryScript) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s.t.Helper()
	// Existing transaction scripts describe resources outside the project
	// namespace. Model that absence through the complete guard query protocol;
	// do not skip production guard code or consume a scripted business failure.
	// Project-owned, deleting and tombstoned resources use real database tests.
	if strings.HasPrefix(query, "SELECT payload,kind FROM control_documents WHERE id=") && (s.calls >= len(s.replies) || !strings.Contains(query, s.replies[s.calls].contains)) {
		if len(args) != 1 || !strings.HasPrefix(fmt.Sprint(args[0].Value), "project-guard:") {
			return nil, errors.New("invalid project guard lookup")
		}
		s.lifecycleLookup = 1
		return &replyRows{reply: &queryReply{columns: []string{"payload", "kind"}}}, nil
	}
	if s.lifecycleLookup > 0 {
		steps := []struct {
			prefix  string
			columns []string
		}{
			{"SELECT kind,parent_id,payload FROM control_documents", []string{"kind", "parent_id", "payload"}},
			{"SELECT payload FROM control_documents", []string{"payload"}},
			{"SELECT application_id,runtime_id,runtime_object_id FROM services", []string{"application_id", "runtime_id", "runtime_object_id"}},
			{"SELECT service_id FROM routes WHERE id=", []string{"service_id"}},
		}
		step := steps[s.lifecycleLookup-1]
		if !strings.HasPrefix(query, step.prefix) {
			return nil, fmt.Errorf("incomplete lifecycle lookup: %s", query)
		}
		s.lifecycleLookup++
		if s.lifecycleLookup > len(steps) {
			s.lifecycleLookup = 0
		}
		return &replyRows{reply: &queryReply{columns: step.columns}}, nil
	}
	if strings.Contains(query, "kind='project-ingress-guard'") && (s.calls >= len(s.replies) || !strings.Contains(query, s.replies[s.calls].contains)) {
		return &replyRows{reply: &queryReply{columns: []string{"id"}}}, nil
	}
	if s.calls >= len(s.replies) {
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
	r := s.replies[s.calls]
	s.calls++
	if !strings.Contains(query, r.contains) {
		return nil, fmt.Errorf("query %q does not address %q", query, r.contains)
	}
	if r.args != nil {
		got := make([]any, len(args))
		for i, arg := range args {
			got[i] = arg.Value
		}
		if !reflect.DeepEqual(got, r.args) {
			return nil, fmt.Errorf("query args = %#v, want %#v", got, r.args)
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	return &replyRows{reply: r}, nil
}

type replyRows struct {
	reply *queryReply
	index int
}

func (r *replyRows) Columns() []string { return r.reply.columns }
func (r *replyRows) Close() error      { r.reply.closed = true; return nil }
func (r *replyRows) Next(dest []driver.Value) error {
	if r.index == len(r.reply.values) {
		if r.reply.rowErr != nil {
			return r.reply.rowErr
		}
		return io.EOF
	}
	copy(dest, r.reply.values[r.index])
	r.index++
	return nil
}
func scriptedStore(t *testing.T, replies ...*queryReply) *SQLStore {
	t.Helper()
	script := &queryScript{t: t, replies: replies}
	return storeWithScript(t, script)
}
func storeWithScript(t *testing.T, script *queryScript) *SQLStore {
	t.Helper()
	db := sql.OpenDB(script)
	t.Cleanup(func() {
		if script.calls != len(script.replies) {
			t.Errorf("queries = %d, want %d", script.calls, len(script.replies))
		}
		if script.execCalls != len(script.execs) {
			t.Errorf("writes = %d, want %d", script.execCalls, len(script.execs))
		}
		for i, reply := range script.replies {
			if reply.err == nil && !reply.closed {
				t.Errorf("query %d leaked rows", i)
			}
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return &SQLStore{db: db, dialect: SQLite}
}

func TestServiceEnvironmentResolutionBoundaries(t *testing.T) {
	failure := errors.New("database stream interrupted")
	claim := func(values ...[]driver.Value) *queryReply {
		return &queryReply{contains: "FROM services", args: []any{"service"}, columns: []string{"id", "payload"}, values: values}
	}
	environment := func(values ...[]driver.Value) *queryReply {
		return &queryReply{contains: "kind='environment'", args: []any{"environment"}, columns: []string{"id", "payload"}, values: values}
	}
	claimed := func() *queryReply { return claim([]driver.Value{"service", `{"environment_id":"environment"}`}) }
	tests := []struct {
		name      string
		replies   func() []*queryReply
		want      map[string]model.EnvironmentReference
		errorText string
		cause     error
	}{
		{name: "missing service", replies: func() []*queryReply { return []*queryReply{claim()} }, want: map[string]model.EnvironmentReference{}},
		{name: "external service without claim", replies: func() []*queryReply { return []*queryReply{claim([]driver.Value{"service", nil})} }, want: map[string]model.EnvironmentReference{}},
		{name: "deleted environment", replies: func() []*queryReply { return []*queryReply{claimed(), environment()} }, want: map[string]model.EnvironmentReference{}},
		{name: "resolved project", replies: func() []*queryReply {
			return []*queryReply{claimed(), environment([]driver.Value{"environment", `{"name":"Production","project_id":"project","project_name":"Checkout"}`})}
		}, want: map[string]model.EnvironmentReference{"service": {ID: "environment", Name: "Production", ProjectID: "project", ProjectName: "Checkout"}}},
		{name: "corrupt claim", replies: func() []*queryReply { return []*queryReply{claim([]driver.Value{"service", `{"environment_id":42}`})} }, errorText: "cannot unmarshal"},
		{name: "corrupt environment", replies: func() []*queryReply {
			return []*queryReply{claimed(), environment([]driver.Value{"environment", `{"name":42}`})}
		}, errorText: "cannot unmarshal"},
		{name: "claim conversion failure", replies: func() []*queryReply { return []*queryReply{claim([]driver.Value{nil, nil})} }, errorText: "converting NULL"},
		{name: "environment conversion failure", replies: func() []*queryReply { return []*queryReply{claimed(), environment([]driver.Value{"environment", nil})} }, errorText: "converting NULL"},
		{name: "claim query failure", replies: func() []*queryReply { r := claim(); r.err = failure; return []*queryReply{r} }, cause: failure},
		{name: "environment query failure", replies: func() []*queryReply { r := environment(); r.err = failure; return []*queryReply{claimed(), r} }, cause: failure},
		{name: "claim stream failure", replies: func() []*queryReply { r := claimed(); r.rowErr = failure; return []*queryReply{r} }, cause: failure},
		{name: "environment stream failure discards partial result", replies: func() []*queryReply {
			r := environment([]driver.Value{"environment", `{"name":"Production"}`})
			r.rowErr = failure
			return []*queryReply{claimed(), r}
		}, cause: failure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := scriptedStore(t, tc.replies()...)
			got, err := store.ServiceEnvironments(context.Background(), []string{"service"})
			if tc.cause != nil {
				if !errors.Is(err, tc.cause) {
					t.Fatalf("error=%v, want cause %v", err, tc.cause)
				}
			} else if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("error=%v, want %q", err, tc.errorText)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("environments=%#v, want %#v", got, tc.want)
			}
		})
	}
	t.Run("empty selection needs no database", func(t *testing.T) {
		got, err := (&SQLStore{}).ServiceEnvironments(context.Background(), nil)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty selection = %#v, %v", got, err)
		}
	})
}
