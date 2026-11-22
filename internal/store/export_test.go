package store

import "context"

// HoldReadForTest opens a read transaction on its own connection and
// keeps it until release: a reader the checkpoint must wait for.
func (s *Store) HoldReadForTest(ctx context.Context) (release func(), err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM action_log`).Scan(&n); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return func() { _ = tx.Rollback() }, nil
}
