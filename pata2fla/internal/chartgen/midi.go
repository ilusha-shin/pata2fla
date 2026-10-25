package chartgen

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

type NoteEvent struct {
	AbsoluteTick uint64
	Channel      uint8
	Note         uint8
	Velocity     uint8
}

type MidiData struct {
	TimeDivision uint16
	Events       []NoteEvent
}

func parseMIDI(r io.Reader) (*MidiData, error) {
	var headerTag [4]byte
	if _, err := io.ReadFull(r, headerTag[:]); err != nil {
		return nil, err
	}
	if string(headerTag[:]) != "MThd" {
		return nil, errors.New("invalid MIDI: missing MThd header")
	}

	var headerLen uint32
	if err := binary.Read(r, binary.BigEndian, &headerLen); err != nil {
		return nil, err
	}

	headerBytes := make([]byte, headerLen)
	if _, err := io.ReadFull(r, headerBytes); err != nil {
		return nil, err
	}

	hBuf := bytes.NewReader(headerBytes)
	var format, numTracks, division uint16
	_ = binary.Read(hBuf, binary.BigEndian, &format)
	_ = binary.Read(hBuf, binary.BigEndian, &numTracks)
	_ = binary.Read(hBuf, binary.BigEndian, &division)

	var allEvents []NoteEvent

	for t := 0; t < int(numTracks); t++ {
		var trackTag [4]byte
		if _, err := io.ReadFull(r, trackTag[:]); err != nil {
			break
		}
		for string(trackTag[:]) != "MTrk" {
			var skipLen uint32
			_ = binary.Read(r, binary.BigEndian, &skipLen)
			_, _ = io.CopyN(io.Discard, r, int64(skipLen))
			if _, err := io.ReadFull(r, trackTag[:]); err != nil {
				break
			}
		}

		var trackLen uint32
		if err := binary.Read(r, binary.BigEndian, &trackLen); err != nil {
			return nil, err
		}

		trackBytes := make([]byte, trackLen)
		if _, err := io.ReadFull(r, trackBytes); err != nil {
			return nil, err
		}

		events, err := parseTrackEvents(trackBytes)
		if err == nil {
			allEvents = append(allEvents, events...)
		}
	}

	return &MidiData{
		TimeDivision: division,
		Events:       allEvents,
	}, nil
}

func parseTrackEvents(data []byte) ([]NoteEvent, error) {
	reader := bytes.NewReader(data)
	var currentTick uint64
	var runningStatus byte
	var events []NoteEvent

	for reader.Len() > 0 {
		delta, err := readVarLength(reader)
		if err != nil {
			break
		}
		currentTick += delta

		status, err := reader.ReadByte()
		if err != nil {
			break
		}

		if status < 0x80 {
			_ = reader.UnreadByte()
			status = runningStatus
		} else {
			runningStatus = status
		}

		eventType := status & 0xF0
		channel := status & 0x0F

		switch eventType {
		case 0x90:
			note, _ := reader.ReadByte()
			vel, _ := reader.ReadByte()
			if vel > 0 {
				events = append(events, NoteEvent{
					AbsoluteTick: currentTick,
					Channel:      channel + 1,
					Note:         note,
					Velocity:     vel,
				})
			}
		case 0x80, 0xA0, 0xB0, 0xE0:
			_, _ = reader.ReadByte()
			_, _ = reader.ReadByte()
		case 0xC0, 0xD0:
			_, _ = reader.ReadByte()
		case 0xF0:
			if status == 0xFF {
				_, _ = reader.ReadByte()
				length, _ := readVarLength(reader)
				_, _ = reader.Seek(int64(length), io.SeekCurrent)
			} else {
				length, _ := readVarLength(reader)
				_, _ = reader.Seek(int64(length), io.SeekCurrent)
			}
		}
	}
	return events, nil
}

func readVarLength(r *bytes.Reader) (uint64, error) {
	var value uint64
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value = (value << 7) | uint64(b&0x7F)
		if (b & 0x80) == 0 {
			break
		}
	}
	return value, nil
}
