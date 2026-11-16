package hiveapp_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
)

// #61: the owner gives a camera its own retention, or puts it back on the
// hive-wide value with null; the camera reports it, the action log records it.
func TestOwnerSetsAndResetsCameraRetention(t *testing.T) {
	f := newAPI(t)
	c, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/a"}}, time.Now())
	_ = f.st.Cameras().Put(context.Background(), c)
	owner, path := f.token(t, auth.RoleOwner, "*"), "/v0/cameras/"+string(c.ID)

	if r := f.do(t, "GET", path, owner, nil); r.Status != 200 || r.Body["retention_days"] != nil {
		t.Fatalf("a new camera follows the hive: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "PATCH", path, owner, map[string]any{"retention_days": 90}); r.Status != 200 || r.Body["retention_days"] != float64(90) {
		t.Fatalf("set: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "PATCH", path, owner, map[string]any{"name": "Front door"}); r.Body["retention_days"] != float64(90) {
		t.Fatalf("an unrelated patch changed retention: %v", r.Body)
	}
	if days, _ := f.st.Cameras().RetentionDays(context.Background()); days[string(c.ID)] != 90 {
		t.Fatalf("stored = %v", days)
	}
	if r := f.do(t, "PATCH", path, owner, map[string]any{"retention_days": nil}); r.Status != 200 || r.Body["retention_days"] != nil {
		t.Fatalf("reset: %d %v", r.Status, r.Body)
	}
	if days, _ := f.st.Cameras().RetentionDays(context.Background()); len(days) != 0 {
		t.Fatalf("stored after reset = %v", days)
	}
	r := f.do(t, "GET", "/v0/actions", owner, nil)
	if len(r.List) < 2 || r.List[0]["action"] != "camera.update" || r.List[0]["details"].(map[string]any)["retention_days"] != "hive" {
		t.Fatalf("action log = %v", r.List)
	}
}

func TestCameraRetentionIsValidatedAndOwnerOnly(t *testing.T) {
	f := newAPI(t)
	c, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/a"}}, time.Now())
	_ = f.st.Cameras().Put(context.Background(), c)
	path := "/v0/cameras/" + string(c.ID)
	owner := f.token(t, auth.RoleOwner, "*")
	for _, bad := range []any{-1, camera.MaxRetentionDays + 1} {
		if r := f.do(t, "PATCH", path, owner, map[string]any{"retention_days": bad}); r.Status != 422 || r.Body["code"] != "invalid" {
			t.Fatalf("%v days: %d %v", bad, r.Status, r.Body)
		}
	}
	for _, bad := range []any{"30", 1.5} {
		if r := f.do(t, "PATCH", path, owner, map[string]any{"retention_days": bad}); r.Status != 400 || r.Body["code"] != "bad_json" {
			t.Fatalf("%v: %d %v", bad, r.Status, r.Body)
		}
	}
	viewer := f.token(t, auth.RoleViewer, string(c.ID))
	if r := f.do(t, "PATCH", path, viewer, map[string]any{"retention_days": 1}); r.Status != 403 {
		t.Fatalf("viewer: %d %v", r.Status, r.Body)
	}
	if days, _ := f.st.Cameras().RetentionDays(context.Background()); len(days) != 0 {
		t.Fatalf("a refused patch was stored: %v", days)
	}
}
