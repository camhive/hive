package hiveapp_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/retention"
	"github.com/camhive/hive/internal/store"
)

const gigabyte = int64(1_000_000_000)

// storageFixture is a hive whose index holds 8 days of footage of two
// cameras in segments of an hour: Door writes 30 GB a day, Yard 18 GB. The
// disk has 1000 GB, 190 GB of it free; hive.yaml has the defaults
// (30 days, 10 GB floor, 10 GB exports).
type storageFixture struct {
	*apiFixture
	paths   hiveconfig.Paths
	sweeper *retention.Sweeper
	control *hiveapp.StorageControl
	door    camera.ID
	yard    camera.ID
	capDays int // the licence's archive.retention_days_max; 0: none
}

func newStorageFixture(t *testing.T) *storageFixture {
	t.Helper()
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	cfg, _, err := hiveconfig.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	sf := &storageFixture{paths: paths}
	sf.apiFixture = newAPIWith(t, func(d *hiveapp.Deps) {
		sf.door, sf.yard = putCamera(t, d.Store, "Door"), putCamera(t, d.Store, "Yard")
		for h := range 8 * 24 {
			start := now.Add(-time.Duration(h+1) * time.Hour) // back to back, as the engine records
			for _, s := range []struct {
				cam   camera.ID
				bytes int64
			}{{sf.door, 30 * gigabyte / 24}, {sf.yard, 18 * gigabyte / 24}} {
				row := store.SegmentRow{Camera: string(s.cam), Stream: "main", Start: start, Duration: time.Hour, Bytes: s.bytes, Path: "/a/" + string(s.cam) + start.Format(time.RFC3339), Complete: true}
				if err := d.Store.Segments().Upsert(context.Background(), row); err != nil {
					t.Fatal(err)
				}
			}
		}
		sf.sweeper = retention.New(archive.NewIndex(d.Store), d.Store.Segments(),
			retention.Policy{DefaultDays: cfg.RetentionDays, MinFreeBytes: int64(cfg.MinFreeGB) << 30, CameraDays: d.Store.Cameras().RetentionDays,
				MaxDays: func() int { return sf.capDays }},
			retention.Options{Now: clock})
		d.Storage = &hiveapp.StorageControl{Paths: paths, Rules: sf.sweeper, ExportMaxBytes: int64(cfg.ExportMaxGB) << 30, Now: clock,
			Disk: func(string) (retention.Disk, error) {
				return retention.Disk{Total: 1000 * gigabyte, Free: 190 * gigabyte}, nil
			}}
		sf.control = d.Storage
	})
	return sf
}

func putCamera(t *testing.T, st *store.Store, name string) camera.ID {
	t.Helper()
	c, err := camera.New(camera.Spec{Name: name, Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/" + name}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Cameras().Put(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// #150: the owner sees the disk, every camera's footage and rate, the
// settings and the forecast: 48 GB a day on this disk holds 11 days, so
// 30 days of retention is effectively 11.
func TestOwnerSeesStorageAndForecast(t *testing.T) {
	f := newStorageFixture(t)
	r := f.do(t, "GET", "/v0/storage", f.token(t, auth.RoleOwner, "*"), nil)
	if r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	archiveBytes := float64(8 * 48 * gigabyte)
	disk := r.Body["disk"].(map[string]any)
	if disk["total_bytes"] != float64(1000*gigabyte) || disk["free_bytes"] != float64(190*gigabyte) || disk["archive_bytes"] != archiveBytes || disk["export_bytes"] != float64(0) {
		t.Fatalf("disk = %v", disk)
	}
	settings := r.Body["settings"].(map[string]any)
	if settings["retention_days"] != float64(30) || settings["min_free_gb"] != float64(10) || settings["export_max_gb"] != float64(10) {
		t.Fatalf("settings = %v", settings)
	}
	if _, ok := settings["retention_days_max"]; ok {
		t.Fatalf("no licence cap, yet %v", settings)
	}
	fc := r.Body["forecast"].(map[string]any)
	if fc["bytes_per_day"] != float64(48*gigabyte) || fc["disk_days"] != float64(11) || fc["horizon_days"] != float64(11) || fc["retention_met"] != false || fc["deleting_for_space"] != false {
		t.Fatalf("forecast = %v", fc)
	}
	if fc["needed_bytes"] != float64(30*48*gigabyte+20<<30) {
		t.Fatalf("needed = %v", fc["needed_bytes"])
	}
	cams := r.Body["cameras"].([]any)
	if len(cams) != 2 {
		t.Fatalf("cameras = %v", cams)
	}
	for _, c := range cams {
		c := c.(map[string]any)
		want := map[string]float64{"Door": 30, "Yard": 18}[c["name"].(string)]
		if c["bytes_per_day_24h"] != want*float64(gigabyte) || c["bytes_per_day_7d"] != want*float64(gigabyte) || c["bytes"] != 8*want*float64(gigabyte) {
			t.Errorf("%v", c)
		}
		if held := c["days_held"].(float64); held != 8 || c["retention_days"] != float64(30) || c["effective_days"] != float64(11) {
			t.Errorf("%v", c)
		}
		if c["oldest"] != "2026-11-14T12:00:00Z" || c["newest"] != "2026-11-22T12:00:00Z" {
			t.Errorf("span %v - %v", c["oldest"], c["newest"])
		}
		if age := c["oldest_age_days"].(float64); age != 8 || c["follows_hive"] != true {
			t.Errorf("age %v, follows %v", c["oldest_age_days"], c["follows_hive"])
		}
	}
}

// Members see only the cameras their token covers, and nothing of the
// hive as a whole.
func TestMembersSeeOnlyTheirCamerasDays(t *testing.T) {
	f := newStorageFixture(t)
	r := f.do(t, "GET", "/v0/storage", f.token(t, auth.RoleViewer, string(f.yard)), nil)
	if r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	cams, _ := r.Body["cameras"].([]any)
	if len(cams) != 1 {
		t.Fatalf("cameras = %v", cams)
	}
	yard := cams[0].(map[string]any)
	if yard["name"] != "Yard" || yard["bytes"] != float64(8*18*gigabyte) || yard["days_held"] != float64(8) || len(yard) != 4 {
		t.Fatalf("a member gets id, name, bytes and days held only: %v", yard)
	}
	for _, k := range []string{"disk", "settings", "forecast"} {
		if _, ok := r.Body[k]; ok {
			t.Errorf("a member sees %s: %v", k, r.Body[k])
		}
	}
}

// The owner changes retention and the floor: hive.yaml and the running
// sweeper take them, the answer is the new forecast, the action log says
// what changed.
func TestOwnerChangesStorageSettings(t *testing.T) {
	f := newStorageFixture(t)
	owner := f.token(t, auth.RoleOwner, "*")
	r := f.do(t, "PATCH", "/v0/hive/storage", owner, map[string]any{"retention_days": 7, "min_free_gb": 25})
	if r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if s := r.Body["settings"].(map[string]any); s["retention_days"] != float64(7) || s["min_free_gb"] != float64(25) {
		t.Fatalf("settings = %v", s)
	}
	if fc := r.Body["forecast"].(map[string]any); fc["retention_met"] != true || fc["horizon_days"] != nil {
		t.Fatalf("7 days fit: %v", fc)
	}
	if got := f.sweeper.Limits(); got != (retention.Limits{DefaultDays: 7, MinFreeBytes: 25 << 30}) {
		t.Fatalf("sweeper limits = %+v", got)
	}
	if cfg, _, _ := hiveconfig.Load(f.paths); cfg.RetentionDays != 7 || cfg.MinFreeGB != 25 {
		t.Fatalf("hive.yaml: %d days, %d GB", cfg.RetentionDays, cfg.MinFreeGB)
	}
	// One field alone keeps the other.
	if r := f.do(t, "PATCH", "/v0/hive/storage", owner, map[string]any{"retention_days": 0}); r.Status != 200 || r.Body["settings"].(map[string]any)["min_free_gb"] != float64(25) {
		t.Fatalf("partial: %d %v", r.Status, r.Body)
	}
	a := f.lastAction(t)
	if a.Action != "storage.settings_changed" || !strings.Contains(string(a.Details), `"retention_days":0`) || !strings.Contains(string(a.Details), `"min_free_gb":25`) {
		t.Fatalf("action = %+v %s", a, a.Details)
	}
}

func TestStorageSettingsAreValidated(t *testing.T) {
	f := newStorageFixture(t)
	owner := f.token(t, auth.RoleOwner, "*")
	before, _ := os.ReadFile(f.paths.Config)
	// The disk has 1000 GB = 931.3 GiB: a floor of all of it would delete everything.
	for _, bad := range []map[string]any{{"retention_days": -1}, {"retention_days": camera.MaxRetentionDays + 1}, {"min_free_gb": 0}, {"min_free_gb": 932}} {
		if r := f.do(t, "PATCH", "/v0/hive/storage", owner, bad); r.Status != 422 || r.Body["code"] != "invalid" {
			t.Errorf("%v: %d %v", bad, r.Status, r.Body)
		}
	}
	for _, bad := range []any{map[string]any{"retention_days": "30"}, map[string]any{"min_free_gb": 1.5}, map[string]any{"export_max_gb": 5}} {
		if r := f.do(t, "PATCH", "/v0/hive/storage", owner, bad); r.Status != 400 || r.Body["code"] != "bad_json" {
			t.Errorf("%v: %d %v", bad, r.Status, r.Body)
		}
	}
	if after, _ := os.ReadFile(f.paths.Config); string(after) != string(before) {
		t.Fatalf("a refused change was written:\n%s", after)
	}
	if r := f.do(t, "PATCH", "/v0/hive/storage", owner, map[string]any{"min_free_gb": 931}); r.Status != 200 {
		t.Fatalf("931 GiB on a 931.3 GiB disk: %d %v", r.Status, r.Body)
	}
}

// Members cannot change the settings, nor can the owner's local emergency
// session (#57): it exists to reach recordings while the cloud is down.
func TestOnlyTheOwnerThroughCamhiveChangesStorage(t *testing.T) {
	f := newStorageFixture(t)
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleOperator} {
		if r := f.do(t, "PATCH", "/v0/hive/storage", f.token(t, role, "*"), map[string]any{"retention_days": 1}); r.Status != http.StatusForbidden {
			t.Errorf("%s: %d %v", role, r.Status, r.Body)
		}
	}
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if _, _, err := hiveconfig.Load(paths); err != nil {
		t.Fatal(err)
	}
	g := newAPIWith(t, func(d *hiveapp.Deps) {
		d.Local = auth.NewLocalSessions("hv_1", time.Now)
		d.Storage = &hiveapp.StorageControl{Paths: paths, Rules: f.sweeper, Disk: func(string) (retention.Disk, error) { return retention.Disk{Total: 1000 * gigabyte}, nil }}
	})
	lan := g.viaFrom(t, hiveapp.LANConn, lanPeer)
	if r := lan.do(t, "PUT", "/v0/local-login", g.token(t, auth.RoleOwner, "*"), map[string]string{"username": "anna", "password": localPassword}); r.Status != 200 {
		t.Fatalf("enable: %d %v", r.Status, r.Body)
	}
	local, _ := lan.do(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": localPassword}).Body["token"].(string)
	if r := lan.do(t, "PATCH", "/v0/hive/storage", local, map[string]any{"retention_days": 1}); r.Status != http.StatusForbidden || r.Body["code"] != "forbidden" {
		t.Fatalf("local session: %d %v", r.Status, r.Body)
	}
	if r := lan.do(t, "GET", "/v0/storage", local, nil); r.Status != 200 {
		t.Fatalf("a local session reads the overview: %d %v", r.Status, r.Body)
	}
}

func TestStorageNotReadyWithoutTheArchive(t *testing.T) {
	f := newAPI(t)
	for _, c := range []struct{ method, path string }{{"GET", "/v0/storage"}, {"PATCH", "/v0/hive/storage"}} {
		if r := f.do(t, c.method, c.path, f.token(t, auth.RoleOwner, "*"), map[string]any{}); r.Status != http.StatusServiceUnavailable || r.Body["code"] != "not_ready" {
			t.Errorf("%s %s: %d %v", c.method, c.path, r.Status, r.Body)
		}
	}
}

// Under a licence cap below the setting the owner sees the cap, and the
// cameras on the hive-wide value still count as following it.
func TestStorageShowsTheLicenceCap(t *testing.T) {
	f := newStorageFixture(t)
	f.capDays = 14
	r := f.do(t, "GET", "/v0/storage", f.token(t, auth.RoleOwner, "*"), nil)
	if s := r.Body["settings"].(map[string]any); s["retention_days"] != float64(30) || s["retention_days_max"] != float64(14) {
		t.Fatalf("settings = %v", s)
	}
	for _, c := range r.Body["cameras"].([]any) {
		if c := c.(map[string]any); c["retention_days"] != float64(14) || c["follows_hive"] != true {
			t.Errorf("%v", c)
		}
	}
}

// A floor of 0 set by hand in hive.yaml (the space rule off) stands when the
// owner changes only the retention in the app; only a sent field is checked.
func TestHandSetZeroFloorStands(t *testing.T) {
	f := newStorageFixture(t)
	if err := hiveconfig.SaveStorageSettings(f.paths, hiveconfig.StorageSettings{RetentionDays: 30, MinFreeGB: 0}); err != nil {
		t.Fatal(err)
	}
	if err := f.sweeper.SetLimits(retention.Limits{DefaultDays: 30}); err != nil {
		t.Fatal(err)
	}
	owner := f.token(t, auth.RoleOwner, "*")
	r := f.do(t, "PATCH", "/v0/hive/storage", owner, map[string]any{"retention_days": 7})
	if r.Status != 200 || r.Body["settings"].(map[string]any)["min_free_gb"] != float64(0) {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if cfg, _, _ := hiveconfig.Load(f.paths); cfg.RetentionDays != 7 || cfg.MinFreeGB != 0 {
		t.Fatalf("hive.yaml: %d days, %d GB", cfg.RetentionDays, cfg.MinFreeGB)
	}
	if r := f.do(t, "PATCH", "/v0/hive/storage", owner, map[string]any{"min_free_gb": 0}); r.Status != 422 {
		t.Fatalf("a floor of 0 from the app: %d %v", r.Status, r.Body)
	}
}
