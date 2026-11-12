package hiveagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/video"
)

// WebRTC signalling (ADR-0010 path 2, #29). The cloud relays a viewer's
// WHEP offer as signal.offer; the hive forwards it to the video engine's
// WHEP endpoint and returns the answer plus an opaque session handle the
// cloud passes back for trickled candidates and for closing. Media never
// touches the cloud: it flows DTLS-SRTP between the viewer and the hive
// (or TURN, #30). The hive trusts the cloud's authorization over mTLS and
// only checks that the camera exists and is enabled.

const whepTimeout = 15 * time.Second

func (a *Agent) whepClient() *http.Client {
	if a.d.HTTP != nil {
		return a.d.HTTP
	}
	return &http.Client{Timeout: whepTimeout}
}

func (a *Agent) requireEngine() *protocol.Result {
	if a.d.Engine == nil {
		r := protocol.Fail(protocol.ErrCodeNotReady, "video engine not running")
		return &r
	}
	return nil
}

// signalOffer answers a WHEP offer for a camera stream.
func (a *Agent) signalOffer(ctx context.Context, args json.RawMessage) protocol.Result {
	if r := a.requireEngine(); r != nil {
		return *r
	}
	var in struct {
		Camera  string `json:"camera"`
		Stream  string `json:"stream"`
		SDP     string `json:"sdp"`
		Account string `json:"account"` // for the local activity log only
		Quality string `json:"quality"` // the hive owner's remote quality; live over the cloud is remote
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	if in.Camera == "" || !strings.Contains(in.SDP, "v=0") {
		return protocol.Fail(protocol.ErrCodeInvalid, "camera and an SDP offer are required")
	}
	cam, r := a.cameraEnabled(ctx, in.Camera)
	if r != nil {
		return *r
	}
	stream := ResolveStream(cam, auth.RemoteStream(in.Quality, in.Stream))
	answer, session, err := ForwardWHEP(ctx, a.whepClient(), a.d.Engine.WHEPURL(video.CameraID(in.Camera), stream), in.SDP)
	if errors.Is(err, ErrStreamUnavailable) {
		return protocol.Fail(protocol.ErrCodeNotFound, err.Error())
	}
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInternal, err.Error())
	}
	a.record(ctx, "signal.offer", in.Camera, map[string]any{"stream": string(stream), "account": in.Account})
	return protocol.Succeed(map[string]any{"sdp": answer, "session": session})
}

// ErrStreamUnavailable means the engine has no such stream (WHEP 404).
var ErrStreamUnavailable = errors.New("stream is not available")

// ForwardWHEP posts a WHEP offer to the engine and returns its SDP answer
// and the session handle for later PATCH and DELETE. Both signalling paths
// (cloud over the tunnel, LAN on the hive API) use it.
func ForwardWHEP(ctx context.Context, c *http.Client, whepURL, sdp string) (answer, session string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, whepURL, strings.NewReader(sdp))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/sdp")
	resp, err := c.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("whep: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusCreated:
		return string(body), sessionHandle(whepURL, resp.Header.Get("Location")), nil
	case http.StatusNotFound:
		return "", "", ErrStreamUnavailable
	default:
		return "", "", fmt.Errorf("whep answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// cameraEnabled checks the camera exists and is enabled in the store and
// returns it (nil camera without a store: the engine decides).
func (a *Agent) cameraEnabled(ctx context.Context, id string) (*camera.Camera, *protocol.Result) {
	if a.d.Store == nil {
		return nil, nil
	}
	c, err := a.d.Store.Cameras().Get(ctx, cameraID(id))
	if r := storeErr(err); r != nil {
		return nil, r
	}
	if !c.Enabled {
		r := protocol.Fail(protocol.ErrCodeInvalid, "camera is disabled")
		return nil, &r
	}
	return &c, nil
}

// ResolveStream picks the stream to serve: the requested one when the
// camera has it, otherwise the other one (an RTSP camera added by address
// has only main; a viewer on a sub-quality plan must still see it). Empty
// means main. Without a camera (no store) the request is taken as is.
func ResolveStream(c *camera.Camera, want string) video.Stream {
	s := video.Stream(want)
	if s == "" {
		s = video.Main
	}
	if c == nil || len(c.Streams) == 0 {
		return s
	}
	if _, ok := c.Streams[camera.Stream(s)]; ok {
		return s
	}
	other := video.Sub
	if s == video.Sub {
		other = video.Main
	}
	if _, ok := c.Streams[camera.Stream(other)]; ok {
		return other
	}
	return s
}

// sessionHandle turns the WHEP Location into an absolute URL the agent can
// PATCH and DELETE later; the cloud treats it as opaque.
func sessionHandle(whepURL, location string) string {
	base, err := url.Parse(whepURL)
	if err != nil || location == "" {
		return ""
	}
	ref, err := url.Parse(location)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

// sessionURL validates a handle: it must point at our engine's host.
func (a *Agent) sessionURL(handle string) (string, *protocol.Result) {
	u, err := url.Parse(handle)
	base, _ := url.Parse(a.d.Engine.WHEPURL("x", video.Main))
	if err != nil || handle == "" || base == nil || u.Host != base.Host || !strings.Contains(u.Path, "/whep/") {
		r := protocol.Fail(protocol.ErrCodeInvalid, "unknown signalling session")
		return "", &r
	}
	return handle, nil
}

// signalCandidate forwards trickled ICE candidates (RFC 8840 SDP fragment).
func (a *Agent) signalCandidate(ctx context.Context, args json.RawMessage) protocol.Result {
	if r := a.requireEngine(); r != nil {
		return *r
	}
	var in struct {
		Session    string `json:"session"`
		Candidates string `json:"candidates"`
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	target, r := a.sessionURL(in.Session)
	if r != nil {
		return *r
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPatch, target, strings.NewReader(in.Candidates))
	req.Header.Set("Content-Type", "application/trickle-ice-sdpfrag")
	resp, err := a.whepClient().Do(req)
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInternal, "whep: "+err.Error())
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return protocol.Fail(protocol.ErrCodeNotFound, "signalling session is gone")
	}
	if resp.StatusCode >= 300 {
		return protocol.Fail(protocol.ErrCodeInternal, fmt.Sprintf("whep answered %d", resp.StatusCode))
	}
	return protocol.Succeed(nil)
}

// signalClose ends a WebRTC session on the engine.
func (a *Agent) signalClose(ctx context.Context, args json.RawMessage) protocol.Result {
	if r := a.requireEngine(); r != nil {
		return *r
	}
	var in struct {
		Session string `json:"session"`
	}
	if r := decodeArgs(args, &in); r != nil {
		return *r
	}
	target, r := a.sessionURL(in.Session)
	if r != nil {
		return *r
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, target, http.NoBody)
	resp, err := a.whepClient().Do(req)
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInternal, "whep: "+err.Error())
	}
	_ = resp.Body.Close()
	return protocol.Succeed(nil) // gone already is fine
}
