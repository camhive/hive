// The fragment muxer in this file is derived from
// MediaMTX (https://github.com/bluenviron/mediamtx, v1.21.3, internal/playback),
// used under the MIT licence:
//
// Copyright (c) 2019 aler9
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package archive

import (
	"io"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
)

// The rules here and in span.go are MediaMTX's playback server's, which
// served archive playback before the Hive did (ADR-0019 §6): the answers
// stay byte for byte what the web app already plays.

// outPartDuration is how much of each track goes into one output fragment.
const outPartDuration = time.Second

// muxTrack collects one track's samples for the next output fragment.
type muxTrack struct {
	id        int
	timeScale uint32
	firstDTS  int64 // -1 until the first sample inside the span
	lastDTS   int64
	samples   []*fmp4.Sample
}

// muxer writes samples, timed relative to the span's start, as fragmented
// MP4: the init when the first fragment is ready, then one fragment per
// outPartDuration. Samples before the start (negative DTS) are kept from
// the last sync sample on, with zero duration, so the player can decode the
// first frame of the span.
type muxer struct {
	w       io.Writer
	init    *fmp4.Init // pending until the first fragment is written
	nextSeq uint32
	tracks  []*muxTrack
	cur     *muxTrack
	buf     seekablebuffer.Buffer
}

func newMuxer(w io.Writer, init *fmp4.Init) *muxer {
	m := &muxer{w: w, init: init}
	for _, t := range init.Tracks {
		m.tracks = append(m.tracks, &muxTrack{id: t.ID, timeScale: t.TimeScale, firstDTS: -1})
	}
	return m
}

func (m *muxer) setTrack(id int) {
	for _, t := range m.tracks {
		if t.id == id {
			m.cur = t
		}
	}
}

// writeSample adds one sample of the current track at dts (track units).
func (m *muxer) writeSample(dts int64, s *fmp4.Sample) error {
	t := m.cur
	out := &fmp4.Sample{PTSOffset: s.PTSOffset, IsNonSyncSample: s.IsNonSyncSample, Payload: s.Payload}
	if dts < 0 {
		if !s.IsNonSyncSample {
			t.samples = t.samples[:0] // a new GOP starts here
		}
		t.samples = append(t.samples, out)
		return nil
	}
	if t.firstDTS < 0 {
		t.firstDTS = dts // the first sample inside the span
		if !s.IsNonSyncSample {
			t.samples = t.samples[:0] // it decodes alone: the GOP before is not needed
		}
	} else {
		t.samples[len(t.samples)-1].Duration = uint32(max(dts-t.lastDTS, 0)) //nolint:gosec // a gap between two samples of one track
	}
	t.samples = append(t.samples, out)
	t.lastDTS = dts
	if t.lastDTS-t.firstDTS >= toTrackUnits(outPartDuration, t.timeScale) {
		return m.writeFragment(false)
	}
	return nil
}

// endRun gives the current track's last sample its duration: up to dts,
// where the next sample would have been.
func (m *muxer) endRun(dts int64) {
	t := m.cur
	if len(t.samples) != 0 && t.firstDTS >= 0 {
		t.samples[len(t.samples)-1].Duration = uint32(max(dts-t.lastDTS, 0)) //nolint:gosec // as in writeSample
	}
}

// writeFragment writes the collected samples as one fragment. Until final,
// each track keeps its last sample back: its duration is only known when
// the next one arrives.
func (m *muxer) writeFragment(final bool) error {
	part := fmp4.Part{SequenceNumber: m.nextSeq}
	for _, t := range m.tracks {
		if pt := t.take(final); pt != nil {
			part.Tracks = append(part.Tracks, pt)
		}
	}
	if part.Tracks == nil {
		if m.init != nil {
			return ErrNoFootage // nothing of the span was found
		}
		return nil
	}
	m.nextSeq++
	if m.init != nil {
		if err := m.emit(m.init); err != nil {
			return err
		}
		m.init = nil
	}
	return m.emit(&part)
}

// take removes the samples ready for a fragment; nil when there are none.
func (t *muxTrack) take(final bool) *fmp4.PartTrack {
	if t.firstDTS < 0 || len(t.samples) == 0 || (!final && len(t.samples) < 2) {
		return nil
	}
	pt := &fmp4.PartTrack{ID: t.id, BaseTime: uint64(t.firstDTS), Samples: t.samples} //nolint:gosec // firstDTS >= 0 here
	if !final {
		pt.Samples = t.samples[:len(t.samples)-1]
		t.samples = t.samples[len(t.samples)-1:]
		t.firstDTS = t.lastDTS
	}
	return pt
}

// emit marshals an init or a fragment and writes it.
func (m *muxer) emit(box interface {
	Marshal(w io.WriteSeeker) error
}) error {
	m.buf.Reset()
	if err := box.Marshal(&m.buf); err != nil {
		return err
	}
	_, err := m.w.Write(m.buf.Bytes())
	return err
}

func (m *muxer) flush() error { return m.writeFragment(true) }

// toTrackUnits converts a duration to a track's timescale.
func toTrackUnits(d time.Duration, timeScale uint32) int64 {
	ts := int64(timeScale)
	return int64(d/time.Second)*ts + int64(d%time.Second)*ts/int64(time.Second)
}

// fromTrackUnits converts a track's timescale to a duration.
func fromTrackUnits(v int64, timeScale uint32) time.Duration {
	ts := int64(timeScale)
	return time.Duration(v/ts)*time.Second + time.Duration(v%ts)*time.Second/time.Duration(ts)
}
