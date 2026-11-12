package retention

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/store"
)

// ErrInvalidScope wraps a deletion scope that selects nothing sensible.
var ErrInvalidScope = errors.New("retention: invalid deletion scope")

// reasonOwner marks deletions the owner asked for in deletion_log.
const reasonOwner = "owner"

// writingWindow: a newest file modified this recently is still being written.
const writingWindow = 2 * time.Minute

// Scope is what the owner asked to delete (ADR-0016): every stream of the
// cameras, between From and To.
type Scope struct {
	Cameras []string  // nil: every camera with footage
	From    time.Time // zero: from the beginning
	To      time.Time
	Exports bool // also the exports made of that footage
}

func (sc Scope) validate() error {
	if sc.To.IsZero() || (!sc.From.IsZero() && !sc.From.Before(sc.To)) {
		return fmt.Errorf("%w: need an end after the start", ErrInvalidScope)
	}
	return nil
}

// overlaps reports whether footage of camera from a to b falls in the scope.
func (sc Scope) overlaps(camera string, a, b time.Time) bool {
	if sc.Cameras != nil && !slices.Contains(sc.Cameras, camera) {
		return false
	}
	return a.Before(sc.To) && (sc.From.IsZero() || b.After(sc.From))
}

// CameraPlan is what a deletion takes from one camera: whole segments, so
// From–To can be wider than asked. Segments and Bytes include the Pending
// ones, which are still being written and go once closed.
type CameraPlan struct {
	Camera   string
	From, To time.Time
	Segments int
	Bytes    int64
	Pending  int
}

// Plan is what a deletion takes, per camera in camera order, and the exports.
type Plan struct {
	Cameras []CameraPlan
	Exports export.Pruned
}

// Delete removes the footage in scope, or with dryRun only reports what it
// would remove. Exports go first when asked (export.ErrRunning stops it
// before anything is deleted); segments still being written are recorded
// and removed by later sweeps once closed.
func (s *Sweeper) Delete(ctx context.Context, sc Scope, dryRun bool) (Plan, error) {
	if err := sc.validate(); err != nil {
		return Plan{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	segs, err := s.idx.All(ctx)
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	if sc.Exports && s.opts.Exports != nil {
		if plan.Exports, err = s.opts.Exports.Remove(ctx, sc.overlaps, dryRun); err != nil {
			return Plan{}, err
		}
	}
	hit := s.inScope(segs, sc)
	plan.Cameras = summarize(hit)
	if dryRun {
		return plan, nil
	}
	return plan, s.deleteNow(ctx, sc, hit)
}

// scoped is a segment in a deletion's scope and whether it is still written.
type scoped struct {
	seg     archive.Segment
	writing bool
}

func (s *Sweeper) inScope(segs []archive.Segment, sc Scope) []scoped {
	var out []scoped
	for _, seg := range segs {
		if sc.overlaps(seg.Camera, seg.Start, seg.Start.Add(seg.Duration)) {
			out = append(out, scoped{seg: seg, writing: s.writing(seg)})
		}
	}
	return out
}

// writing: the engine's newest file of a stream, modified recently.
func (s *Sweeper) writing(seg archive.Segment) bool {
	if seg.Complete {
		return false
	}
	mod, err := s.opts.FS.Modified(seg.Path)
	return err == nil && s.opts.Now().Sub(mod) < writingWindow
}

func summarize(hit []scoped) []CameraPlan {
	byCam := map[string]*CameraPlan{}
	for _, h := range hit {
		p := byCam[h.seg.Camera]
		end := h.seg.Start.Add(h.seg.Duration)
		if p == nil {
			p = &CameraPlan{Camera: h.seg.Camera, From: h.seg.Start, To: end}
			byCam[h.seg.Camera] = p
		}
		p.From, p.To = minTime(p.From, h.seg.Start), maxTime(p.To, end)
		p.Segments++
		p.Bytes += h.seg.Bytes
		if h.writing {
			p.Pending++
		}
	}
	out := make([]CameraPlan, 0, len(byCam))
	for _, p := range byCam {
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b CameraPlan) int { return cmp.Compare(a.Camera, b.Camera) })
	return out
}

// deleteNow removes what is not being written and records the rest, one
// pending deletion per camera that still has some.
func (s *Sweeper) deleteNow(ctx context.Context, sc Scope, hit []scoped) error {
	waiting := map[string]bool{}
	var rep Report
	for _, h := range hit {
		if h.writing {
			waiting[h.seg.Camera] = true
			continue
		}
		if err := s.remove(ctx, h.seg, reasonOwner, &rep); err != nil {
			return err
		}
	}
	cams := make([]string, 0, len(waiting))
	for cam := range waiting {
		cams = append(cams, cam)
	}
	slices.Sort(cams)
	for _, cam := range cams {
		if err := s.repo.AddPending(ctx, store.PendingDeletion{Camera: cam, From: sc.From, To: sc.To, CreatedAt: s.opts.Now().UTC()}); err != nil {
			return err
		}
	}
	return nil
}

// finishPending carries out waiting owner deletions whose files are closed
// now, and drops those with nothing left to wait for.
func (s *Sweeper) finishPending(ctx context.Context, rep *Report) error {
	pending, err := s.repo.Pending(ctx)
	if err != nil || len(pending) == 0 {
		return err
	}
	segs, err := s.idx.All(ctx)
	if err != nil {
		return err
	}
	for _, p := range pending {
		left, err := s.finishOne(ctx, segs, p, rep)
		if err != nil {
			return err
		}
		if !left {
			if err := s.repo.DonePending(ctx, p.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// finishOne removes what one waiting deletion can take now and reports
// whether a file it covers is still being written.
func (s *Sweeper) finishOne(ctx context.Context, segs []archive.Segment, p store.PendingDeletion, rep *Report) (left bool, err error) {
	for _, h := range s.inScope(segs, Scope{Cameras: []string{p.Camera}, From: p.From, To: p.To}) {
		if h.writing {
			left = true
			continue
		}
		if err := s.remove(ctx, h.seg, reasonOwner, rep); err != nil {
			return left, err
		}
		rep.ByOwner++
	}
	return left, nil
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
