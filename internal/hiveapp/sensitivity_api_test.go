package hiveapp_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/hiveapp"
)

// calmed is the motion hub as the API sees it: when a camera was calmed.
type calmed map[string]time.Time

func (c calmed) CalmedSince(cam string) (time.Time, bool) { t, ok := c[cam]; return t, ok }

// #157: the owner sets a camera's motion sensitivity; the camera reports it
// and, while the hive has calmed it, since when; the action log records it.
func TestOwnerSetsCameraSensitivityAndSeesTheCalmedNote(t *testing.T) {
	c, _ := camera.New(camera.Spec{Name: "Garden", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/a"}}, time.Now())
	since := time.Date(2026, 11, 22, 13, 10, 0, 0, time.UTC)
	f := newAPIWith(t, func(d *hiveapp.Deps) { d.Motion = calmed{string(c.ID): since} })
	_ = f.st.Cameras().Put(context.Background(), c)
	owner, path := f.token(t, auth.RoleOwner, "*"), "/v0/cameras/"+string(c.ID)

	if r := f.do(t, "GET", path, owner, nil); r.Status != 200 || r.Body["sensitivity"] != "normal" || r.Body["motion_calmed_since"] != "2026-11-22T13:10:00Z" {
		t.Fatalf("get: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "PATCH", path, owner, map[string]any{"sensitivity": "low"}); r.Status != 200 || r.Body["sensitivity"] != "low" {
		t.Fatalf("set: %d %v", r.Status, r.Body)
	}
	if got, _ := f.st.Cameras().Get(context.Background(), c.ID); got.Sensitivity != camera.SensitivityLow {
		t.Fatalf("stored %v", got.Sensitivity)
	}
	if r := f.do(t, "PATCH", path, owner, map[string]any{"sensitivity": "max"}); r.Status != 422 || r.Body["code"] != "invalid" {
		t.Fatalf("unknown: %d %v", r.Status, r.Body)
	}
	r := f.do(t, "GET", "/v0/actions", owner, nil)
	if len(r.List) != 1 || r.List[0]["action"] != "camera.update" || r.List[0]["details"].(map[string]any)["sensitivity"] != "low" {
		t.Fatalf("action log = %v", r.List)
	}
}

func TestACameraNotCalmedHasNoCalmedNote(t *testing.T) {
	f := newAPI(t)
	c, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/a"}}, time.Now())
	_ = f.st.Cameras().Put(context.Background(), c)
	if r := f.do(t, "GET", "/v0/cameras/"+string(c.ID), f.token(t, auth.RoleOwner, "*"), nil); r.Status != 200 || r.Body["motion_calmed_since"] != nil {
		t.Fatalf("get: %d %v", r.Status, r.Body)
	}
}
