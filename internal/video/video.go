// Package video is the Hive's video engine boundary: ingest camera streams,
// record them continuously, serve live (WebRTC) and archive playback. The
// only implementation today drives MediaMTX as a sidecar (ADR-0002); nothing
// outside this package knows MediaMTX exists.
package video

import (
	"context"
	"errors"
	"time"
)

// CameraID identifies a camera within a Hive. URL-path safe.
type CameraID string

// Stream names one of a camera's streams.
type Stream string

// Every camera exposes a main (full quality) and a sub (reduced) stream.
const (
	Main Stream = "main"
	Sub  Stream = "sub"
)

// Source is one camera stream the engine ingests and records.
type Source struct {
	Camera CameraID
	Stream Stream
	URL    string // rtsp://[user:pass@]host[:port]/path
	Audio  bool   // also take the camera's audio; off means the camera is never asked for it (ADR-0015)
}

// Status is the live state of a source as seen by the engine.
type Status struct {
	Ready         bool     // frames are flowing
	Tracks        []string // codec names, e.g. "H264"
	BytesReceived int64
	Readers       int // current live viewers
}

// ICEUpdater is implemented by engines that can change their advertised
// STUN/TURN servers at runtime (the cloud pushes per-hive TURN credentials).
type ICEUpdater interface {
	SetICEServers(ctx context.Context, servers []ICEServer) error
}

// Segment is one recorded archive file.
type Segment struct {
	Start    time.Time
	Duration time.Duration // zero when the engine cannot tell (e.g. file still open)
}

// Sentinel errors.
var (
	ErrNotFound = errors.New("video: source not found")
	ErrNotReady = errors.New("video: engine not running")
)

// Engine is what the Hive needs from a video backend.
type Engine interface {
	// Start launches the backend and blocks until it accepts control calls.
	Start(ctx context.Context) error
	// Stop terminates the backend. Safe to call more than once.
	Stop() error

	// AddSource starts ingesting and recording a stream. Idempotent: adding
	// the same camera/stream again replaces its configuration.
	AddSource(ctx context.Context, src Source) error
	// RemoveSource stops ingest and recording. Removing an unknown source is
	// not an error. Recorded segments are kept.
	RemoveSource(ctx context.Context, cam CameraID, s Stream) error

	// Status reports the live state of a source.
	Status(ctx context.Context, cam CameraID, s Stream) (Status, error)
	// Segments lists recorded segments of a source, oldest first.
	Segments(ctx context.Context, cam CameraID, s Stream) ([]Segment, error)

	// PlaybackURL serves archive as fMP4 for the given time span.
	PlaybackURL(cam CameraID, s Stream, start time.Time, d time.Duration) string
	// WHEPURL is the WebRTC-HTTP egress endpoint for live viewing.
	WHEPURL(cam CameraID, s Stream) string
}
