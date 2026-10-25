package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drumai/pata2fla/internal/adtof"
	"github.com/drumai/pata2fla/internal/appdir"
	"github.com/drumai/pata2fla/internal/audio"
	"github.com/drumai/pata2fla/internal/chartgen"
	"github.com/drumai/pata2fla/internal/decay"
	"github.com/drumai/pata2fla/internal/dsp"
	"github.com/drumai/pata2fla/internal/midi"
	"github.com/drumai/pata2fla/internal/nms"
	"github.com/drumai/pata2fla/internal/ortx"
	"github.com/drumai/pata2fla/internal/refine"
	"github.com/drumai/pata2fla/internal/sections"
	"github.com/drumai/pata2fla/internal/stemsplit"
	"github.com/drumai/pata2fla/internal/tempo"
)

type Options struct {
	Input         string
	Out           string
	Models        string
	BPM           float64
	ChartQuant    int
	NoChart       bool
	DrumsOnly     bool
	NoCnn         bool
	AllowFallback bool
	Threshold     float64
	AdtofMode     string
}

type hit struct {
	raw       adtof.RawOnset
	kind      string // kick, snare, hihat, tom, cymbal
	pitchHint int    // 0 when ADTOF gave only a 5-class label (no real pitch information)
	cnnIdx    int    // index into the CNN event list, -1 when no CNN is used for this hit
}

func classify(raw adtof.RawOnset) (string, int) {
	switch raw.Class {
	case "kick", "snare", "hihat", "tom", "cymbal":
		return raw.Class, 0
	}
	p := raw.Pitch
	switch {
	case p == 35 || p == 36:
		return "kick", p
	case p == 38 || p == 40:
		return "snare", p
	case p == 42 || p == 44 || p == 46:
		return "hihat", p
	case p == 41 || p == 43 || p == 45 || p == 47 || p == 48 || p == 50:
		return "tom", p
	}
	return "cymbal", p
}

// Process runs the full ONNX pipeline:
// HT-Demucs stemsplit (unless --drums-only) → ADTOF → decay+CNN refine → NMS → MIDI → Clone Hero chart.
// logFn receives progress lines for CLI or GUI console.
func Process(opt Options, logFn func(string)) error {
	if logFn == nil {
		logFn = func(s string) { fmt.Println(s) }
	}
	if opt.Input == "" {
		return fmt.Errorf("no input audio file specified")
	}
	inputFile, statErr := os.Stat(opt.Input)
	if statErr != nil || inputFile.IsDir() {
		return fmt.Errorf("input file not found: %s", opt.Input)
	}
	if inputFile.Size() == 0 {
		return fmt.Errorf("[ERROR] Input file is empty (0 bytes): %s", opt.Input)
	}
	if opt.Out == "" {
		opt.Out = filepath.Join(appdir.Dir(), "output")
	}
	if opt.Models == "" {
		opt.Models = "models"
	}
	// BPM: zero/negative → default 120; explicit out-of-range → hard error.
	if opt.BPM <= 0 {
		opt.BPM = 120
	} else if opt.BPM < 20 || opt.BPM > 350 {
		return fmt.Errorf("[ERROR] BPM %.3f is outside the allowed range 20–350", opt.BPM)
	}
	if opt.ChartQuant < 0 {
		opt.ChartQuant = 24
	}
	modelsDir := appdir.ResolveDir(opt.Models)

	trackName := strings.TrimSuffix(inputFile.Name(), filepath.Ext(inputFile.Name()))
	trackOutDir := filepath.Join(opt.Out, trackName)
	if err := os.MkdirAll(trackOutDir, 0755); err != nil {
		return fmt.Errorf("failed to create folder %s: %w", trackOutDir, err)
	}

	stemTmp := filepath.Join(trackOutDir, "01_isolated_drums.wav")
	targetDrumsWav := filepath.Join(trackOutDir, "drums.wav")
	midiDir := filepath.Join(trackOutDir, "midi")
	if err := os.MkdirAll(midiDir, 0755); err != nil {
		return fmt.Errorf("failed to create midi folder %s: %w", midiDir, err)
	}
	targetMidi := filepath.Join(midiDir, trackName+".mid")

	logFn("   PATA2FLA")
	logFn("   High-Precision Drum Transcription Pipeline")
	logFn("===================================================================")
	logFn(fmt.Sprintf(">> Input track:      %s\n", opt.Input))
	logFn(fmt.Sprintf(">> Output folder:    %s\n", trackOutDir))
	logFn(fmt.Sprintf(">> Drum stem:        %s\n", filepath.Base(targetDrumsWav)))
	logFn(fmt.Sprintf(">> MIDI:             midi/%s\n", filepath.Base(targetMidi)))
	logFn(fmt.Sprintf(">> Models directory: %s\n", modelsDir))
	if opt.DrumsOnly {
		logFn(">> Mode:             --drums-only (input is already an isolated drum track)")
	} else {
		logFn(">> Mode:             full mix -> HT-Demucs FT drums isolation -> verification")
	}
	logFn("-------------------------------------------------------------------")

	startTime := time.Now()

	// ---------------------------------------------------------------- Step 1
	logFn(">> [Step 1] Decoding input and isolating the drum stem...")
	src, err := audio.Load(opt.Input)
	if err != nil {
		return fmt.Errorf("[ERROR] %v", err)
	}
	src = src.Stereo()
	if src.SR != stemsplit.SampleRate {
		logFn(fmt.Sprintf("   resampling %d Hz -> %d Hz\n", src.SR, stemsplit.SampleRate))
		src = &audio.Audio{SR: stemsplit.SampleRate, Ch: [][]float32{
			dsp.Resample(src.Ch[0], src.SR, stemsplit.SampleRate),
			dsp.Resample(src.Ch[1], src.SR, stemsplit.SampleRate)}}
	}
	durSec := src.Duration()
	const (
		minAudioSec = 0.5
		maxAudioSec = 20 * 60 // 20 minutes
	)
	if durSec < minAudioSec {
		return fmt.Errorf("[ERROR] Audio is too short (%.2f sec). Minimum length is %.1f sec", durSec, minAudioSec)
	}
	if durSec > maxAudioSec {
		return fmt.Errorf("[ERROR] Audio is too long (%.1f min). Maximum length is 20 minutes", durSec/60.0)
	}
	logFn(fmt.Sprintf("   [OK] Input decoded: %.1f sec, %d Hz\n", durSec, src.SR))

	if err := ortx.Init(appdir.Dir()); err != nil {
		return fmt.Errorf("[ERROR] %v", err)
	}
	defer ortx.Shutdown()

	var drums *audio.Audio
	var mixRef *audio.Audio
	if opt.DrumsOnly {
		logFn("   [i] --drums-only: separation skipped, the input is used as the drum stem")
		drums = src
	} else {
		sep := stemsplit.NewSeparator(modelsDir)
		drums, err = sep.Separate(src, logFn)
		if err != nil {
			return fmt.Errorf("[CRITICAL STEP 1 ERROR] Drum stem isolation failed!\nReason: %v\n"+
				"PIPELINE STOPPED: working on the raw mix (vocals, guitars...) is not allowed.\n"+
				"If the input is ALREADY an isolated drum track, run with --drums-only", err)
		}
		mixRef = src
	}

	logFn("   Verifying that the stem is really an isolated drum track...")
	rep := stemsplit.VerifyDrumStem(drums, mixRef)
	for _, m := range rep.Metrics {
		logFn(fmt.Sprintf("   [VERIFY] %s\n", m))
	}
	for _, w := range rep.Warnings {
		logFn(fmt.Sprintf("   [VERIFY WARN] %s\n", w))
	}
	if len(rep.Errors) > 0 {
		for _, e := range rep.Errors {
			logFn(fmt.Sprintf("   [VERIFY ERROR] %s\n", e))
		}
		return fmt.Errorf("[CRITICAL STEP 1 ERROR] The extracted stem is not a valid drum stem. Pipeline stopped.")
	}
	logFn("   [VERIFY OK] drum stem passed the checks")

	if err := audio.WriteWav(stemTmp, drums, 24); err != nil {
		return fmt.Errorf("[ERROR] Cannot write %s: %v", stemTmp, err)
	}
	logFn(fmt.Sprintf("   [OK] Drum stem ready: %s\n", filepath.Base(stemTmp)))

	drumAudio := drums.Mono()
	sampleRate := drums.SR
	durationSec := drums.Duration()
	if durationSec < 0.5 {
		return fmt.Errorf("[ERROR] Isolated drum stem is too short (%.2f sec)", durationSec)
	}
	logFn(fmt.Sprintf("   [OK] Audio loaded: %.2f sec, sample rate: %d Hz\n", durationSec, sampleRate))

	// ---------------------------------------------------------------- Step 2
	logFn(">> [Step 2] ADTOF: 5-class onset detection on the isolated drum stem...")
	detector := adtof.NewDetector(modelsDir)
	detector.AllowFallback = opt.AllowFallback
	detector.Threshold = opt.Threshold
	detector.Mode = opt.AdtofMode
	detector.StemPath = stemTmp
	adtofEvents, backend, err := detector.DetectOnsets(drumAudio, sampleRate)
	if err != nil {
		return fmt.Errorf("[ERROR] ADTOF onset detection failed: %v\n(the drum stem was kept: %s)", err, stemTmp)
	}
	if len(adtofEvents) == 0 {
		return fmt.Errorf("[ERROR] ADTOF found no drum hits. Wrong stem, silent track or bad ADTOF front end?\n"+
			"(the drum stem was kept: %s)", stemTmp)
	}
	logFn(fmt.Sprintf("   [OK] %d raw onset events (backend: %s)\n", len(adtofEvents), backend))

	hits := make([]hit, len(adtofEvents))
	var cnnEvents []refine.CnnEvent
	for i, raw := range adtofEvents {
		kind, ph := classify(raw)
		h := hit{raw: raw, kind: kind, pitchHint: ph, cnnIdx: -1}
		cnnKind := ""
		switch kind {
		case "hihat":
			cnnKind = "hh"
		case "tom":
			cnnKind = "tt"
		case "cymbal":
			cnnKind = "cy"
		}
		if cnnKind != "" {
			h.cnnIdx = len(cnnEvents)
			cnnEvents = append(cnnEvents, refine.CnnEvent{T: raw.TimeSec, Kind: cnnKind})
		}
		hits[i] = h
	}

	// ---------------------------------------------------------------- Step 3
	logFn(">> [Step 3] Physical decay prior + CNN specialists / arbiter...")
	decayAnalyzer := decay.NewAnalyzer()
	// attach physics features to each CNN event (needed for mel_phys input)
	workSR := sampleRate
	workAudio := drumAudio
	for i := range cnnEvents {
		cnnEvents[i].Feat = decayAnalyzer.ExtractFeatures(workAudio, cnnEvents[i].T, workSR)
	}
	var cnnProbs []refine.CnnProbs
	if opt.NoCnn {
		logFn("   [!] --no-cnn: CNN models are skipped, heuristic estimates are used")
	} else if len(cnnEvents) > 0 {
		probs, cerr := refine.RunCnn(modelsDir, drumAudio, sampleRate, cnnEvents)
		if cerr != nil {
			logFn(fmt.Sprintf("   [!] CNN stage failed (%v)\n", cerr))
			logFn("   [!] Continuing with heuristic estimates instead of the real CNN outputs")
		} else {
			cnnProbs = probs
		}
	}
	withSpec, withArb := 0, 0
	for _, p := range cnnProbs {
		if p.Spec != nil {
			withSpec++
		}
		if p.Arb != nil {
			withArb++
		}
	}
	logFn(fmt.Sprintf("   [i] Real CNN output: specialists %d/%d events, arbiter %d events\n",
		withSpec, len(cnnEvents), withArb))

	refineMgr := refine.NewManager(modelsDir)

	var candidateEvents []nms.Event
	cKick, cSnare := 0, 0
	cClosedHat, cOpenHat, cPedalHat := 0, 0, 0
	cHighTom, cMidTom, cFloorTom := 0, 0, 0
	cCrash, cRide := 0, 0
	adtofHh, adtofTom, adtofCy := 0, 0, 0

	for _, h := range hits {
		t := h.raw.TimeSec
		vel := h.raw.Velocity

		var probs *refine.CnnProbs
		if h.cnnIdx >= 0 && h.cnnIdx < len(cnnProbs) {
			probs = &cnnProbs[h.cnnIdx]
		}

		piece := ""
		switch h.kind {
		case "kick":
			refineMgr.Stats.AdtofKick++
			piece = "kick"
			cKick++

		case "snare":
			refineMgr.Stats.AdtofSnare++
			piece = "snare"
			cSnare++

		case "hihat":
			adtofHh++
			switch h.pitchHint {
			case 46:
				refineMgr.Stats.AdtofHhOpen++
			case 44:
				refineMgr.Stats.AdtofHhPedal++
			case 42:
				refineMgr.Stats.AdtofHhClosed++
			}
			feat := decayAnalyzer.ExtractFeatures(drumAudio, t, sampleRate)
			piece = refineMgr.RefineHiHat(probs, feat, h.pitchHint)
			switch piece {
			case "open_hat":
				cOpenHat++
			case "pedal_hat":
				cPedalHat++
			default:
				cClosedHat++
			}

		case "tom":
			adtofTom++
			switch h.pitchHint {
			case 48, 50:
				refineMgr.Stats.AdtofTtHigh++
			case 41, 43:
				refineMgr.Stats.AdtofTtFloor++
			case 45, 47:
				refineMgr.Stats.AdtofTtMid++
			}
			feat := decayAnalyzer.ExtractFeatures(drumAudio, t, sampleRate)
			piece = refineMgr.RefineToms(probs, feat, h.pitchHint)
			switch piece {
			case "high_tom":
				cHighTom++
			case "low_tom", "floor_tom":
				cFloorTom++
			default:
				cMidTom++
			}

		default: // cymbal
			adtofCy++
			switch h.pitchHint {
			case 49, 52, 55, 57:
				refineMgr.Stats.AdtofCyCrash++
			case 51, 53, 59:
				refineMgr.Stats.AdtofCyRide++
			}
			feat := decayAnalyzer.ExtractFeatures(drumAudio, t, sampleRate)
			piece = refineMgr.RefineCymbals(probs, feat, h.pitchHint, t)
			switch piece {
			case "crash":
				cCrash++
			case "ride":
				cRide++
			case "open_hat":
				cOpenHat++
			case "closed_hat":
				cClosedHat++
			}
		}

		candidateEvents = append(candidateEvents, nms.Event{
			TimeSec:    t,
			Piece:      piece,
			Velocity:   vel,
			Confidence: 1.0,
		})
	}

	// Cymbals ostinato post-pass: dense crash series (< 280 ms) becomes ride
	postPassCrashesToRide := 0
	nms.SortEventsByTime(candidateEvents)
	nEvents := len(candidateEvents)

	for k := 0; k < nEvents; k++ {
		cur := &candidateEvents[k]
		if cur.Piece == "crash" {
			var prev *nms.Event
			var next *nms.Event
			if k > 0 {
				prev = &candidateEvents[k-1]
			}
			if k+1 < nEvents {
				next = &candidateEvents[k+1]
			}

			hasPrevCymbal := prev != nil && (cur.TimeSec-prev.TimeSec) < 0.280 && isCymbal(prev.Piece)
			hasNextCymbal := next != nil && (next.TimeSec-cur.TimeSec) < 0.280 && isCymbal(next.Piece)

			if hasPrevCymbal || (hasNextCymbal && cur.Velocity < 95) {
				cur.Piece = "ride"
				postPassCrashesToRide++
				if cCrash > 0 {
					cCrash--
				}
				cRide++
			}
		}
	}

	// ---------------------------------------------------------------- Step 4
	logFn(">> [Step 4] Physical drummer NMS (kick+tom exclusion, hands <= 2)...")
	drummerNms := nms.NewPhysicalDrummerNms()
	keptEvents := drummerNms.SuppressEvents(candidateEvents)
	logFn(fmt.Sprintf("   [OK] Clean hits kept: %d\n", len(keptEvents)))
	if len(keptEvents) == 0 {
		return fmt.Errorf("[ERROR] No hits left after NMS - nothing to write.\n(the drum stem was kept: %s)", stemTmp)
	}

	// Form sections for Clone Hero [Events] (Part_A / Intro / Break …).
	// Constant tempo map only — no lead-in; beat.go Track is available but not used by default.
	tm := tempo.Constant(opt.BPM)
	secList, secInfo := sections.Detect(keptEvents, tm, 8)
	if len(secList) > 0 {
		parts := make([]string, 0, len(secList))
		for _, s := range secList {
			parts = append(parts, s.Label)
		}
		logFn(fmt.Sprintf("   [i] Form sections: %d markers (bars≈%d, downbeat_off=%d) → %v\n",
			len(secList), secInfo.Bars, secInfo.DownbeatOffset, parts))
	} else {
		logFn("   [i] Form sections: none detected (too few hits or flat density)\n")
	}

	// ---------------------------------------------------------------- Step 5
	logFn(">> [Step 5] Writing MIDI...")
	if err := midi.WriteMidi(targetMidi, keptEvents, opt.BPM); err != nil {
		return fmt.Errorf("[ERROR] Failed to write MIDI %s: %v\n(the drum stem was kept: %s)", targetMidi, err, stemTmp)
	}
	logFn(fmt.Sprintf("   [OK] MIDI written: midi/%s\n", filepath.Base(targetMidi)))

	// ---------------------------------------------------------------- Step 6
	// Only after the MIDI exists: the intermediate stem becomes drums.wav
	logFn(">> [Step 6] Finalizing output folder...")
	_ = os.Remove(targetDrumsWav)
	if err := os.Rename(stemTmp, targetDrumsWav); err != nil {
		if cerr := audio.CopyFile(stemTmp, targetDrumsWav); cerr != nil {
			return fmt.Errorf("[ERROR] Could not create %s: %v", targetDrumsWav, cerr)
		}
		_ = os.Remove(stemTmp)
	}
	if _, err := os.Stat(targetMidi); err != nil {
		return fmt.Errorf("[ERROR] MIDI file is missing after processing: %s", targetMidi)
	}

	// ---------------------------------------------------------------- Step 7: Clone Hero pack (notes.chart + song.ini + padded drums + song.wav)
	if !opt.NoChart {
		logFn(">> [Step 7] Clone Hero export (notes.chart, song.ini, song.wav)...")
		bpm := opt.BPM
		if bpm <= 0 {
			bpm = 120
		}
		q := opt.ChartQuant // 16 / 24 / 32 / 0=off
		meta := chartgen.ReadTags(opt.Input)
		if meta.Name == "" {
			meta.Name = trackName
		}

		// Notes aligned with audio: Offset=0, no time shift. MIDI is under midi/.
		songWav := filepath.Join(trackOutDir, "song.wav")
		if err := writeSongWav(opt.Input, songWav, 0); err != nil {
			logFn(fmt.Sprintf("   [!] song.wav: %v\n", err))
		} else {
			logFn(fmt.Sprintf("   [OK] song.wav written\n"))
		}

		cfg := chartgen.DefaultConfig()
		cfg.BPM = bpm
		cfg.QuantizeDiv = q
		cfg.StartOffsetSec = 0
		cfg.SongOffsetSec = 0
		cfg.Meta = meta
		cfg.Channel = 10
		// Section markers: tick = beat * Resolution (absolute grid, Offset=0).
		if len(secList) > 0 {
			res := cfg.TargetResolution
			if res <= 0 {
				res = 192
			}
			markers := make([]chartgen.SectionMarker, 0, len(secList))
			for _, s := range secList {
				markers = append(markers, chartgen.SectionMarker{
					Tick:  int64(s.Beat) * int64(res),
					Label: s.Label,
				})
			}
			cfg.Sections = markers
		}

		mf, err := os.Open(targetMidi)
		if err != nil {
			logFn(fmt.Sprintf("   [!] chart: cannot open midi: %v\n", err))
		} else {
			chartStr, cerr := chartgen.NewConverter(cfg).Convert(mf)
			mf.Close()
			if cerr != nil {
				logFn(fmt.Sprintf("   [!] chart convert: %v\n", cerr))
			} else {
				chartPath := filepath.Join(trackOutDir, "notes.chart")
				if err := os.WriteFile(chartPath, []byte(chartStr), 0644); err != nil {
					logFn(fmt.Sprintf("   [!] write notes.chart: %v\n", err))
				} else {
					logFn(fmt.Sprintf("   [OK] notes.chart (pro-drums 4-lane, quantize 1/%d, bpm=%.1f, Offset=0)\n", q, bpm))
				}
			}
		}

		durMs := int(src.Duration() * 1000.0) // no leading pad
		iniPath := filepath.Join(trackOutDir, "song.ini")
		if err := chartgen.WriteSongIni(iniPath, meta, durMs, 0); err != nil {
			logFn(fmt.Sprintf("   [!] song.ini: %v\n", err))
		} else {
			logFn(fmt.Sprintf("   [OK] song.ini  artist=%q title=%q\n", meta.Artist, meta.Name))
		}

		// Clone Hero icon: assets/IconPata2Fla.png → song folder (song.ini: icon = IconPata2Fla)
		if err := copySongIcon(trackOutDir); err != nil {
			logFn(fmt.Sprintf("   [!] icon: %v\n", err))
		} else {
			logFn(fmt.Sprintf("   [OK] IconPata2Fla.png\n"))
		}
	}

	printReport(logFn, refineMgr.Stats, cKick, cSnare, cClosedHat, cOpenHat, cPedalHat,
		cHighTom, cMidTom, cFloorTom, cCrash, cRide, postPassCrashesToRide,
		adtofHh, adtofTom, adtofCy)

	elapsed := time.Since(startTime)
	logFn("===================================================================")
	logFn(fmt.Sprintf("[SUCCESS] Processing finished in %.2f sec\n", elapsed.Seconds()))
	logFn(fmt.Sprintf("Output folder: %s\n", trackOutDir))
	logFn(fmt.Sprintf("   - midi/%s\n", filepath.Base(targetMidi)))
	logFn(fmt.Sprintf("   - %s (isolated drum stem)\n", filepath.Base(targetDrumsWav)))
	if !opt.NoChart {
		logFn(fmt.Sprintf("   - notes.chart / song.ini / song.wav / IconPata2Fla.png (Clone Hero)\n"))
	}
	logFn("===================================================================")

	return nil
}

func isCymbal(piece string) bool {
	return piece == "crash" || piece == "ride" || piece == "open_hat" || piece == "closed_hat"
}

func printReport(logFn func(string), s *refine.DecisionStats, kick, snare, clHat, opHat, pdHat, hTom, mTom, fTom, crash, ride, postCrashes, adtofHh, adtofTom, adtofCy int) {
	logFn("")
	logFn("===================================================================")
	logFn(">> [DECISION TRACE] ADTOF -> DECAY -> SPECIALISTS -> ARBITER -> CONSENSUS:")
	logFn("===================================================================")
	logFn("0. RAW ADTOF INPUT (5 classes):")
	logFn(fmt.Sprintf("   - Kick / Snare:             Kick=%d, Snare=%d\n", s.AdtofKick, s.AdtofSnare))
	logFn(fmt.Sprintf("   - Hi-Hat class:             %d hits (open/closed/pedal decided below)\n", adtofHh))
	logFn(fmt.Sprintf("   - Tom class:                %d hits (floor/mid/high decided below)\n", adtofTom))
	logFn(fmt.Sprintf("   - Cymbal+Ride class:        %d hits (ride/crash decided below)\n", adtofCy))
	if s.AdtofHhClosed+s.AdtofHhOpen+s.AdtofHhPedal+s.AdtofTtHigh+s.AdtofTtMid+s.AdtofTtFloor+s.AdtofCyCrash+s.AdtofCyRide > 0 {
		logFn(fmt.Sprintf("   - Hi-Hats (by MIDI pitch):  Closed(42)=%d, Open(46)=%d, Pedal(44)=%d\n", s.AdtofHhClosed, s.AdtofHhOpen, s.AdtofHhPedal))
		logFn(fmt.Sprintf("   - Toms (by MIDI pitch):     High(48,50)=%d, Mid(45,47)=%d, Floor(41,43)=%d\n", s.AdtofTtHigh, s.AdtofTtMid, s.AdtofTtFloor))
		logFn(fmt.Sprintf("   - Cymbals (by MIDI pitch):  Crash(49,52,55,57)=%d, Ride(51,53,59)=%d\n", s.AdtofCyCrash, s.AdtofCyRide))
	}

	logFn("1. PHYSICAL DECAY 40/100/200ms AND RMS BANDS:")
	logFn(fmt.Sprintf("   - Cymbals (Decay 200ms):    Crash (long tail)=%d, Ride (dry)=%d\n", s.DecayCyCrash, s.DecayCyRide))
	logFn(fmt.Sprintf("   - Hi-Hats (Decay 40/100ms): Closed (quick fall)=%d, Open (sustain)=%d\n", s.DecayHhClosed, s.DecayHhOpen))
	logFn(fmt.Sprintf("   - Toms (Bass RMS 40-300Hz): Floor (bass 40-300Hz)=%d, Mid (body)=%d, High (attack)=%d\n", s.DecayTtFloor, s.DecayTtMid, s.DecayTtHigh))

	logFn("2. 3x SPECIALIST CNNs (64x60):")
	logFn(fmt.Sprintf("   - cy_best CNN (Cymbals):    Ride=%d, Crash=%d\n", s.SpecCyRide, s.SpecCyCrash))
	logFn(fmt.Sprintf("   - hh_best CNN (Hi-Hats):    Closed=%d, Open=%d, Pedal=%d\n", s.SpecHhClosed, s.SpecHhOpen, s.SpecHhPedal))
	logFn(fmt.Sprintf("   - tt_best CNN (Toms):       Floor=%d, Mid=%d, High=%d\n", s.SpecTtFloor, s.SpecTtMid, s.SpecTtHigh))

	logFn("3. ARBITER CNN (64x90):")
	logFn(fmt.Sprintf("   - cy_arbiter (Hardware):    Ride=%d, Crash=%d, Closed=%d, Open=%d, Pedal=%d\n", s.ArbCyRide, s.ArbCyCrash, s.ArbCyClosed, s.ArbCyOpen, s.ArbCyPedal))

	logFn("4. SPECIALISTS FILTERING PASSES:")
	logFn(fmt.Sprintf("   - Pass 1 [High Confidence]:                  %d notes\n", s.SpecPass1HighConfidence))
	logFn(fmt.Sprintf("   - Pass 2 [Confirmed by Physics]:             %d notes\n", s.SpecPass2PhysicsConfirmed))
	logFn(fmt.Sprintf("   - Pass 3 [Conflict with Physics]:            %d notes\n", s.SpecPass3PhysicsConflict))

	logFn("5. ARBITER FILTERING PASSES:")
	logFn(fmt.Sprintf("   - Pass 1 [Arbiter High Confidence]:          %d notes\n", s.ArbPass1HighConfidence))
	logFn(fmt.Sprintf("   - Pass 2 [Arbiter Confirmed by Physics]:     %d notes\n", s.ArbPass2PhysicsConfirmed))
	logFn(fmt.Sprintf("   - Pass 3 [Arbiter Overrides Specialist]:     %d notes\n", s.ArbPass3Overrides))

	logFn("6. FINAL CONSENSUS VERDICT:")
	logFn(fmt.Sprintf("   - Kick (ADTOF):              %d\n", kick))
	logFn(fmt.Sprintf("   - Snare (ADTOF):             %d\n", snare))
	logFn(fmt.Sprintf("   - Hi-Hat:                    Closed=%d, Open=%d, Pedal=%d\n", clHat, opHat, pdHat))
	logFn(fmt.Sprintf("   - Toms:                      High=%d, Mid=%d, Floor=%d\n", hTom, mTom, fTom))
	logFn(fmt.Sprintf("   - Cymbals:                   Crash=%d, Ride=%d\n", crash, ride))
	logFn(fmt.Sprintf("   - Agreement:                 Unanimous=%d, Overridden=%d\n", s.UnanimousDecisions, s.ArbiterOverrides))

	logFn("7. CROSS-FAMILY ARBITRATION (5-CLASS ARBITER):")
	logFn(fmt.Sprintf("   - Open Hat vs Crash (resolved by arbiter):   %d notes\n", s.ArbResolvedOpenHatVsCrash))
	logFn(fmt.Sprintf("   - Closed Hat vs Ride (resolved by arbiter):  %d notes\n", s.ArbResolvedClosedHatVsRide))
	logFn(fmt.Sprintf("   - Pedal Hat protected by specialist:         %d notes\n", s.SpecPedalHatProtected))

	logFn("8. CYMBALS LOGIC (DECAY PRIOR + OSTINATO POST-PASS):")
	logFn(fmt.Sprintf("   - Sticky Ride (Decay Prior == Ride):         %d notes\n", s.DecayPriorStickyRide))
	logFn(fmt.Sprintf("   - Liberated Crash (Decay Prior == Crash):    %d notes\n", s.DecayPriorCrashLiberated))
	logFn(fmt.Sprintf("   - Dense Cymbals Ostinato forced to Ride:     %d notes\n", s.DenseOstinatoForcedRide+postCrashes))
	logFn("===================================================================")
}

// prependSilenceWav loads a wav, prepends sec of silence, writes back.
func prependSilenceWav(path string, sec float64) error {
	a, err := audio.Load(path)
	if err != nil {
		return err
	}
	n := int(sec * float64(a.SR))
	if n <= 0 {
		return nil
	}
	ch := make([][]float32, len(a.Ch))
	for c := range a.Ch {
		ch[c] = append(make([]float32, n), a.Ch[c]...)
	}
	return audio.WriteWav(path, &audio.Audio{Ch: ch, SR: a.SR}, 16)
}


// copySongIcon copies assets/IconPata2Fla.png into the track output folder.
// Looks under appdir (next to the binary) and, as a fallback, a local ./assets folder.
func copySongIcon(trackOutDir string) error {
	const name = "IconPata2Fla.png"
	candidates := []string{
		filepath.Join(appdir.Dir(), "assets", name),
		filepath.Join("assets", name),
	}
	var data []byte
	var err error
	var src string
	for _, c := range candidates {
		data, err = os.ReadFile(c)
		if err == nil && len(data) > 0 {
			src = c
			break
		}
	}
	if src == "" {
		return fmt.Errorf("%s not found (place it in assets/ next to the app)", name)
	}
	dst := filepath.Join(trackOutDir, name)
	return os.WriteFile(dst, data, 0644)
}

// writeSongWav writes the full mix as song.wav, optionally with leading silence.
func writeSongWav(inputPath, outPath string, padSec float64) error {
	a, err := audio.Load(inputPath)
	if err != nil {
		return err
	}
	a = a.Stereo()
	n := int(padSec * float64(a.SR))
	if n > 0 {
		ch := make([][]float32, len(a.Ch))
		for c := range a.Ch {
			ch[c] = append(make([]float32, n), a.Ch[c]...)
		}
		a = &audio.Audio{Ch: ch, SR: a.SR}
	}
	return audio.WriteWav(outPath, a, 16)
}
