package archive_test

import (
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"

	"github.com/camhive/hive/internal/archive"
)

// A segment's length is its video's length. Seen on the dev stack: with a
// camera's audio track in the file, 10 s segments were counted as 7.3 s and
// the timeline showed gaps between every segment.
func TestFMP4DurationCountsTheVideoTrackWhateverComesFirst(t *testing.T) {
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{
		{ID: 1, TimeScale: 8000, Codec: &codecs.LPCM{BitDepth: 16, SampleRate: 8000, ChannelCount: 1}},
		{ID: 2, TimeScale: 90000, Codec: &codecs.H264{SPS: testSPS, PPS: []byte{0x08}}},
	}}
	var buf seekablebuffer.Buffer
	if err := init.Marshal(&buf); err != nil {
		t.Fatal(err)
	}
	audio := func(base uint64) *fmp4.PartTrack {
		return &fmp4.PartTrack{ID: 1, BaseTime: base, Samples: []*fmp4.Sample{{Duration: 8000, Payload: make([]byte, 32)}}}
	}
	video := func(base uint64) *fmp4.PartTrack {
		return &fmp4.PartTrack{ID: 2, BaseTime: base, Samples: []*fmp4.Sample{
			{Duration: 45000, Payload: []byte{0, 0, 0, 1, 0x65}}, {Duration: 45000, IsNonSyncSample: true, Payload: []byte{0, 0, 0, 1, 0x41}},
		}}
	}
	parts := []fmp4.Part{
		{SequenceNumber: 0, Tracks: []*fmp4.PartTrack{audio(0), video(0)}},
		{SequenceNumber: 1, Tracks: []*fmp4.PartTrack{audio(8000)}}, // audio arrived ahead of video
		{SequenceNumber: 2, Tracks: []*fmp4.PartTrack{video(90000), audio(16000)}},
	}
	for _, p := range parts {
		if err := p.Marshal(&buf); err != nil {
			t.Fatal(err)
		}
	}
	got, err := archive.FMP4Duration(buf.Bytes())
	if err != nil || got != 2*time.Second {
		t.Fatalf("duration = %v, %v; want 2s", got, err)
	}
}
