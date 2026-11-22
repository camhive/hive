package hiveapp_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/store"
)

// testClock is a wall and a monotonic clock the test moves by hand.
type testClock struct {
	mu   sync.Mutex
	t    time.Time
	mono time.Duration
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) since() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mono
}

func (c *testClock) pass(d time.Duration) {
	c.mu.Lock()
	c.t, c.mono = c.t.Add(d), c.mono+d
	c.mu.Unlock()
}

// unlocked switches encryption on (which leaves the archive unlocked) and
// sets the automatic lock.
func (f *encFixture) unlocked(t *testing.T, settings map[string]string) {
	t.Helper()
	if r := f.post(t, "", f.owner, map[string]string{"passphrase": passphrase}); r.Status != 200 {
		t.Fatalf("enable: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "PATCH", "/v0/archive/encryption/autolock", f.owner, settings); r.Status != 200 {
		t.Fatalf("autolock: %d %v", r.Status, r.Body)
	}
}

// check runs one pass of the hive's automatic-lock loop.
func (f *encFixture) check(t *testing.T) bool {
	t.Helper()
	return hiveapp.LockWhenDue(context.Background(), *f.deps)
}

func (f *encFixture) autolockView(t *testing.T) map[string]any {
	t.Helper()
	r := f.do(t, "GET", "/v0/archive/encryption", f.owner, nil)
	v, _ := r.Body["autolock"].(map[string]any)
	if r.Status != 200 || v == nil {
		t.Fatalf("encryption: %d %v", r.Status, r.Body)
	}
	return v
}

// wantAutoLock: the archive locked, audited with its reason by the hive and
// sent to the cloud as the button's lock is.
func (f *encFixture) wantAutoLock(t *testing.T, reason string) {
	t.Helper()
	if s := f.state(t, f.viewer); s != "locked" {
		t.Fatalf("state %v, want locked", s)
	}
	wantCode(t, "play after the automatic lock", f.play(t), 423, "archive_locked")
	r := f.do(t, "GET", "/v0/actions", f.owner, nil)
	last := r.List[0]
	if d, _ := last["details"].(map[string]any); last["action"] != "archive.encryption.lock" || last["actor"] != "hive" || d["reason"] != reason {
		t.Fatalf("last action %v, want the hive's lock for %s", last, reason)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	name, data := f.emitted[len(f.emitted)-1], f.sent[len(f.sent)-1].(map[string]any)
	if name != "archive.encryption.lock" || data["reason"] != archivekey.Reason(reason) {
		t.Fatalf("sent %s %v, want the lock with reason %s", name, data, reason)
	}
}

// #147: the settings are off on a fresh hive, the owner sets them, they
// persist; Members do not see them (they would learn when the owner left).
func TestTheOwnerSetsTheAutomaticLock(t *testing.T) {
	f := newEncFixture(t)
	if v := f.autolockView(t); v["after"] != "off" || v["away"] != "off" || v["locks_at"] != nil {
		t.Fatalf("fresh hive: %v", v)
	}
	if r := f.do(t, "GET", "/v0/archive/encryption", f.viewer, nil); r.Body["autolock"] != nil {
		t.Fatalf("a member sees %v", r.Body["autolock"])
	}
	r := f.do(t, "PATCH", "/v0/archive/encryption/autolock", f.owner, map[string]string{"after": "8h", "away": "10m"})
	if v, _ := r.Body["autolock"].(map[string]any); r.Status != 200 || v["after"] != "8h" || v["away"] != "10m" {
		t.Fatalf("patch: %d %v", r.Status, r.Body)
	}
	lan := f.viaFrom(t, hiveapp.LANConn, lanPeer)
	if r := lan.do(t, "PATCH", "/v0/archive/encryption/autolock", f.owner, map[string]string{"away": "1h"}); r.Status != 200 { // a field left out stays
		t.Fatalf("longer grace at home: %d %v", r.Status, r.Body)
	}
	if v := f.autolockView(t); v["after"] != "8h" || v["away"] != "1h" {
		t.Fatalf("after a partial patch: %v", v)
	}
	if got, err := f.st.Autolock().Get(context.Background()); err != nil || got != (store.AutolockSettings{After: 8 * time.Hour, Away: time.Hour}) {
		t.Fatalf("stored %+v, %v", got, err)
	}
	wantCode(t, "nothing to change", f.do(t, "PATCH", "/v0/archive/encryption/autolock", f.owner, map[string]string{}), 400, "invalid")
	wantCode(t, "unknown choice", f.do(t, "PATCH", "/v0/archive/encryption/autolock", f.owner, map[string]string{"after": "2h"}), 400, "invalid")
	wantCode(t, "member", f.do(t, "PATCH", "/v0/archive/encryption/autolock", f.viewer, map[string]string{"after": "1h"}), 403, "forbidden")
	if r := f.do(t, "GET", "/v0/actions", f.owner, nil); r.List[0]["action"] != "archive.encryption.autolock" {
		t.Fatalf("last action %v, want the setting change", r.List[0])
	}
}

// The inactivity timer counts from the unlock and from the last playback,
// sound or export by anyone.
func TestTheArchiveLocksAfterInactivity(t *testing.T) {
	f := newEncFixture(t)
	f.unlocked(t, map[string]string{"after": "1h"})
	if v := f.autolockView(t); v["locks_at"] != "2026-11-22T10:00:00Z" || v["reason"] != "inactivity" {
		t.Fatalf("an hour after the unlock: %v", v)
	}
	for i, read := range []func(){
		func() { f.play(t) },
		func() {
			f.do(t, "GET", "/v0/cameras/"+f.camera+"/archive/audio?start=2026-11-09T10:00:00Z&duration=10", f.viewer, nil)
		},
		func() {
			f.do(t, "POST", "/v0/cameras/"+f.camera+"/exports", f.owner, map[string]any{"start": "2026-11-09T10:00:00Z", "duration_seconds": 10})
		},
	} {
		f.clock.pass(50 * time.Minute)
		read()
		f.clock.pass(50 * time.Minute)
		if f.check(t) {
			t.Fatalf("read %d: locked 50 minutes after a read of footage", i)
		}
	}
	// Requests the hive refuses read nothing and push nothing back.
	wantCode(t, "bad span", f.do(t, "GET", "/v0/cameras/"+f.camera+"/archive/play?start=bad&duration=10", f.owner, nil), 400, "invalid")
	wantCode(t, "long sound", f.do(t, "GET", "/v0/cameras/"+f.camera+"/archive/audio?start=2026-11-09T10:00:00Z&duration=600", f.owner, nil), 400, "invalid")
	f.clock.pass(10 * time.Minute)
	if !f.check(t) {
		t.Fatal("not locked an hour after the last read")
	}
	f.wantAutoLock(t, "inactivity")
	if v := f.autolockView(t); v["locks_at"] != nil {
		t.Fatalf("a locked archive has no timer: %v", v)
	}
	if f.check(t) {
		t.Fatal("locked twice")
	}
}

// Leaving home starts the grace period, a return on the LAN cancels it; an
// "at home" through the relay is away.
func TestTheArchiveLocksWhenTheOwnerLeavesHome(t *testing.T) {
	f := newEncFixture(t)
	f.unlocked(t, map[string]string{"away": "10m"})
	lan := f.viaFrom(t, hiveapp.LANConn, lanPeer)
	relay := f.via(t, hiveapp.RelayedConn)
	presence := func(via *apiFixture, atHome bool) {
		t.Helper()
		if r := via.do(t, "POST", "/v0/archive/encryption/presence", f.owner, map[string]bool{"at_home": atHome}); r.Status != http.StatusNoContent {
			t.Fatalf("presence: %d %v", r.Status, r.Body)
		}
	}
	presence(lan, true)
	if v := f.autolockView(t); v["locks_at"] != nil {
		t.Fatalf("at home: %v", v)
	}
	presence(relay, false)
	f.clock.pass(5 * time.Minute)
	presence(lan, true)
	f.clock.pass(time.Hour)
	if f.check(t) {
		t.Fatal("locked although the owner came back within the grace period")
	}

	presence(relay, true) // claimed through the relay: away
	if v := f.autolockView(t); v["locks_at"] != "2026-11-22T10:15:00Z" || v["reason"] != "away" {
		t.Fatalf("away: %v", v)
	}
	f.clock.pass(9 * time.Minute)
	if f.check(t) {
		t.Fatal("locked within the grace period")
	}
	f.clock.pass(time.Minute)
	if !f.check(t) {
		t.Fatal("not locked after the grace period")
	}
	f.wantAutoLock(t, "away")
}

// From outside (the relay, or a forwarded port) the owner can make the lock
// stricter but not relax it: a stolen token cannot keep the archive open.
func TestRelaxingTheLockTakesTheOwnerAtHome(t *testing.T) {
	f := newEncFixture(t)
	relay := f.via(t, hiveapp.RelayedConn)
	forwarded := f.viaFrom(t, hiveapp.LANConn, "203.0.113.7")
	lan := f.viaFrom(t, hiveapp.LANConn, lanPeer)
	patch := func(via *apiFixture, body map[string]string) apiResp {
		return via.do(t, "PATCH", "/v0/archive/encryption/autolock", f.owner, body)
	}
	if r := patch(relay, map[string]string{"after": "24h", "away": "1h"}); r.Status != 200 {
		t.Fatalf("stricter from outside: %d %v", r.Status, r.Body)
	}
	if r := patch(relay, map[string]string{"after": "1h", "away": "10m"}); r.Status != 200 {
		t.Fatalf("shorter from outside: %d %v", r.Status, r.Body)
	}
	for _, body := range []map[string]string{{"away": "off"}, {"after": "off"}, {"after": "8h"}, {"away": "1h"}} {
		wantCode(t, "relaxed through the relay", patch(relay, body), 403, "lan_only")
		wantCode(t, "relaxed through a forwarded port", patch(forwarded, body), 403, "lan_only")
	}
	if v := f.autolockView(t); v["after"] != "1h" || v["away"] != "10m" {
		t.Fatalf("after refused changes: %v", v)
	}
	if r := patch(lan, map[string]string{"after": "off", "away": "off"}); r.Status != 200 {
		t.Fatalf("off at home: %d %v", r.Status, r.Body)
	}
}

// At home counts only from a machine in a local network on the LAN
// listener: not through a tunnel on the hive itself, nor a forwarded port.
func TestAtHomeCountsOnlyFromTheLocalNetwork(t *testing.T) {
	f := newEncFixture(t)
	f.unlocked(t, map[string]string{"away": "10m"})
	for _, via := range []*apiFixture{f.via(t, hiveapp.LANConn), f.viaFrom(t, hiveapp.LANConn, "203.0.113.7")} {
		if r := via.do(t, "POST", "/v0/archive/encryption/presence", f.owner, map[string]bool{"at_home": true}); r.Status != http.StatusNoContent {
			t.Fatalf("presence: %d %v", r.Status, r.Body)
		}
		if v := f.autolockView(t); v["reason"] != "away" {
			t.Fatalf("at home claimed from outside the local network: %v", v)
		}
	}
}

func TestOnlyTheOwnerReportsPresence(t *testing.T) {
	f := newEncFixture(t)
	wantCode(t, "member", f.post(t, "/presence", f.viewer, map[string]bool{"at_home": false}), 403, "forbidden")
	if r := f.post(t, "/presence", f.owner, map[string]bool{"at_home": false}); r.Status != 204 {
		t.Fatalf("owner while encryption is off: %d %v", r.Status, r.Body)
	}
	plain := newAPIWith(t, nil)
	tok := plain.token(t, auth.RoleOwner, "*")
	wantCode(t, "no autolock", plain.do(t, "POST", "/v0/archive/encryption/presence", tok, map[string]bool{"at_home": false}), 503, "not_ready")
}

// The hive's loop locks a due archive on its own and stops with its context.
func TestTheAutomaticLockLoopLocksADueArchive(t *testing.T) {
	f := newEncFixture(t)
	f.unlocked(t, map[string]string{"after": "1h"})
	f.clock.pass(2 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { hiveapp.RunAutolock(ctx, *f.deps, time.Millisecond); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.vault.State() != archivekey.Locked {
		if time.Now().After(deadline) {
			t.Fatal("the loop did not lock the due archive")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	f.wantAutoLock(t, "inactivity")
}

// Before the store is up the settings cannot be kept: not_ready, not a crash.
func TestSettingsWaitForTheStore(t *testing.T) {
	vault := archivekey.New(t.TempDir())
	f := newAPIWith(t, func(d *hiveapp.Deps) {
		d.Store, d.Vault, d.Autolock = nil, vault, archivekey.NewAutolock(vault)
	})
	wantCode(t, "no store", f.do(t, "PATCH", "/v0/archive/encryption/autolock", f.token(t, auth.RoleOwner, "*"), map[string]string{"after": "1h"}), 503, "not_ready")
}
