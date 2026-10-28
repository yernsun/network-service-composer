package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/yernsun/network-service-composer/internal/model"
)

// SystemApplicationRegistration is an operator-selected inventory association,
// never a deployment request. It contains no executable source or revision.
type SystemApplicationRegistration struct {
	Application      model.Application
	ServiceIDs       []string
	ServiceRevisions map[string]int64
	Replacement      *SystemApplicationReplacement
}

type SystemApplicationReplacement struct {
	PreviousRuntimeID string
	ExpectedRevision  int64
	ServiceRevisions  map[string]int64
}

// ReconcileSystemApplications commits the complete application membership and
// document claims together. The owning project lock serializes all initializers.
func (s *SQLStore) ReconcileSystemApplications(ctx context.Context, project string, registrations []SystemApplicationRegistration, writes []ControlWrite) error {
	if ctx.Value(systemBootstrapKey{}) != true {
		return ErrSystemProtected
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.lockControlOwner(ctx, tx, project); err != nil {
		return err
	}
	var raw string
	query := `SELECT payload FROM control_documents WHERE id=:id AND kind='project'`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	if err = tx.QueryRowContext(ctx, s.bind(query), project).Scan(&raw); err != nil {
		return mapError(err)
	}
	var p struct {
		System bool `json:"system"`
	}
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if !p.System {
		return ErrConflict
	}
	// All discovery snapshots and system registrations acquire Service rows in
	// stable ID order. Lock the complete set before changing any membership;
	// otherwise application grouping can lock A then B while discovery locks B
	// then A and PostgreSQL repeatedly aborts one transaction.
	locked := map[string]bool{}
	for _, r := range registrations {
		for _, id := range r.ServiceIDs {
			locked[id] = true
		}
		if r.Replacement != nil {
			for id := range r.Replacement.ServiceRevisions {
				locked[id] = true
			}
		}
		rows, queryErr := tx.QueryContext(ctx, s.bind(`SELECT id FROM services WHERE application_id=:application_id`), r.Application.ID)
		if queryErr != nil {
			return queryErr
		}
		for rows.Next() {
			var id string
			if queryErr = rows.Scan(&id); queryErr != nil {
				rows.Close()
				return queryErr
			}
			locked[id] = true
		}
		if queryErr = rows.Err(); queryErr != nil {
			rows.Close()
			return queryErr
		}
		if queryErr = rows.Close(); queryErr != nil {
			return queryErr
		}
	}
	serviceIDs := make([]string, 0, len(locked))
	for id := range locked {
		serviceIDs = append(serviceIDs, id)
	}
	sort.Strings(serviceIDs)
	for _, id := range serviceIDs {
		query := `SELECT id FROM services WHERE id=:id`
		if s.dialect != SQLite {
			query += ` FOR UPDATE`
		}
		var found string
		if err = tx.QueryRowContext(ctx, s.bind(query), id).Scan(&found); err != nil {
			return mapError(err)
		}
	}
	for _, r := range registrations {
		a := r.Application
		if !a.System || a.Environment == nil || a.Environment.ProjectID != project || a.RuntimeID == "" || len(r.ServiceIDs) == 0 {
			return ErrConflict
		}
		var runtime, name string
		err = tx.QueryRowContext(ctx, s.bind(`SELECT runtime_id,name FROM applications WHERE id=:id`), a.ID).Scan(&runtime, &name)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err = tx.ExecContext(ctx, s.bind(`INSERT INTO applications (`+applicationColumns+`) VALUES (`+columnPlaceholders(applicationColumns)+`)`), applicationArgs(&a)...); err != nil {
				return mapError(err)
			}
		} else if err != nil {
			return err
		} else {
			if name != a.Name {
				return ErrConflict
			}
			var kind string
			if err = tx.QueryRowContext(ctx, s.bind(`SELECT kind FROM control_documents WHERE id=:id`), a.ID).Scan(&kind); err != nil {
				return mapError(err)
			}
			if kind != "system-application" {
				return ErrConflict
			}
			if runtime != a.RuntimeID {
				if err = s.replaceSystemApplicationRuntimeTx(ctx, tx, r, runtime); err != nil {
					return err
				}
			}
			if _, err = tx.ExecContext(ctx, s.bind(`UPDATE applications SET status=:status,last_error=:last_error,revision=revision+1,updated_at=:updated_at WHERE id=:id AND (status<>:status_2 OR last_error<>:last_error_2)`), a.Status, a.LastError, a.UpdatedAt, a.ID, a.Status, a.LastError); err != nil {
				return err
			}
		}
		// A complete registration replaces current membership. Retired services
		// keep their records and protection claims; missing managed services and
		// still-present objects must never lose ownership here.
		args := []any{a.UpdatedAt, a.ID, a.RuntimeID, model.ServiceRemoved, false}
		for _, sid := range r.ServiceIDs {
			args = append(args, sid)
		}
		if _, err = tx.ExecContext(ctx, s.bind(`UPDATE services SET application_id=NULL,revision=revision+1,updated_at=:updated_at WHERE application_id=:application_id AND runtime_id=:runtime_id AND status=:status AND managed=:managed AND id NOT IN (`+namedPlaceholders("service_id", len(r.ServiceIDs))+`)`), args...); err != nil {
			return err
		}
		for _, sid := range r.ServiceIDs {
			var owner sql.NullString
			var rt string
			var managed bool
			var revision int64
			query := `SELECT application_id,runtime_id,managed,revision FROM services WHERE id=:id`
			if s.dialect != SQLite {
				query += ` FOR UPDATE`
			}
			if err = tx.QueryRowContext(ctx, s.bind(query), sid).Scan(&owner, &rt, &managed, &revision); err != nil {
				return mapError(err)
			}
			if expected, ok := r.ServiceRevisions[sid]; ok && expected != revision {
				return ErrPrecondition
			}
			if rt != a.RuntimeID || managed || (owner.String != "" && owner.String != a.ID) {
				return ErrConflict
			}
			if owner.String != a.ID {
				if _, err = tx.ExecContext(ctx, s.bind(`UPDATE services SET application_id=:application_id,revision=revision+1,updated_at=:updated_at WHERE id=:id`), a.ID, a.UpdatedAt, sid); err != nil {
					return err
				}
			}
		}
	}
	if err = s.writeControlTx(ctx, tx, writes); err != nil {
		return err
	}
	return tx.Commit()
}
