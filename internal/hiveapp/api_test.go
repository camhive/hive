package hiveapp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/store"
)

type apiFixture struct {
	srv *httptest.Server
	iss *auth.Issuer
	st  *store.Store
}

func newAPI(t *testing.T) *apiFixture {
	t.Helper()
	iss, _ := auth.NewIssuer("k1")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ver := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()})
	h := hiveapp.NewAPI(hiveapp.Deps{Store: st, Verifier: ver, Version: "test", Notify: func() {}})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &apiFixture{srv: srv, iss: iss, st: st}
}

func (f *apiFixture) token(t *testing.T, role auth.Role, cams ...string) string {
	t.Helper()
	now := time.Now()
	tok, err := f.iss.Issue(auth.Claims{Account: "acc", HiveID: "hv_1", SessionID: "sid", Cameras: cams, Role: role, IssuedAt: now, Expiry: now.Add(5 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type apiResp struct {
	Status int
	Body   map[string]any
	List   []map[string]any
}

func (f *apiFixture) do(t *testing.T, method, path, token string, body any) apiResp {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, f.srv.URL+path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := apiResp{Status: resp.StatusCode}
	var raw json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &out.List)
	} else if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.Body)
	}
	return out
}

func TestHealthIsPublicEverythingElseNeedsAToken(t *testing.T) {
	f := newAPI(t)
	if r := f.do(t, "GET", "/healthz", "", nil); r.Status != 200 {
		t.Fatalf("healthz = %d", r.Status)
	}
	r := f.do(t, "GET", "/v0/cameras", "", nil)
	if r.Status != 401 || r.Body["code"] != "token_missing" {
		t.Fatalf("no token: %d %v", r.Status, r.Body)
	}
	r = f.do(t, "GET", "/v0/cameras", "v4.public.garbage.e30", nil)
	if r.Status != 401 || r.Body["code"] != "token_invalid" {
		t.Fatalf("bad token: %d %v", r.Status, r.Body)
	}
	other, _ := auth.NewIssuer("k1")
	now := time.Now()
	tok, _ := other.Issue(auth.Claims{Account: "a", HiveID: "hv_1", SessionID: "s", Cameras: []string{"*"}, Role: auth.RoleOwner, IssuedAt: now, Expiry: now.Add(time.Minute)})
	if r := f.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 401 || r.Body["code"] != "token_invalid" {
		t.Fatalf("foreign key: %d %v", r.Status, r.Body)
	}
	// A token from the future means the hive clock is behind: a distinct code
	// so the app can say "check the hive's clock" instead of "token invalid".
	tok, _ = f.iss.Issue(auth.Claims{Account: "a", HiveID: "hv_1", SessionID: "s", Cameras: []string{"*"}, Role: auth.RoleOwner, IssuedAt: now.Add(10 * time.Minute), Expiry: now.Add(20 * time.Minute)})
	if r := f.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 401 || r.Body["code"] != "clock_skew" {
		t.Fatalf("future token: %d %v", r.Status, r.Body)
	}
	tok, _ = f.iss.Issue(auth.Claims{Account: "a", HiveID: "hv_other", SessionID: "s", Cameras: []string{"*"}, Role: auth.RoleOwner, IssuedAt: now, Expiry: now.Add(time.Minute)})
	if r := f.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 401 || r.Body["code"] != "wrong_hive" {
		t.Fatalf("wrong hive: %d %v", r.Status, r.Body)
	}
}

func TestExpiredTokenIsItsOwnCode(t *testing.T) {
	f := newAPI(t)
	past := time.Now().Add(-time.Hour)
	tok, _ := f.iss.Issue(auth.Claims{Account: "a", HiveID: "hv_1", SessionID: "s", Cameras: []string{"*"}, Role: auth.RoleOwner, IssuedAt: past, Expiry: past.Add(time.Minute)})
	if r := f.do(t, "GET", "/v0/cameras", tok, nil); r.Status != 401 || r.Body["code"] != "token_expired" {
		t.Fatalf("expired: %d %v", r.Status, r.Body)
	}
}

func TestOwnerManagesCamerasAndSecretsNeverComeBack(t *testing.T) {
	f := newAPI(t)
	owner := f.token(t, auth.RoleOwner, "*")

	r := f.do(t, "POST", "/v0/cameras", owner, map[string]any{
		"name": "Door", "source": map[string]string{"kind": "rtsp", "address": "rtsp://admin:topsecret@10.0.0.5/live"},
	})
	if r.Status != 201 || r.Body["id"] == nil || r.Body["name"] != "Door" {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
	id := r.Body["id"].(string)
	if raw, _ := json.Marshal(r.Body); bytes.Contains(raw, []byte("topsecret")) {
		t.Fatalf("response leaked the password: %s", raw)
	}
	if r.Body["credentials"].(map[string]any)["username"] != "admin" {
		t.Fatalf("username should be visible: %v", r.Body["credentials"])
	}

	r = f.do(t, "GET", "/v0/cameras", owner, nil)
	if r.Status != 200 || len(r.List) != 1 || r.List[0]["id"] != id {
		t.Fatalf("list: %d %v", r.Status, r.List)
	}
	if r.List[0]["record_audio"] != false {
		t.Fatalf("sound is off by default (ADR-0015): %v", r.List[0])
	}
	r = f.do(t, "PATCH", "/v0/cameras/"+id, owner, map[string]any{"name": "Front door", "enabled": false, "record_audio": true})
	if r.Status != 200 || r.Body["name"] != "Front door" || r.Body["enabled"] != false || r.Body["record_audio"] != true {
		t.Fatalf("patch: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "GET", "/v0/cameras/"+id, owner, nil); r.Status != 200 || r.Body["enabled"] != false {
		t.Fatalf("get: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "DELETE", "/v0/cameras/"+id, owner, nil); r.Status != 204 {
		t.Fatalf("delete: %d", r.Status)
	}
	if r := f.do(t, "GET", "/v0/cameras/"+id, owner, nil); r.Status != 404 || r.Body["code"] != "not_found" {
		t.Fatalf("get after delete: %d %v", r.Status, r.Body)
	}
}

func TestViewerSeesOnlyGrantedCamerasAndCannotManage(t *testing.T) {
	f := newAPI(t)
	ctx := context.Background()
	a, _ := camera.New(camera.Spec{Name: "A", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/a"}}, time.Now())
	b, _ := camera.New(camera.Spec{Name: "B", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/b"}}, time.Now())
	_ = f.st.Cameras().Put(ctx, a)
	_ = f.st.Cameras().Put(ctx, b)

	viewer := f.token(t, auth.RoleViewer, string(a.ID))
	r := f.do(t, "GET", "/v0/cameras", viewer, nil)
	if r.Status != 200 || len(r.List) != 1 || r.List[0]["id"] != string(a.ID) {
		t.Fatalf("viewer list: %d %v", r.Status, r.List)
	}
	if r := f.do(t, "GET", "/v0/cameras/"+string(b.ID), viewer, nil); r.Status != 404 {
		t.Fatalf("ungranted camera must look absent, got %d", r.Status)
	}
	if r := f.do(t, "POST", "/v0/cameras", viewer, map[string]any{"name": "x", "source": map[string]string{"kind": "rtsp", "address": "rtsp://h/x"}}); r.Status != 403 || r.Body["code"] != "forbidden" {
		t.Fatalf("viewer create: %d %v", r.Status, r.Body)
	}
	if r := f.do(t, "DELETE", "/v0/cameras/"+string(a.ID), viewer, nil); r.Status != 403 {
		t.Fatalf("viewer delete: %d", r.Status)
	}
	if r := f.do(t, "GET", "/v0/cameras/"+string(a.ID), viewer, nil); r.Status != 200 || r.Body["credentials"] != nil {
		t.Fatalf("viewer must not see credentials at all: %d %v", r.Status, r.Body)
	}
}

func TestValidationErrorsAreStable(t *testing.T) {
	f := newAPI(t)
	owner := f.token(t, auth.RoleOwner, "*")
	r := f.do(t, "POST", "/v0/cameras", owner, map[string]any{"name": "", "source": map[string]string{"kind": "rtsp", "address": "rtsp://h/x"}})
	if r.Status != 422 || r.Body["code"] != "invalid" || r.Body["message"] == nil {
		t.Fatalf("invalid: %d %v", r.Status, r.Body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), "POST", f.srv.URL+"/v0/cameras", bytes.NewBufferString("{not json"))
	req.Header.Set("Authorization", "Bearer "+owner)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("bad json: %d", resp.StatusCode)
	}
}
