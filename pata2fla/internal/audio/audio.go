// Package audio decodes (WAV, FLAC, MP3, anything else via ffmpeg if present) and writes audio.
package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Audio is planar float32 audio in [-1, 1].
type Audio struct {
	Ch [][]float32
	SR int
}

// Frames returns the number of samples per channel.
func (a *Audio) Frames() int {
	if len(a.Ch) == 0 {
		return 0
	}
	return len(a.Ch[0])
}

// Duration in seconds.
func (a *Audio) Duration() float64 {
	if a.SR <= 0 {
		return 0
	}
	return float64(a.Frames()) / float64(a.SR)
}

// Mono returns the channel average.
func (a *Audio) Mono() []float32 {
	n := a.Frames()
	out := make([]float32, n)
	if len(a.Ch) == 0 {
		return out
	}
	inv := 1.0 / float32(len(a.Ch))
	for i := 0; i < n; i++ {
		var s float32
		for c := range a.Ch {
			s += a.Ch[c][i]
		}
		out[i] = s * inv
	}
	return out
}

// Stereo returns a 2-channel version (mono is duplicated, extra channels are dropped).
func (a *Audio) Stereo() *Audio {
	switch len(a.Ch) {
	case 2:
		return a
	case 1:
		return &Audio{Ch: [][]float32{a.Ch[0], append([]float32(nil), a.Ch[0]...)}, SR: a.SR}
	default:
		return &Audio{Ch: [][]float32{a.Ch[0], a.Ch[1]}, SR: a.SR}
	}
}

// Load decodes an audio file. The format is detected from the file contents.
func Load(path string) (*Audio, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a *Audio
	var derr error
	switch {
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE":
		a, derr = decodeWav(data)
	case len(data) >= 4 && string(data[0:4]) == "fLaC":
		a, derr = decodeFlac(data)
	case isMP3(data):
		a, derr = decodeMP3(data)
	default:
		derr = fmt.Errorf("unknown audio container")
	}
	if derr == nil && a != nil && a.Frames() > 0 {
		return a, nil
	}
	// last resort: ffmpeg, if it is installed
	if fa, ferr := decodeFFmpeg(path); ferr == nil {
		return fa, nil
	}
	if derr == nil {
		derr = fmt.Errorf("file contains no audio")
	}
	return nil, fmt.Errorf("cannot decode %s: %v", filepath.Base(path), derr)
}

func isMP3(d []byte) bool {
	if len(d) >= 3 && string(d[0:3]) == "ID3" {
		return true
	}
	return len(d) >= 2 && d[0] == 0xFF && (d[1]&0xE0) == 0xE0
}

func decodeFFmpeg(path string) (*Audio, error) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}
	const sr = 44100
	cmd := exec.Command(ff, "-v", "error", "-i", path, "-vn", "-f", "f32le", "-acodec", "pcm_f32le",
		"-ac", "2", "-ar", fmt.Sprint(sr), "-")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	b := out.Bytes()
	n := len(b) / 8
	if n == 0 {
		return nil, fmt.Errorf("ffmpeg produced no audio")
	}
	a := &Audio{Ch: [][]float32{make([]float32, n), make([]float32, n)}, SR: sr}
	for i := 0; i < n; i++ {
		a.Ch[0][i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*8:]))
		a.Ch[1][i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*8+4:]))
	}
	return a, nil
}

// ---------------------------------------------------------------------------
// WAV
// ---------------------------------------------------------------------------

func decodeWav(d []byte) (*Audio, error) {
	pos := 12
	var format, channels, bits, blockAlign int
	sr := 0
	var payload []byte
	haveFmt := false
	for pos+8 <= len(d) {
		id := string(d[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(d[pos+4 : pos+8]))
		pos += 8
		if id == "data" {
			end := pos + size
			if size < 0 || end > len(d) || size == 0xFFFFFFFF {
				end = len(d)
			}
			payload = d[pos:end]
			break
		}
		if pos+size > len(d) {
			break
		}
		if id == "fmt " && size >= 16 {
			format = int(binary.LittleEndian.Uint16(d[pos:]))
			channels = int(binary.LittleEndian.Uint16(d[pos+2:]))
			sr = int(binary.LittleEndian.Uint32(d[pos+4:]))
			blockAlign = int(binary.LittleEndian.Uint16(d[pos+12:]))
			bits = int(binary.LittleEndian.Uint16(d[pos+14:]))
			if format == 0xFFFE && size >= 26 {
				format = int(binary.LittleEndian.Uint16(d[pos+24:]))
			}
			haveFmt = true
		}
		pos += size + size%2
	}
	if !haveFmt || payload == nil {
		return nil, fmt.Errorf("WAV: missing fmt or data chunk")
	}
	if channels <= 0 || sr <= 0 {
		return nil, fmt.Errorf("WAV: bad header")
	}
	bps := bits / 8
	if bps == 0 {
		return nil, fmt.Errorf("WAV: bad bit depth %d", bits)
	}
	if blockAlign < channels*bps {
		blockAlign = channels * bps
	}
	n := len(payload) / blockAlign
	a := &Audio{Ch: make([][]float32, channels), SR: sr}
	for c := range a.Ch {
		a.Ch[c] = make([]float32, n)
	}
	for i := 0; i < n; i++ {
		for c := 0; c < channels; c++ {
			p := payload[i*blockAlign+c*bps:]
			var v float32
			switch {
			case format == 3 && bits == 32:
				v = math.Float32frombits(binary.LittleEndian.Uint32(p))
			case format == 3 && bits == 64:
				v = float32(math.Float64frombits(binary.LittleEndian.Uint64(p)))
			case bits == 8:
				v = (float32(p[0]) - 128) / 128
			case bits == 16:
				v = float32(int16(binary.LittleEndian.Uint16(p))) / 32768
			case bits == 24:
				x := int32(p[0]) | int32(p[1])<<8 | int32(p[2])<<16
				if x&0x800000 != 0 {
					x |= ^0xFFFFFF
				}
				v = float32(x) / 8388608
			case bits == 32:
				v = float32(int32(binary.LittleEndian.Uint32(p))) / 2147483648
			default:
				return nil, fmt.Errorf("WAV: unsupported format %d / %d bit", format, bits)
			}
			a.Ch[c][i] = v
		}
	}
	return a, nil
}

// WriteWav writes the audio as PCM WAV (bits = 16 or 24). The file is written atomically.
func WriteWav(path string, a *Audio, bits int) error {
	if bits != 16 && bits != 24 {
		bits = 24
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	ch := len(a.Ch)
	n := a.Frames()
	bps := bits / 8
	dataSize := n * ch * bps
	buf := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	w32 := func(v uint32) { _ = binary.Write(buf, binary.LittleEndian, v) }
	w16 := func(v uint16) { _ = binary.Write(buf, binary.LittleEndian, v) }
	buf.WriteString("RIFF")
	w32(uint32(36 + dataSize))
	buf.WriteString("WAVEfmt ")
	w32(16)
	w16(1)
	w16(uint16(ch))
	w32(uint32(a.SR))
	w32(uint32(a.SR * ch * bps))
	w16(uint16(ch * bps))
	w16(uint16(bits))
	buf.WriteString("data")
	w32(uint32(dataSize))
	for i := 0; i < n; i++ {
		for c := 0; c < ch; c++ {
			s := float64(a.Ch[c][i])
			if s > 1 {
				s = 1
			} else if s < -1 {
				s = -1
			}
			if bits == 16 {
				v := int16(math.Round(s * 32767))
				buf.WriteByte(byte(v))
				buf.WriteByte(byte(v >> 8))
			} else {
				v := int32(math.Round(s * 8388607))
				buf.WriteByte(byte(v))
				buf.WriteByte(byte(v >> 8))
				buf.WriteByte(byte(v >> 16))
			}
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0644); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}

// ReadAudio loads any supported file as mono float32 plus its sample rate.
func ReadAudio(path string) ([]float32, int, error) {
	a, err := Load(path)
	if err != nil {
		return nil, 0, err
	}
	return a.Mono(), a.SR, nil
}

// CopyFile copies src to dst.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
