package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// CloudState returns the repository of cloud-pushed documents.
func (s *Store) CloudState() *CloudState { return &CloudState{s: s} }

// CloudState is a small key → JSON store.
type CloudState struct {
	s *Store
}

// Put replaces the document under key.
func (r *CloudState) Put(ctx context.Context, key string, value json.RawMessage) error {
	if !json.Valid(value) {
		return errors.New("store: cloud state must be valid JSON")
	}
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO cloud_state (key, value_json, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
		key, string(value), ts(time.Now()))
	return err
}

// Get returns the document and when it was stored; ErrNotFound when absent.
func (r *CloudState) Get(ctx context.Context, key string) (json.RawMessage, time.Time, error) {
	var raw, at string
	err := r.s.db.QueryRowContext(ctx, `SELECT value_json, updated_at FROM cloud_state WHERE key = ?`, key).Scan(&raw, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, ErrNotFound
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	t, _ := time.Parse(time.RFC3339Nano, at)
	return json.RawMessage(raw), t, nil
}
