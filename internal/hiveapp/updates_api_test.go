package hiveapp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/update/journal"
)

// updatesFixture serves the API with an updater directory and a hive.yaml.
// updatesFixture serves the API as a hive that hive-updater launched with
// launched in CAMHIVE_UPDATER ("" for none).
func updatesFixture(t *testing.T, launched string) (*apiFixture, hiveconfig.Paths, string) {
	t.Helper()
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if _, _, err := hiveconfig.Load(paths); err != nil { // the defaults
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "updater")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f := newAPIWith(t, func(d *hiveapp.Deps) {
		d.Version = "0.1.6"
		d.Updates = &hiveapp.UpdateControl{Paths: paths, Dir: dir, Launched: launched}
	})
	return f, paths, dir
}

func (f *apiFixture) lastAction(t *testing.T) store.Action {
	t.Helper()
	got, err := f.st.Actions().Recent(context.Background(), 1)
	if err != nil || len(got) == 0 {
		t.Fatalf("action log: %v %v", got, err)
	}
	return got[0]
}

// #82: the owner reads and changes mode, window and channel; they land in
// hive.yaml (where hive-updater reads them at every check) and in the
// action log. Nobody else may, and a bad window has its own code.
func TestOwnerChangesUpdateSettings(t *testing.T) {
	f, paths, _ := updatesFixture(t, "on")
	owner := f.token(t, auth.RoleOwner)
	viewer := f.token(t, auth.RoleOperator, "*")
	for _, c := range []struct{ method, path string }{{"GET", "/v0/hive/updates"}, {"PUT", "/v0/hive/updates"}, {"POST", "/v0/hive/updates/install"}} {
		if res := f.do(t, c.method, c.path, viewer, map[string]string{"updates": "manual", "update_window": "02:00-05:00", "update_channel": "stable"}); res.Status != http.StatusForbidden || res.Body["code"] != "forbidden" {
			t.Errorf("%s %s as an operator = %d %v", c.method, c.path, res.Status, res.Body)
		}
	}

	res := f.do(t, "GET", "/v0/hive/updates", owner, nil)
	if res.Status != 200 || res.Body["updates"] != "automatic" || res.Body["update_window"] != "02:00-05:00" || res.Body["update_channel"] != "stable" ||
		res.Body["running"] != "0.1.6" || res.Body["supported"] != true {
		t.Fatalf("GET = %d %v", res.Status, res.Body)
	}

	want := hiveconfig.UpdateSettings{Updates: "manual", UpdateWindow: "22:00-23:30", UpdateChannel: "beta"}
	res = f.do(t, "PUT", "/v0/hive/updates", owner, want)
	if res.Status != 200 || res.Body["updates"] != "manual" || res.Body["update_window"] != "22:00-23:30" || res.Body["update_channel"] != "beta" {
		t.Fatalf("PUT = %d %v", res.Status, res.Body)
	}
	if cfg, _, err := hiveconfig.Load(paths); err != nil || cfg.UpdateSettings() != want {
		t.Fatalf("hive.yaml = %+v %v", cfg.UpdateSettings(), err)
	}
	if a := f.lastAction(t); a.Action != "update.settings_changed" || a.Actor != "acc" {
		t.Fatalf("action = %+v", a)
	}

	for body, code := range map[string]string{
		`{"updates":"manual","update_window":"25:00-02:00","update_channel":"stable"}`:    "invalid_update_window",
		`{"updates":"manual","update_window":"","update_channel":"stable"}`:               "invalid_update_window",
		`{"updates":"sometimes","update_window":"02:00-05:00","update_channel":"stable"}`: "invalid",
		`{"updates":"manual","update_window":"02:00-05:00","update_channel":"nightly"}`:   "invalid",
	} {
		if res := f.do(t, "PUT", "/v0/hive/updates", owner, json.RawMessage(body)); res.Status != http.StatusBadRequest || res.Body["code"] != code {
			t.Errorf("PUT %s = %d %v, want %s", body, res.Status, res.Body, code)
		}
	}
	if cfg, _, _ := hiveconfig.Load(paths); cfg.UpdateSettings() != want {
		t.Fatalf("a refused change was stored: %+v", cfg.UpdateSettings())
	}
}

// "Update now" writes the request `hive update` writes; hive-updater takes
// it within 30 s. The request is in the action log.
func TestOwnerAsksToUpdateNow(t *testing.T) {
	f, _, dir := updatesFixture(t, "on")
	if res := f.do(t, "POST", "/v0/hive/updates/install", f.token(t, auth.RoleOwner), nil); res.Status != http.StatusAccepted {
		t.Fatalf("install = %d %v", res.Status, res.Body)
	}
	if asked, _ := journal.TakeRequest(dir); !asked {
		t.Fatal("no request for the updater")
	}
	if a := f.lastAction(t); a.Action != "update.requested" {
		t.Fatalf("action = %+v", a)
	}
}

// A build that never updates says why and refuses "Update now".
func TestABuildWithoutUpdatesSaysWhy(t *testing.T) {
	f, _, dir := updatesFixture(t, "dev_build")
	owner := f.token(t, auth.RoleOwner)
	if res := f.do(t, "GET", "/v0/hive/updates", owner, nil); res.Body["supported"] != false || res.Body["unsupported_reason"] != "dev_build" {
		t.Fatalf("GET = %v", res.Body)
	}
	if res := f.do(t, "POST", "/v0/hive/updates/install", owner, nil); res.Status != http.StatusConflict || res.Body["code"] != "updates_unavailable" {
		t.Fatalf("install = %d %v", res.Status, res.Body)
	}
	if asked, _ := journal.TakeRequest(dir); asked {
		t.Fatal("asked an updater that never updates")
	}
}

// The status comes from what hive-updater reported into the action log:
// the version on offer (manual mode) and the last update or rollback. A
// version that rolled back is not offered again.
func TestUpdateStatusFollowsTheUpdatersReports(t *testing.T) {
	f, _, _ := updatesFixture(t, "on")
	owner := f.token(t, auth.RoleOwner)
	report := func(kind string, data map[string]any) {
		raw, _ := json.Marshal(data)
		if err := f.st.Actions().Append(context.Background(), store.Action{Actor: "updater", Action: kind, Target: "hive", Details: raw}); err != nil {
			t.Fatal(err)
		}
	}
	report("update.applied", map[string]any{"from": "0.1.5", "to": "0.1.6"})
	report("update.available", map[string]any{"running": "0.1.5", "offered": "0.1.6"}) // older than what runs: not on offer
	if res := f.do(t, "GET", "/v0/hive/updates", owner, nil); res.Body["available"] != nil || res.Body["last"].(map[string]any)["to"] != "0.1.6" {
		t.Fatalf("after an applied update = %v", res.Body)
	}
	report("update.available", map[string]any{"running": "0.1.6", "offered": "0.1.7"})
	res := f.do(t, "GET", "/v0/hive/updates", owner, nil)
	if last := res.Body["last"].(map[string]any); res.Body["available"] != "0.1.7" || last["action"] != "update.applied" || last["from"] != "0.1.5" || last["at"] == nil {
		t.Fatalf("with an offer = %v", res.Body)
	}
	report("update.rolled_back", map[string]any{"from": "0.1.6", "to": "0.1.7"})
	res = f.do(t, "GET", "/v0/hive/updates", owner, nil)
	if last := res.Body["last"].(map[string]any); res.Body["available"] != nil || last["action"] != "update.rolled_back" || last["to"] != "0.1.7" {
		t.Fatalf("after a rollback = %v", res.Body)
	}
}

// What launched the hive decides the status: the updater says so in
// CAMHIVE_UPDATER. Without it, a current updater that announced itself is
// not running this hive (it runs in the foreground); an updater older than
// v0.1.7 never sets it and reads hive.yaml only at start.
func TestStatusFollowsWhatLaunchedTheHive(t *testing.T) {
	cases := []struct {
		launched, announced string
		noDir               bool
		reason              string
		restart             bool
	}{
		{launched: "on"},
		{launched: "no_root", reason: "no_root"},
		{launched: "", restart: true},                            // an updater older than v0.1.7
		{launched: "", announced: "0.1.7", reason: "no_updater"}, // hive run in the foreground
		{launched: "", noDir: true, reason: "no_updater"},
	}
	for _, c := range cases {
		f, _, dir := updatesFixture(t, c.launched)
		if c.announced != "" {
			if err := journal.Announce(dir, c.announced); err != nil {
				t.Fatal(err)
			}
		}
		if c.noDir {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}
		res := f.do(t, "GET", "/v0/hive/updates", f.token(t, auth.RoleOwner), nil)
		reason, _ := res.Body["unsupported_reason"].(string)
		restart, _ := res.Body["settings_need_restart"].(bool)
		if reason != c.reason || res.Body["supported"] != (c.reason == "") || restart != c.restart {
			t.Errorf("%+v: status = %v", c, res.Body)
		}
	}
}

// A local emergency session (#57) is the owner, but it exists to reach
// recordings while the cloud is down: it may not change update settings or
// force an install (security over convenience; updates need the internet
// anyway).
func TestLocalSessionsCannotManageUpdates(t *testing.T) {
	paths := hiveconfig.Paths{}.WithDataDir(t.TempDir())
	if _, _, err := hiveconfig.Load(paths); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := newAPIWith(t, func(d *hiveapp.Deps) {
		d.Local = auth.NewLocalSessions("hv_1", time.Now)
		d.Updates = &hiveapp.UpdateControl{Paths: paths, Dir: dir}
	})
	lan := f.viaFrom(t, hiveapp.LANConn, lanPeer)
	if r := lan.do(t, "PUT", "/v0/local-login", f.token(t, auth.RoleOwner, "*"), map[string]string{"username": "anna", "password": localPassword}); r.Status != 200 {
		t.Fatalf("enable: %d %v", r.Status, r.Body)
	}
	r := lan.do(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": localPassword})
	local, _ := r.Body["token"].(string)
	for _, c := range []struct{ method, path string }{{"GET", "/v0/hive/updates"}, {"PUT", "/v0/hive/updates"}, {"POST", "/v0/hive/updates/install"}} {
		if res := lan.do(t, c.method, c.path, local, map[string]string{"updates": "manual", "update_window": "02:00-05:00", "update_channel": "beta"}); res.Status != http.StatusForbidden || res.Body["code"] != "forbidden" {
			t.Errorf("%s %s with a local session = %d %v", c.method, c.path, res.Status, res.Body)
		}
	}
	if asked, _ := journal.TakeRequest(dir); asked {
		t.Fatal("a local session asked for an update")
	}
}

// The status reads the updater's reports by kind, so a busy action log
// does not push them out; a release that needs a newer updater is shown as
// such, not as an update "Update now" could install.
func TestStatusSurvivesABusyLogAndShowsANeededPackage(t *testing.T) {
	f, _, _ := updatesFixture(t, "on")
	ctx := context.Background()
	add := func(kind string, data map[string]any) {
		raw, _ := json.Marshal(data)
		if err := f.st.Actions().Append(ctx, store.Action{Actor: "updater", Action: kind, Target: "hive", Details: raw}); err != nil {
			t.Fatal(err)
		}
	}
	add("update.applied", map[string]any{"from": "0.1.5", "to": "0.1.6"})
	add("update.available", map[string]any{"running": "0.1.6", "offered": "0.1.7"})
	add("update.needs_package", map[string]any{"running": "0.1.6", "offered": "0.2.0"})
	for i := 0; i < 250; i++ {
		add("camera.update", map[string]any{"name": "Door"})
	}
	res := f.do(t, "GET", "/v0/hive/updates", f.token(t, auth.RoleOwner), nil)
	last, _ := res.Body["last"].(map[string]any)
	if res.Body["available"] != "0.1.7" || res.Body["needs_package"] != "0.2.0" || last["to"] != "0.1.6" {
		t.Fatalf("status on a busy hive = %v", res.Body)
	}
}
