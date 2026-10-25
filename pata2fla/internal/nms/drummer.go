package nms

import (
	"sort"
)

type Event struct {
	TimeSec    float64
	Piece      string
	Velocity   int
	Confidence float64
	Lab5       string
	Suppressed bool
}

// SuppressEvents: Python-style NMS — max 2 hands, hat exclusions, allowlist pairs.
func SuppressEvents(events []Event, minInterval float64) []Event {
	if minInterval <= 0 {
		minInterval = 0.04
	}
	if len(events) == 0 {
		return nil
	}
	active := append([]Event{}, events...)
	SortEventsByTime(active)

	hands := map[string]bool{
		"snare": true, "high_tom": true, "mid_tom": true, "low_tom": true, "floor_tom": true,
		"closed_hat": true, "open_hat": true, "crash": true, "ride": true,
	}
	allowedPair := func(a, b string) bool {
		if a > b {
			a, b = b, a
		}
		key := a + "|" + b
		ok := map[string]bool{
			"closed_hat|snare": true, "open_hat|snare": true, "ride|snare": true, "crash|snare": true,
			"closed_hat|high_tom": true, "closed_hat|mid_tom": true, "closed_hat|low_tom": true,
			"closed_hat|floor_tom": true,
			"open_hat|high_tom":    true, "open_hat|mid_tom": true, "open_hat|low_tom": true, "open_hat|floor_tom": true,
			"crash|high_tom": true, "crash|mid_tom": true, "crash|low_tom": true, "crash|floor_tom": true,
			"ride|high_tom": true, "ride|mid_tom": true, "ride|low_tom": true, "ride|floor_tom": true,
			"crash|ride": true, "closed_hat|crash": true, "crash|open_hat": true,
			"closed_hat|ride": true, "open_hat|ride": true,
			"high_tom|snare": true, "mid_tom|snare": true, "low_tom|snare": true, "floor_tom|snare": true,
			"high_tom|mid_tom": true, "high_tom|low_tom": true, "floor_tom|high_tom": true,
			"low_tom|mid_tom": true, "floor_tom|mid_tom": true, "floor_tom|low_tom": true,
		}
		return ok[key]
	}
	score := func(e Event) float64 {
		if e.Confidence > 0 {
			return e.Confidence
		}
		return float64(e.Velocity) / 127.0
	}

	var kept []Event
	i := 0
	n := len(active)
	for i < n {
		group := []Event{active[i]}
		j := i + 1
		for j < n && (active[j].TimeSec-group[0].TimeSec) < minInterval {
			group = append(group, active[j])
			j++
		}

		bestByPiece := map[string]Event{}
		for _, e := range group {
			prev, ok := bestByPiece[e.Piece]
			if !ok || score(e) > score(prev) {
				bestByPiece[e.Piece] = e
			}
		}

		// open vs closed / open vs pedal
		if _, hasOpen := bestByPiece["open_hat"]; hasOpen {
			if _, hasClosed := bestByPiece["closed_hat"]; hasClosed {
				if score(bestByPiece["open_hat"]) >= score(bestByPiece["closed_hat"]) {
					delete(bestByPiece, "closed_hat")
				} else {
					delete(bestByPiece, "open_hat")
				}
			}
			if _, hasPedal := bestByPiece["pedal_hat"]; hasPedal {
				if score(bestByPiece["pedal_hat"]) >= score(bestByPiece["open_hat"]) {
					delete(bestByPiece, "open_hat")
				} else {
					delete(bestByPiece, "pedal_hat")
				}
			}
		}

		// max 2 hands
		var handItems []Event
		for _, e := range bestByPiece {
			if hands[e.Piece] {
				handItems = append(handItems, e)
			}
		}
		if len(handItems) > 2 {
			sort.Slice(handItems, func(a, b int) bool {
				return score(handItems[a]) > score(handItems[b])
			})
			// try best allowed pair among top candidates
			names := make([]string, len(handItems))
			for k, e := range handItems {
				names[k] = e.Piece
			}
			bestA, bestB, bestS := "", "", -1.0
			for a := 0; a < len(names); a++ {
				for b := a + 1; b < len(names); b++ {
					if allowedPair(names[a], names[b]) {
						s := score(bestByPiece[names[a]]) + score(bestByPiece[names[b]])
						if s > bestS {
							bestS = s
							bestA, bestB = names[a], names[b]
						}
					}
				}
			}
			keep := map[string]bool{}
			if bestA != "" {
				keep[bestA] = true
				keep[bestB] = true
			} else {
				keep[handItems[0].Piece] = true
				keep[handItems[1].Piece] = true
			}
			for _, e := range handItems {
				if !keep[e.Piece] {
					delete(bestByPiece, e.Piece)
				}
			}
		} else if len(handItems) == 2 {
			a, b := handItems[0].Piece, handItems[1].Piece
			if !allowedPair(a, b) {
				if score(handItems[0]) >= score(handItems[1]) {
					delete(bestByPiece, b)
				} else {
					delete(bestByPiece, a)
				}
			}
		}

		for _, e := range bestByPiece {
			kept = append(kept, e)
		}
		i = j
	}
	SortEventsByTime(kept)
	return kept
}

func SortEventsByTime(ev []Event) {
	sort.Slice(ev, func(i, j int) bool {
		return ev[i].TimeSec < ev[j].TimeSec
	})
}

// PhysicalDrummerNms wraps SuppressEvents for main.go compatibility.
type PhysicalDrummerNms struct {
	MinInterval float64
}

func NewPhysicalDrummerNms() *PhysicalDrummerNms {
	return &PhysicalDrummerNms{MinInterval: 0.04}
}

func (n *PhysicalDrummerNms) SuppressEvents(events []Event) []Event {
	iv := n.MinInterval
	if iv <= 0 {
		iv = 0.04
	}
	return SuppressEvents(events, iv)
}
