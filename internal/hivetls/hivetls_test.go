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

// ADR-0023: a browser that opens the hive at its LAN address sends no SNI
// (an IP literal is never a server name); the listener must still present
// the hive's certificate for its LAN name, the one the user checks in the
// warning, and the current one after the cloud handed over a new one.
func TestListenerPresentsTheHiveCertificateToAnIPLiteralWithoutSNI(t *testing.T) {
	dir := t.TempDir()
	first, err := hivetls.LoadOrCreate(filepath.Join(dir, "a.crt"), filepath.Join(dir, "a.key"), testHive)
	if err != nil {
		t.Fatal(err)
	}
	holder := hivetls.NewHolder(first)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", holder.ServerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sni := make(chan string, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			tc := c.(*tls.Conn)
			_ = tc.Handshake()
			sni <- tc.ConnectionState().ServerName
			_ = tc.Close()
		}
	}()
	presented := func() *tls.Conn {
		t.Helper()
		// No ServerName: crypto/tls, like a browser, sends no SNI for an IP literal.
		c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // the test reads the presented certificate itself
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := presented()
	leaf := c.ConnectionState().PeerCertificates[0]
	_ = c.Close()
	if got := <-sni; got != "" {
		t.Fatalf("client sent SNI %q for an IP literal", got)
	}
	lanName := hivetls.Names(testHive)[0]
	if hivetls.Fingerprint(tls.Certificate{Certificate: [][]byte{leaf.Raw}}) != hivetls.Fingerprint(first) || leaf.VerifyHostname(lanName) != nil || leaf.VerifyHostname("127.0.0.1") == nil {
		t.Fatalf("presented %v, want the hive's certificate for %s only", leaf.DNSNames, lanName)
	}

	second, err := hivetls.LoadOrCreate(filepath.Join(dir, "b.crt"), filepath.Join(dir, "b.key"), testHive)
	if err != nil {
		t.Fatal(err)
	}
	holder.Set(second)
	c = presented()
	leaf = c.ConnectionState().PeerCertificates[0]
	_ = c.Close()
	<-sni
	if hivetls.Fingerprint(tls.Certificate{Certificate: [][]byte{leaf.Raw}}) != hivetls.Fingerprint(second) {
		t.Fatal("after Set the listener must present the new certificate at the address too")
	}
}
