package midi

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

// NoteOn is one note-on event of a MIDI file with its time in seconds.
type NoteOn struct {
	TimeSec  float64
	Pitch    int
	Velocity int
	Channel  int
}

type tempoPoint struct {
	tick int64
	us   int64
}

func readVLQ(d []byte, p int) (int64, int, bool) {
	var v int64
	for i := 0; i < 5; i++ {
		if p >= len(d) {
			return 0, p, false
		}
		c := d[p]
		p++
		v = v<<7 | int64(c&0x7f)
		if c&0x80 == 0 {
			return v, p, true
		}
	}
	return 0, p, false
}

// ReadNotes parses a Standard MIDI File (format 0 or 1) and returns all note-on events
// (velocity > 0) sorted by time, honouring tempo changes.
func ReadNotes(path string) ([]NoteOn, error) {
	d, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(d) < 14 || string(d[0:4]) != "MThd" {
		return nil, fmt.Errorf("%s is not a MIDI file", path)
	}
	hdrLen := int(binary.BigEndian.Uint32(d[4:8]))
	nTracks := int(binary.BigEndian.Uint16(d[10:12]))
	div := int(binary.BigEndian.Uint16(d[12:14]))
	ppq := div
	if div&0x8000 != 0 || div == 0 {
		ppq = 480 // SMPTE timing is not used by drum transcribers
	}

	type rawNote struct {
		tick          int64
		pitch, vel, c int
	}
	var notes []rawNote
	var tempos []tempoPoint

	pos := 8 + hdrLen
	for t := 0; t < nTracks && pos+8 <= len(d); t++ {
		ln := int(binary.BigEndian.Uint32(d[pos+4 : pos+8]))
		end := pos + 8 + ln
		if end > len(d) || end < pos {
			end = len(d)
		}
		if string(d[pos:pos+4]) != "MTrk" {
			pos = end
			continue
		}
		p := pos + 8
		var tick int64
		var running byte
	events:
		for p < end {
			delta, np, ok := readVLQ(d, p)
			if !ok {
				break
			}
			p = np
			tick += delta
			if p >= end {
				break
			}
			status := d[p]
			if status < 0x80 {
				status = running
				if status == 0 {
					break
				}
			} else {
				p++
				if status < 0xF0 {
					running = status
				}
			}
			switch {
			case status == 0xFF:
				if p >= end {
					break events
				}
				typ := d[p]
				p++
				l, np, ok := readVLQ(d, p)
				if !ok {
					break events
				}
				p = np
				if typ == 0x51 && l == 3 && p+3 <= end {
					us := int64(d[p])<<16 | int64(d[p+1])<<8 | int64(d[p+2])
					tempos = append(tempos, tempoPoint{tick: tick, us: us})
				}
				p += int(l)
			case status == 0xF0 || status == 0xF7:
				l, np, ok := readVLQ(d, p)
				if !ok {
					break events
				}
				p = np + int(l)
			default:
				kind := status & 0xF0
				switch kind {
				case 0x80, 0x90, 0xA0, 0xB0, 0xE0:
					if p+2 > end {
						break events
					}
					d1, d2 := int(d[p]), int(d[p+1])
					p += 2
					if kind == 0x90 && d2 > 0 {
						notes = append(notes, rawNote{tick: tick, pitch: d1, vel: d2, c: int(status & 0x0F)})
					}
				default: // 0xC0, 0xD0: one data byte
					p++
				}
			}
		}
		pos = end
	}

	sort.SliceStable(tempos, func(i, j int) bool { return tempos[i].tick < tempos[j].tick })
	segs := []tempoPoint{{tick: 0, us: 500000}}
	for _, tp := range tempos {
		if tp.tick == 0 {
			segs[0].us = tp.us
		} else {
			segs = append(segs, tp)
		}
	}
	toSec := func(tick int64) float64 {
		s := 0.0
		for i, sg := range segs {
			if i+1 >= len(segs) || tick < segs[i+1].tick {
				return s + float64(tick-sg.tick)*float64(sg.us)/1e6/float64(ppq)
			}
			s += float64(segs[i+1].tick-sg.tick) * float64(sg.us) / 1e6 / float64(ppq)
		}
		return s
	}

	out := make([]NoteOn, 0, len(notes))
	for _, n := range notes {
		out = append(out, NoteOn{TimeSec: toSec(n.tick), Pitch: n.pitch, Velocity: n.vel, Channel: n.c})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TimeSec < out[j].TimeSec })
	return out, nil
}
