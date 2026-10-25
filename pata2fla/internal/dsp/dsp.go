// Package dsp contains the signal-processing primitives used by the pipeline.
package dsp

import (
	"math"
	"sort"
)

// FFT computes an in-place radix-2 complex FFT. len(re) must be a power of two.
func FFT(re, im []float64) {
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
		half := l >> 1
		for i := 0; i < n; i += l {
			wR, wI := 1.0, 0.0
			for k := 0; k < half; k++ {
				u := i + k
				v := u + half
				tr := wR*re[v] - wI*im[v]
				ti := wR*im[v] + wI*re[v]
				re[v] = re[u] - tr
				im[v] = im[u] - ti
				re[u] += tr
				im[u] += ti
				nwR := wR*wstepR - wI*wstepI
				wI = wR*wstepI + wI*wstepR
				wR = nwR
			}
		}
	}
}

// Hann returns the symmetric Hann window (same as numpy.hanning).
func Hann(n int) []float64 {
	w := make([]float64, n)
	if n == 1 {
		w[0] = 1
		return w
	}
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2.0*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

// Spectrogram holds frames x bins values.
type Spectrogram struct {
	Frames int
	Bins   int
	Data   []float32 // row-major: Data[f*Bins+b]
}

func (s *Spectrogram) At(f, b int) float32 { return s.Data[f*s.Bins+b] }
func (s *Spectrogram) Row(f int) []float32 { return s.Data[f*s.Bins : (f+1)*s.Bins] }

// STFT returns the magnitude (power=false) or power (power=true) spectrogram.
// The signal is zero padded by nFft/2 on both sides (centered frames).
func STFT(x []float32, nFft, hop int, power bool) *Spectrogram {
	pad := nFft / 2
	padded := make([]float32, len(x)+2*pad)
	copy(padded[pad:], x)
	frames := 1 + (len(padded)-nFft)/hop
	if frames < 1 {
		frames = 1
	}
	bins := nFft/2 + 1
	win := Hann(nFft)
	sp := &Spectrogram{Frames: frames, Bins: bins, Data: make([]float32, frames*bins)}
	re := make([]float64, nFft)
	im := make([]float64, nFft)
	for f := 0; f < frames; f++ {
		off := f * hop
		for i := 0; i < nFft; i++ {
			v := 0.0
			if off+i < len(padded) {
				v = float64(padded[off+i])
			}
			re[i] = v * win[i]
			im[i] = 0
		}
		FFT(re, im)
		row := sp.Row(f)
		for b := 0; b < bins; b++ {
			p := re[b]*re[b] + im[b]*im[b]
			if power {
				row[b] = float32(p)
			} else {
				row[b] = float32(math.Sqrt(p))
			}
		}
	}
	return sp
}

// ---------------------------------------------------------------------------
// Filterbanks
// ---------------------------------------------------------------------------

func hzToMel(f float64, htk bool) float64 {
	if htk {
		return 2595.0 * math.Log10(1.0+f/700.0)
	}
	fSp := 200.0 / 3.0
	minLogHz := 1000.0
	logstep := math.Log(6.4) / 27.0
	minLogMel := minLogHz / fSp
	if f >= minLogHz {
		return minLogMel + math.Log(f/minLogHz)/logstep
	}
	return f / fSp
}

func melToHz(m float64, htk bool) float64 {
	if htk {
		return 700.0 * (math.Pow(10.0, m/2595.0) - 1.0)
	}
	fSp := 200.0 / 3.0
	minLogHz := 1000.0
	logstep := math.Log(6.4) / 27.0
	minLogMel := minLogHz / fSp
	if m >= minLogMel {
		return minLogHz * math.Exp(logstep*(m-minLogMel))
	}
	return fSp * m
}

// MelFilterbank builds a Slaney-normalised triangular mel filterbank (librosa default).
// Result: [nMels][nFft/2+1].
func MelFilterbank(sr, nFft, nMels int, fmin, fmax float64, htk bool) [][]float32 {
	bins := nFft/2 + 1
	fftf := make([]float64, bins)
	for i := range fftf {
		fftf[i] = float64(i) * float64(sr) / float64(nFft)
	}
	pts := make([]float64, nMels+2)
	m0, m1 := hzToMel(fmin, htk), hzToMel(fmax, htk)
	for i := range pts {
		pts[i] = melToHz(m0+(m1-m0)*float64(i)/float64(nMels+1), htk)
	}
	fb := make([][]float32, nMels)
	for i := 0; i < nMels; i++ {
		row := make([]float32, bins)
		fd0 := pts[i+1] - pts[i]
		fd1 := pts[i+2] - pts[i+1]
		enorm := 2.0 / (pts[i+2] - pts[i])
		for b := 0; b < bins; b++ {
			lower := -(pts[i] - fftf[b]) / fd0
			upper := (pts[i+2] - fftf[b]) / fd1
			v := math.Min(lower, upper)
			if v < 0 {
				v = 0
			}
			row[b] = float32(v * enorm)
		}
		fb[i] = row
	}
	return fb
}

// LogFilterbank builds the ADTOF/madmom-style triangular log-frequency filterbank with
// unique bins. Result: [nFilters][nFft/2+1].
func LogFilterbank(sr, nFft, bandsPerOctave int, fmin, fmax float64, norm bool) [][]float32 {
	const fref = 440.0
	bins := nFft/2 + 1
	binFreq := make([]float64, bins)
	for i := range binFreq {
		binFreq[i] = float64(i) * float64(sr) / float64(nFft)
	}
	left := int(math.Floor(math.Log2(fmin/fref) * float64(bandsPerOctave)))
	right := int(math.Ceil(math.Log2(fmax/fref) * float64(bandsPerOctave)))
	var freqs []float64
	for k := left; k < right; k++ {
		f := fref * math.Pow(2.0, float64(k)/float64(bandsPerOctave))
		if f >= fmin && f <= fmax {
			freqs = append(freqs, f)
		}
	}
	var idxs []int
	for _, f := range freqs {
		idx := sort.SearchFloat64s(binFreq, f)
		if idx < 1 {
			idx = 1
		}
		if idx > bins-1 {
			idx = bins - 1
		}
		prev, next := binFreq[idx-1], binFreq[idx]
		b := idx
		if math.Abs(f-prev) < math.Abs(f-next) {
			b = idx - 1
		}
		idxs = append(idxs, b)
	}
	// unique, sorted
	var uniq []int
	for i, b := range idxs {
		if i == 0 || b != idxs[i-1] {
			uniq = append(uniq, b)
		}
	}
	var fb [][]float32
	for k := 0; k+2 < len(uniq); k++ {
		start, center, stop := uniq[k], uniq[k+1], uniq[k+2]
		if stop-start < 2 {
			continue
		}
		row := make([]float32, bins)
		rise := center - start
		fall := stop - center
		for i := 0; i < rise; i++ {
			row[start+i] = float32(i) / float32(rise)
		}
		row[center] = 1
		for i := 0; i < fall; i++ {
			row[center+i] = float32(fall-i) / float32(fall)
		}
		row[center] = 1
		if norm {
			var sum float32
			for _, v := range row {
				sum += v
			}
			if sum > 0 {
				for i := range row {
					row[i] /= sum
				}
			}
		}
		fb = append(fb, row)
	}
	return fb
}

// ---------------------------------------------------------------------------
// Resampling
// ---------------------------------------------------------------------------

// Resample converts x from srIn to srOut with a windowed-sinc interpolator.
func Resample(x []float32, srIn, srOut int) []float32 {
	if srIn == srOut || len(x) == 0 {
		return x
	}
	ratio := float64(srOut) / float64(srIn)
	cutoff := math.Min(1.0, ratio) * 0.97
	const half = 24 // taps on each side (in input samples at cutoff 1)
	width := int(math.Ceil(float64(half) / cutoff))
	nOut := int(float64(len(x)) * ratio)
	out := make([]float32, nOut)

	// kernel table: 1024 sub-sample positions
	const res = 1024
	tabLen := (2*width + 1) * res
	table := make([]float32, tabLen+res)
	for i := range table {
		t := float64(i)/float64(res) - float64(width)
		table[i] = float32(sincKernel(t, cutoff, width))
	}

	for n := 0; n < nOut; n++ {
		pos := float64(n) / ratio
		ip := int(math.Floor(pos))
		frac := pos - float64(ip)
		var acc float64
		for k := -width; k <= width+1; k++ {
			j := ip + k
			if j < 0 || j >= len(x) {
				continue
			}
			t := float64(k) - frac // distance of input sample from output position
			ti := (t + float64(width)) * res
			if ti < 0 || ti >= float64(tabLen) {
				continue
			}
			i0 := int(ti)
			f := ti - float64(i0)
			w := float64(table[i0])*(1-f) + float64(table[i0+1])*f
			acc += float64(x[j]) * w
		}
		out[n] = float32(acc)
	}
	return out
}

func sincKernel(t, cutoff float64, width int) float64 {
	if math.Abs(t) > float64(width) {
		return 0
	}
	var s float64
	if t == 0 {
		s = cutoff
	} else {
		s = math.Sin(math.Pi*cutoff*t) / (math.Pi * t)
	}
	w := 0.5 + 0.5*math.Cos(math.Pi*t/float64(width)) // Hann
	return s * w
}

// ---------------------------------------------------------------------------
// Median helper for HPSS
// ---------------------------------------------------------------------------

// MedianOf returns the median of vals (vals is modified).
func MedianOf(vals []float32) float32 {
	// insertion sort: windows are tiny (<= 17)
	for i := 1; i < len(vals); i++ {
		v := vals[i]
		j := i - 1
		for j >= 0 && vals[j] > v {
			vals[j+1] = vals[j]
			j--
		}
		vals[j+1] = v
	}
	return vals[len(vals)/2]
}
