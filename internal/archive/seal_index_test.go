package archive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/camhive/hive/internal/store"
)

// lockable opens plain files always and sealed ones only while unlocked,
// like the hive's vault.
type lockable struct {
	key    *age.X25519Identity
	locked bool
}

func (l *lockable) open(seg Segment) (SegmentFile, error) {
	if !seg.Sealed() {
		return OpenPlain(seg)
	}
	if l.locked {
		return nil, ErrLocked
	}
	return OpenSealed(seg.Path, l.key)
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func segPath(root string, start time.Time) string {
	return filepath.Join(root, "cam_1", "main", formatFileTime(start)+".mp4")
}

// Three segments, the newest still being written: the two closed ones are
// sealed and their rows keep what the plain files said, so the timeline
// knows them while the archive is locked.
func TestSealingSealsClosedSegmentsAndKeepsTheirRows(t *testing.T) {
	root, keys := t.TempDir(), &lockable{key: newKey(t), locked: true}
	for i := range 3 {
		putSegment(t, root, t0.Add(time.Duration(i)*2*time.Second), segmentBytes(t, nil, 2))
	}
	idx := NewIndex(openStore(t), WithOpener(keys.open))
	now := time.Now()

	n, err := idx.Seal(context.Background(), root, keys.key.Recipient(), now)
	if err != nil || n != 2 {
		t.Fatalf("sealed %d, %v; want 2", n, err)
	}
	if _, err := idx.Reconcile(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	segs, err := idx.All(context.Background())
	if err != nil || len(segs) != 3 {
		t.Fatalf("index %+v, %v", segs, err)
	}
	for i, s := range segs[:2] {
		st, _ := os.Stat(s.Path)
		if !s.Sealed() || st == nil || s.Bytes != st.Size() || s.Duration != 2*time.Second || s.Analysis != AnalysisVersion || !s.Complete {
			t.Errorf("segment %d: %+v; want sealed, its sealed size, 2s, analysed, complete", i, s)
		}
	}
	if segs[2].Sealed() || segs[2].Complete {
		t.Errorf("the segment being written: %+v; want plain and incomplete", segs[2])
	}
}

// The newest segment of a stream whose recording stopped is sealed once
// nothing was written to it for the writing window.
func TestSealingSealsTheLastSegmentOfAStoppedRecording(t *testing.T) {
	root, key := t.TempDir(), newKey(t)
	putSegment(t, root, t0, segmentBytes(t, nil, 2))
	idx := NewIndex(openStore(t))
	if n, _ := idx.Seal(context.Background(), root, key.Recipient(), time.Now()); n != 0 {
		t.Fatalf("sealed %d segments still being written", n)
	}
	if n, err := idx.Seal(context.Background(), root, key.Recipient(), time.Now().Add(WritingWindow)); err != nil || n != 1 {
		t.Fatalf("sealed %d, %v after the writing window; want 1", n, err)
	}
}

// After a crash: a temp seal is removed, a plain file beside its seal is
// removed, a plain closed segment is sealed; a second pass changes nothing.
func TestSealingCleansUpAfterACrashAndIsIdempotent(t *testing.T) {
	root, key := t.TempDir(), newKey(t)
	putSegment(t, root, t0, segmentBytes(t, nil, 2))
	putSegment(t, root, t0.Add(2*time.Second), segmentBytes(t, nil, 2))
	putSegment(t, root, t0.Add(4*time.Second), segmentBytes(t, nil, 2))
	first := segPath(root, t0)
	if _, err := SealFile(first, key.Recipient()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, first, segmentBytes(t, nil, 2)) // the plain file a crash left beside its seal
	tmp := filepath.Join(filepath.Dir(first), "."+filepath.Base(segPath(root, t0.Add(2*time.Second)))+sealingExt)
	writeFile(t, tmp, []byte("half a seal"))
	idx := NewIndex(openStore(t))

	if _, err := idx.Seal(context.Background(), root, key.Recipient(), time.Now()); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Base(first) + SealedExt, filepath.Base(segPath(root, t0.Add(2*time.Second))) + SealedExt, filepath.Base(segPath(root, t0.Add(4*time.Second)))}
	assertFiles(t, filepath.Dir(first), want)
	if n, err := idx.Seal(context.Background(), root, key.Recipient(), time.Now()); err != nil || n != 0 {
		t.Fatalf("second pass sealed %d, %v", n, err)
	}
	assertFiles(t, filepath.Dir(first), want)
}

func assertFiles(t *testing.T, dir string, want []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("files %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("files %v, want %v", got, want)
		}
	}
}

// A sealed segment the index does not know (a rebuilt index) is listed
// while locked, unread; once unlocked, the next reconcile reads it.
func TestASealedSegmentIsReadOnceUnlocked(t *testing.T) {
	root, keys := t.TempDir(), &lockable{key: newKey(t), locked: true}
	putSegment(t, root, t0, segmentBytes(t, nil, 2))
	putSegment(t, root, t0.Add(2*time.Second), segmentBytes(t, nil, 2))
	if _, err := SealFile(segPath(root, t0), keys.key.Recipient()); err != nil {
		t.Fatal(err)
	}
	idx := NewIndex(openStore(t), WithOpener(keys.open))
	if _, err := idx.Reconcile(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	segs, _ := idx.All(context.Background())
	if segs[0].Analysis != 0 || segs[0].Bytes == 0 {
		t.Fatalf("locked: %+v; want listed with its size, unread", segs[0])
	}
	keys.locked = false
	if _, err := idx.Reconcile(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	segs, _ = idx.All(context.Background())
	if segs[0].Duration != 2*time.Second || segs[0].Analysis != AnalysisVersion {
		t.Fatalf("unlocked: %+v; want read", segs[0])
	}
}

func TestStillWritingIsTheNewestFileModifiedRecently(t *testing.T) {
	now := time.Now()
	cases := []struct {
		complete bool
		modified time.Time
		want     bool
	}{
		{false, now.Add(-time.Second), true},
		{false, now.Add(-WritingWindow), false},
		{true, now, false},
	}
	for _, c := range cases {
		if got := StillWriting(Segment{Complete: c.complete}, c.modified, now); got != c.want {
			t.Errorf("complete %v modified %v ago: %v, want %v", c.complete, now.Sub(c.modified), got, c.want)
		}
	}
}

// A segment that cannot be sealed (a permission, a full disk, a file the
// engine still holds on Windows) is reported, and the pass goes on with
// the others: one bad file must not leave everything newer in clear.
func TestOneSegmentThatCannotBeSealedDoesNotStopTheOthers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	root, key := t.TempDir(), newKey(t)
	for i := range 3 {
		putSegment(t, root, t0.Add(time.Duration(i)*2*time.Second), segmentBytes(t, nil, 2))
	}
	if err := os.Chmod(segPath(root, t0), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(segPath(root, t0), 0o600) })

	n, err := NewIndex(openStore(t)).Seal(context.Background(), root, key.Recipient(), time.Now())

	if err == nil || n != 1 {
		t.Fatalf("sealed %d, %v; want the readable closed one and the failure reported", n, err)
	}
	if _, err := os.Stat(segPath(root, t0.Add(2*time.Second)) + SealedExt); err != nil {
		t.Fatalf("the segment after the bad one is not sealed: %v", err)
	}
}

// Enabling on a large archive seals for a long time: the pass stops with
// its context instead of running on.
func TestSealingStopsWithItsContext(t *testing.T) {
	root, key := t.TempDir(), newKey(t)
	for i := range 3 {
		putSegment(t, root, t0.Add(time.Duration(i)*2*time.Second), segmentBytes(t, nil, 2))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := NewIndex(openStore(t)).Seal(ctx, root, key.Recipient(), time.Now())
	if !errors.Is(err, context.Canceled) || n != 0 {
		t.Fatalf("sealed %d, %v; want nothing and context.Canceled", n, err)
	}
}
