package archive

import (
	"io"
	"os"
)

// SegmentFile is an opened segment: its plain fMP4 bytes with random
// access, so a span is read by seeking to the parts it needs instead of
// reading the file from the start.
type SegmentFile interface {
	io.ReaderAt
	io.Closer
	Size() int64 // plain bytes readable through ReadAt
}

// SegmentOpener opens a segment for reading. Everything that reads footage
// (playback, sound, export) goes through one, so where the bytes come from
// is decided in one place: OpenPlain reads the file as it lies on disk, and
// an encrypted archive (ADR-0019) gives an opener that wraps the file in
// age.DecryptReaderAt behind the same signature.
type SegmentOpener func(seg Segment) (SegmentFile, error)

// OpenPlain opens an unencrypted segment file.
func OpenPlain(seg Segment) (SegmentFile, error) { return OpenFile(seg.Path) }

// OpenFile opens an unencrypted segment or export file.
func OpenFile(path string) (SegmentFile, error) {
	f, err := os.Open(path) //nolint:gosec // the path comes from listing our own archive or export directory
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return plainFile{File: f, size: st.Size()}, nil
}

// plainFile is a segment file read as it is; its size is taken when it is
// opened, so a segment still being written is read up to that point.
type plainFile struct {
	*os.File
	size int64
}

// Size implements SegmentFile.
func (f plainFile) Size() int64 { return f.size }

// readAt fills p from off. A ReaderAt may answer a read that reaches the
// end of the file with io.EOF beside the full count (age's does, a plain
// file does not): only a short read is an error, and io.ReaderAt always
// gives one with it.
func readAt(f SegmentFile, p []byte, off int64) error {
	if n, err := f.ReadAt(p, off); n < len(p) {
		return err
	}
	return nil
}
