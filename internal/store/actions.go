package store

import (
	"context"
	"encoding/json"
	"time"
)

// Action is one row of the local audit trail (ADR-0011).
type Action struct {
	ID      int64
	At      time.Time
	Actor   string
	Action  string
	Target  string
	Details json.RawMessage
}

// Actions returns the action log repository.
func (s *Store) Actions() *Actions { return &Actions{s: s} }

// Actions is append-only.
type Actions struct {
	s *Store
}

// Append records an action.
func (r *Actions) Append(ctx context.Context, a Action) error {
	details := a.Details
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	at := a.At
	if at.IsZero() {
		at = time.Now()
	}
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO action_log (at_utc, actor, action, target, details_json) VALUES (?,?,?,?,?)`,
		ts(at), a.Actor, a.Action, a.Target, string(details))
	return err
}

// Recent returns the newest actions, newest first.
func (r *Actions) Recent(ctx context.Context, limit int) ([]Action, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id, at_utc, actor, action, target, details_json FROM action_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Action
	for rows.Next() {
		var a Action
		var at, details string
		if err := rows.Scan(&a.ID, &at, &a.Actor, &a.Action, &a.Target, &details); err != nil {
			return nil, err
		}
		a.At, _ = time.Parse("2006-01-02T15:04:05.000000Z", at)
		a.Details = json.RawMessage(details)
		out = append(out, a)
	}
	return out, rows.Err()
}
