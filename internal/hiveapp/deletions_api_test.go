package hiveapp_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/hiveapp"
	"github.com/camhive/hive/internal/retention"
	"github.com/camhive/hive/internal/store"
)

// deleterFake records the scopes it is asked to delete.
type deleterFake struct {
	scopes []retention.Scope
	dry    []bool
	plan   retention.Plan
	err    error
}

func (d *deleterFake) Delete(_ context.Context, sc retention.Scope, dryRun bool) (retention.Plan, error) {
	d.scopes, d.dry = append(d.scopes, sc), append(d.dry, dryRun)
	return d.plan, d.err
}

func newDeletionAPI(t *testing.T, d hiveapp.Deleter) *apiFixture {
	t.Helper()
	iss, _ := auth.NewIssuer("k1")
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ver := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()})
	srv := httptest.NewServer(hiveapp.NewAPI(hiveapp.Deps{Store: st, Verifier: ver, Version: "test", Deletions: d}))
	t.Cleanup(srv.Close)
	return &apiFixture{srv: srv, iss: iss, st: st}
}

// ADR-0016: the owner previews, then deletes; the deletion is audited.
func TestOwnerPreviewsThenDeletesRecordingsAndTheDeletionIsAudited(t *testing.T) {
	from, to := time.Date(2026, 11, 9, 10, 5, 0, 0, time.UTC), time.Date(2026, 11, 9, 10, 15, 0, 0, time.UTC)
	d := &deleterFake{plan: retention.Plan{
		Cameras: []retention.CameraPlan{{Camera: "cam_1", From: from.Add(-5 * time.Minute), To: to.Add(5 * time.Minute), Segments: 4, Bytes: 400, Pending: 1}},
		Exports: export.Pruned{Jobs: 1, Bytes: 7},
	}}
	f := newDeletionAPI(t, d)
	owner := f.token(t, auth.RoleOwner, "*")
	body := map[string]any{"cameras": []string{"cam_1"}, "from": from, "to": to, "include_exports": true, "dry_run": true}

	r := f.do(t, "POST", "/v0/archive/deletions", owner, body)
	if r.Status != 200 {
		t.Fatalf("preview: %d %v", r.Status, r.Body)
	}
	if sc := d.scopes[0]; !d.dry[0] || len(sc.Cameras) != 1 || sc.Cameras[0] != "cam_1" || !sc.From.Equal(from) || !sc.To.Equal(to) || !sc.Exports {
		t.Fatalf("scope = %+v dry %v", sc, d.dry)
	}
	cams := r.Body["cameras"].([]any)
	c := cams[0].(map[string]any)
	if c["camera"] != "cam_1" || c["from"] != "2026-11-09T10:00:00Z" || c["to"] != "2026-11-09T10:20:00Z" || c["segments"] != 4.0 || c["bytes"] != 400.0 || c["pending"] != 1.0 {
		t.Fatalf("camera plan = %v", c)
	}
	if ex := r.Body["exports"].(map[string]any); ex["count"] != 1.0 || ex["bytes"] != 7.0 {
		t.Fatalf("exports = %v", ex)
	}
	if a := f.do(t, "GET", "/v0/actions", owner, nil); len(a.List) != 0 {
		t.Fatalf("a preview is not audited: %v", a.List)
	}

	body["dry_run"] = false
	if r := f.do(t, "POST", "/v0/archive/deletions", owner, body); r.Status != 200 || d.dry[1] {
		t.Fatalf("delete: %d %v", r.Status, r.Body)
	}
	a := f.do(t, "GET", "/v0/actions", owner, nil)
	if len(a.List) != 1 || a.List[0]["action"] != "archive.delete" || a.List[0]["target"] != "cam_1" {
		t.Fatalf("actions = %v", a.List)
	}
	details := a.List[0]["details"].(map[string]any)
	if details["segments"] != 4.0 || details["bytes"] != 400.0 || details["exports"] != 1.0 || details["from"] != "2026-11-09T10:05:00Z" {
		t.Fatalf("details = %v", details)
	}
}

func TestDeletionScopeDefaultsAndRefusals(t *testing.T) {
	d := &deleterFake{}
	f := newDeletionAPI(t, d)
	owner := f.token(t, auth.RoleOwner, "*")

	// No cameras and no range: every camera, everything up to now; a future end is now.
	before := time.Now()
	for _, body := range []map[string]any{{}, {"to": before.Add(time.Hour)}} {
		if r := f.do(t, "POST", "/v0/archive/deletions", owner, body); r.Status != 200 {
			t.Fatalf("%v: %d %v", body, r.Status, r.Body)
		}
		sc := d.scopes[len(d.scopes)-1]
		if sc.Cameras != nil || !sc.From.IsZero() || sc.To.Before(before) || sc.To.After(time.Now()) {
			t.Fatalf("%v: scope = %+v", body, sc)
		}
	}

	for _, tc := range []struct {
		name   string
		token  string
		body   map[string]any
		status int
		code   string
	}{
		{"operator", f.token(t, auth.RoleOperator, "*"), map[string]any{}, 403, "forbidden"},
		{"viewer", f.token(t, auth.RoleViewer, "*"), map[string]any{}, 403, "forbidden"},
		{"no cameras", owner, map[string]any{"cameras": []string{}}, 422, "invalid"},
		{"backwards", owner, map[string]any{"from": "2026-11-09T10:00:00Z", "to": "2026-11-09T09:00:00Z"}, 422, "invalid"},
	} {
		if r := f.do(t, "POST", "/v0/archive/deletions", tc.token, tc.body); r.Status != tc.status || r.Body["code"] != tc.code {
			t.Errorf("%s: %d %v", tc.name, r.Status, r.Body)
		}
	}

	d.err = export.ErrRunning
	if r := f.do(t, "POST", "/v0/archive/deletions", owner, map[string]any{"include_exports": true}); r.Status != 409 || r.Body["code"] != "export_running" {
		t.Fatalf("running export: %d %v", r.Status, r.Body)
	}

	plain := newDeletionAPI(t, nil)
	if r := plain.do(t, "POST", "/v0/archive/deletions", plain.token(t, auth.RoleOwner, "*"), map[string]any{}); r.Status != 503 || r.Body["code"] != "not_ready" {
		t.Fatalf("without the sweeper: %d %v", r.Status, r.Body)
	}
}
