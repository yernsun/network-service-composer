package storage

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

// ValidateRecoverySchema is read only. Goose's GetDBVersion creates a missing
// journal, which is unsuitable for inspecting an untrusted restored database.
// Match Goose's latest-record-per-version semantics, including rolled back
// versions, against the same embedded migrations used by normal migration.
func (s *SQLStore) ValidateRecoverySchema(ctx context.Context) error {
	entries, err := migrationFS.ReadDir("migrations/" + string(s.dialect))
	if err != nil {
		return err
	}
	var latest int64
	authority := map[int64]bool{}
	versions := []int64{}
	for _, entry := range entries {
		prefix, _, _ := strings.Cut(entry.Name(), "_")
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid embedded migration version: %w", err)
		}
		if version > latest {
			latest = version
		}
		authority[version] = true
		versions = append(versions, version)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT version_id,is_applied FROM "+goose.TableName()+" ORDER BY id DESC")
	if err != nil {
		return fmt.Errorf("recovery requires an existing compatible migration journal: %w", err)
	}
	defer rows.Close()
	states := map[int64]bool{}
	var current int64
	foundCurrent := false
	for rows.Next() {
		var version int64
		var applied bool
		if err = rows.Scan(&version, &applied); err != nil {
			return err
		}
		if _, seen := states[version]; seen {
			continue
		}
		states[version] = applied
		if applied && !foundCurrent {
			current = version
			foundCurrent = true
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if current != latest {
		return fmt.Errorf("recovery schema version %d is incompatible with embedded version %d; migrate a compatible restore explicitly before inspection", current, latest)
	}
	for version, applied := range states {
		if applied && version != 0 && !authority[version] {
			return fmt.Errorf("recovery journal contains an applied migration outside embedded authority: %d", version)
		}
	}
	for _, version := range versions {
		if !states[version] {
			return fmt.Errorf("recovery journal is missing applied embedded migration: %d", version)
		}
	}
	return nil
}
