package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
)

var t0 = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func newPair(t *testing.T) (*auth.Issuer, *auth.Verifier) {
	t.Helper()
	iss, err := auth.NewIssuer("k1")
	if err != nil {
		t.Fatal(err)
	}
	ver := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}, auth.WithNow(fixedNow(t0)))
	return iss, ver
}

func claims() auth.Claims {
	return auth.Claims{
		Account: "acc_1", HiveID: "hv_1", SessionID: "sid_1",
		Cameras: []string{"cam_a", "cam_b"}, Role: auth.RoleViewer,
		IssuedAt: t0, Expiry: t0.Add(15 * time.Minute),
	}
}

func TestIssueAndVerifyRoundTrip(t *testing.T) {
	iss, ver := newPair(t)
	tok, err := iss.Issue(claims())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "v4.public.") {
		t.Fatalf("token = %q", tok)
	}
	got, err := ver.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	want := claims()
	if got.Account != want.Account || got.HiveID != want.HiveID || got.SessionID != want.SessionID || got.Role != want.Role ||
		len(got.Cameras) != 2 || got.Cameras[1] != "cam_b" || !got.Expiry.Equal(want.Expiry) || got.Offline {
		t.Fatalf("claims = %+v", got)
	}
}

func TestVerifyRejectsTamperedExpiredForeignAndWrongHive(t *testing.T) {
	iss, ver := newPair(t)

	tok, _ := iss.Issue(claims())
	tampered := tok[:len(tok)-4] + "AAAA"
	if _, err := ver.Verify(tampered); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("tampered: %v", err)
	}
	if _, err := ver.Verify("not a token"); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("garbage: %v", err)
	}

	// The issuer refuses to mint an already-expired token, so expiry is
	// exercised by moving the verifier's clock past a valid token's lifetime.
	tok, _ = iss.Issue(claims())
	late := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}, auth.WithNow(fixedNow(t0.Add(16*time.Minute))))
	if _, err := late.Verify(tok); !errors.Is(err, auth.ErrExpired) {
		t.Errorf("expired: %v", err)
	}
	early := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}, auth.WithNow(fixedNow(t0.Add(-time.Hour))))
	if _, err := early.Verify(tok); !errors.Is(err, auth.ErrNotYetValid) || !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("not yet valid: %v", err)
	}

	c := claims()
	c.HiveID = "hv_other"
	tok, _ = iss.Issue(c)
	if _, err := ver.Verify(tok); !errors.Is(err, auth.ErrWrongHive) {
		t.Errorf("wrong hive: %v", err)
	}

	other, _ := auth.NewIssuer("k1") // same kid, different key: must not verify
	tok, _ = other.Issue(claims())
	if _, err := ver.Verify(tok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("foreign key: %v", err)
	}

	unknown, _ := auth.NewIssuer("k9")
	tok, _ = unknown.Issue(claims())
	if _, err := ver.Verify(tok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("unknown kid: %v", err)
	}
}

func TestKeyRotationVerifiesBothKids(t *testing.T) {
	old, _ := auth.NewIssuer("k1")
	cur, _ := auth.NewIssuer("k2")
	ver := auth.NewVerifier("hv_1", auth.Keys{"k1": old.PublicKey(), "k2": cur.PublicKey()}, auth.WithNow(fixedNow(t0)))
	for _, iss := range []*auth.Issuer{old, cur} {
		tok, _ := iss.Issue(claims())
		if _, err := ver.Verify(tok); err != nil {
			t.Fatalf("kid %s: %v", iss.KeyID(), err)
		}
	}
}

func TestOfflineTokensOnlyWhileLicenseIsValid(t *testing.T) {
	iss, _ := newPair(t)
	c := claims()
	c.Offline = true
	c.Expiry = t0.Add(7 * 24 * time.Hour)
	tok, _ := iss.Issue(c)

	valid := true
	ver := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}, auth.WithNow(fixedNow(t0)), auth.WithLicenseValid(func() bool { return valid }))
	if _, err := ver.Verify(tok); err != nil {
		t.Fatalf("offline token with valid license: %v", err)
	}
	valid = false
	if _, err := ver.Verify(tok); !errors.Is(err, auth.ErrLicense) {
		t.Fatalf("offline token without license: %v, want ErrLicense", err)
	}
	// A normal (online) token does not depend on the license.
	tok, _ = iss.Issue(claims())
	if _, err := ver.Verify(tok); err != nil {
		t.Fatalf("online token without license: %v", err)
	}
}

func TestIssueRejectsBadClaims(t *testing.T) {
	iss, _ := newPair(t)
	bad := []auth.Claims{
		{},
		{Account: "a", HiveID: "h", SessionID: "s", Role: "god", Expiry: t0.Add(time.Minute), IssuedAt: t0},
		{Account: "a", HiveID: "h", SessionID: "s", Role: auth.RoleViewer, Expiry: t0.Add(-time.Minute), IssuedAt: t0},
		{Account: "a", HiveID: "h", SessionID: "s", Role: auth.RoleViewer, Expiry: t0.Add(time.Hour), IssuedAt: t0}, // > max online TTL
	}
	for i, c := range bad {
		if _, err := iss.Issue(c); !errors.Is(err, auth.ErrInvalidClaims) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestClaimsAllows(t *testing.T) {
	viewer := auth.Claims{Cameras: []string{"cam_a"}, Role: auth.RoleViewer}
	operator := auth.Claims{Cameras: []string{"cam_a"}, Role: auth.RoleOperator}
	owner := auth.Claims{Cameras: []string{"*"}, Role: auth.RoleOwner}

	cases := []struct {
		c      auth.Claims
		cam    string
		action auth.Action
		want   bool
	}{
		{viewer, "cam_a", auth.ActionLive, true},
		{viewer, "cam_a", auth.ActionArchive, true},
		{viewer, "cam_a", auth.ActionPTZ, false},
		{viewer, "cam_a", auth.ActionExport, false},
		{viewer, "cam_z", auth.ActionLive, false},
		{operator, "cam_a", auth.ActionPTZ, true},
		{operator, "cam_a", auth.ActionExport, true},
		{operator, "cam_a", auth.ActionManage, false},
		{owner, "cam_anything", auth.ActionManage, true},
		{owner, "", auth.ActionManage, true}, // hive-level management
		{viewer, "", auth.ActionManage, false},
	}
	for _, tc := range cases {
		if got := tc.c.Allows(tc.cam, tc.action); got != tc.want {
			t.Errorf("%s/%s on %q = %v, want %v", tc.c.Role, tc.action, tc.cam, got, tc.want)
		}
	}
}

func TestKeysMarshalForDistributionToHives(t *testing.T) {
	iss, _ := auth.NewIssuer("k1")
	raw, err := auth.Keys{"k1": iss.PublicKey()}.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var back auth.Keys
	if err := back.UnmarshalJSON(raw); err != nil {
		t.Fatal(err)
	}
	if !back["k1"].Equal(iss.PublicKey()) {
		t.Fatal("key set did not round trip")
	}
	if err := back.UnmarshalJSON([]byte(`{"k1":"zz"}`)); err == nil {
		t.Fatal("bad hex must be rejected")
	}
	if err := back.UnmarshalJSON([]byte(`{"k1":"abcd"}`)); err == nil {
		t.Fatal("wrong key length must be rejected")
	}
	if err := back.UnmarshalJSON([]byte(`nope`)); err == nil {
		t.Fatal("bad json must be rejected")
	}
	if _, err := auth.NewIssuer(""); err == nil {
		t.Fatal("empty kid must be rejected")
	}
}

func TestSetKeysAndRevokeOverTheChannel(t *testing.T) {
	iss, ver := newPair(t)
	tok, _ := iss.Issue(claims())
	// Cloud pushes a new key set that no longer trusts k1.
	next, _ := auth.NewIssuer("k2")
	ver.SetKeys(auth.Keys{"k2": next.PublicKey()})
	if _, err := ver.Verify(tok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("old key must be refused after SetKeys: %v", err)
	}
	ver.SetKeys(auth.Keys{"k1": iss.PublicKey(), "k2": next.PublicKey()})
	if _, err := ver.Verify(tok); err != nil {
		t.Fatal(err)
	}
	// Session revocation until the token would have expired.
	ver.Revoke("sid_1", t0.Add(15*time.Minute))
	if _, err := ver.Verify(tok); !errors.Is(err, auth.ErrRevoked) {
		t.Fatalf("revoked session: %v", err)
	}
	other := claims()
	other.SessionID = "sid_2"
	tok2, _ := iss.Issue(other)
	if _, err := ver.Verify(tok2); err != nil {
		t.Fatal("other sessions unaffected")
	}
	// An expired revocation entry is dropped on the next Revoke call.
	late := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}, auth.WithNow(fixedNow(t0.Add(20*time.Minute))))
	late.Revoke("sid_1", t0.Add(15*time.Minute)) // already in the past
	late.Revoke("sid_9", t0.Add(time.Hour))
	tokLate, _ := iss.Issue(auth.Claims{Account: "acc_1", HiveID: "hv_1", SessionID: "sid_1", Cameras: []string{"*"}, Role: auth.RoleViewer,
		IssuedAt: t0.Add(19 * time.Minute), Expiry: t0.Add(30 * time.Minute)})
	if _, err := late.Verify(tokLate); err != nil {
		t.Fatalf("stale revocation must not stick: %v", err)
	}
}

func TestPlanLimitsTravelInTheToken(t *testing.T) {
	iss, ver := newPair(t)
	c := claims()
	c.Quality, c.ExportMaxMinutes = "sub", 5
	tok, _ := iss.Issue(c)
	got, err := ver.Verify(tok)
	if err != nil || got.Quality != "sub" || got.ExportMaxMinutes != 5 {
		t.Fatalf("got = %+v err=%v", got, err)
	}
	if !got.ExportAllowed(5*time.Minute) || got.ExportAllowed(6*time.Minute) {
		t.Fatal("export cap")
	}
	plain, _ := iss.Issue(claims())
	old, _ := ver.Verify(plain)
	if old.Quality != "" || old.ExportMaxMinutes != 0 || !old.ExportAllowed(24*time.Hour) {
		t.Fatal("tokens without limits are unrestricted")
	}
	if !(auth.Claims{ExportMaxMinutes: -1}).ExportAllowed(24 * time.Hour) {
		t.Fatal("-1 means unlimited")
	}
}

// #58: a sub-quality plan gets the sub stream remotely whatever was asked
// for; any other quality keeps the request.
func TestRemoteStreamFollowsThePlanQuality(t *testing.T) {
	for _, c := range []struct{ quality, want, got string }{
		{"sub", "main", "sub"}, {"sub", "", "sub"}, {"full", "main", "main"}, {"full", "sub", "sub"}, {"", "main", "main"},
	} {
		if got := auth.RemoteStream(c.quality, c.want); got != c.got {
			t.Errorf("RemoteStream(%q, %q) = %q, want %q", c.quality, c.want, got, c.got)
		}
	}
}
