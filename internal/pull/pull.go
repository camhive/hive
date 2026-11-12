// Package pull reads one camera Stream with the Hive's own RTSP client and
// publishes it to the video engine on loopback (ADR-0015). Only the media
// the Camera allows are set up at the camera: video always, audio only when
// the owner switched sound on; metadata and back channels never. When audio
// is off, the camera is never asked for it, so no sound reaches the Hive.
//
// Run keeps the pull alive: it reconnects with exponential backoff when the
// camera or the engine goes away and stops when its context ends.
package pull

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
)

// Config describes one pull.
type Config struct {
	Source string // camera stream, rtsp://[user:pass@]host[:port]/path
	Target string // engine path to publish to, rtsp://user:pass@127.0.0.1:port/path
	Audio  bool   // also pull the camera's audio

	MinBackoff  time.Duration // first wait after a failure; default 1s
	MaxBackoff  time.Duration // cap of the doubling wait; default 30s
	StableAfter time.Duration // a session this long resets the backoff; default 10s
	ReadTimeout time.Duration // RTSP read timeout; default 10s
	Logger      *slog.Logger
}

func (c *Config) applyDefaults() {
	if c.MinBackoff == 0 {
		c.MinBackoff = time.Second
	}
	if c.MaxBackoff == 0 {
		c.MaxBackoff = 30 * time.Second
	}
	if c.StableAfter == 0 {
		c.StableAfter = 10 * time.Second
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 10 * time.Second
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
}

// Select returns the media of a camera's description that may leave the
// camera: every video media, and audio media when audio is true. Back
// channels (the camera's speaker) and application data are never pulled.
func Select(medias []*description.Media, audio bool) []*description.Media {
	out := make([]*description.Media, 0, len(medias))
	for _, m := range medias {
		if m.IsBackChannel {
			continue
		}
		if m.Type == description.MediaTypeVideo || (audio && m.Type == description.MediaTypeAudio) {
			out = append(out, m)
		}
	}
	return out
}

// errNoMedia means the camera offers nothing the Hive may pull.
var errNoMedia = errors.New("pull: camera offers no allowed media")

// backoff doubles a wait from min up to max.
type backoff struct {
	min, max, cur time.Duration
}

func (b *backoff) next() time.Duration {
	if b.cur == 0 {
		b.cur = b.min
	} else {
		b.cur = min(2*b.cur, b.max)
	}
	return b.cur
}

func (b *backoff) reset() { b.cur = 0 }

// Run pulls and publishes until ctx ends, reconnecting after every failure.
// Reconnects are logged at info level when the state changes (connected,
// lost, a new error), at debug level otherwise.
func Run(ctx context.Context, cfg Config) {
	cfg.applyDefaults()
	b := backoff{min: cfg.MinBackoff, max: cfg.MaxBackoff}
	lastErr := ""
	for ctx.Err() == nil {
		var playing time.Time
		err := session(ctx, cfg, func() {
			playing = time.Now()
			cfg.Logger.Info("camera stream connected", "audio", cfg.Audio)
		})
		if ctx.Err() != nil {
			return
		}
		if !playing.IsZero() {
			lastErr = ""
			if time.Since(playing) >= cfg.StableAfter {
				b.reset()
			}
		}
		wait := b.next()
		lastErr = logFailure(cfg.Logger, err, lastErr, wait)
		sleep(ctx, wait)
	}
}

// logFailure logs a failed or ended session and returns the error text to
// compare against next time.
func logFailure(log *slog.Logger, err error, last string, wait time.Duration) string {
	msg := "stream ended"
	if err != nil {
		msg = err.Error()
	}
	level := slog.LevelDebug
	if msg != last {
		level = slog.LevelInfo
	}
	log.Log(context.Background(), level, "camera stream unavailable; reconnecting", "err", msg, "retry_in", wait)
	return msg
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// session runs one connection: describe the camera, set up the allowed
// media, publish them to the target and forward packets until either side
// fails or ctx ends. playing is called once packets can flow.
func session(ctx context.Context, cfg Config, playing func()) error {
	src, err := base.ParseURL(cfg.Source)
	if err != nil {
		return err
	}
	reader := &gortsplib.Client{Scheme: src.Scheme, Host: src.Host, ReadTimeout: cfg.ReadTimeout}
	quiet(reader, cfg.Logger)
	if err := reader.Start(); err != nil {
		return err
	}
	defer reader.Close()
	stop := context.AfterFunc(ctx, reader.Close)
	defer stop()

	medias, err := setup(reader, src, cfg.Audio)
	if err != nil {
		return err
	}
	pub := &gortsplib.Client{Protocol: ptr(gortsplib.ProtocolTCP), ReadTimeout: cfg.ReadTimeout}
	quiet(pub, cfg.Logger)
	if err := pub.StartRecording(cfg.Target, &description.Session{Medias: medias}); err != nil {
		return err
	}
	defer pub.Close()

	reader.OnPacketRTPAny(func(m *description.Media, _ format.Format, pkt *rtp.Packet) {
		_ = pub.WritePacketRTP(m, pkt) // a dead publisher ends the session through Wait
	})
	if _, err := reader.Play(nil); err != nil {
		return err
	}
	playing()
	return firstError(reader, pub)
}

// setup describes the camera stream and sets up only the allowed media.
func setup(reader *gortsplib.Client, src *base.URL, audio bool) ([]*description.Media, error) {
	desc, _, err := reader.Describe(src)
	if err != nil {
		return nil, err
	}
	medias := Select(desc.Medias, audio)
	if len(medias) == 0 {
		return nil, errNoMedia
	}
	for _, m := range medias {
		if _, err := reader.Setup(desc.BaseURL, m, 0, 0); err != nil {
			return nil, err
		}
	}
	return medias, nil
}

// firstError waits until the reader or the publisher stops.
func firstError(reader, pub *gortsplib.Client) error {
	errc := make(chan error, 2)
	go func() { errc <- reader.Wait() }()
	go func() { errc <- pub.Wait() }()
	return <-errc
}

// quiet routes the client's diagnostics to debug logging instead of the
// standard logger; packet loss on a busy network is not news.
func quiet(c *gortsplib.Client, log *slog.Logger) {
	c.OnTransportSwitch = func(err error) { log.Debug("rtsp transport switch", "err", err) }
	c.OnPacketsLost = func(n uint64) { log.Debug("rtp packets lost", "count", n) }
	c.OnDecodeError = func(err error) { log.Debug("rtp decode error", "err", err) }
}

func ptr[T any](v T) *T { return &v }
