package archive

import (
	amp4 "github.com/abema/go-mp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
)

// mtxi is the box the engine (MediaMTX) writes into every segment's
// moov/udta: which recording the segment belongs to, its number within it,
// and the decode time it starts at. Consecutive segments of one recording
// join without a gap; their decode times say where each one starts.
type mtxi struct {
	amp4.FullBox  `mp4:"0,extend"`
	StreamID      [16]byte `mp4:"1,size=8"`
	SegmentNumber uint64   `mp4:"2,size=64"`
	DTS           int64    `mp4:"3,size=64"` // nanoseconds
	NTP           int64    `mp4:"4,size=64"`
}

// GetType implements amp4.IBox.
func (*mtxi) GetType() amp4.BoxType { return amp4.StrToBoxType("mtxi") }

// The box must be known to the MP4 reader, or segment headers do not parse.
func init() { amp4.AddBoxDef(&mtxi{}, 0) } //nolint:gochecknoinits // registry of the MP4 library

// segmentInfo returns the segment's mtxi box, nil when it has none.
func segmentInfo(init *fmp4.Init) *mtxi {
	for _, box := range init.UserData {
		if m, ok := box.(*mtxi); ok {
			return m
		}
	}
	return nil
}
