package archivekey

import (
	"reflect"
	"unsafe"

	"filippo.io/age"
)

// Keeping the private keys in memory only (ADR-0019 §7). age keeps an
// X25519 key's scalar in an unexported slice of its identity; secretOf
// reaches it so the pages holding it can be locked against swapping and the
// bytes wiped on lock. TestWipedKeysOpenNothing fails if age changes the
// layout, rather than leaving keys unwiped unnoticed.
//
// What Go cannot promise: the runtime may have copied key bytes while the
// keyring was parsed or written (bech32 strings, buffers), and those copies
// are freed but not zeroed. Core dumps are off (DisableCoreDumps), so they
// leave the process only through swap or hibernation, which the user
// documentation names.

// secretOf returns the scalar inside id, or nil if age's layout changed.
func secretOf(id *age.X25519Identity) []byte {
	f := reflect.ValueOf(id).Elem().FieldByName("secretKey")
	if !f.IsValid() || f.Type() != reflect.TypeFor[[]byte]() {
		return nil
	}
	return *(*[]byte)(unsafe.Pointer(f.UnsafeAddr())) //nolint:gosec // read age's own field in place to lock and wipe it
}

// protect locks the keys' scalars in RAM where the platform allows it.
func protect(ids []*age.X25519Identity) {
	for _, id := range ids {
		if b := secretOf(id); len(b) > 0 {
			lockMemory(b)
		}
	}
}

// wipe zeroes the keys' scalars and unlocks their pages.
func wipe(ids []*age.X25519Identity) {
	for _, id := range ids {
		if b := secretOf(id); len(b) > 0 {
			clear(b)
			unlockMemory(b)
		}
	}
}
