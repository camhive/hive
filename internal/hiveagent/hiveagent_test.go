package hiveagent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/entitlements"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/hivetls"
	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/tunnel"
	"github.com/camhive/hive/internal/video"
)

type fakeHealth struct{ cams []health.CameraHealth }

func (f fakeHealth) All() []health.CameraHealth { return f.cams }

func newAgent(t *testing.T) (*hiveagent.Agent, *store.Store, *auth.Verifier, *int) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ver := auth.NewVerifier("hv_1", nil)
	notified := 0
	ag := hiveagent.New(hiveagent.Deps{Store: st, Verifier: ver, Version: "0.1.0", Notify: func() { notified++ },
		Health: fakeHealth{cams: []health.CameraHealth{{Camera: "a", Enabled: true, Online: true}, {Camera: "b", Enabled: true}, {Camera: "c"}}},
		Free:   func() (int64, error) { return 42 << 30, nil },
		Diag: func(context.Context) (string, []string, error) {
			return filepath.Join(t.TempDir(), "x.zip"), []string{"README.txt"}, nil
		},
	})
	return ag, st, ver, &notified
}

func run(t *testing.T, ag *hiveagent.Agent, name string, args any) protocol.Result {
	t.Helper()
	var raw json.RawMessage
	if args != nil {
		raw, _ = json.Marshal(args)
	}
	return ag.Handle(context.Background(), protocol.Command{Name: name, Args: raw})
}

func TestCameraCommandsMutateTheStoreWithStableCodes(t *testing.T) {
	ag, st, _, notified := newAgent(t)
	ctx := context.Background()

	if r := run(t, ag, "nope", nil); r.OK || r.Error != protocol.ErrCodeUnsupported {
		t.Fatalf("unknown = %+v", r)
	}
	if r := run(t, ag, "camera.add", json.RawMessage(`{"name": 5}`)); r.Error != protocol.ErrCodeBadArgs {
		t.Fatalf("bad args = %+v", r)
	}
	if r := run(t, ag, "camera.add", map[string]any{"name": "", "source": map[string]string{"kind": "rtsp", "address": "rtsp://x/1"}}); r.Error != protocol.ErrCodeInvalid {
		t.Fatalf("invalid = %+v", r)
	}
	r := run(t, ag, "camera.add", map[string]any{"name": "Door", "group": "Outside", "source": map[string]string{"kind": "rtsp", "address": "rtsp://10.0.0.5/main"},
		"credentials": map[string]string{"username": "admin", "password": "secret-pw"}})
	if !r.OK {
		t.Fatalf("add = %+v", r)
	}
	var added struct {
		ID, Name, Username string
		Enabled            bool
	}
	_ = json.Unmarshal(r.Data, &added)
	if added.Name != "Door" || added.Username != "admin" || !added.Enabled || bytes.Contains(r.Data, []byte("secret-pw")) {
		t.Fatalf("view = %s", r.Data)
	}
	cams, _ := st.Cameras().List(ctx)
	if len(cams) != 1 || cams[0].Credentials.Password != "secret-pw" {
		t.Fatal("camera must be stored with its password")
	}
	if *notified != 1 {
		t.Fatal("ingest must be notified")
	}

	r = run(t, ag, "camera.list", nil)
	var list []map[string]any
	_ = json.Unmarshal(r.Data, &list)
	if !r.OK || len(list) != 1 || list[0]["id"] != added.ID {
		t.Fatalf("list = %+v", r)
	}

	off := false
	if r := run(t, ag, "camera.update", map[string]any{"id": added.ID, "enabled": off, "name": "Front door"}); !r.OK {
		t.Fatalf("update = %+v", r)
	}
	c, _ := st.Cameras().Get(ctx, cams[0].ID)
	if c.Enabled || c.Name != "Front door" {
		t.Fatalf("stored = %+v", c)
	}
	if r := run(t, ag, "camera.update", map[string]any{"id": "cam_missing", "name": "x"}); r.Error != protocol.ErrCodeNotFound {
		t.Fatalf("update missing = %+v", r)
	}
	if r := run(t, ag, "camera.remove", map[string]any{"id": "cam_missing"}); r.Error != protocol.ErrCodeNotFound {
		t.Fatalf("remove missing = %+v", r)
	}
	if r := run(t, ag, "camera.remove", map[string]any{"id": added.ID}); !r.OK {
		t.Fatalf("remove = %+v", r)
	}
	if cams, _ := st.Cameras().List(ctx); len(cams) != 0 {
		t.Fatal("camera must be gone")
	}
	// Every mutation is in the local activity log with actor "cloud".
	actions, _ := st.Actions().Recent(ctx, 10)
	names := map[string]bool{}
	for _, a := range actions {
		if a.Actor != "cloud" {
			t.Fatalf("actor = %q", a.Actor)
		}
		names[a.Action] = true
		if bytes.Contains(a.Details, []byte("secret-pw")) {
			t.Fatal("password in action log")
		}
	}
	if !names["camera.add"] || !names["camera.update"] || !names["camera.remove"] {
		t.Fatalf("actions = %v", names)
	}
}

func TestCloudStateKeysGrantsLicenseAndRevocation(t *testing.T) {
	ag, st, ver, _ := newAgent(t)
	ctx := context.Background()
	iss, _ := auth.NewIssuer("k1")
	claims := auth.Claims{Account: "acc_1", HiveID: "hv_1", SessionID: "sid_1", Cameras: []string{"*"}, Role: auth.RoleViewer, IssuedAt: time.Now(), Expiry: time.Now().Add(10 * time.Minute)}
	tok, _ := iss.Issue(claims)
	if _, err := ver.Verify(tok); err == nil {
		t.Fatal("no keys yet: token must fail")
	}
	if r := run(t, ag, "keys.set", map[string]any{"keys": map[string]string{}}); r.Error != protocol.ErrCodeInvalid {
		t.Fatalf("empty keys = %+v", r)
	}
	keysJSON, _ := json.Marshal(auth.Keys{"k1": iss.PublicKey()})
	if r := run(t, ag, "keys.set", json.RawMessage(`{"keys":`+string(keysJSON)+`}`)); !r.OK {
		t.Fatalf("keys.set = %+v", r)
	}
	if _, err := ver.Verify(tok); err != nil {
		t.Fatalf("pushed key must verify: %v", err)
	}
	if keys, ok := hiveagent.LoadKeys(ctx, st); !ok || len(keys) != 1 {
		t.Fatal("keys must be persisted for the next start")
	}
	if r := run(t, ag, "session.revoke", map[string]any{"session_id": "sid_1"}); !r.OK {
		t.Fatalf("revoke = %+v", r)
	}
	if _, err := ver.Verify(tok); !errors.Is(err, auth.ErrRevoked) {
		t.Fatalf("revoked: %v", err)
	}
	if r := run(t, ag, "session.revoke", map[string]any{}); r.Error != protocol.ErrCodeInvalid {
		t.Fatalf("revoke without id = %+v", r)
	}
	if r := run(t, ag, "grants.set", json.RawMessage(`{"grants":[{"account":"acc_2","cameras":["*"],"role":"viewer"}]}`)); !r.OK {
		t.Fatalf("grants = %+v", r)
	}
	if r := run(t, ag, "license.set", json.RawMessage(`{"token":"x"}`)); r.Error != protocol.ErrCodeNotReady {
		t.Fatalf("license without licensing = %+v", r)
	}
	if _, _, err := st.CloudState().Get(ctx, "nothing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("missing state")
	}
	if r := run(t, ag, "tunnel.open", nil); r.Error != protocol.ErrCodeNotReady {
		t.Fatalf("tunnel.open without relay = %+v", r)
	}
	ag.SetRelay(hiveagent.Relay{}) // identity but no way into the API's relay listener
	if r := run(t, ag, "tunnel.open", nil); r.Error != protocol.ErrCodeNotReady {
		t.Fatalf("tunnel.open without a relay listener = %+v", r)
	}
	for _, sig := range []string{"signal.offer", "signal.candidate", "signal.close"} {
		if r := run(t, ag, sig, nil); r.Error != protocol.ErrCodeNotReady {
			t.Fatalf("%s without engine = %+v", sig, r)
		}
	}
	r := run(t, ag, "diag.bundle", nil)
	if !r.OK || !bytes.Contains(r.Data, []byte("README.txt")) {
		t.Fatalf("diag = %+v", r)
	}
	if r := run(t, ag, "ping", nil); !r.OK || !bytes.Contains(r.Data, []byte(`"version":"0.1.0"`)) {
		t.Fatalf("ping = %+v", r)
	}
}

func TestNotReadyWithoutStore(t *testing.T) {
	ag := hiveagent.New(hiveagent.Deps{})
	for _, name := range []string{"camera.list", "camera.add", "grants.set", "diag.bundle", "session.revoke", "license.set"} {
		r := run(t, ag, name, json.RawMessage(`{"session_id":"s","x":1}`))
		if r.Error != protocol.ErrCodeNotReady {
			t.Fatalf("%s = %+v", name, r)
		}
	}
	if len(hiveagent.Commands()) < 10 {
		t.Fatal("command list")
	}
}

func TestHeartbeatAndQueuedEventsFlushWhenConnected(t *testing.T) {
	ag, _, _, _ := newAgent(t)
	hb := ag.Heartbeat()
	if hb.Cameras != (hiveagent.CamSum{Total: 3, Enabled: 2, Online: 1}) || hb.Disk.FreeBytes != 42<<30 || !hb.Disk.Known || hb.Version != "0.1.0" {
		t.Fatalf("heartbeat = %+v", hb)
	}

	var mu sync.Mutex
	var got []protocol.Event
	connected := false
	emit := func(_ context.Context, e protocol.Event) error {
		mu.Lock()
		defer mu.Unlock()
		if !connected {
			return tunnel.ErrNotConnected
		}
		got = append(got, e)
		return nil
	}
	ag.Emit("camera.offline", map[string]string{"camera": "a"})
	ag.Emit(hiveagent.EventStorageLow, map[string]int64{"free_bytes": 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx, emit, 50*time.Millisecond)
	time.Sleep(150 * time.Millisecond)
	if ag.Pending() != 2 {
		t.Fatalf("offline: %d queued", ag.Pending())
	}
	mu.Lock()
	connected = true
	mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 && ag.Pending() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, 0, len(got))
	for _, e := range got {
		names = append(names, e.Name)
	}
	if len(got) < 3 || ag.Pending() != 0 {
		t.Fatalf("events = %v pending=%d", names, ag.Pending())
	}
	// Queued events keep their order and come before later heartbeats.
	first, second := -1, -1
	for i, e := range got {
		if e.Name == "camera.offline" && first < 0 {
			first = i
		}
		if e.Name == hiveagent.EventStorageLow && second < 0 {
			second = i
		}
	}
	if first < 0 || second < first {
		t.Fatalf("order = %v", names)
	}
}

type fakeICEEngine struct {
	video.Engine
	servers []video.ICEServer
}

func (f *fakeICEEngine) SetICEServers(_ context.Context, s []video.ICEServer) error {
	f.servers = s
	return nil
}

func (f *fakeICEEngine) WHEPURL(video.CameraID, video.Stream) string {
	return "http://127.0.0.1:1/x/whep"
}

func TestICESetIsStoredAppliedAndReplayedOnEngineStart(t *testing.T) {
	ag, st, _, _ := newAgent(t)
	doc := json.RawMessage(`{"servers":[{"urls":["stun:stun.test:3478"]},{"urls":["turn:turn.test:3478?transport=udp"],"username":"1700000000:hv_1","credential":"abc"}]}`)
	// No engine yet: stored, not applied.
	r := run(t, ag, "ice.set", doc)
	if !r.OK || !bytes.Contains(r.Data, []byte(`"applied":false`)) {
		t.Fatalf("ice.set without engine = %+v", r)
	}
	if _, _, err := st.CloudState().Get(context.Background(), hiveagent.StateICE); err != nil {
		t.Fatal("ice document must be stored")
	}
	// Engine appears: the stored servers are replayed.
	eng := &fakeICEEngine{}
	ag.SetEngine(eng)
	if len(eng.servers) != 2 || eng.servers[1].Username != "1700000000:hv_1" || eng.servers[1].Password != "abc" {
		t.Fatalf("replayed servers = %+v", eng.servers)
	}
	// A new push applies at once.
	r = run(t, ag, "ice.set", json.RawMessage(`{"servers":[{"urls":["stun:other:3478"]}]}`))
	if !r.OK || !bytes.Contains(r.Data, []byte(`"applied":true`)) || len(eng.servers) != 1 || eng.servers[0].URL != "stun:other:3478" {
		t.Fatalf("ice.set with engine = %+v servers=%+v", r, eng.servers)
	}
	if r := run(t, ag, "ice.set", json.RawMessage(`{"servers": 5}`)); r.Error != protocol.ErrCodeBadArgs {
		t.Fatalf("bad ice = %+v", r)
	}
}

// A hive id, not a secret (the literal next to "keyPath" trips scanners).
const certTestHive = "hv_0123456789abcdef"

func TestCertSetInstallsOnlyAMatchingChain(t *testing.T) {
	ag, _, _, _ := newAgent(t)
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "public.crt"), filepath.Join(dir, "public.key")
	self, err := hivetls.LoadOrCreate(certPath, keyPath, certTestHive)
	if err != nil {
		t.Fatal(err)
	}
	holder := hivetls.NewHolder(self)
	if r := run(t, ag, "cert.set", map[string]string{"chain_pem": "x"}); r.Error != protocol.ErrCodeNotReady {
		t.Fatalf("cert.set without tls = %+v", r)
	}
	ag.SetTLS(hiveagent.TLSInfo{HiveID: certTestHive, Holder: holder, CertPath: certPath, KeyPath: keyPath, Port: 16443})
	hb := ag.Heartbeat()
	if hb.LAN == nil || hb.LAN.TLSPort != 16443 || hb.LAN.CertIssuer != "self-signed" || hb.LAN.CertFingerprint != hivetls.Fingerprint(self) {
		t.Fatalf("heartbeat lan = %+v", hb.LAN)
	}
	// A chain for another key is refused; a chain for this key is installed.
	other, _ := hivetls.LoadOrCreate(filepath.Join(dir, "o.crt"), filepath.Join(dir, "o.key"), certTestHive)
	otherPEM, _ := os.ReadFile(filepath.Join(dir, "o.crt"))
	if r := run(t, ag, "cert.set", map[string]string{"chain_pem": string(otherPEM)}); r.Error != protocol.ErrCodeInvalid {
		t.Fatalf("foreign key chain = %+v", r)
	}
	_ = other
	selfPEM, _ := os.ReadFile(certPath)
	if r := run(t, ag, "cert.set", map[string]string{"chain_pem": string(selfPEM)}); !r.OK {
		t.Fatalf("own chain = %+v", r)
	}
	// Self-signed (as it still is: our own chain is self-issued) → a reconnect asks the cloud again.
	before := ag.Pending()
	ag.RequestCertIfNeeded()
	if ag.Pending() != before+1 {
		t.Fatalf("RequestCertIfNeeded must queue a cert.csr event: pending %d → %d", before, ag.Pending())
	}
}

func TestLicenseSetVerifiesStoresRestoresAndGates(t *testing.T) {
	_, st, ver, _ := newAgent(t)
	iss, _ := auth.NewIssuer("k1")
	ver.SetKeys(auth.Keys{"k1": iss.PublicKey()})
	now := time.Now()
	lic := hiveagent.NewLicensing(func() time.Time { return now })
	// Attach licensing through a fresh agent sharing the store and verifier.
	ag := hiveagent.New(hiveagent.Deps{Store: st, Verifier: ver, License: lic, Version: "t", Now: func() time.Time { return now }})
	if lic.State() != auth.LicenseNone || lic.Effective().Int(entitlements.HivesMax) != 1 || lic.Usable() {
		t.Fatal("no license: Free seed, offline tokens refused")
	}
	pro := entitlements.OnPlan(entitlements.PlanPro).Set
	good, _ := iss.IssueLicense(auth.License{HiveID: "hv_1", Entitlements: pro, IssuedAt: now, ExpiresAt: now.Add(auth.LicenseTTL)})
	other, _ := auth.NewIssuer("k1")
	forged, _ := other.IssueLicense(auth.License{HiveID: "hv_1", Entitlements: pro, IssuedAt: now, ExpiresAt: now.Add(auth.LicenseTTL)})
	foreign, _ := iss.IssueLicense(auth.License{HiveID: "hv_2", Entitlements: pro, IssuedAt: now, ExpiresAt: now.Add(auth.LicenseTTL)})
	for _, bad := range []string{forged, foreign, "garbage"} {
		if r := run(t, ag, "license.set", map[string]string{"token": bad}); r.Error != protocol.ErrCodeInvalid {
			t.Fatalf("bad license = %+v", r)
		}
	}
	if r := run(t, ag, "license.set", map[string]string{"token": good}); !r.OK || !bytes.Contains(r.Data, []byte(`"state":"valid"`)) {
		t.Fatalf("license.set = %+v", r)
	}
	if lic.State() != auth.LicenseValid || lic.Effective().Int(entitlements.HivesMax) != 10 || !lic.Usable() {
		t.Fatalf("licensed = %s %v", lic.State(), lic.Effective())
	}
	if hb := ag.Heartbeat(); hb.License != "valid" {
		t.Fatalf("heartbeat license = %q", hb.License)
	}
	// Restart: the stored license is restored; after grace the Free seed applies.
	lic2 := hiveagent.NewLicensing(func() time.Time { return now })
	ag2 := hiveagent.New(hiveagent.Deps{Store: st, Verifier: ver, License: lic2, Version: "t", Now: func() time.Time { return now }})
	ag2.LoadLicense(context.Background())
	if lic2.State() != auth.LicenseValid {
		t.Fatalf("restored = %s", lic2.State())
	}
	late := now.Add(auth.LicenseTTL + auth.LicenseGrace + time.Hour)
	lic3 := hiveagent.NewLicensing(func() time.Time { return late })
	lic3.Set(lic2.Current())
	if lic3.State() != auth.LicenseExpired || lic3.Effective().Int(entitlements.HivesMax) != 1 || lic3.Usable() {
		t.Fatalf("expired = %s %v", lic3.State(), lic3.Effective())
	}
	grace := now.Add(auth.LicenseTTL + 24*time.Hour)
	lic4 := hiveagent.NewLicensing(func() time.Time { return grace })
	lic4.Set(lic2.Current())
	if lic4.State() != auth.LicenseGraceOn || !lic4.Usable() || lic4.Effective().Int(entitlements.HivesMax) != 10 {
		t.Fatal("grace keeps the licensed set and offline tokens")
	}
}
