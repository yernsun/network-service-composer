package storage

import (
	"context"
	"database/sql"
	"sort"
)

// Replacement changes inventory ownership only. Retired rows, protection claims,
// runtime objects, volumes and historical revisions are preserved.
func (s *SQLStore) replaceSystemApplicationRuntimeTx(ctx context.Context, tx *sql.Tx, registration SystemApplicationRegistration, previous string) error {
	r := registration.Replacement
	a := registration.Application
	if r == nil || r.PreviousRuntimeID != previous || r.ExpectedRevision <= 0 || len(r.ServiceRevisions) == 0 {
		return ErrConflict
	}
	var count int
	if err := tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM services WHERE application_id=:application_id`), a.ID).Scan(&count); err != nil {
		return err
	}
	if count != len(r.ServiceRevisions) {
		return ErrPrecondition
	}
	ids := make([]string, 0, len(r.ServiceRevisions))
	for id := range r.ServiceRevisions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, s.bind(`UPDATE services SET application_id=NULL,revision=revision+1,updated_at=:updated_at WHERE id=:id AND application_id=:application_id AND runtime_id=:runtime_id AND revision=:revision AND managed=:managed AND running_instances=0`), a.UpdatedAt, id, a.ID, previous, r.ServiceRevisions[id], false)
		if err != nil {
			return err
		}
		if err = requireAffected(result, true); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, s.bind(`UPDATE applications SET runtime_id=:runtime_id,revision=revision+1,updated_at=:updated_at WHERE id=:id AND runtime_id=:runtime_id_2 AND revision=:revision`), a.RuntimeID, a.UpdatedAt, a.ID, previous, r.ExpectedRevision)
	if err != nil {
		return err
	}
	return requireAffected(result, true)
}
