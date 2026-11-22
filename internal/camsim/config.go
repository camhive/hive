// Package camsim simulates IP cameras for tests and local development: RTSP
// streams (H.264 / H.265, main and sub, optionally with G.711 audio)
// published through MediaMTX by ffmpeg,
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
	Audio bool // a microphone: both streams carry a G.711 µ-law tone
	// Motion, when set, replaces the moving test pattern with a still scene
	// that an object crosses in the last third of every Motion period, and
	// the ONVIF mock reports motion on the same schedule (ADR-0020).
	Motion time.Duration
}

// Moving reports whether the object crosses the scene `since` after the
// simulator started.
func (cam Camera) Moving(since time.Duration) bool {
	if cam.Motion <= 0 {
		return false
	}
	return since%cam.Motion >= cam.Motion*2/3
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
	NoEvents     bool          // no ONVIF event service, as on cameras without one
	ClockOffset  time.Duration // GetSystemDateAndTime answers the time this far off (camera care, #163)
	WorkDir      string        // generated config and logs; temp dir when empty
	MediaMTXPath string        // binary; looked up in PATH when empty
	FFmpegPath   string        // binary; looked up in PATH when empty
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

// DeviceServiceURL is the simulator's ONVIF device service address.
func (c *Config) DeviceServiceURL() string { return c.deviceServiceURL() }

func (c *Config) deviceServiceURL() string {
	return fmt.Sprintf("http://%s:%d/onvif/device_service", c.Host, c.ONVIFPort)
}

func (c *Config) mediaServiceURL() string {
	return fmt.Sprintf("http://%s:%d/onvif/media_service", c.Host, c.ONVIFPort)
}

func (c *Config) eventsServiceURL() string {
	return fmt.Sprintf("http://%s:%d/onvif/events_service", c.Host, c.ONVIFPort)
}

func (c *Config) subscriptionURL(id string) string {
	return fmt.Sprintf("http://%s:%d/onvif/subscription/%s", c.Host, c.ONVIFPort, id)
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
// test pattern encoded live at the camera's frame rate and, for a camera
// with a microphone, a G.711 µ-law tone, as most real cameras send.
func (c *Config) ffmpegArgs(cam Camera, s Stream) []string {
	res := cam.resolution(s)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-re", "-f", "lavfi", "-i", cam.scene(res),
	}
	audio := []string{"-an"}
	if cam.Audio {
		args = append(args, "-re", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=8000")
		audio = []string{"-map", "0:v", "-map", "1:a", "-c:a", "pcm_mulaw", "-ar", "8000", "-ac", "1"}
	}
	args = append(args, cam.videoEncoder()...)
	args = append(args, audio...)
	return append(args, "-f", "rtsp", "-rtsp_transport", "tcp", c.RTSPURL(cam, s))
}

// videoEncoder is how every stream is encoded: the camera's codec, fast,
// no B-frames, a keyframe every 2 seconds.
func (cam Camera) videoEncoder() []string {
	enc := "libx264"
	if cam.Codec == H265 {
		enc = "libx265"
	}
	return []string{"-c:v", enc, "-preset", "ultrafast", "-tune", "zerolatency",
		"-g", itoa(cam.FPS * 2), "-pix_fmt", "yuv420p"}
}

// ClipArgs builds the ffmpeg command line that encodes d of the stream's
// test pattern once, as fast as it can, into a raw Annex-B file at out
// with an access unit delimiter before every frame: the live publisher's
// scene and encoder, with temporal noise so the encoder spends the
// bitrate, held at kbps. A source that replays the clip costs
// a fraction of a live encoder, so a benchmark measures the recorder, not
// ffmpeg (tools/sizing, #142).
func (cam Camera) ClipArgs(s Stream, kbps int, d time.Duration, out string) []string {
	rate := strconv.Itoa(kbps) + "k"
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", cam.scene(cam.resolution(s)) + ",noise=alls=12:allf=t",
		"-t", strconv.Itoa(int(d.Seconds()))}
	args = append(args, cam.videoEncoder()...)
	raw := "h264"
	if cam.Codec == H265 {
		raw = "hevc"
	}
	return append(args, "-b:v", rate, "-maxrate", rate, "-bufsize", rate, "-an",
		"-bsf:v", raw+"_metadata=aud=insert", "-f", raw, "-y", out)
}

// scene is the lavfi source of a stream. testsrc2 is a moving pattern with a
// frame counter; no fonts needed, so it behaves the same on Linux and Windows
// builds of ffmpeg. A motion camera instead shows a still, textured scene
// with a little sensor noise, crossed by a light box on its Moving schedule.
func (cam Camera) scene(res Resolution) string {
	if cam.Motion <= 0 {
		return fmt.Sprintf("testsrc2=size=%dx%d:rate=%d", res.Width, res.Height, cam.FPS)
	}
	period := int(cam.Motion.Seconds())
	boxW, boxH := res.Width/10, res.Height/4
	speed := float64(res.Width+boxW) / (cam.Motion.Seconds() / 3) // one crossing per moving third
	return fmt.Sprintf("color=c=0x606060:size=%dx%d:rate=%d,format=yuv420p,noise=alls=20:allf=0,noise=alls=2:allf=t[bg];"+
		"color=c=0xe0e0e0:size=%dx%d:rate=%d[box];"+
		"[bg][box]overlay=x='if(gte(mod(t\\,%d)\\,%d)\\,(mod(t\\,%d)-%d)*%.1f-%d\\,-%d)':y=%d",
		res.Width, res.Height, cam.FPS, boxW, boxH, cam.FPS,
		period, period*2/3, period, period*2/3, speed, boxW, boxW, res.Height/3)
}

func itoa(i int) string { return strconv.Itoa(i) }
