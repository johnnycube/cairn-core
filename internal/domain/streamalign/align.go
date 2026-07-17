// Package streamalign merges per-source stream channels onto a common time axis:
// pick a grid from the finest source, align clock offset, resample each channel,
// and cap interpolation at a max gap (missing, not fabricated). Pure domain.
package streamalign

import (
	"math"
	"sort"
	"time"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// Options tunes the merge.
type Options struct {
	GapCap           time.Duration // max gap to bridge; beyond it → missing (default 30s)
	AlignStarts      bool          // shift each source to a shared start (corrects clock skew; default true)
	MinStep, MaxStep time.Duration // clamp the derived grid step (defaults 1s / 60s)
}

func (o Options) withDefaults() Options {
	if o.GapCap <= 0 {
		o.GapCap = 30 * time.Second
	}
	if o.MinStep <= 0 {
		o.MinStep = time.Second
	}
	if o.MaxStep <= 0 {
		o.MaxStep = 60 * time.Second
	}
	return o
}

// MergedStream is the aligned result: a regular grid, per-channel values
// (nil = missing), and per-channel source provenance.
type MergedStream struct {
	Grid       []time.Time
	Channels   map[domain.StreamChannel][]*float64
	Provenance map[domain.StreamChannel]domain.SourceID
}

// continuousChannels are interpolated linearly. No discrete channels exist yet
// (lap-marker/moving-flag would use nearest/step).
var continuousChannels = []domain.StreamChannel{
	domain.StreamChannelLatitude, domain.StreamChannelLongitude, domain.StreamChannelAltitude,
	domain.StreamChannelDistance, domain.StreamChannelSpeed, domain.StreamChannelHeartRate,
	domain.StreamChannelPower, domain.StreamChannelCadence, domain.StreamChannelTemperature,
	domain.StreamChannelGrade,
}

// Build merges the given per-source streams. winners maps each channel to the
// source it should come from; channels with no winner (or whose winner has no
// data) are omitted. When winners is nil, the data-driven default is used: per
// channel, the source with the most non-nil samples (tie → smallest source id).
func Build(streams map[domain.SourceID]domain.Stream, winners map[domain.StreamChannel]domain.SourceID, opt Options) MergedStream {
	opt = opt.withDefaults()
	out := MergedStream{
		Channels:   map[domain.StreamChannel][]*float64{},
		Provenance: map[domain.StreamChannel]domain.SourceID{},
	}
	if len(streams) == 0 {
		return out
	}

	// Per-source clock-offset shift (align starts to a global origin).
	shift := map[domain.SourceID]time.Duration{}
	var globalStart time.Time
	for _, s := range streams {
		if len(s.Samples) == 0 {
			continue
		}
		first := s.Samples[0].Timestamp
		if globalStart.IsZero() || first.Before(globalStart) {
			globalStart = first
		}
	}
	if opt.AlignStarts {
		for id, s := range streams {
			if len(s.Samples) == 0 {
				continue
			}
			shift[id] = globalStart.Sub(s.Samples[0].Timestamp) // add to each ts
		}
	}

	// Grid step = finest median interval across sources, clamped.
	step := finestStep(streams, opt)

	// Grid span = [globalStart, globalEnd] over shifted timestamps.
	var globalEnd time.Time
	for id, s := range streams {
		if len(s.Samples) == 0 {
			continue
		}
		last := s.Samples[len(s.Samples)-1].Timestamp.Add(shift[id])
		if last.After(globalEnd) {
			globalEnd = last
		}
	}
	if !globalEnd.After(globalStart) {
		return out
	}
	grid := buildGrid(globalStart, globalEnd, step)
	out.Grid = grid

	if winners == nil {
		winners = defaultWinners(streams)
	}

	for _, ch := range continuousChannels {
		srcID, ok := winners[ch]
		if !ok {
			continue
		}
		s, ok := streams[srcID]
		if !ok {
			continue
		}
		pts := extractChannel(s.Samples, ch, shift[srcID])
		if len(pts) == 0 {
			continue
		}
		out.Channels[ch] = resample(pts, grid, opt.GapCap)
		out.Provenance[ch] = srcID
	}
	return out
}

// point is a (time, value) sample for one channel.
type point struct {
	t time.Time
	v float64
}

// finestStep returns the smallest median Δt across sources (not sample count,
// which pauses inflate), clamped to [MinStep, MaxStep].
func finestStep(streams map[domain.SourceID]domain.Stream, opt Options) time.Duration {
	best := time.Duration(0)
	for _, s := range streams {
		m := medianInterval(s.Samples)
		if m <= 0 {
			continue
		}
		if best == 0 || m < best {
			best = m
		}
	}
	if best == 0 {
		best = opt.MinStep
	}
	if best < opt.MinStep {
		best = opt.MinStep
	}
	if best > opt.MaxStep {
		best = opt.MaxStep
	}
	return best
}

func medianInterval(samples []domain.StreamSample) time.Duration {
	if len(samples) < 2 {
		return 0
	}
	deltas := make([]time.Duration, 0, len(samples)-1)
	for i := 1; i < len(samples); i++ {
		d := samples[i].Timestamp.Sub(samples[i-1].Timestamp)
		if d > 0 {
			deltas = append(deltas, d)
		}
	}
	if len(deltas) == 0 {
		return 0
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
	return deltas[len(deltas)/2]
}

func buildGrid(start, end time.Time, step time.Duration) []time.Time {
	var grid []time.Time
	for t := start; !t.After(end); t = t.Add(step) {
		grid = append(grid, t)
		if len(grid) > 1_000_000 { // safety bound
			break
		}
	}
	return grid
}

// resample linearly interpolates the (sorted) points onto grid; a point is nil
// when bracketing samples are >gapCap apart or it falls outside the data range.
func resample(pts []point, grid []time.Time, gapCap time.Duration) []*float64 {
	out := make([]*float64, len(grid))
	if len(pts) == 0 {
		return out
	}
	j := 0
	for i, g := range grid {
		// advance j so pts[j].t <= g < pts[j+1].t (or j at last)
		for j+1 < len(pts) && !pts[j+1].t.After(g) {
			j++
		}
		switch {
		case g.Before(pts[0].t) || g.After(pts[len(pts)-1].t):
			out[i] = nil
		case pts[j].t.Equal(g) || j+1 >= len(pts):
			v := pts[j].v
			out[i] = &v
		default:
			lo, hi := pts[j], pts[j+1]
			if hi.t.Sub(lo.t) > gapCap {
				out[i] = nil
				continue
			}
			frac := float64(g.Sub(lo.t)) / float64(hi.t.Sub(lo.t))
			v := lo.v + frac*(hi.v-lo.v)
			out[i] = &v
		}
	}
	return out
}

// defaultWinners picks, per channel, the source with the most non-nil samples
// (tie → smallest source id).
func defaultWinners(streams map[domain.SourceID]domain.Stream) map[domain.StreamChannel]domain.SourceID {
	ids := make([]domain.SourceID, 0, len(streams))
	for id := range streams {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	winners := map[domain.StreamChannel]domain.SourceID{}
	for _, ch := range continuousChannels {
		bestID := domain.SourceID{}
		best := -1
		for _, id := range ids {
			n := countChannel(streams[id].Samples, ch)
			if n > best {
				best, bestID = n, id
			}
		}
		if best > 0 {
			winners[ch] = bestID
		}
	}
	return winners
}

func countChannel(samples []domain.StreamSample, ch domain.StreamChannel) int {
	n := 0
	for i := range samples {
		if _, ok := channelValue(&samples[i], ch); ok {
			n++
		}
	}
	return n
}

func extractChannel(samples []domain.StreamSample, ch domain.StreamChannel, shift time.Duration) []point {
	pts := make([]point, 0, len(samples))
	for i := range samples {
		if v, ok := channelValue(&samples[i], ch); ok {
			pts = append(pts, point{t: samples[i].Timestamp.Add(shift), v: v})
		}
	}
	return pts
}

// channelValue reads a channel's float value from a sample, or (0,false) if the
// sample doesn't carry that channel.
func channelValue(s *domain.StreamSample, ch domain.StreamChannel) (float64, bool) {
	switch ch {
	case domain.StreamChannelLatitude:
		return derefP(s.Latitude)
	case domain.StreamChannelLongitude:
		return derefP(s.Longitude)
	case domain.StreamChannelAltitude:
		return derefP(s.AltitudeM)
	case domain.StreamChannelDistance:
		return derefP(s.DistanceM)
	case domain.StreamChannelSpeed:
		return derefP(s.SpeedMps)
	case domain.StreamChannelHeartRate:
		return derefI16(s.HeartRateBpm)
	case domain.StreamChannelPower:
		return derefI16(s.PowerW)
	case domain.StreamChannelCadence:
		return derefI16(s.Cadence)
	case domain.StreamChannelTemperature:
		return derefF32(s.TemperatureC)
	case domain.StreamChannelGrade:
		return derefF32(s.Grade)
	}
	return 0, false
}

func derefP(p *float64) (float64, bool) {
	if p == nil || math.IsNaN(*p) {
		return 0, false
	}
	return *p, true
}
func derefI16(p *int16) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return float64(*p), true
}
func derefF32(p *float32) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return float64(*p), true
}
