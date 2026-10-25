package refine

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/drumai/pata2fla/internal/decay"
)

type DecisionStats struct {
	AdtofKick     int
	AdtofSnare    int
	AdtofHhClosed int
	AdtofHhOpen   int
	AdtofHhPedal  int
	AdtofTtHigh   int
	AdtofTtMid    int
	AdtofTtFloor  int
	AdtofCyCrash  int
	AdtofCyRide   int

	DecayCyCrash  int
	DecayCyRide   int
	DecayHhClosed int
	DecayHhOpen   int
	DecayTtFloor  int
	DecayTtMid    int
	DecayTtHigh   int

	SpecCyCrash  int
	SpecCyRide   int
	SpecHhClosed int
	SpecHhOpen   int
	SpecHhPedal  int
	SpecTtHigh   int
	SpecTtMid    int
	SpecTtFloor  int

	ArbCyCrash  int
	ArbCyRide   int
	ArbCyClosed int
	ArbCyOpen   int
	ArbCyPedal  int

	SpecPass1HighConfidence   int
	SpecPass2PhysicsConfirmed int
	SpecPass3PhysicsConflict  int

	ArbPass1HighConfidence   int
	ArbPass2PhysicsConfirmed int
	ArbPass3Overrides        int

	UnanimousDecisions int
	ArbiterOverrides   int

	ArbResolvedOpenHatVsCrash  int
	ArbResolvedClosedHatVsRide int
	SpecPedalHatProtected      int

	DecayPriorStickyRide     int
	DecayPriorCrashLiberated int
	DenseOstinatoForcedRide  int
}

type ModelMetaItem struct {
	Mean  float32  `json:"mean"`
	Std   float32  `json:"std"`
	Names []string `json:"names"`
}

type Manager struct {
	Stats            *DecisionStats
	DecayAnalyzer    *decay.Analyzer
	ModelsMeta       map[string]ModelMetaItem
	LastCymbalTime   float64
	LastCymbalChoice string
	PedalThreshold   float32
	// Arbiter confidence needed to turn a CYMBAL into an open hat (original: 0.75).
	OpenThreshold float32
	// Arbiter confidence needed to turn a HAT into a crash (original: 0.70).
	CrashThreshold float32
	// A cymbal closer than this to the previous cymbal belongs to an ostinato -> ride (original: 0.280 s).
	OstinatoSec float64
}

func NewManager(modelsDir string) *Manager {
	m := &Manager{
		Stats:            &DecisionStats{},
		DecayAnalyzer:    decay.NewAnalyzer(),
		ModelsMeta:       make(map[string]ModelMetaItem),
		LastCymbalTime:   -10.0,
		LastCymbalChoice: "ride",
		PedalThreshold:   0.40,
		OpenThreshold:    0.75,
		CrashThreshold:   0.70,
		OstinatoSec:      0.280,
	}

	metaFile := filepath.Join(modelsDir, "models_meta.json")
	if data, err := os.ReadFile(metaFile); err == nil {
		_ = json.Unmarshal(data, &m.ModelsMeta)
		var th struct {
			Thresholds struct {
				Pedal      *float32 `json:"pedal"`
				Open       *float32 `json:"open"`
				Crash      *float32 `json:"crash"`
				OstinatoMs *float64 `json:"ostinato_ms"`
			} `json:"thresholds"`
		}
		if json.Unmarshal(data, &th) == nil {
			if th.Thresholds.Pedal != nil {
				m.PedalThreshold = *th.Thresholds.Pedal
			}
			if th.Thresholds.Open != nil {
				m.OpenThreshold = *th.Thresholds.Open
			}
			if th.Thresholds.Crash != nil {
				m.CrashThreshold = *th.Thresholds.Crash
			}
			if th.Thresholds.OstinatoMs != nil && *th.Thresholds.OstinatoMs > 0 {
				m.OstinatoSec = *th.Thresholds.OstinatoMs / 1000.0
			}
		}
	}

	return m
}

// ---------------------------------------------------------------------------
// The decision logic below is a 1:1 port of the Groovy TriSpecialistRefineManager.
// Missing models give 0.0 probabilities (Groovy getOrDefault), exactly like the original.
// ---------------------------------------------------------------------------

func at(p []float32, i int) float32 {
	if i < len(p) {
		return p[i]
	}
	return 0
}

func pitchIn(p int, set ...int) bool {
	for _, v := range set {
		if p == v {
			return true
		}
	}
	return false
}

// get reads one class by its canonical name; a missing model / class gives 0.0.
func get(p map[string]float32, name string) float32 {
	if p == nil {
		return 0
	}
	return p[name]
}

func absf(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func (m *Manager) finishCymbal(result string, timeSec float64) string {
	if timeSec >= 0 {
		m.LastCymbalTime = timeSec
	}
	m.LastCymbalChoice = result
	return result
}

// RefineCymbals decides ride / crash (or a hat when the arbiter insists).
func (m *Manager) RefineCymbals(probs *CnnProbs, feat *decay.WindowFeatures, adtofPitch int, timeSec float64) string {
	isAdtofCrash := pitchIn(adtofPitch, 49, 52, 55, 57)
	isAdtofRide := pitchIn(adtofPitch, 51, 53, 59)

	dt := 999.0
	if timeSec >= 0 && m.LastCymbalTime >= 0 {
		dt = timeSec - m.LastCymbalTime
	}
	isDenseOstinato := dt < m.OstinatoSec

	decayPrior := "ride"
	if feat != nil {
		decayPrior = m.DecayAnalyzer.GetDecayPrior(feat)
	} else if isAdtofCrash {
		decayPrior = "crash"
	}
	isDecayCrash := decayPrior == "crash"
	isDecayRide := decayPrior == "ride"
	if isDecayCrash {
		m.Stats.DecayCyCrash++
	} else {
		m.Stats.DecayCyRide++
	}

	// cy_best specialist
	var pRideSpec, pCrashSpec float32
	if probs != nil && probs.Spec != nil {
		pRideSpec, pCrashSpec = get(probs.Spec, "ride"), get(probs.Spec, "crash")
		if pCrashSpec > pRideSpec {
			m.Stats.SpecCyCrash++
		} else {
			m.Stats.SpecCyRide++
		}
	}
	specChoice := "ride"
	if pCrashSpec > pRideSpec {
		specChoice = "crash"
	}
	if absf(pCrashSpec-pRideSpec) >= 0.5 {
		m.Stats.SpecPass1HighConfidence++
	}
	if (specChoice == "crash" && isDecayCrash) || (specChoice == "ride" && isDecayRide) {
		m.Stats.SpecPass2PhysicsConfirmed++
	}
	if (specChoice == "crash" && isDecayRide) || (specChoice == "ride" && isDecayCrash) {
		m.Stats.SpecPass3PhysicsConflict++
	}

	// cy_arbiter (5 classes)
	var pRideArb, pCrashArb, pClosedArb, pOpenArb float32
	if probs != nil && probs.Arb != nil {
		pRideArb, pCrashArb = get(probs.Arb, "ride"), get(probs.Arb, "crash")
		pClosedArb, pOpenArb = get(probs.Arb, "closed_hat"), get(probs.Arb, "open_hat")
		if pCrashArb > pRideArb {
			m.Stats.ArbCyCrash++
		} else {
			m.Stats.ArbCyRide++
		}
	}
	arbChoice := "ride"
	if pCrashArb > pRideArb {
		arbChoice = "crash"
	}
	if absf(pCrashArb-pRideArb) >= 0.4 {
		m.Stats.ArbPass1HighConfidence++
	}
	if (arbChoice == "crash" && isDecayCrash) || (arbChoice == "ride" && isDecayRide) {
		m.Stats.ArbPass2PhysicsConfirmed++
	}
	if arbChoice != specChoice {
		m.Stats.ArbPass3Overrides++
	}

	// 1) cymbal that the arbiter insists is an open hat (never when the physics says crash)
	if !isDecayCrash && feat != nil && feat.Decay200 < 0.15 && pOpenArb >= m.OpenThreshold && pOpenArb > pCrashArb+pRideArb+0.30 {
		m.Stats.ArbResolvedOpenHatVsCrash++
		m.Stats.ArbiterOverrides++
		return m.finishCymbal("open_hat", timeSec)
	}
	// 2) ... or a closed hat
	if !isDecayCrash && feat != nil && feat.Decay40 <= 0.18 && feat.Decay100 <= 0.12 && pClosedArb >= 0.75 && pClosedArb > pRideArb+0.30 {
		m.Stats.ArbResolvedClosedHatVsRide++
		m.Stats.ArbiterOverrides++
		return m.finishCymbal("closed_hat", timeSec)
	}
	// 3) sticky ride: a dry ping stays a ride
	if isDecayRide {
		m.Stats.DecayPriorStickyRide++
		m.Stats.UnanimousDecisions++
		return m.finishCymbal("ride", timeSec)
	}
	// 4) dense ostinato (< 280 ms from the previous cymbal) -> ride, except a massive accent
	if isDenseOstinato {
		isMassiveCrashAccent := feat != nil && feat.Peak >= 0.40 && feat.Decay200 >= 0.36 && (pCrashArb >= 0.30 || pCrashSpec >= 0.25)
		if !isMassiveCrashAccent {
			m.Stats.DenseOstinatoForcedRide++
			m.Stats.UnanimousDecisions++
			return m.finishCymbal("ride", timeSec)
		}
	}
	// 5) physics liberates the crash even when cy_best votes ride
	if isDecayCrash && !isDenseOstinato {
		m.Stats.DecayPriorCrashLiberated++
		m.Stats.ArbiterOverrides++
		return m.finishCymbal("crash", timeSec)
	}
	// 6) weighted vote
	effectiveCrashArb := pCrashArb + pOpenArb*0.25
	effectiveCrashSpec := pCrashSpec * 2.5
	var decayCrashBonus, decayRideBonus, adtofCrashBonus, adtofRideBonus float32
	if feat != nil && feat.Decay200 >= 0.24 {
		decayCrashBonus = 0.7
	}
	if feat != nil && feat.Decay200 < 0.24 {
		decayRideBonus = 0.5
	}
	if isAdtofCrash {
		adtofCrashBonus = 0.35
	}
	if isAdtofRide {
		adtofRideBonus = 0.3
	}
	crashScore := effectiveCrashArb*1.4 + effectiveCrashSpec*1.0 + decayCrashBonus + adtofCrashBonus
	rideScore := pRideArb*1.0 + pRideSpec*1.0 + decayRideBonus + adtofRideBonus
	finalChoice := "ride"
	if crashScore >= rideScore {
		finalChoice = "crash"
	}
	if finalChoice != specChoice {
		m.Stats.ArbiterOverrides++
	} else {
		m.Stats.UnanimousDecisions++
	}
	return m.finishCymbal(finalChoice, timeSec)
}

// RefineHiHat decides closed / open / pedal hat (or a crash the arbiter is sure about).
func (m *Manager) RefineHiHat(probs *CnnProbs, feat *decay.WindowFeatures, adtofPitch int) string {
	adtofHat := "closed_hat"
	if adtofPitch == 46 {
		adtofHat = "open_hat"
	} else if adtofPitch == 44 {
		adtofHat = "pedal_hat"
	}

	var isDecayClosed, isDecayOpen bool
	if feat != nil {
		isDecayClosed = feat.Decay40 <= 0.22 && feat.Decay100 <= 0.14
		isDecayOpen = feat.Decay100 >= 0.20 && feat.HighRatio >= 0.28
	} else {
		isDecayClosed = adtofHat == "closed_hat"
		isDecayOpen = adtofHat == "open_hat"
	}
	if isDecayClosed {
		m.Stats.DecayHhClosed++
	} else if isDecayOpen {
		m.Stats.DecayHhOpen++
	}

	// hh_best specialist
	var pClosedSpec, pOpenSpec, pPedalSpec float32
	if probs != nil && probs.Spec != nil {
		pClosedSpec, pOpenSpec = get(probs.Spec, "closed_hat"), get(probs.Spec, "open_hat")
		pPedalSpec = get(probs.Spec, "pedal_hat")
	}
	specChoice := "closed_hat"
	if pPedalSpec >= m.PedalThreshold {
		specChoice = "pedal_hat"
	} else if pOpenSpec > pClosedSpec {
		specChoice = "open_hat"
	}
	if probs != nil && probs.Spec != nil {
		switch specChoice {
		case "pedal_hat":
			m.Stats.SpecHhPedal++
		case "open_hat":
			m.Stats.SpecHhOpen++
		default:
			m.Stats.SpecHhClosed++
		}
	}
	maxSpec := pClosedSpec
	if pOpenSpec > maxSpec {
		maxSpec = pOpenSpec
	}
	if pPedalSpec > maxSpec {
		maxSpec = pPedalSpec
	}
	if maxSpec >= 0.7 {
		m.Stats.SpecPass1HighConfidence++
	}
	if (specChoice == "closed_hat" && isDecayClosed) || (specChoice == "open_hat" && isDecayOpen) {
		m.Stats.SpecPass2PhysicsConfirmed++
	}
	if (specChoice == "open_hat" && isDecayClosed) || (specChoice == "closed_hat" && isDecayOpen) {
		m.Stats.SpecPass3PhysicsConflict++
	}

	// cy_arbiter (5 classes) is consulted for hats too
	var pClosedArb, pOpenArb, pPedalArb, pCrashArb float32
	if probs != nil && probs.Arb != nil {
		pCrashArb = get(probs.Arb, "crash")
		pClosedArb, pOpenArb = get(probs.Arb, "closed_hat"), get(probs.Arb, "open_hat")
		pPedalArb = get(probs.Arb, "pedal_hat")
		switch {
		case pPedalArb >= m.PedalThreshold:
			m.Stats.ArbCyPedal++
		case pOpenArb > pClosedArb:
			m.Stats.ArbCyOpen++
		default:
			m.Stats.ArbCyClosed++
		}
	}

	// pedal hat: strict threshold, or a dry quiet foot chick with >= 0.45
	isStrictFootChick := feat != nil && feat.Peak < 0.16 && feat.Decay40 <= 0.18 && feat.Decay100 <= 0.12
	if pPedalSpec >= m.PedalThreshold || pPedalArb >= m.PedalThreshold ||
		(isStrictFootChick && (pPedalSpec >= 0.45 || pPedalArb >= 0.45)) {
		m.Stats.SpecPedalHatProtected++
		return "pedal_hat"
	}
	if adtofHat == "pedal_hat" && (pPedalSpec >= 0.5 || pPedalArb >= 0.5) {
		m.Stats.SpecPedalHatProtected++
		return "pedal_hat"
	}
	// a confident arbiter crash with a long tail is not a hat at all
	if pCrashArb >= m.CrashThreshold && pCrashArb > pOpenArb+0.30 && feat != nil && feat.Decay200 >= 0.35 {
		m.Stats.ArbResolvedOpenHatVsCrash++
		return "crash"
	}

	scoreClosed := pClosedArb*0.9 + pClosedSpec*1.3 + boolToFloat(isDecayClosed)*0.5 + boolToFloat(adtofHat == "closed_hat")*0.35
	scoreOpen := pOpenArb*0.9 + pOpenSpec*1.3 + boolToFloat(isDecayOpen)*0.5 + boolToFloat(adtofHat == "open_hat")*0.35
	finalChoice := "closed_hat"
	if scoreOpen > scoreClosed {
		finalChoice = "open_hat"
	}
	if finalChoice != specChoice {
		m.Stats.ArbiterOverrides++
	} else {
		m.Stats.UnanimousDecisions++
	}
	return finalChoice
}

// RefineToms decides floor / mid / high tom: tt_best (rare classes x2.5) + spectral prior.
func (m *Manager) RefineToms(probs *CnnProbs, feat *decay.WindowFeatures, adtofPitch int) string {
	isAdtofFloor := pitchIn(adtofPitch, 41, 43)
	isAdtofHigh := pitchIn(adtofPitch, 48, 50)
	isAdtofMid := pitchIn(adtofPitch, 45, 47)

	var isRmsFloor, isRmsHigh, isRmsMid bool
	if feat != nil {
		lowToMid := 1.0
		if feat.MidRms > 0.00001 {
			lowToMid = feat.LowRms / feat.MidRms
		}
		switch {
		case lowToMid >= 1.25:
			isRmsFloor = true
		case lowToMid <= 0.80:
			isRmsHigh = true
		default:
			isRmsMid = true
		}
	} else {
		isRmsFloor, isRmsHigh, isRmsMid = isAdtofFloor, isAdtofHigh, isAdtofMid
	}
	switch {
	case isRmsFloor:
		m.Stats.DecayTtFloor++
	case isRmsHigh:
		m.Stats.DecayTtHigh++
	default:
		m.Stats.DecayTtMid++
	}

	var pFloorSpec, pMidSpec, pHighSpec float32
	if probs != nil && probs.Spec != nil {
		pFloorSpec, pMidSpec = get(probs.Spec, "floor_tom"), get(probs.Spec, "mid_tom")
		pHighSpec = get(probs.Spec, "high_tom")
		switch {
		case pHighSpec > pMidSpec && pHighSpec > pFloorSpec:
			m.Stats.SpecTtHigh++
		case pFloorSpec > pMidSpec && pFloorSpec > pHighSpec:
			m.Stats.SpecTtFloor++
		default:
			m.Stats.SpecTtMid++
		}
	}

	effectiveFloorSpec := pFloorSpec * 2.5
	effectiveHighSpec := pHighSpec * 2.5
	effectiveMidSpec := pMidSpec * 1.0

	scoreFloor := effectiveFloorSpec + boolToFloat(isRmsFloor)*1.6 + boolToFloat(isAdtofFloor)*0.4
	scoreHigh := effectiveHighSpec + boolToFloat(isRmsHigh)*1.6 + boolToFloat(isAdtofHigh)*0.4
	scoreMid := effectiveMidSpec + boolToFloat(isRmsMid)*1.1 + boolToFloat(isAdtofMid && !isRmsFloor && !isRmsHigh)*0.3

	finalChoice := "mid_tom"
	if scoreFloor >= scoreHigh && scoreFloor >= scoreMid {
		finalChoice = "floor_tom"
	} else if scoreHigh >= scoreMid && scoreHigh >= scoreFloor {
		finalChoice = "high_tom"
	}
	return finalChoice
}

func boolToFloat(b bool) float32 {
	if b {
		return 1.0
	}
	return 0.0
}
