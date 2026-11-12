package hiveapp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/video"
)

// whepEngine is a fake engine whose WHEP endpoint is an httptest server
// behaving like MediaMTX: POST answers 201 with a Location, PATCH and
// DELETE on that resource answer 204, anything else 404.
type whepEngine struct {
	video.Engine
	srv  *httptest.Server
	mu   sync.Mutex
	seen []string
}

func newWHEPEngine(t *testing.T) *whepEngine {
	t.Helper()
	e := &whepEngine{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.seen = append(e.seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type"))
		e.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/whep"):
			if strings.HasPrefix(r.URL.Path, "/gone/") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Location", "/cam_1_main/whep/sess-1")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("v=0 answer"))
		case r.URL.Path == "/cam_1_main/whep/sess-1" && (r.Method == http.MethodPatch || r.Method == http.MethodDelete):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *whepEngine) WHEPURL(cam video.CameraID, s video.Stream) string {
	return e.srv.URL + "/" + string(cam) + "_" + string(s) + "/whep"
}

func TestLocalLiveSignallingForwardsOfferCandidatesAndClose(t *testing.T) {
	iss, _ := auth.NewIssuer("k1")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cam, _ := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.9/s"}}, time.Now())
	if err := st.Cameras().Put(context.Background(), cam); err != nil {
		t.Fatal(err)
	}
	eng := newWHEPEngine(t)
	ver := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()})
	srv := httptest.NewServer(hiveapp.NewAPI(hiveapp.Deps{Store: st, Verifier: ver, Version: "test", Notify: func() {}, Engine: eng}))
	t.Cleanup(srv.Close)
	f := &apiFixture{srv: srv, iss: iss, st: st}
	viewer := f.token(t, auth.RoleViewer, string(cam.ID))

	// Offer → answer with a session handle pointing at the engine.
	st2, body := f.call(t, http.MethodPost, "/v0/cameras/"+string(cam.ID)+"/live/offer", viewer, map[string]string{"sdp": "v=0 offer", "stream": "main"})
	var ans struct{ SDP, Session string }
	_ = json.Unmarshal(body, &ans)
	if st2 != http.StatusCreated || ans.SDP != "v=0 answer" || !strings.HasPrefix(ans.Session, eng.srv.URL+"/cam_1_main/whep/") && !strings.Contains(ans.Session, "/whep/sess-1") {
		t.Fatalf("offer: %d %s", st2, body)
	}
	if st2, _ := f.call(t, http.MethodPost, "/v0/live/candidates", viewer, map[string]string{"session": ans.Session, "candidates": "a=mid:0\r\na=candidate:1 1 udp 1 10.0.0.2 5000 typ host\r\n"}); st2 != http.StatusNoContent {
		t.Fatalf("candidates: %d", st2)
	}
	if st2, _ := f.call(t, http.MethodPost, "/v0/live/close", viewer, map[string]string{"session": ans.Session}); st2 != http.StatusNoContent {
		t.Fatalf("close: %d", st2)
	}
	eng.mu.Lock()
	seen := strings.Join(eng.seen, "\n")
	eng.mu.Unlock()
	for _, want := range []string{"POST /" + string(cam.ID) + "_main/whep application/sdp", "PATCH /cam_1_main/whep/sess-1 application/trickle-ice-sdpfrag", "DELETE /cam_1_main/whep/sess-1 "} {
		if !strings.Contains(seen, want) {
			t.Errorf("engine did not see %q in:\n%s", want, seen)
		}
	}

	// Guard rails: a forged handle, a token without the camera, a bad offer, a missing engine.
	if st2, body := f.call(t, http.MethodPost, "/v0/live/close", viewer, map[string]string{"session": "https://evil.example/whep/x"}); st2 != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte("invalid")) {
		t.Fatalf("forged handle: %d %s", st2, body)
	}
	if st2, _ := f.call(t, http.MethodPost, "/v0/cameras/"+string(cam.ID)+"/live/offer", f.token(t, auth.RoleViewer, "cam_other"), map[string]string{"sdp": "v=0 offer"}); st2 != http.StatusNotFound {
		t.Fatalf("ungranted camera: %d", st2)
	}
	if st2, _ := f.call(t, http.MethodPost, "/v0/cameras/"+string(cam.ID)+"/live/offer", viewer, map[string]string{"sdp": "nope"}); st2 != http.StatusUnprocessableEntity {
		t.Fatalf("bad sdp: %d", st2)
	}
	plain := newAPI(t)
	if st2, body := plain.call(t, http.MethodPost, "/v0/live/close", plain.token(t, auth.RoleViewer, "*"), map[string]string{"session": "x"}); st2 != http.StatusServiceUnavailable || !bytes.Contains(body, []byte("not_ready")) {
		t.Fatalf("no engine: %d %s", st2, body)
	}
}

// statusEngine answers every WHEP offer with one status.
type statusEngine struct {
	video.Engine
	srv *httptest.Server
}

func (e statusEngine) WHEPURL(cam video.CameraID, s video.Stream) string {
	return e.srv.URL + "/" + string(cam) + "/" + string(s) + "/whep"
}

// A local live offer explains why there is no picture: switched-off camera,
// a stream the engine does not have, an engine that fails.
func TestLocalLiveOfferExplainsMissingPictures(t *testing.T) {
	iss, _ := auth.NewIssuer("k1")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	on, _ := camera.New(camera.Spec{Name: "On", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.9/a"}}, time.Now())
	off, _ := camera.New(camera.Spec{Name: "Off", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://10.0.0.9/b"}}, time.Now())
	off.Enabled = false
	for _, c := range []camera.Camera{on, off} {
		if err := st.Cameras().Put(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	status := http.StatusNotFound
	eng := statusEngine{srv: httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))}
	t.Cleanup(eng.srv.Close)
	srv := httptest.NewServer(hiveapp.NewAPI(hiveapp.Deps{Store: st, Verifier: auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}), Version: "test", Notify: func() {}, Engine: eng}))
	t.Cleanup(srv.Close)
	f := &apiFixture{srv: srv, iss: iss, st: st}
	tok := f.token(t, auth.RoleViewer, "*")
	offer := map[string]string{"sdp": "v=0 offer"}

	if r := f.do(t, "POST", "/v0/cameras/"+string(off.ID)+"/live/offer", tok, offer); r.Status != 404 || r.Body["message"] != "camera is not available" {
		t.Fatalf("switched-off camera: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "POST", "/v0/cameras/cam_nope/live/offer", tok, offer); r.Status != 404 || r.Body["message"] != "camera is not available" {
		t.Fatalf("unknown camera: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "POST", "/v0/cameras/"+string(on.ID)+"/live/offer", tok, offer); r.Status != 404 || r.Body["message"] != "stream is not available" {
		t.Fatalf("stream the engine lacks: %d %v", r.Status, r.Body)
	}
	status = http.StatusInternalServerError
	if r := f.do(t, "POST", "/v0/cameras/"+string(on.ID)+"/live/offer", tok, offer); r.Status != 502 || r.Body["code"] != "engine" {
		t.Fatalf("failing engine: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "POST", "/v0/cameras/"+string(on.ID)+"/live/offer", tok, map[string]string{"sdp": "hello"}); r.Status != 422 {
		t.Fatalf("not an SDP offer: %d %v", r.Status, r.Body)
	}
}
