package archive_test

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"

	"github.com/camhive/hive/internal/archive"
)

// pcm returns n big-endian 16-bit samples of a square wave of the given
// amplitude (its RMS is the amplitude).
func pcm(n int, amplitude int16) []byte {
	out := make([]byte, 2*n)
	for i := range n {
		v := amplitude
		if i%2 == 1 {
			v = -amplitude
		}
		binary.BigEndian.PutUint16(out[2*i:], uint16(v))
	}
	return out
}

// withEngineUserData adds what MediaMTX puts in every segment's moov: a udta
// with its own "mtxi" box, which general MP4 readers do not know.
func withEngineUserData(init []byte) []byte {
	mtxi := append([]byte{0, 0, 0, 20, 'm', 't', 'x', 'i'}, make([]byte, 12)...)
	udta := append([]byte{0, 0, 0, byte(8 + len(mtxi)), 'u', 'd', 't', 'a'}, mtxi...)
	ftypSize := int(binary.BigEndian.Uint32(init[:4]))
	moov := append(append([]byte(nil), init[ftypSize:]...), udta...)
	binary.BigEndian.PutUint32(moov[:4], uint32(len(moov)))
	return append(append([]byte(nil), init[:ftypSize]...), moov...)
}

// segmentWith builds 25 s of footage: video at 1 frame per second whose
// frames are 1000 bytes for the first 10 s, 5000 bytes for the next 10 s
// and 0 after, and 8 kHz audio at half scale for 10 s, then silence.
func segmentWith(t *testing.T, audio bool) []byte {
	t.Helper()
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{{ID: 1, TimeScale: 90000, Codec: &codecs.H264{SPS: testSPS, PPS: []byte{0x08}}}}}
	if audio {
		init.Tracks = append(init.Tracks, &fmp4.InitTrack{ID: 2, TimeScale: 8000, Codec: &codecs.LPCM{BitDepth: 16, SampleRate: 8000, ChannelCount: 1}})
	}
	var ib seekablebuffer.Buffer
	if err := init.Marshal(&ib); err != nil {
		t.Fatal(err)
	}
	var buf seekablebuffer.Buffer
	_, _ = buf.Write(withEngineUserData(ib.Bytes()))
	const base = 123456 // segments do not start at time 0
	for sec := range 25 {
		size := 0
		switch {
		case sec < 10:
			size = 1000
		case sec < 20:
			size = 5000
		}
		part := fmp4.Part{SequenceNumber: uint32(sec), Tracks: []*fmp4.PartTrack{{ID: 1, BaseTime: base + uint64(sec)*90000,
			Samples: []*fmp4.Sample{{Duration: 90000, IsNonSyncSample: sec > 0, Payload: make([]byte, size+5)}}}}}
		if audio {
			amp := int16(0)
			if sec < 10 {
				amp = 16384
			}
			part.Tracks = append(part.Tracks, &fmp4.PartTrack{ID: 2, BaseTime: uint64(sec) * 8000,
				Samples: []*fmp4.Sample{{Duration: 8000, Payload: pcm(8000, amp)}}})
		}
		if err := part.Marshal(&buf); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// ADR-0015: per 10 s, the video's bytes per second (a still scene
// compresses, movement does not) and the sound level in dBFS.
func TestActivityProfilesVideoBytesAndSoundLevel(t *testing.T) {
	a, err := archive.ActivityOf(segmentWith(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if a.StepS != 10 || len(a.VideoKbps) != 3 || len(a.SoundDB) != 3 {
		t.Fatalf("profile shape: %+v", a)
	}
	// 1005 bytes/s = 8.04 kbit/s; 5005 bytes/s = 40.04 kbit/s; the last
	// bucket holds 5 frames of 5 bytes over 5 s.
	if a.VideoKbps[0] != 8 || a.VideoKbps[1] != 40 || a.VideoKbps[2] != 0 {
		t.Fatalf("video kbit/s = %v", a.VideoKbps)
	}
	// Half scale is -6 dBFS; silence is the floor.
	if a.SoundDB[0] != -6 || a.SoundDB[1] != archive.SilenceDB || a.SoundDB[2] != archive.SilenceDB {
		t.Fatalf("sound dBFS = %v", a.SoundDB)
	}
}

func TestActivityWithoutAudioHasNoSoundLevel(t *testing.T) {
	a, err := archive.ActivityOf(segmentWith(t, false))
	if err != nil || len(a.VideoKbps) != 3 || a.SoundDB != nil {
		t.Fatalf("profile = %+v, %v", a, err)
	}
}

// A segment still being written is profiled up to its last whole fragment.
func TestActivityOfATruncatedSegment(t *testing.T) {
	data := segmentWith(t, true)
	a, err := archive.ActivityOf(data[:len(data)-100])
	if err != nil || len(a.VideoKbps) != 3 {
		t.Fatalf("profile = %+v, %v", a, err)
	}
	if _, err := archive.ActivityOf([]byte("not mp4")); err == nil {
		t.Fatal("junk profiled")
	}
}

// The index keeps each segment's profile, so the timeline gets it without
// reading files.
func TestReconcileStoresTheActivityProfile(t *testing.T) {
	s := openStore(t)
	idx := archive.NewIndex(s)
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "cam_a", "main")
	_ = os.MkdirAll(dir, 0o750)
	for _, f := range []string{"2026-11-09_10-00-00-000000.mp4", "2026-11-09_10-10-00-000000.mp4"} {
		_ = os.WriteFile(filepath.Join(dir, f), segmentWith(t, true), 0o600)
	}
	if _, err := idx.Reconcile(ctx, root); err != nil {
		t.Fatal(err)
	}
	segs, _ := idx.Range(ctx, "cam_a", "main", time.Time{}, time.Now().Add(24*time.Hour))
	if len(segs) != 2 || segs[0].Duration != 25*time.Second || segs[0].Activity == nil {
		t.Fatalf("segments = %+v", segs)
	}
	if a := segs[0].Activity; a.VideoKbps[1] != 40 || a.SoundDB[0] != -6 {
		t.Fatalf("stored profile = %+v", a)
	}
}

// Only 16-bit LPCM is measured; other audio leaves the sound level out, and
// little-endian samples read the same as big-endian ones.
func TestActivitySoundLevelByAudioFormat(t *testing.T) {
	for _, c := range []struct {
		name  string
		codec codecs.Codec
		want  bool
	}{
		{"LPCM little-endian", &codecs.LPCM{LittleEndian: true, BitDepth: 16, SampleRate: 8000, ChannelCount: 1}, true},
		{"LPCM 24-bit", &codecs.LPCM{BitDepth: 24, SampleRate: 8000, ChannelCount: 1}, false},
		{"AAC", &codecs.MPEG4Audio{Config: mpeg4audio.AudioSpecificConfig{Type: mpeg4audio.ObjectTypeAACLC, SampleRate: 8000, ChannelConfig: 1}}, false},
	} {
		init := fmp4.Init{Tracks: []*fmp4.InitTrack{
			{ID: 1, TimeScale: 90000, Codec: &codecs.H264{SPS: testSPS, PPS: []byte{0x08}}},
			{ID: 2, TimeScale: 8000, Codec: c.codec},
		}}
		var buf seekablebuffer.Buffer
		if err := init.Marshal(&buf); err != nil {
			t.Fatal(err)
		}
		payload := pcm(8000, 16384)
		if l, ok := c.codec.(*codecs.LPCM); ok && l.LittleEndian {
			for i := 0; i+1 < len(payload); i += 2 {
				payload[i], payload[i+1] = payload[i+1], payload[i]
			}
		}
		part := fmp4.Part{Tracks: []*fmp4.PartTrack{
			{ID: 1, Samples: []*fmp4.Sample{{Duration: 90000, Payload: make([]byte, 100)}}},
			{ID: 2, Samples: []*fmp4.Sample{{Duration: 8000, Payload: payload}}},
		}}
		if err := part.Marshal(&buf); err != nil {
			t.Fatal(err)
		}
		a, err := archive.ActivityOf(buf.Bytes())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := a.SoundDB != nil; got != c.want || (c.want && a.SoundDB[0] != -6) {
			t.Errorf("%s: sound = %v", c.name, a.SoundDB)
		}
	}
	if _, err := archive.ActivityOf([]byte("\x00\x00\x00\x08ftyp")); err == nil {
		t.Error("a file without moov profiled")
	}
}
