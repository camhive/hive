package hiveagent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/entitlements"
	"github.com/camhive/hive/internal/protocol"
)

// Licensing on the hive (ADR-0006, #38): the cloud pushes a signed license
// (license.set {token}); the agent verifies it with the cloud keys it
// holds, stores it, and exposes the effective entitlements with grace and
// expiry handled by auth.License. Local enforcement points (PTZ, retention
// cap, offline tokens) read Licensed().

// Licensing holds the current license.
type Licensing struct {
	mu  sync.RWMutex
	lic auth.License
	now func() time.Time
}

// NewLicensing creates an empty Licensing (everything on the Free seed).
func NewLicensing(now func() time.Time) *Licensing {
	if now == nil {
		now = time.Now
	}
	return &Licensing{now: now}
}

// Set replaces the license.
func (l *Licensing) Set(lic auth.License) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lic = lic
}

// Current returns the stored license (zero when none).
func (l *Licensing) Current() auth.License {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.lic
}

// State is the license state now.
func (l *Licensing) State() auth.LicenseState { return l.Current().State(l.now()) }

// Effective is the set to enforce now.
func (l *Licensing) Effective() entitlements.Set { return l.Current().Effective(l.now()) }

// Usable is what auth.WithLicenseValid wants: offline tokens are accepted
// while the license is valid or in grace.
func (l *Licensing) Usable() bool { return l.Current().Usable(l.now()) }

// licenseSet verifies, stores and applies a pushed license.
func (a *Agent) licenseSet(ctx context.Context, args json.RawMessage) protocol.Result {
	var in struct {
		Token string `json:"token"`
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	if a.d.Verifier == nil || a.d.License == nil {
		return protocol.Fail(protocol.ErrCodeNotReady, "licensing not available")
	}
	lic, err := a.d.Verifier.VerifyLicense(in.Token)
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInvalid, err.Error())
	}
	if r := a.putState(ctx, StateLicense, args); r != nil {
		return *r
	}
	a.d.License.Set(lic)
	a.record(ctx, "license.set", "", map[string]any{"expires_at": lic.ExpiresAt, "state": lic.State(a.d.Now())})
	return protocol.Succeed(map[string]any{"state": lic.State(a.d.Now()), "expires_at": lic.ExpiresAt})
}

// LoadLicense restores the stored license at start, if it still verifies.
func (a *Agent) LoadLicense(ctx context.Context) {
	if a.d.Store == nil || a.d.Verifier == nil || a.d.License == nil {
		return
	}
	raw, _, err := a.d.Store.CloudState().Get(ctx, StateLicense)
	if err != nil {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return
	}
	lic, err := a.d.Verifier.VerifyLicense(in.Token)
	if err != nil {
		a.log.Warn("stored license", "err", err)
		return
	}
	a.d.License.Set(lic)
	a.log.Info("license restored", "state", lic.State(a.d.Now()), "expires_at", lic.ExpiresAt)
}
