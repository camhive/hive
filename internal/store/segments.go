package store

import (
	"context"
	"fmt"
	"math"
	"strings"
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

// CameraBytes is one camera's footage in the index, both streams
// together (#150): what the storage overview and forecast read.
type CameraBytes struct {
	Camera   string
	Bytes    int64
	Oldest   time.Time // start of the oldest segment
	Newest   time.Time // end of the newest segment, to the millisecond
	Since24h int64     // bytes of segments that started at or after the first time given
	Since7d  int64     // and at or after the second
	First24h time.Time // start of the first segment at or after the first time; zero when none
	First7d  time.Time // and at or after the second
}

// ByCamera sums the index per camera, sorted by camera id.
func (r *Segments) ByCamera(ctx context.Context, since24h, since7d time.Time) ([]CameraBytes, error) {
	rows, err := r.s.db.QueryContext(ctx, `
		SELECT camera, SUM(bytes), MIN(start_utc), MAX(julianday(start_utc) + duration_ms/86400000.0),
		  SUM(CASE WHEN start_utc >= ?1 THEN bytes ELSE 0 END), SUM(CASE WHEN start_utc >= ?2 THEN bytes ELSE 0 END),
		  COALESCE(MIN(CASE WHEN start_utc >= ?1 THEN start_utc END), ''), COALESCE(MIN(CASE WHEN start_utc >= ?2 THEN start_utc END), '')
		FROM segment GROUP BY camera ORDER BY camera`, ts(since24h), ts(since7d))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CameraBytes
	for rows.Next() {
		var (
			c                     CameraBytes
			oldest, first, first7 string
			newest                float64 // Julian day
		)
		if err := rows.Scan(&c.Camera, &c.Bytes, &oldest, &newest, &c.Since24h, &c.Since7d, &first, &first7); err != nil {
			return nil, err
		}
		if c.Oldest, err = parseStart(oldest); err != nil {
			return nil, err
		}
		if c.First24h, err = parseStart(first); err != nil {
			return nil, err
		}
		if c.First7d, err = parseStart(first7); err != nil {
			return nil, err
		}
		c.Newest = time.UnixMilli(int64(math.Round((newest - julianUnixEpoch) * 86_400_000))).UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// parseStart reads a stored segment start; "" is the zero time.
func parseStart(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02T15:04:05.000000Z", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: segment start %q: %w", s, err)
	}
	return t, nil
}

// julianUnixEpoch is 1970-01-01T00:00:00Z as a Julian day.
const julianUnixEpoch = 2440587.5

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

// Rebase points every row under the directory from at the same place under
// to (#155: the archive moved to a new disk) and says how many it changed.
func (r *Segments) Rebase(ctx context.Context, from, to string) (int64, error) {
	from = strings.TrimRight(from, `/\`)
	to = strings.TrimRight(to, `/\`)
	res, err := r.s.db.ExecContext(ctx, `UPDATE segment SET path = ?2 || substr(path, length(?1) + 1)
		WHERE substr(path, 1, length(?1)) = ?1 AND substr(path, length(?1) + 1, 1) IN ('/', '\')`, from, to)
	if err != nil {
		return 0, fmt.Errorf("store: rebase segments: %w", err)
	}
	return res.RowsAffected()
}

// SegmentSize is what camera care (#163) reads of a segment.
type SegmentSize struct {
	Start    time.Time
	Duration time.Duration
	Bytes    int64
}

// Sizes returns start, length and size of a stream's segments that start
// in [from, to), oldest first: three columns over the primary key, so a
// week of segments stays cheap to read every few minutes.
func (r *Segments) Sizes(ctx context.Context, camera, stream string, from, to time.Time) ([]SegmentSize, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT start_utc, duration_ms, bytes FROM segment
		WHERE camera = ? AND stream = ? AND start_utc >= ? AND start_utc < ? ORDER BY start_utc`, camera, stream, ts(from), ts(to))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SegmentSize
	for rows.Next() {
		var start string
		var ms int64
		var s SegmentSize
		if err := rows.Scan(&start, &ms, &s.Bytes); err != nil {
			return nil, err
		}
		if s.Start, err = parseStart(start); err != nil {
			return nil, err
		}
		s.Duration = time.Duration(ms) * time.Millisecond
		out = append(out, s)
	}
	return out, rows.Err()
}
