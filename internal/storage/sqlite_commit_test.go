package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// A busy COMMIT is an unknown/failed write, never a reusable open transaction.
// Regression for the driver defect observed during real workspace acceptance.
func TestSQLiteFailedCommitKeepsSubsequentProjectWritesUsable(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "commit.db")
	writer, err := Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx := context.Background()
	if _, err = writer.DB().ExecContext(ctx, "CREATE TABLE fixture (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	read, err := reader.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback()
	var count int
	if err = read.QueryRowContext(ctx, "SELECT COUNT(*) FROM fixture").Scan(&count); err != nil {
		t.Fatal(err)
	}
	write, err := writer.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer write.Rollback()
	if _, err = write.ExecContext(ctx, "INSERT INTO fixture VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	err = write.Commit()
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code() != 5 {
		t.Fatal("expected a busy commit while the reader holds its snapshot", err)
	}
	if err = read.Rollback(); err != nil {
		t.Fatal(err)
	}
	fresh, err := writer.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("failed commit poisoned the pooled connection", err)
	}
	defer fresh.Rollback()
	if err = fresh.QueryRowContext(ctx, "SELECT COUNT(*) FROM fixture").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed transaction leaked uncommitted state", count, err)
	}
	if _, err = fresh.ExecContext(ctx, "INSERT INTO fixture VALUES(2)"); err != nil {
		t.Fatal(err)
	}
	if err = fresh.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = reader.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM fixture WHERE id=2").Scan(&count); err != nil || count != 1 {
		t.Fatal("subsequent transaction did not persist", count, err)
	}
}
