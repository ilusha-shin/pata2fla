package midi

import (
	"bytes"
	"encoding/binary"
	"os"
	"sort"

	"github.com/drumai/pata2fla/internal/nms"
)

var PieceToMidi = map[string]byte{
	"kick":       36,
	"snare":      38,
	"closed_hat": 42,
	"pedal_hat":  44,
	"open_hat":   46,
	"low_tom":    41,
	"floor_tom":  43,
	"mid_tom":    47,
	"high_tom":   50,
	"crash":      49,
	"ride":       51,
}

func WriteMidi(path string, events []nms.Event, bpm float64) error {
	if bpm <= 0 {
		bpm = 120.0
	}

	ppq := 480
	usPerQuarter := int(60000000.0 / bpm)
	secPerTick := (60.0 / bpm) / float64(ppq)

	var trk bytes.Buffer

	// 1. Мета-событие: Темп BPM (FF 51 03 tt tt tt)
	trk.WriteByte(0x00) // Delta 0
	trk.Write([]byte{0xFF, 0x51, 0x03})
	trk.WriteByte(byte((usPerQuarter >> 16) & 0xFF))
	trk.WriteByte(byte((usPerQuarter >> 8) & 0xFF))
	trk.WriteByte(byte(usPerQuarter & 0xFF))
	// Super secret project signature (meta text; not a note)
	const createdBy = "created by pata2fla project"
	writeVLQ(&trk, 0)
	trk.Write([]byte{0xFF, 0x01, byte(len(createdBy))})
	trk.WriteString(createdBy)	

	type MidiMsg struct {
		Tick   int
		Status byte
		Note   byte
		Vel    byte
	}

	var messages []MidiMsg
	durationTicks := int(0.100 / secPerTick) // Длительность ноты 100 мс
	if durationTicks < 24 {
		durationTicks = 24
	}

	for _, e := range events {
		note, ok := PieceToMidi[e.Piece]
		if !ok {
			note = 38
		}
		if e.TimeSec < 0 {
			continue
		}
		onTick := int(e.TimeSec / secPerTick)
		offTick := onTick + durationTicks

		velInt := e.Velocity
		if velInt <= 0 {
			velInt = 90
		}
		if velInt > 127 {
			velInt = 127
		}
		vel := byte(velInt)

		messages = append(messages, MidiMsg{
			Tick:   onTick,
			Status: 0x99, // Note On, Channel 10 (0x99)
			Note:   note,
			Vel:    vel,
		})
		messages = append(messages, MidiMsg{
			Tick:   offTick,
			Status: 0x89, // Note Off, Channel 10 (0x89)
			Note:   note,
			Vel:    0,
		})
	}

	// Stable sort by tick; at the same tick Note Off (0x89) goes before Note On (0x99)
	sort.SliceStable(messages, func(i, j int) bool {
		if messages[i].Tick != messages[j].Tick {
			return messages[i].Tick < messages[j].Tick
		}
		return messages[i].Status < messages[j].Status
	})

	lastTick := 0
	for _, m := range messages {
		delta := m.Tick - lastTick
		if delta < 0 {
			delta = 0
		}
		writeVLQ(&trk, uint32(delta))
		trk.WriteByte(m.Status)
		trk.WriteByte(m.Note)
		trk.WriteByte(m.Vel)
		lastTick = m.Tick
	}

	// Конец дорожки (FF 2F 00)
	writeVLQ(&trk, 0)
	trk.Write([]byte{0xFF, 0x2F, 0x00})

	// Сохраняем заголовок MThd и трек MTrk
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// MThd: Format 0 (single track), 1 track, PPQ
	f.WriteString("MThd")
	binary.Write(f, binary.BigEndian, uint32(6))
	binary.Write(f, binary.BigEndian, uint16(0))   // Format 0
	binary.Write(f, binary.BigEndian, uint16(1))   // 1 track
	binary.Write(f, binary.BigEndian, uint16(ppq)) // PPQ

	// MTrk
	f.WriteString("MTrk")
	binary.Write(f, binary.BigEndian, uint32(trk.Len()))
	f.Write(trk.Bytes())

	return nil
}

func writeVLQ(buf *bytes.Buffer, value uint32) {
	var buffer [4]byte
	i := 3
	buffer[i] = byte(value & 0x7F)
	value >>= 7

	for value > 0 {
		i--
		buffer[i] = byte((value & 0x7F) | 0x80)
		value >>= 7
	}

	buf.Write(buffer[i:])
}
