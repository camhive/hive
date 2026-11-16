package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
)

// The archive encrypted at rest (ADR-0019): a closed segment or export is
// sealed to the archive key's public half as <name>.mp4.age, and opened
// again only with the private half, which the owner unlocks.

// SealedExt ends the name of a file sealed to the archive key.
const SealedExt = ".age"

// sealingExt ends the temp file a seal is written to; ParsePath ignores it
// and the next sealing pass removes it if a crash left it behind.
const sealingExt = SealedExt + ".tmp"

// ErrLocked means a file is sealed and the archive key is locked: the owner
// has not unlocked it since the hive started, or locked it again.
var ErrLocked = errors.New("archive: the archive is locked")

// Sealed says whether the segment's file is sealed to the archive key.
func (s Segment) Sealed() bool { return IsSealed(s.Path) }

// IsSealed says whether a file is sealed to the archive key, by its name.
func IsSealed(path string) bool { return strings.HasSuffix(path, SealedExt) }

// SealFile seals a closed plain file to the archive key as path+SealedExt:
// written to a temp file, synced, renamed into place, then the plain file
// removed. A sealed file already there is kept (it was synced before its
// rename, so a crash after the rename left it whole) and only the plain
// one goes. It returns the sealed file's path.
func SealFile(path string, to age.Recipient) (string, error) {
	out := path + SealedExt
	if _, err := os.Stat(out); errors.Is(err, os.ErrNotExist) {
		if err := writeSealed(path, out, to); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return out, nil
}

// writeSealed encrypts src into a temp file beside out and renames it to out.
func writeSealed(src, out string, to age.Recipient) error {
	in, err := os.Open(src) //nolint:gosec // a segment or export path the hive listed itself
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".tmp")
	if err := writeSynced(tmp, func(w io.Writer) error { return SealTo(w, in, to) }); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(out))
	return nil
}

// writeSynced creates path (it must not exist), fills it with write and
// syncs it; on any failure the file is removed.
func writeSynced(path string, write func(io.Writer) error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // a temp name beside our own file
	if err != nil {
		return err
	}
	err = write(f)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// SealTo copies r to w sealed to the archive key.
func SealTo(w io.Writer, r io.Reader, to age.Recipient) error {
	enc, err := age.Encrypt(w, to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(enc, r); err != nil {
		return err
	}
	return enc.Close()
}

// syncDir makes a rename in dir durable where the platform can (Windows
// cannot open a directory for syncing; NTFS journals the rename).
func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // our own archive directory
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// OpenSealed opens a sealed file with the archive keys: random access, so
// a read deep into the file decrypts only the chunks it touches.
func OpenSealed(path string, keys ...age.Identity) (SegmentFile, error) {
	f, err := os.Open(path) //nolint:gosec // a path the hive listed itself
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	r, size, err := age.DecryptReaderAt(f, st.Size(), keys...)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("archive: %s: %w", path, err)
	}
	return sealedFile{ReaderAt: r, Closer: f, size: size}, nil
}

// sealedFile is a sealed file read through its decryption.
type sealedFile struct {
	io.ReaderAt
	io.Closer
	size int64
}

// Size implements SegmentFile: the plain size.
func (f sealedFile) Size() int64 { return f.size }

// preferSealed drops a plain segment whose sealed file is there too: the
// seal was renamed into place and the plain file not yet removed.
func preferSealed(segs []Segment) []Segment {
	sealed := map[string]bool{}
	for _, s := range segs {
		if s.Sealed() {
			sealed[strings.TrimSuffix(s.Path, SealedExt)] = true
		}
	}
	out := segs[:0]
	for _, s := range segs {
		if !sealed[s.Path] {
			out = append(out, s)
		}
	}
	return out
}

// WritingWindow is how recently the newest file of a stream was modified
// while the engine still writes it (parts are written every second).
const WritingWindow = 2 * time.Minute

// StillWriting says whether the engine may still write seg: the newest of
// its stream, modified within WritingWindow. Retention and sealing leave
// such a file alone.
func StillWriting(seg Segment, modified, now time.Time) bool {
	return !seg.Complete && now.Sub(modified) < WritingWindow
}

// Seal seals the closed plain segments under root to the archive key and
// points their rows at the sealed files (ADR-0019 §2). Each row is filled
// from the plain file first, so the timeline knows the segment while the
// archive is locked. What a crash left behind is finished: a temp seal is
// removed, a plain file beside its seal goes. A segment that cannot be
// sealed is reported in the error and the pass goes on with the others;
// the pass stops with ctx. The index lock is taken per segment, so a long
// first pass (encryption switched on over a large archive) does not hold
// reconciles back. Running it again changes nothing. It returns how many
// segments it sealed.
func (i *Index) Seal(ctx context.Context, root string, to age.Recipient, now time.Time) (int, error) {
	all, err := listFiles(root)
	if err != nil {
		return 0, err
	}
	removeTempSeals(all)
	markIncomplete(all)
	n := 0
	var failed []error
	for _, seg := range all {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if seg.Sealed() || !closed(seg, now) {
			continue
		}
		sealed, err := i.sealLocked(ctx, seg, to)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		if sealed {
			n++
		}
	}
	return n, errors.Join(failed...)
}

// sealLocked is sealOne under the index lock.
func (i *Index) sealLocked(ctx context.Context, seg Segment, to age.Recipient) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.sealOne(ctx, seg, to)
}

// closed: the engine no longer writes the plain file.
func closed(seg Segment, now time.Time) bool {
	st, err := os.Stat(seg.Path)
	return err == nil && !StillWriting(seg, st.ModTime(), now)
}

// sealOne seals one closed plain segment and stores its row; sealed is
// false when the plain file was only the leftover beside its seal.
func (i *Index) sealOne(ctx context.Context, seg Segment, to age.Recipient) (sealed bool, err error) {
	if _, err := os.Stat(seg.Path); errors.Is(err, os.ErrNotExist) {
		return false, nil // deleted (retention, the owner) since the listing
	}
	_, statErr := os.Stat(seg.Path + SealedExt)
	leftover := statErr == nil
	if !leftover {
		if err := i.fill(&seg); err != nil {
			i.log.Warn("segment unreadable; sealed as it is", "path", seg.Path, "err", err)
		}
	}
	out, err := SealFile(seg.Path, to)
	if err != nil {
		return false, fmt.Errorf("archive: seal %s: %w", seg.Path, err)
	}
	if leftover {
		return false, nil
	}
	st, err := os.Stat(out)
	if err != nil {
		return false, err
	}
	seg.Path, seg.Bytes, seg.Complete = out, st.Size(), true
	return true, i.repo.Upsert(ctx, toRow(seg))
}

// removeTempSeals removes the temp files of seals a crash cut off; only
// Seal writes them, under the index lock, so none is in progress.
func removeTempSeals(segs []Segment) {
	dirs := map[string]bool{}
	for _, s := range segs {
		dirs[filepath.Dir(s.Path)] = true
	}
	for d := range dirs {
		tmps, _ := filepath.Glob(filepath.Join(d, ".*"+sealingExt)) // the pattern is fixed and valid
		for _, t := range tmps {
			_ = os.Remove(t)
		}
	}
}
