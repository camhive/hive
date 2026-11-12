package archive

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
)

// ActivityStepS is the width of one activity bucket in seconds.
const ActivityStepS = 10

// SilenceDB is the sound level of a silent bucket, and the floor of the scale.
const SilenceDB = -90

// Activity is a segment's profile for the timeline (ADR-0015): per
// ActivityStepS seconds from the segment's first video frame, the video's
// bit rate (a still scene compresses, movement does not) and, when the
// segment has LPCM audio, its loudness. Both are hints, not detections.
type Activity struct {
	StepS     int   `json:"step_s"`
	VideoKbps []int `json:"video_kbps"`
	SoundDB   []int `json:"sound_db,omitempty"` // dBFS, SilenceDB for silence; nil without audio
}

// ActivityOf profiles a fragmented MP4 segment up to its last whole
// fragment, so a file still being written yields what is already there.
func ActivityOf(data []byte) (Activity, error) {
	head, frags, err := splitFMP4(data)
	if err != nil {
		return Activity{}, err
	}
	moov, _ := find(head, "moov")
	video, audio, _ := pickTracks(moov)
	if video == nil {
		return Activity{}, errors.New("archive: no video track")
	}
	var parts fmp4.Parts
	if len(frags) > 0 {
		if err := parts.Unmarshal(frags); err != nil {
			return Activity{}, err
		}
	}
	a := Activity{StepS: ActivityStepS, VideoKbps: videoKbps(parts, video)}
	if audio != nil {
		a.SoundDB = soundDB(parts, audio, len(a.VideoKbps))
	}
	return a, nil
}

// splitFMP4 returns the boxes up to moov and the whole moof+mdat pairs after
// it; a trailing fragment without its mdat is left out.
func splitFMP4(data []byte) (head, frags []byte, err error) {
	if len(data) < 8 || string(data[4:8]) != "ftyp" {
		return nil, nil, errors.New("archive: not an MP4 file")
	}
	headEnd, end, pairEnd := -1, 0, 0
	for end+8 <= len(data) {
		size := int(binary.BigEndian.Uint32(data[end : end+4]))
		if size < 8 || end+size > len(data) {
			break
		}
		typ := string(data[end+4 : end+8])
		end += size
		switch {
		case typ == "moov":
			headEnd, pairEnd = end, end
		case typ == "mdat" && headEnd >= 0:
			pairEnd = end
		}
	}
	if headEnd < 0 {
		return nil, nil, errors.New("archive: no moov")
	}
	return data[:headEnd], data[headEnd:pairEnd], nil
}

// mediaTrack is what profiling needs of a track.
type mediaTrack struct {
	id        int
	timescale uint32
	little    bool // LPCM byte order
	channels  int  // LPCM channels
}

// pickTracks returns the first video track and the first 16-bit LPCM
// ("ipcm") audio track of a moov, and whether it has any sound track. It
// reads the boxes itself: the engine's segments carry user data general MP4
// readers refuse.
func pickTracks(moov []byte) (video, audio *mediaTrack, sound bool) {
	for _, b := range boxes(moov) {
		if b.typ != "trak" {
			continue
		}
		t, handler := trakInfo(b.payload)
		mt := &mediaTrack{id: int(t.id), timescale: t.timescale}
		switch {
		case handler == "vide" && video == nil && mt.timescale > 0:
			video = mt
		case handler == "soun":
			sound = true
			if little, channels, ok := pcm16(b.payload); ok && audio == nil && mt.timescale > 0 {
				mt.little, mt.channels = little, channels
				audio = mt
			}
		}
	}
	return video, audio, sound
}

// pcm16 reads a sound trak's sample entry: ok for 16-bit LPCM ("ipcm" with
// its pcmC box), whether the samples are little-endian, and the channels.
func pcm16(trak []byte) (little bool, channels int, ok bool) {
	children, channels, ok := ipcmEntry(trak)
	if !ok {
		return false, 0, false
	}
	pcmC, found := find(children, "pcmC")
	// pcmC: version/flags(4) format_flags(1) PCM_sample_size(1)
	if !found || len(pcmC) < 6 || pcmC[5] != 16 || channels == 0 {
		return false, 0, false
	}
	return pcmC[4]&1 != 0, channels, true
}

// ipcmEntry finds a trak's first sample entry if it is "ipcm" and returns
// its child boxes and channel count.
func ipcmEntry(trak []byte) (children []byte, channels int, ok bool) {
	stsd, found := descend(trak, "mdia", "minf", "stbl", "stsd")
	// stsd: version/flags(4) entry_count(4), then the first entry: size(4)
	// type(4) and a 28-byte AudioSampleEntry before its child boxes.
	if !found || len(stsd) < 8+8+28 || string(stsd[12:16]) != "ipcm" {
		return nil, 0, false
	}
	size := int(binary.BigEndian.Uint32(stsd[8:12]))
	if size < 8+28 || 8+size > len(stsd) {
		return nil, 0, false
	}
	channels = int(binary.BigEndian.Uint16(stsd[32:34])) // AudioSampleEntry: 16 reserved bytes, then channelcount
	return stsd[8+8+28 : 8+size], channels, true
}

// descend follows a path of child boxes.
func descend(payload []byte, path ...string) ([]byte, bool) {
	for _, typ := range path {
		next, ok := find(payload, typ)
		if !ok {
			return nil, false
		}
		payload = next
	}
	return payload, true
}

// timedSample is a sample with its time in seconds from the track's first sample.
type timedSample struct {
	at      float64
	payload []byte
}

// samplesOf lists a track's samples in order with their times.
func samplesOf(parts fmp4.Parts, tr *mediaTrack) (out []timedSample, end float64) {
	first, started := uint64(0), false
	for _, p := range parts {
		for _, pt := range p.Tracks {
			if pt.ID != tr.id {
				continue
			}
			if !started {
				first, started = pt.BaseTime, true
			}
			t := pt.BaseTime
			for _, s := range pt.Samples {
				out = append(out, timedSample{at: float64(t-first) / float64(tr.timescale), payload: s.Payload})
				t += uint64(s.Duration)
			}
			end = float64(t-first) / float64(tr.timescale)
		}
	}
	return out, end
}

// videoKbps is the video bit rate per bucket; the last bucket is averaged
// over the part of it the segment covers.
func videoKbps(parts fmp4.Parts, video *mediaTrack) []int {
	samples, end := samplesOf(parts, video)
	n := int(math.Ceil(end / ActivityStepS))
	if n <= 0 {
		return nil
	}
	byteSum := make([]float64, n)
	for _, s := range samples {
		byteSum[min(int(s.at/ActivityStepS), n-1)] += float64(len(s.payload))
	}
	out := make([]int, n)
	for k := range out {
		span := min(float64(ActivityStepS), end-float64(k*ActivityStepS))
		out[k] = int(math.Round(byteSum[k] * 8 / 1000 / span))
	}
	return out
}

// soundDB is the RMS level per bucket in dBFS, n buckets long.
func soundDB(parts fmp4.Parts, audio *mediaTrack, n int) []int {
	little := audio.little
	samples, _ := samplesOf(parts, audio)
	sumSq, count := make([]float64, n), make([]int, n)
	for _, s := range samples {
		k := int(s.at / ActivityStepS)
		if k >= n {
			break
		}
		for i := 0; i+1 < len(s.payload); i += 2 {
			v := sample16(s.payload[i:], little)
			sumSq[k] += v * v
			count[k]++
		}
	}
	out := make([]int, n)
	for k := range out {
		out[k] = level(sumSq[k], count[k])
	}
	return out
}

// sample16 reads one signed 16-bit PCM sample.
func sample16(b []byte, little bool) float64 {
	u := binary.BigEndian.Uint16(b)
	if little {
		u = binary.LittleEndian.Uint16(b)
	}
	return float64(int16(u)) //nolint:gosec // two's complement: the bits are a signed sample
}

// level turns a sum of squared 16-bit samples into dBFS, floored at SilenceDB.
func level(sumSq float64, count int) int {
	if count == 0 || sumSq == 0 {
		return SilenceDB
	}
	db := 20 * math.Log10(math.Sqrt(sumSq/float64(count))/32768)
	return max(SilenceDB, int(math.Round(db)))
}
