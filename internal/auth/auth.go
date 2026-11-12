// Package auth defines the access token a client presents to a Hive
// (ADR-0004): PASETO v4.public signed by the cloud with Ed25519, bound to
// one hive, one session and a set of cameras with a role. The Issuer lives
// in the cloud (and in tests); the Verifier runs on the hive with the
// cloud's public keys, which rotate by key id.
package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"aidanwoods.dev/go-paseto"
)

// Role is what a token holder may do with the cameras it names.
type Role string

// Roles, from least to most capable.
const (
	RoleViewer   Role = "viewer"   // live and archive
	RoleOperator Role = "operator" // + PTZ and export
	RoleOwner    Role = "owner"    // + manage cameras and hive settings
)

// Action is something a client wants to do.
type Action string

// Actions.
const (
	ActionLive    Action = "live"
	ActionArchive Action = "archive"
	ActionPTZ     Action = "ptz"
	ActionExport  Action = "export"
	ActionManage  Action = "manage"
)

var roleActions = map[Role]map[Action]bool{
	RoleViewer:   {ActionLive: true, ActionArchive: true},
	RoleOperator: {ActionLive: true, ActionArchive: true, ActionPTZ: true, ActionExport: true},
	RoleOwner:    {ActionLive: true, ActionArchive: true, ActionPTZ: true, ActionExport: true, ActionManage: true},
}

// Limits on token lifetime.
const (
	MaxOnlineTTL  = 15 * time.Minute
	MaxOfflineTTL = 7 * 24 * time.Hour
)

// Errors.
var (
	ErrInvalidToken = errors.New("auth: invalid token")
	ErrExpired      = errors.New("auth: token expired")
	ErrWrongHive    = errors.New("auth: token is for another hive")
	ErrLicense      = errors.New("auth: offline token requires a valid license")
	// ErrNotYetValid wraps ErrInvalidToken: the token was issued in what this
	// hive believes is the future, which in practice means the hive's clock
	// is behind the cloud's.
	ErrNotYetValid   = fmt.Errorf("%w: not yet valid (hive clock behind the cloud?)", ErrInvalidToken)
	ErrInvalidClaims = errors.New("auth: invalid claims")
)

// Claims is what a token says.
type Claims struct {
	Account   string
	HiveID    string
	SessionID string
	Cameras   []string // camera ids, or ["*"] for all
	Role      Role
	Offline   bool // long-lived LAN token, valid only while the hive's license is
	IssuedAt  time.Time
	Expiry    time.Time
	// Plan-derived limits the hive enforces locally (ADR-0006). Empty or
	// zero means "not restricted" so tokens minted before these existed
	// keep working.
	Quality          string // remote live quality: sub | full
	ExportMaxMinutes int    // longest export; 0 or -1 = no cap
}

// RemoteStream is the stream a remote request may have: a plan with
// remote quality "sub" gets the sub stream whatever was asked for (the hive
// falls back to main when a camera has no sub stream).
func RemoteStream(quality, want string) string {
	if quality == "sub" {
		return "sub"
	}
	return want
}

// ExportAllowed reports whether an export of d fits the token's cap.
func (c Claims) ExportAllowed(d time.Duration) bool {
	return c.ExportMaxMinutes <= 0 || d <= time.Duration(c.ExportMaxMinutes)*time.Minute
}

// Allows reports whether the claims permit action on camera (empty camera =
// hive-level action, owner only).
func (c Claims) Allows(camera string, action Action) bool {
	if !roleActions[c.Role][action] {
		return false
	}
	if camera == "" {
		return c.Role == RoleOwner
	}
	for _, id := range c.Cameras {
		if id == "*" || id == camera {
			return true
		}
	}
	return false
}

func (c Claims) validate() error {
	switch {
	case c.Account == "" || c.HiveID == "" || c.SessionID == "":
		return fmt.Errorf("%w: account, hive and session are required", ErrInvalidClaims)
	case roleActions[c.Role] == nil:
		return fmt.Errorf("%w: unknown role %q", ErrInvalidClaims, c.Role)
	case !c.Expiry.After(c.IssuedAt):
		return fmt.Errorf("%w: expiry before issue", ErrInvalidClaims)
	case !c.Offline && c.Expiry.Sub(c.IssuedAt) > MaxOnlineTTL:
		return fmt.Errorf("%w: online token lifetime exceeds %s", ErrInvalidClaims, MaxOnlineTTL)
	case c.Offline && c.Expiry.Sub(c.IssuedAt) > MaxOfflineTTL:
		return fmt.Errorf("%w: offline token lifetime exceeds %s", ErrInvalidClaims, MaxOfflineTTL)
	}
	return nil
}

// Issuer signs tokens. One per signing key; the key id travels in the footer.
type Issuer struct {
	kid string
	key paseto.V4AsymmetricSecretKey
}

// NewIssuer generates a fresh Ed25519 signing key with the given key id.
func NewIssuer(kid string) (*Issuer, error) {
	if kid == "" {
		return nil, errors.New("auth: key id is required")
	}
	return &Issuer{kid: kid, key: paseto.NewV4AsymmetricSecretKey()}, nil
}

// NewIssuerFromKey wraps a persisted Ed25519 private key (token key files).
func NewIssuerFromKey(kid string, key ed25519.PrivateKey) (*Issuer, error) {
	if kid == "" {
		return nil, errors.New("auth: key id is required")
	}
	sk, err := paseto.NewV4AsymmetricSecretKeyFromEd25519(key)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &Issuer{kid: kid, key: sk}, nil
}

// KeyID returns the issuer's key id.
func (i *Issuer) KeyID() string { return i.kid }

// PublicKey returns the verification key to distribute to hives.
func (i *Issuer) PublicKey() ed25519.PublicKey {
	return ed25519.PublicKey(i.key.Public().ExportBytes())
}

// Issue signs claims into a token.
func (i *Issuer) Issue(c Claims) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	tok := paseto.NewToken()
	tok.SetSubject(c.Account)
	tok.SetAudience(c.HiveID)
	tok.SetIssuedAt(c.IssuedAt)
	tok.SetNotBefore(c.IssuedAt)
	tok.SetExpiration(c.Expiry)
	tok.SetString("sid", c.SessionID)
	tok.SetString("role", string(c.Role))
	if err := tok.Set("cams", c.Cameras); err != nil {
		return "", err
	}
	if c.Offline {
		tok.SetString("off", "1")
	}
	if c.Quality != "" {
		tok.SetString("q", c.Quality)
	}
	if c.ExportMaxMinutes != 0 {
		if err := tok.Set("xmin", c.ExportMaxMinutes); err != nil {
			return "", err
		}
	}
	footer, _ := json.Marshal(map[string]string{"kid": i.kid})
	tok.SetFooter(footer)
	return tok.V4Sign(i.key, nil), nil
}

// Keys maps key id to public key; serialised for distribution to hives.
type Keys map[string]ed25519.PublicKey

// MarshalJSON encodes keys as {"kid": "<hex>"}.
func (k Keys) MarshalJSON() ([]byte, error) {
	m := make(map[string]string, len(k))
	for kid, pub := range k {
		m[kid] = hex.EncodeToString(pub)
	}
	return json.Marshal(m)
}

// UnmarshalJSON decodes {"kid": "<hex>"}.
func (k *Keys) UnmarshalJSON(raw []byte) error {
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	out := make(Keys, len(m))
	for kid, h := range m {
		b, err := hex.DecodeString(h)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return fmt.Errorf("auth: key %q: bad public key", kid)
		}
		out[kid] = ed25519.PublicKey(b)
	}
	*k = out
	return nil
}

// Verifier checks tokens on a hive.
type Verifier struct {
	hiveID       string
	now          func() time.Time
	licenseValid func() bool

	mu      sync.RWMutex
	keys    Keys
	revoked map[string]time.Time // session id → when the revocation can be forgotten
}

// VerifierOption customises a Verifier.
type VerifierOption func(*Verifier)

// WithNow overrides the clock (tests).
func WithNow(now func() time.Time) VerifierOption { return func(v *Verifier) { v.now = now } }

// WithLicenseValid supplies the check offline tokens depend on; default: never valid.
func WithLicenseValid(f func() bool) VerifierOption { return func(v *Verifier) { v.licenseValid = f } }

// NewVerifier creates a Verifier for this hive with the cloud's public keys.
func NewVerifier(hiveID string, keys Keys, opts ...VerifierOption) *Verifier {
	v := &Verifier{hiveID: hiveID, keys: keys, now: time.Now, licenseValid: func() bool { return false }, revoked: map[string]time.Time{}}
	for _, o := range opts {
		o(v)
	}
	return v
}

// SetKeys replaces the trusted cloud keys (pushed over the channel).
func (v *Verifier) SetKeys(keys Keys) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys = keys
}

// Revoke refuses tokens of a session until `until`, after which the entry
// is dropped (tokens of that session have expired by then anyway).
func (v *Verifier) Revoke(sessionID string, until time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	for id, t := range v.revoked {
		if !t.After(now) {
			delete(v.revoked, id)
		}
	}
	v.revoked[sessionID] = until
}

// ErrRevoked means the session behind the token was revoked by the cloud.
var ErrRevoked = errors.New("auth: session revoked")

// Verify checks signature, lifetime, audience and (for offline tokens) the
// license, and returns the claims.
func (v *Verifier) Verify(token string) (Claims, error) {
	kid, err := footerKeyID(token)
	if err != nil {
		return Claims{}, err
	}
	v.mu.RLock()
	pub, ok := v.keys[kid]
	v.mu.RUnlock()
	if !ok {
		return Claims{}, fmt.Errorf("%w: unknown key id %q", ErrInvalidToken, kid)
	}
	key, err := paseto.NewV4AsymmetricPublicKeyFromBytes(pub)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	parser := paseto.NewParserWithoutExpiryCheck() // time is validated against our own clock below
	tok, err := parser.ParseV4Public(key, token, nil)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	c, err := claimsFromToken(tok)
	if err != nil {
		return Claims{}, err
	}
	now := v.now()
	switch {
	case now.Before(c.IssuedAt.Add(-time.Minute)): // small skew allowance
		return Claims{}, ErrNotYetValid
	case !now.Before(c.Expiry):
		return Claims{}, ErrExpired
	case c.HiveID != v.hiveID:
		return Claims{}, ErrWrongHive
	case c.Offline && !v.licenseValid():
		return Claims{}, ErrLicense
	case v.isRevoked(c.SessionID, now):
		return Claims{}, ErrRevoked
	}
	return c, nil
}

func (v *Verifier) isRevoked(sid string, now time.Time) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	until, ok := v.revoked[sid]
	return ok && until.After(now)
}

func claimsFromToken(tok *paseto.Token) (Claims, error) {
	var c Claims
	var err error
	if c.Account, err = tok.GetSubject(); err != nil {
		return Claims{}, fmt.Errorf("%w: sub", ErrInvalidToken)
	}
	if c.HiveID, err = tok.GetAudience(); err != nil {
		return Claims{}, fmt.Errorf("%w: aud", ErrInvalidToken)
	}
	if c.IssuedAt, err = tok.GetIssuedAt(); err != nil {
		return Claims{}, fmt.Errorf("%w: iat", ErrInvalidToken)
	}
	if c.Expiry, err = tok.GetExpiration(); err != nil {
		return Claims{}, fmt.Errorf("%w: exp", ErrInvalidToken)
	}
	if c.SessionID, err = tok.GetString("sid"); err != nil {
		return Claims{}, fmt.Errorf("%w: sid", ErrInvalidToken)
	}
	role, err := tok.GetString("role")
	if err != nil {
		return Claims{}, fmt.Errorf("%w: role", ErrInvalidToken)
	}
	c.Role = Role(role)
	if err := tok.Get("cams", &c.Cameras); err != nil {
		return Claims{}, fmt.Errorf("%w: cams", ErrInvalidToken)
	}
	if off, err := tok.GetString("off"); err == nil && off == "1" {
		c.Offline = true
	}
	if q, err := tok.GetString("q"); err == nil {
		c.Quality = q
	}
	_ = tok.Get("xmin", &c.ExportMaxMinutes) // absent on older tokens
	if err := c.validate(); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	return c, nil
}

// footerKeyID reads the kid from the (signed) footer without verifying yet;
// it only selects which key to verify with.
func footerKeyID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v4" || parts[1] != "public" {
		return "", fmt.Errorf("%w: not a v4.public token", ErrInvalidToken)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", fmt.Errorf("%w: footer", ErrInvalidToken)
	}
	var f struct {
		KID string `json:"kid"`
	}
	if err := json.Unmarshal(raw, &f); err != nil || f.KID == "" {
		return "", fmt.Errorf("%w: footer kid", ErrInvalidToken)
	}
	return f.KID, nil
}
