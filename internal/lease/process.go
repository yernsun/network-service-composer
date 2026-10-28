package lease

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ProcessLock prevents two controller processes from opening the same SQLite
// database. Database-level configuration validation alone cannot catch that
// unsafe deployment topology.
type ProcessLock struct {
	file *os.File
}

func AcquireSQLiteProcessLock(dsn string) (*ProcessLock, error) {
	path, err := sqlitePath(dsn)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path+".nsc.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open SQLite process lock: %w", err)
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("SQLite database is already owned by another controller: %w", err)
	}
	return &ProcessLock{file: file}, nil
}

func (l *ProcessLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func sqlitePath(dsn string) (string, error) {
	value := strings.TrimSpace(dsn)
	if value == "" {
		return "", fmt.Errorf("SQLite DSN is empty")
	}
	if strings.HasPrefix(value, "file:") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", fmt.Errorf("parse SQLite DSN: %w", err)
		}
		value = parsed.Path
		if value == "" {
			value = parsed.Opaque
		}
	}
	if value == ":memory:" || value == "::memory:" || strings.Contains(value, "mode=memory") {
		return "", fmt.Errorf("in-memory SQLite is not supported by the controller process")
	}
	value = strings.SplitN(value, "?", 2)[0]
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve SQLite path: %w", err)
	}
	return absolute, nil
}
