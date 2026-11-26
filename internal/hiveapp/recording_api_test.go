package hiveapp_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/recording"
)

var stoppedAt = time.Date(2026, 11, 24, 10, 30, 0, 0, time.UTC)

func stopped() recording.State {
	return recording.State{Since: stoppedAt, Reason: recording.ArchiveMissing, Detail: "stat /media/disk/camhive/archive: no such file or directory"}
}

// #172: every member's app learns from GET /v0/hive that the hive does not
// record, since when and why; the detail (paths) stays on the hive.
func TestHiveInfoSaysWhenTheHiveDoesNotRecord(t *testing.T) {
	f := newAPIWith(t, func(d *hiveapp.Deps) { d.Recording = stopped })
	res := f.do(t, http.MethodGet, "/v0/hive", f.token(t, auth.RoleViewer, "cam_x"), nil)
	nr, _ := res.Body["not_recording"].(map[string]any)
	if res.Status != http.StatusOK || nr["reason"] != "archive_missing" || nr["since"] != "2026-11-24T10:30:00Z" {
		t.Fatalf("GET /v0/hive = %d %v, want not_recording since 10:30 for archive_missing", res.Status, res.Body)
	}
	if _, ok := nr["detail"]; ok {
		t.Fatalf("not_recording = %v, want no detail for the app", nr)
	}

	f = newAPIWith(t, func(d *hiveapp.Deps) {
		d.Recording = func() recording.State { return recording.State{Since: stoppedAt} }
	})
	res = f.do(t, http.MethodGet, "/v0/hive", f.token(t, auth.RoleViewer, "cam_x"), nil)
	if _, ok := res.Body["not_recording"]; ok || res.Status != http.StatusOK {
		t.Fatalf("GET /v0/hive while recording = %v, want no not_recording", res.Body)
	}
}

// `hive status` reads /healthz: a program on this machine gets the state
// with its detail; liveness stays ok, so hive-updater does not roll an
// update back because a disk vanished.
func TestHealthzTellsTheLocalToolTheHiveDoesNotRecord(t *testing.T) {
	f := newAPIWith(t, func(d *hiveapp.Deps) { d.Recording = stopped })
	res, err := http.Get(f.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Status       string
		NotRecording *struct{ Since, Reason, Detail string } `json:"not_recording"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body.Status != "ok" || body.NotRecording == nil || body.NotRecording.Reason != "archive_missing" || body.NotRecording.Detail == "" {
		t.Fatalf("healthz = %+v, want ok with not_recording and its detail", body)
	}
}
