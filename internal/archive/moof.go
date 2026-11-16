package archive

import (
	"bytes"
	"errors"
	"fmt"

	amp4 "github.com/abema/go-mp4"
)

// trackRun is one track's samples in a fragment, as far as their timing
// goes: what a seek needs to skip a fragment without reading its mdat.
type trackRun struct {
	id      int
	base    uint64 // decode time of the first sample, in track units
	total   uint64 // all durations
	last    uint64 // duration of the last sample
	samples int
	sync    bool // holds a sync sample
}

// lastDTS is the decode time of the run's last sample.
func (r *trackRun) lastDTS() uint64 { return r.base + r.total - r.last }

// Flags of a trun box and of a sample, as fmp4.Parts reads them: the
// timing here must match what the full read sees.
const (
	trunSampleDurationPresent = 0x100
	trunSampleFlagsPresent    = 0x400
	sampleIsNonSync           = 1 << 16
)

// nextMoof reads only the moof of the next fragment from off on, and
// returns its track runs and the offset after the fragment's mdat.
// errTruncated: there is no complete one.
func nextMoof(f SegmentFile, off int64) ([]*trackRun, int64, error) {
	moof, err := findBox(f, off, "moof")
	if err != nil {
		return nil, 0, err
	}
	mdat, err := boxAt(f, moof.off+moof.size)
	if err != nil {
		return nil, 0, err
	}
	if moof.size > maxHeaderBytes {
		return nil, 0, fmt.Errorf("archive: moof of %d bytes", moof.size)
	}
	raw := make([]byte, moof.size)
	if err := readAt(f, raw, moof.off); err != nil {
		return nil, 0, err
	}
	runs, err := moofRuns(raw)
	return runs, mdat.off + mdat.size, err
}

// findBox finds the next top-level box of type typ from off on.
// errTruncated: there is none (boxAt answers it at the end of the file).
func findBox(f SegmentFile, off int64, typ string) (boxHeader, error) {
	for {
		b, err := boxAt(f, off)
		if err != nil || b.typ == typ {
			return b, err
		}
		off += b.size
	}
}

// moofRuns reads the timing of every track run in a moof, as fmp4.Parts
// reads it (tfhd defaults, no trex): FMP4Duration has its own reading for
// the timeline.
func moofRuns(raw []byte) ([]*trackRun, error) {
	var runs []*trackRun
	var tfhd *amp4.Tfhd
	_, err := amp4.ReadBoxStructure(bytes.NewReader(raw), func(h *amp4.ReadHandle) (any, error) {
		switch h.BoxInfo.Type.String() {
		case "moof":
			return h.Expand()
		case "traf":
			runs, tfhd = append(runs, &trackRun{}), nil
			return h.Expand()
		case "tfhd", "tfdt", "trun":
			if len(runs) == 0 {
				return nil, errors.New("archive: track box outside a traf")
			}
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			tfhd, err = runs[len(runs)-1].add(box, tfhd)
			return nil, err
		}
		return nil, nil
	})
	return runs, err
}

// add takes one box of the run's traf into account; tfhd is the traf's
// track header seen so far, which a trun's defaults come from.
func (r *trackRun) add(box amp4.IBox, tfhd *amp4.Tfhd) (*amp4.Tfhd, error) {
	switch b := box.(type) {
	case *amp4.Tfhd:
		r.id = int(b.TrackID)
		return b, nil
	case *amp4.Tfdt:
		r.base = b.GetBaseMediaDecodeTime()
	case *amp4.Trun:
		if tfhd == nil {
			return nil, errors.New("archive: trun before tfhd")
		}
		r.addSamples(b, tfhd)
	}
	return tfhd, nil
}

func (r *trackRun) addSamples(trun *amp4.Trun, tfhd *amp4.Tfhd) {
	for _, e := range trun.Entries {
		dur, flags := tfhd.DefaultSampleDuration, tfhd.DefaultSampleFlags
		if trun.CheckFlag(trunSampleDurationPresent) {
			dur = e.SampleDuration
		}
		if trun.CheckFlag(trunSampleFlagsPresent) {
			flags = e.SampleFlags
		}
		r.total += uint64(dur)
		r.last = uint64(dur)
		r.samples++
		r.sync = r.sync || flags&sampleIsNonSync == 0
	}
}
