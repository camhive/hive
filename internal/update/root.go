package update

import (
	"bytes"
	_ "embed"
)

// shippedRoot is the TUF root.json built into this updater (ADR-0018 §2):
// the one trust anchor. Empty until the key ceremony has produced it; an
// updater without a root keeps the Hive running and never updates.
//
//go:embed root.json
var shippedRoot []byte

// ShippedRoot returns the built-in root, or nil when there is none yet.
func ShippedRoot() []byte {
	if len(bytes.TrimSpace(shippedRoot)) == 0 {
		return nil
	}
	return shippedRoot
}
