// Package camera is the domain model of a camera attached to a Hive: how it
// is reached (ONVIF device or a bare RTSP URL), its credentials, its main and
// sub streams and whether it is enabled. It is pure: no I/O, no engine, no
// database. Illegal states are rejected at construction (New, Update,
// WithStreams) so the rest of the hive can trust a Camera value.
package camera

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("camera: invalid")

// ID identifies a camera within a hive: "cam_" + 12 hex.
type ID string

// SourceKind says how the camera is reached.
type SourceKind string

// Source kinds.
const (
	SourceONVIF SourceKind = "onvif" // Address is the device service XAddr
	SourceRTSP  SourceKind = "rtsp"  // Address is the main stream RTSP URL
)

// Stream names one of a camera's streams.
type Stream string

// Every camera has a main stream; sub is optional for RTSP cameras.
const (
	Main Stream = "main"
	Sub  Stream = "sub"
)

// Source is the camera's address without credentials.
type Source struct {
	Kind    SourceKind
	Address string
}

// Credentials authenticate against the camera. Never logged; String redacts.
type Credentials struct {
	Username string
	Password string
}

// String implements fmt.Stringer and never reveals the password.
func (c Credentials) String() string {
	if c.Username == "" && c.Password == "" {
		return "(none)"
	}
	return c.Username + ":***"
}

// LogValue implements slog.LogValuer so structured logs cannot leak the
// password even when the struct is passed as a value.
func (c Credentials) LogValue() slog.Value { return slog.StringValue(c.String()) }

// LogValue implements slog.LogValuer for Spec, which may carry credentials.
func (s Spec) LogValue() slog.Value {
	return slog.GroupValue(slog.String("name", s.Name), slog.String("kind", string(s.Source.Kind)), slog.String("address", s.Source.Address), slog.Any("credentials", s.Credentials))
}

// LogValue implements slog.LogValuer for Camera.
func (c Camera) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", string(c.ID)), slog.String("name", c.Name), slog.String("kind", string(c.Source.Kind)), slog.String("address", c.Source.Address), slog.Bool("enabled", c.Enabled))
}

// StreamSpec describes one stream as discovered or configured.
type StreamSpec struct {
	URL          string // rtsp://host[:port]/path (no credentials)
	ProfileToken string // ONVIF profile token, if any
	Codec        string // H264, H265, ...
	Width        int
	Height       int
}

// Spec is the input for New.
type Spec struct {
	Name        string
	Group       string
	Source      Source
	Credentials Credentials
	Streams     map[Stream]StreamSpec // optional; for RTSP the main stream is derived from Source
}

// Camera is the validated model.
type Camera struct {
	ID          ID
	Name        string
	Group       string
	Source      Source
	Credentials Credentials
	Streams     map[Stream]StreamSpec
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// String implements fmt.Stringer without credentials.
func (c Camera) String() string {
	return fmt.Sprintf("camera %s %q %s %s enabled=%v", c.ID, c.Name, c.Source.Kind, c.Source.Address, c.Enabled)
}

// Patch lists fields Update may change; nil means unchanged.
type Patch struct {
	Name        *string
	Group       *string
	Enabled     *bool
	Credentials *Credentials
}

// New validates spec and returns an enabled camera with a fresh ID.
func New(spec Spec, now time.Time) (Camera, error) {
	src, creds, err := normalizeSource(spec.Source, spec.Credentials)
	if err != nil {
		return Camera{}, err
	}
	streams := cloneStreams(spec.Streams)
	if src.Kind == SourceRTSP {
		if _, ok := streams[Main]; !ok {
			streams[Main] = StreamSpec{URL: src.Address}
		}
	}
	c := Camera{
		ID: newID(), Name: strings.TrimSpace(spec.Name), Group: strings.TrimSpace(spec.Group),
		Source: src, Credentials: creds, Streams: streams, Enabled: true,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}
	if err := c.validate(); err != nil {
		return Camera{}, err
	}
	return c, nil
}

// Update returns a copy with the patch applied and UpdatedAt set to now.
func (c Camera) Update(p Patch, now time.Time) (Camera, error) {
	u := c
	u.Streams = cloneStreams(c.Streams)
	if p.Name != nil {
		u.Name = strings.TrimSpace(*p.Name)
	}
	if p.Group != nil {
		u.Group = strings.TrimSpace(*p.Group)
	}
	if p.Enabled != nil {
		u.Enabled = *p.Enabled
	}
	if p.Credentials != nil {
		u.Credentials = *p.Credentials
	}
	u.UpdatedAt = now.UTC()
	if err := u.validate(); err != nil {
		return Camera{}, err
	}
	return u, nil
}

// WithStreams returns a copy with streams replaced (e.g. after ONVIF
// discovery filled profile tokens and codecs).
func (c Camera) WithStreams(streams map[Stream]StreamSpec, now time.Time) (Camera, error) {
	u := c
	u.Streams = cloneStreams(streams)
	u.UpdatedAt = now.UTC()
	if err := u.validate(); err != nil {
		return Camera{}, err
	}
	return u, nil
}

func (c Camera) validate() error {
	switch {
	case c.Name == "":
		return fmt.Errorf("%w: name is required", ErrInvalid)
	case len(c.Name) > 100:
		return fmt.Errorf("%w: name longer than 100 characters", ErrInvalid)
	case len(c.Group) > 100:
		return fmt.Errorf("%w: group longer than 100 characters", ErrInvalid)
	}
	// An ONVIF camera may be created before its profiles are known; once any
	// stream is set, main must be among them. RTSP cameras always have main.
	if _, ok := c.Streams[Main]; !ok && (len(c.Streams) > 0 || c.Source.Kind == SourceRTSP) {
		return fmt.Errorf("%w: main stream is required", ErrInvalid)
	}
	for name, s := range c.Streams {
		if name != Main && name != Sub {
			return fmt.Errorf("%w: unknown stream %q", ErrInvalid, name)
		}
		if err := validRTSP(s.URL); err != nil {
			return fmt.Errorf("%w: stream %s: %w", ErrInvalid, name, err)
		}
	}
	return nil
}

// normalizeSource validates the address for its kind and, for RTSP, moves
// credentials embedded in the URL into Credentials so the URL can be stored
// and logged safely.
func normalizeSource(src Source, creds Credentials) (Source, Credentials, error) {
	src.Address = strings.TrimSpace(src.Address)
	switch src.Kind {
	case SourceONVIF:
		u, err := url.Parse(src.Address)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Source{}, Credentials{}, fmt.Errorf("%w: onvif address must be an http(s) URL", ErrInvalid)
		}
		return src, creds, nil
	case SourceRTSP:
		return normalizeRTSP(src, creds)
	default:
		return Source{}, Credentials{}, fmt.Errorf("%w: unknown source kind %q", ErrInvalid, src.Kind)
	}
}

func normalizeRTSP(src Source, creds Credentials) (Source, Credentials, error) {
	u, err := url.Parse(src.Address)
	if err != nil || (u.Scheme != "rtsp" && u.Scheme != "rtsps") || u.Host == "" {
		return Source{}, Credentials{}, fmt.Errorf("%w: rtsp address must be an rtsp(s) URL", ErrInvalid)
	}
	if u.User != nil {
		if creds.Username == "" {
			pw, _ := u.User.Password()
			creds = Credentials{Username: u.User.Username(), Password: pw}
		}
		u.User = nil
		src.Address = u.String()
	}
	return src, creds, nil
}

func validRTSP(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "rtsp" && u.Scheme != "rtsps") || u.Host == "" {
		return errors.New("must be an rtsp(s) URL")
	}
	if u.User != nil {
		return errors.New("must not embed credentials")
	}
	return nil
}

func cloneStreams(in map[Stream]StreamSpec) map[Stream]StreamSpec {
	out := make(map[Stream]StreamSpec, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func newID() ID {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return ID("cam_" + hex.EncodeToString(b[:]))
}
