package pki_test

import (
	"crypto/ed25519"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/camhive/hive/internal/pki"
)

func TestNewCAProducesSelfSignedEd25519Authority(t *testing.T) {
	ca, err := pki.NewCA("Camhive Device CA (test)", 10*365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cert := ca.Certificate()
	if !cert.IsCA || cert.PublicKeyAlgorithm != x509.Ed25519 {
		t.Fatalf("not an Ed25519 CA: isCA=%v alg=%v", cert.IsCA, cert.PublicKeyAlgorithm)
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Fatalf("CA must be self-signed: %v", err)
	}
}

func TestIssueDeviceCertificateForHive(t *testing.T) {
	ca, _ := pki.NewCA("test CA", time.Hour*24)
	pub, _, _ := ed25519.GenerateKey(nil)

	cert, err := ca.IssueDevice(pki.HiveID("hv_01"), pub, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := pki.HiveIDFromCertificate(cert); got != "hv_01" {
		t.Fatalf("HiveIDFromCertificate = %q", got)
	}
	if cert.IsCA {
		t.Error("device cert must not be a CA")
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("device cert must be client-auth only, got %v", cert.ExtKeyUsage)
	}
	if ttl := cert.NotAfter.Sub(cert.NotBefore); ttl < 29*24*time.Hour || ttl > 31*24*time.Hour {
		t.Errorf("ttl = %v", ttl)
	}
	if err := ca.Verify(cert); err != nil {
		t.Fatalf("CA must verify its own device cert: %v", err)
	}
}

func TestVerifyRejectsCertificateFromAnotherCA(t *testing.T) {
	ours, _ := pki.NewCA("ours", time.Hour)
	theirs, _ := pki.NewCA("theirs", time.Hour)
	pub, _, _ := ed25519.GenerateKey(nil)
	cert, _ := theirs.IssueDevice("hv_evil", pub, time.Hour)
	if err := ours.Verify(cert); err == nil {
		t.Fatal("certificate from another CA must be rejected")
	}
}

func TestIssueDeviceRejectsInvalidHiveID(t *testing.T) {
	ca, _ := pki.NewCA("test", time.Hour)
	pub, _, _ := ed25519.GenerateKey(nil)
	for _, bad := range []pki.HiveID{"", "has space", "slash/inside", "Upper"} {
		if _, err := ca.IssueDevice(bad, pub, time.Hour); !errors.Is(err, pki.ErrInvalidHiveID) {
			t.Errorf("IssueDevice(%q) = %v, want ErrInvalidHiveID", bad, err)
		}
	}
}

func TestPEMRoundTripForCAAndKeys(t *testing.T) {
	ca, _ := pki.NewCA("test", time.Hour)
	certPEM, keyPEM, err := ca.PEM()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := pki.LoadCA(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Certificate().Equal(ca.Certificate()) {
		t.Fatal("loaded CA certificate differs")
	}
	// A device key pair round-trips too.
	pub, priv, _ := ed25519.GenerateKey(nil)
	pem := pki.MarshalPrivateKey(priv)
	back, err := pki.ParsePrivateKey(pem)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Public().(ed25519.PublicKey).Equal(pub) {
		t.Fatal("private key round trip lost the key")
	}
}

func TestTLSMaterialBuildsMutualConfigs(t *testing.T) {
	ca, _ := pki.NewCA("test", time.Hour)
	pub, priv, _ := ed25519.GenerateKey(nil)
	cert, _ := ca.IssueDevice("hv_1", pub, time.Hour)

	srv, err := ca.ServerTLSConfig("gateway.local")
	if err != nil {
		t.Fatal(err)
	}
	if srv.ClientAuth != 4 /* tls.RequireAndVerifyClientCert */ || srv.ClientCAs == nil || srv.MinVersion < 0x0304 {
		t.Fatalf("server config must require and verify client certs over TLS 1.3: %+v", srv.ClientAuth)
	}
	cli, err := pki.ClientTLSConfig(ca.Certificate(), cert, priv, "gateway.local")
	if err != nil {
		t.Fatal(err)
	}
	if cli.RootCAs == nil || len(cli.Certificates) != 1 || cli.ServerName != "gateway.local" {
		t.Fatalf("client config incomplete: %+v", cli)
	}
}

func TestLoadCARejectsBrokenOrMismatchedMaterial(t *testing.T) {
	ca, _ := pki.NewCA("a", time.Hour)
	other, _ := pki.NewCA("b", time.Hour)
	certPEM, _, _ := ca.PEM()
	_, otherKeyPEM, _ := other.PEM()
	if _, err := pki.LoadCA(certPEM, otherKeyPEM); err == nil {
		t.Error("key from another CA must be rejected")
	}
	if _, err := pki.LoadCA([]byte("garbage"), otherKeyPEM); err == nil {
		t.Error("garbage certificate must be rejected")
	}
	if _, err := pki.LoadCA(certPEM, []byte("garbage")); err == nil {
		t.Error("garbage key must be rejected")
	}
	if _, err := pki.ParsePrivateKey([]byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")); err == nil {
		t.Error("malformed PKCS#8 must be rejected")
	}
	if _, err := pki.ClientTLSConfig(nil, nil, nil, "x"); err == nil {
		t.Error("ClientTLSConfig with nil material must fail")
	}
}
