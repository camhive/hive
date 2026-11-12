package export

import (
	"context"
	"errors"
	"os"
	"sort"
	"time"
)

// Keep says how long and how much of finished exports the hive keeps (#59).
// Zero disables a rule.
type Keep struct {
	MaxAge   time.Duration // finished jobs go this long after they finished
	MaxBytes int64         // then the oldest finished files go while they take more
}

// WithKeep sets the limits Prune applies.
func WithKeep(k Keep) Option { return func(m *Manager) { m.keep = k } }

// expired picks the jobs to remove: finished ones older than MaxAge, then the
// oldest finished files while the rest take more than MaxBytes. Running jobs
// are never picked.
func expired(jobs []Job, now time.Time, keep Keep) []Job {
	out, kept := tooOld(jobs, now, keep.MaxAge)
	return append(out, overCap(kept, keep.MaxBytes)...)
}

// tooOld splits finished jobs into those older than maxAge and the rest.
func tooOld(jobs []Job, now time.Time, maxAge time.Duration) (old, kept []Job) {
	for _, j := range jobs {
		switch {
		case j.Status == StatusRunning:
		case maxAge > 0 && now.Sub(j.DoneAt) > maxAge:
			old = append(old, j)
		default:
			kept = append(kept, j)
		}
	}
	return old, kept
}

// overCap picks the oldest jobs while all of them take more than maxBytes.
func overCap(kept []Job, maxBytes int64) []Job {
	if maxBytes <= 0 {
		return nil
	}
	var out []Job
	var total int64
	for _, j := range kept {
		total += j.Bytes
	}
	sort.SliceStable(kept, func(a, b int) bool { return kept[a].DoneAt.Before(kept[b].DoneAt) })
	for _, j := range kept {
		if total <= maxBytes {
			break
		}
		if j.Bytes > 0 {
			out = append(out, j)
			total -= j.Bytes
		}
	}
	return out
}

// Pruned says what Prune removed.
type Pruned struct {
	Jobs  int
	Bytes int64
}

// Prune removes the jobs Keep no longer allows, file first, then the row; a
// file that is already gone is fine. The retention sweeper calls it.
func (m *Manager) Prune(ctx context.Context, now time.Time) (Pruned, error) {
	rows, err := m.jobs.All(ctx)
	if err != nil {
		return Pruned{}, err
	}
	all := make([]Job, 0, len(rows))
	for _, r := range rows {
		all = append(all, fromRow(r))
	}
	var p Pruned
	for _, j := range expired(all, now, m.keep) {
		if err := m.removeJob(ctx, j); err != nil {
			return p, err
		}
		p.Jobs++
		p.Bytes += j.Bytes
	}
	return p, nil
}

// ErrRunning means an export of the footage being deleted is still being
// made; deleting now would leave its copy behind (ADR-0016).
var ErrRunning = errors.New("export: an export of that footage is still running")

// Footage selects exports by camera and the span they cover.
type Footage func(camera string, from, to time.Time) bool

// Remove deletes the exports whose footage matches, file first, then the
// row, and reports what it removed; with dryRun it only reports. It refuses
// with ErrRunning while a matching export is still running.
func (m *Manager) Remove(ctx context.Context, match Footage, dryRun bool) (Pruned, error) {
	rows, err := m.jobs.All(ctx)
	if err != nil {
		return Pruned{}, err
	}
	var hit []Job
	for _, r := range rows {
		j := fromRow(r)
		if !match(j.Request.Camera, j.Request.Start, j.Request.Start.Add(j.Request.Duration)) {
			continue
		}
		if j.Status == StatusRunning {
			return Pruned{}, ErrRunning
		}
		hit = append(hit, j)
	}
	var p Pruned
	for _, j := range hit {
		if !dryRun {
			if err := m.removeJob(ctx, j); err != nil {
				return p, err
			}
		}
		p.Jobs++
		p.Bytes += j.Bytes
	}
	return p, nil
}

// removeJob deletes a job's file (already gone is fine) and its row.
func (m *Manager) removeJob(ctx context.Context, j Job) error {
	if err := os.Remove(m.path(j.ID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return m.jobs.Delete(ctx, j.ID)
}
