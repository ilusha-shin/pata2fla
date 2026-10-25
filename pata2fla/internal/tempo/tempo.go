// Package tempo estimates a constant tempo from drum onsets and describes the tempo map that
// is written to the MIDI and .chart files, so that the beat grid matches the music.
package tempo

import (
	"math"
	"math/cmplx"
)

// Point is a tempo change at a whole beat (quarter note) index.
type Point struct {
	Beat int
	BPM  float64
}

// Map is a piecewise-constant tempo map. The first point is always at beat 0.
type Map struct {
	Points []Point
}

// Constant returns a map with a single tempo.
func Constant(bpm float64) *Map {
	if bpm <= 0 {
		bpm = 120
	}
	return &Map{Points: []Point{{Beat: 0, BPM: bpm}}}
}

// WithGrid returns a map in which beats fall on anchorSec + k*60/bpm (k integer). It stretches
// one beat near the start; every other beat has exactly the tempo bpm.
func WithGrid(bpm, anchorSec float64) *Map {
	if bpm <= 0 {
		bpm = 120
	}
	p := 60.0 / bpm
	if anchorSec < 0 {
		anchorSec = 0
	}
	m := int(math.Floor(anchorSec / p))
	l := anchorSec - float64(m)*p + p // length of the stretched beat, in [p, 2p)
	if math.Abs(l-p) < 1e-4 {
		return Constant(bpm)
	}
	stretched := 60.0 / l
	if m == 0 {
		return &Map{Points: []Point{{0, stretched}, {1, bpm}}}
	}
	return &Map{Points: []Point{{0, bpm}, {m, stretched}, {m + 1, bpm}}}
}

// BPM returns the main (steady) tempo of the map.
func (m *Map) BPM() float64 {
	return m.Points[len(m.Points)-1].BPM
}

// SecToBeat converts a time to a (fractional) beat index.
func (m *Map) SecToBeat(sec float64) float64 {
	if sec <= 0 {
		return sec * m.Points[0].BPM / 60.0
	}
	t := 0.0
	for i, p := range m.Points {
		if i+1 < len(m.Points) {
			seg := float64(m.Points[i+1].Beat-p.Beat) * 60.0 / p.BPM
			if sec < t+seg {
				return float64(p.Beat) + (sec-t)*p.BPM/60.0
			}
			t += seg
			continue
		}
		return float64(p.Beat) + (sec-t)*p.BPM/60.0
	}
	return 0
}

// Estimate finds the constant tempo (beats per minute) in [minBPM, maxBPM] that best explains
// the beat-bearing hits fitTimes (kick and snare, seconds), plus the beat phase in seconds
// (a beat falls on phase + k*60/bpm) and the fit strength in [0, 1].
//
// nTotal is the total number of drum hits and duration the length of the track in seconds;
// they are used to avoid the "double tempo" ambiguity (a real beat carries ~2-3 hits).
func Estimate(fitTimes []float64, nTotal int, duration, minBPM, maxBPM float64) (bpm, phase, strength float64) {
	if len(fitTimes) < 8 || duration <= 0 {
		return 0, 0, 0
	}
	if minBPM <= 0 {
		minBPM = 70
	}
	if maxBPM <= minBPM {
		maxBPM = minBPM + 1
	}
	sum := func(b float64) complex128 {
		p := 60.0 / b
		var s complex128
		for _, t := range fitTimes {
			s += cmplx.Rect(1, 2*math.Pi*math.Mod(t, p)/p)
		}
		return s
	}
	r := func(b float64) float64 { return cmplx.Abs(sum(b)) / float64(len(fitTimes)) }
	prior := func(b float64) float64 {
		x := math.Log2(b/125.0) / 0.8
		return math.Exp(-0.5 * x * x)
	}
	density := func(b float64) float64 {
		d := float64(nTotal) * (60.0 / b) / duration
		return math.Min(1.0, (d/2.0)*(d/2.0))
	}

	bestScore, best := -1.0, minBPM
	for b := minBPM; b <= maxBPM+1e-9; b += 0.1 {
		sc := r(b) * prior(b) * density(b)
		if sc > bestScore {
			bestScore, best = sc, b
		}
	}
	bestR, fine := -1.0, best
	for b := best - 0.15; b <= best+0.15+1e-9; b += 0.01 {
		if v := r(b); v > bestR {
			bestR, fine = v, b
		}
	}
	s := sum(fine)
	p := 60.0 / fine
	ph := math.Mod(cmplx.Phase(s)/(2*math.Pi)*p, p)
	if ph < 0 {
		ph += p
	}
	return fine, ph, bestR
}

// BeatToSec converts a (fractional) beat index to a time.
func (m *Map) BeatToSec(beat float64) float64 {
	t := 0.0
	for i, p := range m.Points {
		if i+1 < len(m.Points) && beat >= float64(m.Points[i+1].Beat) {
			t += float64(m.Points[i+1].Beat-p.Beat) * 60.0 / p.BPM
			continue
		}
		return t + (beat-float64(p.Beat))*60.0/p.BPM
	}
	return t
}
