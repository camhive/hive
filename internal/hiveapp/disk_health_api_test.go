package hiveapp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/datamove"
	"github.com/camhive/hive/internal/diskhealth"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/hiveconfig"
)

// #155: the owner's storage view says how the archive disk is, with the
// evidence; before the first check the state is unknown without a time.
func TestOwnerSeesTheDiskHealth(t *testing.T) {
	f := newStorageFixture(t)
	at := time.Date(2026, 11, 22, 11, 30, 0, 0, time.UTC)
	report := diskhealth.Report{State: diskhealth.Unknown}
	f.control.Health = func() diskhealth.Report { return report }
	r := f.do(t, "GET", "/v0/storage", f.token(t, auth.RoleOwner, "*"), nil)
	health, _ := r.Body["disk"].(map[string]any)["health"].(map[string]any)
	if health["state"] != "unknown" || health["checked_at"] != nil {
		t.Fatalf("health before a check = %v, want unknown without checked_at", health)
	}
	report = diskhealth.Report{State: diskhealth.Failing, CheckedAt: at, Device: "/dev/sdb", Evidence: []diskhealth.Evidence{
		{Signal: diskhealth.SignalSMART, Detail: "/dev/sdb: pending sectors", Values: map[string]int64{"pending_sectors": 8}}}}
	r = f.do(t, "GET", "/v0/storage", f.token(t, auth.RoleOwner, "*"), nil)
	health, _ = r.Body["disk"].(map[string]any)["health"].(map[string]any)
	ev, _ := health["evidence"].([]any)
	if health["state"] != "failing" || health["checked_at"] != "2026-11-22T11:30:00Z" || health["device"] != "/dev/sdb" || len(ev) != 1 {
		t.Fatalf("health = %v", health)
	}
	if e := ev[0].(map[string]any); e["signal"] != "smart" || e["values"].(map[string]any)["pending_sectors"] != float64(8) {
		t.Fatalf("evidence = %v", e)
	}
}

// #155: the owner follows hive move-data from the app: nothing before a
// move, then the note the command writes; members are refused.
func TestOwnerSeesTheDataMove(t *testing.T) {
	dir := t.TempDir()
	paths := hiveconfig.DefaultPathsFor("linux", func(string) string { return "" }).WithDataDir(dir)
	paths.Config = "/etc/camhive/hive.yaml" // a default Linux install keeps it there
	f := newAPIWith(t, func(d *hiveapp.Deps) {
		d.Move = &hiveapp.MoveInfo{Paths: paths, ArchiveDir: "/mnt/cams/archive", Supported: true}
	})
	owner := f.token(t, auth.RoleOwner, "*")
	r := f.do(t, "GET", "/v0/hive/move", owner, nil)
	if r.Status != 200 || r.Body["state"] != "none" || r.Body["supported"] != true || r.Body["data_dir"] != dir || r.Body["archive_dir"] != "/mnt/cams/archive" {
		t.Fatalf("before a move: %d %v", r.Status, r.Body)
	}
	files, _ := r.Body["files"].([]any)
	got := make([]string, 0, len(files))
	for _, f := range files {
		m := f.(map[string]any)
		got = append(got, m["name"].(string)+"="+m["path"].(string))
	}
	want := []string{"hive.key=" + filepath.Join(dir, "hive.key"), "cloud.json=" + filepath.Join(dir, "cloud.json"), "hive.yaml=/etc/camhive/hive.yaml",
		"archive-key=" + filepath.Join(dir, "archive-key") + "/", "hive.db=" + filepath.Join(dir, "hive.db")}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("files = %v, want %v", got, want)
	}
	note := `{"state":"copying","from":"` + dir + `","to":"/mnt/new","step":"archive","copied_bytes":5,"planned_bytes":10,"skipped_segments":0,"skipped_bytes":0}`
	if err := os.WriteFile(filepath.Join(dir, datamove.File), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	r = f.do(t, "GET", "/v0/hive/move", owner, nil)
	if r.Body["state"] != "copying" || r.Body["to"] != "/mnt/new" || r.Body["step"] != "archive" || r.Body["copied_bytes"] != float64(5) {
		t.Fatalf("during a move: %v", r.Body)
	}
	if r := f.do(t, "GET", "/v0/hive/move", f.token(t, auth.RoleViewer, "*"), nil); r.Status != 403 {
		t.Fatalf("member: %d, want 403", r.Status)
	}
}

// #155 re-review: while hive move-data copies, a change made through the
// API would stay on the old disk once the database's last copy is taken;
// writes answer 409 moving, reads and what lives in memory (sign-in,
// unlocking, the owner's presence) go on.
func TestWhileMovingWritesAnswerMoving(t *testing.T) {
	for _, moving := range []bool{true, false} {
		f := newAPIWith(t, func(d *hiveapp.Deps) {
			d.Move = &hiveapp.MoveInfo{Paths: hiveconfig.Paths{}.WithDataDir(t.TempDir()), Moving: moving}
		})
		owner := f.token(t, auth.RoleOwner, "*")
		for _, c := range []struct {
			method, path string
			refused      bool
		}{
			{"POST", "/v0/cameras", true},
			{"PATCH", "/v0/cameras/cam_x", true},
			{"DELETE", "/v0/cameras/cam_x", true},
			{"PUT", "/v0/local-login", true},
			{"PATCH", "/v0/hive/storage", true},
			{"PUT", "/v0/hive/updates", true},
			{"POST", "/v0/exports", true},
			{"GET", "/v0/cameras", false},
			{"GET", "/v0/hive/move", false},
			{"POST", "/v0/local-login/sessions", false},
			{"POST", "/v0/archive/encryption/unlock", false},
			{"POST", "/v0/archive/encryption/lock", false},
			{"POST", "/v0/archive/encryption/presence", false},
		} {
			r := f.do(t, c.method, c.path, owner, map[string]any{})
			got := r.Status == 409 && r.Body["code"] == "moving"
			if got != (c.refused && moving) {
				t.Errorf("moving %v: %s %s = %d %v", moving, c.method, c.path, r.Status, r.Body["code"])
			}
		}
	}
}
