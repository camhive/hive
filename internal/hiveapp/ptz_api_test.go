package hiveapp_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/entitlements"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/ptz"
	"github.com/camhive/hive/internal/store"
)

type fakePTZ struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakePTZ) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakePTZ) Supported(_ context.Context, c camera.Camera) (bool, error) {
	return c.Source.Kind == camera.SourceONVIF, nil
}
func (f *fakePTZ) Move(_ context.Context, c camera.Camera, pan, tilt, zoom float64) error {
	if c.Source.Kind != camera.SourceONVIF {
		return ptz.ErrUnsupported
	}
	if pan > 1 {
		return ptz.ErrInvalid
	}
	f.record("move")
	return nil
}
func (f *fakePTZ) Stop(context.Context, camera.Camera) error { f.record("stop"); return nil }
func (f *fakePTZ) Presets(context.Context, camera.Camera) ([]ptz.Preset, error) {
	return []ptz.Preset{{Token: "p1", Name: "Door"}}, nil
}
func (f *fakePTZ) GotoPreset(_ context.Context, _ camera.Camera, token string) error {
	if token != "p1" {
		return errors.New("onvif: fault NoToken")
	}
	f.record("goto:" + token)
	return nil
}

func newPTZAPI(t *testing.T) (*apiFixture, *fakePTZ, camera.Camera, camera.Camera) {
	t.Helper()
	iss, _ := auth.NewIssuer("k1")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	onvifCam, _ := camera.New(camera.Spec{Name: "O", Source: camera.Source{Kind: camera.SourceONVIF, Address: "http://h/onvif/device_service"}}, time.Now())
	rtspCam, _ := camera.New(camera.Spec{Name: "R", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/r"}}, time.Now())
	_ = st.Cameras().Put(context.Background(), onvifCam)
	_ = st.Cameras().Put(context.Background(), rtspCam)
	fp := &fakePTZ{}
	h := hiveapp.NewAPI(hiveapp.Deps{Store: st, Verifier: auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}), Version: "test", PTZ: fp})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &apiFixture{srv: srv, iss: iss, st: st}, fp, onvifCam, rtspCam
}

func TestPTZRequiresOperatorAndReportsSupport(t *testing.T) {
	f, fp, o, r := newPTZAPI(t)
	viewer := f.token(t, auth.RoleViewer, string(o.ID), string(r.ID))
	if res := f.do(t, "GET", "/v0/cameras/"+string(o.ID)+"/ptz", viewer, nil); res.Status != 200 || res.Body["supported"] != true {
		t.Fatalf("support (viewer may ask): %d %v", res.Status, res.Body)
	}
	if res := f.do(t, "GET", "/v0/cameras/"+string(r.ID)+"/ptz", viewer, nil); res.Status != 200 || res.Body["supported"] != false {
		t.Fatalf("rtsp support: %d %v", res.Status, res.Body)
	}
	if res := f.do(t, "POST", "/v0/cameras/"+string(o.ID)+"/ptz/move", viewer, map[string]any{"pan": 0.5}); res.Status != 403 {
		t.Fatalf("viewer move: %d", res.Status)
	}
	operator := f.token(t, auth.RoleOperator, string(o.ID), string(r.ID))
	if res := f.do(t, "POST", "/v0/cameras/"+string(o.ID)+"/ptz/move", operator, map[string]any{"pan": 0.5, "tilt": -0.2}); res.Status != 204 {
		t.Fatalf("operator move: %d %v", res.Status, res.Body)
	}
	if res := f.do(t, "POST", "/v0/cameras/"+string(o.ID)+"/ptz/stop", operator, nil); res.Status != 204 {
		t.Fatalf("stop: %d", res.Status)
	}
	if res := f.do(t, "GET", "/v0/cameras/"+string(o.ID)+"/ptz/presets", operator, nil); res.Status != 200 || len(res.List) != 1 || res.List[0]["token"] != "p1" {
		t.Fatalf("presets: %d %v", res.Status, res.List)
	}
	if res := f.do(t, "POST", "/v0/cameras/"+string(o.ID)+"/ptz/presets/p1/goto", operator, nil); res.Status != 204 {
		t.Fatalf("goto: %d", res.Status)
	}
	if res := f.do(t, "POST", "/v0/cameras/"+string(o.ID)+"/ptz/presets/p9/goto", operator, nil); res.Status != 502 || res.Body["code"] != "camera" {
		t.Fatalf("bad preset: %d %v", res.Status, res.Body)
	}
	if res := f.do(t, "POST", "/v0/cameras/"+string(r.ID)+"/ptz/move", operator, map[string]any{"pan": 0.5}); res.Status != 409 || res.Body["code"] != "unsupported" {
		t.Fatalf("rtsp move: %d %v", res.Status, res.Body)
	}
	if res := f.do(t, "POST", "/v0/cameras/"+string(o.ID)+"/ptz/move", operator, map[string]any{"pan": 5}); res.Status != 422 || res.Body["code"] != "invalid" {
		t.Fatalf("out of range: %d %v", res.Status, res.Body)
	}
	if got := fp.calls; len(got) != 3 || got[0] != "move" || got[1] != "stop" || got[2] != "goto:p1" {
		t.Fatalf("controller saw %v", got)
	}
}

func TestPTZNotReadyWithoutController(t *testing.T) {
	f := newAPI(t)
	owner := f.token(t, auth.RoleOwner, "*")
	if res := f.do(t, "GET", "/v0/cameras/cam_x/ptz", owner, nil); res.Status != 503 {
		t.Fatalf("no controller: %d", res.Status)
	}
}

func TestPTZIsRefusedWhenTheLicenseExcludesIt(t *testing.T) {
	f, fp, o, r := newPTZAPI(t)
	noPTZ := func() entitlements.Set {
		set := entitlements.OnPlan(entitlements.PlanFree).Set
		set[entitlements.PTZEnabled] = entitlements.B(false)
		return set
	}
	h := hiveapp.NewAPI(hiveapp.Deps{Store: f.st, Verifier: auth.NewVerifier("hv_1", auth.Keys{"k1": f.iss.PublicKey()}), Version: "test", PTZ: fp, Licensed: noPTZ})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	g := &apiFixture{srv: srv, iss: f.iss, st: f.st}
	operator := g.token(t, auth.RoleOperator, string(o.ID), string(r.ID))
	if res := g.do(t, "GET", "/v0/cameras/"+string(o.ID)+"/ptz", operator, nil); res.Status != 200 || res.Body["supported"] != false || res.Body["licensed"] != false {
		t.Fatalf("ptz info unlicensed = %d %v", res.Status, res.Body)
	}
	if res := g.do(t, "POST", "/v0/cameras/"+string(o.ID)+"/ptz/move", operator, map[string]any{"pan": 0.5}); res.Status != 403 || res.Body["code"] != "plan_limit" {
		t.Fatalf("ptz move unlicensed = %d %v", res.Status, res.Body)
	}
}
