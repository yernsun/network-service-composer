package lease

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

var (
	ErrBusy = errors.New("lease is held by another controller")
	ErrLost = errors.New("lease ownership was lost")
)

type Lease interface {
	Check(context.Context) error
	Release(context.Context) error
}
type Locker interface {
	TryLock(context.Context, string) (Lease, error)
}

// SessionLease exposes the database session owning the lock. A durable term can
// bind to this identity so another connection can reject an already-lost owner
// without waiting for a heartbeat timeout. Local SQLite locks have no session.
type SessionLease interface {
	Lease
	SessionID(context.Context) (int64, error)
}

func (l *dbLease) SessionID(ctx context.Context) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return 0, ErrLost
	}
	query := "SELECT pg_backend_pid()"
	if l.dialect == storage.MySQL {
		query = "SELECT CONNECTION_ID()"
	}
	var owner int64
	err := l.conn.QueryRowContext(ctx, query).Scan(&owner)
	return owner, err
}

// CheckOwner verifies a named lock through an independent database connection.
// It must be used with an independently checked, monotonic durable term: database
// session identifiers alone may eventually be reused.
func CheckOwner(ctx context.Context, db *sql.DB, dialect storage.Dialect, key string, owner int64) error {
	return CheckOwnerQuery(ctx, db, dialect, key, owner)
}

type OwnerQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CheckOwnerQuery also accepts an existing transaction, so execution fences do
// not acquire a second pool connection while holding a resource transaction.
func CheckOwnerQuery(ctx context.Context, db OwnerQueryer, dialect storage.Dialect, key string, owner int64) error {
	if dialect == storage.SQLite {
		return nil
	}
	if owner <= 0 {
		return ErrLost
	}
	var held bool
	var err error
	switch dialect {
	case storage.Postgres:
		value := uint64(hashKey(key))
		err = db.QueryRowContext(ctx, storage.BindSQL(dialect, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=:owner AND classid=:class_id AND objid=:object_id AND objsubid=1 AND granted)`), owner, int64(uint32(value>>32)), int64(uint32(value))).Scan(&held)
	case storage.MySQL:
		err = db.QueryRowContext(ctx, storage.BindSQL(dialect, `SELECT COALESCE(IS_USED_LOCK(:key) = :owner, FALSE)`), boundedKey(key), owner).Scan(&held)
	default:
		return ErrLost
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLost, err)
	}
	if !held {
		return ErrLost
	}
	return nil
}

type DBLocker struct {
	db      *sql.DB
	dialect storage.Dialect
	localMu sync.Mutex
	local   map[string]struct{}
}

func New(db *sql.DB, dialect storage.Dialect) *DBLocker {
	return &DBLocker{db: db, dialect: dialect, local: make(map[string]struct{})}
}

func (l *DBLocker) TryLock(ctx context.Context, key string) (Lease, error) {
	if l.dialect == storage.SQLite {
		return l.tryLocal(key)
	}
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	ok := false
	switch l.dialect {
	case storage.Postgres:
		err = conn.QueryRowContext(ctx, storage.BindSQL(l.dialect, `SELECT pg_try_advisory_lock(:key)`), hashKey(key)).Scan(&ok)
	case storage.MySQL:
		var value sql.NullInt64
		err = conn.QueryRowContext(ctx, storage.BindSQL(l.dialect, `SELECT GET_LOCK(:key,0)`), boundedKey(key)).Scan(&value)
		ok = value.Valid && value.Int64 == 1
	default:
		err = fmt.Errorf("unsupported lease dialect %q", l.dialect)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !ok {
		conn.Close()
		return nil, ErrBusy
	}
	return &dbLease{conn: conn, dialect: l.dialect, key: key}, nil
}

func (l *DBLocker) tryLocal(key string) (Lease, error) {
	l.localMu.Lock()
	defer l.localMu.Unlock()
	if _, exists := l.local[key]; exists {
		return nil, ErrBusy
	}
	l.local[key] = struct{}{}
	return &localLease{key: key, owner: l}, nil
}

type dbLease struct {
	conn     *sql.Conn
	dialect  storage.Dialect
	key      string
	mu       sync.Mutex
	released bool
	once     sync.Once
	err      error
}

func (l *dbLease) Check(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return ErrLost
	}
	var held bool
	var err error
	switch l.dialect {
	case storage.Postgres:
		value := uint64(hashKey(l.key))
		classID := int64(uint32(value >> 32))
		objectID := int64(uint32(value))
		err = l.conn.QueryRowContext(ctx, storage.BindSQL(l.dialect, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND classid=:class_id AND objid=:object_id AND objsubid=1 AND granted)`), classID, objectID).Scan(&held)
	case storage.MySQL:
		err = l.conn.QueryRowContext(ctx, storage.BindSQL(l.dialect, `SELECT COALESCE(IS_USED_LOCK(:key) = CONNECTION_ID(), FALSE)`), boundedKey(l.key)).Scan(&held)
	default:
		err = fmt.Errorf("unsupported lease dialect %q", l.dialect)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLost, err)
	}
	if !held {
		return ErrLost
	}
	return nil
}

func (l *dbLease) Release(context.Context) error {
	l.once.Do(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		switch l.dialect {
		case storage.Postgres:
			var unlocked bool
			l.err = l.conn.QueryRowContext(releaseCtx, storage.BindSQL(l.dialect, `SELECT pg_advisory_unlock(:key)`), hashKey(l.key)).Scan(&unlocked)
			if l.err == nil && !unlocked {
				l.err = ErrLost
			}
		case storage.MySQL:
			var unlocked sql.NullInt64
			l.err = l.conn.QueryRowContext(releaseCtx, storage.BindSQL(l.dialect, `SELECT RELEASE_LOCK(:key)`), boundedKey(l.key)).Scan(&unlocked)
			if l.err == nil && (!unlocked.Valid || unlocked.Int64 != 1) {
				l.err = ErrLost
			}
		}
		if l.err != nil {
			// A failed session unlock must never return a potentially lock-bearing
			// physical connection to database/sql's idle pool.
			_ = l.conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		if err := l.conn.Close(); l.err == nil {
			l.err = err
		}
		l.released = true
	})
	return l.err
}

type localLease struct {
	key   string
	owner *DBLocker
	once  sync.Once
}

func (l *localLease) Check(context.Context) error {
	l.owner.localMu.Lock()
	defer l.owner.localMu.Unlock()
	if _, held := l.owner.local[l.key]; !held {
		return ErrLost
	}
	return nil
}

func (l *localLease) Release(context.Context) error {
	l.once.Do(func() {
		l.owner.localMu.Lock()
		delete(l.owner.local, l.key)
		l.owner.localMu.Unlock()
	})
	return nil
}

func hashKey(value string) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return int64(hash.Sum64())
}
func boundedKey(value string) string {
	if len(value) <= 64 {
		return value
	}
	return fmt.Sprintf("nsc:%016x", uint64(hashKey(value)))
}
