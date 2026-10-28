package lease

import (
	"testing"
)

func TestSQLiteProcessLockExcludesSecondOwner(t *testing.T) {
	dsn := t.TempDir() + "/state.db"
	first, err := AcquireSQLiteProcessLock(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err = AcquireSQLiteProcessLock(dsn); err == nil {
		t.Fatal("expected the second process lock to fail")
	}
}

func TestSQLiteProcessLockRejectsMemoryDatabase(t *testing.T) {
	if _, err := AcquireSQLiteProcessLock(":memory:"); err == nil {
		t.Fatal("expected in-memory SQLite rejection")
	}
}
