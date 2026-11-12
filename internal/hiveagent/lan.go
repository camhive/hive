package hiveagent

import (
	"context"
	"encoding/json"
	"net"
	"sort"
	"time"

	"github.com/camhive/hive/internal/hivetls"
	"github.com/camhive/hive/internal/protocol"
)

// LAN path (ADR-0012): the heartbeat carries the hive's LAN addresses and
// the public certificate it serves; the cloud publishes DNS and returns
// both to viewers. Certificates come from the cloud: the hive sends a CSR
// (cert.csr event) and installs the chain (cert.set command); the private
// key never leaves the hive.

// TLSInfo is what the agent needs about the hive's public TLS listener.
type TLSInfo struct {
	HiveID   string
	Domains  hivetls.Domains // deployment suffixes; zero = production
	Holder   *hivetls.Holder
	CertPath string
	KeyPath  string
	Port     int // TLS listener port advertised on the LAN
}

// SetTLS enables LAN reporting and certificate commands.
func (a *Agent) SetTLS(t TLSInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tls = &t
}

func (a *Agent) tlsInfo() *TLSInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tls
}

// LANInfo is the heartbeat's LAN section.
type LANInfo struct {
	Addrs           []string  `json:"addrs"`
	TLSPort         int       `json:"tls_port"`
	CertFingerprint string    `json:"cert_fingerprint,omitempty"`
	CertNotAfter    time.Time `json:"cert_not_after,omitempty"`
	CertIssuer      string    `json:"cert_issuer,omitempty"`
}

// EventCertCSR carries a CSR for the hive's public names.
const EventCertCSR = "cert.csr"

// lanInfo gathers the LAN section; nil when TLS is not configured.
func (a *Agent) lanInfo() *LANInfo {
	t := a.tlsInfo()
	if t == nil {
		return nil
	}
	info := &LANInfo{Addrs: LANAddrs(), TLSPort: t.Port}
	cert := t.Holder.Current()
	if len(cert.Certificate) > 0 {
		info.CertFingerprint = hivetls.Fingerprint(cert)
		info.CertIssuer = hivetls.IssuerOf(cert)
		if cert.Leaf != nil {
			info.CertNotAfter = cert.Leaf.NotAfter
		}
	}
	return info
}

// LANAddrs lists the hive's private, non-loopback addresses (IPv4 first).
func LANAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var v4, v6 []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || !ipn.IP.IsPrivate() {
				continue
			}
			if ip4 := ipn.IP.To4(); ip4 != nil {
				v4 = append(v4, ip4.String())
			} else {
				v6 = append(v6, ipn.IP.String())
			}
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return append(v4, v6...)
}

// RequestCert emits a CSR for the cloud to get a public certificate.
func (a *Agent) RequestCert() {
	t := a.tlsInfo()
	if t == nil {
		return
	}
	csr, err := t.Domains.CSR(t.Holder.Current(), t.HiveID)
	if err != nil {
		a.log.Error("csr", "err", err)
		return
	}
	a.Emit(EventCertCSR, map[string]string{"csr_pem": string(csr)})
}

// CertLoop requests a certificate when the current one is self-signed or
// due for renewal: checked every interval, and every selfSignedRetry while
// the hive still serves a self-signed certificate (the first request may
// race the cloud's start or be refused by an older cloud).
func (a *Agent) CertLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	a.RequestCertIfNeeded()
	tk := time.NewTicker(interval)
	defer tk.Stop()
	retry := time.NewTicker(selfSignedRetry)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			a.RequestCertIfNeeded()
		case <-retry.C:
			if t := a.tlsInfo(); t != nil && hivetls.IssuerOf(t.Holder.Current()) == "self-signed" {
				a.RequestCert()
			}
		}
	}
}

// RequestCertIfNeeded asks the cloud for a certificate when the current one
// is self-signed or due for renewal; wired to every reconnect so a redeployed
// cloud (new issuer, new domains) is picked up at once.
func (a *Agent) RequestCertIfNeeded() {
	t := a.tlsInfo()
	if t == nil {
		return
	}
	cert := t.Holder.Current()
	if hivetls.IssuerOf(cert) == "self-signed" || hivetls.RenewDue(cert, a.d.Now()) {
		a.RequestCert()
	}
}

// selfSignedRetry is how often a hive without a real certificate asks again.
var selfSignedRetry = 10 * time.Minute

// certSet installs a certificate chain the cloud issued for the hive's key.
func (a *Agent) certSet(ctx context.Context, args json.RawMessage) protocol.Result {
	t := a.tlsInfo()
	if t == nil {
		return protocol.Fail(protocol.ErrCodeNotReady, "tls listener not configured")
	}
	var in struct {
		ChainPEM string `json:"chain_pem"`
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	cert, err := t.Domains.Install(t.CertPath, t.KeyPath, t.HiveID, []byte(in.ChainPEM))
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInvalid, err.Error())
	}
	t.Holder.Set(cert)
	a.record(ctx, "cert.set", "", map[string]any{"issuer": hivetls.IssuerOf(cert), "not_after": cert.Leaf.NotAfter, "fingerprint": hivetls.Fingerprint(cert)})
	return protocol.Succeed(map[string]any{"fingerprint": hivetls.Fingerprint(cert), "not_after": cert.Leaf.NotAfter})
}
