// Package onvif is the Hive's client for IP cameras: WS-Discovery, Device,
// Media and PTZ services over SOAP 1.2. It is deliberately small and
// tolerant: responses are read by local element names, service addresses
// announced by the camera are rebased onto the address we actually reached
// it on, and quirks seen in cheap cameras (no Media capability, query
// strings in stream URIs, slow replies) are handled. Tested against camsim
// profiles; vendor-specific behaviour is covered by the acceptance bench.
package onvif

import (
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // WS-UsernameToken PasswordDigest is SHA-1 by specification
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Credentials authenticate SOAP calls with WS-Security UsernameToken.
type Credentials struct {
	Username string
	Password string
}

// ErrUnauthorized is returned when the camera rejects our credentials.
var ErrUnauthorized = errors.New("onvif: not authorized")

// Fault is a SOAP fault returned by the camera.
type Fault struct {
	Code    string
	Subcode string // without the namespace prefix, e.g. "NoProfile"
	Reason  string
	Status  int
}

func (f *Fault) Error() string {
	return fmt.Sprintf("onvif: fault %s/%s: %s (http %d)", f.Code, f.Subcode, f.Reason, f.Status)
}

// Namespaces of the services we use.
const (
	nsDevice = "http://www.onvif.org/ver10/device/wsdl"
	nsMedia  = "http://www.onvif.org/ver10/media/wsdl"
	nsPTZ    = "http://www.onvif.org/ver20/ptz/wsdl"
	nsSchema = "http://www.onvif.org/ver10/schema"
)

// Client talks to one camera.
type Client struct {
	device string // device service XAddr as given by the user/discovery
	creds  Credentials
	http   *http.Client
	now    func() time.Time

	mu    sync.Mutex
	media string // resolved media service XAddr
	ptz   string // resolved PTZ service XAddr, "" when unsupported
}

// Option customises a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client (timeouts, proxies, tests).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// NewClient creates a client for the camera's device service address, e.g.
// "http://192.168.1.10/onvif/device_service".
func NewClient(deviceXAddr string, creds Credentials, opts ...Option) *Client {
	c := &Client{device: deviceXAddr, creds: creds, http: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
	for _, o := range opts {
		o(c)
	}
	return c
}

// DeviceInfo is what GetDeviceInformation returns.
type DeviceInfo struct {
	Manufacturer    string `xml:"Manufacturer"`
	Model           string `xml:"Model"`
	FirmwareVersion string `xml:"FirmwareVersion"`
	SerialNumber    string `xml:"SerialNumber"`
	HardwareID      string `xml:"HardwareId"`
}

// DeviceInformation calls GetDeviceInformation.
func (c *Client) DeviceInformation(ctx context.Context) (DeviceInfo, error) {
	var out DeviceInfo
	err := c.call(ctx, c.device, `<tds:GetDeviceInformation xmlns:tds="`+nsDevice+`"/>`, "GetDeviceInformationResponse", &out)
	return out, err
}

// Capabilities summarises which services the camera offers.
type Capabilities struct {
	Media bool
	PTZ   bool
}

// Capabilities resolves service addresses (GetServices, falling back to
// GetCapabilities) and reports what is available.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	if err := c.resolveServices(ctx); err != nil {
		return Capabilities{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Capabilities{Media: c.media != "", PTZ: c.ptz != ""}, nil
}

// Profile is a media profile: one encoder configuration of one video source.
type Profile struct {
	Token    string
	Name     string
	Encoding string // H264, H265, JPEG, ...
	Width    int
	Height   int
	FPS      int
}

// Profiles calls GetProfiles on the media service.
func (c *Client) Profiles(ctx context.Context) ([]Profile, error) {
	media, err := c.mediaXAddr(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Profiles []struct {
			Token   string `xml:"token,attr"`
			Name    string `xml:"Name"`
			Encoder struct {
				Encoding   string `xml:"Encoding"`
				Resolution struct {
					Width  int `xml:"Width"`
					Height int `xml:"Height"`
				} `xml:"Resolution"`
				RateControl struct {
					FrameRateLimit int `xml:"FrameRateLimit"`
				} `xml:"RateControl"`
			} `xml:"VideoEncoderConfiguration"`
		} `xml:"Profiles"`
	}
	if err := c.call(ctx, media, `<trt:GetProfiles xmlns:trt="`+nsMedia+`"/>`, "GetProfilesResponse", &out); err != nil {
		return nil, err
	}
	profiles := make([]Profile, 0, len(out.Profiles))
	for _, p := range out.Profiles {
		profiles = append(profiles, Profile{
			Token: p.Token, Name: p.Name, Encoding: strings.ToUpper(p.Encoder.Encoding),
			Width: p.Encoder.Resolution.Width, Height: p.Encoder.Resolution.Height, FPS: p.Encoder.RateControl.FrameRateLimit,
		})
	}
	return profiles, nil
}

// StreamURI calls GetStreamUri for RTSP over TCP and returns the URI as the
// camera gave it (query strings included).
func (c *Client) StreamURI(ctx context.Context, profileToken string) (string, error) {
	media, err := c.mediaXAddr(ctx)
	if err != nil {
		return "", err
	}
	body := `<trt:GetStreamUri xmlns:trt="` + nsMedia + `" xmlns:tt="` + nsSchema + `">` +
		`<trt:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream><tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport></trt:StreamSetup>` +
		`<trt:ProfileToken>` + xmlEscape(profileToken) + `</trt:ProfileToken></trt:GetStreamUri>`
	var out struct {
		URI string `xml:"MediaUri>Uri"`
	}
	if err := c.call(ctx, media, body, "GetStreamUriResponse", &out); err != nil {
		return "", err
	}
	if out.URI == "" {
		return "", errors.New("onvif: empty stream uri")
	}
	return strings.TrimSpace(out.URI), nil
}

// SetSystemDateAndTime sets the camera clock to t (UTC, manual mode).
func (c *Client) SetSystemDateAndTime(ctx context.Context, t time.Time) error {
	t = t.UTC()
	body := fmt.Sprintf(`<tds:SetSystemDateAndTime xmlns:tds="%s" xmlns:tt="%s">`+
		`<tds:DateTimeType>Manual</tds:DateTimeType><tds:DaylightSavings>false</tds:DaylightSavings>`+
		`<tds:TimeZone><tt:TZ>UTC0</tt:TZ></tds:TimeZone>`+
		`<tds:UTCDateTime><tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time>`+
		`<tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date></tds:UTCDateTime>`+
		`</tds:SetSystemDateAndTime>`, nsDevice, nsSchema, t.Hour(), t.Minute(), t.Second(), t.Year(), int(t.Month()), t.Day())
	return c.call(ctx, c.device, body, "SetSystemDateAndTimeResponse", nil)
}

// mediaXAddr returns the resolved media service address.
func (c *Client) mediaXAddr(ctx context.Context) (string, error) {
	if err := c.resolveServices(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.media == "" {
		return "", errors.New("onvif: camera has no media service")
	}
	return c.media, nil
}

// resolveServices fills media/ptz addresses once, preferring GetServices and
// falling back to GetCapabilities. Addresses are rebased onto the device
// address's scheme and host, because cameras often announce an internal or
// stale address.
func (c *Client) resolveServices(ctx context.Context) error {
	c.mu.Lock()
	resolved := c.media != ""
	c.mu.Unlock()
	if resolved {
		return nil
	}
	media, ptz, err := c.fromServices(ctx)
	if err != nil || media == "" {
		m2, p2, err2 := c.fromCapabilities(ctx)
		if err2 != nil && err != nil {
			return err
		}
		if media == "" {
			media = m2
		}
		if ptz == "" {
			ptz = p2
		}
	}
	if media == "" {
		// Last resort used by many clients: assume the conventional path.
		media = strings.Replace(c.device, "device_service", "media_service", 1)
	}
	c.mu.Lock()
	c.media, c.ptz = c.rebase(media), c.rebase(ptz)
	c.mu.Unlock()
	return nil
}

func (c *Client) fromServices(ctx context.Context) (media, ptz string, err error) {
	var out struct {
		Services []struct {
			Namespace string `xml:"Namespace"`
			XAddr     string `xml:"XAddr"`
		} `xml:"Service"`
	}
	body := `<tds:GetServices xmlns:tds="` + nsDevice + `"><tds:IncludeCapability>false</tds:IncludeCapability></tds:GetServices>`
	if err := c.call(ctx, c.device, body, "GetServicesResponse", &out); err != nil {
		return "", "", err
	}
	for _, s := range out.Services {
		switch strings.TrimSpace(s.Namespace) {
		case nsMedia:
			media = strings.TrimSpace(s.XAddr)
		case nsPTZ:
			ptz = strings.TrimSpace(s.XAddr)
		}
	}
	return media, ptz, nil
}

func (c *Client) fromCapabilities(ctx context.Context) (media, ptz string, err error) {
	var out struct {
		Media string `xml:"Capabilities>Media>XAddr"`
		PTZ   string `xml:"Capabilities>PTZ>XAddr"`
	}
	body := `<tds:GetCapabilities xmlns:tds="` + nsDevice + `"><tds:Category>All</tds:Category></tds:GetCapabilities>`
	if err := c.call(ctx, c.device, body, "GetCapabilitiesResponse", &out); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(out.Media), strings.TrimSpace(out.PTZ), nil
}

// rebase keeps the path of xaddr but uses the scheme and host we reached the
// device service on.
func (c *Client) rebase(xaddr string) string {
	if xaddr == "" {
		return ""
	}
	dev, err1 := url.Parse(c.device)
	u, err2 := url.Parse(xaddr)
	if err1 != nil || err2 != nil {
		return xaddr
	}
	u.Scheme, u.Host = dev.Scheme, dev.Host
	return u.String()
}

// call posts one SOAP action and decodes the element named responseLocal
// from the body into out (out may be nil to only check for faults).
func (c *Client) call(ctx context.Context, xaddr, body, responseLocal string, out any) error {
	env := `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">` +
		c.securityHeader() + `<s:Body>` + body + `</s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, xaddr, strings.NewReader(env))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("onvif: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("onvif: read response: %w", err)
	}
	if f := parseFault(raw, resp.StatusCode); f != nil {
		if f.Subcode == "NotAuthorized" || resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("%w: %s", ErrUnauthorized, f.Reason)
		}
		return f
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("onvif: http %d from %s", resp.StatusCode, xaddr)
	}
	if out == nil {
		return nil
	}
	return decodeBodyElement(raw, responseLocal, out)
}

// securityHeader builds the WS-Security UsernameToken header, or nothing when
// no credentials are configured.
func (c *Client) securityHeader() string {
	if c.creds.Username == "" {
		return ""
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	created := c.now().UTC().Format("2006-01-02T15:04:05.000Z")
	h := sha1.New() //nolint:gosec // specification
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(c.creds.Password))
	return `<s:Header><wsse:Security s:mustUnderstand="1" xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"` +
		` xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"><wsse:UsernameToken>` +
		`<wsse:Username>` + xmlEscape(c.creds.Username) + `</wsse:Username>` +
		`<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + base64.StdEncoding.EncodeToString(h.Sum(nil)) + `</wsse:Password>` +
		`<wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">` + base64.StdEncoding.EncodeToString(nonce) + `</wsse:Nonce>` +
		`<wsu:Created>` + created + `</wsu:Created></wsse:UsernameToken></wsse:Security></s:Header>`
}

// parseFault returns a Fault when the envelope body is a SOAP fault.
func parseFault(raw []byte, status int) *Fault {
	var env struct {
		Fault *struct {
			Code struct {
				Value   string `xml:"Value"`
				Subcode struct {
					Value string `xml:"Value"`
				} `xml:"Subcode"`
			} `xml:"Code"`
			Reason struct {
				Text string `xml:"Text"`
			} `xml:"Reason"`
		} `xml:"Body>Fault"`
	}
	// A body that is not XML is not a fault either; the caller reports the
	// HTTP status or the missing response element.
	_ = xml.Unmarshal(raw, &env)
	if env.Fault == nil {
		return nil
	}
	sub := env.Fault.Code.Subcode.Value
	if i := strings.LastIndex(sub, ":"); i >= 0 {
		sub = sub[i+1:]
	}
	return &Fault{Code: env.Fault.Code.Value, Subcode: sub, Reason: strings.TrimSpace(env.Fault.Reason.Text), Status: status}
}

// decodeBodyElement finds the first element named local inside the SOAP body
// and decodes it into out, ignoring namespaces.
func decodeBodyElement(raw []byte, local string, out any) error {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("onvif: %s not found in response: %w", local, err)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == local {
			return dec.DecodeElement(out, &se)
		}
	}
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
