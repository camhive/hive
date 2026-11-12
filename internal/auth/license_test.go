package auth_test

import (
	"errors"
	"testing"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/entitlements"
)

func TestLicenseRoundTripStatesAndOfflineTokens(t *testing.T) {
	iss, ver := newPair(t)
	pro := entitlements.OnPlan(entitlements.PlanPro).Set
	lic := auth.License{HiveID: "hv_1", Entitlements: pro, IssuedAt: t0, ExpiresAt: t0.Add(auth.LicenseTTL)}
	tok, err := iss.IssueLicense(lic)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ver.VerifyLicense(tok)
	if err != nil || got.HiveID != "hv_1" || got.Entitlements.Int(entitlements.HivesMax) != 10 || !got.ExpiresAt.Equal(lic.ExpiresAt) {
		t.Fatalf("got = %+v err=%v", got, err)
	}
	// States with a moving clock.
	for _, c := range []struct {
		at   time.Time
		want auth.LicenseState
		ptz  bool
		full bool
	}{
		{t0.Add(time.Hour), auth.LicenseValid, true, true},
		{t0.Add(auth.LicenseTTL + 24*time.Hour), auth.LicenseGraceOn, true, true},
		{t0.Add(auth.LicenseTTL + auth.LicenseGrace + time.Minute), auth.LicenseExpired, true, false},
	} {
		if s := got.State(c.at); s != c.want {
			t.Fatalf("state at %v = %s want %s", c.at, s, c.want)
		}
		eff := got.Effective(c.at)
		if eff.Bool(entitlements.PTZEnabled) != c.ptz || eff.AtLeast(entitlements.AnalyticsLevel, "full") != c.full {
			t.Fatalf("effective at %v = %v", c.at, eff)
		}
		if got.Usable(c.at) != (c.want != auth.LicenseExpired) {
			t.Fatalf("usable at %v", c.at)
		}
	}
	if (auth.License{}).State(t0) != auth.LicenseNone || (auth.License{}).Usable(t0) {
		t.Fatal("empty license")
	}
	// Offline tokens (fresh at each moment) are accepted while the license
	// is valid or in grace and refused after.
	for _, c := range []struct {
		at   time.Time
		want error
	}{{t0.Add(time.Hour), nil}, {t0.Add(auth.LicenseTTL + time.Hour), nil}, {t0.Add(auth.LicenseTTL + auth.LicenseGrace + time.Hour), auth.ErrLicense}} {
		offline := claims()
		offline.Offline, offline.IssuedAt, offline.Expiry = true, c.at.Add(-time.Hour), c.at.Add(time.Hour)
		otok, _ := iss.Issue(offline)
		v := auth.NewVerifier("hv_1", auth.Keys{"k1": iss.PublicKey()}, auth.WithNow(fixedNow(c.at)), auth.WithLicenseValid(func() bool { return got.Usable(c.at) }))
		if _, err := v.Verify(otok); !errors.Is(err, c.want) {
			t.Fatalf("offline token at %v: %v (want %v)", c.at, err, c.want)
		}
	}
	// Verification failures.
	other, _ := auth.NewIssuer("k1")
	forged, _ := other.IssueLicense(lic)
	if _, err := ver.VerifyLicense(forged); !errors.Is(err, auth.ErrInvalidLicense) {
		t.Fatalf("forged: %v", err)
	}
	foreign, _ := iss.IssueLicense(auth.License{HiveID: "hv_2", Entitlements: pro, IssuedAt: t0, ExpiresAt: t0.Add(time.Hour)})
	if _, err := ver.VerifyLicense(foreign); !errors.Is(err, auth.ErrInvalidLicense) {
		t.Fatalf("foreign hive: %v", err)
	}
	access, _ := iss.Issue(claims())
	if _, err := ver.VerifyLicense(access); !errors.Is(err, auth.ErrInvalidLicense) {
		t.Fatalf("access token as license: %v", err)
	}
	if _, err := iss.IssueLicense(auth.License{HiveID: "hv_1"}); !errors.Is(err, auth.ErrInvalidLicense) {
		t.Fatal("empty entitlements")
	}
	if _, err := iss.IssueLicense(auth.License{HiveID: "hv_1", Entitlements: entitlements.Set{"nope": entitlements.I(1)}, IssuedAt: t0, ExpiresAt: t0.Add(time.Hour)}); !errors.Is(err, auth.ErrInvalidLicense) {
		t.Fatal("invalid entitlements")
	}
}
