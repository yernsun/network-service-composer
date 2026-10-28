package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yernsun/network-service-composer/internal/id"
)

func (s *SQLStore) RecordSnapshot(ctx context.Context, poolID string, revision int64, digest string, publicPayload []byte, createdAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	lockQuery := `SELECT id FROM gateway_pools WHERE id=:id`
	if s.dialect != SQLite {
		lockQuery += ` FOR UPDATE`
	}
	var lockedPoolID string
	if err = tx.QueryRowContext(ctx, s.bind(lockQuery), poolID).Scan(&lockedPoolID); err != nil {
		return mapError(err)
	}
	var existingDigest string
	err = tx.QueryRowContext(ctx, s.bind(`SELECT sha256 FROM pool_snapshots WHERE pool_id=:pool_id AND revision=:revision`), poolID, revision).Scan(&existingDigest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO pool_snapshots(id,pool_id,revision,sha256,public_payload,created_at) VALUES(:id,:pool_id,:revision,:sha256,:public_payload,:created_at)`),
			id.New(id.Snapshot), poolID, revision, digest, publicPayload, createdAt)
	case err != nil:
		return err
	default:
		if existingDigest != digest {
			return fmt.Errorf("%w: revision %d for pool %s produced multiple snapshot digests", ErrConflict, revision, poolID)
		}
		_, err = tx.ExecContext(ctx, s.bind(`UPDATE pool_snapshots SET public_payload=:public_payload,created_at=:created_at WHERE pool_id=:pool_id AND revision=:revision`), publicPayload, createdAt, poolID, revision)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE gateway_pools SET last_snapshot_sha256=:last_snapshot_sha256,last_snapshot_built_at=:last_snapshot_built_at WHERE id=:id`), digest, createdAt, poolID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, s.bind(`SELECT id FROM pool_snapshots WHERE pool_id=:pool_id ORDER BY revision DESC,id DESC`), poolID)
	if err != nil {
		return err
	}
	var staleIDs []string
	index := 0
	for rows.Next() {
		var snapshotID string
		if err = rows.Scan(&snapshotID); err != nil {
			rows.Close()
			return err
		}
		if index >= 3 {
			staleIDs = append(staleIDs, snapshotID)
		}
		index++
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, snapshotID := range staleIDs {
		if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM pool_snapshots WHERE id=:id`), snapshotID); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
