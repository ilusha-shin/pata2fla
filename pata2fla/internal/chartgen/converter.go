package chartgen

import (
	"fmt"
	"io"
	"math"
	"sort"
)

const (
	LaneKick       = 0
	LaneRed        = 1
	LaneYellow     = 2
	LaneBlue       = 3
	LaneGreen      = 4
	LaneDoubleKick = 32
	CymbalModifier = 64
)

type Config struct {
	TargetResolution int
	// StartOffsetSec optional extra lead-in on the chart timeline (normally 0).
	StartOffsetSec float64
	// SongOffsetSec is [Song] Offset (normally 0 so notes match audio).
	SongOffsetSec   float64
	Channel         int // 10 = drums; -1 = all channels
	Auto2xKick      bool
	MaxKickInterval int64
	AutoToms        bool
	FullFile        bool
	BPM             float64
	// QuantizeDiv: 16 = 1/16, 24 = 1/24 (16th triplets / shuffle-friendly), 32 = 1/32; 0 = off
	QuantizeDiv int
	Meta        SongMeta
	// Sections optional form markers for [Events] (tick = beat * Resolution).
	Sections []SectionMarker
}

func DefaultConfig() Config {
	return Config{
		TargetResolution: 192,
		StartOffsetSec:   0,
		SongOffsetSec:    0,
		Channel:          10,
		Auto2xKick:       true,
		MaxKickInterval:  96,
		AutoToms:         true,
		FullFile:         true,
		BPM:              120,
		QuantizeDiv:      24,
	}
}

type DrumHit struct {
	Lane     int
	IsCymbal bool
}

type DrumConverter struct {
	cfg               Config
	kickNotes         map[uint8]bool
	redNotes          map[uint8]bool
	yellowTomNotes    map[uint8]bool
	blueTomNotes      map[uint8]bool
	greenTomNotes     map[uint8]bool
	yellowCymbalNotes map[uint8]bool
	blueCymbalNotes   map[uint8]bool
	greenCymbalNotes  map[uint8]bool
	ignoredNotes      map[uint8]bool
}

func NewConverter(cfg Config) *DrumConverter {
	return &DrumConverter{
		cfg:            cfg,
		kickNotes:      makeMap(35, 36),
		redNotes:       makeMap(38, 39, 40),
		yellowTomNotes: makeMap(48, 50),
		blueTomNotes:   makeMap(45, 47, 56),
		greenTomNotes:  makeMap(41, 43),
		// 4-lane pro-drums: hats yellow cym, ride blue cym, crash green cym
		yellowCymbalNotes: makeMap(42, 44, 46, 54, 55, 95),
		blueCymbalNotes:   makeMap(51, 52, 53, 59),
		greenCymbalNotes:  makeMap(49, 57),
		ignoredNotes:      makeMap(0, 37, 58),
	}
}

func makeMap(notes ...uint8) map[uint8]bool {
	m := make(map[uint8]bool, len(notes))
	for _, n := range notes {
		m[n] = true
	}
	return m
}

func (c *DrumConverter) Convert(r io.Reader) (string, error) {
	midiData, err := parseMIDI(r)
	if err != nil {
		return "", fmt.Errorf("midi parse error: %w", err)
	}

	var filtered []NoteEvent
	usedNotes := make(map[uint8]bool)
	for _, ev := range midiData.Events {
		if c.cfg.Channel == -1 || int(ev.Channel) == c.cfg.Channel {
			filtered = append(filtered, ev)
			usedNotes[ev.Note] = true
		}
	}
	if len(filtered) == 0 {
		// fallback: accept any channel (some writers use ch 0)
		for _, ev := range midiData.Events {
			filtered = append(filtered, ev)
			usedNotes[ev.Note] = true
		}
	}
	if len(filtered) == 0 {
		return "", fmt.Errorf("no drum notes found")
	}

	if c.cfg.AutoToms {
		c.optimizeToms(usedNotes)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].AbsoluteTick < filtered[j].AbsoluteTick
	})

	ppq := float64(midiData.TimeDivision)
	if ppq <= 0 {
		ppq = 480
	}
	res := float64(c.cfg.TargetResolution)
	ratio := ppq / res

	bpm := c.cfg.BPM
	if bpm <= 0 {
		bpm = 120
	}
	// offset ticks from seconds: sec * (bpm/60) * resolution
	startTick := int64(math.Round(c.cfg.StartOffsetSec * (bpm / 60.0) * res))

	// quantize grid in chart ticks (Resolution = quarter note)
	// 16 → quarter/4, 24 → quarter/6 (16th-triplet feel), 32 → quarter/8
	qStep := int64(0)
	switch {
	case c.cfg.QuantizeDiv >= 32:
		qStep = int64(res) / 8
	case c.cfg.QuantizeDiv == 24:
		qStep = int64(res) / 6
	case c.cfg.QuantizeDiv >= 16:
		qStep = int64(res) / 4
	}

	// Absolute timeline from song t=0 (same as song.wav / drums.wav).
	// Do NOT subtract the first-note tick — that pulled every chart to 0 while audio still had intro.
	// MIDI was written as tick = timeSec / secPerTick with the same BPM; chart tick = midiTick * (res/ppq).
	tickMap := make(map[int64][]uint8)
	_ = ratio // kept for clarity: ratio = ppq/res, chart = midi / ratio
	for _, ev := range filtered {
		ct := int64(math.Round(float64(ev.AbsoluteTick) * res / ppq))
		ct += startTick
		if ct < 0 {
			ct = 0
		}
		if qStep > 0 {
			ct = quantizeTick(ct, qStep)
		}
		tickMap[ct] = append(tickMap[ct], ev.Note)
	}

	uniqueTicks := make([]int64, 0, len(tickMap))
	for tick := range tickMap {
		uniqueTicks = append(uniqueTicks, tick)
	}
	sort.Slice(uniqueTicks, func(i, j int) bool { return uniqueTicks[i] < uniqueTicks[j] })

	var chartEvents []ChartNote
	var lastKickTick int64 = -1
	lastWasDouble := true

	for _, tick := range uniqueTicks {
		notes := tickMap[tick]
		var kickHit *DrumHit
		var handHits []DrumHit

		for _, note := range notes {
			if c.ignoredNotes[note] {
				continue
			}
			if c.kickNotes[note] {
				lane := LaneKick
				if c.cfg.Auto2xKick {
					if !lastWasDouble && lastKickTick >= 0 && (lastKickTick+c.cfg.MaxKickInterval >= tick) {
						lane = LaneDoubleKick
						lastWasDouble = true
					} else {
						lastWasDouble = false
					}
				}
				lastKickTick = tick
				kickHit = &DrumHit{Lane: lane, IsCymbal: false}
			} else if c.redNotes[note] {
				handHits = append(handHits, DrumHit{Lane: LaneRed, IsCymbal: false})
			} else if c.yellowCymbalNotes[note] {
				handHits = append(handHits, DrumHit{Lane: LaneYellow, IsCymbal: true})
			} else if c.blueCymbalNotes[note] {
				handHits = append(handHits, DrumHit{Lane: LaneBlue, IsCymbal: true})
			} else if c.greenCymbalNotes[note] {
				handHits = append(handHits, DrumHit{Lane: LaneGreen, IsCymbal: true})
			} else if c.yellowTomNotes[note] {
				handHits = append(handHits, DrumHit{Lane: LaneYellow, IsCymbal: false})
			} else if c.blueTomNotes[note] {
				handHits = append(handHits, DrumHit{Lane: LaneBlue, IsCymbal: false})
			} else if c.greenTomNotes[note] {
				handHits = append(handHits, DrumHit{Lane: LaneGreen, IsCymbal: false})
			}
		}

		if kickHit != nil {
			chartEvents = append(chartEvents, ChartNote{Tick: tick, Lane: kickHit.Lane})
		}
		for _, h := range c.processHandHits(handHits) {
			if h.IsCymbal {
				chartEvents = append(chartEvents, ChartNote{Tick: tick, Lane: h.Lane + CymbalModifier})
			}
			chartEvents = append(chartEvents, ChartNote{Tick: tick, Lane: h.Lane})
		}
	}

	return formatChart(chartEvents, c.cfg.TargetResolution, bpm, c.cfg.Meta, c.cfg.SongOffsetSec, c.cfg.FullFile, c.cfg.Sections), nil
}

func quantizeTick(t, step int64) int64 {
	if step <= 0 {
		return t
	}
	return ((t + step/2) / step) * step
}

func (c *DrumConverter) processHandHits(hits []DrumHit) []DrumHit {
	if len(hits) == 0 {
		return nil
	}
	if len(hits) == 1 {
		return hits
	}
	// keep up to 2 distinct lanes (pro-drums hands)
	out := []DrumHit{hits[0]}
	for _, h := range hits[1:] {
		dup := false
		for _, o := range out {
			if o.Lane == h.Lane {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, h)
		}
		if len(out) >= 2 {
			break
		}
	}
	return out
}

func (c *DrumConverter) optimizeToms(usedNotes map[uint8]bool) {
	var toms []uint8
	for i := uint8(35); i <= 81; i++ {
		if c.yellowTomNotes[i] || c.blueTomNotes[i] || c.greenTomNotes[i] {
			if usedNotes[i] {
				toms = append(toms, i)
			}
		}
	}
	sort.Slice(toms, func(i, j int) bool { return toms[i] < toms[j] })
	c.yellowTomNotes = make(map[uint8]bool)
	c.blueTomNotes = make(map[uint8]bool)
	c.greenTomNotes = make(map[uint8]bool)
	switch len(toms) {
	case 1:
		c.blueTomNotes[toms[0]] = true
	case 2:
		c.greenTomNotes[toms[0]] = true
		c.blueTomNotes[toms[1]] = true
	case 3:
		c.greenTomNotes[toms[0]] = true
		c.blueTomNotes[toms[1]] = true
		c.yellowTomNotes[toms[2]] = true
	default:
		if len(toms) >= 4 {
			c.greenTomNotes[toms[0]] = true
			c.blueTomNotes[toms[1]] = true
			c.yellowTomNotes[toms[2]] = true
			if len(toms) > 3 {
				c.yellowTomNotes[toms[3]] = true
			}
		}
	}
}
