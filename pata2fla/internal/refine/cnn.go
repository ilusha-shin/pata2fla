package refine

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/drumai/pata2fla/internal/decay"
	"github.com/drumai/pata2fla/internal/dsp"
	"github.com/drumai/pata2fla/internal/ortx"
)

// This file is a 1:1 port of the Groovy TransientMelFeatureExtractor + the inference part of
// TriSpecialistRefineManager (runInference / softmax / ModelClassVerifier).

// CnnProbs holds real network outputs for one onset, keyed by the CANONICAL class NAME that
// the ClassVerifier assigned to every output index (never by index):
//
//	Spec: hh {closed_hat, open_hat, pedal_hat} | cy {ride, crash} | tt {floor_tom, mid_tom, high_tom}
//	Arb:  cymbal arbiter {ride, crash, closed_hat, open_hat, pedal_hat}
//
// A nil map means the model was not available; Get then returns 0.0 (Groovy getOrDefault).
type CnnProbs struct {
	Spec map[string]float32
	Arb  map[string]float32
}

// CnnEvent is one onset for the CNN stage. Kind is "hh", "tt" or "cy".
type CnnEvent struct {
	T    float64
	Kind string
	Feat *decay.WindowFeatures // decay/spectral features of the onset (used for the phys input)
}

const (
	melSR     = 44100
	melNFft   = 2048
	melHop    = 512
	melNMels  = 64
	melFMin   = 20.0
	melFMaxCy = 16000.0 // hats / cymbals
	melFMaxTt = 8000.0  // toms
)

// ---------------------------------------------------------------------------
// Mel filterbank (Groovy createMelFilterbank): HTK mel, floor() bins, unnormalised triangles
// ---------------------------------------------------------------------------

func hzToMelHTK(hz float64) float64  { return 2595.0 * math.Log10(1.0+hz/700.0) }
func melToHzHTK(mel float64) float64 { return 700.0 * (math.Pow(10.0, mel/2595.0) - 1.0) }

func createMelFilterbank(sr, nFft, nMels int, fMin, fMax float64) [][]float32 {
	nBins := 1 + nFft>>1
	minMel, maxMel := hzToMelHTK(fMin), hzToMelHTK(fMax)
	bins := make([]int, nMels+2)
	for i := range bins {
		hz := melToHzHTK(minMel + (maxMel-minMel)*(float64(i)/float64(nMels+1)))
		bins[i] = int(math.Floor(float64(nFft+1) * hz / float64(sr)))
	}
	w := make([][]float32, nMels)
	for i := range w {
		w[i] = make([]float32, nBins)
	}
	for m := 1; m <= nMels; m++ {
		fl, fc, fr := bins[m-1], bins[m], bins[m+1]
		for k := fl; k < fc; k++ {
			if fc <= fl {
				break
			}
			if k >= nBins {
				break
			}
			w[m-1][k] = float32(k-fl) / float32(fc-fl)
		}
		for k := fc; k < fr; k++ {
			if fr <= fc {
				break
			}
			if k >= nBins {
				break
			}
			w[m-1][k] = float32(fr-k) / float32(fr-fc)
		}
	}
	return w
}

type melExtractor struct {
	relToMax bool // subtract the patch maximum: values in [-80, 0] dB (librosa power_to_db ref=max)
	fbFull [][]float32
	fbTt   [][]float32
	hann   []float64
}

func newMelExtractor(relToMax bool) *melExtractor {
	e := &melExtractor{
		relToMax: relToMax,
		fbFull: createMelFilterbank(melSR, melNFft, melNMels, melFMin, melFMaxCy),
		fbTt:   createMelFilterbank(melSR, melNFft, melNMels, melFMin, melFMaxTt),
		hann:   make([]float64, melNFft),
	}
	for k := range e.hann {
		e.hann[k] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(k)/float64(melNFft)))
	}
	return e
}

// patch returns a [nMels][numFrames] patch in dB, clamped to [max-80, max] (Groovy extractMelPatch).
func (e *melExtractor) patch(sig []float32, timeSec float64, isToms bool, numFrames int) [][]float32 {
	fb := e.fbFull
	if isToms {
		fb = e.fbTt
	}
	onset := int(math.Round(timeSec * float64(melSR)))
	anchor := 10
	if numFrames <= 20 {
		anchor = 2
	}
	start0 := onset - anchor*melHop

	re := make([]float64, melNFft)
	im := make([]float64, melNFft)
	power := make([]float64, 1+melNFft>>1)
	patch := make([][]float32, melNMels)
	for m := range patch {
		patch[m] = make([]float32, numFrames)
	}
	maxDb := -999.0
	for t := 0; t < numFrames; t++ {
		sPos := start0 + t*melHop
		for k := 0; k < melNFft; k++ {
			re[k], im[k] = 0, 0
			idx := sPos + k
			if idx >= 0 && idx < len(sig) {
				re[k] = float64(sig[idx]) * e.hann[k]
			}
		}
		dsp.FFT(re, im)
		for k := range power {
			power[k] = re[k]*re[k] + im[k]*im[k]
		}
		for m := 0; m < melNMels; m++ {
			sum := 0.0
			row := fb[m]
			for k, wv := range row {
				if wv > 0 {
					sum += power[k] * float64(wv)
				}
			}
			db := -100.0
			if sum > 1e-10 {
				db = 10.0 * math.Log10(sum)
			}
			patch[m][t] = float32(db)
			if db > maxDb {
				maxDb = db
			}
		}
	}
	minDb := float32(maxDb - 80.0)
	for m := range patch {
		for t := range patch[m] {
			if patch[m][t] < minDb {
				patch[m][t] = minDb
			}
			if e.relToMax {
				patch[m][t] -= float32(maxDb)
			}
		}
	}
	return patch
}

// ---------------------------------------------------------------------------
// Class names (ModelClassVerifier)
// ---------------------------------------------------------------------------

func canonicalize(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	has := func(sub string) bool { return strings.Contains(s, sub) }
	switch {
	case s == "ride" || has("cy_ride") || has("cymbal_ride") || has("ride_bell"):
		return "ride"
	case s == "crash" || has("cy_crash") || has("cymbal_crash") || strings.HasPrefix(s, "crash"):
		return "crash"
	case s == "closed_hat" || s == "closed" || has("hh_closed") || has("closed_hihat") || s == "ch":
		return "closed_hat"
	case s == "open_hat" || s == "open" || has("hh_open") || has("open_hihat") || s == "oh":
		return "open_hat"
	case s == "pedal_hat" || s == "pedal" || has("hh_pedal") || has("pedal_hihat") || s == "ph" || s == "foot":
		return "pedal_hat"
	case s == "floor_tom" || s == "floor" || has("tom_floor") || s == "low_tom" || s == "ft" || has("floor"):
		return "floor_tom"
	case s == "mid_tom" || s == "mid" || has("tom_mid") || s == "rack2" || s == "t2" || s == "mt":
		return "mid_tom"
	case s == "high_tom" || s == "high" || has("tom_high") || s == "rack1" || s == "t1" || s == "ht":
		return "high_tom"
	}
	return s
}

type cnnSpec struct {
	key     string   // model file / meta key
	order   []string // canonical output order requested by the manager
	classes int
}

var (
	specHH  = cnnSpec{"hh_best", []string{"closed_hat", "open_hat", "pedal_hat"}, 3}
	specCY  = cnnSpec{"cy_best", []string{"ride", "crash"}, 2}
	specTT  = cnnSpec{"tt_best", []string{"high_tom", "mid_tom", "floor_tom"}, 3}
	specARB = cnnSpec{"cy_arbiter", []string{"ride", "crash", "closed_hat", "open_hat", "pedal_hat"}, 5}
)

type physSpec struct {
	Features []string  `json:"features"` // order of the phys vector, see featureValue
	Mean     []float32 `json:"mean"`
	Std      []float32 `json:"std"`
}

type metaItem struct {
	Mean     float32           `json:"mean"`
	Std      float32           `json:"std"`
	Names    []string          `json:"names"`
	ClassMap map[string]string `json:"class_map"`
	// Nested form (optional): "phys": {"features":[...],"mean":[...],"std":[...]}
	Phys *physSpec `json:"phys"`
	// Flat form from export_to_onnx_n.py / train_refine_phys checkpoints:
	PhysMean []float32 `json:"phys_mean"`
	PhysStd  []float32 `json:"phys_std"`
	PhysDim  int       `json:"phys_dim"`
	Arch     string    `json:"arch"`
}

type frontendMeta struct {
	DbReference string `json:"db_reference"` // "max" (default) or "absolute"
}

// defaultPhysFeatures matches prepare_refine_phys_dataset / train_refine_phys:
// [d40, d100, d200, peak, high_ratio, decay_tail]
var defaultPhysFeatures = []string{
	"decay40", "decay100", "decay200", "peak", "high_ratio", "decay_tail",
}

func featureValue(f *decay.WindowFeatures, name string) (float32, bool) {
	if f == nil {
		return 0, false
	}
	switch strings.ToLower(strings.ReplaceAll(name, "-", "_")) {
	case "decay40", "d40":
		return float32(f.Decay40), true
	case "decay100", "d100":
		return float32(f.Decay100), true
	case "decay200", "d200", "decay":
		return float32(f.Decay200), true
	case "peak":
		return float32(f.Peak), true
	case "highratio", "high_ratio":
		return float32(f.HighRatio), true
	case "decay_tail", "decaytail", "tail":
		return float32(f.DecayTail), true
	case "lowrms", "low_rms":
		return float32(f.LowRms), true
	case "midrms", "mid_rms":
		return float32(f.MidRms), true
	case "highrms", "high_rms":
		return float32(f.HighRms), true
	case "lowtomid", "low_to_mid":
		if f.MidRms > 0.00001 {
			return float32(f.LowRms / f.MidRms), true
		}
		return 1, true
	}
	return 0, false
}

// resolvePhysSpec builds physSpec from either nested "phys" or flat export fields
// phys_mean / phys_std / phys_dim (export_to_onnx_n.py format).
func resolvePhysSpec(mi metaItem, physDimFromONNX int) *physSpec {
	if mi.Phys != nil && len(mi.Phys.Features) > 0 {
		ps := *mi.Phys
		if len(ps.Mean) == 0 && len(mi.PhysMean) > 0 {
			ps.Mean = mi.PhysMean
		}
		if len(ps.Std) == 0 && len(mi.PhysStd) > 0 {
			ps.Std = mi.PhysStd
		}
		return &ps
	}
	dim := mi.PhysDim
	if dim <= 0 {
		dim = physDimFromONNX
	}
	if dim <= 0 {
		dim = len(mi.PhysMean)
	}
	if dim <= 0 && len(mi.PhysStd) > 0 {
		dim = len(mi.PhysStd)
	}
	if dim <= 0 {
		return nil
	}
	// Prefer explicit features from nested phys; else training default order.
	feats := append([]string(nil), defaultPhysFeatures...)
	if mi.Phys != nil && len(mi.Phys.Features) > 0 {
		feats = append([]string(nil), mi.Phys.Features...)
	}
	if len(feats) > dim {
		feats = feats[:dim]
	}
	for len(feats) < dim {
		feats = append(feats, fmt.Sprintf("f%d", len(feats)))
	}
	mean := mi.PhysMean
	std := mi.PhysStd
	if mi.Phys != nil {
		if len(mean) == 0 {
			mean = mi.Phys.Mean
		}
		if len(std) == 0 {
			std = mi.Phys.Std
		}
	}
	// pad / trim to dim
	if len(mean) < dim {
		pad := make([]float32, dim)
		copy(pad, mean)
		mean = pad
	} else if len(mean) > dim {
		mean = mean[:dim]
	}
	if len(std) < dim {
		pad := make([]float32, dim)
		for i := range pad {
			pad[i] = 1
		}
		copy(pad, std)
		std = pad
	} else if len(std) > dim {
		std = std[:dim]
	}
	return &physSpec{Features: feats, Mean: mean, Std: std}
}

// loadedModel is one ONNX model plus everything needed to run it like Groovy runInference().
type loadedModel struct {
	spec  cnnSpec
	model *ortx.Model
	mean  float32
	std   float32
	names []string // canonical names by output index

	physName string // name of the extra (physics) input, "" if the model has none
	physDim  int
	phys     *physSpec

	statN       float64 // diagnostics: statistics of the normalised mel input
	statSum     float64
	statSq      float64
}

func loadCnn(modelsDir string, spec cnnSpec, meta map[string]json.RawMessage, ver *ClassVerifier) *loadedModel {
	candidates := []string{spec.key + ".onnx"}
	if spec.key == "cy_arbiter" {
		candidates = append(candidates, "cymbals_arbiter.onnx")
	}
	var path string
	for _, c := range candidates {
		p := filepath.Join(modelsDir, c)
		if _, err := os.Stat(p); err == nil {
			path = p
			break
		}
	}
	if path == "" {
		fmt.Printf("   [!] %s.onnx not found - the manager uses 0.0 probabilities for it (like the original)\n", spec.key)
		return nil
	}
	model, err := ortx.Load(path)
	if err != nil {
		fmt.Printf("   [!] cannot load %s: %v\n", spec.key, err)
		return nil
	}
	var mi metaItem
	if raw, ok := meta[spec.key]; ok {
		_ = json.Unmarshal(raw, &mi)
	}
	nOut := 0
	if n := len(model.OutDims); n > 0 && model.OutDims[n-1] > 0 {
		nOut = int(model.OutDims[n-1])
	}
	names, ok := ver.Register(spec, nOut, mi)
	if !ok {
		model.Close()
		return nil
	}
	lm := &loadedModel{spec: spec, model: model, mean: mi.Mean, std: mi.Std, names: names}
	if lm.std == 0 {
		lm.std = 1
	}
	fmt.Printf("   %s: mel input '%s' %v, mean=%.4f std=%.4f arch=%s\n",
		spec.key, model.InName, model.InDims, lm.mean, lm.std, mi.Arch)

	// ortx.Model: ExtraInputNames() + Run(..., extra map); no ExtraInfo/RunExtra.
	if extraNames := model.ExtraInputNames(); len(extraNames) > 0 {
		lm.physName = extraNames[0]
		lm.physDim = mi.PhysDim
		if lm.physDim <= 0 {
			lm.physDim = 6
		}
		if len(mi.PhysMean) > 0 && lm.physDim != len(mi.PhysMean) {
			lm.physDim = len(mi.PhysMean)
		}
		lm.phys = resolvePhysSpec(mi, lm.physDim)
		switch {
		case lm.phys == nil || len(lm.phys.Features) != lm.physDim:
			fmt.Printf("   [!] %s has physics input '%s' dim=%d, but models_meta has no usable phys_mean/phys_std/phys\n"+
				"       (expected flat phys_mean/phys_std/phys_dim or nested phys.features). Fed with ZEROS.\n",
				spec.key, lm.physName, lm.physDim)
			lm.phys = nil
		default:
			fmt.Printf("   %s: physics input '%s' dim=%d <- %v\n",
				spec.key, lm.physName, lm.physDim, lm.phys.Features)
		}
	} else if mi.Arch == "mel_phys" || len(mi.PhysMean) > 0 {
		fmt.Printf("   [!] %s meta arch=mel_phys (phys_dim=%d) but ONNX has no physics input — mel only\n",
			spec.key, mi.PhysDim)
	}
	return lm
}

// infer feeds one [nMels][frames] patch exactly like Groovy runInference: crop / zero-pad to
// the model input, (v-mean)/(std+1e-6), softmax of the logits, canonical class order.
func (lm *loadedModel) infer(patch [][]float32, feat *decay.WindowFeatures) (map[string]float32, error) {
	dims := lm.model.InDims
	expRows, expCols := len(patch), len(patch[0])
	is3D := false
	if len(dims) == 3 {
		is3D = true
		if dims[1] > 0 {
			expRows = int(dims[1])
		}
		if dims[2] > 0 {
			expCols = int(dims[2])
		}
	} else if len(dims) == 4 {
		if dims[2] > 0 {
			expRows = int(dims[2])
		}
		if dims[3] > 0 {
			expCols = int(dims[3])
		}
	}
	flat := make([]float32, expRows*expCols)
	for r := 0; r < expRows; r++ {
		for c := 0; c < expCols; c++ {
			var v float32
			if r < len(patch) && c < len(patch[r]) {
				v = patch[r][c]
			}
			nv := (v - lm.mean) / (lm.std + 1e-6)
			flat[r*expCols+c] = nv
			lm.statN++
			lm.statSum += float64(nv)
			lm.statSq += float64(nv) * float64(nv)
		}
	}
	shape := []int64{1, 1, int64(expRows), int64(expCols)}
	if is3D {
		shape = []int64{1, int64(expRows), int64(expCols)}
	}
	var out []float32
	var err error
	if lm.physName != "" && lm.phys != nil && lm.physDim > 0 {
		vec := make([]float32, lm.physDim)
		for i, name := range lm.phys.Features {
			if i >= lm.physDim {
				break
			}
			v, _ := featureValue(feat, name)
			if i < len(lm.phys.Mean) && i < len(lm.phys.Std) {
				std := lm.phys.Std[i]
				if std == 0 {
					std = 1
				}
				v = (v - lm.phys.Mean[i]) / (std + 1e-6)
			}
			vec[i] = v
		}
		extra := map[string][]float32{lm.physName: vec}
		out, _, err = lm.model.Run(shape, flat, extra)
	} else {
		out, _, err = lm.model.Run(shape, flat)
	}
	if err != nil {
		return nil, err
	}
	if len(out) < lm.spec.classes {
		return nil, fmt.Errorf("%s returned %d values, expected %d classes", lm.spec.key, len(out), lm.spec.classes)
	}
	logits := out
	if len(out) > lm.spec.classes {
		logits = out[:lm.spec.classes]
	}
	mx := logits[0]
	for _, v := range logits {
		if v > mx {
			mx = v
		}
	}
	probs := make([]float64, len(logits))
	sum := 0.0
	for i, v := range logits {
		probs[i] = math.Exp(float64(v - mx))
		sum += probs[i]
	}
	byName := map[string]float32{}
	for i := range probs {
		if i < len(lm.names) {
			byName[lm.names[i]] = float32(probs[i] / sum)
		}
	}
	return byName, nil
}

// RunCnn runs hh_best / cy_best / tt_best / cy_arbiter on mel patches cut from the drum stem
// (mono, 44.1 kHz). Hats and cymbals use a 90-frame patch (full filterbank), toms a 60-frame
// patch (toms filterbank); models cut/pad it to their own input size like the original.
func RunCnn(modelsDir string, mono []float32, sampleRate int, events []CnnEvent) ([]CnnProbs, error) {
	results := make([]CnnProbs, len(events))
	if len(events) == 0 {
		return results, nil
	}
	if sampleRate != melSR {
		mono = dsp.Resample(mono, sampleRate, melSR)
	}
	meta := map[string]json.RawMessage{}
	if data, err := os.ReadFile(filepath.Join(modelsDir, "models_meta.json")); err == nil {
		if err := json.Unmarshal(data, &meta); err != nil {
			fmt.Printf("   [!] cannot parse models_meta.json: %v\n", err)
		}
	}
	var fe frontendMeta
	if raw, ok := meta["frontend"]; ok {
		_ = json.Unmarshal(raw, &fe)
	}
	relToMax := strings.ToLower(fe.DbReference) != "absolute"
	ref := "dB relative to the patch maximum (values in [-80, 0], like librosa ref=max)"
	if !relToMax {
		ref = "absolute dB clamped to max-80 (original Groovy extractor)"
	}
	fmt.Printf("   mel front end: n_fft=%d hop=%d n_mels=%d fmin=%.0f fmax=%.0f (toms %.0f), %s\n",
		melNFft, melHop, melNMels, melFMin, melFMaxCy, melFMaxTt, ref)

	ver := &ClassVerifier{}
	hh := loadCnn(modelsDir, specHH, meta, ver)
	cy := loadCnn(modelsDir, specCY, meta, ver)
	tt := loadCnn(modelsDir, specTT, meta, ver)
	arb := loadCnn(modelsDir, specARB, meta, ver)
	ver.Report()
	for _, lm := range []*loadedModel{hh, cy, tt, arb} {
		if lm != nil {
			defer lm.model.Close()
		}
	}

	ex := newMelExtractor(relToMax)
	counts := map[string]int{}
	fail := map[string]bool{}
	run := func(lm *loadedModel, patch [][]float32, feat *decay.WindowFeatures) map[string]float32 {
		if lm == nil || fail[lm.spec.key] {
			return nil
		}
		p, err := lm.infer(patch, feat)
		if err != nil {
			fmt.Printf("   [!] %s inference failed: %v (disabled)\n", lm.spec.key, err)
			fail[lm.spec.key] = true
			return nil
		}
		counts[lm.spec.key]++
		return p
	}

	for i, e := range events {
		switch e.Kind {
		case "hh", "cy":
			patch := ex.patch(mono, e.T, false, 90)
			if e.Kind == "hh" {
				results[i].Spec = run(hh, patch, e.Feat)
			} else {
				results[i].Spec = run(cy, patch, e.Feat)
			}
			results[i].Arb = run(arb, patch, e.Feat)
		case "tt":
			results[i].Spec = run(tt, ex.patch(mono, e.T, true, 60), e.Feat)
		}
	}
	// self-check: with the right preprocessing the normalised input is ~N(0,1)
	for _, lm := range []*loadedModel{hh, cy, tt, arb} {
		if lm == nil || lm.statN == 0 {
			continue
		}
		mean := lm.statSum / lm.statN
		std := math.Sqrt(math.Max(0, lm.statSq/lm.statN-mean*mean))
		flag := "ok"
		if math.Abs(mean) > 1.0 || std < 0.5 || std > 2.0 {
			flag = "SUSPICIOUS - preprocessing probably differs from training (try frontend.db_reference)"
		}
		fmt.Printf("   [CHECK] %s normalised mel input: mean=%.2f std=%.2f (expect ~0 / ~1) -> %s\n", lm.spec.key, mean, std, flag)
	}
	fmt.Printf("   CNN runs: hh_best=%d cy_best=%d tt_best=%d cy_arbiter=%d (of %d events)\n",
		counts["hh_best"], counts["cy_best"], counts["tt_best"], counts["cy_arbiter"], len(events))
	return results, nil
}
