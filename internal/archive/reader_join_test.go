package archive

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amp4 "github.com/abema/go-mp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
)

var joinSPS = []byte{
	0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02,
	0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04,
	0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9,
	0x20,
}

var t0 = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

// segmentBytes is a segment as the engine writes it: one H.264 track,
// seconds 1 s fragments of two half-second samples, an mtxi box when info
// is given.
func segmentBytes(t *testing.T, info *mtxi, seconds int) []byte {
	t.Helper()
	return buildSegment(t, segmentSpec{info: info, seconds: seconds, gop: 1})
}

// segmentSpec describes a synthetic segment: seconds 1 s fragments of two
// half-second samples, a sync sample opening every gop-th fragment, each
// sample padded to payload bytes; with sound, a second track whose samples
// are never marked sync.
type segmentSpec struct {
	info    *mtxi
	seconds int
	gop     int
	payload int
	sound   bool
}

func buildSegment(t testing.TB, spec segmentSpec) []byte {
	t.Helper()
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{{ID: 1, TimeScale: 90000, Codec: &codecs.H264{SPS: joinSPS, PPS: []byte{0x08}}}}}
	if spec.sound {
		init.Tracks = append(init.Tracks, &fmp4.InitTrack{ID: 2, TimeScale: 8000, Codec: &codecs.LPCM{BitDepth: 16, SampleRate: 8000, ChannelCount: 1}})
	}
	if spec.info != nil {
		init.UserData = []amp4.IBox{spec.info}
	}
	var buf seekablebuffer.Buffer
	if err := init.Marshal(&buf); err != nil {
		t.Fatal(err)
	}
	for s := range spec.seconds {
		p := fmp4.Part{SequenceNumber: uint32(s), Tracks: spec.tracksAt(s)}
		if err := p.Marshal(&buf); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// tracksAt is the fragment of second s: the video run and, with sound, two
// sound samples marked non-sync.
func (spec segmentSpec) tracksAt(s int) []*fmp4.PartTrack {
	sample := func(nal byte) []byte {
		p := make([]byte, max(spec.payload, 6))
		copy(p, []byte{0, 0, 0, 1, nal, byte(s)})
		return p
	}
	first := &fmp4.Sample{Duration: 45000, Payload: sample(0x65)}
	if s%spec.gop != 0 {
		first = &fmp4.Sample{Duration: 45000, IsNonSyncSample: true, Payload: sample(0x41)}
	}
	tracks := []*fmp4.PartTrack{{ID: 1, BaseTime: uint64(s) * 90000, Samples: []*fmp4.Sample{
		first, {Duration: 45000, IsNonSyncSample: true, Payload: sample(0x41)},
	}}}
	if spec.sound {
		tracks = append(tracks, &fmp4.PartTrack{ID: 2, BaseTime: uint64(s) * 8000, Samples: []*fmp4.Sample{
			{Duration: 4000, IsNonSyncSample: true, Payload: make([]byte, 32)},
			{Duration: 4000, IsNonSyncSample: true, Payload: make([]byte, 32)},
		}})
	}
	return tracks
}

func putSegment(t testing.TB, root string, start time.Time, data []byte) {
	t.Helper()
	dir := filepath.Join(root, "cam_1", "main")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, formatFileTime(start)+".mp4"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// spanLength reads a span and returns how much video it holds.
func spanLength(t *testing.T, root string, start time.Time, length time.Duration) time.Duration {
	t.Helper()
	return spanLengthWith(t, root, OpenPlain, start, length)
}

func spanLengthWith(t *testing.T, root string, open SegmentOpener, start time.Time, length time.Duration) time.Duration {
	t.Helper()
	body, err := NewReader(root, open).Span(context.Background(), "cam_1", "main", start, length)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	d, err := FMP4Duration(data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSpansJoinOnlyTheNextSegmentOfTheSameRecording(t *testing.T) {
	recA, recB := [16]byte{1}, [16]byte{2}
	cases := []struct {
		name   string
		second *mtxi
		want   time.Duration
	}{
		{"the next segment joins", &mtxi{StreamID: recA, SegmentNumber: 1, DTS: int64(2 * time.Second)}, 4 * time.Second},
		{"a new recording does not", &mtxi{StreamID: recB, SegmentNumber: 0}, 2 * time.Second},
		{"a new recording numbered next does not", &mtxi{StreamID: recB, SegmentNumber: 1, DTS: int64(2 * time.Second)}, 2 * time.Second},
		{"a missing segment stops the join", &mtxi{StreamID: recA, SegmentNumber: 2, DTS: int64(2 * time.Second)}, 2 * time.Second},
		{"a segment without mtxi does not", nil, 2 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			putSegment(t, root, t0, segmentBytes(t, &mtxi{StreamID: recA, SegmentNumber: 0}, 2))
			putSegment(t, root, t0.Add(2*time.Second), segmentBytes(t, c.second, 2))
			if got := spanLength(t, root, t0, time.Minute); got != c.want {
				t.Fatalf("span holds %v, want %v", got, c.want)
			}
		})
	}
}

// A sample exactly at the span's end belongs to what comes after it.
func TestSpanEndsBeforeTheSampleAtItsEnd(t *testing.T) {
	root := t.TempDir()
	putSegment(t, root, t0, segmentBytes(t, nil, 3))
	if got := spanLength(t, root, t0, time.Second); got != time.Second {
		t.Fatalf("span holds %v, want 1s", got)
	}
}

// Segments without mtxi (no engine info) join when the next one starts
// within a second of where the previous one ended.
func TestSpansWithoutEngineInfoJoinByTime(t *testing.T) {
	for _, c := range []struct {
		gap  time.Duration
		want time.Duration
	}{{500 * time.Millisecond, 4*time.Second + 500*time.Millisecond}, {3 * time.Second, 2 * time.Second}} {
		root := t.TempDir()
		putSegment(t, root, t0, segmentBytes(t, nil, 2))
		putSegment(t, root, t0.Add(2*time.Second+c.gap), segmentBytes(t, nil, 2))
		if got := spanLength(t, root, t0, time.Minute); got != c.want {
			t.Errorf("gap %v: span holds %v, want %v", c.gap, got, c.want)
		}
	}
}

// The segment being written ends in a fragment that is not complete yet:
// the span holds what is complete.
func TestSpanReadsASegmentStillBeingWritten(t *testing.T) {
	root := t.TempDir()
	data := segmentBytes(t, nil, 3)
	putSegment(t, root, t0, data[:len(data)-4])
	if got := spanLength(t, root, t0, time.Minute); got != 2*time.Second {
		t.Fatalf("span holds %v, want the 2 complete seconds", got)
	}
}

// A segment cut inside a box header (the engine had written fewer than 8
// bytes of the next box) holds what is complete, read from its start or
// from a seek into it.
func TestSpanReadsASegmentCutInsideABoxHeader(t *testing.T) {
	root := t.TempDir()
	putSegment(t, root, t0, append(segmentBytes(t, nil, 3), 0, 0, 0))
	for _, from := range []time.Duration{0, 1500 * time.Millisecond} {
		if got := spanLength(t, root, t0.Add(from), time.Minute); got != 3*time.Second-from {
			t.Errorf("from %v: span holds %v, want %v", from, got, 3*time.Second-from)
		}
	}
}

func TestSpanEndsWithTheCallersContext(t *testing.T) {
	root := t.TempDir()
	putSegment(t, root, t0, segmentBytes(t, nil, 30))
	ctx, cancel := context.WithCancel(context.Background())
	body, err := NewReader(root, OpenPlain).Span(ctx, "cam_1", "main", t0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	cancel()
	if _, err := io.ReadAll(body); !errors.Is(err, context.Canceled) {
		t.Fatalf("read after cancel: %v, want context.Canceled", err)
	}
}

func TestSpanRefusesNamesOutsideTheArchive(t *testing.T) {
	root := t.TempDir()
	putSegment(t, root, t0, segmentBytes(t, nil, 2))
	r := NewReader(filepath.Join(root, "cam_1"), OpenPlain)
	for _, cam := range []string{"..", ".", "", "a/b", `a\b`} {
		if _, err := r.Span(context.Background(), cam, "main", t0, time.Minute); !errors.Is(err, ErrNoFootage) {
			t.Errorf("camera %q: %v, want ErrNoFootage", cam, err)
		}
	}
}

// memFile is a SegmentFile over bytes.
type memFile struct{ *bytes.Reader }

func (memFile) Close() error { return nil }

func TestBoxSizesAsMP4WritesThem(t *testing.T) {
	be := func(n int) []byte { return binary.BigEndian.AppendUint32(nil, uint32(n)) }
	large := append(append(be(1), "mdat"...), binary.BigEndian.AppendUint64(nil, 24)...)
	cases := []struct {
		name string
		data []byte
		size int64
		err  error
	}{
		{"32-bit size", append(append(be(12), "free"...), 0, 0, 0, 0), 12, nil},
		{"64-bit size", append(large, make([]byte, 8)...), 24, nil},
		{"size 0 runs to the end", append(append(be(0), "mdat"...), make([]byte, 5)...), 13, nil},
		{"smaller than a header", append(be(4), "free"...), 0, errTruncated},
		{"past the end", append(be(64), "free"...), 0, errTruncated},
		{"no room for a header", []byte{0, 0, 0}, 0, errTruncated},
		{"64-bit size cut off", append(be(1), "mdat"...), 0, errTruncated},
	}
	for _, c := range cases {
		b, err := boxAt(memFile{bytes.NewReader(c.data)}, 0)
		if !errors.Is(err, c.err) || (err == nil && b.size != c.size) {
			t.Errorf("%s: size %d, err %v; want %d, %v", c.name, b.size, err, c.size, c.err)
		}
	}
}

// A first segment that is not MP4 is an error before any byte is answered.
func TestSpanOfAnUnreadableSegmentIsAnError(t *testing.T) {
	root := t.TempDir()
	putSegment(t, root, t0, []byte("not an mp4 file at all"))
	_, err := NewReader(root, OpenPlain).Span(context.Background(), "cam_1", "main", t0, time.Minute)
	if err == nil || errors.Is(err, ErrNoFootage) {
		t.Fatalf("err = %v, want a read error", err)
	}
}

// countingOpener opens plain files and counts how many are still open
// and how many bytes were read from them.
type countingOpener struct {
	opened, open atomic.Int32
	read         atomic.Int64
}

func (c *countingOpener) Open(seg Segment) (SegmentFile, error) {
	f, err := OpenPlain(seg)
	if err != nil {
		return nil, err
	}
	c.opened.Add(1)
	c.open.Add(1)
	return &countedFile{SegmentFile: f, c: c}, nil
}

type countedFile struct {
	SegmentFile
	c    *countingOpener
	once sync.Once
}

func (f *countedFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.SegmentFile.ReadAt(p, off)
	f.c.read.Add(int64(n))
	return n, err
}

func (f *countedFile) Close() error {
	f.once.Do(func() { f.c.open.Add(-1) })
	return f.SegmentFile.Close()
}

// A viewer who stops watching closes the answer without cancelling
// anything: every segment file the reader opened is closed again (an open
// handle would keep retention from deleting the file on Windows).
func TestClosingASpanEarlyClosesItsSegmentFiles(t *testing.T) {
	root := t.TempDir()
	rec := [16]byte{7}
	for i := range 3 {
		putSegment(t, root, t0.Add(time.Duration(i)*10*time.Second), segmentBytes(t, &mtxi{StreamID: rec, SegmentNumber: uint64(i), DTS: int64(time.Duration(i) * 10 * time.Second)}, 10))
	}
	for _, c := range []struct {
		name     string
		minFiles int32 // read until the reader has opened this many
	}{{"before reading", 0}, {"inside a following segment", 2}} {
		t.Run(c.name, func(t *testing.T) {
			counter := &countingOpener{}
			body, err := NewReader(root, counter.Open).Span(context.Background(), "cam_1", "main", t0, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 64)
			for counter.opened.Load() < c.minFiles {
				if _, err := body.Read(buf); err != nil {
					t.Fatalf("read: %v", err)
				}
			}
			if err := body.Close(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(2 * time.Second)
			for counter.open.Load() != 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if n := counter.open.Load(); n != 0 || counter.opened.Load() < max(c.minFiles, 1) {
				t.Fatalf("%d of %d opened segment files still open", n, counter.opened.Load())
			}
		})
	}
}
