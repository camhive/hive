package hiveapp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/store"
)

type fakeHealth struct {
	cams map[string]health.CameraHealth
}

func (f fakeHealth) Camera(id string) health.CameraHealth { return f.cams[id] }
func (f fakeHealth) All() []health.CameraHealth {
	out := make([]health.CameraHealth, 0, len(f.cams))
	for _, h := range f.cams {
		out = append(out, h)
	}
	return out
}

func newHealthAPI(t *testing.T) (*apiFixture, camera.Camera, camera.Camera) {
	t.Helper()
	iss, _ := auth.NewIssuer("k1")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	a, _ := camera.New(camera.Spec{Name: "A", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/a"}}, time.Now())
	b, _ := camera.New(camera.Spec{Name: "B", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/b"}}, time.Now())
	_ = st.Cameras().Put(context.Background(), a)
	_ = st.Cameras().Put(context.Background(), b)
	at := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	_, _ = st.Events().Append(context.Background(), store.Event{Camera: string(a.ID), Kind: "camera.offline", At: at, Data: json.RawMessage(`{"stream":"main"}`)})
	_, _ = st.Events().Append(context.Background(), store.Event{Camera: string(a.ID), Kind: "camera.online", At: at.Add(time.Minute)})
	_, _ = st.Events().Append(context.Background(), store.Event{Camera: string(b.ID), Kind: "camera.online", At: at})
	fh := fakeHealth{cams: map[string]health.CameraHealth{
		string(a.ID): {Camera: string(a.ID), Enabled: true, Online: true, Streams: map[string]health.StreamHealth{"main": {Stream: "main", Online: true, BitrateKbps: 1500, Tracks: []string{"H264"}}}},
		string(b.ID): {Camera: string(b.ID), Enabled: true, Online: false, Streams: map[string]health.StreamHealth{"main": {Stream: "main", Online: false, LastError: "connection refused"}}},
	}}
	h := hiveapp.NewAPI(hiveapp.Deps{Store: st, Verifier: auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}), Version: "test", Health: fh})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &apiFixture{srv: srv, iss: iss, st: st}, a, b
}

func TestHealthPerCameraAndOverviewRespectScope(t *testing.T) {
	f, a, b := newHealthAPI(t)
	viewer := f.token(t, auth.RoleViewer, string(a.ID))
	r := f.do(t, "GET", "/v0/cameras/"+string(a.ID)+"/health", viewer, nil)
	if r.Status != 200 || r.Body["online"] != true || r.Body["streams"].(map[string]any)["main"].(map[string]any)["bitrate_kbps"] != float64(1500) {
		t.Fatalf("health a: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "GET", "/v0/cameras/"+string(b.ID)+"/health", viewer, nil); r.Status != 404 {
		t.Fatalf("ungranted health: %d", r.Status)
	}
	r = f.do(t, "GET", "/v0/health/cameras", viewer, nil)
	if r.Status != 200 || len(r.List) != 1 || r.List[0]["camera"] != string(a.ID) {
		t.Fatalf("overview for viewer: %d %v", r.Status, r.List)
	}
	owner := f.token(t, auth.RoleOwner, "*")
	if r := f.do(t, "GET", "/v0/health/cameras", owner, nil); r.Status != 200 || len(r.List) != 2 {
		t.Fatalf("overview for owner: %d %v", r.Status, r.List)
	}
}

func TestEventsRangeForTimeline(t *testing.T) {
	f, a, _ := newHealthAPI(t)
	viewer := f.token(t, auth.RoleViewer, string(a.ID))
	r := f.do(t, "GET", "/v0/cameras/"+string(a.ID)+"/events?from=2026-11-09T09:00:00Z&to=2026-11-09T11:00:00Z", viewer, nil)
	if r.Status != 200 || len(r.List) != 2 || r.List[0]["kind"] != "camera.offline" || r.List[1]["kind"] != "camera.online" {
		t.Fatalf("events: %d %v", r.Status, r.List)
	}
	if d, ok := r.List[0]["data"].(map[string]any); !ok || d["stream"] != "main" {
		t.Fatalf("event data must be an object: %v", r.List[0]["data"])
	}
	if r := f.do(t, "GET", "/v0/cameras/"+string(a.ID)+"/events?from=nope", viewer, nil); r.Status != 400 {
		t.Fatalf("bad range: %d", r.Status)
	}
}

func TestHealthNotReadyWithoutMonitor(t *testing.T) {
	f := newAPI(t)
	owner := f.token(t, auth.RoleOwner, "*")
	if r := f.do(t, "GET", "/v0/health/cameras", owner, nil); r.Status != 503 {
		t.Fatalf("no monitor: %d", r.Status)
	}
}
