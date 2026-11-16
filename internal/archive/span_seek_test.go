package archive

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
)

// fragmentLayout measures a synthetic segment: its header and one fragment,
// and the moof inside that fragment.
func fragmentLayout(t *testing.T, spec segmentSpec) (header, fragment, moof int64) {
	t.Helper()
	spec.seconds = 1
	one := int64(len(buildSegment(t, spec)))
	spec.seconds = 2
	fragment = int64(len(buildSegment(t, spec))) - one
	mdat := int64(8 + 2*max(spec.payload, 6))
	return one - fragment, fragment, fragment - mdat
}

// A seek to the end of a ten-minute segment reads the segment's moofs and
// the fragments from the group of pictures before the start on (issue
// #100), not every fragment from the start of the file.
func TestSeekingIntoALongSegmentReadsABoundedAmount(t *testing.T) {
	spec := segmentSpec{seconds: 600, gop: 1, payload: 8 << 10}
	root := t.TempDir()
	putSegment(t, root, t0, buildSegment(t, spec))
	counter := &countingOpener{}

	data := readAll(t, NewReader(root, counter.Open), t0.Add(599*time.Second), time.Second)

	if d, err := FMP4Duration(data); err != nil || d != time.Second {
		t.Fatalf("span holds %v, %v; want 1s", d, err)
	}
	header, fragment, moof := fragmentLayout(t, spec)
	bound := header + int64(spec.seconds)*moof + 3*fragment
	if got := counter.read.Load(); got > bound {
		t.Fatalf("read %d bytes of a %d-byte segment, want at most %d", got, header+int64(spec.seconds)*fragment, bound)
	}
}

// With a long group of pictures the span still starts with the sync sample
// before the seek point, so the player can decode its first frame.
func TestSeekingIntoALongGOPStartsAtItsSyncSample(t *testing.T) {
	root := t.TempDir()
	putSegment(t, root, t0, buildSegment(t, segmentSpec{seconds: 20, gop: 4}))

	data := readAll(t, NewReader(root, OpenPlain), t0.Add(10500*time.Millisecond), time.Second)

	first := firstSample(t, data)
	if first.IsNonSyncSample || first.Payload[5] != 8 {
		t.Fatalf("span starts with sample of second %d (sync %v), want the sync sample of second 8", first.Payload[5], !first.IsNonSyncSample)
	}
}

func readAll(t *testing.T, r *Reader, start time.Time, length time.Duration) []byte {
	t.Helper()
	body, err := r.Span(context.Background(), "cam_1", "main", start, length)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// firstSample is the first sample of the first fragment of an answer.
func firstSample(t *testing.T, data []byte) *fmp4.Sample {
	t.Helper()
	f := memFile{bytes.NewReader(data)}
	ftyp, err := boxAt(f, 0)
	if err != nil {
		t.Fatal(err)
	}
	moov, err := boxAt(f, ftyp.size)
	if err != nil {
		t.Fatal(err)
	}
	var parts fmp4.Parts
	if err := parts.Unmarshal(data[ftyp.size+moov.size:]); err != nil {
		t.Fatal(err)
	}
	return parts[0].Tracks[0].Samples[0]
}

// BenchmarkSeekToTheEndOfATenMinuteSegment measures issue #100 on a
// ten-minute 4 Mbit/s segment (about 300 MB): a one-second span from minute
// 9:59, with the bytes read from the segment per seek.
func BenchmarkSeekToTheEndOfATenMinuteSegment(b *testing.B) {
	root := b.TempDir()
	putSegment(b, root, t0, buildSegment(b, segmentSpec{seconds: 600, gop: 1, payload: 250_000}))
	counter := &countingOpener{}
	r := NewReader(root, counter.Open)
	b.ResetTimer()
	for b.Loop() {
		body, err := r.Span(context.Background(), "cam_1", "main", t0.Add(599*time.Second), time.Second)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, body); err != nil {
			b.Fatal(err)
		}
		_ = body.Close()
	}
	b.ReportMetric(float64(counter.read.Load())/float64(b.N), "bytes-read/op")
}

// The resume point is per track (#100 review): a track that never marks a
// sync sample keeps every sample before the start, so reading resumes at
// its first fragment, not at the video's group of pictures, and the answer
// is byte for byte what a read from offset 0 gives.
func TestATrackWithoutSyncSamplesKeepsItsSamplesBeforeTheStart(t *testing.T) {
	root := t.TempDir()
	putSegment(t, root, t0, buildSegment(t, segmentSpec{seconds: 20, gop: 4, sound: true}))
	start := t0.Add(10500 * time.Millisecond)

	got := readAll(t, NewReader(root, OpenPlain), start, 2*time.Second)
	walkBeforeStart = false
	t.Cleanup(func() { walkBeforeStart = true })
	want := readAll(t, NewReader(root, OpenPlain), start, 2*time.Second)

	if !bytes.Equal(got, want) {
		t.Fatalf("%d bytes with the walk, %d from offset 0", len(got), len(want))
	}
}
