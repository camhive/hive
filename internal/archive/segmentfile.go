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
func OpenPlain(seg Segment) (SegmentFile, error) {
	f, err := os.Open(seg.Path) // the path comes from listing our own archive root
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
