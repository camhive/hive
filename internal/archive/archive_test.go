package archive_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestParseSegmentPath(t *testing.T) {
	root := filepath.Join("/", "archive")
	good := filepath.Join(root, "cam_a", "main", "2026-11-09_10-30-15-250000.mp4")
	seg, ok := archive.ParsePath(root, good)
	if !ok {
		t.Fatal("valid path not parsed")
	}
	want := time.Date(2026, 11, 9, 10, 30, 15, 250000000, time.UTC)
	if seg.Camera != "cam_a" || seg.Stream != "main" || !seg.Start.Equal(want) || seg.Path != good {
		t.Fatalf("seg = %+v", seg)
	}
	// With an offset: 11:30:15.25 +01:00 is the same instant as 10:30:15.25Z.
	withTZ := filepath.Join(root, "cam_a", "main", "2026-11-09_11-30-15-250000_+0100.mp4")
	seg, ok = archive.ParsePath(root, withTZ)
	if !ok || !seg.Start.Equal(want) {
		t.Fatalf("offset name: ok=%v start=%v", ok, seg.Start)
	}
	utcZ := filepath.Join(root, "cam_a", "main", "2026-11-09_10-30-15-250000_Z.mp4")
	if seg, ok := archive.ParsePath(root, utcZ); !ok || !seg.Start.Equal(want) {
		t.Fatalf("Z name: ok=%v start=%v", ok, seg.Start)
	}
	for _, bad := range []string{
		filepath.Join(root, "cam_a", "main", "2026-11-09_10-30-15-250000_+2500.mp4"),
		filepath.Join(root, "cam_a", "main", "notes.txt"),
		filepath.Join(root, "cam_a", "2026-11-09_10-30-15-250000.mp4"),
		filepath.Join(root, "cam_a", "main", "2026-13-09_10-30-15-250000.mp4"),
		filepath.Join(root, "cam_a", "main", "2026-11-09_10-30-15.mp4"),
		filepath.Join("/", "elsewhere", "cam_a", "main", "2026-11-09_10-30-15-250000.mp4"),
	} {
		if _, ok := archive.ParsePath(root, bad); ok {
			t.Errorf("%s must not parse", bad)
		}
	}
}

func TestFMP4DurationFromSyntheticBoxes(t *testing.T) {
	// 90 kHz timescale; two fragments: 30 samples × 3000 ticks and 15 samples
	// with the default duration 3000 → 45 × 3000 / 90000 = 1.5 s.
	data := buildFMP4(t, 90000, 3000, []fragment{{samples: 30, explicitDur: 3000}, {samples: 15}})
	d, err := archive.FMP4Duration(data)
	if err != nil {
		t.Fatal(err)
	}
	if d != 1500*time.Millisecond {
		t.Fatalf("duration = %v, want 1.5s", d)
	}
	if _, err := archive.FMP4Duration([]byte("not an mp4 at all")); err == nil {
		t.Fatal("garbage must error")
	}
	// Truncated inside the last moof (a file still being written): whatever
	// fragments are complete still count.
	d, err = archive.FMP4Duration(data[:len(data)-40])
	if err != nil || d != 1000*time.Millisecond {
		t.Fatalf("truncated: %v %v, want 1s", d, err)
	}
}

func TestIndexUpsertRangeCalendarDelete(t *testing.T) {
	s := openStore(t)
	idx := archive.NewIndex(s)
	ctx := context.Background()
	base := time.Date(2026, 11, 9, 23, 50, 0, 0, time.UTC)
	for i := range 4 { // 23:50, 00:00, 00:10, 00:20 UTC — crosses midnight
		seg := archive.Segment{Camera: "cam_a", Stream: "main", Start: base.Add(time.Duration(i) * 10 * time.Minute), Duration: 10 * time.Minute, Bytes: 100, Path: "/p" + string(rune('0'+i)), Complete: true}
		if err := idx.Upsert(ctx, seg); err != nil {
			t.Fatal(err)
		}
	}
	// Upsert is an update on the same start.
	if err := idx.Upsert(ctx, archive.Segment{Camera: "cam_a", Stream: "main", Start: base, Duration: 9 * time.Minute, Bytes: 99, Path: "/p0", Complete: true}); err != nil {
		t.Fatal(err)
	}

	// Range: overlap semantics — a segment counts if any part of it is inside.
	got, err := idx.Range(ctx, "cam_a", "main", base.Add(5*time.Minute), base.Add(25*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Duration != 9*time.Minute || !got[2].Start.Equal(base.Add(20*time.Minute)) {
		t.Fatalf("range = %+v", got)
	}
	if got, _ := idx.Range(ctx, "cam_b", "main", base, base.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("other camera = %+v", got)
	}

	// Calendar in UTC: two days; in Europe/Berlin (UTC+1): one day (2026-11-10).
	days, err := idx.Calendar(ctx, "cam_a", "main", time.UTC)
	if err != nil || len(days) != 2 || days[0].Format("2006-01-02") != "2026-11-09" || days[1].Format("2006-01-02") != "2026-11-10" {
		t.Fatalf("utc calendar = %v err=%v", days, err)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	days, _ = idx.Calendar(ctx, "cam_a", "main", berlin)
	if len(days) != 1 || days[0].Format("2006-01-02") != "2026-11-10" {
		t.Fatalf("berlin calendar = %v", days)
	}

	if err := idx.Delete(ctx, "cam_a", "main", base); err != nil {
		t.Fatal(err)
	}
	got, _ = idx.Range(ctx, "cam_a", "main", base.Add(-time.Hour), base.Add(time.Hour))
	if len(got) != 3 {
		t.Fatalf("after delete = %d", len(got))
	}
	total, err := idx.Usage(ctx)
	if err != nil || total.Bytes != 300 || total.Segments != 3 {
		t.Fatalf("usage = %+v err=%v", total, err)
	}
}

func TestReconcileMakesTheIndexMatchTheFiles(t *testing.T) {
	s := openStore(t)
	idx := archive.NewIndex(s)
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "cam_a", "main")
	_ = os.MkdirAll(dir, 0o750)
	_ = os.MkdirAll(filepath.Join(root, "cam_a", "sub"), 0o750)
	files := []string{"2026-11-09_10-00-00-000000.mp4", "2026-11-09_10-10-00-000000.mp4", "2026-11-09_10-20-00-000000.mp4"}
	for _, f := range files {
		_ = os.WriteFile(filepath.Join(dir, f), buildFMP4(t, 90000, 3000, []fragment{{samples: 30}}), 0o600)
	}
	_ = os.WriteFile(filepath.Join(dir, "junk.txt"), []byte("x"), 0o600)

	rep, err := idx.Reconcile(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added != 3 || rep.Removed != 0 {
		t.Fatalf("first reconcile = %+v", rep)
	}
	segs, _ := idx.Range(ctx, "cam_a", "main", time.Time{}, time.Now().Add(24*time.Hour))
	if len(segs) != 3 || segs[0].Duration != time.Second || !segs[0].Complete || segs[2].Complete {
		t.Fatalf("segments = %+v (newest must be marked incomplete, others complete with 1s)", segs)
	}

	// A file vanishes (deleted by hand) and a new one appears.
	_ = os.Remove(filepath.Join(dir, files[0]))
	_ = os.WriteFile(filepath.Join(dir, "2026-11-09_10-30-00-000000.mp4"), buildFMP4(t, 90000, 3000, []fragment{{samples: 60}}), 0o600)
	rep, _ = idx.Reconcile(ctx, root)
	if rep.Added != 1 || rep.Removed != 1 {
		t.Fatalf("second reconcile = %+v", rep)
	}
	segs, _ = idx.Range(ctx, "cam_a", "main", time.Time{}, time.Now().Add(24*time.Hour))
	if len(segs) != 3 || segs[0].Path != filepath.Join(dir, files[1]) {
		t.Fatalf("after change = %+v", segs)
	}
	// Idempotent.
	rep, _ = idx.Reconcile(ctx, root)
	if rep.Added != 0 || rep.Removed != 0 {
		t.Fatalf("third reconcile = %+v", rep)
	}
}

// --- synthetic fMP4 builder (the lever for testing the duration parser) ---

type fragment struct {
	samples     int
	explicitDur uint32 // per-sample duration written in trun; 0 = rely on trex default
}

func box(typ string, payload ...[]byte) []byte {
	size := 8
	for _, p := range payload {
		size += len(p)
	}
	out := []byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
	out = append(out, typ...)
	for _, p := range payload {
		out = append(out, p...)
	}
	return out
}

func u32(v uint32) []byte { return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }

func buildFMP4(t *testing.T, timescale, defaultDur uint32, frags []fragment) []byte {
	t.Helper()
	// mdhd v0: version/flags(4) ctime(4) mtime(4) timescale(4) duration(4) lang(2) pre(2)
	mdhd := box("mdhd", u32(0), u32(0), u32(0), u32(timescale), u32(0), []byte{0, 0, 0, 0})
	mdia := box("mdia", mdhd)
	tkhd := box("tkhd", u32(0), make([]byte, 80))
	trak := box("trak", tkhd, mdia)
	// trex: version/flags, track_ID, default_sample_description_index, default_sample_duration, size, flags
	trex := box("trex", u32(0), u32(1), u32(1), u32(defaultDur), u32(0), u32(0))
	mvex := box("mvex", trex)
	moov := box("moov", box("mvhd", u32(0), make([]byte, 96)), trak, mvex)
	out := box("ftyp", []byte("iso5"), u32(0), []byte("iso5"))
	out = append(out, moov...)
	for _, f := range frags {
		tfhd := box("tfhd", u32(0x020000), u32(1)) // default-base-is-moof, track 1
		var trun []byte
		if f.explicitDur != 0 {
			// flags: data-offset-present (0x1) | sample-duration-present (0x100)
			trun = append(trun, u32(0x101)...)
			trun = append(trun, u32(uint32(f.samples))...)
			trun = append(trun, u32(0)...)
			for range f.samples {
				trun = append(trun, u32(f.explicitDur)...)
			}
		} else {
			trun = append(trun, u32(0x1)...)
			trun = append(trun, u32(uint32(f.samples))...)
			trun = append(trun, u32(0)...)
		}
		moof := box("moof", box("mfhd", u32(0), u32(1)), box("traf", tfhd, box("trun", trun)))
		out = append(out, moof...)
		out = append(out, box("mdat", make([]byte, 16))...)
	}
	return out
}
