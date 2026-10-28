package storage

import "context"

// Drafts do not change the desired application revision. Their writes still
// serialize with deployment admission and project deletion in one transaction.
func (s *SQLStore) WriteApplicationDocuments(ctx context.Context, application string, expected int64, writes []ControlWrite) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err := s.guardProjectResourceTx(ctx, tx, application); err != nil {
		return err
	}
	query := `SELECT revision FROM applications WHERE id=:id`
	if s.dialect != SQLite {
		query += ` FOR UPDATE`
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, s.bind(query), application).Scan(&revision); err != nil {
		return mapError(err)
	}
	if revision != expected {
		return ErrPrecondition
	}
	for _, write := range writes {
		if write.Document.ParentID != application {
			return ErrConflict
		}
	}
	if err := s.writeControlTx(ctx, tx, writes); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
