package archive

import (
	"encoding/binary"
	"errors"
	"time"
)

// FMP4Duration computes the duration of a fragmented MP4 from its video
// track: the sample durations of that track in every complete moof (trun
// entries, falling back to tfhd and trex defaults) at its timescale. Other
// tracks (camera audio) are ignored; a file without video falls back to its
// first track. Truncated trailing data is ignored, so a file still being
// written yields the duration of what is already there.
func FMP4Duration(data []byte) (time.Duration, error) {
	if len(data) < 8 || string(data[4:8]) != "ftyp" {
		return 0, errors.New("archive: not an MP4 file")
	}
	var (
		tr    track
		total uint64
	)
	for _, b := range boxes(data) {
		switch b.typ {
		case "moov":
			tr = timedTrack(b.payload)
		case "moof":
			total += parseMoof(b.payload, tr)
		}
	}
	if tr.timescale == 0 {
		return 0, errors.New("archive: no timescale in moov")
	}
	return time.Duration(float64(total) / float64(tr.timescale) * float64(time.Second)), nil
}

// track is what timing a segment needs of one track.
type track struct {
	id         uint32
	timescale  uint32
	defaultDur uint32 // from trex; 0 when absent
}

type mp4box struct {
	typ     string
	payload []byte
}

// boxes splits data into top-level boxes, stopping at the first truncated one.
func boxes(data []byte) []mp4box {
	var out []mp4box
	for len(data) >= 8 {
		size := int(binary.BigEndian.Uint32(data[:4]))
		if size < 8 || size > len(data) {
			break
		}
		out = append(out, mp4box{typ: string(data[4:8]), payload: data[8:size]})
		data = data[size:]
	}
	return out
}

// find returns the payload of the first child box with the given type.
func find(payload []byte, typ string) ([]byte, bool) {
	for _, b := range boxes(payload) {
		if b.typ == typ {
			return b.payload, true
		}
	}
	return nil, false
}

// timedTrack picks the track that times the segment: the first video track
// (handler "vide"), else the first track.
func timedTrack(moov []byte) track {
	var first, video *track
	for _, b := range boxes(moov) {
		if b.typ != "trak" {
			continue
		}
		t, handler := trakInfo(b.payload)
		if first == nil {
			first = &t
		}
		if handler == "vide" {
			video = &t
			break
		}
	}
	pick := first
	if video != nil {
		pick = video
	}
	if pick == nil {
		return track{}
	}
	pick.defaultDur = trexDefaultDuration(moov, pick.id)
	return *pick
}

// trakInfo reads a trak's ID (tkhd), timescale (mdhd) and handler type (hdlr).
func trakInfo(trak []byte) (track, string) {
	var t track
	if tkhd, ok := find(trak, "tkhd"); ok {
		t.id = versioned(tkhd, 12, 20)
	}
	mdia, ok := find(trak, "mdia")
	if !ok {
		return t, ""
	}
	if mdhd, ok := find(mdia, "mdhd"); ok {
		t.timescale = versioned(mdhd, 12, 20)
	}
	handler := ""
	if hdlr, ok := find(mdia, "hdlr"); ok && len(hdlr) >= 12 {
		handler = string(hdlr[8:12])
	}
	return t, handler
}

// versioned reads a 32-bit field of a full box whose offset depends on the
// version (v0: 32-bit times before it, v1: 64-bit); 0 when the box is short.
func versioned(box []byte, v0, v1 int) uint32 {
	off := v0
	if len(box) > 0 && box[0] == 1 {
		off = v1
	}
	if len(box) < off+4 {
		return 0
	}
	return binary.BigEndian.Uint32(box[off : off+4])
}

// trexDefaultDuration is the default sample duration trex declares for the
// track (0 when absent).
func trexDefaultDuration(moov []byte, id uint32) uint32 {
	mvex, ok := find(moov, "mvex")
	if !ok {
		return 0
	}
	for _, b := range boxes(mvex) {
		if b.typ == "trex" && len(b.payload) >= 16 && binary.BigEndian.Uint32(b.payload[4:8]) == id {
			return binary.BigEndian.Uint32(b.payload[12:16])
		}
	}
	return 0
}

// parseMoof sums the durations of the track's samples in one fragment.
func parseMoof(moof []byte, tr track) uint64 {
	var total uint64
	for _, b := range boxes(moof) {
		if b.typ == "traf" {
			total += parseTraf(b.payload, tr)
		}
	}
	return total
}

// tfhdDefaultDuration is the default sample duration a tfhd declares, or
// fallback (the trex default) when it declares none.
func tfhdDefaultDuration(tfhd []byte, fallback uint32) uint32 {
	flags := binary.BigEndian.Uint32(tfhd[:4]) & 0xffffff
	off := 8 // version/flags + track_ID
	if flags&0x1 != 0 {
		off += 8 // base_data_offset
	}
	if flags&0x2 != 0 {
		off += 4 // sample_description_index
	}
	if flags&0x8 != 0 && len(tfhd) >= off+4 { // default-sample-duration-present
		return binary.BigEndian.Uint32(tfhd[off : off+4])
	}
	return fallback
}

// parseTraf sums one traf's sample durations if it belongs to the track.
func parseTraf(traf []byte, tr track) uint64 {
	tfhd, ok := find(traf, "tfhd")
	if !ok || len(tfhd) < 8 || binary.BigEndian.Uint32(tfhd[4:8]) != tr.id {
		return 0
	}
	defaultDur := tfhdDefaultDuration(tfhd, tr.defaultDur)
	var total uint64
	for _, b := range boxes(traf) {
		if b.typ == "trun" {
			total += parseTrun(b.payload, defaultDur)
		}
	}
	return total
}

// parseTrun sums sample durations of one run.
func parseTrun(trun []byte, defaultDur uint32) uint64 {
	if len(trun) < 8 {
		return 0
	}
	flags := binary.BigEndian.Uint32(trun[:4]) & 0xffffff
	count := binary.BigEndian.Uint32(trun[4:8])
	off := 8
	if flags&0x1 != 0 {
		off += 4 // data_offset
	}
	if flags&0x4 != 0 {
		off += 4 // first_sample_flags
	}
	perSample := 0
	for _, bit := range []uint32{0x100, 0x200, 0x400, 0x800} { // duration, size, flags, cts offset
		if flags&bit != 0 {
			perSample += 4
		}
	}
	if flags&0x100 == 0 {
		return uint64(count) * uint64(defaultDur)
	}
	var total uint64
	for k := uint32(0); k < count; k++ {
		p := off + int(k)*perSample
		if p+4 > len(trun) {
			break
		}
		total += uint64(binary.BigEndian.Uint32(trun[p : p+4]))
	}
	return total
}
