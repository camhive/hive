package store

import (
	"context"
	"fmt"
	"time"
)

// SegmentRow is one archive segment as stored (ADR-0009).
type SegmentRow struct {
	Camera   string
	Stream   string
	Start    time.Time
	Duration time.Duration
	Bytes    int64
	Path     string
	Complete bool
	Analysis int    // version of the reading that filled the row
	Activity []byte // JSON profile for the timeline; nil when none
}

// Segments returns the archive segment repository.
func (s *Store) Segments() *Segments { return &Segments{s: s} }

// Segments persists SegmentRow values.
type Segments struct {
	s *Store
}

const segmentColumns = `camera, stream, start_utc, duration_ms, bytes, path, complete, analysis, activity`

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z") }

// Upsert inserts or updates by (camera, stream, start).
func (r *Segments) Upsert(ctx context.Context, row SegmentRow) error {
	_, err := r.s.db.ExecContext(ctx, `
		INSERT INTO segment (`+segmentColumns+`) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(camera, stream, start_utc) DO UPDATE SET
		  duration_ms=excluded.duration_ms, bytes=excluded.bytes, path=excluded.path, complete=excluded.complete,
		  analysis=excluded.analysis, activity=excluded.activity`,
		row.Camera, row.Stream, ts(row.Start), row.Duration.Milliseconds(), row.Bytes, row.Path, boolInt(row.Complete), row.Analysis, nullText(row.Activity))
	if err != nil {
		return fmt.Errorf("store: upsert segment: %w", err)
	}
	return nil
}

// Delete removes one segment row; unknown rows are not an error.
func (r *Segments) Delete(ctx context.Context, camera, stream string, start time.Time) error {
	_, err := r.s.db.ExecContext(ctx, `DELETE FROM segment WHERE camera=? AND stream=? AND start_utc=?`, camera, stream, ts(start))
	return err
}

// Range returns segments of a stream that overlap [from, to), oldest first.
// A segment with unknown duration (0) is treated as a point at its start.
func (r *Segments) Range(ctx context.Context, camera, stream string, from, to time.Time) ([]SegmentRow, error) {
	// Overlap: start < to AND start + duration > from. SQLite compares the
	// RFC 3339 strings lexically, which is correct for fixed-width UTC.
	rows, err := r.s.db.QueryContext(ctx, `
		SELECT `+segmentColumns+` FROM segment
		WHERE camera=? AND stream=? AND start_utc < ?
		  AND strftime('%Y-%m-%dT%H:%M:%fZ', start_utc, '+' || (duration_ms/1000.0) || ' seconds') > ?
		ORDER BY start_utc`, camera, stream, ts(to), ts(from))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanSegments(rows)
}

// All returns every segment, oldest first (used by reconciliation).
func (r *Segments) All(ctx context.Context) ([]SegmentRow, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+segmentColumns+` FROM segment ORDER BY camera, stream, start_utc`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanSegments(rows)
}

// Usage is the archive total.
type Usage struct {
	Segments int
	Bytes    int64
}

// Usage sums segments and bytes.
func (r *Segments) Usage(ctx context.Context) (Usage, error) {
	var u Usage
	err := r.s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(bytes),0) FROM segment`).Scan(&u.Segments, &u.Bytes)
	return u, err
}

// nullText stores an empty value as NULL.
func nullText(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

type rowsLike interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanSegments(rows rowsLike) ([]SegmentRow, error) {
	var out []SegmentRow
	for rows.Next() {
		var (
			row        SegmentRow
			start      string
			durationMS int64
			complete   int
		)
		if err := rows.Scan(&row.Camera, &row.Stream, &start, &durationMS, &row.Bytes, &row.Path, &complete, &row.Analysis, &row.Activity); err != nil {
			return nil, err
		}
		t, err := time.Parse("2006-01-02T15:04:05.000000Z", start)
		if err != nil {
			return nil, fmt.Errorf("store: segment start %q: %w", start, err)
		}
		row.Start, row.Duration, row.Complete = t, time.Duration(durationMS)*time.Millisecond, complete != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

// Deletion is one row of deletion_log.
type Deletion struct {
	DeletedAt time.Time
	Camera    string
	Stream    string
	Start     time.Time
	Bytes     int64
	Reason    string
}

// LogDeletion appends to deletion_log.
func (r *Segments) LogDeletion(ctx context.Context, d Deletion) error {
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO deletion_log (deleted_at, camera, stream, start_utc, bytes, reason) VALUES (?,?,?,?,?,?)`,
		ts(d.DeletedAt), d.Camera, d.Stream, ts(d.Start), d.Bytes, d.Reason)
	return err
}

// Deletions returns the most recent deletions, newest first.
func (r *Segments) Deletions(ctx context.Context, limit int) ([]Deletion, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT deleted_at, camera, stream, start_utc, bytes, reason FROM deletion_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Deletion
	for rows.Next() {
		var d Deletion
		var at, start string
		if err := rows.Scan(&at, &d.Camera, &d.Stream, &start, &d.Bytes, &d.Reason); err != nil {
			return nil, err
		}
		d.DeletedAt, _ = time.Parse("2006-01-02T15:04:05.000000Z", at)
		d.Start, _ = time.Parse("2006-01-02T15:04:05.000000Z", start)
		out = append(out, d)
	}
	return out, rows.Err()
}
