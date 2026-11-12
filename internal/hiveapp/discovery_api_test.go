package hiveapp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/onvif"
	"github.com/camhive/hive/internal/store"
)

type fakeDiscovery struct {
	found []onvif.Discovered
	creds onvif.Credentials
	err   error
}

func (f *fakeDiscovery) Discover(context.Context) ([]onvif.Discovered, error) { return f.found, f.err }
func (f *fakeDiscovery) Probe(_ context.Context, xaddr string, creds onvif.Credentials) (hiveapp.ProbeResult, error) {
	if creds != f.creds {
		return hiveapp.ProbeResult{}, onvif.ErrUnauthorized
	}
	if xaddr == "http://dead/onvif/device_service" {
		return hiveapp.ProbeResult{}, errors.New("dial tcp: no route")
	}
	return hiveapp.ProbeResult{Device: onvif.DeviceInfo{Manufacturer: "Camsim", Model: "Sim-1"}, Profiles: []hiveapp.ProbedProfile{
		{Token: "p-main", Name: "main", Encoding: "H264", Width: 1920, Height: 1080, FPS: 25, URL: "rtsp://cam/main"},
		{Token: "p-sub", Name: "sub", Encoding: "H264", Width: 640, Height: 360, FPS: 15, URL: "rtsp://cam/sub"},
	}}, nil
}

func newAPIWithDiscovery(t *testing.T, d hiveapp.Discoverer) *apiFixture {
	t.Helper()
	iss, _ := auth.NewIssuer("k1")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ver := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()})
	srv := httptest.NewServer(hiveapp.NewAPI(hiveapp.Deps{Store: st, Verifier: ver, Version: "test", Notify: func() {}, Discovery: d}))
	t.Cleanup(srv.Close)
	return &apiFixture{srv: srv, iss: iss, st: st}
}

func (f *apiFixture) call(t *testing.T, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out bytes.Buffer
	_, _ = out.ReadFrom(res.Body)
	return res.StatusCode, out.Bytes()
}

// The owner discovers cameras, probes one with credentials, and adds it with
// the probed streams; the ingest then has URLs to pull.
func TestDiscoverProbeAndAddONVIFCamera(t *testing.T) {
	disc := &fakeDiscovery{found: []onvif.Discovered{{XAddr: "http://10.0.0.5/onvif/device_service", Name: "Door", Scopes: []string{"onvif://www.onvif.org/name/Door"}}}, creds: onvif.Credentials{Username: "admin", Password: "pw"}}
	f := newAPIWithDiscovery(t, disc)
	owner := f.token(t, auth.RoleOwner, "*")

	st, body := f.call(t, http.MethodGet, "/v0/discovery", owner, nil)
	var found []map[string]any
	_ = json.Unmarshal(body, &found)
	if st != http.StatusOK || len(found) != 1 || found[0]["xaddr"] != "http://10.0.0.5/onvif/device_service" || found[0]["name"] != "Door" {
		t.Fatalf("discover: %d %s", st, body)
	}
	if st, _ := f.call(t, http.MethodGet, "/v0/discovery", f.token(t, auth.RoleOperator, "*"), nil); st != http.StatusForbidden {
		t.Fatalf("operator must not discover: %d", st)
	}

	// Probe: wrong credentials are told apart from an unreachable camera.
	st, body = f.call(t, http.MethodPost, "/v0/discovery/probe", owner, map[string]any{"address": "http://10.0.0.5/onvif/device_service", "credentials": map[string]string{"username": "admin", "password": "wrong"}})
	if st != http.StatusBadGateway || !bytes.Contains(body, []byte(`"camera_auth"`)) {
		t.Fatalf("bad credentials: %d %s", st, body)
	}
	st, body = f.call(t, http.MethodPost, "/v0/discovery/probe", owner, map[string]any{"address": "http://dead/onvif/device_service", "credentials": map[string]string{"username": "admin", "password": "pw"}})
	if st != http.StatusBadGateway || !bytes.Contains(body, []byte(`"camera"`)) || bytes.Contains(body, []byte(`"camera_auth"`)) {
		t.Fatalf("dead camera: %d %s", st, body)
	}
	if st, _ := f.call(t, http.MethodPost, "/v0/discovery/probe", owner, map[string]any{}); st != http.StatusUnprocessableEntity {
		t.Fatalf("missing address: %d", st)
	}
	st, body = f.call(t, http.MethodPost, "/v0/discovery/probe", owner, map[string]any{"address": "http://10.0.0.5/onvif/device_service", "credentials": map[string]string{"username": "admin", "password": "pw"}})
	var probe hiveapp.ProbeResult
	_ = json.Unmarshal(body, &probe)
	if st != http.StatusOK || probe.Device.Model != "Sim-1" || len(probe.Profiles) != 2 || probe.Profiles[0].URL != "rtsp://cam/main" {
		t.Fatalf("probe: %d %s", st, body)
	}

	// Add the camera with the two probed streams.
	st, body = f.call(t, http.MethodPost, "/v0/cameras", owner, map[string]any{
		"name": "Door", "group": "Outside", "source": map[string]string{"kind": "onvif", "address": "http://10.0.0.5/onvif/device_service"},
		"credentials": map[string]string{"username": "admin", "password": "pw"},
		"streams": map[string]any{
			"main": map[string]any{"url": "rtsp://cam/main", "profile_token": "p-main", "codec": "H264", "width": 1920, "height": 1080},
			"sub":  map[string]any{"url": "rtsp://cam/sub", "profile_token": "p-sub", "codec": "H264", "width": 640, "height": 360},
		},
	})
	var cam struct {
		ID      string `json:"id"`
		Streams map[string]struct {
			URL          string `json:"URL"`
			ProfileToken string `json:"ProfileToken"`
			Codec        string `json:"Codec"`
		} `json:"streams"`
	}
	_ = json.Unmarshal(body, &cam)
	if st != http.StatusCreated || cam.Streams["main"].URL != "rtsp://cam/main" || cam.Streams["sub"].ProfileToken != "p-sub" || cam.Streams["main"].Codec != "H264" {
		t.Fatalf("create: %d %s", st, body)
	}

	// Without a discoverer the endpoint says so instead of failing oddly.
	plain := newAPI(t)
	if st, body := plain.call(t, http.MethodGet, "/v0/discovery", plain.token(t, auth.RoleOwner, "*"), nil); st != http.StatusServiceUnavailable || !bytes.Contains(body, []byte("not_ready")) {
		t.Fatalf("no discoverer: %d %s", st, body)
	}
}
