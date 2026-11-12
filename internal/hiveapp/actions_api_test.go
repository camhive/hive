package hiveapp_test

import (
	"testing"

	"github.com/camhive/hive/internal/auth"
)

// Owner actions through the API leave an audit trail the owner can read.
func TestCameraChangesAreRecordedInTheActionLog(t *testing.T) {
	f := newAPI(t)
	owner := f.token(t, auth.RoleOwner, "*")
	r := f.do(t, "POST", "/v0/cameras", owner, map[string]any{"name": "Door", "source": map[string]string{"kind": "rtsp", "address": "rtsp://u:pw@h/x"}})
	id := r.Body["id"].(string)
	name := "Front"
	f.do(t, "PATCH", "/v0/cameras/"+id, owner, map[string]any{"name": name})
	f.do(t, "DELETE", "/v0/cameras/"+id, owner, nil)

	r = f.do(t, "GET", "/v0/actions", owner, nil)
	if r.Status != 200 || len(r.List) != 3 {
		t.Fatalf("actions: %d %v", r.Status, r.List)
	}
	if r.List[0]["action"] != "camera.remove" || r.List[1]["action"] != "camera.update" || r.List[2]["action"] != "camera.add" {
		t.Fatalf("order/newest first: %v", r.List)
	}
	if r.List[2]["actor"] != "acc" || r.List[2]["target"] != id {
		t.Fatalf("actor/target: %v", r.List[2])
	}
	for _, a := range r.List {
		if d, _ := a["details"].(map[string]any); d["password"] != nil {
			t.Fatalf("details must not carry secrets: %v", a)
		}
	}
	viewer := f.token(t, auth.RoleViewer, id)
	if r := f.do(t, "GET", "/v0/actions", viewer, nil); r.Status != 403 {
		t.Fatalf("viewer must not read the action log: %d", r.Status)
	}
}
