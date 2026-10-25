package tempo

import (
	"math"
	"sort"
)

// BeatInfo describes the outcome of Track.
type BeatInfo struct {
	Tracked int     // beats found by the tracker
	Points  int     // tempo points of the resulting map
	ConstMs float64 // mean distance of kick/snare to the nearest eighth line, constant grid
	MapMs   float64 // the same for the tracked map
	UsedMap bool
	Reason  string
}

type segment struct {
	start int
	bpm   float64
}

// mergeBeats turns beat times into as few constant-tempo segments as possible; inside a
// segment no beat deviates more than tol seconds from the straight grid.
func mergeBeats(b []float64, tol float64) []segment {
	var out []segment
	s, nb := 0, len(b)
	for s < nb-1 {
		m := 1
		best := b[s+1] - b[s]
		for s+m+1 < nb {
			t := (b[s+m+1] - b[s]) / float64(m+1)
			ok := true
			for i := 0; i <= m+1; i++ {
				if math.Abs(b[s+i]-(b[s]+float64(i)*t)) > tol {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
			m++
			best = t
		}
		out = append(out, segment{start: s, bpm: 60.0 / best})
		s += m
	}
	return out
}

// halfBeatMs is the mean distance (ms) of times from the nearest eighth-note line of map m.
func halfBeatMs(m *Map, times []float64, bpm0 float64) float64 {
	if len(times) == 0 {
		return 0
	}
	var sum float64
	for _, t := range times {
		x := m.SecToBeat(t) * 2
		sum += math.Abs(x - math.Round(x))
	}
	return sum / float64(len(times)) * (60.0 / bpm0 / 2.0) * 1000.0
}

// Track follows the beats of a live (non-constant) drummer with dynamic programming over the
// kick/snare hits (seconds, song time) and returns a tempo map whose beat lines follow the
// music. Notes are not moved: only the grid is. bpm0 is the global tempo estimate, leadin the
// silence added in front of the audio. constant is the fallback map; it is returned when the
// tracked map does not fit the hits at least 10% better.
func Track(fit []float64, bpm0, duration, leadin float64, constant *Map) (*Map, BeatInfo) {
	var info BeatInfo
	if len(fit) < 16 || bpm0 <= 0 || duration <= 0 {
		info.Reason = "too few kick/snare hits"
		return constant, info
	}
	sorted := append([]float64(nil), fit...)
	sort.Float64s(sorted)

	p0 := 60.0 / bpm0
	const res = 0.01
	n := int(duration/res) + 2
	o := make([]float64, n)
	for _, t := range sorted {
		k := int(math.Round(t / res))
		for d := -4; d <= 4; d++ {
			if k+d >= 0 && k+d < n {
				x := float64(d) * res / 0.02
				o[k+d] += math.Exp(-0.5 * x * x)
			}
		}
	}
	mx := 0.0
	for _, v := range o {
		if v > mx {
			mx = v
		}
	}
	if mx <= 0 {
		info.Reason = "no onsets"
		return constant, info
	}
	for i := range o {
		o[i] /= mx
	}

	lo := int(0.7 * p0 / res)
	hi := int(1.4 * p0 / res)
	if lo < 1 {
		lo = 1
	}
	if hi <= lo || n <= hi {
		info.Reason = "tempo out of range for tracking"
		return constant, info
	}
	pen := make([]float64, hi-lo+1)
	for i := range pen {
		x := math.Log(float64(lo+i) * res / p0)
		pen[i] = 40.0 * x * x
	}
	c := append([]float64(nil), o...)
	back := make([]int, n)
	for i := range back {
		back[i] = -1
	}
	for t := lo; t < n; t++ {
		bestV, bestTau := -1e18, -1
		for i := range pen {
			tau := lo + i
			if t-tau < 0 {
				break
			}
			if v := c[t-tau] - pen[i]; v > bestV {
				bestV, bestTau = v, tau
			}
		}
		if bestTau >= 0 {
			c[t] = o[t] + bestV
			back[t] = t - bestTau
		}
	}
	from := n - int(2*p0/res)
	if from < 0 {
		from = 0
	}
	end, endV := from, -1e18
	for t := from; t < n; t++ {
		if c[t] > endV {
			end, endV = t, c[t]
		}
	}
	var beats []float64
	for t := end; t >= 0; t = back[t] {
		beats = append(beats, float64(t)*res)
		if back[t] < 0 {
			break
		}
	}
	for i, j := 0, len(beats)-1; i < j; i, j = i+1, j-1 {
		beats[i], beats[j] = beats[j], beats[i]
	}
	info.Tracked = len(beats)
	if len(beats) < 8 {
		info.Reason = "too few beats tracked"
		return constant, info
	}

	// pull every beat to the centroid of the kick/snare hits next to it (+-30 ms)
	for i, b := range beats {
		var sum float64
		cnt := 0
		j := sort.SearchFloat64s(sorted, b-0.03)
		for ; j < len(sorted) && sorted[j] <= b+0.03; j++ {
			sum += sorted[j]
			cnt++
		}
		if cnt > 0 {
			beats[i] = sum / float64(cnt)
		}
	}
	// smooth the beat intervals (moving average over 5 beats)
	iv := make([]float64, len(beats)-1)
	for i := range iv {
		iv[i] = beats[i+1] - beats[i]
	}
	sm := make([]float64, len(iv))
	for i := range iv {
		var s float64
		cnt := 0
		for k := i - 2; k <= i+2; k++ {
			kk := k
			if kk < 0 {
				kk = 0
			}
			if kk >= len(iv) {
				kk = len(iv) - 1
			}
			s += iv[kk]
			cnt++
		}
		sm[i] = s / float64(cnt)
	}
	smooth := make([]float64, len(beats))
	smooth[0] = beats[0]
	for i := range sm {
		smooth[i+1] = smooth[i] + sm[i]
	}

	segs := mergeBeats(smooth, 0.008)
	if len(segs) == 0 {
		info.Reason = "no tempo segments"
		return constant, info
	}

	// pre-roll: whole beats at bpm0, then one stretched beat that lands on the first tracked beat
	b0 := smooth[0] + leadin
	nPre := int(math.Round(b0 / p0))
	if nPre < 1 {
		nPre = 1
	}
	l := b0 - float64(nPre-1)*p0
	if l < 0.5*p0 && nPre > 1 {
		nPre--
		l += p0
	}
	if l < 0.25 {
		info.Reason = "first beat too close to the start"
		return constant, info
	}
	var pts []Point
	if nPre-1 > 0 {
		pts = append(pts, Point{Beat: 0, BPM: bpm0})
	}
	pts = append(pts, Point{Beat: nPre - 1, BPM: 60.0 / l})
	for _, sg := range segs {
		pts = append(pts, Point{Beat: nPre + sg.start, BPM: sg.bpm})
	}
	tracked := &Map{Points: pts}

	// decide: keep the tracked map only if it clearly fits the hits better
	shifted := make([]float64, len(sorted))
	for i, t := range sorted {
		shifted[i] = t + leadin
	}
	info.ConstMs = halfBeatMs(constant, shifted, bpm0)
	info.MapMs = halfBeatMs(tracked, shifted, bpm0)
	info.Points = len(pts)
	if info.MapMs <= 0.9*info.ConstMs {
		info.UsedMap = true
		return tracked, info
	}
	info.Reason = "the tracked map did not fit better than the constant tempo"
	return constant, info
}
