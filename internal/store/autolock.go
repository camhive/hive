package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AutolockSettings are the owner's automatic-lock settings for the
// encrypted archive (#147); zero is off.
type AutolockSettings struct {
	After time.Duration // lock after this long without footage being read
	Away  time.Duration // lock this long after the owner left home
}

// Autolock returns the repository of the automatic-lock settings.
func (s *Store) Autolock() *Autolock { return &Autolock{s: s} }

// Autolock holds one row of settings.
type Autolock struct {
	s *Store
}

// Get returns the settings; both off when never set.
func (r *Autolock) Get(ctx context.Context) (AutolockSettings, error) {
	var after, away int64
	err := r.s.db.QueryRowContext(ctx, `SELECT after_s, away_s FROM archive_autolock WHERE id = 1`).Scan(&after, &away)
	if errors.Is(err, sql.ErrNoRows) {
		return AutolockSettings{}, nil
	}
	if err != nil {
		return AutolockSettings{}, err
	}
	return AutolockSettings{After: time.Duration(after) * time.Second, Away: time.Duration(away) * time.Second}, nil
}

// Set replaces the settings.
func (r *Autolock) Set(ctx context.Context, a AutolockSettings) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO archive_autolock (id, after_s, away_s, updated_at) VALUES (1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET after_s = excluded.after_s, away_s = excluded.away_s, updated_at = excluded.updated_at`,
		int64(a.After/time.Second), int64(a.Away/time.Second), ts(time.Now()))
	return err
}
