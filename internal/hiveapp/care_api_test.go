package hiveapp_test

import (
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/hiveapp"
)

type fakeCare map[string]health.CameraCare

func (f fakeCare) Camera(id string) health.CameraCare {
	if c, ok := f[id]; ok {
		return c
	}
	return health.CameraCare{Camera: id, Reasons: []health.ReasonView{}}
}

// GET /v0/cameras/{id}/care (#163) answers viewers of the camera with the
// signals and the suggestion per reason; the health views carry the
// summary the app's badges read.
func TestCameraCareForViewersOfTheCamera(t *testing.T) {
	care := fakeCare{}
	f, a, b := newHealthAPI(t, func(d *hiveapp.Deps) { d.Care = care })
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	care[string(a.ID)] = health.CameraCare{Camera: string(a.ID), CheckedAt: &at, Degraded: true, Since: &at,
		Reasons:    []health.ReasonView{{Reason: health.Reconnects, Suggestion: health.Reconnects.Suggestion()}},
		Thresholds: health.DefaultThresholds()}

	viewer := f.token(t, auth.RoleViewer, string(a.ID))
	r := f.do(t, "GET", "/v0/cameras/"+string(a.ID)+"/care", viewer, nil)
	reasons, _ := r.Body["reasons"].([]any)
	if r.Status != 200 || r.Body["degraded"] != true || len(reasons) != 1 ||
		reasons[0].(map[string]any)["suggestion"] != "Check the cable or the PoE budget." || r.Body["thresholds"].(map[string]any)["reconnects"] != float64(4) {
		t.Fatalf("care: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "GET", "/v0/cameras/"+string(b.ID)+"/care", viewer, nil); r.Status != 404 {
		t.Fatalf("ungranted care: %d", r.Status)
	}
	if r := f.do(t, "GET", "/v0/cameras/cam_000000000000/care", f.token(t, auth.RoleOwner, "*"), nil); r.Status != 404 {
		t.Fatalf("unknown camera with a * token: %d", r.Status)
	}
	r = f.do(t, "GET", "/v0/health/cameras", f.token(t, auth.RoleOwner, "*"), nil)
	for _, h := range r.List {
		summary := h["care"].(map[string]any)
		if want := h["camera"] == string(a.ID); summary["degraded"] != want {
			t.Fatalf("summary of %v: %v", h["camera"], summary)
		}
	}
	if r := f.do(t, "GET", "/v0/cameras/"+string(a.ID)+"/health", viewer, nil); r.Body["care"].(map[string]any)["reasons"].([]any)[0] != "reconnects" {
		t.Fatalf("health a: %v", r.Body)
	}
}

func TestCameraCareNotReadyWithoutTheEngine(t *testing.T) {
	f, a, _ := newHealthAPI(t)
	if r := f.do(t, "GET", "/v0/cameras/"+string(a.ID)+"/care", f.token(t, auth.RoleOwner, "*"), nil); r.Status != 503 {
		t.Fatalf("care without care: %d", r.Status)
	}
}
