package archive

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
)

// ErrNoAudio means the footage has no sound track.
var ErrNoAudio = errors.New("archive: no audio")

// ErrAudioUnsupported means the footage has sound in a format the hive does
// not turn into WAV (only 16-bit LPCM, which is how the engine stores G.711).
var ErrAudioUnsupported = errors.New("archive: audio format not supported")

// AudioWAV reads fragmented MP4 (the engine's playback answer) and returns
// its 16-bit LPCM track as a WAV file (ADR-0015): browsers refuse LPCM in MP4
// but all of them play WAV. The answer is built in memory, so callers keep
// spans short (a minute of G.711 is about 1 MB).
func AudioWAV(r io.Reader) ([]byte, error) {
	audio, err := audioTrack(r)
	if err != nil {
		return nil, err
	}
	pcm, err := readPCM(r, audio.id)
	if err != nil {
		return nil, err
	}
	if !audio.little {
		swap16(pcm)
	}
	return wav(pcm, audio.timescale, audio.channels), nil
}

// audioTrack reads the header and returns its 16-bit LPCM track.
func audioTrack(r io.Reader) (*mediaTrack, error) {
	head, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	moov, _ := find(head, "moov")
	_, audio, sound := pickTracks(moov)
	switch {
	case audio != nil:
		return audio, nil
	case sound:
		return nil, ErrAudioUnsupported
	default:
		return nil, ErrNoAudio
	}
}

// readPCM collects one track's samples from the fragments that follow,
// up to the end of the input or a cut-off fragment.
func readPCM(r io.Reader, id int) ([]byte, error) {
	var pcm []byte
	for {
		moof, typ, err := readBox(r)
		if err != nil {
			return pcm, endOfInput(err)
		}
		if typ != "moof" {
			continue
		}
		mdat, _, err := readBox(r)
		if err != nil {
			return pcm, endOfInput(err) // a cut-off fragment: what came before is the answer
		}
		var parts fmp4.Parts
		if parts.Unmarshal(append(moof, mdat...)) != nil {
			continue
		}
		pcm = appendTrackSamples(pcm, parts, id)
	}
}

// appendTrackSamples appends the payloads of one track's samples.
func appendTrackSamples(dst []byte, parts fmp4.Parts, id int) []byte {
	for _, p := range parts {
		for _, t := range p.Tracks {
			if t.ID == id {
				for _, s := range t.Samples {
					dst = append(dst, s.Payload...)
				}
			}
		}
	}
	return dst
}

// swap16 turns big-endian 16-bit samples into little-endian in place.
func swap16(b []byte) {
	for i := 0; i+1 < len(b); i += 2 {
		b[i], b[i+1] = b[i+1], b[i]
	}
}

// wav wraps little-endian 16-bit PCM in a RIFF/WAVE header.
func wav(pcm []byte, rate uint32, channels int) []byte {
	le := binary.LittleEndian
	blockAlign := uint16(channels * 2) //nolint:gosec // channels comes from a 16-bit field
	out := make([]byte, 44, 44+len(pcm))
	copy(out[0:], "RIFF")
	le.PutUint32(out[4:], uint32(36+len(pcm))) //nolint:gosec // spans are minutes long, far below 4 GB
	copy(out[8:], "WAVEfmt ")
	le.PutUint32(out[16:], 16)               // fmt chunk size
	le.PutUint16(out[20:], 1)                // PCM
	le.PutUint16(out[22:], uint16(channels)) //nolint:gosec // as above
	le.PutUint32(out[24:], rate)
	le.PutUint32(out[28:], rate*uint32(blockAlign))
	le.PutUint16(out[32:], blockAlign)
	le.PutUint16(out[34:], 16)
	copy(out[36:], "data")
	le.PutUint32(out[40:], uint32(len(pcm))) //nolint:gosec // as above
	return append(out, pcm...)
}
