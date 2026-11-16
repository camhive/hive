package archive

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
)

func newKey(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestASealedFileReplacesThePlainOneAndReadsBackWithRandomAccess(t *testing.T) {
	key := newKey(t)
	plain := buildSegment(t, segmentSpec{seconds: 30, gop: 1, payload: 4 << 10})
	path := filepath.Join(t.TempDir(), "2026-11-16_08-00-00-000000_Z.mp4")
	writeFile(t, path, plain)

	out, err := SealFile(path, key.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if out != path+SealedExt {
		t.Fatalf("sealed as %s", out)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plain file is still there: %v", err)
	}
	f, err := OpenSealed(out, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if f.Size() != int64(len(plain)) {
		t.Fatalf("plain size %d, want %d", f.Size(), len(plain))
	}
	tail := make([]byte, 100)
	if _, err := f.ReadAt(tail, f.Size()-100); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if string(tail) != string(plain[len(plain)-100:]) {
		t.Fatal("the end of the sealed file reads back different bytes")
	}
}

func TestASealedFileDoesNotOpenWithAnotherKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.mp4")
	writeFile(t, path, []byte("footage"))
	out, err := SealFile(path, newKey(t).Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSealed(out, newKey(t)); err == nil {
		t.Fatal("a sealed file opened with another key")
	}
}

// A crash after the rename leaves both files: sealing again keeps the
// sealed one (it was synced before the rename) and removes the plain one.
func TestSealingAgainAfterACrashKeepsTheSealedFile(t *testing.T) {
	key := newKey(t)
	path := filepath.Join(t.TempDir(), "a.mp4")
	writeFile(t, path, []byte("footage"))
	out, err := SealFile(path, key.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, []byte("left behind"))
	if _, err := SealFile(path, newKey(t).Recipient()); err != nil {
		t.Fatal(err)
	}
	f, err := OpenSealed(out, key)
	if err != nil {
		t.Fatalf("the first sealed file was replaced: %v", err)
	}
	_ = f.Close()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plain file is still there: %v", err)
	}
}

func TestParsePathKnowsSealedSegments(t *testing.T) {
	root := "/a"
	plain, ok1 := ParsePath(root, "/a/cam_1/main/2026-11-16_08-00-00-000000_Z.mp4")
	seal, ok2 := ParsePath(root, "/a/cam_1/main/2026-11-16_08-00-00-000000_Z.mp4"+SealedExt)
	if !ok1 || !ok2 || !plain.Start.Equal(seal.Start) || plain.Sealed() || !seal.Sealed() {
		t.Fatalf("plain %+v %v, sealed %+v %v", plain, ok1, seal, ok2)
	}
	for _, name := range []string{".2026-11-16_08-00-00-000000_Z.mp4.age.tmp", "2026-11-16_08-00-00-000000_Z.age", "2026-11-16_08-00-00-000000_Z.mp4.age.age"} {
		if _, ok := ParsePath(root, "/a/cam_1/main/"+name); ok {
			t.Errorf("%s parsed as a segment", name)
		}
	}
}

// While a segment is being sealed both files exist for a moment; the
// sealed one is the segment.
func TestTheSealedFileWinsOverItsPlainLeftover(t *testing.T) {
	root := t.TempDir()
	key := newKey(t)
	putSegment(t, root, t0, segmentBytes(t, nil, 2))
	plain := filepath.Join(root, "cam_1", "main", formatFileTime(t0)+".mp4")
	if _, err := SealFile(plain, key.Recipient()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, plain, []byte("not a segment"))
	open := func(seg Segment) (SegmentFile, error) {
		if seg.Sealed() {
			return OpenSealed(seg.Path, key)
		}
		return OpenPlain(seg)
	}
	segs, err := NewReader(root, open).segments("cam_1", "main")
	if err != nil || len(segs) != 1 || !segs[0].Sealed() {
		t.Fatalf("segments %+v, %v; want the sealed one only", segs, err)
	}
	if got := spanLengthWith(t, root, open, t0, time.Minute); got != 2*time.Second {
		t.Fatalf("span holds %v, want 2s", got)
	}
}

// refusing is a recipient whose wrap fails, as a broken key would.
type refusing struct{}

func (refusing) Wrap([]byte) ([]*age.Stanza, error) { return nil, errors.New("no") }

// A seal that fails keeps the plain file and leaves no temp file behind;
// a missing plain file is an error, not an empty seal.
func TestAFailedSealKeepsThePlainFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp4")
	writeFile(t, path, []byte("footage"))
	if _, err := SealFile(path, refusing{}); err == nil {
		t.Fatal("sealing with a broken key succeeded")
	}
	assertFiles(t, dir, []string{"a.mp4"})
	if _, err := SealFile(filepath.Join(dir, "gone.mp4"), newKey(t).Recipient()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealing a missing file: %v", err)
	}
	assertFiles(t, dir, []string{"a.mp4"})
}
