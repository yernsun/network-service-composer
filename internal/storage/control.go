package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

// ControlDocument stores versioned control-plane configuration. Secrets must
// be encrypted by the caller before entering Payload. Writes can atomically
// guard/update multiple documents (for example a draft and its history).
type ControlDocument struct {
	ID        string          `json:"id"`
	Kind      string          `json:"-"`
	ParentID  string          `json:"-"`
	Payload   json.RawMessage `json:"-"`
	Revision  int64           `json:"revision"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
type ControlWrite struct {
	Document ControlDocument
	Expected int64
	Remove   bool
	Archive  bool
}
type RouteWrite struct {
	Route            model.Route `json:"route"`
	Delete           bool        `json:"delete,omitempty"`
	ExpectedRevision int64       `json:"expected_revision"`
}

func (s *SQLStore) PruneControlHistory(ctx context.Context, kind, parent string, keep int, protected []string) error {
	if keep < 1 {
		return ErrConflict
	}
	rows, e := s.db.QueryContext(ctx, s.bind(`SELECT id FROM control_documents WHERE kind=:kind AND parent_id=:parent_id ORDER BY created_at DESC,id DESC`), kind, parent)
	if e != nil {
		return e
	}
	protect := map[string]bool{}
	for _, key := range protected {
		protect[key] = true
	}
	stale := []string{}
	count := 0
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			rows.Close()
			return e
		}
		if count >= keep && !protect[key] {
			stale = append(stale, key)
		}
		count++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, key := range stale {
		if _, e = s.db.ExecContext(ctx, s.bind(`DELETE FROM control_documents WHERE id=:id AND kind=:kind AND parent_id=:parent_id`), key, kind, parent); e != nil {
			return e
		}
	}
	return nil
}

func (s *SQLStore) GetControlDocument(ctx context.Context, key string) (ControlDocument, error) {
	var d ControlDocument
	var payload string
	err := s.db.QueryRowContext(ctx, s.bind(`SELECT id,kind,parent_id,payload,revision,created_at,updated_at FROM control_documents WHERE id=:id`), key).Scan(&d.ID, &d.Kind, &d.ParentID, &payload, &d.Revision, &d.CreatedAt, &d.UpdatedAt)
	d.Payload = json.RawMessage(payload)
	return d, mapError(err)
}
func (s *SQLStore) ListControlDocuments(ctx context.Context, kind, parent string, page Page) ([]ControlDocument, error) {
	page = page.normalized()
	args := []any{kind}
	query := `SELECT id,kind,parent_id,payload,revision,created_at,updated_at FROM control_documents WHERE kind=:kind`
	if parent != "" {
		query += ` AND parent_id=:parent_id`
		args = append(args, parent)
	}
	if page.After != "" {
		query += ` AND id>:id`
		args = append(args, page.After)
	}
	query += ` ORDER BY id LIMIT :limit`
	args = append(args, page.Limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ControlDocument{}
	for rows.Next() {
		var d ControlDocument
		var payload string
		if err = rows.Scan(&d.ID, &d.Kind, &d.ParentID, &payload, &d.Revision, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.Payload = json.RawMessage(payload)
		result = append(result, d)
	}
	return result, rows.Err()
}
func (s *SQLStore) WriteControlDocuments(ctx context.Context, writes []ControlWrite) error {
	return s.writeControlDocuments(ctx, "", 0, writes, nil)
}
func (s *SQLStore) PublishNginxDocuments(ctx context.Context, pool string, revision int64, writes []ControlWrite) error {
	return s.writeControlDocuments(ctx, pool, revision, writes, nil)
}
func (s *SQLStore) PublishNginxChanges(ctx context.Context, pool string, revision int64, writes []ControlWrite, routes []RouteWrite) error {
	return s.writeControlDocuments(ctx, pool, revision, writes, routes)
}
func (s *SQLStore) writeControlDocuments(ctx context.Context, pool string, revision int64, writes []ControlWrite, routes []RouteWrite) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if pool != "" {
		var guard string
		e := tx.QueryRowContext(ctx, s.bind(`SELECT id FROM control_documents WHERE id=:id AND kind='project-ingress-guard'`), "project-ingress-guard:"+pool).Scan(&guard)
		if e == nil {
			return ErrConflict
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		result, e := tx.ExecContext(ctx, s.bind(`UPDATE gateway_pools SET desired_revision=desired_revision+1,revision=revision+1,updated_at=:updated_at WHERE id=:id AND desired_revision=:desired_revision AND enabled=:enabled`), time.Now().UTC(), pool, revision, true)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrPrecondition
		}
	}
	if err = s.writeControlTx(ctx, tx, writes); err != nil {
		return err
	}
	if err = s.writeNginxRoutesTx(ctx, tx, pool, routes); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) writeControlTx(ctx context.Context, tx *sql.Tx, writes []ControlWrite) error {
	for _, w := range writes {
		d := w.Document
		if !w.Archive && (d.Kind == "nginx-draft" || d.Kind == "nginx-active") {
			var configuration struct {
				Snippets []struct {
					ResourceID string `json:"resource_id"`
				} `json:"snippets"`
			}
			if err := json.Unmarshal(d.Payload, &configuration); err != nil {
				return err
			}
			for _, snippet := range configuration.Snippets {
				if err := s.guardProjectResourceTx(ctx, tx, snippet.ResourceID); err != nil {
					return err
				}
			}
		}
		if d.Kind != "project-guard" && d.Kind != "project-ingress-guard" && d.Kind != "project-deleted" && d.Kind != "project-deletion" && d.Kind != "project-deletion-preview" {
			if err := s.guardProjectResourceTx(ctx, tx, d.ID); err != nil {
				return err
			}
			if d.ParentID != "" {
				if err := s.guardProjectResourceTx(ctx, tx, d.ParentID); err != nil {
					return err
				}
			}
		}
		var err error
		if d.Kind == "variables" {
			if err = s.lockControlOwner(ctx, tx, d.ParentID); err != nil {
				return err
			}
		}
		if w.Remove || w.Archive {
			var result sql.Result
			if w.Remove {
				result, err = tx.ExecContext(ctx, s.bind(`DELETE FROM control_documents WHERE id=:id AND kind=:kind AND revision=:revision`), d.ID, d.Kind, w.Expected)
			} else {
				result, err = tx.ExecContext(ctx, s.bind(`UPDATE control_documents SET kind=:kind,revision=revision+1,updated_at=:updated_at WHERE id=:id AND kind=:kind_2 AND revision=:revision`), "deleted-"+d.Kind, d.UpdatedAt, d.ID, d.Kind, w.Expected)
			}
			if err == nil {
				err = requireAffected(result, true)
			}
		} else if w.Expected == 0 {
			_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO control_documents(id,kind,parent_id,payload,revision,created_at,updated_at) VALUES(:id,:kind,:parent_id,:payload,1,:revision,:created_at)`), d.ID, d.Kind, d.ParentID, string(d.Payload), d.CreatedAt, d.UpdatedAt)
		} else {
			result, e := tx.ExecContext(ctx, s.bind(`UPDATE control_documents SET payload=:payload,revision=revision+1,updated_at=:updated_at WHERE id=:id AND kind=:kind AND parent_id=:parent_id AND revision=:revision`), string(d.Payload), d.UpdatedAt, d.ID, d.Kind, d.ParentID, w.Expected)
			err = e
			if err == nil {
				n, e := result.RowsAffected()
				err = e
				if n != 1 && err == nil {
					err = ErrPrecondition
				}
			}
		}
		if err != nil {
			return mapError(err)
		}
		if err = s.projectNotificationWork(ctx, tx, w); err != nil {
			return mapError(err)
		}
	}
	return nil
}

// Serialize layer creation as well as updates with deployment snapshots.
func (s *SQLStore) lockControlOwner(ctx context.Context, tx *sql.Tx, key string) error {
	query := `SELECT id FROM control_documents WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var value string
	return mapError(tx.QueryRowContext(ctx, s.bind(query), key).Scan(&value))
}

func (s *SQLStore) ReencryptControlDocument(ctx context.Context, old ControlDocument, payload json.RawMessage) error {
	result, e := s.db.ExecContext(ctx, s.bind(`UPDATE control_documents SET payload=:payload WHERE id=:id AND revision=:revision AND payload=:payload_2`), string(payload), old.ID, old.Revision, string(old.Payload))
	if e != nil {
		return e
	}
	return requireAffected(result, true)
}
