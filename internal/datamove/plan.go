package datamove

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	"github.com/camhive/hive/internal/archive"
)

// Fill is how full the move lets the new disk get with footage: the rest
// is the hive's room to record and the space rule's to work in.
const Fill = 0.8

// segment is one archive file to move.
type segment struct {
	rel   string // below the archive directory
	start time.Time
	size  int64
}

// listing is what the archive directory holds.
type listing struct {
	root     string    // where the archive really is (archive_dir with its links resolved)
	segments []segment // the footage
	unusual  []string  // entries that are neither directories nor regular files (links, devices): not copied, reported
}

// listSegments lists the archive at its real place: archive_dir may be a
// symbolic link to another disk. Inside it, links are not followed (the
// engine writes none); they are reported. Other files (a seal cut off by
// a crash, the health probe) are not footage. A missing archive is empty.
func listSegments(dir string) (listing, error) {
	root, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return listing{root: dir}, nil
	}
	if err != nil {
		return listing{}, err
	}
	l := listing{root: root}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			l.unusual = append(l.unusual, p)
			return nil
		}
		seg, ok := archive.ParsePath(root, p)
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		l.segments = append(l.segments, segment{rel: rel, start: seg.Start, size: info.Size()})
		return nil
	})
	return l, err
}

// fit splits segs into what is copied, newest first, while the disk's used
// bytes stay within limit, and what is left behind: once one does not fit,
// every older one stays too, so the new disk holds the most recent days
// without gaps. A segment the new disk already has (a resumed move) costs
// nothing more and is always kept.
func fit(segs []segment, have map[string]int64, used, limit int64) (take, skip []segment) {
	sorted := append([]segment(nil), segs...)
	sort.Slice(sorted, func(a, b int) bool {
		if !sorted[a].start.Equal(sorted[b].start) {
			return sorted[a].start.After(sorted[b].start)
		}
		return sorted[a].rel < sorted[b].rel
	})
	full := false
	for _, s := range sorted {
		if size, ok := have[s.rel]; ok && size == s.size {
			take = append(take, s)
			continue
		}
		if full || used+s.size > limit {
			full = true
			skip = append(skip, s)
			continue
		}
		used += s.size
		take = append(take, s)
	}
	return take, skip
}

// limit is how many bytes of the disk may be used once the footage is
// copied: Fill of it, and never into the hive's free-space floor, or the
// new hive's sweeper would delete what was just copied.
func limit(total, minFree int64) int64 {
	return min(int64(Fill*float64(total)), total-minFree)
}
