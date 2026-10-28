package lease

import (
	"context"
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestSQLiteLease(t *testing.T) {
	locker := New(nil, storage.SQLite)
	first, err := locker.TryLock(context.Background(), "runtime/1")
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Check(context.Background()); err != nil {
		t.Fatalf("fresh lease was not held: %v", err)
	}
	if _, err = locker.TryLock(context.Background(), "runtime/1"); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected busy, got %v", err)
	}
	if err = first.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = first.Check(context.Background()); !errors.Is(err, ErrLost) {
		t.Fatalf("released lease check = %v, want ErrLost", err)
	}
	if _, err = locker.TryLock(context.Background(), "runtime/1"); err != nil {
		t.Fatal(err)
	}
}
