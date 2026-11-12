// Package hivelink is the hive's side of the cloud binding (ADR-0004,
// ADR-0008): claiming (code → certificate), the stored link (who we are
// and where the cloud is), the persistent tunnel to the gateway and
// certificate renewal at two thirds of lifetime.
package hivelink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/hivetls"
	"github.com/camhive/hive/internal/pki"
	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/tunnel"
)

// DefaultAPI is the production cloud API.
const DefaultAPI = "https://api.camhive.app"

// Errors.
var (
	ErrNotClaimed    = errors.New("hivelink: this hive is not claimed yet (run `hive claim`)")
	ErrCodeExpired   = errors.New("hivelink: claim code expired before the owner entered it")
	ErrAlreadyLinked = errors.New("hivelink: this hive is already claimed")
)

// Link is what the hive stores after a claim.
type Link struct {
	HiveID     string `json:"hive_id"`
	APIURL     string `json:"api_url"`
	GatewayURL string `json:"gateway_url"` // wss://host[:port]
	// LANDomain and RelayDomain are the deployment's suffixes for
	// <id>.<domain> (hivetls.Domains); empty for links made before the
	// cloud sent them, see Domains.
	LANDomain   string `json:"lan_domain,omitempty"`
	RelayDomain string `json:"relay_domain,omitempty"`
	// ServerName overrides the name expected in the gateway certificate
	// (development gateways reached by IP); empty means the URL's host.
	ServerName string    `json:"server_name,omitempty"`
	ClaimedAt  time.Time `json:"claimed_at"`
}

func (l Link) serverName(host *url.URL) string {
	if l.ServerName != "" {
		return l.ServerName
	}
	return host.Hostname()
}

// State is the stored link plus its parsed certificates.
type State struct {
	Link Link
	Cert *x509.Certificate
	CA   *x509.Certificate
}

// Load reads the link and certificates; ErrNotClaimed when absent.
func Load(p hiveconfig.Paths) (State, error) {
	raw, err := os.ReadFile(p.Link) //nolint:gosec // our own path
	if errors.Is(err, os.ErrNotExist) {
		return State{}, ErrNotClaimed
	}
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(raw, &st.Link); err != nil {
		return State{}, fmt.Errorf("hivelink: %s: %w", p.Link, err)
	}
	if st.Cert, err = readCert(p.Cert); err != nil {
		return State{}, err
	}
	if st.CA, err = readCert(p.CACert); err != nil {
		return State{}, err
	}
	return st, nil
}

// Save writes the link and certificates atomically enough for a single
// process: certificates first, link last (its presence means "claimed").
func Save(p hiveconfig.Paths, link Link, certPEM, caPEM []byte) error {
	if _, err := parseCert(certPEM); err != nil {
		return err
	}
	if _, err := parseCert(caPEM); err != nil {
		return err
	}
	if err := p.EnsureDirs(); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(link, "", "  ")
	for _, f := range []struct {
		path string
		data []byte
	}{{p.Cert, certPEM}, {p.CACert, caPEM}, {p.Link, raw}} {
		if err := os.WriteFile(f.path, f.data, 0o600); err != nil {
			return fmt.Errorf("hivelink: write %s: %w", f.path, err)
		}
	}
	return nil
}

// Client talks to the cloud API before the hive has an identity.
type Client struct {
	API  string
	HTTP *http.Client
}

// NewClient creates a Client for the API base URL.
func NewClient(api string) *Client {
	return &Client{API: strings.TrimRight(api, "/"), HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Code is a claim code the owner types into the web app.
type Code struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Issued is the collected certificate.
type Issued struct {
	HiveID      string
	CertPEM     []byte
	CAPEM       []byte
	GatewayURL  string
	LANDomain   string
	RelayDomain string
}

// Domains returns the deployment's domains for this link: the ones the
// cloud sent at claim time, else derived from the gateway host (gw.<base>
// → h.<base> and relay.<base>, which is how deployments are laid out), else
// the production defaults.
func (l Link) Domains() hivetls.Domains {
	d := hivetls.Domains{LAN: l.LANDomain, Relay: l.RelayDomain}
	if base := deploymentBase(l.GatewayURL); base != "" {
		if d.LAN == "" {
			d.LAN = "h." + base
		}
		if d.Relay == "" {
			d.Relay = "relay." + base
		}
	}
	return d.WithDefaults()
}

// deploymentBase strips the first label of the gateway host ("gw.dev.camhive.app"
// → "dev.camhive.app"); "" when the URL is unusable or has no base domain.
func deploymentBase(gatewayURL string) string {
	u, err := gatewayHost(gatewayURL)
	if err != nil {
		return ""
	}
	_, base, ok := strings.Cut(u.Hostname(), ".")
	if !ok || !strings.Contains(base, ".") {
		return ""
	}
	return base
}

// RequestCode asks the cloud for a claim code for this public key.
func (c *Client) RequestCode(ctx context.Context, pub ed25519.PublicKey) (Code, error) {
	body, _ := json.Marshal(map[string]string{"public_key": base64.StdEncoding.EncodeToString(pub)})
	var out Code
	if err := c.do(ctx, http.MethodPost, "/v1/claim/codes", body, &out); err != nil {
		return Code{}, err
	}
	return out, nil
}

// Poll returns nil while the owner has not claimed yet.
func (c *Client) Poll(ctx context.Context, code string) (*Issued, error) {
	var out struct {
		Status      string `json:"status"`
		HiveID      string `json:"hive_id"`
		CertPEM     string `json:"cert_pem"`
		CAPEM       string `json:"ca_pem"`
		GatewayURL  string `json:"gateway_url"`
		LANDomain   string `json:"lan_domain"`
		RelayDomain string `json:"relay_domain"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/claim/codes/"+url.PathEscape(code), nil, &out); err != nil {
		return nil, err
	}
	if out.Status != "issued" {
		return nil, nil
	}
	return &Issued{HiveID: out.HiveID, CertPEM: []byte(out.CertPEM), CAPEM: []byte(out.CAPEM), GatewayURL: out.GatewayURL, LANDomain: out.LANDomain, RelayDomain: out.RelayDomain}, nil
}

type apiError struct {
	Status int
	Code   string
	Msg    string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("hivelink: api %d %s: %s", e.Status, e.Code, e.Msg)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.API+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("hivelink: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &apiError{Status: resp.StatusCode, Code: e.Error.Code, Msg: e.Error.Message}
	}
	return json.Unmarshal(raw, out)
}

// ClaimOptions tune Claim.
type ClaimOptions struct {
	API      string        // default DefaultAPI
	Interval time.Duration // poll interval, default 3s
	Out      io.Writer     // where the code is shown, default os.Stdout
}

// Claim runs the whole hive side: request a code, show it, poll until the
// owner claims, store the result. It refuses to run on a claimed hive.
func Claim(ctx context.Context, p hiveconfig.Paths, key ed25519.PrivateKey, opt ClaimOptions) (Link, error) {
	if _, err := Load(p); err == nil {
		return Link{}, ErrAlreadyLinked
	}
	if opt.API == "" {
		opt.API = DefaultAPI
	}
	if opt.Interval == 0 {
		opt.Interval = 3 * time.Second
	}
	if opt.Out == nil {
		opt.Out = os.Stdout
	}
	c := NewClient(opt.API)
	code, err := c.RequestCode(ctx, key.Public().(ed25519.PublicKey))
	if err != nil {
		return Link{}, err
	}
	fmt.Fprintf(opt.Out, "Claim code: %s\nEnter it in the Camhive app (Add hive) within %d minutes. Waiting...\n",
		code.Code, int(time.Until(code.ExpiresAt).Minutes()))
	t := time.NewTicker(opt.Interval)
	defer t.Stop()
	for {
		issued, err := c.Poll(ctx, code.Code)
		var ae *apiError
		switch {
		case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
			return Link{}, ErrCodeExpired
		case err != nil:
			fmt.Fprintf(opt.Out, "cloud unreachable (%v), retrying\n", err)
		case issued != nil:
			link := Link{HiveID: issued.HiveID, APIURL: opt.API, GatewayURL: issued.GatewayURL, LANDomain: issued.LANDomain, RelayDomain: issued.RelayDomain, ClaimedAt: time.Now()}
			if err := Save(p, link, issued.CertPEM, issued.CAPEM); err != nil {
				return Link{}, err
			}
			fmt.Fprintf(opt.Out, "Claimed. Hive id %s; gateway %s\n", link.HiveID, link.GatewayURL)
			return link, nil
		}
		select {
		case <-ctx.Done():
			return Link{}, ctx.Err()
		case <-t.C:
		}
	}
}

// RenewAt is the moment a certificate should be renewed: two thirds of
// its lifetime, counted from issue time (NotBefore is backdated by
// pki.Backdate for clock skew).
func RenewAt(cert *x509.Certificate) time.Time {
	issued := cert.NotBefore.Add(pki.Backdate)
	if !issued.Before(cert.NotAfter) {
		issued = cert.NotBefore
	}
	return issued.Add(cert.NotAfter.Sub(issued) * 2 / 3)
}

// RenewMessage mirrors claim.RenewMessage on the cloud side.
func RenewMessage(hiveID string, at time.Time) []byte {
	return []byte("camhive-renew-v1\n" + hiveID + "\n" + at.UTC().Format(time.RFC3339))
}

// Renew asks the API for a fresh certificate, proving possession of the
// hive key with a signature, and stores it. It does not depend on the
// current certificate, so an expired one is no obstacle.
func Renew(ctx context.Context, p hiveconfig.Paths, st State, key ed25519.PrivateKey) (State, error) {
	at := time.Now().UTC().Truncate(time.Second)
	body, _ := json.Marshal(map[string]any{
		"hive_id":   st.Link.HiveID,
		"at":        at,
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, RenewMessage(st.Link.HiveID, at))),
	})
	var out struct {
		CertPEM string `json:"cert_pem"`
		CAPEM   string `json:"ca_pem"`
	}
	if err := NewClient(st.Link.APIURL).do(ctx, http.MethodPost, "/v1/claim/renew", body, &out); err != nil {
		return st, fmt.Errorf("renew: %w", err)
	}
	if err := Save(p, st.Link, []byte(out.CertPEM), []byte(out.CAPEM)); err != nil {
		return st, err
	}
	return Load(p)
}

// RunOptions configure Run.
type RunOptions struct {
	Version       string
	Handler       tunnel.Handler
	Logger        *slog.Logger
	RenewCheck    time.Duration // how often the certificate is inspected, default 1h
	ReconnectMin  time.Duration
	ReconnectMax  time.Duration
	OnStateChange func(State) // after a renewal
	OnConnect     func()      // after every (re)connect to the gateway
	// OnEmitter receives, once, a function that sends an event over the
	// current connection (tunnel.ErrNotConnected while offline). The hive
	// agent uses it for heartbeats and forwarded events.
	OnEmitter func(emit func(ctx context.Context, e protocol.Event) error)
}

// Run keeps the tunnel to the gateway open and renews the certificate on
// time. It returns when ctx is cancelled.
func Run(ctx context.Context, p hiveconfig.Paths, key ed25519.PrivateKey, opt RunOptions) error {
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.DiscardHandler)
	}
	if opt.RenewCheck == 0 {
		opt.RenewCheck = time.Hour
	}
	if opt.Handler == nil {
		opt.Handler = func(context.Context, protocol.Command) protocol.Result { return protocol.Result{Error: "unsupported"} }
	}
	st, err := Load(p)
	if err != nil {
		return err
	}
	var current atomic.Pointer[tunnel.Agent]
	if opt.OnEmitter != nil {
		opt.OnEmitter(func(ctx context.Context, e protocol.Event) error {
			ag := current.Load()
			if ag == nil {
				return tunnel.ErrNotConnected
			}
			return ag.Emit(ctx, e)
		})
	}
	for ctx.Err() == nil {
		st, err = renewIfDue(ctx, p, st, key, opt)
		if err != nil {
			return err
		}
		// Serve until renewal is due; never spin if the clock says it is
		// already due right after a renewal.
		deadline := RenewAt(st.Cert)
		if floor := time.Now().Add(opt.RenewCheck); deadline.Before(floor) {
			deadline = floor
		}
		agentCtx, cancel := context.WithDeadline(ctx, deadline)
		err := runAgent(agentCtx, st, key, opt, &current)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

// renewIfDue renews when past RenewAt, retrying while the cloud is
// unreachable; the old certificate keeps working until NotAfter.
func renewIfDue(ctx context.Context, p hiveconfig.Paths, st State, key ed25519.PrivateKey, opt RunOptions) (State, error) {
	for time.Now().After(RenewAt(st.Cert)) {
		renewed, err := Renew(ctx, p, st, key)
		if err == nil {
			opt.Logger.Info("certificate renewed", "not_after", renewed.Cert.NotAfter)
			if opt.OnStateChange != nil {
				opt.OnStateChange(renewed)
			}
			return renewed, nil
		}
		opt.Logger.Warn("certificate renewal failed", "err", err, "retry_in", opt.RenewCheck)
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(opt.RenewCheck):
		}
	}
	return st, nil
}

func runAgent(ctx context.Context, st State, key ed25519.PrivateKey, opt RunOptions, current *atomic.Pointer[tunnel.Agent]) error {
	host, err := gatewayHost(st.Link.GatewayURL)
	if err != nil {
		return err
	}
	tlsCfg, err := pki.ClientTLSConfig(st.CA, st.Cert, key, st.Link.serverName(host))
	if err != nil {
		return err
	}
	ag := tunnel.NewAgent(tunnel.AgentConfig{URL: "wss://" + host.Host + "/v1/hive", TLS: tlsCfg, HiveID: pki.HiveID(st.Link.HiveID),
		Version: opt.Version, ReconnectMin: opt.ReconnectMin, ReconnectMax: opt.ReconnectMax, Logger: opt.Logger, OnConnect: opt.OnConnect}, opt.Handler)
	current.Store(ag)
	defer current.Store(nil)
	ag.Run(ctx)
	return nil
}

func gatewayHost(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("hivelink: bad gateway url %q", raw)
	}
	return u, nil
}

func readCert(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // our own path
	if err != nil {
		return nil, fmt.Errorf("hivelink: %w", err)
	}
	return parseCert(raw)
}

func parseCert(p []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(p)
	if block == nil {
		return nil, errors.New("hivelink: certificate PEM missing")
	}
	return x509.ParseCertificate(block.Bytes)
}
