package archive_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"

	"github.com/camhive/hive/internal/archive"
)

func avFMP4(t *testing.T, audio codecs.Codec, payloads ...[]byte) []byte {
	t.Helper()
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{{ID: 1, TimeScale: 90000, Codec: &codecs.H264{SPS: testSPS, PPS: []byte{0x08}}}}}
	if audio != nil {
		init.Tracks = append(init.Tracks, &fmp4.InitTrack{ID: 2, TimeScale: 8000, Codec: audio})
	}
	var buf seekablebuffer.Buffer
	if err := init.Marshal(&buf); err != nil {
		t.Fatal(err)
	}
	for k, p := range payloads {
		part := fmp4.Part{SequenceNumber: uint32(k), Tracks: []*fmp4.PartTrack{{ID: 1, BaseTime: uint64(k) * 90000, Samples: []*fmp4.Sample{{Duration: 90000, Payload: []byte{0, 0, 0, 1, 0x65}}}}}}
		if audio != nil {
			part.Tracks = append(part.Tracks, &fmp4.PartTrack{ID: 2, BaseTime: uint64(k) * 8000, Samples: []*fmp4.Sample{{Duration: uint32(len(p) / 2), Payload: p}}})
		}
		if err := part.Marshal(&buf); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

// ADR-0015: recorded sound plays next to the video as WAV, which every
// browser decodes; the engine's big-endian LPCM becomes WAV's little-endian.
func TestAudioWAVFromTheLPCMTrack(t *testing.T) {
	a, b := []byte{0x12, 0x34, 0xff, 0xfe}, []byte{0x00, 0x01}
	wav, err := archive.AudioWAV(bytes.NewReader(avFMP4(t, &codecs.LPCM{BitDepth: 16, SampleRate: 8000, ChannelCount: 1}, a, b)))
	if err != nil {
		t.Fatal(err)
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[12:16]) != "fmt " || string(wav[36:40]) != "data" {
		t.Fatalf("not a WAV header: %q", wav[:44])
	}
	le := binary.LittleEndian
	if le.Uint16(wav[20:]) != 1 || le.Uint16(wav[22:]) != 1 || le.Uint32(wav[24:]) != 8000 || le.Uint16(wav[34:]) != 16 {
		t.Fatalf("format: pcm=%d ch=%d rate=%d bits=%d", le.Uint16(wav[20:]), le.Uint16(wav[22:]), le.Uint32(wav[24:]), le.Uint16(wav[34:]))
	}
	if le.Uint32(wav[40:]) != 6 || le.Uint32(wav[4:]) != uint32(len(wav)-8) {
		t.Fatalf("sizes: data=%d riff=%d len=%d", le.Uint32(wav[40:]), le.Uint32(wav[4:]), len(wav))
	}
	if got := wav[44:]; !bytes.Equal(got, []byte{0x34, 0x12, 0xfe, 0xff, 0x01, 0x00}) {
		t.Fatalf("samples: % x", got)
	}
}

func TestAudioWAVWithoutPlayableSound(t *testing.T) {
	if _, err := archive.AudioWAV(bytes.NewReader(avFMP4(t, nil, nil))); !errors.Is(err, archive.ErrNoAudio) {
		t.Errorf("video only: %v", err)
	}
	aac := &codecs.MPEG4Audio{Config: mpeg4audio.AudioSpecificConfig{Type: mpeg4audio.ObjectTypeAACLC, SampleRate: 8000, ChannelConfig: 1}}
	if _, err := archive.AudioWAV(bytes.NewReader(avFMP4(t, aac, []byte{1, 2}))); !errors.Is(err, archive.ErrAudioUnsupported) {
		t.Errorf("AAC: %v", err)
	}
	if _, err := archive.AudioWAV(bytes.NewReader([]byte("junk"))); err == nil || errors.Is(err, archive.ErrNoAudio) {
		t.Errorf("junk: %v", err)
	}
}
