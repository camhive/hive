package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ExportJob is one export as stored (#59).
type ExportJob struct {
	ID        string
	Owner     string
	Camera    string
	Stream    string // main | sub
	Start     time.Time
	Duration  time.Duration
	Status    string // running | done | failed
	Error     string
	Bytes     int64
	FileName  string
	CreatedAt time.Time
	DoneAt    time.Time // zero while running
}

// Exports returns the export job repository.
func (s *Store) Exports() *Exports { return &Exports{s: s} }

// Exports persists ExportJob values.
type Exports struct {
	s *Store
}

const (
	exportColumns = `id, owner, camera, stream, start_utc, duration_ms, status, error, bytes, file_name, created_utc, done_utc`
	exportSelect  = `SELECT ` + exportColumns + ` FROM export_job `
)

// Put inserts or replaces a job by id.
func (r *Exports) Put(ctx context.Context, j ExportJob) error {
	done := ""
	if !j.DoneAt.IsZero() {
		done = ts(j.DoneAt)
	}
	_, err := r.s.db.ExecContext(ctx, `INSERT OR REPLACE INTO export_job (`+exportColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Owner, j.Camera, j.Stream, ts(j.Start), j.Duration.Milliseconds(), j.Status, j.Error, j.Bytes, j.FileName, ts(j.CreatedAt), done)
	if err != nil {
		return fmt.Errorf("store: put export %s: %w", j.ID, err)
	}
	return nil
}

// Get returns one job or ErrNotFound.
func (r *Exports) Get(ctx context.Context, id string) (ExportJob, error) {
	jobs, err := r.query(ctx, exportSelect+`WHERE id=?`, id)
	if err != nil {
		return ExportJob{}, err
	}
	if len(jobs) == 0 {
		return ExportJob{}, ErrNotFound
	}
	return jobs[0], nil
}

// List returns an owner's jobs, newest first.
func (r *Exports) List(ctx context.Context, owner string) ([]ExportJob, error) {
	return r.query(ctx, exportSelect+`WHERE owner=? ORDER BY created_utc DESC, id`, owner)
}

// All returns every job, oldest first.
func (r *Exports) All(ctx context.Context) ([]ExportJob, error) {
	return r.query(ctx, exportSelect+`ORDER BY created_utc, id`)
}

// Delete removes a job; an unknown id is not an error.
func (r *Exports) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, `DELETE FROM export_job WHERE id=?`, id)
	return err
}

// query runs one of the constant queries above.
func (r *Exports) query(ctx context.Context, q string, args ...any) ([]ExportJob, error) {
	rows, err := r.s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ExportJob
	for rows.Next() {
		j, err := scanExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func scanExport(rows *sql.Rows) (ExportJob, error) {
	var j ExportJob
	var start, created, done string
	var ms int64
	if err := rows.Scan(&j.ID, &j.Owner, &j.Camera, &j.Stream, &start, &ms, &j.Status, &j.Error, &j.Bytes, &j.FileName, &created, &done); err != nil {
		return ExportJob{}, err
	}
	var errs [3]error
	j.Start, errs[0] = parseTS(start)
	j.CreatedAt, errs[1] = parseTS(created)
	if done != "" {
		j.DoneAt, errs[2] = parseTS(done)
	}
	j.Duration = time.Duration(ms) * time.Millisecond
	return j, errors.Join(errs[:]...)
}

func parseTS(s string) (time.Time, error) { return time.Parse("2006-01-02T15:04:05.000000Z", s) }
