package camsim

import "time"

// Scenario is a camera falling ill and getting well again, told as what a
// hive sees of it over time: the bitrate it sends and when its stream is
// down. Tests drive it on a fake clock (camera care, #163), where a real
// simulator would need hours.
type Scenario struct {
	Kbps           int           // the healthy bitrate
	Trouble        time.Duration // when trouble begins, after the start
	Recover        time.Duration // when it ends, after the start; 0: never
	BitrateFactor  float64       // the share of Kbps sent during trouble; 0: all of it
	ReconnectEvery time.Duration // during trouble the stream drops this often; 0: never
	Outage         time.Duration // how long one drop lasts; default 10 s
}

// troubled reports whether at (after the start) falls in the trouble.
func (s Scenario) troubled(at time.Duration) bool {
	return at >= s.Trouble && (s.Recover == 0 || at < s.Recover)
}

// KbpsAt is the bitrate the camera sends at (after the start).
func (s Scenario) KbpsAt(at time.Duration) int {
	if s.troubled(at) && s.BitrateFactor > 0 {
		return int(float64(s.Kbps) * s.BitrateFactor)
	}
	return s.Kbps
}

// UpAt reports whether the stream is up at (after the start): during
// trouble it drops for Outage at the end of every ReconnectEvery.
func (s Scenario) UpAt(at time.Duration) bool {
	if !s.troubled(at) || s.ReconnectEvery <= 0 {
		return true
	}
	outage := s.Outage
	if outage <= 0 {
		outage = 10 * time.Second
	}
	return (at-s.Trouble)%s.ReconnectEvery < s.ReconnectEvery-outage
}
