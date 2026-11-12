// Package hivetls manages the hive's public-facing TLS certificate
// (ADR-0012): the name <hive-id>.h.camhive.app for the LAN and
// <hive-id>.relay.camhive.app for the cloud relay. Until the cloud hands
// over a Let's Encrypt certificate (#32), a self-signed ECDSA certificate
// is generated so the relay path (TLS passthrough, terminated on the hive)
// works for clients that pin the hive's key.
package hivetls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"
)

// Domains the hive is reachable under (production defaults).
const (
	LANDomain   = "h.camhive.app"
	RelayDomain = "relay.camhive.app"
)

// Domains are the DNS suffixes of one deployment: a hive is reachable as
// <id>.<LAN> on its network and <id>.<Relay> through the relay. The cloud
// hands them to the hive at claim time (see hivelink.Link); zero values mean
// the production domains.
type Domains struct {
	LAN   string
	Relay string
}

// DefaultDomains are the production domains.
var DefaultDomains = Domains{LAN: LANDomain, Relay: RelayDomain}

// WithDefaults fills empty fields from DefaultDomains.
func (d Domains) WithDefaults() Domains {
	if d.LAN == "" {
		d.LAN = LANDomain
	}
	if d.Relay == "" {
		d.Relay = RelayDomain
	}
	return d
}

// Names returns the DNS names a hive's certificate must cover: LAN name first.
func (d Domains) Names(hiveID string) []string {
	d = d.WithDefaults()
	return []string{Label(hiveID) + "." + d.LAN, Label(hiveID) + "." + d.Relay}
}

// Label is the DNS label of a hive id: public CAs issue only for
// letter-digit-hyphen hostnames, so the underscore of `hv_<hex>` becomes a
// hyphen (`hv-<hex>`). IDFromLabel reverses it.
func Label(hiveID string) string { return strings.ReplaceAll(hiveID, "_", "-") }

// IDFromLabel turns a DNS label back into the hive id ("hv-<hex>" → "hv_<hex>").
func IDFromLabel(label string) string {
	if rest, ok := strings.CutPrefix(label, "hv-"); ok {
		return "hv_" + rest
	}
	return label
}

// Names returns the production names of a hive.
func Names(hiveID string) []string { return DefaultDomains.Names(hiveID) }

// LoadOrCreate is Domains.LoadOrCreate for the production domains.
func LoadOrCreate(certPath, keyPath, hiveID string) (tls.Certificate, error) {
	return DefaultDomains.LoadOrCreate(certPath, keyPath, hiveID)
}

// LoadOrCreate returns the certificate at certPath/keyPath, generating a
// self-signed one for the hive's names when absent, expired or issued for
// other names (a deployment's domains changed).
func (d Domains) LoadOrCreate(certPath, keyPath, hiveID string) (tls.Certificate, error) {
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		leaf, perr := x509.ParseCertificate(cert.Certificate[0])
		if perr == nil && time.Now().Before(leaf.NotAfter.Add(-7*24*time.Hour)) && covers(leaf, d.Names(hiveID)) {
			cert.Leaf = leaf
			return cert, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, fmt.Errorf("hivetls: %w", err)
	}
	return d.create(certPath, keyPath, hiveID)
}

func covers(leaf *x509.Certificate, names []string) bool {
	for _, n := range names {
		if leaf.VerifyHostname(n) != nil {
			return false
		}
	}
	return true
}

func (d Domains) create(certPath, keyPath, hiveID string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: d.Names(hiveID)[0], Organization: []string{"Camhive Hive (self-signed)"}},
		DNSNames:     d.Names(hiveID),
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil { //nolint:gosec // public certificate
		return tls.Certificate{}, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, err
	}
	cert.Leaf, _ = x509.ParseCertificate(der)
	return cert, nil
}

// Fingerprint is the SHA-256 of the leaf certificate, hex; the cloud
// reports it to clients that pin self-signed hives.
func Fingerprint(cert tls.Certificate) string {
	sum := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(sum[:])
}

// ServerConfig is the TLS configuration for the hive's public listener.
func ServerConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
}

// PinningClientConfig verifies the server by certificate fingerprint and
// name instead of a CA (test and pinning clients).
func PinningClientConfig(fingerprint, serverName string) *tls.Config {
	return &tls.Config{
		ServerName:             serverName,
		InsecureSkipVerify:     true, //nolint:gosec // replaced by VerifyConnection below, which also runs on resumed sessions
		MinVersion:             tls.VersionTLS12,
		SessionTicketsDisabled: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("hivetls: no certificate")
			}
			leaf := cs.PeerCertificates[0]
			sum := sha256.Sum256(leaf.Raw)
			if hex.EncodeToString(sum[:]) != fingerprint {
				return errors.New("hivetls: certificate fingerprint mismatch")
			}
			return leaf.VerifyHostname(serverName)
		},
	}
}

// Holder serves the current certificate to a TLS listener and lets it be
// replaced without a restart (the cloud hands over a public certificate
// for the same key).
type Holder struct {
	mu   sync.RWMutex
	cert tls.Certificate
}

// NewHolder starts with cert.
func NewHolder(cert tls.Certificate) *Holder { return &Holder{cert: cert} }

// Current returns the certificate in use.
func (h *Holder) Current() tls.Certificate {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cert
}

// Set replaces the certificate.
func (h *Holder) Set(cert tls.Certificate) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cert = cert
}

// ServerConfig is a TLS config that always serves the holder's current
// certificate.
func (h *Holder) ServerConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			c := h.Current()
			return &c, nil
		}}
}

// CSR is Domains.CSR for the production domains.
func CSR(cert tls.Certificate, hiveID string) ([]byte, error) {
	return DefaultDomains.CSR(cert, hiveID)
}

// CSR builds a certificate signing request for the hive's names with the
// private key behind cert; the key never leaves the hive.
func (d Domains) CSR(cert tls.Certificate, hiveID string) ([]byte, error) {
	tmpl := &x509.CertificateRequest{Subject: pkix.Name{CommonName: d.Names(hiveID)[0]}, DNSNames: d.Names(hiveID)}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, cert.PrivateKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// Install is Domains.Install for the production domains.
func Install(certPath, keyPath, hiveID string, chainPEM []byte) (tls.Certificate, error) {
	return DefaultDomains.Install(certPath, keyPath, hiveID, chainPEM)
}

// Install pairs an issued chain (PEM, leaf first) with the key at keyPath,
// checks it covers the hive's names and belongs to that key, stores it at
// certPath and returns it.
func (d Domains) Install(certPath, keyPath, hiveID string, chainPEM []byte) (tls.Certificate, error) {
	keyPEM, err := os.ReadFile(keyPath) //nolint:gosec // our own path
	if err != nil {
		return tls.Certificate{}, err
	}
	cert, err := tls.X509KeyPair(chainPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("hivetls: chain does not match the hive key: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, err
	}
	for _, n := range d.Names(hiveID) {
		if leaf.VerifyHostname(n) != nil {
			return tls.Certificate{}, fmt.Errorf("hivetls: certificate does not cover %s", n)
		}
	}
	if time.Now().After(leaf.NotAfter) {
		return tls.Certificate{}, errors.New("hivetls: certificate already expired")
	}
	if err := os.WriteFile(certPath, chainPEM, 0o644); err != nil { //nolint:gosec // public certificate chain
		return tls.Certificate{}, err
	}
	cert.Leaf = leaf
	return cert, nil
}

// IssuerOf classifies the certificate for reporting.
func IssuerOf(cert tls.Certificate) string {
	leaf := cert.Leaf
	if leaf == nil {
		if l, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
			leaf = l
		} else {
			return ""
		}
	}
	switch {
	case leaf.Issuer.CommonName == leaf.Subject.CommonName:
		return "self-signed"
	case strings.Contains(strings.ToLower(leaf.Issuer.CommonName), "let's encrypt") || strings.HasPrefix(leaf.Issuer.CommonName, "R") || strings.HasPrefix(leaf.Issuer.CommonName, "E"):
		return "letsencrypt"
	default:
		return "camhive"
	}
}

// RenewDue reports whether a certificate should be renewed: less than a
// third of its lifetime left (30 days for Let's Encrypt's 90).
func RenewDue(cert tls.Certificate, now time.Time) bool {
	leaf := cert.Leaf
	if leaf == nil {
		return true
	}
	life := leaf.NotAfter.Sub(leaf.NotBefore)
	return now.After(leaf.NotAfter.Add(-life / 3))
}
