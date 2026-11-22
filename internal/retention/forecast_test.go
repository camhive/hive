package retention_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/retention"
)

const gb = int64(1_000_000_000)

var now = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

// footage is a camera that has recorded rate bytes per day for days days
// up to now, as the index sums it.
func footage(id string, rate int64, days float64, keep int) retention.CameraUse {
	held := time.Duration(days * float64(24*time.Hour))
	in7 := min(days, 7)
	in24 := min(days, 1)
	ago := func(d float64) time.Time { return now.Add(-time.Duration(d * float64(24*time.Hour))) }
	return retention.CameraUse{Camera: id, Bytes: int64(float64(rate) * days), Oldest: now.Add(-held), Newest: now,
		Bytes24h: int64(float64(rate) * in24), Bytes7d: int64(float64(rate) * in7), First24h: ago(in24), First7d: ago(in7), RetentionDays: keep}
}

// The issue's case: 46 GB per day, 30 days retention, a disk that gives
// 11 days. Retention is effectively 11, and 30 days would need 1.38 TB plus
// the reserve.
func TestForecastWhenTheDiskCannotMeetRetention(t *testing.T) {
	cams := []retention.CameraUse{footage("cam_a", 30*gb, 5, 30), footage("cam_b", 16*gb, 5, 30)}
	in := retention.ForecastInput{Now: now, Cameras: cams,
		Disk:         retention.Disk{Total: 1000 * gb, Free: 300 * gb},
		ArchiveBytes: 230 * gb, ExportBytes: 2 * gb, MinFreeBytes: 10 << 30, ExportMaxBytes: 10 << 30}
	f := retention.Forecast(in)

	if f.BytesPerDay != 46*gb {
		t.Fatalf("bytes per day = %d", f.BytesPerDay)
	}
	capacity := 230*gb + 2*gb + 300*gb - 20<<30
	if f.CapacityBytes != capacity {
		t.Fatalf("capacity = %d, want %d", f.CapacityBytes, capacity)
	}
	if f.DiskDays != 11 || f.HorizonDays == nil || *f.HorizonDays != 11 || f.RetentionMet {
		t.Fatalf("disk days %d, horizon %v, met %v; want 11, 11, false", f.DiskDays, f.HorizonDays, f.RetentionMet)
	}
	if want := 30*46*gb + 20<<30; f.NeededBytes == nil || *f.NeededBytes != want {
		t.Fatalf("needed = %v, want %d", f.NeededBytes, want)
	}
	if f.FillsInDays == nil || *f.FillsInDays != 6 { // (300 + 2 - 21.47) GB / 46 GB
		t.Fatalf("fills in = %v, want 6", f.FillsInDays)
	}
	for _, c := range f.Cameras {
		if c.EffectiveDays != 11 {
			t.Errorf("%s holds %d days, want 11", c.Camera, c.EffectiveDays)
		}
	}
}

// A camera with a short retention of its own leaves room for the others:
// the space rule deletes the oldest footage of any camera, so the others
// reach further back than disk/rate.
func TestForecastWithACameraOnItsOwnShortRetention(t *testing.T) {
	cams := []retention.CameraUse{footage("cam_a", 40*gb, 3, 2), footage("cam_b", 10*gb, 3, 30)}
	in := retention.ForecastInput{Now: now, Cameras: cams, Disk: retention.Disk{Total: 300 * gb, Free: 150 * gb}, ArchiveBytes: 150 * gb}
	f := retention.Forecast(in)

	// 300 GB: cam_a keeps 2 days (80 GB), cam_b gets the remaining 220 GB = 22 days.
	if f.DiskDays != 6 || f.HorizonDays == nil || *f.HorizonDays != 22 || f.RetentionMet {
		t.Fatalf("disk days %d, horizon %v, met %v; want 6, 22, false", f.DiskDays, f.HorizonDays, f.RetentionMet)
	}
	if f.Cameras[0].EffectiveDays != 2 || f.Cameras[1].EffectiveDays != 22 {
		t.Fatalf("effective = %d, %d; want 2, 22", f.Cameras[0].EffectiveDays, f.Cameras[1].EffectiveDays)
	}
}

func TestForecastWhenRetentionFits(t *testing.T) {
	cams := []retention.CameraUse{footage("cam_a", 10*gb, 10, 30)}
	in := retention.ForecastInput{Now: now, Cameras: cams, Disk: retention.Disk{Total: 2000 * gb, Free: 1500 * gb}, ArchiveBytes: 100 * gb,
		MinFreeBytes: 10 << 30, ExportMaxBytes: 10 << 30}
	f := retention.Forecast(in)
	if !f.RetentionMet || f.HorizonDays != nil || f.FillsInDays != nil || f.Cameras[0].EffectiveDays != 30 {
		t.Fatalf("met %v, horizon %v, fills %v, effective %d", f.RetentionMet, f.HorizonDays, f.FillsInDays, f.Cameras[0].EffectiveDays)
	}
	if f.DiskDays != 157 { // (1600 GB - 21.47 GB) / 10 GB
		t.Fatalf("disk days = %d", f.DiskDays)
	}
}

// An unlimited camera keeps footage until space runs out: retention is
// never met and no number of bytes is enough.
func TestForecastWithUnlimitedRetention(t *testing.T) {
	cams := []retention.CameraUse{footage("cam_a", 10*gb, 10, 0)}
	f := retention.Forecast(retention.ForecastInput{Now: now, Cameras: cams, Disk: retention.Disk{Total: 500 * gb, Free: 400 * gb}, ArchiveBytes: 100 * gb})
	if f.RetentionMet || f.NeededBytes != nil || f.HorizonDays == nil || *f.HorizonDays != 50 || f.Cameras[0].EffectiveDays != 50 {
		t.Fatalf("met %v, needed %v, horizon %v, effective %d", f.RetentionMet, f.NeededBytes, f.HorizonDays, f.Cameras[0].EffectiveDays)
	}
}

// The rate is the larger of the last day's and the last week's, rounded up: a bitrate
// raised yesterday shows at once, and the forecast never flatters.
func TestForecastRateIsThePessimisticWindow(t *testing.T) {
	raised := retention.CameraUse{Camera: "cam_a", Bytes: 6*10*gb + 40*gb, Oldest: now.Add(-7 * 24 * time.Hour), Newest: now,
		Bytes24h: 40 * gb, Bytes7d: 6*10*gb + 40*gb, First24h: now.Add(-24 * time.Hour), First7d: now.Add(-7 * 24 * time.Hour), RetentionDays: 30}
	f := retention.Forecast(retention.ForecastInput{Now: now, Cameras: []retention.CameraUse{raised}, Disk: retention.Disk{Total: 1000 * gb, Free: 500 * gb}})
	if f.Cameras[0].BytesPerDay24h != 40*gb || f.Cameras[0].BytesPerDay7d != (100*gb+6)/7 || f.BytesPerDay != 40*gb {
		t.Fatalf("24h %d, 7d %d, used %d", f.Cameras[0].BytesPerDay24h, f.Cameras[0].BytesPerDay7d, f.BytesPerDay)
	}
	// A camera recording for 2 hours has its rate over those 2 hours, not over a day.
	young := retention.CameraUse{Camera: "cam_b", Bytes: gb, Oldest: now.Add(-2 * time.Hour), Newest: now, Bytes24h: gb, Bytes7d: gb,
		First24h: now.Add(-2 * time.Hour), First7d: now.Add(-2 * time.Hour), RetentionDays: 30}
	f = retention.Forecast(retention.ForecastInput{Now: now, Cameras: []retention.CameraUse{young}, Disk: retention.Disk{Total: 1000 * gb, Free: 500 * gb}})
	if f.BytesPerDay != 12*gb {
		t.Fatalf("young camera rate = %d, want 12 GB", f.BytesPerDay)
	}
	if held := f.Cameras[0].DaysHeld; held < 0.083 || held > 0.084 {
		t.Fatalf("days held = %v", held)
	}
}

// Nothing recorded yet: no rate, no days.
func TestForecastWithoutFootage(t *testing.T) {
	f := retention.Forecast(retention.ForecastInput{Now: now, Disk: retention.Disk{Total: 1000 * gb, Free: 900 * gb}})
	if f.BytesPerDay != 0 || f.DiskDays != 0 || f.HorizonDays != nil || f.FillsInDays != nil || !f.RetentionMet {
		t.Fatalf("%+v", f)
	}
}

// #150: the owner changes the hive-wide retention and the floor in the
// app; the next sweep applies them without a restart.
func TestSetLimitsAppliesAtTheNextSweep(t *testing.T) {
	f := newFixture(t)
	old := f.add(t, "cam_a", 10*24*time.Hour, true)
	s := f.sweeper(retention.Policy{DefaultDays: 30, MinFreeBytes: 1})
	if got := s.Limits(); got != (retention.Limits{DefaultDays: 30, MinFreeBytes: 1}) {
		t.Fatalf("limits = %+v", got)
	}
	if rep, _ := s.Sweep(context.Background()); rep.ByAge != 0 {
		t.Fatalf("30 days deleted %d", rep.ByAge)
	}
	if err := s.SetLimits(retention.Limits{DefaultDays: 7, MinFreeBytes: 2}); err != nil {
		t.Fatal(err)
	}
	if rep, _ := s.Sweep(context.Background()); rep.ByAge != 1 || contains(f.fs.paths(), old.Path) {
		t.Fatalf("7 days: %+v, files %v", rep, f.fs.paths())
	}
	if err := s.SetLimits(retention.Limits{DefaultDays: -1}); !errors.Is(err, retention.ErrInvalidPolicy) {
		t.Fatalf("negative days: %v", err)
	}
	if got := s.Limits(); got != (retention.Limits{DefaultDays: 7, MinFreeBytes: 2}) {
		t.Fatalf("a refused change was kept: %+v", got)
	}
}

// The forecast's footage comes from the index, both streams together, with
// the retention the sweeper applies to each camera.
func TestFootageSumsTheIndexPerCamera(t *testing.T) {
	f := newFixture(t)
	f.ownRetention(t, "cam_b", 60)
	f.add(t, "cam_a", 3*24*time.Hour, true)
	f.add(t, "cam_a", 12*time.Hour, true)
	sub := archive.Segment{Camera: "cam_a", Stream: "sub", Start: f.now.Add(-time.Hour), Duration: 10 * time.Minute, Bytes: 20, Path: "/archive/cam_a/sub/x.mp4", Complete: false}
	if err := f.idx.Upsert(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
	f.add(t, "cam_b", 8*24*time.Hour, true)
	s := f.sweeper(retention.Policy{DefaultDays: 30, CameraDays: f.st.Cameras().RetentionDays, MaxDays: func() int { return 45 }})
	got, err := s.Footage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []retention.CameraUse{
		{Camera: "cam_a", Bytes: 220, Oldest: f.now.Add(-3 * 24 * time.Hour), Newest: f.now.Add(-50 * time.Minute), Bytes24h: 120, Bytes7d: 220,
			First24h: f.now.Add(-12 * time.Hour), First7d: f.now.Add(-3 * 24 * time.Hour), RetentionDays: 30},
		{Camera: "cam_b", Bytes: 100, Oldest: f.now.Add(-8 * 24 * time.Hour), Newest: f.now.Add(-8*24*time.Hour + 10*time.Minute), RetentionDays: 45, OwnRetention: true},
	}
	if len(got) != len(want) {
		t.Fatalf("footage = %+v", got)
	}
	for i := range want {
		g := got[i]
		if g.Camera != want[i].Camera || g.Bytes != want[i].Bytes || !g.Oldest.Equal(want[i].Oldest) || !g.Newest.Equal(want[i].Newest) ||
			g.Bytes24h != want[i].Bytes24h || g.Bytes7d != want[i].Bytes7d || g.RetentionDays != want[i].RetentionDays ||
			!g.First24h.Equal(want[i].First24h) || !g.First7d.Equal(want[i].First7d) || g.OwnRetention != want[i].OwnRetention {
			t.Errorf("camera %d = %+v, want %+v", i, g, want[i])
		}
	}
}

func TestDiskOfTheRealVolume(t *testing.T) {
	d, err := retention.DiskOf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	free, _ := retention.FreeSpace(t.TempDir())
	if d.Total <= 0 || d.Free <= 0 || d.Free > d.Total || free <= 0 {
		t.Fatalf("disk = %+v, free space = %d", d, free)
	}
}

// Retention that fills the capacity exactly is met: the boundary belongs
// to "fits".
func TestForecastExactFitIsMet(t *testing.T) {
	cams := []retention.CameraUse{footage("cam_a", 10*gb, 10, 30)}
	f := retention.Forecast(retention.ForecastInput{Now: now, Cameras: cams, Disk: retention.Disk{Total: 1000 * gb, Free: 200 * gb}, ArchiveBytes: 100 * gb})
	if !f.RetentionMet || f.HorizonDays != nil || f.Cameras[0].EffectiveDays != 30 {
		t.Fatalf("met %v, horizon %v, effective %d", f.RetentionMet, f.HorizonDays, f.Cameras[0].EffectiveDays)
	}
}

// Three cameras with stepped retention: 10 days and 20 days fit, the
// unlimited one takes the rest. 600 GB = 10×10 + 20×10 + 30×10.
func TestForecastHorizonStepsThroughEachRetention(t *testing.T) {
	cams := []retention.CameraUse{footage("cam_a", 10*gb, 10, 10), footage("cam_b", 10*gb, 10, 20), footage("cam_c", 10*gb, 10, 0)}
	f := retention.Forecast(retention.ForecastInput{Now: now, Cameras: cams, Disk: retention.Disk{Total: 1000 * gb, Free: 300 * gb}, ArchiveBytes: 300 * gb})
	if f.HorizonDays == nil || *f.HorizonDays != 30 || f.RetentionMet {
		t.Fatalf("horizon %v, met %v; want 30, false", f.HorizonDays, f.RetentionMet)
	}
	if got := []int{f.Cameras[0].EffectiveDays, f.Cameras[1].EffectiveDays, f.Cameras[2].EffectiveDays}; got[0] != 10 || got[1] != 20 || got[2] != 30 {
		t.Fatalf("effective = %v", got)
	}
}

// A camera whose first segment just started has no rate yet, not an
// infinite one.
func TestForecastRateOfAFirstSegment(t *testing.T) {
	first := retention.CameraUse{Camera: "cam_a", Bytes: 5 * gb, Oldest: now, Newest: now, Bytes24h: 5 * gb, Bytes7d: 5 * gb, First24h: now, First7d: now, RetentionDays: 30}
	f := retention.Forecast(retention.ForecastInput{Now: now, Cameras: []retention.CameraUse{first}, Disk: retention.Disk{Total: 1000 * gb, Free: 500 * gb}})
	if f.BytesPerDay != 0 || f.Cameras[0].BytesPerDay24h != 0 {
		t.Fatalf("rate = %d", f.BytesPerDay)
	}
}

// A camera that resumed 2 hours ago after 3 days off writes at its real
// rate: the window starts at its first segment inside it, not at its
// oldest footage ever.
func TestForecastRateAfterAGap(t *testing.T) {
	resumed := retention.CameraUse{Camera: "cam_a", Bytes: 40 * gb, Oldest: now.Add(-5 * 24 * time.Hour), Newest: now,
		Bytes24h: gb, First24h: now.Add(-2 * time.Hour), Bytes7d: 25 * gb, First7d: now.Add(-5 * 24 * time.Hour), RetentionDays: 30}
	f := retention.Forecast(retention.ForecastInput{Now: now, Cameras: []retention.CameraUse{resumed}, Disk: retention.Disk{Total: 1000 * gb, Free: 404 * gb}, ArchiveBytes: 40 * gb})
	if f.Cameras[0].BytesPerDay24h != 12*gb || f.BytesPerDay != 12*gb || f.DiskDays != 37 {
		t.Fatalf("24h %d, used %d, disk days %d; want 12 GB, 12 GB, 37", f.Cameras[0].BytesPerDay24h, f.BytesPerDay, f.DiskDays)
	}
	if age := f.Cameras[0].OldestAgeDays; age != 5 {
		t.Fatalf("oldest age = %v", age)
	}
}

// Only cameras still growing toward their retention fill the disk: one at
// its 7 days deletes as much as it writes. 293 GB free last 293 days at the
// unlimited camera's 1 GB a day, not 2.9 days at 101 GB.
func TestForecastFillsInCountsOnlyNetGrowth(t *testing.T) {
	cams := []retention.CameraUse{footage("cam_a", 100*gb, 7, 7), footage("cam_b", gb, 7, 0)}
	f := retention.Forecast(retention.ForecastInput{Now: now, Cameras: cams, Disk: retention.Disk{Total: 2000 * gb, Free: 293 * gb}, ArchiveBytes: 707 * gb})
	if f.FillsInDays == nil || *f.FillsInDays != 293 {
		t.Fatalf("fills in %v, want 293", f.FillsInDays)
	}
	if f.HorizonDays == nil || *f.HorizonDays != 300 || f.Cameras[0].EffectiveDays != 7 || f.Cameras[1].EffectiveDays != 300 {
		t.Fatalf("horizon %v, effective %d %d", f.HorizonDays, f.Cameras[0].EffectiveDays, f.Cameras[1].EffectiveDays)
	}
}

// The space rule's state is the last sweep's: deleting while it deleted
// for space or could not reach the floor.
func TestSweeperReportsTheSpaceRule(t *testing.T) {
	f := newFixture(t)
	f.add(t, "cam_a", 2*time.Hour, true)
	f.add(t, "cam_a", time.Hour, true)
	f.fs.free = 50
	s := f.sweeper(retention.Policy{MinFreeBytes: 120})
	if s.DeletingForSpace() {
		t.Fatal("deleting before any sweep")
	}
	if _, err := s.Sweep(context.Background()); err != nil || !s.DeletingForSpace() {
		t.Fatalf("after deleting for space: %v %v", err, s.DeletingForSpace())
	}
	f.fs.free = 1 << 30
	if _, err := s.Sweep(context.Background()); err != nil || s.DeletingForSpace() {
		t.Fatalf("after a sweep with room: %v %v", err, s.DeletingForSpace())
	}
}

func TestSweeperReportsTheLicenceCap(t *testing.T) {
	f := newFixture(t)
	if c := f.sweeper(retention.Policy{}).RetentionCap(); c != 0 {
		t.Fatalf("no cap = %d", c)
	}
	if c := f.sweeper(retention.Policy{MaxDays: func() int { return -1 }}).RetentionCap(); c != 0 {
		t.Fatalf("unlimited cap = %d", c)
	}
	if c := f.sweeper(retention.Policy{MaxDays: func() int { return 14 }}).RetentionCap(); c != 14 {
		t.Fatalf("cap = %d", c)
	}
}
