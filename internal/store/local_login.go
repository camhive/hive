package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// LocalCredential is the owner's local emergency login (#57): a username
// and the argon2id hash of the password as a PHC string.
type LocalCredential struct {
	Username  string
	Hash      string
	UpdatedAt time.Time
}

// LocalLogin returns the repository of the local login.
func (s *Store) LocalLogin() *LocalLogin { return &LocalLogin{s: s} }

// LocalLogin holds at most one credential.
type LocalLogin struct {
	s *Store
}

// Set enables the local login or replaces its credential.
func (r *LocalLogin) Set(ctx context.Context, c LocalCredential) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO local_login (id, username, password_hash, updated_at) VALUES (1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET username = excluded.username, password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		c.Username, c.Hash, ts(c.UpdatedAt))
	return err
}

// Get returns the credential; ErrNotFound while the local login is disabled.
func (r *LocalLogin) Get(ctx context.Context) (LocalCredential, error) {
	var c LocalCredential
	var at string
	err := r.s.db.QueryRowContext(ctx, `SELECT username, password_hash, updated_at FROM local_login WHERE id = 1`).Scan(&c.Username, &c.Hash, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return LocalCredential{}, ErrNotFound
	}
	if err != nil {
		return LocalCredential{}, err
	}
	c.UpdatedAt, _ = time.Parse("2006-01-02T15:04:05.000000Z", at)
	return c, nil
}

// Delete disables the local login; nothing of it is left.
func (r *LocalLogin) Delete(ctx context.Context) error {
	_, err := r.s.db.ExecContext(ctx, `DELETE FROM local_login`)
	return err
}
