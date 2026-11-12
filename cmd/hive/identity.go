package main

import (
	"crypto/ed25519"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/hivelink"
	"github.com/camhive/hive/internal/hivetls"
	"github.com/camhive/hive/internal/pki"
)

// effectiveHiveID is the identity the hive runs under. Before a claim it is
// derived from the key (LocalHiveID); after a claim it is the id the cloud
// put into the device certificate, because licences, access tokens and the
// LAN name (<id>.h.camhive.app) are all issued for that id.
func effectiveHiveID(paths hiveconfig.Paths, pub ed25519.PublicKey) pki.HiveID {
	id, _ := effectiveIdentity(paths, pub)
	return id
}

// effectiveIdentity also returns the deployment's domains (hivetls.Domains)
// the hive's public certificate must be issued for.
func effectiveIdentity(paths hiveconfig.Paths, pub ed25519.PublicKey) (pki.HiveID, hivetls.Domains) {
	if st, err := hivelink.Load(paths); err == nil && st.Link.HiveID != "" {
		return pki.HiveID(st.Link.HiveID), st.Link.Domains()
	}
	return hiveconfig.LocalHiveID(pub), hivetls.DefaultDomains
}
