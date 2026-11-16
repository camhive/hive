package hiveapp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/hiveapp"
)

const localPassword = "correct horse battery"

// lanPeer is where LAN clients appear to connect from in these tests.
const lanPeer = "192.168.1.50"

// localFixture serves one API three ways: on the LAN listener, through the
// relay and on the plain loopback listener.
type localFixture struct {
	*apiFixture
	lan, relay *apiFixture
	owner      string // a cloud owner token
}

func newLocalFixture(t *testing.T) *localFixture {
	t.Helper()
	f := newAPIWith(t, func(d *hiveapp.Deps) { d.Local = auth.NewLocalSessions("hv_1", time.Now) })
	return &localFixture{apiFixture: f, lan: f.viaFrom(t, hiveapp.LANConn, lanPeer), relay: f.via(t, hiveapp.RelayedConn), owner: f.token(t, auth.RoleOwner, "*")}
}

func (f *localFixture) enable(t *testing.T) {
	t.Helper()
	if r := f.lan.do(t, "PUT", "/v0/local-login", f.owner, map[string]string{"username": "anna", "password": localPassword}); r.Status != 200 {
		t.Fatalf("enable: %d %v", r.Status, r.Body)
	}
}

func (f *localFixture) signIn(t *testing.T, password string) apiResp {
	t.Helper()
	return f.lan.do(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": password})
}

func (f *localFixture) actions(t *testing.T) []string {
	t.Helper()
	r := f.do(t, "GET", "/v0/actions", f.owner, nil)
	out := make([]string, 0, len(r.List))
	for _, a := range r.List {
		out = append(out, a["action"].(string)+" by "+a["actor"].(string))
	}
	return out
}

func TestLocalLoginIsOffUntilTheOwnerEnablesIt(t *testing.T) {
	f := newLocalFixture(t)
	if r := f.do(t, "GET", "/v0/local-login", f.owner, nil); r.Status != 200 || r.Body["enabled"] != false || r.Body["username"] != nil {
		t.Fatalf("status on a fresh hive: %d %v", r.Status, r.Body)
	}
	if r := f.signIn(t, localPassword); r.Status != 403 || r.Body["code"] != "local_login_disabled" {
		t.Fatalf("sign-in on a fresh hive: %d %v", r.Status, r.Body)
	}
}

func TestOwnerSignsInLocallyAndActsAsOwnerOnTheLAN(t *testing.T) {
	f := newLocalFixture(t)
	f.enable(t)
	if r := f.do(t, "GET", "/v0/local-login", f.owner, nil); r.Body["enabled"] != true || r.Body["username"] != "anna" || r.Body["updated_at"] == nil {
		t.Fatalf("status: %v", r.Body)
	}
	r := f.signIn(t, localPassword)
	if r.Status != 201 || r.Body["role"] != "owner" || r.Body["local"] != true || r.Body["expires_at"] == nil || r.Body["session_id"] == nil {
		t.Fatalf("sign-in: %d %v", r.Status, r.Body)
	}
	tok, _ := r.Body["token"].(string)
	if !auth.IsLocalToken(tok) {
		t.Fatalf("token %q is not a local token", tok)
	}
	if r := f.lan.do(t, "POST", "/v0/cameras", tok, map[string]any{"name": "Door", "source": map[string]string{"kind": "rtsp", "address": "rtsp://10.0.0.5/live"}}); r.Status != 201 {
		t.Fatalf("a local owner manages cameras: %d %v", r.Status, r.Body)
	}
	if r := f.lan.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 200 || len(r.List) != 1 {
		t.Fatalf("cameras with a local token: %d %v", r.Status, r.List)
	}
	got := strings.Join(f.actions(t), "; ")
	for _, want := range []string{"local_login.enable by acc", "local_login.success by local:anna", "camera.add by local:anna"} {
		if !strings.Contains(got, want) {
			t.Errorf("action log %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, localPassword) {
		t.Fatal("the action log carries the password")
	}
}

// Credentials are set by the owner with a cloud session; a local session
// cannot change or switch off its own way in.
func TestOnlyTheOwnerOnlineSetsTheLocalLogin(t *testing.T) {
	f := newLocalFixture(t)
	viewer := f.token(t, auth.RoleViewer, "*")
	if r := f.do(t, "PUT", "/v0/local-login", viewer, map[string]string{"username": "anna", "password": localPassword}); r.Status != 403 || r.Body["code"] != "forbidden" {
		t.Fatalf("viewer: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "PUT", "/v0/local-login", f.owner, map[string]string{"username": "anna", "password": "short"}); r.Status != 422 || r.Body["code"] != "weak_password" {
		t.Fatalf("weak password: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "PUT", "/v0/local-login", f.owner, map[string]string{"username": "", "password": localPassword}); r.Status != 422 || r.Body["code"] != "invalid" {
		t.Fatalf("empty username: %d %v", r.Status, r.Body)
	}
	f.enable(t)
	tok := f.signIn(t, localPassword).Body["token"].(string)
	for _, m := range []string{"PUT", "DELETE"} {
		if r := f.lan.do(t, m, "/v0/local-login", tok, map[string]string{"username": "eve", "password": "another long password"}); r.Status != 403 || r.Body["code"] != "forbidden" {
			t.Fatalf("%s with a local token: %d %v", m, r.Status, r.Body)
		}
	}
}

func TestLocalLoginAndLocalTokensStayOnTheLAN(t *testing.T) {
	f := newLocalFixture(t)
	f.enable(t)
	tok := f.signIn(t, localPassword).Body["token"].(string)
	// Loopback on the LAN listener is any forwarder on the hive machine
	// (ssh -R, a tunnel agent, a reverse proxy) carrying outside traffic.
	for name, g := range map[string]*apiFixture{"relay": f.relay, "loopback listener": f.apiFixture, "loopback peer on the LAN listener": f.via(t, hiveapp.LANConn)} {
		if r := g.do(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": localPassword}); r.Status != 403 || r.Body["code"] != "lan_only" {
			t.Errorf("sign-in via %s: %d %v", name, r.Status, r.Body)
		}
		if r := g.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 403 || r.Body["code"] != "lan_only" {
			t.Errorf("local token via %s: %d %v", name, r.Status, r.Body)
		}
	}
	// A LAN listener reached from a public address (a forwarded port) is not the LAN.
	req := httptest.NewRequestWithContext(hiveapp.LANConn(context.Background(), nil), "GET", "/v0/cameras", nil)
	req.RemoteAddr = "203.0.113.7:50000"
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "lan_only") {
		t.Fatalf("public source: %d %s", rec.Code, rec.Body)
	}
}

func TestRepeatedFailuresLockTheSourceOut(t *testing.T) {
	f := newLocalFixture(t)
	f.enable(t)
	for i := range hiveapp.LocalLoginLockout.MaxFailures {
		if r := f.signIn(t, "wrong password "+string(rune('a'+i))); r.Status != 401 || r.Body["code"] != "invalid_credentials" {
			t.Fatalf("failure %d: %d %v", i+1, r.Status, r.Body)
		}
	}
	r := f.signIn(t, localPassword) // even the right password waits now
	if r.Status != 429 || r.Body["code"] != "local_login_rate_limited" || r.Header.Get("Retry-After") == "" {
		t.Fatalf("locked: %d %v %v", r.Status, r.Body, r.Header)
	}
	got := strings.Join(f.actions(t), "; ")
	if strings.Count(got, "local_login.failure") != hiveapp.LocalLoginLockout.MaxFailures || !strings.Contains(got, "local_login.locked") {
		t.Fatalf("action log %q", got)
	}
}

// A password typed into the username field must not end up in the log.
func TestFailedSignInDoesNotLogTheTypedUsername(t *testing.T) {
	f := newLocalFixture(t)
	f.enable(t)
	if r := f.lan.do(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "my secret passphrase", "password": "x"}); r.Status != 401 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	raw, _ := json.Marshal(f.do(t, "GET", "/v0/actions", f.owner, nil).List)
	if strings.Contains(string(raw), "my secret passphrase") || !strings.Contains(string(raw), "local_login.failure") {
		t.Fatalf("action log %s", raw)
	}
}

// Parallel guesses from one source must not all reach the hash: the
// attempt is counted before the password is checked.
func TestParallelGuessesFromOneSourceStopAtTheLimit(t *testing.T) {
	f := newLocalFixture(t)
	f.enable(t)
	const n = 20
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- f.signIn(t, fmt.Sprintf("wrong password %02d", i)).Status
		}()
	}
	wg.Wait()
	close(codes)
	count := map[int]int{}
	for c := range codes {
		count[c]++
	}
	if count[401] > hiveapp.LocalLoginLockout.MaxFailures || count[401]+count[429] != n {
		t.Fatalf("answers %v: at most %d may be checked, the rest refused", count, hiveapp.LocalLoginLockout.MaxFailures)
	}
}

func TestChangingOrDisablingTheLocalLoginEndsLocalSessions(t *testing.T) {
	f := newLocalFixture(t)
	f.enable(t)
	tok := f.signIn(t, localPassword).Body["token"].(string)
	f.enable(t) // same or new password: sessions signed in with the old one end
	if r := f.lan.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 401 || r.Body["code"] != "token_invalid" {
		t.Fatalf("after a credential change: %d %v", r.Status, r.Body)
	}
	tok = f.signIn(t, localPassword).Body["token"].(string)
	if r := f.do(t, "DELETE", "/v0/local-login", f.owner, nil); r.Status != 204 {
		t.Fatalf("disable: %d %v", r.Status, r.Body)
	}
	if r := f.lan.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 401 {
		t.Fatalf("after disable: %d %v", r.Status, r.Body)
	}
	if r := f.signIn(t, localPassword); r.Status != 403 || r.Body["code"] != "local_login_disabled" {
		t.Fatalf("sign-in after disable: %d %v", r.Status, r.Body)
	}
	if got := strings.Join(f.actions(t), "; "); !strings.Contains(got, "local_login.disable by acc") {
		t.Fatalf("action log %q", got)
	}
}

// `hive local-login` on the console writes the row while the hive runs; the
// running hive must honour it for sessions already signed in.
func TestConsoleChangesEndRunningLocalSessions(t *testing.T) {
	ctx := context.Background()
	f := newLocalFixture(t)
	f.enable(t)
	tok := f.signIn(t, localPassword).Body["token"].(string)
	c, err := hiveapp.NewLocalCredential("anna", "a new long password", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.LocalLogin().Set(ctx, c); err != nil {
		t.Fatal(err)
	}
	if r := f.lan.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 401 || r.Body["code"] != "token_invalid" {
		t.Fatalf("after a console password change: %d %v", r.Status, r.Body)
	}
	tok = f.lan.do(t, "POST", "/v0/local-login/sessions", "", map[string]string{"username": "anna", "password": "a new long password"}).Body["token"].(string)
	if err := f.st.LocalLogin().Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if r := f.lan.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 401 {
		t.Fatalf("after a console disable: %d %v", r.Status, r.Body)
	}
}

// A hive without the local issuer (or a test that leaves it out) treats a
// local token as any other unknown token.
func TestLocalTokenWithoutLocalSessionsIsInvalid(t *testing.T) {
	f := newAPI(t).viaFrom(t, hiveapp.LANConn, lanPeer)
	tok, _, _ := auth.NewLocalSessions("hv_1", time.Now).Issue("local:anna", "v1")
	if r := f.do(t, "GET", "/v0/cameras", tok, nil); r.Status != http.StatusUnauthorized || r.Body["code"] != "token_invalid" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}
