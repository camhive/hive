package hiveapp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/store"
)

// #109: hive-updater runs its own copy of the hive, so `hive status` asks
// the running hive, over the loopback /healthz the updater already uses,
// which version runs and what the updater did; the answer goes only to
// this machine.
func TestHealthzTellsThisMachineWhatTheUpdaterDid(t *testing.T) {
	f := newAPIWith(t, func(d *hiveapp.Deps) { d.Version = "0.1.6 (a73f337, 2026-11-15)" })
	ctx := context.Background()
	at := time.Date(2026, 11, 16, 3, 12, 0, 0, time.UTC)
	for _, a := range []store.Action{
		{At: at.Add(-time.Hour), Actor: "updater", Action: "update.available", Target: "hive", Details: json.RawMessage(`{"offered":"0.1.6"}`)},
		{At: at, Actor: "updater", Action: "update.applied", Target: "hive", Details: json.RawMessage(`{"from":"0.1.5","to":"0.1.6"}`)},
		{At: at.Add(time.Hour), Actor: "updater", Action: "update.available", Target: "hive", Details: json.RawMessage(`{"offered":"0.1.7"}`)},
	} {
		if err := f.st.Actions().Append(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	got := healthz(t, f.srv.URL)
	if got.Version != "0.1.6 (a73f337, 2026-11-15)" || got.Updates == nil {
		t.Fatalf("healthz = %+v", got)
	}
	u := got.Updates
	if u.Available != "0.1.7" || u.Last == nil || u.Last.Action != "update.applied" || u.Last.From != "0.1.5" || u.Last.To != "0.1.6" || !u.Last.At.Equal(at) {
		t.Fatalf("updates = %+v last %+v", u, u.Last)
	}

	relayed := f.via(t, hiveapp.RelayedConn)
	if got := healthz(t, relayed.srv.URL); got.Updates != nil || got.Version == "" {
		t.Fatalf("relayed healthz = %+v", got)
	}
}

func healthz(t *testing.T, base string) hiveapp.Health {
	t.Helper()
	res, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var h hiveapp.Health
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	return h
}

// A browser on the hive's machine is loopback too, and /healthz answers any
// origin: a web page there must not read the patch level or the camera
// count. Browsers send Origin on cross-origin requests; hive-updater and
// the hive CLI never do (#120 review).
func TestHealthzKeepsTheLocalExtrasFromWebPages(t *testing.T) {
	f := newAPIWith(t, func(d *hiveapp.Deps) {
		d.Version = "0.1.6"
		d.Health = fakeHealth{cams: map[string]health.CameraHealth{"cam_a": {Camera: "cam_a", Enabled: true, Online: true}}}
	})
	if got := healthz(t, f.srv.URL); got.Updates == nil || got.Recording == nil {
		t.Fatalf("local tool: %+v", got)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, f.srv.URL+"/healthz", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body["status"] != "ok" || body["version"] != "0.1.6" || body["updates"] != nil || body["recording"] != nil {
		t.Fatalf("web page got %v", body)
	}
}
