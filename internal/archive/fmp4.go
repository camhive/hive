package archive

import (
	"encoding/binary"
	"errors"
	"time"
)

// FMP4Duration computes the duration of a fragmented MP4 by summing sample
// durations from every complete moof (trun entries, falling back to tfhd and
// trex defaults) at the first video track's timescale. Truncated trailing
// data is ignored, so a file still being written yields the duration of what
// is already there.
func FMP4Duration(data []byte) (time.Duration, error) {
	if len(data) < 8 || string(data[4:8]) != "ftyp" {
		return 0, errors.New("archive: not an MP4 file")
	}
	var (
		timescale  uint32
		defaultDur uint32 // from trex
		total      uint64
	)
	for _, b := range boxes(data) {
		switch b.typ {
		case "moov":
			timescale, defaultDur = parseMoov(b.payload)
		case "moof":
			total += parseMoof(b.payload, defaultDur)
		}
	}
	if timescale == 0 {
		return 0, errors.New("archive: no timescale in moov")
	}
	return time.Duration(float64(total) / float64(timescale) * float64(time.Second)), nil
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

// parseMoov returns the first track's mdhd timescale and the trex default
// sample duration (0 when absent).
func parseMoov(moov []byte) (timescale, defaultDur uint32) {
	return mdhdTimescale(moov), trexDefaultDuration(moov)
}

func mdhdTimescale(moov []byte) uint32 {
	trak, ok := find(moov, "trak")
	if !ok {
		return 0
	}
	mdia, ok := find(trak, "mdia")
	if !ok {
		return 0
	}
	mdhd, ok := find(mdia, "mdhd")
	switch {
	case !ok || len(mdhd) < 16:
		return 0
	case mdhd[0] == 1 && len(mdhd) >= 24: // version 1: 64-bit times
		return binary.BigEndian.Uint32(mdhd[20:24])
	default:
		return binary.BigEndian.Uint32(mdhd[12:16])
	}
}

func trexDefaultDuration(moov []byte) uint32 {
	mvex, ok := find(moov, "mvex")
	if !ok {
		return 0
	}
	trex, ok := find(mvex, "trex")
	if !ok || len(trex) < 16 {
		return 0
	}
	return binary.BigEndian.Uint32(trex[12:16])
}

// parseMoof sums the durations of all samples in the fragment's first traf.
func parseMoof(moof []byte, trexDefault uint32) uint64 {
	traf, ok := find(moof, "traf")
	if !ok {
		return 0
	}
	defaultDur := trexDefault
	if tfhd, ok := find(traf, "tfhd"); ok && len(tfhd) >= 8 {
		flags := binary.BigEndian.Uint32(tfhd[:4]) & 0xffffff
		off := 8 // version/flags + track_ID
		if flags&0x1 != 0 {
			off += 8 // base_data_offset
		}
		if flags&0x2 != 0 {
			off += 4 // sample_description_index
		}
		if flags&0x8 != 0 && len(tfhd) >= off+4 { // default-sample-duration-present
			defaultDur = binary.BigEndian.Uint32(tfhd[off : off+4])
		}
	}
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
