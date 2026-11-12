// Package retention is the only code in the hive that deletes footage
// (ADR-0009): by its rules, and when the owner asks (ADR-0016, owner.go). Old exports go first (#59): they are copies, so footage is
// deleted for space only when that was not enough. Then two rules, in order:
// segments older than the camera's day limit go; then, while free space on
// the archive disk is below the threshold, the oldest complete segment
// across all cameras goes. The file
// currently being written is never touched, every deletion is logged, and
// when the target cannot be met the hive raises storage.low rather than
// stopping to record.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/store"
)

// ErrInvalidPolicy wraps policy validation failures.
var ErrInvalidPolicy = errors.New("retention: invalid policy")

// Policy says what to keep. Zero values disable a rule.
type Policy struct {
	DefaultDays   int            // keep footage this many days; 0 = unlimited
	DaysPerCamera map[string]int // per-camera override
	MinFreeBytes  int64          // keep at least this much free on the archive disk
	// MaxDays caps every retention setting from the license
	// (archive.retention_days_max); nil or -1 means no cap.
	MaxDays func() int
}

// Validate rejects negative limits.
func (p Policy) Validate() error {
	if p.DefaultDays < 0 || p.MinFreeBytes < 0 {
		return fmt.Errorf("%w: limits must not be negative", ErrInvalidPolicy)
	}
	for cam, d := range p.DaysPerCamera {
		if d < 0 {
			return fmt.Errorf("%w: camera %s days must not be negative", ErrInvalidPolicy, cam)
		}
	}
	return nil
}

func (p Policy) days(camera string) int {
	if d, ok := p.DaysPerCamera[camera]; ok {
		return p.cap(d)
	}
	return p.cap(p.DefaultDays)
}

// cap applies the licensed maximum: a cap always wins over "unlimited".
func (p Policy) cap(days int) int {
	if p.MaxDays == nil {
		return days
	}
	m := p.MaxDays()
	if m <= 0 {
		return days
	}
	if days == 0 || days > m {
		return m
	}
	return days
}

// FS is the filesystem the sweeper acts on (real disk or a test double).
type FS interface {
	Remove(path string) error
	Free(dir string) (int64, error)          // bytes available to us on dir's volume
	Modified(path string) (time.Time, error) // last write to a file
}

// Options tune the sweeper.
type Options struct {
	FS           FS               // default: the real filesystem
	Now          func() time.Time // default: time.Now
	OnStorageLow func(free int64) // called when the space target cannot be met
	ArchiveDir   string           // the volume to measure
	Logger       *slog.Logger
	Exports      Exports // old exports to remove first; nil: none
}

// Exports is what the sweeper needs from the export manager.
type Exports interface {
	Prune(ctx context.Context, now time.Time) (export.Pruned, error)
	Remove(ctx context.Context, match export.Footage, dryRun bool) (export.Pruned, error)
}

// Report says what a sweep deleted.
type Report struct {
	ByAge       int
	BySpace     int
	ByOwner     int   // waiting owner deletions finished (ADR-0016)
	Bytes       int64 // footage
	Exports     int
	ExportBytes int64
}

// Sweeper applies a Policy.
type Sweeper struct {
	mu     sync.Mutex // one writer: sweeps and owner deletions never overlap
	idx    *archive.Index
	repo   *store.Segments
	policy Policy
	opts   Options
	log    *slog.Logger
}

// New creates a Sweeper.
func New(idx *archive.Index, repo *store.Segments, policy Policy, opts Options) *Sweeper {
	if opts.FS == nil {
		opts.FS = osFS{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.OnStorageLow == nil {
		opts.OnStorageLow = func(int64) {}
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Sweeper{idx: idx, repo: repo, policy: policy, opts: opts, log: opts.Logger}
}

// Sweep applies both rules once.
func (s *Sweeper) Sweep(ctx context.Context) (Report, error) {
	if err := s.policy.Validate(); err != nil {
		return Report{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var rep Report
	if err := s.finishPending(ctx, &rep); err != nil {
		return rep, err
	}
	segs, err := s.idx.All(ctx)
	if err != nil {
		return rep, err
	}
	now := s.opts.Now()
	if err := s.pruneExports(ctx, now, &rep); err != nil {
		return rep, err
	}
	remaining := segs[:0:0]
	for _, seg := range segs {
		if s.expired(seg, now) {
			if err := s.remove(ctx, seg, "age", &rep); err != nil {
				return rep, err
			}
			rep.ByAge++
			continue
		}
		remaining = append(remaining, seg)
	}
	if s.policy.MinFreeBytes > 0 {
		if err := s.freeSpace(ctx, remaining, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// pruneExports removes old exports first: they are copies of footage.
func (s *Sweeper) pruneExports(ctx context.Context, now time.Time, rep *Report) error {
	if s.opts.Exports == nil {
		return nil
	}
	p, err := s.opts.Exports.Prune(ctx, now)
	if err != nil {
		return fmt.Errorf("retention: exports: %w", err)
	}
	rep.Exports, rep.ExportBytes = p.Jobs, p.Bytes
	return nil
}

func (s *Sweeper) expired(seg archive.Segment, now time.Time) bool {
	days := s.policy.days(seg.Camera)
	if days == 0 || !seg.Complete {
		return false
	}
	end := seg.Start.Add(seg.Duration)
	return end.Before(now.Add(-time.Duration(days) * 24 * time.Hour))
}

// freeSpace deletes oldest complete segments across cameras until the disk
// has MinFreeBytes available, or nothing deletable is left.
func (s *Sweeper) freeSpace(ctx context.Context, segs []archive.Segment, rep *Report) error {
	candidates := make([]archive.Segment, 0, len(segs))
	for _, seg := range segs {
		if seg.Complete {
			candidates = append(candidates, seg)
		}
	}
	sort.Slice(candidates, func(a, b int) bool { return candidates[a].Start.Before(candidates[b].Start) })
	for _, seg := range candidates {
		free, err := s.opts.FS.Free(s.opts.ArchiveDir)
		if err != nil {
			return fmt.Errorf("retention: free space: %w", err)
		}
		if free >= s.policy.MinFreeBytes {
			return nil
		}
		if err := s.remove(ctx, seg, "space", rep); err != nil {
			return err
		}
		rep.BySpace++
	}
	free, err := s.opts.FS.Free(s.opts.ArchiveDir)
	if err != nil {
		return fmt.Errorf("retention: free space: %w", err)
	}
	if free < s.policy.MinFreeBytes {
		s.log.Warn("storage low: nothing left to delete", "free", free, "want", s.policy.MinFreeBytes)
		s.opts.OnStorageLow(free)
	}
	return nil
}

// remove deletes the file (a missing file is fine), the index row and logs it.
func (s *Sweeper) remove(ctx context.Context, seg archive.Segment, reason string, rep *Report) error {
	if err := s.opts.FS.Remove(seg.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retention: remove %s: %w", seg.Path, err)
	}
	if err := s.idx.Delete(ctx, seg.Camera, seg.Stream, seg.Start); err != nil {
		return err
	}
	rep.Bytes += seg.Bytes
	s.log.Info("segment deleted", "camera", seg.Camera, "stream", seg.Stream, "start", seg.Start, "bytes", seg.Bytes, "reason", reason)
	return s.repo.LogDeletion(ctx, store.Deletion{DeletedAt: s.opts.Now().UTC(), Camera: seg.Camera, Stream: seg.Stream, Start: seg.Start, Bytes: seg.Bytes, Reason: reason})
}

// Run sweeps now and then every interval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if rep, err := s.Sweep(ctx); err != nil {
			s.log.Warn("retention sweep", "err", err)
		} else if rep.ByAge+rep.BySpace+rep.ByOwner+rep.Exports > 0 {
			s.log.Info("retention sweep", "by_age", rep.ByAge, "by_space", rep.BySpace, "by_owner", rep.ByOwner, "bytes", rep.Bytes, "exports", rep.Exports, "export_bytes", rep.ExportBytes)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// FreeSpace returns the free bytes on the volume holding dir.
func FreeSpace(dir string) (int64, error) { return osFS{}.Free(dir) }

// osFS is the real filesystem.
type osFS struct{}

// Remove deletes a file.
func (osFS) Remove(path string) error { return os.Remove(path) }

// Modified returns a file's last write time.
func (osFS) Modified(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}
