package hiveapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/update"
	"github.com/camhive/hive/internal/update/journal"
)

// UpdateControl is the owner's handle on hive-updater (#82, ADR-0018 §6):
// the update settings in hive.yaml, which the updater reads at every
// check, and the request to update now that `hive update` writes too.
type UpdateControl struct {
	Paths hiveconfig.Paths // hive.yaml
	Dir   string           // hive-updater's directory
	// Launched is CAMHIVE_UPDATER as hive-updater set it when it started
	// this hive (update.LaunchedEnv): on, dev_build or no_root; "" when it
	// did not set it.
	Launched string

	mu sync.Mutex // one change of hive.yaml at a time
}

// updateStatus is GET /v0/hive/updates.
type updateStatus struct {
	hiveconfig.UpdateSettings
	Running           string `json:"running"`
	Supported         bool   `json:"supported"`
	UnsupportedReason string `json:"unsupported_reason,omitempty"`
	// SettingsNeedRestart: the running updater reads hive.yaml only at
	// start (older than v0.1.7).
	SettingsNeedRestart bool `json:"settings_need_restart,omitempty"`
	UpdateReports
}

// UpdateReports is what hive-updater reported into the action log.
type UpdateReports struct {
	// Available is waiting: in manual mode for Update now, in automatic
	// mode for the update window.
	Available string `json:"available,omitempty"`
	// NeedsPackage needs a newer hive-updater than this one: a package
	// install, not Update now.
	NeedsPackage string      `json:"needs_package,omitempty"`
	Last         *LastUpdate `json:"last,omitempty"`
}

// LastUpdate is the updater's last update.applied or update.rolled_back.
type LastUpdate struct {
	Action string    `json:"action"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	At     time.Time `json:"at"`
}

// updaterState says whether hive-updater updates this hive and, if not,
// why (dev_build and no_root are the updater's own verdict); needRestart
// when an updater older than v0.1.7 runs it, which never sets
// CAMHIVE_UPDATER and reads hive.yaml only at start.
func (u *UpdateControl) updaterState() (unsupported string, needRestart bool) {
	switch {
	case u.Launched == "on":
		return "", false
	case u.Launched != "":
		return u.Launched, false
	}
	if _, err := os.Stat(u.Dir); err != nil {
		return "no_updater", false
	}
	if announced, _ := journal.Announced(u.Dir); announced != "" {
		return "no_updater", false // a current updater runs, but not this hive (`hive run`)
	}
	return "", true
}

// ownerUpdates answers 403 unless the caller is the owner signed in
// through Camhive, 503 without an UpdateControl; otherwise it returns the
// control. A local emergency session (#57) is refused: it exists to reach
// recordings while the cloud is down, and updates need the internet.
func (a *api) ownerUpdates(w http.ResponseWriter, r *http.Request) *UpdateControl {
	c := claimsFrom(r)
	switch {
	case c.Role != auth.RoleOwner || c.Local:
		writeError(w, http.StatusForbidden, "forbidden", "only the owner, signed in through Camhive, can manage updates")
	case a.d.Updates == nil || a.d.Store == nil:
		writeError(w, http.StatusServiceUnavailable, "not_ready", "updates are not available yet")
	default:
		return a.d.Updates
	}
	return nil
}

func (a *api) getUpdates(w http.ResponseWriter, r *http.Request) {
	if u := a.ownerUpdates(w, r); u != nil {
		a.writeUpdateStatus(w, r, u)
	}
}

func (a *api) setUpdates(w http.ResponseWriter, r *http.Request) {
	u := a.ownerUpdates(w, r)
	if u == nil {
		return
	}
	var in hiveconfig.UpdateSettings
	if !decodeJSON(w, r, &in) {
		return
	}
	u.mu.Lock()
	err := hiveconfig.SaveUpdateSettings(u.Paths, in)
	u.mu.Unlock()
	switch {
	case errors.Is(err, hiveconfig.ErrUpdateWindow):
		writeError(w, http.StatusBadRequest, "invalid_update_window", "update_window must look like 02:00-05:00, two different times")
		return
	case errors.Is(err, hiveconfig.ErrUpdateSettings):
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	case err != nil:
		a.d.Logger.Error("update settings", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "the update settings could not be stored")
		return
	}
	a.record(r, "update.settings_changed", "hive", map[string]any{"updates": in.Updates, "update_window": in.UpdateWindow, "update_channel": in.UpdateChannel})
	a.writeUpdateStatus(w, r, u)
}

func (a *api) installUpdate(w http.ResponseWriter, r *http.Request) {
	u := a.ownerUpdates(w, r)
	if u == nil {
		return
	}
	if why, _ := u.updaterState(); why != "" {
		writeError(w, http.StatusConflict, "updates_unavailable", "this hive does not update itself ("+why+")")
		return
	}
	if err := journal.RequestUpdate(u.Dir); err != nil {
		a.d.Logger.Error("update request", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "the update request could not be written")
		return
	}
	a.record(r, "update.requested", "hive", nil)
	writeJSON(w, http.StatusAccepted, map[string]any{})
}

func (a *api) writeUpdateStatus(w http.ResponseWriter, r *http.Request, u *UpdateControl) {
	cfg, _, err := hiveconfig.Load(u.Paths)
	if err != nil {
		a.d.Logger.Error("update settings", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "hive.yaml could not be read")
		return
	}
	reports, err := a.updateReports(r.Context())
	if err != nil {
		a.d.Logger.Error("action log", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "the activity log could not be read")
		return
	}
	why, restart := u.updaterState()
	st := updateStatus{UpdateSettings: cfg.UpdateSettings(), Running: a.d.Version, Supported: why == "", UnsupportedReason: why, SettingsNeedRestart: restart}
	st.UpdateReports = reports
	writeJSON(w, http.StatusOK, st)
}

// updateReports reads what hive-updater reported about this hive.
func (a *api) updateReports(ctx context.Context) (UpdateReports, error) {
	recent, err := a.d.Store.Actions().RecentOf(ctx, []string{"update.applied", "update.rolled_back", "update.available", "update.needs_package"}, 100)
	if err != nil {
		return UpdateReports{}, err
	}
	return reported(a.d.Version, recent), nil
}

// reported reads what hive-updater reported (newest first): the version
// waiting and the one that needs a package, each newer than what runs and
// not rolled back since, and the last update or rollback.
func reported(running string, recent []store.Action) UpdateReports {
	cur, _ := update.ParseVersion(running)
	var out UpdateReports
	rolledBack := map[string]bool{}
	for _, a := range recent {
		var d struct{ From, To, Offered string }
		_ = json.Unmarshal(a.Details, &d)
		switch a.Action {
		case "update.applied", "update.rolled_back":
			rolledBack[d.To] = rolledBack[d.To] || a.Action == "update.rolled_back"
			if out.Last == nil {
				out.Last = &LastUpdate{Action: a.Action, From: d.From, To: d.To, At: a.At}
			}
		case "update.available":
			out.Available = newestOffer(out.Available, d.Offered, cur, rolledBack)
		case "update.needs_package":
			out.NeedsPackage = newestOffer(out.NeedsPackage, d.Offered, cur, rolledBack)
		}
	}
	return out
}

// newestOffer keeps the offer already found, else takes offered when it is
// newer than what runs and did not roll back.
func newestOffer(found, offered string, running update.Version, rolledBack map[string]bool) string {
	if found != "" || rolledBack[offered] {
		return found
	}
	if v, err := update.ParseVersion(offered); err == nil && v.Compare(running) > 0 {
		return offered
	}
	return ""
}
