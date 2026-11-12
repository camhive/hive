// Package pki issues and verifies the certificates that identify Hives to the
// cloud (ADR-0004). A Hive generates an Ed25519 key locally; the cloud's Device
// CA signs a short-lived client certificate for it; the gateway accepts only
// certificates chaining to that CA. The hive id is the certificate's CN and is
// repeated as a camhive:// URI SAN.
package pki

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"time"
)

// HiveID identifies a Hive. Lower-case letters, digits, '_' and '-'.
type HiveID string

var hiveIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ErrInvalidHiveID is returned for ids that do not match the allowed form.
var ErrInvalidHiveID = errors.New("pki: invalid hive id")

// Valid reports whether the id has the allowed form.
func (id HiveID) Valid() bool { return hiveIDRE.MatchString(string(id)) }

// URI is the SAN that carries the id in a device certificate.
func (id HiveID) URI() *url.URL {
	return &url.URL{Scheme: "camhive", Host: "hive", Path: "/" + string(id)}
}

// CA is a certificate authority that issues device certificates.
type CA struct {
	cert *x509.Certificate
	key  ed25519.PrivateKey
	pool *x509.CertPool
}

// NewCA creates a self-signed Ed25519 authority valid for ttl.
func NewCA(commonName string, ttl time.Duration) (*CA, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          newSerial(),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"Camhive"}},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(ttl),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return newCA(cert, priv), nil
}

func newCA(cert *x509.Certificate, key ed25519.PrivateKey) *CA {
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &CA{cert: cert, key: key, pool: pool}
}

// LoadCA restores an authority from PEM-encoded certificate and key.
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("pki: CA certificate PEM missing")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: CA certificate: %w", err)
	}
	key, err := ParsePrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("pki: CA key: %w", err)
	}
	if !key.Public().(ed25519.PublicKey).Equal(cert.PublicKey) {
		return nil, errors.New("pki: CA key does not match certificate")
	}
	return newCA(cert, key), nil
}

// Certificate returns the CA certificate.
func (ca *CA) Certificate() *x509.Certificate { return ca.cert }

// Pool returns a pool containing only this CA, for verification.
func (ca *CA) Pool() *x509.CertPool { return ca.pool.Clone() }

// PEM returns the CA certificate and private key, PEM-encoded.
func (ca *CA) PEM() (certPEM, keyPEM []byte, err error) {
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
	return certPEM, MarshalPrivateKey(ca.key), nil
}

// Backdate is how far NotBefore is set into the past on issued
// certificates, so that hives with a slow clock accept them at once.
const Backdate = 5 * time.Minute

// IssueDevice signs a client-auth certificate for a Hive's public key.
func (ca *CA) IssueDevice(id HiveID, pub ed25519.PublicKey, ttl time.Duration) (*x509.Certificate, error) {
	if !id.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidHiveID, id)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: newSerial(),
		Subject:      pkix.Name{CommonName: string(id), Organization: []string{"Camhive Hive"}},
		URIs:         []*url.URL{id.URI()},
		NotBefore:    now.Add(-Backdate),
		NotAfter:     now.Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// Sign issues a certificate from a template for a public key (server
// certificates for hives, ADR-0012 fallback issuer).
func (ca *CA) Sign(tmpl *x509.Certificate, pub any) ([]byte, error) {
	return x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
}

// Verify checks that cert is a currently valid client certificate issued by
// this CA.
func (ca *CA) Verify(cert *x509.Certificate) error {
	_, err := cert.Verify(x509.VerifyOptions{Roots: ca.pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return err
}

// HiveIDFromCertificate extracts the id from a device certificate (CN).
func HiveIDFromCertificate(cert *x509.Certificate) HiveID {
	return HiveID(cert.Subject.CommonName)
}

// ServerTLSConfig builds the gateway's TLS config: TLS 1.3, a server
// certificate for serverName issued by this CA, and mandatory client
// certificates verified against this CA.
func (ca *CA) ServerTLSConfig(serverName string) (*tls.Config, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: newSerial(),
		Subject:      pkix.Name{CommonName: serverName},
		DNSNames:     []string{serverName},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: priv}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
	}, nil
}

// ClientTLSConfig builds a Hive's TLS config: pin the cloud CA, present the
// device certificate, expect serverName.
func ClientTLSConfig(caCert, deviceCert *x509.Certificate, key crypto.PrivateKey, serverName string) (*tls.Config, error) {
	if caCert == nil || deviceCert == nil || key == nil {
		return nil, errors.New("pki: CA certificate, device certificate and key are required")
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      pool,
		ServerName:   serverName,
		Certificates: []tls.Certificate{{Certificate: [][]byte{deviceCert.Raw}, PrivateKey: key}},
	}, nil
}

// MarshalPrivateKey encodes an Ed25519 private key as PKCS#8 PEM.
func MarshalPrivateKey(key ed25519.PrivateKey) []byte {
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// ParsePrivateKey decodes a PKCS#8 PEM Ed25519 private key.
func ParsePrivateKey(pemBytes []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("pki: private key PEM missing")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("pki: not an Ed25519 key")
	}
	return key, nil
}

func newSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, _ := rand.Int(rand.Reader, limit)
	return n
}
