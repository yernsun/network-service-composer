package lease

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/storage"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

type leaseReply struct {
	query   string
	value   driver.Value
	failure error
}
type leaseFixtureDriver struct {
	replies []leaseReply
	calls   int
	closed  int
	openErr error
	problem error
}
type leaseFixtureConn struct{ fixture *leaseFixtureDriver }
type leaseFixtureRows struct {
	value driver.Value
	done  bool
}

var leaseDriverSequence atomic.Int64

func (d *leaseFixtureDriver) Open(string) (driver.Conn, error) {
	if d.openErr != nil {
		return nil, d.openErr
	}
	return &leaseFixtureConn{d}, nil
}
func (c *leaseFixtureConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *leaseFixtureConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *leaseFixtureConn) Close() error { c.fixture.closed++; return nil }
func (c *leaseFixtureConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	d := c.fixture
	i := d.calls
	d.calls++
	if ctx.Err() != nil {
		d.problem = errors.New("cleanup reused canceled request context")
		return nil, d.problem
	}
	if i >= len(d.replies) {
		d.problem = fmt.Errorf("unexpected query %s", q)
		return nil, d.problem
	}
	r := d.replies[i]
	if !strings.Contains(q, r.query) {
		d.problem = fmt.Errorf("query %s does not match %s", q, r.query)
		return nil, d.problem
	}
	if r.failure != nil {
		return nil, r.failure
	}
	return &leaseFixtureRows{value: r.value}, nil
}
func (r *leaseFixtureRows) Columns() []string { return []string{"result"} }
func (r *leaseFixtureRows) Close() error      { return nil }
func (r *leaseFixtureRows) Next(v []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	v[0] = r.value
	return nil
}
func leaseSQLFixture(t *testing.T, replies []leaseReply) (*sql.DB, *leaseFixtureDriver) {
	t.Helper()
	d := &leaseFixtureDriver{replies: replies}
	name := fmt.Sprintf("nsc-lease-boundary-%d", leaseDriverSequence.Add(1))
	sql.Register(name, d)
	db, e := sql.Open(name, "")
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxIdleConns(1)
	t.Cleanup(func() {
		db.Close()
		if d.calls != len(d.replies) || d.problem != nil {
			t.Errorf("lease SQL protocol: calls %d/%d, error %v", d.calls, len(d.replies), d.problem)
		}
	})
	return db, d
}
func TestDatabaseLeaseOwnershipAndCleanupBoundaries(t *testing.T) {
	failure := errors.New("isolated database failure")
	for _, dialect := range []storage.Dialect{storage.Postgres, storage.MySQL} {
		lock, check, unlock := "pg_try_advisory_lock", "pg_locks", "pg_advisory_unlock"
		var acquired driver.Value = true
		if dialect == storage.MySQL {
			lock, check, unlock = "GET_LOCK", "IS_USED_LOCK", "RELEASE_LOCK"
			acquired = int64(1)
		}
		for _, stage := range []string{"success", "busy", "null acquisition", "lock query failure", "lock scan failure", "check lost", "check query failure", "check scan failure", "unlock lost", "unlock null", "unlock query failure", "unlock scan failure"} {
			t.Run(string(dialect)+"/"+stage, func(t *testing.T) {
				replies := []leaseReply{{query: lock, value: acquired}, {query: check, value: true}, {query: unlock, value: acquired}}
				switch stage {
				case "busy":
					replies[0].value = false
					if dialect == storage.MySQL {
						replies[0].value = int64(0)
					}
					replies = replies[:1]
				case "null acquisition":
					replies[0].value = nil
					replies = replies[:1]
				case "lock query failure":
					replies[0].failure = failure
					replies = replies[:1]
				case "lock scan failure":
					replies[0].value = "invalid"
					replies = replies[:1]
				case "check lost":
					replies[1].value = false
				case "check query failure":
					replies[1].failure = failure
				case "check scan failure":
					replies[1].value = "invalid"
				case "unlock lost":
					replies[2].value = false
					if dialect == storage.MySQL {
						replies[2].value = int64(0)
					}
				case "unlock null":
					replies[2].value = nil
				case "unlock query failure":
					replies[2].failure = failure
				case "unlock scan failure":
					replies[2].value = "invalid"
				}
				db, d := leaseSQLFixture(t, replies)
				locker := New(db, dialect)
				held, e := locker.TryLock(context.Background(), strings.Repeat("application/", 20))
				if len(replies) == 1 {
					if e == nil || held != nil {
						t.Fatal("unacquired lease returned")
					}
					if stage == "busy" || stage == "null acquisition" && dialect == storage.MySQL {
						if !errors.Is(e, ErrBusy) {
							t.Fatalf("busy ownership lost: %v", e)
						}
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				e = held.Check(context.Background())
				if strings.HasPrefix(stage, "check ") {
					if !errors.Is(e, ErrLost) {
						t.Fatalf("ownership uncertainty not lost: %v", e)
					}
				} else if e != nil {
					t.Fatal(e)
				}
				canceled, cancel := context.WithCancel(context.Background())
				cancel()
				e = held.Release(canceled)
				if strings.HasPrefix(stage, "unlock ") {
					if e == nil || d.closed != 1 || db.Stats().Idle != 0 {
						t.Fatalf("failed unlock returned lock-bearing connection: %v closed=%d idle=%d", e, d.closed, db.Stats().Idle)
					}
				} else if e != nil || d.closed != 0 || db.Stats().Idle != 1 {
					t.Fatalf("successful unlock failed to reuse connection: %v", e)
				}
				if again := held.Release(context.Background()); again != e {
					t.Fatalf("release retry changed recorded result: %v vs %v", again, e)
				}
				if e = held.Check(context.Background()); !errors.Is(e, ErrLost) {
					t.Fatal("released handle accepted", e)
				}
			})
		}
	}
}
func TestDatabaseLeaseRejectsUnavailableAndUnsupportedConnections(t *testing.T) {
	for _, stage := range []string{"closed", "canceled", "open failure", "unsupported"} {
		t.Run(stage, func(t *testing.T) {
			db, d := leaseSQLFixture(t, nil)
			ctx := context.Background()
			dialect := storage.Postgres
			switch stage {
			case "closed":
				db.Close()
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "open failure":
				d.openErr = errors.New("connect failed")
			case "unsupported":
				dialect = storage.Dialect("unsupported")
			}
			if held, e := New(db, dialect).TryLock(ctx, "key"); e == nil || held != nil {
				t.Fatal("unavailable lease accepted")
			}
		})
	}
	db, _ := leaseSQLFixture(t, nil)
	conn, e := db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	held := &dbLease{conn: conn, dialect: storage.Dialect("unsupported"), key: "key"}
	if e = held.Check(context.Background()); !errors.Is(e, ErrLost) {
		t.Fatal("unsupported ownership accepted")
	}
	if e = held.Release(context.Background()); e != nil {
		t.Fatal(e)
	}
	if boundedKey("short") != "short" || len(boundedKey(strings.Repeat("x", 65))) > 64 || boundedKey(strings.Repeat("x", 65)) == boundedKey(strings.Repeat("y", 65)) {
		t.Fatal("lease identity not stable and bounded")
	}
}
