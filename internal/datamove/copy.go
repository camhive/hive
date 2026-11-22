package datamove

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// partExt marks a copy not yet verified; a cut-off move leaves it and the
// next run overwrites it.
const partExt = ".moving"

// copied is one file's copy.
type copied struct {
	bytes int64
	sum   []byte // SHA-256 of the source as it was read; nil when an earlier run's copy stood
}

// readError is a failure to read the source: on a dying disk, a bad
// sector. The caller decides whether the file can be done without.
type readError struct{ err error }

func (e readError) Error() string { return "read " + e.err.Error() }
func (e readError) Unwrap() error { return e.err }

// unreadable reports whether err is a failure to read the source.
func unreadable(err error) bool {
	var r readError
	return errors.As(err, &r)
}

// openSource opens a file to copy; tests replace it.
var openSource = func(path string) (io.ReadCloser, error) { return os.Open(path) } //nolint:gosec // the hive's own files

// srcReader tags its read errors as the source's.
type srcReader struct{ r io.Reader }

func (s srcReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = readError{err}
	}
	return n, err
}

// copyFile copies src to dst through dst.moving: synced, read back from
// the disk and renamed only when size and SHA-256 match the source as
// read, then given the source's modification time. A dst of the source's
// size and modification time is such a verified copy from an earlier run
// and is left alone, unless force.
func copyFile(src, dst string, force bool) (copied, error) {
	st, err := os.Stat(src)
	if err != nil {
		return copied{}, readError{err}
	}
	if !st.Mode().IsRegular() {
		return copied{}, fmt.Errorf("datamove: %s is not a regular file", src)
	}
	if !force && verified(dst, st) {
		return copied{bytes: st.Size()}, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return copied{}, err
	}
	part := dst + partExt
	sum, n, err := writeCopy(src, part, st.Mode().Perm())
	if err == nil {
		err = verify(part, sum, n)
	}
	if err == nil {
		err = os.Rename(part, dst)
	}
	if err != nil {
		_ = os.Remove(part)
		return copied{}, err
	}
	if err := os.Chtimes(dst, st.ModTime(), st.ModTime()); err != nil {
		return copied{}, err
	}
	syncDir(filepath.Dir(dst))
	return copied{bytes: n, sum: sum}, nil
}

// verified: dst has the size and modification time of src, which only a
// verified copy is given.
func verified(dst string, src fs.FileInfo) bool {
	d, err := os.Stat(dst)
	return err == nil && d.Mode().IsRegular() && d.Size() == src.Size() && d.ModTime().Equal(src.ModTime())
}

// writeCopy copies src into part, syncs it and drops it from the page
// cache; it returns the SHA-256 of what it read.
func writeCopy(src, part string, mode fs.FileMode) ([]byte, int64, error) {
	in, err := openSource(src)
	if err != nil {
		return nil, 0, readError{err}
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode) //nolint:gosec // the new data directory
	if err != nil {
		return nil, 0, err
	}
	h := sha256.New()
	n, err := io.Copy(out, io.TeeReader(srcReader{in}, h))
	if err == nil {
		err = out.Sync()
	}
	if err == nil {
		dropCache(out)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(part, mode) // the umask must not widen or narrow it
	}
	return h.Sum(nil), n, err
}

// verify reads part back: n bytes with the SHA-256 sum.
func verify(part string, sum []byte, n int64) error {
	back, m, err := hashFile(part)
	if err != nil {
		return err
	}
	if m != n || !bytes.Equal(back, sum) {
		return fmt.Errorf("datamove: %s: the copy reads back differently (%d of %d bytes)", part, m, n)
	}
	return nil
}

func hashFile(path string) ([]byte, int64, error) {
	f, err := os.Open(path) //nolint:gosec // the hive's own files
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return h.Sum(nil), n, err
}

// copyTree copies every file under src (its real place, when src is a
// symbolic link) to the same place under dst, keeping the directories'
// modes (archive-key/ is 0700). Anything inside that is not a directory
// or a regular file (a symbolic link, a device) stops it: these trees
// are small and must arrive whole. A missing src copies nothing.
func copyTree(src, dst string, each func(copied)) error {
	root, err := filepath.EvalSymlinks(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return readError{err}
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return readError{err}
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		return copyEntry(p, filepath.Join(dst, rel), d, each)
	})
}

// copyEntry makes a directory or copies a regular file of copyTree.
func copyEntry(src, dst string, d fs.DirEntry, each func(copied)) error {
	switch {
	case d.IsDir():
		info, err := d.Info()
		if err != nil {
			return readError{err}
		}
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chmod(dst, info.Mode().Perm())
	case filepath.Ext(src) == partExt:
		return nil // a copy of ours cut off: not the hive's
	case !d.Type().IsRegular():
		return fmt.Errorf("datamove: %s is a %s, not a file: copy what it points to by hand, or replace it with the file", src, kind(d.Type()))
	}
	c, err := copyFile(src, dst, false)
	if err == nil && each != nil {
		each(c)
	}
	return err
}

func kind(m fs.FileMode) string {
	if m&fs.ModeSymlink != 0 {
		return "symbolic link"
	}
	return "special file"
}

// syncDir makes a rename in dir durable; not every system can.
func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil { //nolint:gosec // the new data directory
		_ = f.Sync()
		_ = f.Close()
	}
}
