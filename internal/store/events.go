package store

import (
	"context"
	"encoding/json"
	"time"
)

// Event is one timeline event.
type Event struct {
	ID     int64
	Camera string // "" for hive-level events
	Kind   string
	At     time.Time
	Data   json.RawMessage
}

// Events returns the event repository.
func (s *Store) Events() *Events { return &Events{s: s} }

// Events persists timeline events.
type Events struct {
	s *Store
}

// Append stores an event and returns its id.
func (r *Events) Append(ctx context.Context, e Event) (int64, error) {
	data := e.Data
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	res, err := r.s.db.ExecContext(ctx, `INSERT INTO event (camera, kind, at_utc, data_json) VALUES (?,?,?,?)`, e.Camera, e.Kind, ts(e.At), string(data))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Range returns a camera's events in [from, to), oldest first.
func (r *Events) Range(ctx context.Context, camera string, from, to time.Time) ([]Event, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id, camera, kind, at_utc, data_json FROM event WHERE camera=? AND at_utc >= ? AND at_utc < ? ORDER BY at_utc, id`, camera, ts(from), ts(to))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var e Event
		var at, data string
		if err := rows.Scan(&e.ID, &e.Camera, &e.Kind, &at, &data); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse("2006-01-02T15:04:05.000000Z", at)
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}
