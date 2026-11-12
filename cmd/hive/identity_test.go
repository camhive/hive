package main

import (
	"crypto/ed25519"
	"encoding/pem"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/hivelink"
	"github.com/camhive/hive/internal/pki"
)

// The cloud assigns the hive id at claim time; the running hive must adopt
// it, or licences and access tokens issued for that id are refused.
func TestEffectiveHiveIDFollowsTheClaim(t *testing.T) {
	paths := hiveconfig.DefaultPaths().WithDataDir(t.TempDir())
	pub, priv, _ := ed25519.GenerateKey(nil)
	_ = priv
	local := hiveconfig.LocalHiveID(pub)
	if got := effectiveHiveID(paths, pub); got != local {
		t.Fatalf("unclaimed: %s, want %s", got, local)
	}
	ca, _ := pki.NewCA("test", time.Hour)
	cert, err := ca.IssueDevice("hv_cloud00000000", pub, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Certificate().Raw})
	if err := hivelink.Save(paths, hivelink.Link{HiveID: "hv_cloud00000000", APIURL: "https://api.test", GatewayURL: "wss://gw.dev.test:8443", ClaimedAt: time.Now()}, certPEM, caPEM); err != nil {
		t.Fatal(err)
	}
	if got := effectiveHiveID(paths, pub); got != "hv_cloud00000000" {
		t.Fatalf("claimed: %s", got)
	}
	// Domains: derived from the gateway host when the cloud did not send them.
	if _, d := effectiveIdentity(paths, pub); d.LAN != "h.dev.test" || d.Relay != "relay.dev.test" {
		t.Fatalf("derived domains: %+v", d)
	}
}
