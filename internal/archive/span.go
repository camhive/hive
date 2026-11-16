// The segment selection, joining and part walk in this file are derived from
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
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
)

// joinTolerance is how far apart the end of one segment and the start of
// the next may be for segments without an mtxi box to join.
const joinTolerance = time.Second

// Bounds on what one read holds in memory: a segment header, and one
// fragment (moof and mdat) of about a second of video.
const (
	maxHeaderBytes   = 1 << 20
	maxFragmentBytes = 64 << 20
)

// pickSegments chooses, from a stream's segments sorted by start, the ones
// a span reads: from the one the start falls in (or the first, when the
// span starts before any footage) up to the last starting before the end.
func pickSegments(all []Segment, start, end time.Time) []Segment {
	var segs []Segment
	for _, s := range all {
		if !end.Before(s.Start) {
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 || start.Before(segs[0].Start) {
		return segs
	}
	for i := 0; i < len(segs)-1; i++ {
		if !start.Before(segs[i].Start) && start.Before(segs[i+1].Start) {
			return segs[i:]
		}
	}
	return segs[len(segs)-1:]
}

// openedSegment is a segment opened with its header read.
type openedSegment struct {
	file SegmentFile
	init *fmp4.Init
}

func openSegment(open SegmentOpener, seg Segment) (openedSegment, error) {
	f, err := open(seg)
	if err != nil {
		return openedSegment{}, err
	}
	init, err := readSegmentInit(f)
	if err != nil {
		_ = f.Close()
		return openedSegment{}, fmt.Errorf("archive: %s: %w", seg.Path, err)
	}
	return openedSegment{file: f, init: init}, nil
}

// readSegmentInit reads the ftyp and moov boxes at the start of a segment.
func readSegmentInit(f SegmentFile) (*fmp4.Init, error) {
	ftyp, err := boxAt(f, 0)
	if err != nil || ftyp.typ != "ftyp" {
		return nil, errors.New("not an MP4 file")
	}
	moov, err := boxAt(f, ftyp.size)
	if err != nil || moov.typ != "moov" {
		return nil, errors.New("moov not found")
	}
	n := ftyp.size + moov.size
	if n > maxHeaderBytes {
		return nil, errors.New("header too large")
	}
	raw := make([]byte, n)
	if err := readAt(f, raw, 0); err != nil {
		return nil, err
	}
	var init fmp4.Init
	if err := init.Unmarshal(bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	return &init, nil
}

// boxHeader is a top-level box found in a segment.
type boxHeader struct {
	typ  string
	off  int64
	size int64 // header included
}

// errTruncated means a box runs past the end of what can be read: the
// segment is still being written, or was cut off.
var errTruncated = errors.New("archive: truncated box")

// boxAt reads the header of the box at off.
func boxAt(f SegmentFile, off int64) (boxHeader, error) {
	var hdr [16]byte
	if off+8 > f.Size() {
		return boxHeader{}, errTruncated
	}
	if err := readAt(f, hdr[:8], off); err != nil {
		return boxHeader{}, err
	}
	b := boxHeader{typ: string(hdr[4:8]), off: off, size: int64(binary.BigEndian.Uint32(hdr[:4]))}
	switch b.size {
	case 0: // to the end of the file
		b.size = f.Size() - off
	case 1: // a 64-bit size follows
		if err := readAt(f, hdr[8:16], off+8); err != nil {
			return boxHeader{}, errTruncated
		}
		b.size = int64(binary.BigEndian.Uint64(hdr[8:16])) //nolint:gosec // checked against the file size below
	}
	if b.size < 8 || b.size > f.Size()-off {
		return boxHeader{}, errTruncated
	}
	return b, nil
}

// walkBeforeStart turns the walk of skipBeforeStart on; tests switch it off
// to compare with a read from offset 0, as the reader did before #100.
var walkBeforeStart = true

// spanWriter writes one span across the segments that hold it.
type spanWriter struct {
	ctx    context.Context
	m      *muxer
	start  time.Time
	length time.Duration
	tracks []*fmp4.InitTrack // of the first segment: they time every segment
	info   *mtxi             // of the first segment; nil for segments without one
	offset time.Duration     // first segment's start relative to the span: negative inside it
}

// writeSpan writes the span [start, start+length) of segs (from
// pickSegments) to w as fragmented MP4. It stops early at the first segment
// that does not continue the one before (a gap, a new recording, other
// tracks). ErrNoFootage: no sample falls in the span.
func writeSpan(ctx context.Context, w io.Writer, open SegmentOpener, segs []Segment, start time.Time, length time.Duration) error {
	first, err := openSegment(open, segs[0])
	if err != nil {
		return err
	}
	defer func() { _ = first.file.Close() }()
	sw := &spanWriter{ctx: ctx, m: newMuxer(w, &fmp4.Init{Tracks: first.init.Tracks}), start: start, length: length,
		tracks: first.init.Tracks, info: segmentInfo(first.init), offset: segs[0].Start.Sub(start)}
	elapsed, err := sw.writeParts(first.file, sw.offset)
	if err != nil {
		return err
	}
	if err := sw.writeFollowing(open, segs[1:], first.init, segs[0].Start.Add(elapsed)); err != nil {
		return err
	}
	return sw.m.flush()
}

// writeFollowing writes the segments after the first while they continue
// the previous one; prevEnd is where the previous one's samples ended.
func (sw *spanWriter) writeFollowing(open SegmentOpener, segs []Segment, prev *fmp4.Init, prevEnd time.Time) error {
	for _, seg := range segs {
		cur, err := openSegment(open, seg)
		if err != nil {
			return err
		}
		if !continues(prev, prevEnd, cur.init, seg.Start) {
			_ = cur.file.Close()
			return nil
		}
		elapsed, err := sw.writeParts(cur.file, sw.startOf(cur.init, seg))
		_ = cur.file.Close()
		if err != nil {
			return err
		}
		prev, prevEnd = cur.init, seg.Start.Add(elapsed)
	}
	return nil
}

// startOf is where a following segment starts relative to the span: from
// the decode times the engine recorded, or from its file name without them.
func (sw *spanWriter) startOf(init *fmp4.Init, seg Segment) time.Duration {
	if sw.info != nil {
		return time.Duration(segmentInfo(init).DTS-sw.info.DTS) + sw.offset
	}
	return seg.Start.Sub(sw.start)
}

// continues says whether cur carries on where prev ended: the next segment
// of the same recording, or (without mtxi boxes) the same tracks starting
// within joinTolerance of prev's end.
func continues(prev *fmp4.Init, prevEnd time.Time, cur *fmp4.Init, curStart time.Time) bool {
	a, b := segmentInfo(prev), segmentInfo(cur)
	switch {
	case a != nil && b != nil:
		return a.StreamID == b.StreamID && a.SegmentNumber+1 == b.SegmentNumber
	case a != nil || b != nil:
		return false
	default:
		return sameTracks(prev.Tracks, cur.Tracks) &&
			!curStart.Before(prevEnd.Add(-joinTolerance)) && !curStart.After(prevEnd.Add(joinTolerance))
	}
}

func sameTracks(a, b []*fmp4.InitTrack) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].TimeScale != b[i].TimeScale || reflect.TypeOf(a[i].Codec) != reflect.TypeOf(b[i].Codec) {
			return false
		}
	}
	return true
}

// writeParts feeds a segment's fragments to the muxer, its samples shifted
// by startDTS, until a track reaches the end of the span. It returns how
// far into the segment the samples it read reach.
func (sw *spanWriter) writeParts(f SegmentFile, startDTS time.Duration) (time.Duration, error) {
	var elapsed time.Duration
	for off := sw.skipBeforeStart(f, startDTS); ; {
		if err := sw.ctx.Err(); err != nil {
			return 0, err
		}
		parts, next, err := nextFragment(f, off)
		if errors.Is(err, io.EOF) || errors.Is(err, errTruncated) {
			return elapsed, nil
		}
		if err != nil {
			return 0, err
		}
		done, e, err := sw.writeFragment(parts, startDTS)
		if err != nil {
			return 0, err
		}
		elapsed = max(elapsed, e)
		if done {
			return elapsed, nil
		}
		off = next
	}
}

// nextFragment reads the next moof and the mdat after it, from off on.
// errTruncated: there is no complete one.
func nextFragment(f SegmentFile, off int64) (fmp4.Parts, int64, error) {
	moof, err := findBox(f, off, "moof")
	if err != nil {
		return nil, 0, err
	}
	mdat, err := boxAt(f, moof.off+moof.size)
	if err != nil {
		return nil, 0, err
	}
	n := moof.size + mdat.size
	if n > maxFragmentBytes {
		return nil, 0, fmt.Errorf("archive: fragment of %d bytes", n)
	}
	raw := make([]byte, n)
	if err := readAt(f, raw, moof.off); err != nil {
		return nil, 0, err
	}
	var parts fmp4.Parts
	if err := parts.Unmarshal(raw); err != nil {
		return nil, 0, err
	}
	return parts, moof.off + n, nil
}

// skipBeforeStart walks the fragments whose samples all lie before the
// span's start reading only their moofs (issue #100), and returns where
// reading samples resumes: for every track, the fragment of its last sync
// sample before the start. The muxer keeps the samples from that sample on
// so the player can decode the first frame, and drops everything earlier,
// so what it writes does not change. How far the segment reaches is taken
// from the fragments read: each track skipped has a later sample in the
// fragment reading resumes at or after it.
//
// The walk reads only timing, so it is laxer than fmp4.Parts, which reads
// the samples: a skipped fragment whose payload Parts would refuse (a trun
// without a data offset, a sample outside the fragment, a box other than
// mdat after the moof) no longer fails the span. A moof the walk cannot
// read ends the walk, and the full read meets it there.
func (sw *spanWriter) skipBeforeStart(f SegmentFile, startDTS time.Duration) int64 {
	if !walkBeforeStart {
		return 0
	}
	keep := map[int]int64{} // track ID -> offset of the fragment its kept samples start in
	off := int64(0)
	for {
		runs, next, err := nextMoof(f, off)
		if err != nil {
			break // the full read starts here and meets the same end or error
		}
		if !sw.beforeStart(runs, startDTS) {
			break
		}
		for _, r := range runs {
			if _, seen := keep[r.id]; r.sync || !seen {
				keep[r.id] = off
			}
		}
		off = next
	}
	for _, k := range keep {
		off = min(off, k)
	}
	return off
}

// beforeStart says whether every sample of a fragment lies before the
// span's start.
func (sw *spanWriter) beforeStart(runs []*trackRun, startDTS time.Duration) bool {
	for _, r := range runs {
		ts, ok := sw.timeScale(r.id)
		if !ok || (r.samples > 0 && int64(r.lastDTS())+toTrackUnits(startDTS, ts) >= 0) { //nolint:gosec // decode times of recordings fit in int64
			return false
		}
	}
	return true
}

// writeFragment feeds one fragment's tracks to the muxer. done: a track
// reached the end of the span, so nothing after this fragment is needed.
func (sw *spanWriter) writeFragment(parts fmp4.Parts, startDTS time.Duration) (done bool, elapsed time.Duration, err error) {
	for _, p := range parts {
		for _, pt := range p.Tracks {
			end, e, err := sw.writeTrack(pt, startDTS)
			if err != nil {
				return false, 0, err
			}
			done, elapsed = done || end, max(elapsed, e)
		}
	}
	return done, elapsed, nil
}

// writeTrack feeds one track's run of samples, stopping at the span's end.
func (sw *spanWriter) writeTrack(pt *fmp4.PartTrack, startDTS time.Duration) (end bool, elapsed time.Duration, err error) {
	ts, ok := sw.timeScale(pt.ID)
	if !ok {
		return false, 0, fmt.Errorf("archive: invalid track ID: %d", pt.ID)
	}
	sw.m.setTrack(pt.ID)
	startUnits, lengthUnits := toTrackUnits(startDTS, ts), toTrackUnits(sw.length, ts)
	dts := int64(pt.BaseTime) + startUnits //nolint:gosec // decode times of recordings fit in int64
	for _, s := range pt.Samples {
		if dts >= lengthUnits {
			end = true
			break
		}
		if err := sw.m.writeSample(dts, s); err != nil {
			return false, 0, err
		}
		dts += int64(s.Duration)
	}
	sw.m.endRun(dts)
	return end, fromTrackUnits(dts-startUnits, ts), nil
}

func (sw *spanWriter) timeScale(id int) (uint32, bool) {
	for _, t := range sw.tracks {
		if t.ID == id {
			return t.TimeScale, true
		}
	}
	return 0, false
}
