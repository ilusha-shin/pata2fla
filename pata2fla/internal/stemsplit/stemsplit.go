// Package stemsplit isolates the drum stem with the HT-Demucs FT drums specialist (ONNX)
// and verifies that the result really is a drum stem.
package stemsplit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drumai/pata2fla/internal/audio"
	"github.com/drumai/pata2fla/internal/dsp"
	"github.com/drumai/pata2fla/internal/ortx"
)

// SampleRate is fixed by the model.
const SampleRate = 44100

const defaultSegment = 343980 // 7.8 s

// ModelNames are the accepted file names of the model.
var ModelNames = []string{"stemsplit_drums.onnx", "htdemucs_ft_drums.onnx", "stemsplitio.onnx"}

// minModelBytes: the real model is ~316 MB; anything much smaller is a broken download.
const minModelBytes = 100 * 1000 * 1000

type Separator struct {
	ModelsDir string
}

func NewSeparator(modelsDir string) *Separator { return &Separator{ModelsDir: modelsDir} }

// FindModel returns the path of the separation model or "".
func (s *Separator) FindModel() string {
	for _, name := range ModelNames {
		p := filepath.Join(s.ModelsDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Separate returns the drum stem (stereo, 44.1 kHz) of mix (stereo, 44.1 kHz).
// logFn receives progress lines (CLI/GUI); if nil, prints to stdout.
func (s *Separator) Separate(mix *audio.Audio, logFn func(string)) (*audio.Audio, error) {
	if logFn == nil {
		logFn = func(msg string) { fmt.Print(msg) }
	}
	model := s.FindModel()
	if model == "" {
		return nil, fmt.Errorf("drum separation model not found in '%s' (expected %s).\n"+
			"Run setup.bat / setup.sh to download it, or pass --drums-only if the input is already an isolated drum track",
			s.ModelsDir, strings.Join(ModelNames, " or "))
	}
	if st, err := os.Stat(model); err == nil && st.Size() < minModelBytes {
		return nil, fmt.Errorf("model file '%s' is only %d bytes - the download is incomplete.\n"+
			"Delete it and run setup again", model, st.Size())
	}
	logFn(fmt.Sprintf("   [+] Drum separation model: %s\n", filepath.Base(model)))
	m, err := ortx.Load(model)
	if err != nil {
		return nil, fmt.Errorf("cannot load the separation model (corrupted download?): %v", err)
	}
	defer m.Close()
	seg := defaultSegment
	if len(m.InDims) == 3 && m.InDims[2] > 0 {
		seg = int(m.InDims[2])
	}
	total := mix.Frames()
	overlap := seg / 4
	stride := seg - overlap
	nChunks := (total + stride - 1) / stride
	if nChunks < 1 {
		nChunks = 1
	}
	logFn(fmt.Sprintf("   input %.1f s, %d chunks of %.2f s (overlap %.2f s)\n",
		float64(total)/SampleRate, nChunks, float64(seg)/SampleRate, float64(overlap)/SampleRate))
	// transition window
	window := make([]float32, seg)
	for i := range window {
		window[i] = 1
	}
	for i := 0; i < overlap; i++ {
		f := float32(i) / float32(overlap-1)
		window[i] = f
		window[seg-1-i] = f
	}
	out := &audio.Audio{SR: SampleRate, Ch: [][]float32{make([]float32, total), make([]float32, total)}}
	weight := make([]float32, total)
	buf := make([]float32, 2*seg)
	t0 := time.Now()
	for i := 0; i < nChunks; i++ {
		start := i * stride
		end := start + seg
		if end > total {
			end = total
		}
		n := end - start
		for c := 0; c < 2; c++ {
			dst := buf[c*seg : (c+1)*seg]
			copy(dst, mix.Ch[c][start:end])
			for j := n; j < seg; j++ {
				dst[j] = 0
			}
		}
		res, shape, err := m.Run([]int64{1, 2, int64(seg)}, buf)
		if err != nil {
			return nil, fmt.Errorf("separation failed on chunk %d/%d: %v", i+1, nChunks, err)
		}
		if len(res) < 2*seg {
			return nil, fmt.Errorf("unexpected model output shape %v", shape)
		}
		// drums are the first source: res[0 : 2*seg] = [left(seg), right(seg)]
		for j := 0; j < n; j++ {
			w := window[j]
			if i == 0 && j < overlap {
				w = 1
			}
			if i == nChunks-1 && j >= seg-overlap {
				w = 1
			}
			out.Ch[0][start+j] += res[j] * w
			out.Ch[1][start+j] += res[seg+j] * w
			weight[start+j] += w
		}
		el := time.Since(t0).Seconds()
		eta := el / float64(i+1) * float64(nChunks-i-1)
		logFn(fmt.Sprintf("   chunk %d/%d  elapsed %.0fs  eta %.0fs\n", i+1, nChunks, el, eta))
	}
	for j := 0; j < total; j++ {
		w := weight[j]
		if w < 1e-8 {
			w = 1e-8
		}
		out.Ch[0][j] /= w
		out.Ch[1][j] /= w
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Verification
// ---------------------------------------------------------------------------

// Report is the outcome of VerifyDrumStem.
type Report struct {
	Errors   []string
	Warnings []string
	Metrics  []string
}

func sumSq(x []float32) float64 {
	var s float64
	for _, v := range x {
		s += float64(v) * float64(v)
	}
	return s
}

// percussiveRatio returns the share of spectral energy that is percussive (HPSS by median
// filtering, as in librosa), measured on the loudest ~15 s of the signal.
func percussiveRatio(mono []float32) float64 {
	const maxSec = 15
	n := SampleRate * maxSec
	if len(mono) > n {
		bestStart, bestE := 0, -1.0
		for s := 0; s+SampleRate <= len(mono); s += SampleRate {
			e := sumSq(mono[s : s+SampleRate])
			if e > bestE {
				bestE, bestStart = e, s
			}
		}
		if bestStart+n > len(mono) {
			bestStart = len(mono) - n
		}
		mono = mono[bestStart : bestStart+n]
	}
	sp := dsp.STFT(mono, 2048, 512, false)
	F, B := sp.Frames, sp.Bins
	const w = 17
	half := w / 2
	win := make([]float32, w)
	H := make([]float32, F*B)
	P := make([]float32, F*B)
	// H: median along time for every bin
	for b := 0; b < B; b++ {
		for f := 0; f < F; f++ {
			for k := 0; k < w; k++ {
				ff := f - half + k
				if ff < 0 {
					ff = 0
				}
				if ff >= F {
					ff = F - 1
				}
				win[k] = sp.Data[ff*B+b]
			}
			H[f*B+b] = dsp.MedianOf(win)
		}
	}
	// P: median along frequency for every frame
	for f := 0; f < F; f++ {
		for b := 0; b < B; b++ {
			for k := 0; k < w; k++ {
				bb := b - half + k
				if bb < 0 {
					bb = 0
				}
				if bb >= B {
					bb = B - 1
				}
				win[k] = sp.Data[f*B+bb]
			}
			P[f*B+b] = dsp.MedianOf(win)
		}
	}
	var eh, ep float64
	for i := range H {
		eh += float64(H[i]) * float64(H[i])
		ep += float64(P[i]) * float64(P[i])
	}
	return ep / (eh + ep + 1e-20)
}

// VerifyDrumStem checks that drums looks like an isolated drum stem. mix may be nil
// (--drums-only), then only the standalone checks run.
func VerifyDrumStem(drums, mix *audio.Audio) *Report {
	r := &Report{}
	d := drums.Mono()
	for _, ch := range drums.Ch {
		for _, v := range ch {
			if v != v || v > 1e6 || v < -1e6 {
				r.Errors = append(r.Errors, "stem contains NaN/Inf")
				return r
			}
		}
	}
	eDr := sumSq(d)
	rms := sqrt(eDr / float64(len(d)+1))
	peak := 0.0
	for _, ch := range drums.Ch {
		for _, v := range ch {
			a := float64(v)
			if a < 0 {
				a = -a
			}
			if a > peak {
				peak = a
			}
		}
	}
	rmsDb := 20 * log10(rms+1e-12)
	r.Metrics = append(r.Metrics, fmt.Sprintf("drums rms = %.1f dBFS, peak = %.3f", rmsDb, peak))
	if peak < 1e-3 || rmsDb < -70 {
		r.Errors = append(r.Errors, fmt.Sprintf("stem is (almost) silent: rms %.1f dBFS", rmsDb))
	}

	var mixMono []float32
	if mix != nil {
		mixMono = mix.Mono()
		if drums.Frames() != mix.Frames() {
			r.Errors = append(r.Errors, "stem length differs from the input")
		} else {
			eMix := sumSq(mixMono) + 1e-12
			ratio := eDr / eMix
			var dot float64
			for i := range d {
				dot += float64(d[i]) * float64(mixMono[i])
			}
			corr := dot / sqrt(eMix*(eDr+1e-12))
			r.Metrics = append(r.Metrics, fmt.Sprintf("energy ratio stem/mix = %.4f, correlation = %.3f", ratio, corr))
			switch {
			case ratio > 0.9 && corr > 0.99:
				r.Errors = append(r.Errors, fmt.Sprintf(
					"stem is identical to the input mix - separation did not happen (energy ratio %.2f, correlation %.3f)", ratio, corr))
			case ratio > 1.5:
				r.Errors = append(r.Errors, fmt.Sprintf("stem is louder than the whole mix (ratio %.2f) - broken separation", ratio))
			case ratio < 1e-3:
				r.Warnings = append(r.Warnings, fmt.Sprintf(
					"stem carries <0.1%% of the mix energy (ratio %.5f): quiet or almost no drums in this track?", ratio))
			}
		}
	}

	if len(r.Errors) == 0 {
		pr := percussiveRatio(d)
		r.Metrics = append(r.Metrics, fmt.Sprintf("percussive ratio (stem) = %.2f", pr))
		if mixMono != nil {
			pm := percussiveRatio(mixMono)
			r.Metrics = append(r.Metrics, fmt.Sprintf("percussive ratio (mix)  = %.2f", pm))
			if pr < pm-0.02 {
				r.Warnings = append(r.Warnings, fmt.Sprintf(
					"stem is LESS percussive than the full mix (%.2f vs %.2f) - it may not be a pure drum stem", pr, pm))
			}
		}
		if pr < 0.30 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("stem looks tonal rather than percussive (percussive ratio %.2f)", pr))
		}
	}
	return r
}
