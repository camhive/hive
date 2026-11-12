// Package camsim simulates IP cameras for tests and local development: RTSP
// streams (H.264 / H.265, main and sub) published through MediaMTX by ffmpeg,
// plus an ONVIF mock (device and media services) and a WS-Discovery responder.
// Every integration test of the hive runs against camsim; real cameras are for
// acceptance only.
package camsim

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Codec is a video codec a simulated camera encodes with.
type Codec string

// Supported codecs.
const (
	H264 Codec = "h264"
	H265 Codec = "h265"
)

// Stream names a camera's stream.
type Stream string

// A camera has two streams, as real cameras do.
const (
	Main Stream = "main"
	Sub  Stream = "sub"
)

// Resolution in pixels.
type Resolution struct {
	Width, Height int
}

// Camera describes one simulated camera.
type Camera struct {
	Name  string // URL-path safe, unique
	Codec Codec
	Main  Resolution
	Sub   Resolution
	FPS   int
}

// Profile selects how faithfully the ONVIF mock behaves.
type Profile string

// Profiles. Quirky imitates cheap cameras: no Media XAddr in GetCapabilities
// (GetServices still works), stream URIs with a query string, slow replies.
const (
	Normal Profile = "normal"
	Quirky Profile = "quirky"
)

// Config describes a simulator instance.
type Config struct {
	Profile      Profile
	Username     string // when set, ONVIF requires a WS-UsernameToken digest
	Password     string
	quirkDelay   time.Duration // response delay for Quirky; default 300ms
	Host         string        // advertised host for RTSP and ONVIF URLs
	RTSPPort     int
	APIPort      int // MediaMTX control API (bound to Host)
	ONVIFPort    int
	Cameras      []Camera
	WorkDir      string // generated config and logs; temp dir when empty
	MediaMTXPath string // binary; looked up in PATH when empty
	FFmpegPath   string // binary; looked up in PATH when empty
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Validate reports configuration errors a user can fix.
func (c *Config) Validate() error {
	if len(c.Cameras) == 0 {
		return errors.New("camsim: at least one camera is required")
	}
	if c.Profile != "" && c.Profile != Normal && c.Profile != Quirky {
		return fmt.Errorf("camsim: unknown profile %q", c.Profile)
	}
	seen := make(map[string]bool, len(c.Cameras))
	for _, cam := range c.Cameras {
		if !nameRE.MatchString(cam.Name) {
			return fmt.Errorf("camsim: camera name %q must match %s", cam.Name, nameRE)
		}
		if seen[cam.Name] {
			return fmt.Errorf("camsim: duplicate camera name %q", cam.Name)
		}
		seen[cam.Name] = true
		if cam.Codec != H264 && cam.Codec != H265 {
			return fmt.Errorf("camsim: camera %q: unsupported codec %q", cam.Name, cam.Codec)
		}
	}
	return nil
}

// applyDefaults fills zero values with sensible defaults.
func (c *Config) applyDefaults() {
	if c.Profile == "" {
		c.Profile = Normal
	}
	if c.quirkDelay == 0 {
		c.quirkDelay = 300 * time.Millisecond
	}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.RTSPPort == 0 {
		c.RTSPPort = 8554
	}
	if c.APIPort == 0 {
		c.APIPort = 9997
	}
	if c.ONVIFPort == 0 {
		c.ONVIFPort = 8080
	}
	for i := range c.Cameras {
		cam := &c.Cameras[i]
		if cam.Codec == "" {
			cam.Codec = H264
		}
		if cam.FPS == 0 {
			cam.FPS = 15
		}
		if cam.Main.Width == 0 {
			cam.Main = Resolution{1280, 720}
		}
		if cam.Sub.Width == 0 {
			cam.Sub = Resolution{640, 360}
		}
	}
}

// StreamPath is the RTSP path of a camera stream, e.g. "front/main".
func (cam Camera) StreamPath(s Stream) string {
	return cam.Name + "/" + string(s)
}

func (cam Camera) resolution(s Stream) Resolution {
	if s == Sub {
		return cam.Sub
	}
	return cam.Main
}

// RTSPURL is the URL a client uses to play a camera stream.
func (c *Config) RTSPURL(cam Camera, s Stream) string {
	return fmt.Sprintf("rtsp://%s:%d/%s", c.Host, c.RTSPPort, cam.StreamPath(s))
}

func (c *Config) deviceServiceURL() string {
	return fmt.Sprintf("http://%s:%d/onvif/device_service", c.Host, c.ONVIFPort)
}

func (c *Config) mediaServiceURL() string {
	return fmt.Sprintf("http://%s:%d/onvif/media_service", c.Host, c.ONVIFPort)
}

func (c *Config) ptzServiceURL() string {
	return fmt.Sprintf("http://%s:%d/onvif/ptz_service", c.Host, c.ONVIFPort)
}

// mediamtxYAML renders the MediaMTX configuration: one publish-only path per
// stream, everything else disabled.
func (c *Config) mediamtxYAML() string {
	var b strings.Builder
	fmt.Fprintf(&b, "logLevel: warn\napi: yes\napiAddress: %s:%d\n", c.Host, c.APIPort)
	fmt.Fprintf(&b, "rtsp: yes\nrtspAddress: :%d\nrtspTransports: [tcp]\n", c.RTSPPort)
	b.WriteString("rtmp: no\nhls: no\nwebrtc: no\nsrt: no\nmoq: no\nmetrics: no\npprof: no\nplayback: no\n")
	b.WriteString("paths:\n")
	for _, cam := range c.Cameras {
		for _, s := range []Stream{Main, Sub} {
			fmt.Fprintf(&b, "  %s:\n    overridePublisher: yes\n", cam.StreamPath(s))
		}
	}
	return b.String()
}

// ffmpegArgs builds the publisher command line for one stream: a synthetic
// test pattern encoded live at the camera's frame rate.
func (c *Config) ffmpegArgs(cam Camera, s Stream) []string {
	res := cam.resolution(s)
	enc := "libx264"
	if cam.Codec == H265 {
		enc = "libx265"
	}
	// testsrc2 is a moving pattern with a frame counter; no fonts needed, so it
	// behaves the same on Linux and Windows builds of ffmpeg.
	src := fmt.Sprintf("testsrc2=size=%dx%d:rate=%d", res.Width, res.Height, cam.FPS)
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-re", "-f", "lavfi", "-i", src,
		"-c:v", enc, "-preset", "ultrafast", "-tune", "zerolatency",
		"-g", itoa(cam.FPS * 2), "-pix_fmt", "yuv420p",
		"-f", "rtsp", "-rtsp_transport", "tcp", c.RTSPURL(cam, s),
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
