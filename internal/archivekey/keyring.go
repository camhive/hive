// Package archivekey keeps the archive key (ADR-0019): an age X25519 key
// pair whose public half seals footage as it is closed, and whose private
// half, in a keyring under the owner's passphrase, opens it again once the
// owner unlocks the archive. The private half lives in memory only.
package archivekey

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/camhive/hive/internal/auth"
)

// The keyring's files in its directory. keyring.age is a standard age file
// encrypted with the owner's passphrase (scrypt); inside is an age identity
// file, one AGE-SECRET-KEY-1 line per key, the newest (the one sealing new
// footage) last. It is also the key file the owner downloads: with the
// passphrase, the stock age tool recovers the keys. recipient.txt is the
// newest key's public half, so the hive seals footage without the owner.
const (
	keyringFile   = "keyring.age"
	recipientFile = "recipient.txt"
)

// WorkFactor is the scrypt cost of the keyring, log2 of N (r=8, p=1, age's
// fixed parameters). ADR-0019 §3 makes the keyring an age passphrase file,
// so its derivation is age's scrypt, not the argon2id of auth.HashPassword
// (a hash to compare, not a key). 2^17 takes 128 MiB, only while a
// passphrase is checked, and about 0.56 s on a laptop core (Ryzen 7 3700U;
// 2^16: 0.27 s, 2^18: 1.0 s and 256 MiB, too much beside the engine on a
// 1 GB Raspberry Pi). The key file is meant to be kept apart from the hive,
// and a guess on the hive is rate-limited. A keyring written with another
// cost still opens (age reads it from the file); the next passphrase change
// writes this one. Tests lower it.
var WorkFactor = 17

// Errors. A passphrase follows the password policy of the accounts and the
// local login (auth.CheckPassword), so ErrWeakPassphrase is its error.
var (
	ErrOff             = errors.New("archivekey: archive encryption is off")
	ErrOn              = errors.New("archivekey: archive encryption is already on")
	ErrWrongPassphrase = errors.New("archivekey: wrong passphrase")
	ErrWeakPassphrase  = auth.ErrWeakPassword
)

func checkPassphrase(p string) error { return auth.CheckPassword(p, "") }

// keyring is the keyring's directory.
type keyring struct{ dir string }

func (k keyring) path(name string) string { return filepath.Join(k.dir, name) }

// enabled: encryption was switched on, the keyring exists.
func (k keyring) enabled() bool {
	_, err := os.Stat(k.path(keyringFile))
	return err == nil
}

// recipient reads the public key new footage is sealed to.
func (k keyring) recipient() (*age.X25519Recipient, error) {
	raw, err := os.ReadFile(k.path(recipientFile))
	if err != nil {
		return nil, err
	}
	return age.ParseX25519Recipient(strings.TrimSpace(string(raw)))
}

// create writes a keyring of one new key under pass.
func (k keyring) create(pass string, now time.Time) ([]*age.X25519Identity, error) {
	if k.enabled() {
		return nil, ErrOn
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	ids := []*age.X25519Identity{id}
	if err := k.write(ids, pass, now); err != nil {
		wipe(ids)
		return nil, err
	}
	return ids, nil
}

// write seals the keys under pass and writes the newest key's public half,
// then the keyring. Each file is replaced atomically; the keyring is the
// "encryption on" marker and comes last, so a power cut in between leaves
// encryption off (a stray public key is replaced by the next enable), never
// on without a key to seal to.
func (k keyring) write(ids []*age.X25519Identity, pass string, now time.Time) error {
	if err := os.MkdirAll(k.dir, 0o700); err != nil {
		return err
	}
	to, err := age.NewScryptRecipient(pass)
	if err != nil {
		return err
	}
	to.SetWorkFactor(WorkFactor)
	var sealed bytes.Buffer
	enc, err := age.Encrypt(&sealed, to)
	if err != nil {
		return err
	}
	plain := identityFile(ids, now)
	_, err = enc.Write(plain)
	clear(plain)
	if err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := k.writeRecipient(ids); err != nil {
		return err
	}
	return replaceFile(k.path(keyringFile), sealed.Bytes())
}

// writeRecipient writes the newest key's public half.
func (k keyring) writeRecipient(ids []*age.X25519Identity) error {
	return replaceFile(k.path(recipientFile), []byte(ids[len(ids)-1].Recipient().String()+"\n"))
}

// repairRecipient writes the newest key's public half again when the file
// is missing, unreadable or names another key.
func (k keyring) repairRecipient(ids []*age.X25519Identity) error {
	want := ids[len(ids)-1].Recipient().String()
	if r, err := k.recipient(); err == nil && r.String() == want {
		return nil
	}
	return k.writeRecipient(ids)
}

// identityFile is the keys as an age identity file, newest last.
func identityFile(ids []*age.X25519Identity, now time.Time) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Camhive archive keys, newest last. Written %s.\n", now.UTC().Format(time.RFC3339))
	for _, id := range ids {
		fmt.Fprintf(&b, "# public key: %s\n%s\n", id.Recipient(), id)
	}
	return b.Bytes()
}

// open decrypts the keyring with pass.
func (k keyring) open(pass string) ([]*age.X25519Identity, error) {
	sealed, err := os.ReadFile(k.path(keyringFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrOff
	}
	if err != nil {
		return nil, err
	}
	scrypt, err := age.NewScryptIdentity(pass)
	if err != nil {
		return nil, ErrWrongPassphrase // an empty passphrase opens nothing
	}
	r, err := age.Decrypt(bytes.NewReader(sealed), scrypt)
	if err != nil {
		return nil, fmt.Errorf("%w (%v)", ErrWrongPassphrase, err) //nolint:errorlint // the cause is detail for the log only
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	return parseKeys(plain)
}

// parseKeys reads an identity file of X25519 keys.
func parseKeys(plain []byte) ([]*age.X25519Identity, error) {
	parsed, err := age.ParseIdentities(bytes.NewReader(plain))
	if err != nil {
		return nil, fmt.Errorf("archivekey: keyring: %w", err)
	}
	ids := make([]*age.X25519Identity, 0, len(parsed))
	for _, p := range parsed {
		id, ok := p.(*age.X25519Identity)
		if !ok {
			wipe(ids)
			return nil, fmt.Errorf("archivekey: keyring holds a %T", p)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// file returns the keyring as it lies on disk: the owner's backup.
func (k keyring) file() ([]byte, error) {
	raw, err := os.ReadFile(k.path(keyringFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrOff
	}
	return raw, err
}

// replaceFile writes data to a temp file beside path, syncs it and renames
// it over path.
func replaceFile(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
