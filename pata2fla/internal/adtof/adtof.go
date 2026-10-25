// Package adtof runs the ADTOF drum-onset network (ONNX) on the isolated drum stem.
//
// Front end (ADTOF paper): 44.1 kHz, Hann window 2048, hop 441 (100 fps), triangular
// log-frequency filterbank with 12 bands/octave between 20 and 20000 Hz (84 bins),
// log10(1 + x). Network input: 400 frames x 84 bins. Output: 5 classes per frame.
// Everything can be overridden in models/adtof_meta.json.
package adtof

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/drumai/pata2fla/internal/dsp"
	"github.com/drumai/pata2fla/internal/ortx"
)

// RawOnset is one detected hit. Class is one of "kick", "snare", "tom", "hihat", "cymbal"
// for the 5-class ADTOF output (Pitch is then only a representative MIDI note); Class is
// empty for the crude fallback detector, where Pitch decides.
type RawOnset struct {
	TimeSec  float64
	Pitch    int
	Velocity int
	Class    string
}

// Config describes the ADTOF front end and peak picking.
type Config struct {
	SR             int                `json:"sr"`
	FrameSize      int                `json:"frame_size"`
	Hop            int                `json:"hop"`
	BandsPerOctave int                `json:"bands_per_octave"`
	FMin           float64            `json:"fmin"`
	FMax           float64            `json:"fmax"`
	NormFilters    bool               `json:"norm_filters"`
	LogAdd         float64            `json:"log_add"`
	Window         int                `json:"window"`
	Classes        []string           `json:"classes"`
	Pitches        map[string]int     `json:"pitches"`
	Threshold      map[string]float64 `json:"threshold"`
	MinGapS        map[string]float64 `json:"min_gap_s"`
}

// DefaultConfig returns the ADTOF defaults.
func DefaultConfig() Config {
	return Config{
		SR: 44100, FrameSize: 2048, Hop: 441, BandsPerOctave: 12, FMin: 20, FMax: 20000,
		NormFilters: true, LogAdd: 1.0, Window: 400,
		Classes:   []string{"kick", "snare", "tom", "hihat", "cymbal"},
		Pitches:   map[string]int{"kick": 36, "snare": 38, "tom": 47, "hihat": 42, "cymbal": 49},
		Threshold: map[string]float64{"kick": 0.30, "snare": 0.30, "tom": 0.30, "hihat": 0.30, "cymbal": 0.30},
		MinGapS:   map[string]float64{"kick": 0.030, "snare": 0.030, "tom": 0.030, "hihat": 0.025, "cymbal": 0.040},
	}
}

// LoadConfig reads models/adtof_meta.json over the defaults (if the file exists).
func LoadConfig(modelsDir string) Config {
	cfg := DefaultConfig()
	p := filepath.Join(modelsDir, "adtof_meta.json")
	if data, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			fmt.Printf("   [!] cannot parse %s: %v (defaults are used)\n", p, err)
			cfg = DefaultConfig()
		} else {
			fmt.Printf("   [i] loaded %s\n", p)
		}
	}
	return cfg
}

type Detector struct {
	ModelsDir string
	// AllowFallback lets the program continue with the crude built-in energy detector
	// when the ADTOF model is unavailable. The fallback finds no kicks and no toms.
	AllowFallback bool
	Threshold     float64 // >0 overrides every class threshold
	// Mode selects the backend: "auto" (adtof.onnx, else the local ADTOF-pytorch install),
	// "onnx" or "local".
	Mode string
	// StemPath is the isolated drum stem WAV (needed by the local backend).
	StemPath string
}

func NewDetector(modelsDir string) *Detector { return &Detector{ModelsDir: modelsDir} }

// ModelPath returns the expected location of the ADTOF ONNX model.
func (d *Detector) ModelPath() string { return filepath.Join(d.ModelsDir, "adtof.onnx") }

// DetectOnsets runs ADTOF on mono (any sample rate; it must equal the stem rate). It
// returns the onsets sorted by time and the name of the backend that produced them.
func (d *Detector) DetectOnsets(mono []float32, sr int) ([]RawOnset, string, error) {
	mode := d.Mode
	if mode == "" {
		mode = "auto"
	}
	var errOnnx, errLocal error

	if mode == "auto" || mode == "onnx" {
		onsets, err := d.runModel(mono, sr)
		if err == nil {
			return onsets, "onnx:adtof", nil
		}
		errOnnx = err
		if mode == "onnx" {
			return d.fallbackOrFail(mono, sr, errOnnx, nil)
		}
		fmt.Printf("   [i] adtof.onnx is not usable (%v)\n", errOnnx)
	}
	onsets, err := d.runLocal(mono, sr)
	if err == nil {
		return onsets, "local:adtof_pytorch", nil
	}
	errLocal = err
	return d.fallbackOrFail(mono, sr, errOnnx, errLocal)
}

func (d *Detector) fallbackOrFail(mono []float32, sr int, errOnnx, errLocal error) ([]RawOnset, string, error) {
	if !d.AllowFallback {
		msg := ""
		if errOnnx != nil {
			msg += fmt.Sprintf("ONNX backend: %v\n", errOnnx)
		}
		if errLocal != nil {
			msg += fmt.Sprintf("local ADTOF-pytorch backend: %v\n", errLocal)
		}
		return nil, "", fmt.Errorf("%s"+
			"ADTOF is required for 5-class onset detection (kick/snare/hat/tom/cymbal).\n"+
			"Create models/adtof.onnx (setup.bat / export_adtof_to_onnx.py), or install ADTOF-pytorch for Python\n"+
			"(pip install git+https://github.com/xavriley/ADTOF-pytorch.git), or pass --allow-fallback for the crude detector", msg)
	}
	fmt.Println("   [!] ADTOF unavailable - using the CRUDE built-in energy detector: no kicks, no toms, poor accuracy!")
	return detectEnergyOnsets(mono, sr), "fallback-energy", nil
}

func (d *Detector) runModel(mono []float32, sr int) ([]RawOnset, error) {
	path := d.ModelPath()
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("ADTOF model not found: %s", path)
	}
	cfg := LoadConfig(d.ModelsDir)
	if d.Threshold > 0 {
		for k := range cfg.Threshold {
			cfg.Threshold[k] = d.Threshold
		}
	}
	if sr != cfg.SR {
		mono = dsp.Resample(mono, sr, cfg.SR)
		sr = cfg.SR
	}

	model, err := ortx.Load(path)
	if err != nil {
		return nil, err
	}
	defer model.Close()

	spec := frontend(mono, cfg)
	fmt.Printf("   spectrogram: %d frames x %d bins\n", spec.Frames, spec.Bins)
	act, err := activations(spec, model, cfg)
	if err != nil {
		return nil, err
	}
	onsets := buildOnsets(act, spec.Frames, mono, sr, cfg)

	counts := map[string]int{}
	for _, o := range onsets {
		counts[o.Class]++
	}
	fmt.Printf("   ADTOF onsets: %d  %v\n", len(onsets), counts)
	return onsets, nil
}

// frontend computes the log-frequency log-magnitude spectrogram.
func frontend(mono []float32, cfg Config) *dsp.Spectrogram {
	mag := dsp.STFT(mono, cfg.FrameSize, cfg.Hop, false)
	fb := dsp.LogFilterbank(cfg.SR, cfg.FrameSize, cfg.BandsPerOctave, cfg.FMin, cfg.FMax, cfg.NormFilters)
	nb := len(fb)
	out := &dsp.Spectrogram{Frames: mag.Frames, Bins: nb, Data: make([]float32, mag.Frames*nb)}
	for f := 0; f < mag.Frames; f++ {
		row := mag.Row(f)
		for k := 0; k < nb; k++ {
			var acc float64
			for b, w := range fb[k] {
				if w != 0 {
					acc += float64(row[b]) * float64(w)
				}
			}
			out.Data[f*nb+k] = float32(math.Log10(acc + cfg.LogAdd))
		}
	}
	return out
}

// detectLayout works out where batch / time / frequency / channel sit in the model input.
func detectLayout(dims []int64, nBins int) (string, error) {
	nb := int64(nBins)
	switch len(dims) {
	case 3:
		if dims[2] == nb || (dims[1] != nb && dims[2] <= 0) {
			return "BTF", nil
		}
		return "BFT", nil
	case 4:
		if dims[3] == 1 && (dims[2] == nb || dims[2] <= 0) && dims[1] != nb {
			return "BTFC", nil
		}
		if dims[1] == 1 {
			if dims[2] == nb && dims[3] != nb {
				return "BCFT", nil
			}
			return "BCTF", nil
		}
		return "BTFC", nil
	}
	return "", fmt.Errorf("unsupported ADTOF input rank %d (shape %v)", len(dims), dims)
}

func layoutAxes(layout string) (tAxis, fAxis int) {
	switch layout {
	case "BTF":
		return 1, 2
	case "BFT":
		return 2, 1
	case "BTFC":
		return 1, 2
	case "BCTF":
		return 2, 3
	default: // BCFT
		return 3, 2
	}
}

// activations returns the frame-level class activations [frames][classes].
func activations(spec *dsp.Spectrogram, model *ortx.Model, cfg Config) ([][]float32, error) {
	nCls := len(cfg.Classes)
	layout, err := detectLayout(model.InDims, spec.Bins)
	if err != nil {
		return nil, err
	}
	tAxis, fAxis := layoutAxes(layout)
	W := cfg.Window
	if model.InDims[tAxis] > 0 {
		W = int(model.InDims[tAxis])
	}
	if model.InDims[fAxis] > 0 && int(model.InDims[fAxis]) != spec.Bins {
		return nil, fmt.Errorf("model expects %d frequency bins but the front end produces %d "+
			"(set bands_per_octave / fmin / fmax in models/adtof_meta.json)", model.InDims[fAxis], spec.Bins)
	}
	fmt.Printf("   ADTOF input '%s' %v -> layout %s, window %d frames\n", model.InName, model.InDims, layout, W)

	T, F := spec.Frames, spec.Bins
	hopw := W / 2
	margin := W / 4
	act := make([][]float32, T)
	for i := range act {
		act[i] = make([]float32, nCls)
	}

	win := make([]float32, W*F) // [W][F]
	for s := 0; s < T; s += hopw {
		for t := 0; t < W; t++ {
			src := s - margin + t
			row := win[t*F : (t+1)*F]
			if src < 0 || src >= T {
				for b := range row {
					row[b] = 0
				}
			} else {
				copy(row, spec.Row(src))
			}
		}
		var in []float32
		var shape []int64
		switch layout {
		case "BTF":
			in, shape = win, []int64{1, int64(W), int64(F)}
		case "BTFC":
			in, shape = win, []int64{1, int64(W), int64(F), 1}
		case "BCTF":
			in, shape = win, []int64{1, 1, int64(W), int64(F)}
		case "BFT", "BCFT":
			tr := make([]float32, W*F) // [F][W]
			for t := 0; t < W; t++ {
				for b := 0; b < F; b++ {
					tr[b*W+t] = win[t*F+b]
				}
			}
			in = tr
			if layout == "BFT" {
				shape = []int64{1, int64(F), int64(W)}
			} else {
				shape = []int64{1, 1, int64(F), int64(W)}
			}
		}
		out, oshape, err := model.Run(shape, in)
		if err != nil {
			return nil, fmt.Errorf("ADTOF inference failed: %v", err)
		}
		probs, Wo, err := reshapeOutput(out, oshape, nCls)
		if err != nil {
			return nil, err
		}
		// keep the central part of the window: frames [margin, margin+hopw)
		for t := margin; t < margin+hopw && t < Wo; t++ {
			dst := s + (t - margin)
			if dst >= T {
				break
			}
			copy(act[dst], probs[t*nCls:(t+1)*nCls])
		}
	}

	// logits -> probabilities
	mn, mx := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	for _, r := range act {
		for _, v := range r {
			if v < mn {
				mn = v
			}
			if v > mx {
				mx = v
			}
		}
	}
	if mn < -1e-3 || mx > 1.0+1e-3 {
		for _, r := range act {
			for i, v := range r {
				r[i] = float32(1.0 / (1.0 + math.Exp(-float64(v))))
			}
		}
	}
	return act, nil
}

// reshapeOutput converts the network output to [time][classes] (flat) and returns the time length.
func reshapeOutput(out []float32, shape []int64, nCls int) ([]float32, int, error) {
	if len(shape) < 3 {
		return nil, 0, fmt.Errorf("unexpected ADTOF output shape %v", shape)
	}
	var X, Y int
	if int(shape[len(shape)-1]) == nCls {
		Y = nCls
		X = len(out) / nCls
	} else {
		X = int(shape[1])
		if X <= 0 {
			return nil, 0, fmt.Errorf("unexpected ADTOF output shape %v", shape)
		}
		Y = len(out) / X
	}
	if Y == nCls {
		return out, X, nil
	}
	if X == nCls { // [C][T] -> [T][C]
		T := Y
		tr := make([]float32, len(out))
		for c := 0; c < nCls; c++ {
			for t := 0; t < T; t++ {
				tr[t*nCls+c] = out[c*T+t]
			}
		}
		return tr, T, nil
	}
	return nil, 0, fmt.Errorf("ADTOF output has no axis with %d classes (shape %v)", nCls, shape)
}

func pickPeaks(a []float32, thr float32, minGap int) []int {
	var cand []int
	for i := 1; i+1 < len(a); i++ {
		if a[i] >= thr && a[i] >= a[i-1] && a[i] > a[i+1] {
			cand = append(cand, i)
		}
	}
	sort.SliceStable(cand, func(i, j int) bool { return a[cand[i]] > a[cand[j]] })
	var taken []int
	for _, c := range cand {
		ok := true
		for _, t := range taken {
			d := c - t
			if d < 0 {
				d = -d
			}
			if d < minGap {
				ok = false
				break
			}
		}
		if ok {
			taken = append(taken, c)
		}
	}
	sort.Ints(taken)
	return taken
}

// loudnessReference returns the 99.5th percentile of |x| (estimated on every 16th sample).
func loudnessReference(mono []float32) float64 {
	absSorted := make([]float32, 0, len(mono)/16+1)
	for i := 0; i < len(mono); i += 16 {
		v := mono[i]
		if v < 0 {
			v = -v
		}
		absSorted = append(absSorted, v)
	}
	sort.Slice(absSorted, func(i, j int) bool { return absSorted[i] < absSorted[j] })
	p99 := 1e-9
	if len(absSorted) > 0 {
		p99 += float64(absSorted[int(float64(len(absSorted)-1)*0.995)])
	}
	return p99
}

func buildOnsets(act [][]float32, T int, mono []float32, sr int, cfg Config) []RawOnset {
	fps := float64(cfg.SR) / float64(cfg.Hop)
	p99 := loudnessReference(mono)

	var onsets []RawOnset
	col := make([]float32, T)
	for ci, name := range cfg.Classes {
		for t := 0; t < T; t++ {
			col[t] = act[t][ci]
		}
		thr := float32(0.3)
		if v, ok := cfg.Threshold[name]; ok {
			thr = float32(v)
		}
		gap := 1
		if v, ok := cfg.MinGapS[name]; ok {
			gap = int(math.Round(v * fps))
			if gap < 1 {
				gap = 1
			}
		}
		for _, f := range pickPeaks(col, thr, gap) {
			tsec := float64(f) / fps
			i0 := int(tsec * float64(sr))
			i1 := i0 + int(0.03*float64(sr))
			if i1 > len(mono) {
				i1 = len(mono)
			}
			peak := 0.0
			for i := i0; i < i1; i++ {
				v := math.Abs(float64(mono[i]))
				if v > peak {
					peak = v
				}
			}
			rel := math.Min(1.0, peak/p99)
			vel := int(math.Round(30 + 97*(0.5*float64(col[f])+0.5*rel)))
			if vel < 1 {
				vel = 1
			}
			if vel > 127 {
				vel = 127
			}
			onsets = append(onsets, RawOnset{
				TimeSec: math.Round(tsec*10000) / 10000, Pitch: cfg.Pitches[name], Velocity: vel, Class: name,
			})
		}
	}
	sort.SliceStable(onsets, func(i, j int) bool { return onsets[i].TimeSec < onsets[j].TimeSec })
	return onsets
}

// detectEnergyOnsets is the emergency fallback (energy gate only).
func detectEnergyOnsets(samples []float32, sampleRate int) []RawOnset {
	hopSize := int(float64(sampleRate) * 0.010)
	windowSize := int(float64(sampleRate) * 0.025)
	if hopSize <= 0 {
		hopSize = 441
	}
	if windowSize <= 0 {
		windowSize = 1103
	}
	var onsets []RawOnset
	lastOnsetSec := -1.0
	for i := 0; i+windowSize < len(samples); i += hopSize {
		tSec := float64(i) / float64(sampleRate)
		if tSec-lastOnsetSec < 0.040 {
			continue
		}
		energy, maxPeak := 0.0, 0.0
		for k := 0; k < windowSize; k++ {
			val := math.Abs(float64(samples[i+k]))
			energy += val * val
			if val > maxPeak {
				maxPeak = val
			}
		}
		rms := math.Sqrt(energy / float64(windowSize))
		if rms > 0.045 && maxPeak > 0.12 {
			pitch := 38
			if maxPeak > 0.35 && tSec > 0.5 {
				pitch = 49
			}
			vel := int(maxPeak * 127.0)
			if vel > 127 {
				vel = 127
			}
			if vel < 40 {
				vel = 70
			}
			onsets = append(onsets, RawOnset{TimeSec: tSec, Pitch: pitch, Velocity: vel})
			lastOnsetSec = tSec
		}
	}
	return onsets
}
