package archivekey_test

import (
	"bytes"
	"testing"

	"filippo.io/age"

	"github.com/camhive/hive/internal/archivekey"
)

// The scalar the vault locks and wipes is the one age decrypts with: a
// wiped key opens nothing. Fails if age changes how it keeps the key.
func TestWipedKeysOpenNothing(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	secret := archivekey.SecretOf(id)
	if len(secret) != 32 {
		t.Fatalf("found a %d-byte secret in age's identity", len(secret))
	}
	var sealed bytes.Buffer
	enc, err := age.Encrypt(&sealed, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = enc.Write([]byte("footage"))
	_ = enc.Close()

	archivekey.Wipe([]*age.X25519Identity{id})

	if !bytes.Equal(secret, make([]byte, 32)) {
		t.Fatal("the secret is not zeroed")
	}
	if _, err := age.Decrypt(bytes.NewReader(sealed.Bytes()), id); err == nil {
		t.Fatal("a wiped key still decrypts")
	}
}

// Keys the vault replaces (a second unlock, a passphrase change) are wiped,
// not left in memory beside the new ones.
func TestReplacedKeysAreWiped(t *testing.T) {
	v := archivekey.New(t.TempDir())
	if err := v.Enable(pass); err != nil {
		t.Fatal(err)
	}
	first := archivekey.Held(v)[0]
	secret := archivekey.SecretOf(first)
	if err := v.Unlock(pass); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secret, make([]byte, 32)) {
		t.Fatal("the keys held before the second unlock are still in memory")
	}
}
