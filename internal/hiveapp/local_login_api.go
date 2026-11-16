package hiveapp

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/store"
)

// The local emergency login (#57, ADR-0004): while online, the owner sets a
// username and password that the hive keeps as an argon2id hash. When the
// cloud is unreachable and no offline grant is left, the owner signs in on
// the LAN listener and gets a local session (auth.LocalSessions).

// LocalLoginLockout limits guessing from one source address (an IPv6 /64
// counts as one source): five failures in 15 minutes lock it for 15 minutes.
var LocalLoginLockout = auth.Lockout{MaxFailures: 5, Window: 15 * time.Minute, LockFor: 15 * time.Minute}

// localLoginLockoutAll bounds a guess spread over many addresses: fifty
// failures from anywhere in 15 minutes lock every source.
var localLoginLockoutAll = auth.Lockout{MaxFailures: 50, Window: 15 * time.Minute, LockFor: 15 * time.Minute}

const allSources = "*"

// MaxLocalUsername is the longest local username in characters.
const MaxLocalUsername = 64

// loginLimiter keeps recent local sign-in attempts in memory, per source
// and for all sources together.
type loginLimiter struct {
	now      func() time.Time
	mu       sync.Mutex
	attempts map[string][]auth.Attempt
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	return &loginLimiter{now: now, attempts: map[string][]auth.Attempt{}}
}

func (l *loginLimiter) lockedLocked(source string, now time.Time) time.Time {
	until := LocalLoginLockout.LockedUntil(l.attempts[source], now)
	if all := localLoginLockoutAll.LockedUntil(l.attempts[allSources], now); all.After(until) {
		until = all
	}
	if !until.After(now) {
		return time.Time{}
	}
	return until
}

// attempt is a sign-in that reserve let through. It counts as a failure
// until succeeded drops it.
type attempt struct {
	source string
	at     time.Time
	locks  time.Time // non-zero: this attempt, if it fails, locks the source until then
}

// reserve decides under one lock whether the source may try now and, if
// so, counts the attempt as a failure at once, before the password is
// checked: parallel requests cannot all pass the check before any of them
// counts. It returns the attempt, or when the lock that refuses it ends.
func (l *loginLimiter) reserve(source string) (attempt, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.forgetBefore(now.Add(-max(LocalLoginLockout.Window, localLoginLockoutAll.Window)))
	if until := l.lockedLocked(source, now); !until.IsZero() {
		return attempt{}, until
	}
	for _, k := range []string{source, allSources} {
		l.attempts[k] = append(l.attempts[k], auth.Attempt{At: now})
	}
	return attempt{source: source, at: now, locks: l.lockedLocked(source, now)}, time.Time{}
}

// succeeded turns a reserved attempt into a success: the source's failures
// are forgotten and the attempt stops counting for everyone, while other
// sources' failures still count.
func (l *loginLimiter) succeeded(a attempt) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, a.source)
	all := l.attempts[allSources]
	for i, x := range all {
		if x.At.Equal(a.at) {
			l.attempts[allSources] = append(all[:i], all[i+1:]...)
			break
		}
	}
	if len(l.attempts[allSources]) == 0 {
		delete(l.attempts, allSources)
	}
}

// forgetBefore drops attempts too old to count, so the map stays small.
func (l *loginLimiter) forgetBefore(cutoff time.Time) {
	for k, as := range l.attempts {
		kept := as[:0]
		for _, a := range as {
			if a.At.After(cutoff) {
				kept = append(kept, a)
			}
		}
		if len(kept) == 0 {
			delete(l.attempts, k)
			continue
		}
		l.attempts[k] = kept
	}
}

// sourceKey is the address a limit applies to: the IPv4 address, or the
// /64 of an IPv6 address (one host can use a whole /64).
func sourceKey(host string) string {
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() != nil {
		return host
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// --- owner settings (cloud session) ---------------------------------------

type localLoginView struct {
	Enabled   bool       `json:"enabled"`
	Username  string     `json:"username,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type localLoginInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func localLoginViewOf(c store.LocalCredential) localLoginView {
	at := c.UpdatedAt
	return localLoginView{Enabled: true, Username: c.Username, UpdatedAt: &at}
}

// requireOnlineOwner: changing the local login takes the owner signed in
// through the cloud, never a local session.
func requireOnlineOwner(w http.ResponseWriter, r *http.Request) bool {
	c := claimsFrom(r)
	if !c.Allows("", auth.ActionManage) || c.Local {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner, signed in through Camhive, can change the local login")
		return false
	}
	return true
}

func (a *api) localLoginStatus(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) {
		return
	}
	if !claimsFrom(r).Allows("", auth.ActionManage) {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner can see the local login")
		return
	}
	c, err := a.d.Store.LocalLogin().Get(r.Context())
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusOK, localLoginView{})
	case err != nil:
		a.internal(w, err)
	default:
		writeJSON(w, http.StatusOK, localLoginViewOf(c))
	}
}

// ErrBadUsername rejects a local username the hive will not store.
var ErrBadUsername = errors.New("username must be 1-64 printable characters without spaces at either end")

// NewLocalCredential checks a local username and password against the
// rules (auth.CheckPassword, the username may not be the password) and
// hashes the password with argon2id. The API and `hive local-login` share it.
func NewLocalCredential(username, password string, now time.Time) (store.LocalCredential, error) {
	if username == "" || username != strings.TrimSpace(username) || utf8.RuneCountInString(username) > MaxLocalUsername || strings.ContainsFunc(username, unicode.IsControl) {
		return store.LocalCredential{}, ErrBadUsername
	}
	if err := auth.CheckPassword(password, username); err != nil {
		return store.LocalCredential{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return store.LocalCredential{}, err
	}
	return store.LocalCredential{Username: username, Hash: hash, UpdatedAt: now.UTC()}, nil
}

func (a *api) setLocalLogin(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) || !requireOnlineOwner(w, r) {
		return
	}
	var in localLoginInput
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := NewLocalCredential(in.Username, in.Password, time.Now())
	switch {
	case errors.Is(err, ErrBadUsername):
		writeError(w, http.StatusUnprocessableEntity, "invalid", err.Error())
		return
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(w, http.StatusUnprocessableEntity, "weak_password", "password must be 10-256 characters and not the username")
		return
	case err != nil:
		a.internal(w, err)
		return
	}
	_, prev := a.d.Store.LocalLogin().Get(r.Context())
	if err := a.d.Store.LocalLogin().Set(r.Context(), c); err != nil {
		a.internal(w, err)
		return
	}
	a.record(r, "local_login.enable", "", map[string]any{"username": in.Username, "replaced": prev == nil})
	writeJSON(w, http.StatusOK, localLoginViewOf(c))
}

func (a *api) disableLocalLogin(w http.ResponseWriter, r *http.Request) {
	if !a.requireStore(w) || !requireOnlineOwner(w, r) {
		return
	}
	if err := a.d.Store.LocalLogin().Delete(r.Context()); err != nil {
		a.internal(w, err)
		return
	}
	a.record(r, "local_login.disable", "", nil)
	w.WriteHeader(http.StatusNoContent)
}

// --- sign-in (LAN only, no token) -----------------------------------------

// credentialVersion identifies one setting of the local credential; a local
// token carries it, and any change by the API or the console changes it.
func credentialVersion(c store.LocalCredential) string {
	return c.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// errLocalChanged: the credential the session signed in with was changed or
// removed (in the app or with hive local-login on the console).
var errLocalChanged = fmt.Errorf("%w: the local login changed since this session signed in", auth.ErrInvalidToken)

// stillCurrent refuses a local session whose credential is gone or changed.
func (a *api) stillCurrent(ctx context.Context, c auth.Claims) error {
	cred, err := a.d.Store.LocalLogin().Get(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return errLocalChanged
	case err != nil:
		return err
	case credentialVersion(cred) != c.Credential:
		return errLocalChanged
	}
	return nil
}

type localSessionView struct {
	Token     string    `json:"token"`
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Role      auth.Role `json:"role"`
	Cameras   []string  `json:"cameras"`
	Local     bool      `json:"local"`
}

func (a *api) localSignIn(w http.ResponseWriter, r *http.Request) {
	if !onLAN(r) {
		a.d.Logger.Warn("local sign-in refused off the LAN listener", "remote", r.RemoteAddr, "relayed", Relayed(r.Context()))
		writeError(w, http.StatusForbidden, "lan_only", errLANOnly.Error())
		return
	}
	if !a.requireStore(w) {
		return
	}
	var in localLoginInput
	if !decodeJSON(w, r, &in) {
		return
	}
	cred, ok := a.enabledCredential(w, r)
	if !ok {
		return
	}
	host := remoteHost(r)
	try, refused := a.logins.reserve(sourceKey(host))
	if !refused.IsZero() {
		refuseLocked(w, refused)
		return
	}
	if userOK, ok := a.matches(cred, in); !ok {
		a.localFailure(w, r, try, host, userOK)
		return
	}
	a.logins.succeeded(try)
	a.localSuccess(w, r, host, cred)
}

// enabledCredential returns the local credential, or answers why there is none.
func (a *api) enabledCredential(w http.ResponseWriter, r *http.Request) (store.LocalCredential, bool) {
	cred, err := a.d.Store.LocalLogin().Get(r.Context())
	switch {
	case errors.Is(err, store.ErrNotFound) || a.d.Local == nil:
		writeError(w, http.StatusForbidden, "local_login_disabled", "the local login is not enabled on this hive")
		return cred, false
	case err != nil:
		a.internal(w, err)
		return cred, false
	}
	return cred, true
}

func (a *api) localSuccess(w http.ResponseWriter, r *http.Request, host string, cred store.LocalCredential) {
	tok, c, err := a.d.Local.Issue("local:"+cred.Username, credentialVersion(cred))
	if err != nil {
		a.internal(w, err)
		return
	}
	a.recordAs(r, c.Account, "local_login.success", host, map[string]any{"session": c.SessionID})
	writeJSON(w, http.StatusCreated, localSessionView{Token: tok, SessionID: c.SessionID, ExpiresAt: c.Expiry, Role: c.Role, Cameras: c.Cameras, Local: true})
}

// matches checks username and password (both must be right) and says
// whether the username was; the hash runs even for a wrong username so
// timing does not tell which was wrong.
func (a *api) matches(cred store.LocalCredential, in localLoginInput) (userOK, ok bool) {
	a.hashMu.Lock()
	defer a.hashMu.Unlock()
	userOK = subtle.ConstantTimeCompare([]byte(in.Username), []byte(cred.Username)) == 1
	passOK, err := auth.VerifyPassword(cred.Hash, in.Password)
	if err != nil {
		a.d.Logger.Error("local login hash", "err", err)
	}
	return userOK, userOK && passOK
}

// localFailure logs a failed sign-in without what was typed (a password
// typed into the username field must not land in the log), only whether
// the username was right.
func (a *api) localFailure(w http.ResponseWriter, r *http.Request, try attempt, host string, userOK bool) {
	a.recordAs(r, "local", "local_login.failure", host, map[string]any{"username_correct": userOK})
	if !try.locks.IsZero() {
		a.d.Logger.Warn("local login locked", "source", try.source, "until", try.locks)
		a.recordAs(r, "local", "local_login.locked", host, map[string]any{"source": try.source, "until": try.locks.UTC()})
	}
	writeError(w, http.StatusUnauthorized, "invalid_credentials", "wrong username or password")
}

func refuseLocked(w http.ResponseWriter, until time.Time) {
	secs := int(math.Ceil(time.Until(until).Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	writeError(w, http.StatusTooManyRequests, "local_login_rate_limited", "too many failed sign-ins from this address; try again later")
}
