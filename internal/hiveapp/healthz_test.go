package hiveapp_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/hiveapp"
)

// ADR-0018: the updater on the same machine reads the version and how many
// cameras record from /healthz to decide whether an update is healthy.
func TestHealthzTellsTheLocalUpdaterHowManyCamerasRecord(t *testing.T) {
	fh := fakeHealth{cams: map[string]health.CameraHealth{
		"cam_a": {Camera: "cam_a", Enabled: true, Online: true},
		"cam_b": {Camera: "cam_b", Enabled: true, Online: false},
		"cam_c": {Camera: "cam_c", Enabled: false, Online: true},
	}}
	f := newAPIWith(t, func(d *hiveapp.Deps) { d.Health = fh; d.Version = "0.1.5" })
	res, err := http.Get(f.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body["status"] != "ok" || body["version"] != "0.1.5" || body["recording"] != 1.0 {
		t.Fatalf("healthz = %v", body)
	}
}
