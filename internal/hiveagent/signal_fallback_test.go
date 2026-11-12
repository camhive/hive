package hiveagent_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/video"
)

type pathEngine struct {
	video.Engine
	base string
}

func (e pathEngine) WHEPURL(cam video.CameraID, s video.Stream) string {
	return e.base + "/" + string(cam) + "/" + string(s) + "/whep"
}

// A camera added by RTSP address has only a main stream; a viewer whose
// plan (or grid) asks for sub must still get a picture, and the other way
// round for a camera that only offers sub.
func TestSignalOfferFallsBackToTheStreamTheCameraHas(t *testing.T) {
	mainOnly, _ := camera.New(camera.Spec{Name: "m", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://x/m"}}, time.Now())
	if got := hiveagent.ResolveStream(&mainOnly, "sub"); got != video.Main {
		t.Fatalf("sub on a main-only camera = %s", got)
	}
	if got := hiveagent.ResolveStream(&mainOnly, ""); got != video.Main {
		t.Fatalf("empty = %s", got)
	}
	both, _ := camera.New(camera.Spec{Name: "b", Source: camera.Source{Kind: camera.SourceONVIF, Address: "http://x/onvif/device_service"},
		Streams: map[camera.Stream]camera.StreamSpec{camera.Main: {URL: "rtsp://x/main"}, camera.Sub: {URL: "rtsp://x/sub"}}}, time.Now())
	if got := hiveagent.ResolveStream(&both, "sub"); got != video.Sub {
		t.Fatalf("sub on a camera with sub = %s", got)
	}
	if got := hiveagent.ResolveStream(nil, "sub"); got != video.Sub {
		t.Fatalf("no store keeps the request: %s", got)
	}

	// End to end through the agent: the engine sees the main path.
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		w.Header().Set("Location", "/s1")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0 answer"))
	}))
	defer srv.Close()
	ag, st, _, _ := newAgent(t)
	if err := st.Cameras().Put(t.Context(), mainOnly); err != nil {
		t.Fatal(err)
	}
	ag.SetEngine(pathEngine{base: srv.URL})
	r := run(t, ag, "signal.offer", map[string]string{"camera": string(mainOnly.ID), "stream": "sub", "sdp": "v=0 offer"})
	if !r.OK {
		t.Fatalf("offer = %+v", r)
	}
	if len(seen) != 1 || !strings.HasSuffix(seen[0], "/"+string(mainOnly.ID)+"/main/whep") {
		t.Fatalf("engine path = %v", seen)
	}
}

// #58: live through the cloud is remote; the cloud passes the hive owner's
// remote quality and the hive serves sub whatever the viewer asked for.
func TestSignalOfferServesThePlanQuality(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		w.Header().Set("Location", "/s1")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0 answer"))
	}))
	defer srv.Close()
	both, _ := camera.New(camera.Spec{Name: "b", Source: camera.Source{Kind: camera.SourceONVIF, Address: "http://x/onvif/device_service"},
		Streams: map[camera.Stream]camera.StreamSpec{camera.Main: {URL: "rtsp://x/main"}, camera.Sub: {URL: "rtsp://x/sub"}}}, time.Now())
	ag, st, _, _ := newAgent(t)
	if err := st.Cameras().Put(t.Context(), both); err != nil {
		t.Fatal(err)
	}
	ag.SetEngine(pathEngine{base: srv.URL})
	for _, c := range []struct{ quality, want string }{{"sub", "sub"}, {"full", "main"}, {"", "main"}} {
		r := run(t, ag, "signal.offer", map[string]string{"camera": string(both.ID), "stream": "main", "sdp": "v=0 offer", "quality": c.quality})
		if !r.OK || !strings.HasSuffix(seen[len(seen)-1], "/"+string(both.ID)+"/"+c.want+"/whep") {
			t.Fatalf("quality %q: %+v, engine path %v", c.quality, r, seen)
		}
	}
}
