package retention

import (
	"context"
	"math"
	"sort"
	"time"
)

// The storage arithmetic of ADR-0024 §1, on measured bytes. The website's
// calculator (web/site/src/lib/sizing.ts) does the same on presets; both
// follow docs/dev/sizing.md ("The storage arithmetic"). Days round down,
// rates and bytes needed round up: the forecast never flatters.

const day = 24 * time.Hour

// Disk is the archive's volume: its size and the bytes available to the
// hive (Bavail, as the space rule measures it).
type Disk struct {
	Total int64
	Free  int64
}

// CameraUse is one camera's footage as the index sums it, with the
// retention the sweeper applies to it (own or hive-wide, licence cap;
// 0 = unlimited).
type CameraUse struct {
	Camera   string
	Bytes    int64
	Oldest   time.Time // start of the oldest segment
	Newest   time.Time // end of the newest segment
	Bytes24h int64     // bytes of segments that started in the last 24 hours
	Bytes7d  int64     // and in the last 7 days
	// First24h and First7d are the starts of the first segment inside each
	// window (zero when none): a rate covers the time since then, so a
	// camera that resumed after a gap is not diluted over the gap.
	First24h      time.Time
	First7d       time.Time
	RetentionDays int
	OwnRetention  bool // the camera has its own retention (#61), not the hive-wide one
}

// ForecastInput is what the forecast needs: the cameras' footage, the disk
// and the space the hive keeps off the archive.
type ForecastInput struct {
	Now            time.Time
	Cameras        []CameraUse
	Disk           Disk
	ArchiveBytes   int64
	ExportBytes    int64
	MinFreeBytes   int64 // min_free_gb
	ExportMaxBytes int64 // export_max_gb
}

// CameraOutlook is one camera's figures and the days it will hold.
type CameraOutlook struct {
	CameraUse
	BytesPerDay24h int64
	BytesPerDay7d  int64
	DaysHeld       float64 // newest minus oldest, in days
	// OldestAgeDays is now minus the oldest segment's start: what the age
	// rule compares with the retention.
	OldestAgeDays float64
	// EffectiveDays is what the camera will hold once the settings have
	// done their work: its retention, or less when the disk runs out first.
	EffectiveDays int
}

// Outlook is the forecast for the whole archive.
type Outlook struct {
	BytesPerDay   int64 // every camera's rate, the larger window each
	CapacityBytes int64 // what the archive may fill
	DiskDays      int   // days the capacity holds at BytesPerDay, every camera alike
	// HorizonDays is how far back the space rule lets footage reach when
	// retention cannot be met; nil when it can.
	HorizonDays *int
	// NeededBytes is the space retention needs (footage plus the reserve);
	// nil when a recording camera keeps footage without limit.
	NeededBytes *int64
	// FillsInDays is when the space rule starts deleting: the free space
	// over the growth of the cameras still short of their retention. Nil
	// when retention is met or the growth stops before the disk is full.
	FillsInDays  *int
	RetentionMet bool
	Cameras      []CameraOutlook
}

// Forecast applies the storage arithmetic to measured footage.
func Forecast(in ForecastInput) Outlook {
	reserve := in.MinFreeBytes + in.ExportMaxBytes
	out := Outlook{CapacityBytes: max(0, in.ArchiveBytes+in.ExportBytes+in.Disk.Free-reserve), Cameras: make([]CameraOutlook, len(in.Cameras))}
	for i, c := range in.Cameras {
		out.Cameras[i] = outlook(c, in.Now)
		out.BytesPerDay += out.Cameras[i].rate()
	}
	if out.BytesPerDay > 0 {
		out.DiskDays = int(out.CapacityBytes / out.BytesPerDay)
	}
	h, met := waterline(out.Cameras, float64(out.CapacityBytes), limit)
	horizon := int(math.Floor(h))
	out.RetentionMet = met
	if needed, ok := neededBytes(out.Cameras); ok {
		needed += reserve
		out.NeededBytes = &needed
	}
	for i := range out.Cameras {
		c := &out.Cameras[i]
		c.EffectiveDays = c.RetentionDays
		if !met && (c.RetentionDays == 0 || c.RetentionDays > horizon) {
			c.EffectiveDays = horizon
		}
	}
	if !met {
		out.HorizonDays = &horizon
		out.FillsInDays = fillsIn(out.Cameras, out.CapacityBytes-in.ArchiveBytes)
	}
	return out
}

// fillsIn is when the free part of the capacity is used up: each camera
// grows until its footage reaches its retention (an unlimited one without
// end), then deletes as much as it writes. Nil when the growth stops first.
func fillsIn(cams []CameraOutlook, free int64) *int {
	t, fits := waterline(cams, float64(max(0, free)), func(c CameraOutlook) float64 { return max(0, limit(c)-c.OldestAgeDays) })
	if fits {
		return nil
	}
	days := int(math.Floor(t))
	return &days
}

// outlook is one camera's rates, days held and age.
func outlook(c CameraUse, now time.Time) CameraOutlook {
	o := CameraOutlook{CameraUse: c, BytesPerDay24h: rate(c.Bytes24h, c.First24h, now), BytesPerDay7d: rate(c.Bytes7d, c.First7d, now)}
	if c.Newest.After(c.Oldest) {
		o.DaysHeld = float64(c.Newest.Sub(c.Oldest)) / float64(day)
	}
	if now.After(c.Oldest) {
		o.OldestAgeDays = float64(now.Sub(c.Oldest)) / float64(day)
	}
	return o
}

// rate is bytes per day over the time since the first segment inside the
// window, rounded up; 0 without footage or time in the window.
func rate(bytes int64, first, now time.Time) int64 {
	covered := now.Sub(first)
	if bytes <= 0 || first.IsZero() || covered <= 0 {
		return 0
	}
	return int64(math.Ceil(float64(bytes) * day.Seconds() / covered.Seconds()))
}

// rate is the camera's pessimistic rate: the larger window.
func (o CameraOutlook) rate() int64 { return max(o.BytesPerDay24h, o.BytesPerDay7d) }

// waterline finds T with Σ rate·min(days(c), T) = budget over the cameras
// that record: the depth the space rule converges to (days = retention) or
// the time until the disk is full (days = what a camera still grows).
// fits when every camera's days fit in the budget; days may be +Inf.
func waterline(cams []CameraOutlook, budget float64, days func(CameraOutlook) float64) (float64, bool) {
	order := make([]CameraOutlook, 0, len(cams))
	var active float64 // bytes per day of the cameras still growing at T
	for _, c := range cams {
		if r := c.rate(); r > 0 {
			order = append(order, c)
			active += float64(r)
		}
	}
	sort.Slice(order, func(a, b int) bool { return days(order[a]) < days(order[b]) })
	left, at := budget, 0.0
	for _, c := range order {
		d := days(c)
		if math.IsInf(d, 1) || active*(d-at) > left {
			break
		}
		left -= active * (d - at)
		at = d
		active -= float64(c.rate())
	}
	if active <= 0 {
		return 0, true
	}
	return max(0, at+left/active), false
}

// limit is a camera's retention, unlimited as +Inf.
func limit(c CameraOutlook) float64 {
	if c.RetentionDays == 0 {
		return math.Inf(1)
	}
	return float64(c.RetentionDays)
}

// neededBytes is the footage retention keeps at these rates; false when a
// recording camera keeps footage without limit.
func neededBytes(cams []CameraOutlook) (int64, bool) {
	var sum int64
	for _, c := range cams {
		r := c.rate()
		if r == 0 {
			continue
		}
		if c.RetentionDays == 0 {
			return 0, false
		}
		sum += r * int64(c.RetentionDays)
	}
	return sum, true
}

// Footage sums the index per camera with the retention this sweeper
// applies to each (own or hive-wide, licence cap): the forecast's input.
func (s *Sweeper) Footage(ctx context.Context) ([]CameraUse, error) {
	now := s.opts.Now()
	rows, err := s.repo.ByCamera(ctx, now.Add(-day), now.Add(-7*day))
	if err != nil {
		return nil, err
	}
	p := s.current()
	own, err := p.cameraDays(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CameraUse, len(rows))
	for i, r := range rows {
		_, mine := own[r.Camera]
		out[i] = CameraUse{Camera: r.Camera, Bytes: r.Bytes, Oldest: r.Oldest, Newest: r.Newest, Bytes24h: r.Since24h, Bytes7d: r.Since7d,
			First24h: r.First24h, First7d: r.First7d, RetentionDays: p.days(own, r.Camera), OwnRetention: mine}
	}
	return out, nil
}

// DeletingForSpace reports whether the last sweep deleted footage for
// space or could not reach the floor: the space rule is at work now.
func (s *Sweeper) DeletingForSpace() bool { return s.spaceActive.Load() }

// RetentionCap is the licence's longest retention (archive.retention_days_max),
// 0 when there is none.
func (s *Sweeper) RetentionCap() int {
	if s.policy.MaxDays == nil {
		return 0
	}
	return max(0, s.policy.MaxDays())
}
