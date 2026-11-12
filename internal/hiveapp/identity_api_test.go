package hiveapp_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/hiveapp"
)

// locatorFake answers what the device at a host is.
type locatorFake struct {
	at    map[string]camera.Identity
	asked []string
}

func (l *locatorFake) Probe(_ context.Context, _ camera.Camera, host string) (camera.Identity, camera.Network) {
	l.asked = append(l.asked, host)
	return l.at[host], camera.NetworkLocal
}

func newLocatorAPI(t *testing.T, l hiveapp.CameraLocator) (*apiFixture, camera.Camera) {
	t.Helper()
	c, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://192.168.1.10:554/main"}}, time.Now())
	c.Identity, c.Network = camera.Identity{MAC: "aa:bb:cc:00:11:22", Serial: "SN1", Model: "X1"}, camera.NetworkLocal
	f := newAPIWith(t, func(d *hiveapp.Deps) {
		d.Locator = l
		if err := d.Store.Cameras().Put(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	})
	return f, c
}

// ADR-0017: identity and network are shown read-only; the identity to the owner only.
func TestCameraViewShowsIdentityToTheOwnerAndNetworkToAll(t *testing.T) {
	f, c := newLocatorAPI(t, nil)
	r := f.do(t, "GET", "/v0/cameras/"+string(c.ID), f.token(t, auth.RoleOwner, "*"), nil)
	id, _ := r.Body["identity"].(map[string]any)
	if r.Body["network"] != "local" || id["mac"] != "aa:bb:cc:00:11:22" || id["serial"] != "SN1" || id["model"] != "X1" {
		t.Fatalf("owner view = %v", r.Body)
	}
	r = f.do(t, "GET", "/v0/cameras/"+string(c.ID), f.token(t, auth.RoleViewer, string(c.ID)), nil)
	if r.Body["network"] != "local" || r.Body["identity"] != nil {
		t.Fatalf("viewer view = %v", r.Body)
	}
	// Identity cannot be written.
	if r := f.do(t, "PATCH", "/v0/cameras/"+string(c.ID), f.token(t, auth.RoleOwner, "*"), map[string]any{"identity": map[string]string{"mac": "x"}}); r.Status != 400 {
		t.Fatalf("identity patch: %d", r.Status)
	}
}

// The owner moves a camera by hand; another device at the new host is
// refused unless the owner says the camera was replaced.
func TestOwnerChangesTheAddressAndAMismatchNeedsConfirmation(t *testing.T) {
	l := &locatorFake{at: map[string]camera.Identity{
		"192.168.1.23": {MAC: "aa:bb:cc:00:11:22", Serial: "SN1"},
		"192.168.1.50": {MAC: "11:22:33:44:55:66", Serial: "SN9", Model: "Y2"},
	}}
	f, c := newLocatorAPI(t, l)
	owner := f.token(t, auth.RoleOwner, "*")
	path := "/v0/cameras/" + string(c.ID)

	r := f.do(t, "PATCH", path, owner, map[string]any{"host": "192.168.1.23"})
	if r.Status != 200 || r.Body["source"].(map[string]any)["address"] != "rtsp://192.168.1.23:554/main" || r.Body["id"] != string(c.ID) {
		t.Fatalf("move: %d %v", r.Status, r.Body)
	}

	r = f.do(t, "PATCH", path, owner, map[string]any{"host": "192.168.1.50"})
	if r.Status != 409 || r.Body["code"] != "camera_mismatch" {
		t.Fatalf("mismatch: %d %v", r.Status, r.Body)
	}
	if got, _ := f.st.Cameras().Get(context.Background(), c.ID); got.Host() != "192.168.1.23" {
		t.Fatalf("moved despite the mismatch: %s", got.Host())
	}
	r = f.do(t, "PATCH", path, owner, map[string]any{"host": "192.168.1.50", "replace_identity": true})
	if r.Status != 200 {
		t.Fatalf("replace: %d %v", r.Status, r.Body)
	}
	got, _ := f.st.Cameras().Get(context.Background(), c.ID)
	if got.Host() != "192.168.1.50" || got.Identity != (camera.Identity{MAC: "11:22:33:44:55:66", Serial: "SN9", Model: "Y2"}) {
		t.Fatalf("camera = %s %+v", got.Host(), got.Identity)
	}
	a := f.do(t, "GET", "/v0/actions", owner, nil)
	if a.List[0]["action"] != "camera.move" || a.List[0]["details"].(map[string]any)["replaced_identity"] != true {
		t.Fatalf("actions = %v", a.List)
	}

	if r := f.do(t, "PATCH", path, owner, map[string]any{"host": "not a host"}); r.Status != 422 {
		t.Fatalf("bad host: %d", r.Status)
	}
}
