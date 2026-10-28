package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

// RuntimePresence distinguishes an observed zero-replica Service from a stable
// managed claim whose runtime object is absent. It is private inventory evidence,
// not a mutation authority or a replacement for Service ownership history.
type RuntimePresence struct {
	RuntimeID   string    `json:"runtime_id"`
	Observation int64     `json:"observation"`
	ObservedAt  time.Time `json:"observed_at"`
	ServiceIDs  []string  `json:"service_ids"`
}

func RuntimePresenceID(runtimeID string) string { return runtimeID + ":presence" }

func runtimeObservationTime(dialect Dialect, observed time.Time) time.Time {
	if dialect != SQLite {
		// Shared SQL columns preserve microseconds. Keep the private JSON payload
		// and every authoritative inventory timestamp at the same precision.
		return observed.UTC().Truncate(time.Microsecond)
	}
	return observed
}

func (s *SQLStore) writeRuntimePresenceTx(ctx context.Context, tx *sql.Tx, snapshot model.RuntimeSnapshot, seen map[string]struct{}) error {
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	raw, err := json.Marshal(RuntimePresence{RuntimeID: snapshot.RuntimeID, Observation: snapshot.Observation, ObservedAt: snapshot.ObservedAt, ServiceIDs: ids})
	if err != nil {
		return err
	}
	query := `INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at)
		VALUES(:id,'runtime-presence',:parent_id,:payload,1,:created_at,:updated_at)
		ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,revision=control_documents.revision+1,updated_at=excluded.updated_at`
	if s.dialect == MySQL {
		query = `INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at)
			VALUES(:id,'runtime-presence',:parent_id,:payload,1,:created_at,:updated_at)
			ON DUPLICATE KEY UPDATE payload=VALUES(payload),revision=revision+1,updated_at=VALUES(updated_at)`
	}
	_, err = tx.ExecContext(ctx, s.bind(query), RuntimePresenceID(snapshot.RuntimeID), snapshot.RuntimeID, string(raw), snapshot.ObservedAt, snapshot.ObservedAt)
	return err
}
