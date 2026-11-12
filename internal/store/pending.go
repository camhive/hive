package store

import (
	"context"
	"time"
)

// PendingDeletion is an owner's deletion (ADR-0016) still waiting for a
// segment the engine is writing: footage of Camera from From (zero: the
// beginning) to To.
type PendingDeletion struct {
	ID        int64
	Camera    string
	From, To  time.Time
	CreatedAt time.Time
}

// AddPending records a deletion to finish later.
func (r *Segments) AddPending(ctx context.Context, p PendingDeletion) error {
	from := ""
	if !p.From.IsZero() {
		from = ts(p.From)
	}
	_, err := r.s.db.ExecContext(ctx, `INSERT INTO pending_deletion (camera, from_utc, to_utc, created_at) VALUES (?,?,?,?)`,
		p.Camera, from, ts(p.To), ts(p.CreatedAt))
	return err
}

// Pending lists the waiting deletions, oldest first.
func (r *Segments) Pending(ctx context.Context) ([]PendingDeletion, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id, camera, from_utc, to_utc, created_at FROM pending_deletion ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PendingDeletion
	for rows.Next() {
		var p PendingDeletion
		var from, to, created string
		if err := rows.Scan(&p.ID, &p.Camera, &from, &to, &created); err != nil {
			return nil, err
		}
		if from != "" {
			p.From, _ = parseTS(from)
		}
		p.To, _ = parseTS(to)
		p.CreatedAt, _ = parseTS(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DonePending removes a finished deletion.
func (r *Segments) DonePending(ctx context.Context, id int64) error {
	_, err := r.s.db.ExecContext(ctx, `DELETE FROM pending_deletion WHERE id = ?`, id)
	return err
}
