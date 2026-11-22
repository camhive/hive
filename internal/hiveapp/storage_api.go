package hiveapp

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/diskhealth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/retention"
)

// StorageRules is the running retention sweeper as the storage overview
// needs it (#150, ADR-0024): its hive-wide limits and the footage it keeps.
type StorageRules interface {
	Limits() retention.Limits
	SetLimits(retention.Limits) error
	Footage(ctx context.Context) ([]retention.CameraUse, error)
	DeletingForSpace() bool
	RetentionCap() int
}

// StorageControl is the owner's view of the archive disk and handle on
// retention_days and min_free_gb: hive.yaml for the next start, the
// running sweeper for now.
type StorageControl struct {
	Paths          hiveconfig.Paths // hive.yaml
	ArchiveDir     string
	Rules          StorageRules
	ExportMaxBytes int64                                    // export_max_gb, the exports' share of the disk
	Disk           func(dir string) (retention.Disk, error) // nil: retention.DiskOf
	Now            func() time.Time                         // nil: time.Now
	Health         func() diskhealth.Report                 // the archive disk's last check (#155); nil: not watched

	mu sync.Mutex // one change of hive.yaml and the sweeper at a time
}

// storageView is GET /v0/storage for the owner: every camera, the disk,
// the settings and the forecast.
type storageView struct {
	Cameras  []cameraStorage `json:"cameras"`
	Disk     diskView        `json:"disk"`
	Settings storageSettings `json:"settings"`
	Forecast forecastView    `json:"forecast"`
}

// memberStorageView is GET /v0/storage for a member: their cameras' size
// and days held, nothing of the hive as a whole.
type memberStorageView struct {
	Cameras []memberCamera `json:"cameras"`
}

type memberCamera struct {
	ID       string  `json:"id"`
	Name     string  `json:"name,omitempty"` // absent for a removed camera's footage
	Bytes    int64   `json:"bytes"`
	DaysHeld float64 `json:"days_held"`
}

type cameraStorage struct {
	memberCamera
	Oldest         time.Time `json:"oldest"`
	Newest         time.Time `json:"newest"`
	BytesPerDay24h int64     `json:"bytes_per_day_24h"`
	BytesPerDay7d  int64     `json:"bytes_per_day_7d"`
	OldestAgeDays  float64   `json:"oldest_age_days"`
	RetentionDays  int       `json:"retention_days"`
	FollowsHive    bool      `json:"follows_hive"`
	EffectiveDays  int       `json:"effective_days"`
	exists         bool      // the camera is still on the hive
}

type diskView struct {
	TotalBytes   int64           `json:"total_bytes"`
	ArchiveBytes int64           `json:"archive_bytes"`
	ExportBytes  int64           `json:"export_bytes"`
	FreeBytes    int64           `json:"free_bytes"`
	Health       *diskHealthView `json:"health,omitempty"`
}

// diskHealthView is the archive disk's last health check (#155).
type diskHealthView struct {
	State        diskhealth.State      `json:"state"`
	CheckedAt    *time.Time            `json:"checked_at,omitempty"` // absent before the first check
	Device       string                `json:"device,omitempty"`
	TemperatureC *int                  `json:"temperature_c,omitempty"`
	Evidence     []diskhealth.Evidence `json:"evidence"`
}

func healthView(health func() diskhealth.Report) *diskHealthView {
	if health == nil {
		return nil
	}
	r := health()
	v := &diskHealthView{State: r.State, Device: r.Device, TemperatureC: r.TemperatureC, Evidence: r.Evidence}
	if !r.CheckedAt.IsZero() {
		at := r.CheckedAt.UTC()
		v.CheckedAt = &at
	}
	if v.Evidence == nil {
		v.Evidence = []diskhealth.Evidence{}
	}
	return v
}

type storageSettings struct {
	RetentionDays int `json:"retention_days"`
	MinFreeGB     int `json:"min_free_gb"`
	ExportMaxGB   int `json:"export_max_gb"`
	// RetentionDaysMax is the licence's cap on every retention; absent when none.
	RetentionDaysMax int `json:"retention_days_max,omitempty"`
}

type forecastView struct {
	BytesPerDay   int64  `json:"bytes_per_day"`
	CapacityBytes int64  `json:"capacity_bytes"`
	DiskDays      int    `json:"disk_days"`
	HorizonDays   *int   `json:"horizon_days,omitempty"`
	NeededBytes   *int64 `json:"needed_bytes,omitempty"`
	FillsInDays   *int   `json:"fills_in_days,omitempty"`
	RetentionMet  bool   `json:"retention_met"`
	// DeletingForSpace: the last sweep deleted for space or missed the floor.
	DeletingForSpace bool `json:"deleting_for_space"`
}

// storagePatch is PATCH /v0/hive/storage; an absent field keeps its value.
type storagePatch struct {
	RetentionDays *int `json:"retention_days"`
	MinFreeGB     *int `json:"min_free_gb"`
}

func (a *api) storageControl(w http.ResponseWriter) *StorageControl {
	if a.d.Storage == nil || a.d.Storage.Rules == nil || a.d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "the archive is not available yet")
		return nil
	}
	return a.d.Storage
}

func (a *api) getStorage(w http.ResponseWriter, r *http.Request) {
	if s := a.storageControl(w); s != nil {
		a.writeStorage(w, r, s)
	}
}

func (a *api) patchStorage(w http.ResponseWriter, r *http.Request) {
	if c := claimsFrom(r); c.Role != auth.RoleOwner || c.Local {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner, signed in through Camhive, can change the storage settings")
		return
	}
	s := a.storageControl(w)
	if s == nil {
		return
	}
	var in storagePatch
	if !decodeJSON(w, r, &in) {
		return
	}
	disk, err := s.disk()
	if err != nil {
		a.internal(w, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.Rules.Limits()
	next := in.apply(old)
	if msg := in.validate(disk); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid", msg)
		return
	}
	if err := s.store(next); err != nil {
		a.d.Logger.Error("storage settings", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "the storage settings could not be stored")
		return
	}
	a.record(r, "storage.settings_changed", "hive", map[string]any{"retention_days": next.RetentionDays, "min_free_gb": next.MinFreeGB,
		"old_retention_days": old.DefaultDays, "old_min_free_gb": old.MinFreeBytes >> 30})
	a.writeStorage(w, r, s)
}

// apply puts the patch's fields over the running limits.
func (in storagePatch) apply(old retention.Limits) hiveconfig.StorageSettings {
	next := hiveconfig.StorageSettings{RetentionDays: old.DefaultDays, MinFreeGB: int(old.MinFreeBytes >> 30)}
	if in.RetentionDays != nil {
		next.RetentionDays = *in.RetentionDays
	}
	if in.MinFreeGB != nil {
		next.MinFreeGB = *in.MinFreeGB
	}
	return next
}

// store writes the settings into hive.yaml, then into the running sweeper.
func (s *StorageControl) store(next hiveconfig.StorageSettings) error {
	if err := hiveconfig.SaveStorageSettings(s.Paths, next); err != nil {
		return err
	}
	return s.Rules.SetLimits(retention.Limits{DefaultDays: next.RetentionDays, MinFreeBytes: int64(next.MinFreeGB) << 30})
}

// validate holds the rules the app repeats for the fields sent: retention
// as a camera's own (0 = until space runs out), a floor of at least 1 GiB
// and below the disk's size. A floor of 0 set by hand in hive.yaml (the
// space rule off) stands while the patch does not send one.
func (in storagePatch) validate(disk retention.Disk) string {
	switch {
	case in.RetentionDays != nil && (*in.RetentionDays < 0 || *in.RetentionDays > camera.MaxRetentionDays):
		return "retention_days must be between 0 and 3650"
	case in.MinFreeGB != nil && *in.MinFreeGB < 1:
		return "min_free_gb must be at least 1"
	case in.MinFreeGB != nil && int64(*in.MinFreeGB)<<30 >= disk.Total:
		return "min_free_gb must be smaller than the disk"
	}
	return ""
}

func (s *StorageControl) disk() (retention.Disk, error) {
	if s.Disk != nil {
		return s.Disk(s.ArchiveDir)
	}
	return retention.DiskOf(s.ArchiveDir)
}

func (s *StorageControl) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (a *api) writeStorage(w http.ResponseWriter, r *http.Request, s *StorageControl) {
	view, err := a.storage(r.Context(), s)
	if err != nil {
		a.internal(w, err)
		return
	}
	if c := claimsFrom(r); c.Role != auth.RoleOwner {
		writeJSON(w, http.StatusOK, view.forMember(c))
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// forMember keeps the cameras that still exist and the token covers, with
// their size and days held.
func (v storageView) forMember(c auth.Claims) memberStorageView {
	out := memberStorageView{Cameras: []memberCamera{}}
	for _, cam := range v.Cameras {
		if cam.exists && c.Allows(cam.ID, auth.ActionLive) {
			out.Cameras = append(out.Cameras, cam.memberCamera)
		}
	}
	return out
}

// storage gathers the figures and runs the forecast over every camera.
func (a *api) storage(ctx context.Context, s *StorageControl) (storageView, error) {
	in, err := a.forecastInput(ctx, s)
	if err != nil {
		return storageView{}, err
	}
	names, err := a.cameraNames(ctx)
	if err != nil {
		return storageView{}, err
	}
	out := retention.Forecast(in)
	view := storageView{Cameras: make([]cameraStorage, len(out.Cameras))}
	for i, o := range out.Cameras {
		name, exists := names[o.Camera]
		view.Cameras[i] = cameraStorage{memberCamera: memberCamera{ID: o.Camera, Name: name, Bytes: o.Bytes, DaysHeld: o.DaysHeld},
			Oldest: o.Oldest, Newest: o.Newest, BytesPerDay24h: o.BytesPerDay24h, BytesPerDay7d: o.BytesPerDay7d, OldestAgeDays: o.OldestAgeDays,
			RetentionDays: o.RetentionDays, FollowsHive: !o.OwnRetention, EffectiveDays: o.EffectiveDays, exists: exists}
	}
	limits := s.Rules.Limits()
	view.Disk = diskView{TotalBytes: in.Disk.Total, ArchiveBytes: in.ArchiveBytes, ExportBytes: in.ExportBytes, FreeBytes: in.Disk.Free, Health: healthView(s.Health)}
	view.Settings = storageSettings{RetentionDays: limits.DefaultDays, MinFreeGB: int(limits.MinFreeBytes >> 30), ExportMaxGB: int(s.ExportMaxBytes >> 30),
		RetentionDaysMax: s.Rules.RetentionCap()}
	view.Forecast = forecastView{BytesPerDay: out.BytesPerDay, CapacityBytes: out.CapacityBytes, DiskDays: out.DiskDays, HorizonDays: out.HorizonDays,
		NeededBytes: out.NeededBytes, FillsInDays: out.FillsInDays, RetentionMet: out.RetentionMet, DeletingForSpace: s.Rules.DeletingForSpace()}
	return view, nil
}

// forecastInput reads the footage, the disk and the exports.
func (a *api) forecastInput(ctx context.Context, s *StorageControl) (retention.ForecastInput, error) {
	footage, err := s.Rules.Footage(ctx)
	if err != nil {
		return retention.ForecastInput{}, err
	}
	disk, err := s.disk()
	if err != nil {
		return retention.ForecastInput{}, err
	}
	exports, err := a.d.Store.Exports().All(ctx)
	if err != nil {
		return retention.ForecastInput{}, err
	}
	in := retention.ForecastInput{Now: s.now(), Cameras: footage, Disk: disk, MinFreeBytes: s.Rules.Limits().MinFreeBytes, ExportMaxBytes: s.ExportMaxBytes}
	for _, f := range footage {
		in.ArchiveBytes += f.Bytes
	}
	for _, e := range exports {
		in.ExportBytes += e.Bytes
	}
	return in, nil
}

// cameraNames maps the cameras that exist to their names.
func (a *api) cameraNames(ctx context.Context) (map[string]string, error) {
	cams, err := a.d.Store.Cameras().List(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(cams))
	for _, c := range cams {
		names[string(c.ID)] = c.Name
	}
	return names, nil
}
