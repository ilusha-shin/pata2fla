package decay

import (
	"math"
)

// WindowFeatures mirrors Python window_features / physics vector fields.
type WindowFeatures struct {
	Decay40   float64 // residual energy ~40 ms / attack
	Decay100  float64 // residual energy ~100 ms / attack
	Decay200  float64 // residual energy ~200 ms / attack
	Peak      float64 // max abs in attack window
	HighRatio float64 // spectral energy >= 6 kHz / total
	LowRms    float64 // band energy 40–300 Hz (toms)
	MidRms    float64 // 300–3000 Hz
	HighRms   float64 // 3000–16000 Hz
	DecayTail float64 // 100–220 ms / attack (legacy compat)
}

type Analyzer struct{}

func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

func bandMeanEnergy(audio []float32, i0 int, ms0, ms1 float64, sr int) float64 {
	a := i0 + int(ms0*0.001*float64(sr))
	b := i0 + int(ms1*0.001*float64(sr))
	if b <= a {
		return 1e-12
	}
	if a < 0 {
		a = 0
	}
	if b > len(audio) {
		b = len(audio)
	}
	if b-a < 4 {
		return 1e-12
	}
	sum := 0.0
	for k := a; k < b; k++ {
		x := float64(audio[k])
		sum += x * x
	}
	return sum/float64(b-a) + 1e-12
}

// ExtractFeatures matches Python window_features: RAW energy ratios (not HP-filtered).
func (a *Analyzer) ExtractFeatures(audio []float32, timeSec float64, sampleRate int) *WindowFeatures {
	if sampleRate <= 0 || len(audio) == 0 {
		return nil
	}
	i0 := int(timeSec * float64(sampleRate))
	if i0 < 0 || i0 >= len(audio) {
		return nil
	}

	// Attack 0–40 ms (Python)
	a1 := i0 + int(0.040*float64(sampleRate))
	if a1 > len(audio) {
		a1 = len(audio)
	}
	if a1-i0 < 8 {
		return nil
	}
	eAtt := 0.0
	maxPeak := 0.0
	for k := i0; k < a1; k++ {
		x := float64(audio[k])
		eAtt += x * x
		if ax := math.Abs(x); ax > maxPeak {
			maxPeak = ax
		}
	}
	eAtt = eAtt/float64(a1-i0) + 1e-12

	d40 := bandMeanEnergy(audio, i0, 30, 50, sampleRate) / eAtt
	d100 := bandMeanEnergy(audio, i0, 90, 110, sampleRate) / eAtt
	d200 := bandMeanEnergy(audio, i0, 190, 210, sampleRate) / eAtt
	decayTail := bandMeanEnergy(audio, i0, 100, 220, sampleRate) / eAtt

	// Spectrum on short window for high_ratio + tom bands
	nFFT := 512
	seg := make([]float64, nFFT)
	for i := 0; i < nFFT; i++ {
		idx := i0 + i
		if idx >= len(audio) {
			break
		}
		w := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(nFFT-1)))
		seg[i] = float64(audio[idx]) * w
	}
	re := append([]float64{}, seg...)
	im := make([]float64, nFFT)
	fft(re, im)

	srF := float64(sampleRate)
	total, high, lowBand, midBand, highBand := 1e-12, 0.0, 0.0, 0.0, 0.0
	half := nFFT/2 + 1
	for k := 0; k < half; k++ {
		pwr := re[k]*re[k] + im[k]*im[k]
		freq := float64(k) * srF / float64(nFFT)
		total += pwr
		if freq >= 6000 {
			high += pwr
		}
		if freq >= 40 && freq <= 300 {
			lowBand += pwr
		} else if freq > 300 && freq <= 3000 {
			midBand += pwr
		} else if freq > 3000 && freq <= 16000 {
			highBand += pwr
		}
	}

	return &WindowFeatures{
		Decay40:   d40,
		Decay100:  d100,
		Decay200:  d200,
		Peak:      maxPeak,
		HighRatio: high / total,
		LowRms:    lowBand / total,
		MidRms:    midBand / total,
		HighRms:   highBand / total,
		DecayTail: decayTail,
	}
}

// GetDecayPrior — Demucs-friendly thresholds.
// Raw 0.22/0.26 marked ~all CY as crash on compressed stems.
func (a *Analyzer) GetDecayPrior(feat *WindowFeatures) string {
	if feat == nil {
		return "ride"
	}
	d40, d100, d200 := feat.Decay40, feat.Decay100, feat.Decay200
	peak, high := feat.Peak, feat.HighRatio

	if d100 <= 0.16 && d200 <= 0.14 {
		if high >= 0.42 && d40 > 0.10 {
			return "open_hat"
		}
		return "closed_hat"
	}
	// true crash only with strong residual + peak
	if d200 >= 0.45 && peak >= 0.22 {
		return "crash"
	}
	if d200 >= 0.55 {
		return "crash"
	}
	return "ride"
}

func fft(re, im []float64) {
	n := len(re)
	j := 0
	for i := 0; i < n-1; i++ {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
		k := n >> 1
		for k <= j {
			j -= k
			k >>= 1
		}
		j += k
	}
	for l := 2; l <= n; l <<= 1 {
		ang := -2.0 * math.Pi / float64(l)
		wstepR := math.Cos(ang)
		wstepI := math.Sin(ang)
		for i := 0; i < n; i += l {
			wR, wI := 1.0, 0.0
			half := l >> 1
			for k := 0; k < half; k++ {
				u := i + k
				v := u + half
				tr := wR*re[v] - wI*im[v]
				ti := wR*im[v] + wI*re[v]
				re[v] = re[u] - tr
				im[v] = im[u] - ti
				re[u] += tr
				im[u] += ti
				nextWr := wR*wstepR - wI*wstepI
				wI = wR*wstepI + wI*wstepR
				wR = nextWr
			}
		}
	}
}
