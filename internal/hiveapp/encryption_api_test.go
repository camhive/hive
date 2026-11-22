package hiveapp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/hiveapp"
)

func init() { archivekey.WorkFactor = 10 } // fast scrypt in tests

const passphrase = "correct horse battery"

// encFixture is the archive API with the archive key and the cloud events
// the hive emits.
type encFixture struct {
	*apiFixture
	owner, viewer string
	camera        string
	vault         *archivekey.Vault
	clock         *testClock // the automatic lock's clock (#147)
	autolock      *archivekey.Autolock
	deps          *hiveapp.Deps
	mu            sync.Mutex
	emitted       []string
	sent          []any // the data of each emitted event
}

func newEncFixture(t *testing.T) *encFixture {
	t.Helper()
	f := &encFixture{vault: archivekey.New(t.TempDir()), clock: &testClock{t: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)}}
	f.autolock = archivekey.NewAutolock(f.vault, archivekey.WithAutolockClock(f.clock.now, f.clock.since))
	exportDir := t.TempDir()
	api, _, cam := newArchiveAPIWith(t, func(d *hiveapp.Deps) {
		d.Vault = f.vault
		d.Autolock = f.autolock
		f.deps = d
		d.Exports = export.NewManager(exportDir, d.Playback, d.Store.Exports(), export.WithVault(f.vault))
		d.Emit = func(name string, data any) {
			f.mu.Lock()
			f.emitted, f.sent = append(f.emitted, name), append(f.sent, data)
			f.mu.Unlock()
		}
	})
	f.apiFixture, f.camera = api, string(cam.ID)
	f.owner, f.viewer = api.token(t, auth.RoleOwner, "*"), api.token(t, auth.RoleViewer, "*")
	return f
}

func (f *encFixture) state(t *testing.T, tok string) any {
	t.Helper()
	r := f.do(t, "GET", "/v0/archive/encryption", tok, nil)
	if r.Status != 200 {
		t.Fatalf("state: %d %v", r.Status, r.Body)
	}
	return r.Body["state"]
}

func (f *encFixture) post(t *testing.T, path, tok string, body any) apiResp {
	t.Helper()
	return f.do(t, "POST", "/v0/archive/encryption"+path, tok, body)
}

func (f *encFixture) play(t *testing.T) apiResp {
	t.Helper()
	return f.do(t, "GET", "/v0/cameras/"+f.camera+"/archive/play?start=2026-11-09T10:00:00Z&duration=10", f.owner, nil)
}

func wantCode(t *testing.T, what string, r apiResp, status int, code string) {
	t.Helper()
	if r.Status != status || r.Body["code"] != code {
		t.Fatalf("%s: %d %v, want %d %s", what, r.Status, r.Body, status, code)
	}
}

func TestArchiveEncryptionIsOffUntilTheOwnerEnablesIt(t *testing.T) {
	f := newEncFixture(t)
	if s := f.state(t, f.viewer); s != "off" {
		t.Fatalf("state %v, want off", s)
	}
	wantCode(t, "unlock while off", f.post(t, "/unlock", f.owner, map[string]string{"passphrase": passphrase}), 409, "encryption_off")
	wantCode(t, "key file while off", f.do(t, "GET", "/v0/archive/encryption/key-file", f.owner, nil), 409, "encryption_off")
	if r := f.play(t); r.Status != 200 {
		t.Fatalf("play with encryption off: %d", r.Status)
	}
	if r := f.do(t, "GET", "/v0/archive/encryption", f.viewer, nil); r.Body["sealing"] != false {
		t.Fatalf("sealing while off: %v", r.Body)
	}
	f.post(t, "", f.owner, map[string]string{"passphrase": passphrase})
	f.post(t, "/lock", f.owner, nil)
	if r := f.do(t, "GET", "/v0/archive/encryption", f.viewer, nil); r.Body["sealing"] != true {
		t.Fatalf("sealing once on, locked: %v", r.Body)
	}
}

// The hive status carries the state, so the app shows a locked archive in
// the hive list and on every camera without another request.
func TestTheHiveStatusSaysWhetherTheArchiveIsLocked(t *testing.T) {
	f := newEncFixture(t)
	hiveState := func() any { return f.do(t, "GET", "/v0/hive", f.viewer, nil).Body["archive_encryption"] }
	if s := hiveState(); s != "off" {
		t.Fatalf("fresh hive: %v", s)
	}
	f.post(t, "", f.owner, map[string]string{"passphrase": passphrase})
	f.post(t, "/lock", f.owner, nil)
	if s := hiveState(); s != "locked" {
		t.Fatalf("after lock: %v", s)
	}
}

func TestOnlyTheOwnerEnablesEncryptionWithAStrongPassphrase(t *testing.T) {
	f := newEncFixture(t)
	wantCode(t, "viewer", f.post(t, "", f.viewer, map[string]string{"passphrase": passphrase}), 403, "forbidden")
	wantCode(t, "weak", f.post(t, "", f.owner, map[string]string{"passphrase": "short"}), 422, "weak_password")
	if r := f.post(t, "", f.owner, map[string]string{"passphrase": passphrase}); r.Status != 200 || r.Body["state"] != "unlocked" {
		t.Fatalf("enable: %d %v", r.Status, r.Body)
	}
	wantCode(t, "enable twice", f.post(t, "", f.owner, map[string]string{"passphrase": passphrase}), 409, "encryption_on")
}

// The locked archive (ADR-0019 §3): playback, sound and exports answer 423
// until the owner unlocks it; the state tells every member why.
func TestALockedArchivePlaysNothingUntilTheOwnerUnlocksIt(t *testing.T) {
	f := newEncFixture(t)
	if r := f.post(t, "", f.owner, map[string]string{"passphrase": passphrase}); r.Status != 200 {
		t.Fatalf("enable: %d %v", r.Status, r.Body)
	}
	if r := f.post(t, "/lock", f.owner, nil); r.Status != 200 || r.Body["state"] != "locked" {
		t.Fatalf("lock: %d %v", r.Status, r.Body)
	}
	if s := f.state(t, f.viewer); s != "locked" {
		t.Fatalf("a member sees %v, want locked", s)
	}
	wantCode(t, "play", f.play(t), 423, "archive_locked")
	wantCode(t, "audio", f.do(t, "GET", "/v0/cameras/"+f.camera+"/archive/audio?start=2026-11-09T10:00:00Z&duration=10", f.owner, nil), 423, "archive_locked")
	wantCode(t, "export", f.do(t, "POST", "/v0/cameras/"+f.camera+"/exports", f.owner, map[string]any{"start": "2026-11-09T10:00:00Z", "duration_seconds": 10}), 423, "archive_locked")
	if r := f.do(t, "GET", "/v0/cameras/"+f.camera+"/archive/timeline?from=2026-11-09T00:00:00Z&to=2026-11-10T00:00:00Z", f.viewer, nil); r.Status != 200 || len(r.List) != 3 {
		t.Fatalf("the timeline stays readable (the index is not encrypted): %d %v", r.Status, r.List)
	}

	wantCode(t, "viewer unlocks", f.post(t, "/unlock", f.viewer, map[string]string{"passphrase": passphrase}), 403, "forbidden")
	wantCode(t, "wrong passphrase", f.post(t, "/unlock", f.owner, map[string]string{"passphrase": "not the passphrase"}), 403, "wrong_passphrase")
	if r := f.post(t, "/unlock", f.owner, map[string]string{"passphrase": passphrase}); r.Status != 200 || r.Body["state"] != "unlocked" {
		t.Fatalf("unlock: %d %v", r.Status, r.Body)
	}
	if r := f.play(t); r.Status != 200 {
		t.Fatalf("play after unlock: %d %v", r.Status, r.Body)
	}
}

// Enable, lock, unlock and passphrase changes are audited in the action log
// and sent to the cloud, never with the passphrase.
func TestEncryptionChangesAreAuditedWithoutThePassphrase(t *testing.T) {
	f := newEncFixture(t)
	f.post(t, "", f.owner, map[string]string{"passphrase": passphrase})
	f.post(t, "/lock", f.owner, nil)
	f.post(t, "/unlock", f.owner, map[string]string{"passphrase": "not the passphrase"})
	f.post(t, "/unlock", f.owner, map[string]string{"passphrase": passphrase})
	if r := f.post(t, "/passphrase", f.owner, map[string]string{"passphrase": passphrase, "new_passphrase": "another long passphrase"}); r.Status != 204 {
		t.Fatalf("change: %d %v", r.Status, r.Body)
	}
	r := f.do(t, "GET", "/v0/actions", f.owner, nil)
	got := make([]string, 0, len(r.List))
	for _, a := range r.List {
		got = append(got, a["action"].(string))
		if raw, _ := a["details"].(map[string]any); strings.Contains(strings.ToLower(strings.Join(keys(raw), ",")), "pass") {
			t.Errorf("action %v carries %v", a["action"], raw)
		}
	}
	want := []string{"archive.encryption.passphrase_change", "archive.encryption.unlock", "archive.encryption.unlock_failed", "archive.encryption.lock", "archive.encryption.enable"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("actions %v, want %v", got, want)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Join(f.emitted, " ") != "archive.encryption.enable archive.encryption.lock archive.encryption.unlock_failed archive.encryption.unlock archive.encryption.passphrase_change" {
		t.Fatalf("sent to the cloud: %v", f.emitted)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestChangingThePassphraseNeedsTheCurrentOne(t *testing.T) {
	f := newEncFixture(t)
	f.post(t, "", f.owner, map[string]string{"passphrase": passphrase})
	wantCode(t, "wrong current", f.post(t, "/passphrase", f.owner, map[string]string{"passphrase": "not the passphrase", "new_passphrase": "another long passphrase"}), 403, "wrong_passphrase")
	wantCode(t, "weak new", f.post(t, "/passphrase", f.owner, map[string]string{"passphrase": passphrase, "new_passphrase": "short"}), 422, "weak_password")
	f.post(t, "/passphrase", f.owner, map[string]string{"passphrase": passphrase, "new_passphrase": "another long passphrase"})
	f.post(t, "/lock", f.owner, nil)
	wantCode(t, "old passphrase", f.post(t, "/unlock", f.owner, map[string]string{"passphrase": passphrase}), 403, "wrong_passphrase")
	if r := f.post(t, "/unlock", f.owner, map[string]string{"passphrase": "another long passphrase"}); r.Status != 200 {
		t.Fatalf("new passphrase: %d %v", r.Status, r.Body)
	}
}

func TestGuessingThePassphraseIsRateLimited(t *testing.T) {
	f := newEncFixture(t)
	f.post(t, "", f.owner, map[string]string{"passphrase": passphrase})
	f.post(t, "/lock", f.owner, nil)
	for range archivekey.MaxFailures {
		f.post(t, "/unlock", f.owner, map[string]string{"passphrase": "not the passphrase"})
	}
	r := f.post(t, "/unlock", f.owner, map[string]string{"passphrase": passphrase})
	wantCode(t, "after the guesses", r, 429, "unlock_rate_limited")
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
}

// The owner's backup: the keyring as stored, which the stock age tool
// opens with the passphrase.
func TestTheOwnerDownloadsTheKeyFile(t *testing.T) {
	f := newEncFixture(t)
	f.post(t, "", f.owner, map[string]string{"passphrase": passphrase})
	wantCode(t, "viewer", f.do(t, "GET", "/v0/archive/encryption/key-file", f.viewer, nil), 403, "forbidden")
	req, _ := http.NewRequestWithContext(context.Background(), "GET", f.srv.URL+"/v0/archive/encryption/key-file", nil)
	req.Header.Set("Authorization", "Bearer "+f.owner)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/octet-stream" || !strings.Contains(resp.Header.Get("Content-Disposition"), `filename="camhive-archive-key-hv_1.age"`) {
		t.Fatalf("key file: %d %v", resp.StatusCode, resp.Header)
	}
	scrypt, _ := age.NewScryptIdentity(passphrase)
	plain, err := age.Decrypt(bytes.NewReader(body), scrypt)
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := age.ParseIdentities(plain); err != nil || len(ids) != 1 {
		t.Fatalf("keys in the file: %d, %v", len(ids), err)
	}
}

// A finished export of sealed footage downloads only while unlocked.
func TestASealedExportDownloadsOnlyWhileUnlocked(t *testing.T) {
	f := newEncFixture(t)
	f.post(t, "", f.owner, map[string]string{"passphrase": passphrase})
	r := f.do(t, "POST", "/v0/cameras/"+f.camera+"/exports", f.owner, map[string]any{"start": "2026-11-09T10:00:00Z", "duration_seconds": 10})
	if r.Status != 202 {
		t.Fatalf("export: %d %v", r.Status, r.Body)
	}
	id, _ := r.Body["id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for f.do(t, "GET", "/v0/exports/"+id, f.owner, nil).Body["status"] != "done" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	f.post(t, "/lock", f.owner, nil)
	wantCode(t, "download while locked", f.do(t, "GET", "/v0/exports/"+id+"/file", f.owner, nil), 423, "archive_locked")
	f.post(t, "/unlock", f.owner, map[string]string{"passphrase": passphrase})
	req, _ := http.NewRequestWithContext(context.Background(), "GET", f.srv.URL+"/v0/exports/"+id+"/file", nil)
	req.Header.Set("Authorization", "Bearer "+f.owner)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.HasPrefix(body, []byte("\x00\x00\x00\x18ftyp")) {
		t.Fatalf("download: %d %q", resp.StatusCode, body)
	}
}

// The archive can lock between the state check and the read: the reader's
// ErrLocked is a 423 too, any other read error stays 502 engine.
func TestAReadThatMeetsTheLockedArchiveAnswers423(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{{archive.ErrLocked, 423, "archive_locked"}, {errors.New("disk gone"), 502, "engine"}} {
		f, play, cam := newArchiveAPI(t)
		play.real = func(context.Context, export.Request) (io.ReadCloser, error) { return nil, c.err }
		tok := f.token(t, auth.RoleOwner, "*")
		wantCode(t, "play", f.do(t, "GET", "/v0/cameras/"+string(cam.ID)+"/archive/play?start=2026-11-09T10:00:00Z&duration=10", tok, nil), c.status, c.code)
		wantCode(t, "audio", f.do(t, "GET", "/v0/cameras/"+string(cam.ID)+"/archive/audio?start=2026-11-09T10:00:00Z&duration=10", tok, nil), c.status, c.code)
	}
}

// sealedRecording copies the recorded test segments under root for camera
// and seals them to the vault; it returns the sealed paths, oldest first.
func sealedRecording(t *testing.T, root, camera string, v *archivekey.Vault) []string {
	t.Helper()
	src := filepath.Join("..", "archive", "testdata", "recording", "cam_gold", "main")
	dst := filepath.Join(root, camera, "main")
	if err := os.MkdirAll(dst, 0o750); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(src, e.Name())) //nolint:gosec // test data
		if err != nil {
			t.Fatal(err)
		}
		plain := filepath.Join(dst, e.Name())
		if err := os.WriteFile(plain, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		sealed, err := archive.SealFile(plain, v.Recipient())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, sealed)
	}
	return out
}

// flipInChunk changes one byte inside age chunk c of a sealed file.
func flipInChunk(t *testing.T, path string, c int) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	mac := bytes.Index(raw, []byte("\n--- "))
	start := mac + 1 + bytes.IndexByte(raw[mac+1:], '\n') + 1 + 16 // header, then the nonce
	raw[start+c*(64<<10+16)+100] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A sealed segment changed on disk is refused, never played as if whole: a
// changed first segment answers 502 without video (age checks the header
// chunk when it reads it and the last chunk when it opens the file); a
// changed segment the answer reaches later breaks it off, so the player sees
// an error, not a short clip that looks whole.
func TestATamperedSealedSegmentIsNotPlayed(t *testing.T) {
	for _, c := range []struct {
		name           string
		segment, chunk int
		brokenOff      bool
	}{{"header chunk", 0, 0, false}, {"last chunk", 0, 1, false}, {"a later segment", 1, 0, true}} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			f := &encFixture{vault: archivekey.New(t.TempDir())}
			api, _, cam := newArchiveAPIWith(t, func(d *hiveapp.Deps) {
				d.Vault = f.vault
				d.Playback = hiveapp.ArchiveFetch(archive.NewReader(root, f.vault.OpenSegment))
			})
			f.apiFixture, f.camera = api, string(cam.ID)
			f.owner = api.token(t, auth.RoleOwner, "*")
			if r := f.post(t, "", f.owner, map[string]string{"passphrase": passphrase}); r.Status != 200 {
				t.Fatalf("enable: %d", r.Status)
			}
			flipInChunk(t, sealedRecording(t, root, f.camera, f.vault)[c.segment], c.chunk)

			req, _ := http.NewRequestWithContext(context.Background(), "GET", f.srv.URL+"/v0/cameras/"+f.camera+"/archive/play?start=2026-11-15T23:22:00Z&duration=60", nil)
			req.Header.Set("Authorization", "Bearer "+f.owner)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, readErr := io.ReadAll(resp.Body)
			if !c.brokenOff {
				if resp.StatusCode != 502 || bytes.Contains(body, []byte("ftyp")) {
					t.Fatalf("%d with %d bytes; want 502 and no video", resp.StatusCode, len(body))
				}
				return
			}
			if readErr == nil {
				t.Fatalf("%d: %d bytes read to a clean end; want the answer broken off", resp.StatusCode, len(body))
			}
		})
	}
}

// A local emergency session (#57) is the owner on the LAN while the cloud
// is down: it may unlock the recordings (that is the emergency) and lock
// them again, but switching encryption on, changing the passphrase and
// taking the key file need the owner signed in through Camhive, as update
// settings do (#107).
func TestALocalSessionUnlocksButDoesNotChangeTheKey(t *testing.T) {
	f := &encFixture{vault: archivekey.New(t.TempDir())}
	api, _, cam := newArchiveAPIWith(t, func(d *hiveapp.Deps) {
		d.Vault = f.vault
		d.Local = auth.NewLocalSessions("hv_1", time.Now)
	})
	f.apiFixture, f.camera = api, string(cam.ID)
	f.owner = api.token(t, auth.RoleOwner, "*")
	lan := f.viaFrom(t, hiveapp.LANConn, lanPeer)
	if r := lan.do(t, "PUT", "/v0/local-login", f.owner, map[string]string{"username": "anna", "password": localPassword}); r.Status != 200 {
		t.Fatalf("local login: %d %v", r.Status, r.Body)
	}
	local, _ := lan.do(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": localPassword}).Body["token"].(string)
	enc := func(method, path string, body any) apiResp {
		return lan.do(t, method, "/v0/archive/encryption"+path, local, body)
	}

	wantCode(t, "local enable", enc("POST", "", map[string]string{"passphrase": passphrase}), 403, "forbidden")
	if r := f.post(t, "", f.owner, map[string]string{"passphrase": passphrase}); r.Status != 200 {
		t.Fatalf("cloud owner enables: %d %v", r.Status, r.Body)
	}
	if r := enc("POST", "/lock", nil); r.Status != 200 || r.Body["state"] != "locked" {
		t.Fatalf("local lock: %d %v", r.Status, r.Body)
	}
	if r := enc("POST", "/unlock", map[string]string{"passphrase": passphrase}); r.Status != 200 || r.Body["state"] != "unlocked" {
		t.Fatalf("local unlock: %d %v", r.Status, r.Body)
	}
	wantCode(t, "local passphrase change", enc("POST", "/passphrase", map[string]string{"passphrase": passphrase, "new_passphrase": "another long passphrase"}), 403, "forbidden")
	wantCode(t, "local key file", enc("GET", "/key-file", nil), 403, "forbidden")
}
