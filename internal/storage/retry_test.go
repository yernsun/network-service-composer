package storage

import (
	"context"
	"errors"
	"testing"
)

func TestRetryableTransactionError(t *testing.T) {
	for _, value := range []error{
		errors.New("deadlock detected (SQLSTATE 40P01)"),
		errors.New("could not serialize access (SQLSTATE 40001)"),
		errors.New("Error 1213: Deadlock found when trying to get lock"),
		errors.New("database is locked"),
	} {
		if !retryableTransactionError(value) {
			t.Errorf("expected %q to be retryable", value)
		}
	}
	if retryableTransactionError(errors.New("duplicate key")) || retryableTransactionError(context.Canceled) {
		t.Fatal("non-transactional errors must not be retried")
	}
}
