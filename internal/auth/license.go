package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"aidanwoods.dev/go-paseto"

	"github.com/camhive/hive/internal/entitlements"
)

// License is the cloud's signed statement of what a hive may do locally
// (ADR-0006): the owner's effective entitlements with a short lifetime,
// refreshed on every connection. Signed with the access-token keys, so the
// hive verifies it with the key set it already holds.
type License struct {
	HiveID       string
	Entitlements entitlements.Set
	IssuedAt     time.Time
	ExpiresAt    time.Time
}

// License lifetimes (docs/PLAN.md §11.3).
const (
	LicenseTTL   = 7 * 24 * time.Hour  // refreshed on every connection
	LicenseGrace = 30 * 24 * time.Hour // after expiry: still enforced as valid, with a warning
)

// LicenseState is where a license is in its life.
type LicenseState string

// States.
const (
	LicenseValid   LicenseState = "valid"
	LicenseGraceOn LicenseState = "grace"
	LicenseExpired LicenseState = "expired"
	LicenseNone    LicenseState = "none"
)

// State classifies the license at t.
func (l License) State(t time.Time) LicenseState {
	switch {
	case l.HiveID == "":
		return LicenseNone
	case t.Before(l.ExpiresAt):
		return LicenseValid
	case t.Before(l.ExpiresAt.Add(LicenseGrace)):
		return LicenseGraceOn
	default:
		return LicenseExpired
	}
}

// Effective returns the entitlements to enforce at t: the licensed set
// while valid or in grace, the Free seed afterwards (paid local features
// pause; recording and the owner's LAN access never depend on this).
func (l License) Effective(t time.Time) entitlements.Set {
	if s := l.State(t); s == LicenseValid || s == LicenseGraceOn {
		return l.Entitlements
	}
	return entitlements.OnPlan(entitlements.PlanFree).Set
}

// Usable reports whether offline tokens may be accepted (valid or grace).
func (l License) Usable(t time.Time) bool {
	s := l.State(t)
	return s == LicenseValid || s == LicenseGraceOn
}

// ErrInvalidLicense wraps every license verification failure.
var ErrInvalidLicense = errors.New("auth: invalid license")

// IssueLicense signs a license.
func (i *Issuer) IssueLicense(l License) (string, error) {
	if l.HiveID == "" || len(l.Entitlements) == 0 || !l.ExpiresAt.After(l.IssuedAt) {
		return "", fmt.Errorf("%w: hive, entitlements and a lifetime are required", ErrInvalidLicense)
	}
	if err := l.Entitlements.Validate(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidLicense, err)
	}
	tok := paseto.NewToken()
	tok.SetSubject(l.HiveID)
	tok.SetIssuedAt(l.IssuedAt)
	tok.SetNotBefore(l.IssuedAt)
	tok.SetExpiration(l.ExpiresAt)
	tok.SetString("typ", "license")
	raw, _ := json.Marshal(l.Entitlements)
	if err := tok.Set("ent", json.RawMessage(raw)); err != nil {
		return "", err
	}
	footer, _ := json.Marshal(map[string]string{"kid": i.kid})
	tok.SetFooter(footer)
	return tok.V4Sign(i.key, nil), nil
}

// VerifyLicense checks the signature and that the license names this
// hive; expiry is not checked here (State does, with grace) so a stored
// license can still be classified after it ran out.
func (v *Verifier) VerifyLicense(token string) (License, error) {
	kid, err := footerKeyID(token)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrInvalidLicense, err)
	}
	v.mu.RLock()
	pub, ok := v.keys[kid]
	v.mu.RUnlock()
	if !ok {
		return License{}, fmt.Errorf("%w: unknown key id %q", ErrInvalidLicense, kid)
	}
	key, err := paseto.NewV4AsymmetricPublicKeyFromBytes(pub)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrInvalidLicense, err)
	}
	tok, err := paseto.NewParserWithoutExpiryCheck().ParseV4Public(key, token, nil)
	if err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrInvalidLicense, err)
	}
	if typ, err := tok.GetString("typ"); err != nil || typ != "license" {
		return License{}, fmt.Errorf("%w: not a license", ErrInvalidLicense)
	}
	var l License
	if l.HiveID, err = tok.GetSubject(); err != nil || l.HiveID != v.hiveID {
		return License{}, fmt.Errorf("%w: license is for another hive", ErrInvalidLicense)
	}
	if l.IssuedAt, err = tok.GetIssuedAt(); err != nil {
		return License{}, fmt.Errorf("%w: iat", ErrInvalidLicense)
	}
	if l.ExpiresAt, err = tok.GetExpiration(); err != nil {
		return License{}, fmt.Errorf("%w: exp", ErrInvalidLicense)
	}
	var raw json.RawMessage
	if err := tok.Get("ent", &raw); err != nil {
		return License{}, fmt.Errorf("%w: ent", ErrInvalidLicense)
	}
	if err := json.Unmarshal(raw, &l.Entitlements); err != nil {
		return License{}, fmt.Errorf("%w: %w", ErrInvalidLicense, err)
	}
	return l, nil
}
