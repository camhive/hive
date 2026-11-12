package archive

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
)

// CopyPlayable copies fragmented MP4 from r to w keeping only its video
// tracks. Browsers refuse a whole file when one track is a format they
// cannot decode, and the engine records camera audio as it arrives (G.711
// becomes LPCM, "ipcm", which no browser plays), so archive playback would
// show nothing at all. Exports keep every track; this is for playback.
//
// Input that is not fragmented MP4, or has nothing to drop, is copied
// unchanged. Output ends after the last whole box when the input is cut off.
func CopyPlayable(w io.Writer, r io.Reader) error {
	var head bytes.Buffer
	keep, init, err := readInit(io.TeeReader(r, &head))
	if err != nil || keep == nil {
		return passThrough(w, head.Bytes(), r)
	}
	if _, err := w.Write(init); err != nil {
		return err
	}
	for {
		box, typ, err := readBox(r)
		if err != nil {
			return endOfInput(err)
		}
		if typ == "moof" {
			mdat, _, err := readBox(r)
			if err != nil {
				return endOfInput(err)
			}
			box = filterParts(append(box, mdat...), keep)
		}
		if _, err := w.Write(box); err != nil {
			return err
		}
	}
}

// readInit reads the boxes up to moov and returns the video track IDs and
// the init segment with only those tracks. keep is nil when every track is
// video (nothing to drop) or none is (dropping would leave nothing).
func readInit(r io.Reader) (keep map[int]bool, init []byte, err error) {
	raw, err := readHeader(r)
	if err != nil {
		return nil, nil, err
	}
	var in fmp4.Init
	if err := in.Unmarshal(bytes.NewReader(raw)); err != nil {
		return nil, nil, err
	}
	out := fmp4.Init{UserData: in.UserData}
	keep = map[int]bool{}
	for _, t := range in.Tracks {
		if t.Codec != nil && t.Codec.IsVideo() {
			keep[t.ID] = true
			out.Tracks = append(out.Tracks, t)
		}
	}
	if len(keep) == 0 || len(keep) == len(in.Tracks) {
		return nil, nil, nil
	}
	var buf seekablebuffer.Buffer
	if err := out.Marshal(&buf); err != nil {
		return nil, nil, err
	}
	return keep, buf.Bytes(), nil
}

// readHeader reads the boxes from ftyp up to and including moov.
func readHeader(r io.Reader) ([]byte, error) {
	var raw []byte
	for {
		box, typ, err := readBox(r)
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 && typ != "ftyp" {
			return nil, errors.New("archive: not an MP4 file")
		}
		raw = append(raw, box...)
		if typ == "moov" {
			return raw, nil
		}
	}
}

// filterParts rewrites a moof+mdat pair with only the kept tracks; a pair it
// cannot read is passed on as it is.
func filterParts(raw []byte, keep map[int]bool) []byte {
	var parts fmp4.Parts
	if err := parts.Unmarshal(raw); err != nil {
		return raw
	}
	var buf seekablebuffer.Buffer
	for _, p := range parts {
		tracks := p.Tracks[:0]
		for _, t := range p.Tracks {
			if keep[t.ID] {
				tracks = append(tracks, t)
			}
		}
		p.Tracks = tracks
		if len(tracks) == 0 {
			continue
		}
		if err := p.Marshal(&buf); err != nil {
			return raw
		}
	}
	return buf.Bytes()
}

// readBox reads one whole top-level box, header included.
func readBox(r io.Reader) (box []byte, typ string, err error) {
	hdr := make([]byte, 8)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, "", err
	}
	size := uint64(binary.BigEndian.Uint32(hdr[:4]))
	typ = string(hdr[4:8])
	switch size {
	case 0: // to the end of the stream
		rest, err := io.ReadAll(r)
		return append(hdr, rest...), typ, err
	case 1: // 64-bit size follows
		ext := make([]byte, 8)
		if _, err := io.ReadFull(r, ext); err != nil {
			return nil, "", unexpected(err)
		}
		hdr = append(hdr, ext...)
		size = binary.BigEndian.Uint64(ext)
	}
	if size < uint64(len(hdr)) || size > 1<<31 {
		return nil, "", errBadBox
	}
	box = make([]byte, size)
	copy(box, hdr)
	if _, err := io.ReadFull(r, box[len(hdr):]); err != nil {
		return nil, "", unexpected(err)
	}
	return box, typ, nil
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// errBadBox is a box size no box can have: the data from here is not MP4.
var errBadBox = errors.New("archive: bad box size")

// endOfInput: the input ended, cleanly, cut off mid-box or turned into
// something that is not MP4; the output so far is whole boxes either way.
// Other read errors are returned.
func endOfInput(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, errBadBox) {
		return nil
	}
	return err
}

func passThrough(w io.Writer, head []byte, r io.Reader) error {
	if _, err := w.Write(head); err != nil {
		return err
	}
	_, err := io.Copy(w, r)
	return err
}
