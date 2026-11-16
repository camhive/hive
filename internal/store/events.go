package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Event is one timeline Event (ADR-0009, ADR-0020).
type Event struct {
	ID     int64
	Camera string // "" for hive-level events
	Kind   string
	At     time.Time
	End    time.Time // zero only while a lasting Event is still going on
	Data   json.RawMessage
}

// Events returns the event repository.
func (s *Store) Events() *Events { return &Events{s: s} }

// Events persists timeline events.
type Events struct {
	s *Store
}

// Append stores an Event that has happened: an instantaneous one ends where
// it began unless End says otherwise. It returns the id.
func (r *Events) Append(ctx context.Context, e Event) (int64, error) {
	if e.End.IsZero() {
		e.End = e.At
	}
	return r.insert(ctx, e, ts(e.End))
}

// Begin stores a lasting Event that is still going on (no end yet); End
// closes it. It returns the id.
func (r *Events) Begin(ctx context.Context, e Event) (int64, error) {
	return r.insert(ctx, e, nil)
}

func (r *Events) insert(ctx context.Context, e Event, end any) (int64, error) {
	data := e.Data
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	res, err := r.s.db.ExecContext(ctx, `INSERT INTO event (camera, kind, at_utc, end_utc, data_json) VALUES (?,?,?,?,?)`, e.Camera, e.Kind, ts(e.At), end, string(data))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// End sets the end of an event. Setting it again overwrites; an unknown id
// is not an error.
func (r *Events) End(ctx context.Context, id int64, end time.Time) error {
	_, err := r.s.db.ExecContext(ctx, `UPDATE event SET end_utc=? WHERE id=?`, ts(end), id)
	return err
}

// CloseOpen ends every Event still going on at its own start: after a crash
// nothing knows how long it lasted. It returns how many it closed.
func (r *Events) CloseOpen(ctx context.Context) (int64, error) {
	res, err := r.s.db.ExecContext(ctx, `UPDATE event SET end_utc=at_utc WHERE end_utc IS NULL`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Range returns a camera's events and the hive-level ones that overlap
// [from, to): they begin before to and end at or after from, or still go
// on. Oldest first.
func (r *Events) Range(ctx context.Context, camera string, from, to time.Time) ([]Event, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id, camera, kind, at_utc, end_utc, data_json FROM event
		WHERE camera IN (?, '') AND at_utc < ? AND (end_utc IS NULL OR end_utc >= ?) ORDER BY at_utc, id`, camera, ts(to), ts(from))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var e Event
		var at, data string
		var end sql.NullString
		if err := rows.Scan(&e.ID, &e.Camera, &e.Kind, &at, &end, &data); err != nil {
			return nil, err
		}
		e.At, _ = parseTS(at)
		if end.Valid {
			e.End, _ = parseTS(end.String)
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}
