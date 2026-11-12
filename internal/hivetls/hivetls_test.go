package hivetls_test

import (
	"crypto/tls"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/hivetls"
)

// A hive id, not a secret (the literal next to "keyPath" trips scanners).
const testHive = "hv_0123456789abcdef"

func TestSelfSignedCertificateIsCreatedOnceAndPinnable(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "public.crt"), filepath.Join(dir, "public.key")
	c1, err := hivetls.LoadOrCreate(certPath, keyPath, testHive)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := hivetls.LoadOrCreate(certPath, keyPath, testHive)
	if err != nil || hivetls.Fingerprint(c1) != hivetls.Fingerprint(c2) {
		t.Fatal("second load must return the stored certificate")
	}
	names := hivetls.Names(testHive)
	if len(c1.Leaf.DNSNames) != 2 || c1.Leaf.DNSNames[1] != names[1] || names[0] != "hv-0123456789abcdef.h.camhive.app" {
		t.Fatalf("names = %v", c1.Leaf.DNSNames)
	}
	// A pinning client accepts the right fingerprint and name, refuses others.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", hivetls.ServerConfig(c1))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = c.Write([]byte("hi")); _ = c.Close() }()
		}
	}()
	dial := func(cfg *tls.Config) error {
		raw, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return err
		}
		defer raw.Close()
		c := tls.Client(raw, cfg)
		return c.Handshake()
	}
	if err := dial(hivetls.PinningClientConfig(hivetls.Fingerprint(c1), names[1])); err != nil {
		t.Fatalf("pinned dial: %v", err)
	}
	if err := dial(hivetls.PinningClientConfig("00", names[1])); err == nil {
		t.Fatal("wrong fingerprint must fail")
	}
	if err := dial(hivetls.PinningClientConfig(hivetls.Fingerprint(c1), "other.example")); err == nil {
		t.Fatal("wrong name must fail")
	}
}

func TestDomainsNameTheHiveAndReissueOnChange(t *testing.T) {
	dev := hivetls.Domains{LAN: "h.dev.camhive.app", Relay: "relay.dev.camhive.app"}
	if got := dev.Names("hv_1"); got[0] != "hv-1.h.dev.camhive.app" || got[1] != "hv-1.relay.dev.camhive.app" {
		t.Fatalf("names = %v", got)
	}
	if got := (hivetls.Domains{}).Names("hv_1"); got[0] != "hv-1.h.camhive.app" {
		t.Fatalf("zero domains must mean production: %v", got)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "p.crt"), filepath.Join(dir, "p.key")
	prod, err := hivetls.LoadOrCreate(certPath, keyPath, "hv_1")
	if err != nil {
		t.Fatal(err)
	}
	// Moving the hive to another deployment replaces a certificate for the wrong names.
	again, err := dev.LoadOrCreate(certPath, keyPath, "hv_1")
	if err != nil {
		t.Fatal(err)
	}
	if hivetls.Fingerprint(again) == hivetls.Fingerprint(prod) || again.Leaf.DNSNames[0] != "hv-1.h.dev.camhive.app" {
		t.Fatalf("certificate must be reissued for the new names: %v", again.Leaf.DNSNames)
	}
	if third, _ := dev.LoadOrCreate(certPath, keyPath, "hv_1"); hivetls.Fingerprint(third) != hivetls.Fingerprint(again) {
		t.Fatal("matching names must keep the certificate")
	}
	csr, err := dev.CSR(again, "hv_1")
	if err != nil || !strings.Contains(string(csr), "CERTIFICATE REQUEST") {
		t.Fatalf("csr: %v", err)
	}
}

func TestLabelRoundTrip(t *testing.T) {
	if hivetls.Label("hv_0123456789abcdef") != "hv-0123456789abcdef" || hivetls.IDFromLabel("hv-0123456789abcdef") != "hv_0123456789abcdef" {
		t.Fatal("label mapping must swap the underscore for a hyphen and back")
	}
	if hivetls.IDFromLabel("other") != "other" {
		t.Fatal("foreign labels pass through")
	}
}
