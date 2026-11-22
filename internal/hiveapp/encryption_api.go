package hiveapp

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/auth"
)

// The archive encrypted at rest (ADR-0019): the owner switches it on with a
// passphrase, unlocks it after every start of the hive, can lock it again
// and change the passphrase. Members see the state. The passphrase reaches
// only the hive, over the TLS connection that ends on it.

// ArchiveVault is the archive key as the API uses it (archivekey.Vault).
type ArchiveVault interface {
	State() archivekey.State
	Sealing() bool
	Enable(passphrase string) error
	Unlock(passphrase string) error
	Lock()
	ChangePassphrase(current, next string) error
	KeyFile() ([]byte, error)
}

type encryptionView struct {
	State archivekey.State `json:"state"`
	// Sealing: new footage is being sealed. False while off, and while on
	// with the public key unreadable (unlocking writes it again).
	Sealing bool `json:"sealing"`
	// Autolock: the automatic lock (#147), for the owner only.
	Autolock *autolockView `json:"autolock,omitempty"`
}

// encryptionStatusFor is the vault's state as the API shows it to the
// caller; the owner also sees the automatic lock.
func (a *api) encryptionStatusFor(r *http.Request) encryptionView {
	var v encryptionView
	if a.d.Vault == nil {
		v.State = archivekey.Off
	} else {
		v.State, v.Sealing = a.d.Vault.State(), a.d.Vault.Sealing()
	}
	if isOwner(r) {
		v.Autolock = a.autolockOf()
	}
	return v
}

type passphraseInput struct {
	Passphrase    string `json:"passphrase"`
	NewPassphrase string `json:"new_passphrase"`
}

// vaultState is the archive key's state; off without a vault.
func (a *api) vaultState() archivekey.State {
	if a.d.Vault == nil {
		return archivekey.Off
	}
	return a.d.Vault.State()
}

// archiveLocked answers 423 while the archive is locked: playback, sound and
// exports wait for the owner (ADR-0019 §3).
func (a *api) archiveLocked(w http.ResponseWriter) bool {
	if a.vaultState() != archivekey.Locked {
		return false
	}
	writeLocked(w)
	return true
}

func writeLocked(w http.ResponseWriter) {
	writeError(w, http.StatusLocked, "archive_locked", "the archive is locked until its owner unlocks it")
}

func (a *api) encryptionStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.encryptionStatusFor(r))
}

// requireVaultOwner: unlocking and locking take the owner, signed in
// through Camhive or with a local emergency session (#57): reaching the
// recordings while the cloud is down is what that session is for.
func (a *api) requireVaultOwner(w http.ResponseWriter, r *http.Request) bool {
	return a.requireVault(w, r, true)
}

// requireCloudVaultOwner: switching encryption on, changing the passphrase
// and taking the key file take the owner signed in through Camhive; a local
// session is refused, as for update settings (#107).
func (a *api) requireCloudVaultOwner(w http.ResponseWriter, r *http.Request) bool {
	return a.requireVault(w, r, false)
}

func (a *api) requireVault(w http.ResponseWriter, r *http.Request, localOK bool) bool {
	if a.d.Vault == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "archive encryption is not available")
		return false
	}
	if c := claimsFrom(r); !c.Allows("", auth.ActionManage) || (c.Local && !localOK) {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner, signed in through Camhive, can change the archive key")
		return false
	}
	return true
}

func (a *api) enableEncryption(w http.ResponseWriter, r *http.Request) {
	var in passphraseInput
	if !a.requireCloudVaultOwner(w, r) || !decodeJSON(w, r, &in) {
		return
	}
	a.vaultAnswer(w, r, "enable", a.unlocked(a.d.Vault.Enable(in.Passphrase)), http.StatusOK)
}

func (a *api) unlockArchive(w http.ResponseWriter, r *http.Request) {
	var in passphraseInput
	if !a.requireVaultOwner(w, r) || !decodeJSON(w, r, &in) {
		return
	}
	a.vaultAnswer(w, r, "unlock", a.unlocked(a.d.Vault.Unlock(in.Passphrase)), http.StatusOK)
}

// unlocked starts the automatic lock's timers after a successful unlock
// (switching encryption on leaves the archive unlocked too); it passes err on.
func (a *api) unlocked(err error) error {
	if err == nil && a.d.Autolock != nil {
		a.d.Autolock.Unlocked()
	}
	return err
}

func (a *api) lockArchive(w http.ResponseWriter, r *http.Request) {
	if !a.requireVaultOwner(w, r) {
		return
	}
	var err error
	if a.d.Vault.State() == archivekey.Off {
		err = archivekey.ErrOff
	} else {
		a.d.Vault.Lock()
	}
	a.vaultAnswer(w, r, "lock", err, http.StatusOK)
}

func (a *api) changePassphrase(w http.ResponseWriter, r *http.Request) {
	var in passphraseInput
	if !a.requireCloudVaultOwner(w, r) || !decodeJSON(w, r, &in) {
		return
	}
	a.vaultAnswer(w, r, "passphrase_change", a.d.Vault.ChangePassphrase(in.Passphrase, in.NewPassphrase), http.StatusNoContent)
}

// vaultAnswer audits a change and answers it: the new state, or why not.
// A wrong passphrase is audited too (unlock_failed); the passphrase never is.
func (a *api) vaultAnswer(w http.ResponseWriter, r *http.Request, change string, err error, status int) {
	if errors.Is(err, archivekey.ErrWrongPassphrase) {
		a.audit(r, "archive.encryption."+change+"_failed")
	}
	if err != nil {
		a.vaultError(w, err)
		return
	}
	a.audit(r, "archive.encryption."+change)
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, a.encryptionStatusFor(r))
}

// audit records an archive key change in the action log and sends it to
// the cloud (ADR-0019 §3).
func (a *api) audit(r *http.Request, action string) {
	a.record(r, action, "", nil)
	if a.d.Emit != nil {
		a.d.Emit(action, map[string]any{"actor": claimsFrom(r).Account})
	}
}

func (a *api) vaultError(w http.ResponseWriter, err error) {
	var limited *archivekey.RateLimitedError
	switch {
	case errors.As(err, &limited):
		secs := int(math.Ceil(time.Until(limited.Until).Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		writeError(w, http.StatusTooManyRequests, "unlock_rate_limited", "too many wrong passphrases; try again later")
	case errors.Is(err, archivekey.ErrWrongPassphrase):
		writeError(w, http.StatusForbidden, "wrong_passphrase", "wrong passphrase")
	case errors.Is(err, archivekey.ErrWeakPassphrase):
		writeError(w, http.StatusUnprocessableEntity, "weak_password", "the passphrase must be 10-256 characters")
	case errors.Is(err, archivekey.ErrOn):
		writeError(w, http.StatusConflict, "encryption_on", "archive encryption is already on")
	case errors.Is(err, archivekey.ErrOff):
		writeError(w, http.StatusConflict, "encryption_off", "archive encryption is off")
	default:
		a.internal(w, err)
	}
}

// keyFile answers the keyring as stored: the owner's backup, an age file
// under the passphrase (ADR-0019 §4).
func (a *api) keyFile(w http.ResponseWriter, r *http.Request) {
	if !a.requireCloudVaultOwner(w, r) {
		return
	}
	raw, err := a.d.Vault.KeyFile()
	if err != nil {
		a.vaultError(w, err)
		return
	}
	a.record(r, "archive.encryption.key_file", "", nil)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="camhive-archive-key-`+claimsFrom(r).HiveID+`.age"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

// lockedError answers 423 when reading footage met the locked archive (it
// was locked while the request was on its way).
func lockedError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, archive.ErrLocked) {
		writeLocked(w)
		return true
	}
	return false
}
