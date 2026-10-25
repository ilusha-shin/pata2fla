package audio

import (
	"bytes"
	"io"

	mp3 "github.com/hajimehoshi/go-mp3"
)

// decodeMP3 decodes an MP3 file (go-mp3 outputs 16-bit little-endian stereo).
func decodeMP3(data []byte) (*Audio, error) {
	dec, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(dec)
	if err != nil && len(raw) == 0 {
		return nil, err
	}
	n := len(raw) / 4
	a := &Audio{Ch: [][]float32{make([]float32, n), make([]float32, n)}, SR: dec.SampleRate()}
	for i := 0; i < n; i++ {
		l := int16(uint16(raw[i*4]) | uint16(raw[i*4+1])<<8)
		r := int16(uint16(raw[i*4+2]) | uint16(raw[i*4+3])<<8)
		a.Ch[0][i] = float32(l) / 32768
		a.Ch[1][i] = float32(r) / 32768
	}
	return a, nil
}
