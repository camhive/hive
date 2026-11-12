package archive_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"

	"github.com/camhive/hive/internal/archive"
)

var testSPS = []byte{
	0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02,
	0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04,
	0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9,
	0x20,
}

// recording is fragmented MP4 as the engine writes it for a camera with
// G.711 audio: an H.264 track and an LPCM ("ipcm") track, two parts.
func recording(t *testing.T, withAudio bool) []byte {
	t.Helper()
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{{ID: 1, TimeScale: 90000, Codec: &codecs.H264{SPS: testSPS, PPS: []byte{0x08}}}}}
	if withAudio {
		init.Tracks = append(init.Tracks, &fmp4.InitTrack{ID: 2, TimeScale: 8000, Codec: &codecs.LPCM{BitDepth: 16, SampleRate: 8000, ChannelCount: 1}})
	}
	var buf seekablebuffer.Buffer
	if err := init.Marshal(&buf); err != nil {
		t.Fatal(err)
	}
	out := append([]byte(nil), buf.Bytes()...)
	for seq := range 2 {
		part := fmp4.Part{SequenceNumber: uint32(seq), Tracks: []*fmp4.PartTrack{{ID: 1, BaseTime: uint64(seq) * 90000, Samples: []*fmp4.Sample{
			{Duration: 45000, Payload: []byte{0, 0, 0, 1, 0x65, byte(seq)}},
			{Duration: 45000, IsNonSyncSample: true, Payload: []byte{0, 0, 0, 1, 0x41, byte(seq)}},
		}}}}
		if withAudio {
			part.Tracks = append(part.Tracks, &fmp4.PartTrack{ID: 2, BaseTime: uint64(seq) * 8000, Samples: []*fmp4.Sample{{Duration: 8000, Payload: bytes.Repeat([]byte{0xaa}, 64)}}})
		}
		var pb seekablebuffer.Buffer
		if err := part.Marshal(&pb); err != nil {
			t.Fatal(err)
		}
		out = append(out, pb.Bytes()...)
	}
	return out
}

func playable(t *testing.T, in []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := archive.CopyPlayable(&out, bytes.NewReader(in)); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Browsers reject the whole file when one track is a format they cannot
// decode (Chrome: "Unsupported audio format ipcm"), so playback keeps the
// video tracks and drops the rest; the samples are untouched.
func TestCopyPlayableKeepsOnlyVideo(t *testing.T) {
	got := playable(t, recording(t, true))
	var init fmp4.Init
	if err := init.Unmarshal(bytes.NewReader(got)); err != nil {
		t.Fatal(err)
	}
	if len(init.Tracks) != 1 || init.Tracks[0].ID != 1 {
		t.Fatalf("tracks: %+v", init.Tracks)
	}
	var parts fmp4.Parts
	if err := parts.Unmarshal(got); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts: %d", len(parts))
	}
	for i, p := range parts {
		if len(p.Tracks) != 1 || p.Tracks[0].ID != 1 || len(p.Tracks[0].Samples) != 2 || p.Tracks[0].BaseTime != uint64(i)*90000 {
			t.Fatalf("part %d: %+v", i, p.Tracks)
		}
		if s := p.Tracks[0].Samples[1]; !s.IsNonSyncSample || !bytes.Equal(s.Payload, []byte{0, 0, 0, 1, 0x41, byte(i)}) {
			t.Fatalf("part %d sample: %+v", i, s)
		}
	}
}

func TestCopyPlayablePassesVideoOnlyAndUnknownDataThrough(t *testing.T) {
	video := recording(t, false)
	if got := playable(t, video); !bytes.Equal(got, video) {
		t.Fatal("a video-only recording changed")
	}
	junk := []byte("\x00\x00\x00\x18ftypiso5 not a real file")
	if got := playable(t, junk); !bytes.Equal(got, junk) {
		t.Fatalf("unparseable data changed: %q", got)
	}
}

// A truncated answer (the engine stopped mid-part) ends the output after the
// last whole box; nothing half-written follows it.
func TestCopyPlayableStopsAtATruncatedBox(t *testing.T) {
	full := recording(t, true)
	got := playable(t, full[:len(full)-10])
	var parts fmp4.Parts
	if err := parts.Unmarshal(got); err != nil {
		t.Fatalf("output is not whole boxes: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("parts: %d", len(parts))
	}
	_ = io.Discard
}

// Boxes after the init segment arrive in every size form MP4 allows: a
// 64-bit size, "to the end of the file", and a size no box can have, which
// ends the output.
func TestCopyPlayableReadsEveryBoxSizeForm(t *testing.T) {
	rec := recording(t, true)
	large := append([]byte{0, 0, 0, 1, 'f', 'r', 'e', 'e', 0, 0, 0, 0, 0, 0, 0, 20}, "xyzw"...)
	toEnd := append([]byte{0, 0, 0, 0, 's', 'k', 'i', 'p'}, "rest"...)
	got := playable(t, append(append(append([]byte(nil), rec...), large...), toEnd...))
	if !bytes.HasSuffix(got, append(append([]byte(nil), large...), toEnd...)) {
		t.Fatalf("64-bit and to-the-end boxes not passed on: %q", got[len(got)-30:])
	}
	bad := []byte{0, 0, 0, 4, 'f', 'r', 'e', 'e'}
	cut := playable(t, append(append([]byte(nil), rec...), bad...))
	if bytes.Contains(cut[len(cut)-8:], bad) {
		t.Fatal("a box with an impossible size was passed on")
	}
}
