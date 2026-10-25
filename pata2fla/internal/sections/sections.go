// Package sections guesses the form of the drum part (Intro, Part_A, Part_B, Part_A2, Break ...)
// from the hits and the tempo map, so the .chart can carry [Events] section markers.
//
// Method:
//  1. every hit gets a bar number and a slot inside the bar (16th grid or 16th-triplet grid,
//     whichever fits the bar better); the bar becomes a fingerprint: which instrument group
//     played in which slot (all slots on a common 48-slot scale);
//  2. section boundaries are found where the L bars before a bar line differ clearly from the
//     L bars after it (L = window / 2);
//  3. the resulting segments are compared bar by bar; similar segments get the same label,
//     close but different ones become a variant (Part_A2), the rest a new part.
//
// Only the drums are known: a chorus that differs only in vocals/guitar looks like a verse.
package sections

import (
	"math"
	"math/bits"
	"sort"
	"strings"

	"github.com/drumai/pata2fla/internal/nms"
	"github.com/drumai/pata2fla/internal/tempo"
)

// Section is a labelled start of a part. Beat is the absolute beat (quarter note) index, which
// is also the .chart tick divided by the chart resolution.
type Section struct {
	Beat  int
	Label string
}

// Info describes the analysis.
type Info struct {
	DownbeatOffset int // which beat of the tempo map is treated as beat 1 of a bar (0..3)
	Bars           int // bars between the first and the last hit
}

const (
	nGroups   = 7
	sameThr   = 0.90 // >= : the same part
	variantTh = 0.70 // >= : a variant of a part (A2)
	noveltyTh = 0.22 // boundary strength
	minDensit = 3.0  // average occupied (instrument, slot) pairs per bar below this = nearly empty
)

// kick snare hat openhat tom crash ride
var weights = [nGroups]int{3, 3, 1, 2, 2, 3, 2}

type fingerprint [nGroups]uint64

func groupOf(piece string) int {
	switch piece {
	case "kick":
		return 0
	case "snare":
		return 1
	case "closed_hat", "pedal_hat":
		return 2
	case "open_hat":
		return 3
	case "high_tom", "mid_tom", "floor_tom", "low_tom":
		return 4
	case "crash":
		return 5
	case "ride":
		return 6
	}
	return -1
}

type beatEv struct {
	beat  float64
	group int
	piece string
}

// downbeatOffset finds which of the 4 beats is most likely the first beat of a bar:
// kick and crash like beat 1, the snare backbeat sits on beats 2 and 4.
func downbeatOffset(evs []beatEv) int {
	kick := [4]float64{1.0, 0.2, 0.6, 0.2}
	snare := [4]float64{0, 1, 0, 1}
	crash := [4]float64{2, 0, 0, 0}
	best, bestK := -1.0, 0
	for k := 0; k < 4; k++ {
		s := 0.0
		for _, e := range evs {
			r := math.Round(e.beat)
			if math.Abs(e.beat-r) > 0.12 {
				continue
			}
			idx := ((int(r)-k)%4 + 4) % 4
			switch e.piece {
			case "kick":
				s += kick[idx]
			case "snare":
				s += snare[idx]
			case "crash":
				s += crash[idx]
			}
		}
		if s > best+1e-9 {
			best, bestK = s, k
		}
	}
	return bestK
}

type slotEv struct {
	g        int
	pos      float64
	s16, s24 int
}

func fingerprints(evs []beatEv, k int) map[int]*fingerprint {
	perBar := map[int][]slotEv{}
	for _, e := range evs {
		rel := e.beat - float64(k) + 4
		bar := int(math.Floor((rel + 0.125) / 4))
		pos := rel - 4*float64(bar)
		s16 := clampInt(int(math.Round(pos*4)), 0, 15)
		s24 := clampInt(int(math.Round(pos*6)), 0, 23)
		perBar[bar] = append(perBar[bar], slotEv{e.group, pos, s16, s24})
	}
	out := map[int]*fingerprint{}
	for bar, lst := range perBar {
		var r16, r24 float64
		for _, s := range lst {
			d16 := s.pos - float64(s.s16)*0.25
			d24 := s.pos - float64(s.s24)/6.0
			r16 += d16 * d16
			r24 += d24 * d24
		}
		use24 := len(lst) >= 3 && r24 < 0.6*r16
		fp := &fingerprint{}
		for _, s := range lst {
			if use24 {
				fp[s.g] |= 1 << uint(s.s24*2)
			} else {
				fp[s.g] |= 1 << uint(s.s16*3)
			}
		}
		out[bar] = fp
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func occupancy(f *fingerprint) int {
	if f == nil {
		return 0
	}
	n := 0
	for _, m := range f {
		n += bits.OnesCount64(m)
	}
	return n
}

func barSim(a, b *fingerprint) float64 {
	var za, zb fingerprint
	if a == nil {
		a = &za
	}
	if b == nil {
		b = &zb
	}
	inter, union := 0, 0
	for g := 0; g < nGroups; g++ {
		inter += weights[g] * bits.OnesCount64(a[g]&b[g])
		union += weights[g] * bits.OnesCount64(a[g]|b[g])
	}
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

func blockSim(fp map[int]*fingerprint, s1, s2, n int) float64 {
	if n <= 0 {
		return 1
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += barSim(fp[s1+i], fp[s2+i])
	}
	return sum / float64(n)
}

type rep struct {
	label string
	start int
	n     int
}

func partName(i int) string {
	if i < 26 {
		return "Part_" + string(rune('A'+i))
	}
	return "Part_" + string(rune('A'+i/26-1)) + string(rune('A'+i%26))
}

// Detect labels the parts of the song. events and tm must be on the same timeline; bars is the
// comparison window (4, 8 or 16 bars; boundaries use half of it on each side).
func Detect(events []nms.Event, tm *tempo.Map, bars int) ([]Section, Info) {
	var info Info
	if tm == nil || len(events) == 0 {
		return nil, info
	}
	if bars < 4 {
		bars = 8
	}
	l := bars / 2

	var evs []beatEv
	for _, e := range events {
		if g := groupOf(e.Piece); g >= 0 {
			evs = append(evs, beatEv{beat: tm.SecToBeat(e.TimeSec), group: g, piece: e.Piece})
		}
	}
	if len(evs) < 16 {
		return nil, info
	}
	k := downbeatOffset(evs)
	info.DownbeatOffset = k
	fp := fingerprints(evs, k)

	var barList []int
	for b, f := range fp {
		if occupancy(f) >= 2 {
			barList = append(barList, b)
		}
	}
	if len(barList) == 0 {
		return nil, info
	}
	sort.Ints(barList)
	first, last := barList[0], barList[len(barList)-1]
	info.Bars = last - first + 1

	// boundaries: where the l bars before differ from the l bars after
	type cand struct {
		bar int
		nov float64
	}
	var cands []cand
	for i := first + l; i <= last-l+1; i++ {
		if nov := 1 - blockSim(fp, i-l, i, l); nov >= noveltyTh {
			cands = append(cands, cand{i, nov})
		}
	}
	sort.SliceStable(cands, func(a, b int) bool { return cands[a].nov > cands[b].nov })
	var chosen []int
	for _, c := range cands {
		ok := true
		for _, j := range chosen {
			if abs(c.bar-j) < l {
				ok = false
				break
			}
		}
		if ok {
			chosen = append(chosen, c.bar)
		}
	}
	sort.Ints(chosen)
	starts := append([]int{first}, chosen...)
	type seg struct{ start, n int }
	segs := make([]seg, len(starts))
	for i, s := range starts {
		end := last + 1
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		segs[i] = seg{s, end - s}
	}

	// label the segments
	var reps []rep
	variants := map[string]int{}
	nClusters := 0
	labels := make([]string, len(segs))
	lowDensity := make([]bool, len(segs))
	for i, sg := range segs {
		occ := 0
		for j := 0; j < sg.n; j++ {
			occ += occupancy(fp[sg.start+j])
		}
		if float64(occ)/float64(sg.n) < minDensit {
			labels[i], lowDensity[i] = "Break", true
			continue
		}
		bestSim, bestRep := -1.0, -1
		for ri, r := range reps {
			n := minInt(minInt(sg.n, r.n), 16)
			if s := blockSim(fp, sg.start, r.start, n); s > bestSim {
				bestSim, bestRep = s, ri
			}
		}
		switch {
		case bestRep >= 0 && bestSim >= sameThr:
			labels[i] = reps[bestRep].label
		case bestRep >= 0 && bestSim >= variantTh:
			base := strings.TrimRight(reps[bestRep].label, "0123456789")
			variants[base]++
			if variants[base] < 2 {
				variants[base] = 2
			}
			labels[i] = base + itoa(variants[base])
			reps = append(reps, rep{labels[i], sg.start, sg.n})
		default:
			base := partName(nClusters)
			nClusters++
			variants[base] = 1
			labels[i] = base
			reps = append(reps, rep{base, sg.start, sg.n})
		}
	}
	// a short first / last part that never comes back is the intro / outro
	count := map[string]int{}
	for _, lb := range labels {
		count[lb]++
	}
	if len(labels) > 1 {
		if lowDensity[0] || (count[labels[0]] == 1 && segs[0].n <= 8) {
			labels[0] = "Intro"
		}
	}
	if n := len(labels); n > 2 {
		if lowDensity[n-1] || (count[labels[n-1]] == 1 && segs[n-1].n <= 8) {
			labels[n-1] = "Outro"
		}
	}

	var out []Section
	prev := ""
	for i, sg := range segs {
		if labels[i] != prev {
			beat := k - 4 + 4*sg.start
			if beat < 0 {
				beat = 0
			}
			out = append(out, Section{Beat: beat, Label: labels[i]})
		}
		prev = labels[i]
	}
	return out, info
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
