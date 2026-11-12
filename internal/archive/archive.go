// Package archive is the hive's view of recorded video (ADR-0009): fMP4
// segments written by the video engine under ARCHIVE/<camera>/<stream>/,
// indexed in SQLite for calendar and timeline queries, and reconciled
// against the filesystem because files are the truth.
package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/camhive/hive/internal/store"
)

// Segment is one recorded file.
type Segment struct {
	Camera   string
	Stream   string
	Start    time.Time
	Duration time.Duration // 0 when unknown (file still open or unparsable)
	Bytes    int64
	Path     string
	Complete bool      // not the newest file of its stream, i.e. no longer written
	Analysis int       // AnalysisVersion of the reading that filled it; 0 when unread
	Activity *Activity // profile for the timeline; nil when unread or without video
}

// MediaMTX writes <YYYY-MM-DD_HH-MM-SS-ffffff_±HHMM>.mp4 from our recordPath
// template, in the hive's local time with the UTC offset appended. Names
// without an offset (older recordings) are read as UTC. Go layouts cannot
// express "-ffffff", so the name is parsed by hand.
const fileNameLayout = "2006-01-02_15-04-05"

var fileNameRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}-\d{6}(_(Z|[+-]\d{4}))?\.mp4$`)

// ParsePath interprets ARCHIVE/<camera>/<stream>/<timestamp>.mp4. Paths
// outside root or with another shape are rejected.
func ParsePath(root, path string) (Segment, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return Segment{}, false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || !fileNameRE.MatchString(parts[2]) {
		return Segment{}, false
	}
	start, ok := parseFileTime(strings.TrimSuffix(parts[2], ".mp4"))
	if !ok {
		return Segment{}, false
	}
	return Segment{Camera: parts[0], Stream: parts[1], Start: start, Path: path}, true
}

// parseFileTime parses "2006-01-02_15-04-05-ffffff[_±HHMM]".
func parseFileTime(name string) (time.Time, bool) {
	// Split "<date>_<time>-<micros>[_<offset>]": the first '_' separates
	// date and time, a second one introduces the offset.
	i := strings.LastIndex(name, "_")
	loc := time.UTC
	if i > len(fileNameLayout) {
		off, ok := parseOffset(name[i+1:])
		if !ok {
			return time.Time{}, false
		}
		loc, name = time.FixedZone("", off), name[:i]
	}
	if len(name) != len(fileNameLayout)+7 {
		return time.Time{}, false
	}
	base, err := time.ParseInLocation(fileNameLayout, name[:len(fileNameLayout)], loc)
	if err != nil {
		return time.Time{}, false
	}
	micros := 0
	for _, c := range name[len(fileNameLayout)+1:] {
		if c < '0' || c > '9' {
			return time.Time{}, false
		}
		micros = micros*10 + int(c-'0')
	}
	return base.Add(time.Duration(micros) * time.Microsecond).UTC(), true
}

// parseOffset reads "Z", "+0100" or "-0530" as seconds east of UTC
// (MediaMTX writes "Z" when the zone offset is zero).
func parseOffset(s string) (int, bool) {
	if s == "Z" {
		return 0, true
	}
	if len(s) != 5 || (s[0] != '+' && s[0] != '-') {
		return 0, false
	}
	var h, m int
	if _, err := fmt.Sscanf(s[1:], "%02d%02d", &h, &m); err != nil || h > 23 || m > 59 {
		return 0, false
	}
	off := h*3600 + m*60
	if s[0] == '-' {
		off = -off
	}
	return off, true
}

func formatFileTime(t time.Time) string {
	t = t.UTC()
	return t.Format(fileNameLayout) + fmt.Sprintf("-%06d", t.Nanosecond()/1000)
}

// AnalysisVersion is the version of how fill reads a segment file. Raise it
// when that reading changes; settled rows from an older version are read
// again on the next reconcile. 1: duration from the video track only.
// 2: and the activity profile (ADR-0015).
const AnalysisVersion = 2

// Index is the archive index over the store.
type Index struct {
	repo *store.Segments
	log  *slog.Logger
}

// NewIndex creates an Index backed by s.
func NewIndex(s *store.Store, opts ...Option) *Index {
	idx := &Index{repo: s.Segments(), log: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(idx)
	}
	return idx
}

// Option customises an Index.
type Option func(*Index)

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(i *Index) { i.log = l } }

func toRow(s Segment) store.SegmentRow {
	row := store.SegmentRow{Camera: s.Camera, Stream: s.Stream, Start: s.Start, Duration: s.Duration, Bytes: s.Bytes, Path: s.Path, Complete: s.Complete, Analysis: s.Analysis}
	if s.Activity != nil {
		row.Activity, _ = json.Marshal(s.Activity) // plain ints and a slice: cannot fail
	}
	return row
}

func fromRow(r store.SegmentRow) Segment {
	s := Segment{Camera: r.Camera, Stream: r.Stream, Start: r.Start, Duration: r.Duration, Bytes: r.Bytes, Path: r.Path, Complete: r.Complete, Analysis: r.Analysis}
	if len(r.Activity) > 0 {
		var a Activity
		if json.Unmarshal(r.Activity, &a) == nil {
			s.Activity = &a
		}
	}
	return s
}

// Upsert records a segment.
func (i *Index) Upsert(ctx context.Context, s Segment) error { return i.repo.Upsert(ctx, toRow(s)) }

// Delete forgets a segment (the file is the caller's business).
func (i *Index) Delete(ctx context.Context, camera, stream string, start time.Time) error {
	return i.repo.Delete(ctx, camera, stream, start)
}

// All returns every indexed segment, oldest first per stream.
func (i *Index) All(ctx context.Context) ([]Segment, error) {
	rows, err := i.repo.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Segment, len(rows))
	for k, r := range rows {
		out[k] = fromRow(r)
	}
	return out, nil
}

// Range returns the segments of a stream overlapping [from, to), oldest first.
func (i *Index) Range(ctx context.Context, camera, stream string, from, to time.Time) ([]Segment, error) {
	rows, err := i.repo.Range(ctx, camera, stream, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]Segment, len(rows))
	for k, r := range rows {
		out[k] = fromRow(r)
	}
	return out, nil
}

// Calendar returns the days (midnight in loc) on which a stream has footage.
func (i *Index) Calendar(ctx context.Context, camera, stream string, loc *time.Location) ([]time.Time, error) {
	rows, err := i.repo.Range(ctx, camera, stream, time.Time{}, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return nil, err
	}
	seen := map[time.Time]bool{}
	var days []time.Time
	for _, r := range rows {
		for _, t := range []time.Time{r.Start, r.Start.Add(r.Duration)} {
			l := t.In(loc)
			day := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
			if !seen[day] {
				seen[day] = true
				days = append(days, day)
			}
		}
	}
	sort.Slice(days, func(a, b int) bool { return days[a].Before(days[b]) })
	return days, nil
}

// Usage returns the archive total.
func (i *Index) Usage(ctx context.Context) (store.Usage, error) { return i.repo.Usage(ctx) }

// Run reconciles root now and then every interval until ctx is cancelled.
func (i *Index) Run(ctx context.Context, root string, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if rep, err := i.Reconcile(ctx, root); err != nil {
			i.log.Warn("archive reconcile", "err", err)
		} else if rep.Added+rep.Updated+rep.Removed > 0 {
			i.log.Info("archive reconciled", "added", rep.Added, "updated", rep.Updated, "removed", rep.Removed)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Report says what a reconcile changed.
type Report struct {
	Added   int
	Updated int
	Removed int
}

// Reconcile walks root and makes the index match the files: new files are
// indexed (duration parsed from the fMP4), rows without files are removed,
// the newest file of each stream is marked incomplete. It is idempotent.
func (i *Index) Reconcile(ctx context.Context, root string) (Report, error) {
	found, err := scan(root)
	if err != nil {
		return Report{}, err
	}
	existing, err := i.repo.All(ctx)
	if err != nil {
		return Report{}, err
	}
	byKey := make(map[string]store.SegmentRow, len(existing))
	for _, r := range existing {
		byKey[key(r.Camera, r.Stream, r.Start)] = r
	}
	rep, seen, err := i.indexFound(ctx, found, byKey)
	if err != nil {
		return rep, err
	}
	for k, r := range byKey {
		if seen[k] {
			continue
		}
		if err := i.repo.Delete(ctx, r.Camera, r.Stream, r.Start); err != nil {
			return rep, err
		}
		rep.Removed++
	}
	return rep, nil
}

// indexFound upserts files that are new or still changing; settled rows
// (complete, same size and path) are left alone so their file is not re-read.
func (i *Index) indexFound(ctx context.Context, found []Segment, byKey map[string]store.SegmentRow) (Report, map[string]bool, error) {
	var rep Report
	seen := make(map[string]bool, len(found))
	for _, seg := range found {
		k := key(seg.Camera, seg.Stream, seg.Start)
		seen[k] = true
		old, had := byKey[k]
		if had && settled(old, seg) {
			continue
		}
		if err := i.fill(&seg); err != nil {
			i.log.Warn("segment unreadable", "path", seg.Path, "err", err)
		}
		if err := i.repo.Upsert(ctx, toRow(seg)); err != nil {
			return rep, seen, err
		}
		if had {
			rep.Updated++
		} else {
			rep.Added++
		}
	}
	return rep, seen, nil
}

// settled says a row already describes the file as the current reading
// would: complete, same size and path, filled by this AnalysisVersion.
func settled(old store.SegmentRow, seg Segment) bool {
	return old.Complete && old.Bytes == seg.Bytes && old.Path == seg.Path && old.Analysis == AnalysisVersion
}

func key(camera, stream string, start time.Time) string {
	return camera + "/" + stream + "/" + formatFileTime(start)
}

// fill reads size and duration from the file.
func (i *Index) fill(seg *Segment) error {
	st, err := os.Stat(seg.Path)
	if err != nil {
		return err
	}
	seg.Bytes = st.Size()
	data, err := os.ReadFile(seg.Path) //nolint:gosec // path produced by scan under our archive root
	if err != nil {
		return err
	}
	d, err := FMP4Duration(data)
	if err != nil {
		return err
	}
	seg.Duration, seg.Analysis = d, AnalysisVersion
	if a, err := ActivityOf(data); err == nil {
		seg.Activity = &a
	}
	return nil
}

// scan lists segment files under root, newest per stream marked incomplete.
func scan(root string) ([]Segment, error) {
	var out []Segment
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		switch {
		case err != nil && errors.Is(err, os.ErrNotExist) && path == root:
			return filepath.SkipAll // no archive yet
		case err != nil:
			return err
		case d.IsDir():
			return nil
		}
		if seg, ok := ParsePath(root, path); ok {
			// The size decides whether a settled row needs reading again.
			if info, err := d.Info(); err == nil {
				seg.Bytes = info.Size()
			}
			out = append(out, seg)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("archive: scan %s: %w", root, err)
	}
	markIncomplete(out)
	return out, nil
}

// markIncomplete flags the newest segment of every stream as still open.
func markIncomplete(segs []Segment) {
	newest := map[string]int{}
	for k, seg := range segs {
		sk := seg.Camera + "/" + seg.Stream
		if j, ok := newest[sk]; !ok || seg.Start.After(segs[j].Start) {
			newest[sk] = k
		}
	}
	for k := range segs {
		segs[k].Complete = newest[segs[k].Camera+"/"+segs[k].Stream] != k
	}
}
