package archive

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrNoFootage means nothing was recorded in the span asked for.
var ErrNoFootage = errors.New("archive: no footage in the span")

// Reader serves spans of the archive as fragmented MP4 for playback, sound
// and export (ADR-0009, ADR-0019 §6): it opens the segments itself through
// a SegmentOpener, cuts them to the span and joins them across segment
// boundaries, without transcoding. Memory stays bounded by about one
// fragment and one group of pictures per track, however long the span.
type Reader struct {
	root string
	open SegmentOpener
}

// NewReader reads the archive under root (ARCHIVE/<camera>/<stream>/).
func NewReader(root string, open SegmentOpener) *Reader {
	return &Reader{root: root, open: open}
}

// Span returns the footage of a stream from start for length. The answer
// streams as the caller reads it; Close it to stop early. Errors found
// before the first byte (no footage, an unreadable first segment) are
// returned here; later ones end the stream with that error.
func (r *Reader) Span(ctx context.Context, camera, stream string, start time.Time, length time.Duration) (io.ReadCloser, error) {
	all, err := r.segments(camera, stream)
	if err != nil {
		return nil, err
	}
	segs := pickSegments(all, start, start.Add(length))
	if len(segs) == 0 {
		return nil, ErrNoFootage
	}
	return streamOut(ctx, func(w io.Writer) error { return writeSpan(ctx, w, r.open, segs, start, length) })
}

// segments lists a stream's segment files, oldest first. The files are the
// truth (ADR-0009): the one being written is there before the index knows it.
func (r *Reader) segments(camera, stream string) ([]Segment, error) {
	if !plainName(camera) || !plainName(stream) {
		return nil, ErrNoFootage
	}
	dir := filepath.Join(r.root, camera, stream)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoFootage
	}
	if err != nil {
		return nil, err
	}
	var out []Segment
	for _, e := range entries {
		if seg, ok := ParsePath(r.root, filepath.Join(dir, e.Name())); ok && !e.IsDir() {
			out = append(out, seg)
		}
	}
	out = preferSealed(out)
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}

// plainName is a single path element: no separators, not "." or "..".
func plainName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`)
}

// streamOut runs write in the background and returns what it writes. It
// returns once the first byte is ready, or with write's error when it ends
// without writing anything.
func streamOut(ctx context.Context, write func(io.Writer) error) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	first := &firstWrite{w: pw, ready: make(chan struct{})}
	done := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() { _ = pw.CloseWithError(ctx.Err()) }) // ends a blocked write too
	go func() {
		defer stop()
		err := write(first)
		_ = pw.CloseWithError(err)
		done <- err
	}()
	select {
	case <-first.ready:
		return pr, nil
	case err := <-done:
		_ = pr.Close()
		if err == nil {
			err = ErrNoFootage
		}
		return nil, err
	}
}

// firstWrite tells when the first byte is about to be written.
type firstWrite struct {
	w     io.Writer
	ready chan struct{}
	once  bool
}

func (f *firstWrite) Write(p []byte) (int, error) {
	if !f.once {
		f.once = true
		close(f.ready)
	}
	return f.w.Write(p)
}
