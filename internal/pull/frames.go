package pull

import (
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/pion/rtp"
)

// FrameFunc is told about each video access unit the pull forwards: when it
// arrived, its size in bytes and whether it is a keyframe (ADR-0020).
type FrameFunc func(at time.Time, size int, key bool)

// frameReader reassembles one video media's RTP packets into access units
// and reports their sizes. It reads NAL unit lengths and headers only.
type frameReader struct {
	decode func(*rtp.Packet) ([][]byte, error)
	key    func([][]byte) bool
	emit   FrameFunc
}

// newFrameReader returns a reader for H.264 and H.265, nil for anything else.
func newFrameReader(f format.Format, emit FrameFunc) *frameReader {
	switch f := f.(type) {
	case *format.H264:
		if d, err := f.CreateDecoder(); err == nil {
			return &frameReader{decode: d.Decode, key: h264.IsRandomAccess, emit: emit}
		}
	case *format.H265:
		if d, err := f.CreateDecoder(); err == nil {
			return &frameReader{decode: d.Decode, key: h265.IsRandomAccess, emit: emit}
		}
	}
	return nil
}

// packet consumes one RTP packet; a completed access unit is reported.
// Incomplete or damaged units are skipped: the next one starts afresh.
func (r *frameReader) packet(pkt *rtp.Packet) {
	au, err := r.decode(pkt)
	if err != nil {
		return
	}
	size := 0
	for _, nalu := range au {
		size += len(nalu)
	}
	r.emit(time.Now(), size, r.key(au))
}

// videoFrames finds the first video media among medias that can be read and
// returns it with its reader; nil when there is none or emit is nil.
func videoFrames(medias []*description.Media, emit FrameFunc) (*description.Media, *frameReader) {
	if emit == nil {
		return nil, nil
	}
	for _, m := range medias {
		if m.Type != description.MediaTypeVideo {
			continue
		}
		for _, f := range m.Formats {
			if r := newFrameReader(f, emit); r != nil {
				return m, r
			}
		}
	}
	return nil, nil
}
