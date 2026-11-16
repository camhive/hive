package archivekey

import "filippo.io/age"

func init() { WorkFactor = 10 } // tests: fast scrypt

// SecretOf exposes secretOf to the wipe test.
var SecretOf = secretOf

// Wipe exposes wipe.
var Wipe = wipe

// Held returns the keys the vault holds now.
func Held(v *Vault) []*age.X25519Identity {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.keys
}
