package archive

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"filippo.io/age"
)

// age seals in 64 KiB chunks, each authenticated on its own.
const ageChunk = 64 << 10

// sealedSegment records a 20-second segment of 8 KiB samples (about 320 KiB,
// five chunks) under root and seals it; it returns the sealed path.
func sealedSegment(t *testing.T, root string, key *age.X25519Identity) string {
	t.Helper()
	putSegment(t, root, t0, buildSegment(t, segmentSpec{seconds: 20, gop: 1, payload: 8 << 10}))
	out, err := SealFile(segPath(root, t0), key.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// payloadStart is where the sealed chunks begin: after the age header and
// the 16-byte nonce.
func payloadStart(t *testing.T, raw []byte) int {
	t.Helper()
	mac := bytes.Index(raw, []byte("\n--- "))
	end := bytes.IndexByte(raw[mac+1:], '\n')
	if mac < 0 || end < 0 {
		t.Fatal("no age header")
	}
	return mac + 1 + end + 1 + 16
}

// tamper flips one byte inside sealed chunk c (0 is the first).
func tamper(t *testing.T, path string, c int) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	raw[payloadStart(t, raw)+c*(ageChunk+16)+100] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A byte changed deep inside a sealed segment: the chunk holding it reads
// as an error with no bytes, the others still read, and a span over it
// ends with an error rather than with footage that looks whole.
func TestATamperedChunkIsRefused(t *testing.T) {
	root, key := t.TempDir(), newKey(t)
	path := sealedSegment(t, root, key)
	tamper(t, path, 2)

	f, err := OpenSealed(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 1000)
	if n, err := f.ReadAt(buf, 2*ageChunk+500); err == nil || n != 0 {
		t.Fatalf("tampered chunk: %d bytes, %v; want none and an error", n, err)
	}
	if n, err := f.ReadAt(buf, ageChunk+500); err != nil || n != len(buf) {
		t.Fatalf("an untouched chunk: %d bytes, %v", n, err)
	}

	open := func(seg Segment) (SegmentFile, error) { return OpenSealed(seg.Path, key) }
	body, err := NewReader(root, open).Span(context.Background(), "cam_1", "main", t0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	if _, err := io.ReadAll(body); err == nil {
		t.Fatal("a span over the tampered chunk read to its end without an error")
	}
}

// A changed header chunk (ftyp and moov): the span fails before it answers
// a byte.
func TestATamperedFirstChunkAnswersNothing(t *testing.T) {
	root, key := t.TempDir(), newKey(t)
	tamper(t, sealedSegment(t, root, key), 0)
	open := func(seg Segment) (SegmentFile, error) { return OpenSealed(seg.Path, key) }
	if body, err := NewReader(root, open).Span(context.Background(), "cam_1", "main", t0, time.Minute); err == nil {
		_ = body.Close()
		t.Fatal("a span over a tampered header started")
	}
}
