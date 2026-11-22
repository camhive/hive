package hiveapp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/store"
)

// Automatic locking of the encrypted archive (ADR-0019 addendum, #147): the
// owner's two settings, the app's presence report, and the loop that locks
// when a timer is due. The cloud never takes part.

func choiceName(cs []archivekey.Choice, d time.Duration) string {
	for _, c := range cs {
		if c.D == d {
			return c.Name
		}
	}
	return "off" // never stored: openAutolock and setAutolock take only the choices
}

// parseChoice reads an optional setting; nil keeps the current value.
func parseChoice(cs []archivekey.Choice, name *string, current time.Duration) (time.Duration, bool) {
	if name == nil {
		return current, true
	}
	for _, c := range cs {
		if c.Name == *name {
			return c.D, true
		}
	}
	return 0, false
}

type autolockView struct {
	After   string            `json:"after"`
	Away    string            `json:"away"`
	LocksAt *time.Time        `json:"locks_at,omitempty"`
	Reason  archivekey.Reason `json:"reason,omitempty"`
}

// autolockOf is the owner's view; nil without automatic locking.
func (a *api) autolockOf() *autolockView {
	if a.d.Autolock == nil {
		return nil
	}
	s := a.d.Autolock.Settings()
	v := &autolockView{After: choiceName(archivekey.AfterChoices, s.After), Away: choiceName(archivekey.AwayChoices, s.Away)}
	if at, why := a.d.Autolock.LocksAt(); !at.IsZero() {
		at = at.UTC()
		v.LocksAt, v.Reason = &at, why
	}
	return v
}

// requireAutolock: the owner (requireVault) on a hive that locks itself.
func (a *api) requireAutolock(w http.ResponseWriter, r *http.Request, localOK bool) bool {
	if a.d.Autolock == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "automatic locking is not available")
		return false
	}
	return a.requireVault(w, r, localOK)
}

type autolockInput struct {
	After *string `json:"after"`
	Away  *string `json:"away"`
}

// setAutolock changes the settings: the owner signed in through Camhive.
// A change that makes the archive lock later (a timer off or longer) takes
// the owner at home, on the LAN, the same rule as presence: a stolen token
// cannot relax the lock from outside.
func (a *api) setAutolock(w http.ResponseWriter, r *http.Request) {
	var in autolockInput
	if !a.requireAutolock(w, r, false) || !a.requireStore(w) || !decodeJSON(w, r, &in) {
		return
	}
	if in.After == nil && in.Away == nil {
		writeError(w, http.StatusBadRequest, "invalid", "nothing to change: give after, away or both")
		return
	}
	a.autolockMu.Lock() // one change at a time: the check below reads what it replaces
	defer a.autolockMu.Unlock()
	cur := a.d.Autolock.Settings()
	after, ok1 := parseChoice(archivekey.AfterChoices, in.After, cur.After)
	away, ok2 := parseChoice(archivekey.AwayChoices, in.Away, cur.Away)
	if !ok1 || !ok2 {
		writeError(w, http.StatusBadRequest, "invalid", "after is off, 1h, 8h, 24h or 7d; away is off, 10m or 1h")
		return
	}
	next := archivekey.AutolockSettings{After: after, Away: away}
	if cur.Weakens(next) && !onLAN(r) {
		writeError(w, http.StatusForbidden, "lan_only", "switching a lock off or making it later works only at home, on the hive's LAN address")
		return
	}
	if err := a.d.Store.Autolock().Set(r.Context(), store.AutolockSettings(next)); err != nil {
		a.internal(w, err)
		return
	}
	a.d.Autolock.Set(next)
	a.record(r, "archive.encryption.autolock", "", map[string]any{"after": choiceName(archivekey.AfterChoices, after), "away": choiceName(archivekey.AwayChoices, away)})
	writeJSON(w, http.StatusOK, a.encryptionStatusFor(r))
}

type presenceInput struct {
	AtHome bool `json:"at_home"`
}

// presence takes the owner's app's report (cloud or local session). At home
// counts only on the LAN listener from a machine in a local network (onLAN,
// as for the local login): through the relay, a tunnel or a forwarded port
// the owner is away.
// Not audited: it is frequent and says where the owner is.
func (a *api) presence(w http.ResponseWriter, r *http.Request) {
	var in presenceInput
	if !a.requireAutolock(w, r, true) || !decodeJSON(w, r, &in) {
		return
	}
	a.d.Autolock.Presence(in.AtHome && onLAN(r))
	w.WriteHeader(http.StatusNoContent)
}

// readingFootage records a playback, sound or export request for the
// inactivity timer.
func (a *api) readingFootage() {
	if a.d.Autolock != nil {
		a.d.Autolock.Touch()
	}
}

// isOwner: the caller manages the hive.
func isOwner(r *http.Request) bool { return claimsFrom(r).Allows("", auth.ActionManage) }

// LockWhenDue locks the archive when one of its automatic-lock timers is
// due, audits the lock with its reason as the hive's, and sends it to the
// cloud like the owner's lock. It reports whether it locked.
func LockWhenDue(ctx context.Context, d Deps) bool {
	if d.Autolock == nil {
		return false
	}
	why, ok := d.Autolock.Check()
	if !ok {
		return false
	}
	if d.Store != nil {
		raw, _ := json.Marshal(map[string]any{"reason": why})
		// A lock that happened is audited, even when the hive stops meanwhile.
		if err := d.Store.Actions().Append(context.WithoutCancel(ctx), store.Action{Actor: "hive", Action: "archive.encryption.lock", Details: raw}); err != nil && d.Logger != nil {
			d.Logger.Error("action log", "err", err)
		}
	}
	if d.Emit != nil {
		d.Emit("archive.encryption.lock", map[string]any{"actor": "hive", "reason": why})
	}
	if d.Logger != nil {
		d.Logger.Info("the archive locked itself", "reason", why)
	}
	return true
}

// RunAutolock runs LockWhenDue every `every` until ctx ends.
func RunAutolock(ctx context.Context, d Deps, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			LockWhenDue(ctx, d)
		}
	}
}
