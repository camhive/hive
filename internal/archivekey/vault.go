package archivekey

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/auth"
)

// State is what the archive key allows.
type State string

// States (glossary: locked archive).
const (
	Off      State = "off"      // encryption was never switched on: footage is plain
	Locked   State = "locked"   // footage is sealed as it closes, nothing sealed can be read
	Unlocked State = "unlocked" // the owner unlocked it: sealed footage reads
)

// SegmentDuration is the longest segment of an encrypted stream: the one
// being written stays in clear until it closes, so at most this much of
// each stream waits in clear (ADR-0019 §2).
const SegmentDuration = 2 * time.Minute

// Vault holds the archive key for one hive: the keyring on disk and, while
// unlocked, the private keys in memory. It is the one place that decides
// whether a file opens (Open).
type Vault struct {
	ring  keyring
	now   func() time.Time
	log   *slog.Logger
	guard guard
	kdf   sync.Mutex // one passphrase check at a time: each takes 64 MiB

	mu   sync.RWMutex
	keys []*age.X25519Identity // nil while locked
	to   age.Recipient         // nil while off
}

// Option customises a Vault.
type Option func(*Vault)

// WithClock sets the clock the guessing guard uses.
func WithClock(now func() time.Time) Option { return func(v *Vault) { v.now = now } }

// WithLogger sets the logger; the default discards.
func WithLogger(l *slog.Logger) Option { return func(v *Vault) { v.log = l } }

// New opens the vault over the keyring directory dir. It starts locked
// when encryption is on (the private keys are never on disk in clear) and
// off when it was never switched on.
func New(dir string, opts ...Option) *Vault {
	v := &Vault{ring: keyring{dir: dir}, now: time.Now, log: slog.New(slog.DiscardHandler)}
	for _, o := range opts {
		o(v)
	}
	if v.ring.enabled() {
		to, err := v.ring.recipient()
		if err != nil {
			v.log.Error("archive encryption is on but its public key is unreadable: new footage stays in clear until the owner unlocks the archive", "err", err)
			return v
		}
		v.to = to
	}
	return v
}

// Sealing says whether new footage is being sealed: encryption is on and
// its public key is known. A keyring without a readable public key (a
// damaged disk, a file removed by hand) is on but not sealing until the
// owner unlocks the archive, which writes the public key again.
func (v *Vault) Sealing() bool { return v.Recipient() != nil }

// State says whether encryption is on and the archive unlocked.
func (v *Vault) State() State {
	v.mu.RLock()
	defer v.mu.RUnlock()
	switch {
	case v.keys != nil:
		return Unlocked
	case v.ring.enabled():
		return Locked
	default:
		return Off
	}
}

// Recipient is the public key closed footage is sealed to; nil while
// encryption is off.
func (v *Vault) Recipient() age.Recipient {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.to
}

// Enable switches encryption on with a new key under pass and leaves the
// archive unlocked. ErrOn when it is on already.
func (v *Vault) Enable(pass string) error {
	if err := checkPassphrase(pass); err != nil {
		return err
	}
	v.kdf.Lock()
	defer v.kdf.Unlock()
	keys, err := v.ring.create(pass, v.now())
	if err != nil {
		return err
	}
	v.hold(keys)
	return nil
}

// Unlock opens the keyring with pass and keeps its keys in memory.
// ErrWrongPassphrase, or a RateLimitedError after too many wrong ones.
func (v *Vault) Unlock(pass string) error {
	v.kdf.Lock()
	keys, err := v.check(pass)
	v.kdf.Unlock()
	if err != nil {
		return err
	}
	if err := v.ring.repairRecipient(keys); err != nil {
		v.log.Error("writing the archive's public key", "err", err)
	}
	v.hold(keys)
	return nil
}

// ChangePassphrase seals the keyring under next; the keys, and so the
// footage, stay the same, and so does the lock state. One change at a time:
// a second change with the old passphrase finds it changed.
func (v *Vault) ChangePassphrase(current, next string) error {
	if !v.ring.enabled() {
		return ErrOff
	}
	if err := checkPassphrase(next); err != nil {
		return err
	}
	v.kdf.Lock()
	defer v.kdf.Unlock()
	keys, err := v.check(current)
	if err != nil {
		return err
	}
	if err := v.ring.write(keys, next, v.now()); err != nil {
		wipe(keys)
		return err
	}
	if v.State() == Unlocked {
		v.hold(keys)
	} else {
		wipe(keys)
	}
	return nil
}

// check opens the keyring with pass under the guessing guard; the caller
// holds kdf.
func (v *Vault) check(pass string) ([]*age.X25519Identity, error) {
	if !v.ring.enabled() {
		return nil, ErrOff
	}
	if until := v.guard.blockedUntil(v.now()); !until.IsZero() {
		return nil, &RateLimitedError{Until: until}
	}
	keys, err := v.ring.open(pass)
	if errors.Is(err, ErrWrongPassphrase) {
		v.guard.failed(v.now())
		return nil, ErrWrongPassphrase
	}
	if err != nil {
		return nil, err
	}
	v.guard.reset()
	return keys, nil
}

// hold makes keys the unlocked keys, wiping any held before, and takes the
// newest key's public half as the recipient.
func (v *Vault) hold(keys []*age.X25519Identity) {
	protect(keys)
	v.mu.Lock()
	defer v.mu.Unlock()
	wipe(v.keys) // nothing while locked
	v.keys, v.to = keys, keys[len(keys)-1].Recipient()
}

// Lock forgets the private keys: they are wiped from memory, and sealed
// footage reads again only after Unlock. Files already open keep reading.
func (v *Vault) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	wipe(v.keys)
	v.keys = nil
}

// KeyFile is the keyring as stored: the owner's backup, useless without
// the passphrase.
func (v *Vault) KeyFile() ([]byte, error) { return v.ring.file() }

// Open opens a segment or export file: a plain one as it is, a sealed one
// (archive.SealedExt) with the unlocked keys, or archive.ErrLocked. A plain
// path whose file was just sealed opens the sealed file.
func (v *Vault) Open(path string) (archive.SegmentFile, error) {
	if !archive.IsSealed(path) {
		f, err := archive.OpenFile(path)
		if !errors.Is(err, os.ErrNotExist) {
			return f, err
		}
		path += archive.SealedExt
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.keys == nil {
		return nil, archive.ErrLocked
	}
	ids := make([]age.Identity, len(v.keys))
	for i, k := range v.keys {
		ids[i] = k
	}
	return archive.OpenSealed(path, ids...)
}

// OpenSegment is Open as an archive.SegmentOpener.
func (v *Vault) OpenSegment(seg archive.Segment) (archive.SegmentFile, error) {
	return v.Open(seg.Path)
}

// RateLimitedError refuses a passphrase check after too many wrong ones.
type RateLimitedError struct{ Until time.Time }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("archivekey: too many wrong passphrases; try again after %s", e.Until.UTC().Format(time.RFC3339))
}

// PassphraseLockout slows guessing on the hive: five wrong passphrases in
// 15 minutes refuse checks for 15 minutes after the last one, as for the
// local login (#57). Each check also costs 64 MiB of scrypt, one at a time.
var PassphraseLockout = auth.Lockout{MaxFailures: 5, Window: 15 * time.Minute, LockFor: 15 * time.Minute}

// MaxFailures is how many wrong passphrases the lockout allows.
var MaxFailures = PassphraseLockout.MaxFailures

// guard keeps the recent passphrase checks; the vault's kdf lock guards it.
type guard struct{ attempts []auth.Attempt }

func (g *guard) blockedUntil(now time.Time) time.Time {
	kept := g.attempts[:0]
	for _, a := range g.attempts {
		if now.Sub(a.At) <= PassphraseLockout.Window {
			kept = append(kept, a)
		}
	}
	g.attempts = kept
	if until := PassphraseLockout.LockedUntil(kept, now); until.After(now) {
		return until
	}
	return time.Time{}
}

func (g *guard) failed(now time.Time) { g.attempts = append(g.attempts, auth.Attempt{At: now}) }
func (g *guard) reset()               { g.attempts = nil }
