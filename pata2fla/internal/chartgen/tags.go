package chartgen

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

// ReadTags extracts basic tags from WAV LIST/INFO, FLAC Vorbis, MP3 ID3v2; falls back to filename.
func ReadTags(path string) SongMeta {
	meta := SongMeta{}
	data, err := os.ReadFile(path)
	if err == nil {
		switch {
		case len(data) >= 4 && string(data[:4]) == "fLaC":
			meta = tagsFLAC(data)
		case len(data) >= 3 && string(data[:3]) == "ID3":
			meta = tagsID3(data)
		case len(data) >= 12 && string(data[:4]) == "RIFF":
			meta = tagsWAV(data)
		}
	}
	if meta.Name == "" {
		meta.Name = titleFromFilename(path)
	}
	return meta
}

func titleFromFilename(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	// strip leading track numbers: "01 - Title", "01. Title"
	re := regexp.MustCompile(`(?i)^\d{1,3}\s*[-_.]\s*`)
	name = re.ReplaceAllString(name, "")
	return strings.TrimSpace(name)
}

func tagsWAV(data []byte) SongMeta {
	// find LIST INFO
	m := SongMeta{}
	i := 12
	for i+8 <= len(data) {
		id := string(data[i : i+4])
		sz := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		if id == "LIST" && i+12 <= len(data) && string(data[i+8:i+12]) == "INFO" {
			j := i + 12
			end := i + 8 + sz
			if end > len(data) {
				end = len(data)
			}
			for j+8 <= end {
				cid := string(data[j : j+4])
				csz := int(binary.LittleEndian.Uint32(data[j+4 : j+8]))
				payload := data[j+8 : min(j+8+csz, end)]
				val := string(bytes.TrimRight(payload, "\x00"))
				switch cid {
				case "INAM":
					m.Name = val
				case "IART":
					m.Artist = val
				case "IPRD":
					m.Album = val
				case "ICRD":
					m.Year = firstYear(val)
				case "IGNR":
					m.Genre = val
				}
				j += 8 + csz
				if csz&1 == 1 {
					j++
				}
			}
			return m
		}
		i += 8 + sz
		if sz&1 == 1 {
			i++
		}
	}
	return m
}

func tagsFLAC(data []byte) SongMeta {
	m := SongMeta{}
	// skip fLaC
	pos := 4
	for pos+4 <= len(data) {
		blockType := data[pos] & 0x7F
		last := data[pos]&0x80 != 0
		sz := int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4
		if pos+sz > len(data) {
			break
		}
		payload := data[pos : pos+sz]
		pos += sz
		if blockType == 4 { // VORBIS_COMMENT
			// vendor length LE
			if len(payload) < 4 {
				break
			}
			vlen := int(binary.LittleEndian.Uint32(payload[0:4]))
			p := 4 + vlen
			if p+4 > len(payload) {
				break
			}
			ncom := int(binary.LittleEndian.Uint32(payload[p : p+4]))
			p += 4
			for i := 0; i < ncom && p+4 <= len(payload); i++ {
				clen := int(binary.LittleEndian.Uint32(payload[p : p+4]))
				p += 4
				if p+clen > len(payload) {
					break
				}
				kv := string(payload[p : p+clen])
				p += clen
				eq := strings.IndexByte(kv, '=')
				if eq <= 0 {
					continue
				}
				k := strings.ToUpper(kv[:eq])
				v := kv[eq+1:]
				switch k {
				case "TITLE":
					m.Name = v
				case "ARTIST":
					m.Artist = v
				case "ALBUM":
					m.Album = v
				case "DATE", "YEAR":
					m.Year = firstYear(v)
				case "GENRE":
					m.Genre = v
				}
			}
			break
		}
		if last {
			break
		}
	}
	return m
}

func tagsID3(data []byte) SongMeta {
	m := SongMeta{}
	if len(data) < 10 || string(data[:3]) != "ID3" {
		return m
	}
	// size synchsafe
	size := int(data[6]&0x7F)<<21 | int(data[7]&0x7F)<<14 | int(data[8]&0x7F)<<7 | int(data[9]&0x7F)
	pos := 10
	if data[5]&0x40 != 0 && pos+4 <= len(data) { // extended header
		eh := int(data[pos])<<21 | int(data[pos+1])<<14 | int(data[pos+2])<<7 | int(data[pos+3])
		pos += eh
	}
	end := 10 + size
	if end > len(data) {
		end = len(data)
	}
	for pos+10 <= end {
		id := string(data[pos : pos+4])
		fsz := int(data[pos+4])<<24 | int(data[pos+5])<<16 | int(data[pos+6])<<8 | int(data[pos+7])
		pos += 10
		if fsz <= 0 || pos+fsz > end {
			break
		}
		payload := data[pos : pos+fsz]
		pos += fsz
		val := id3Text(payload)
		switch id {
		case "TIT2":
			m.Name = val
		case "TPE1":
			m.Artist = val
		case "TALB":
			m.Album = val
		case "TDRC", "TYER":
			m.Year = firstYear(val)
		case "TCON":
			m.Genre = val
		}
	}
	return m
}

// id3Text decodes an ID3v2 text frame payload (encoding byte + data).
func id3Text(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	enc := payload[0]
	data := payload[1:]
	if len(data) == 0 {
		return ""
	}

	switch enc {
	case 0x00: // ISO-8859-1
		// single-byte NUL terminator
		for len(data) > 0 && data[len(data)-1] == 0 {
			data = data[:len(data)-1]
		}
		r := make([]rune, len(data))
		for i, c := range data {
			r[i] = rune(c)
		}
		return strings.TrimSpace(string(r))

	case 0x01: // UTF-16 with BOM
		// UTF-16 terminator is 0x00 0x00 — strip only full pairs
		for len(data) >= 2 && data[len(data)-1] == 0 && data[len(data)-2] == 0 {
			data = data[:len(data)-2]
		}
		if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
			return strings.TrimSpace(utf16ToString(data[2:], true))
		}
		if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
			return strings.TrimSpace(utf16ToString(data[2:], false))
		}
		return strings.TrimSpace(utf16ToString(data, false))

	case 0x02: // UTF-16BE, no BOM
		for len(data) >= 2 && data[len(data)-1] == 0 && data[len(data)-2] == 0 {
			data = data[:len(data)-2]
		}
		return strings.TrimSpace(utf16ToString(data, true))

	case 0x03: // UTF-8
		for len(data) > 0 && data[len(data)-1] == 0 {
			data = data[:len(data)-1]
		}
		return strings.TrimSpace(string(data))

	default:
		return strings.TrimSpace(string(data))
	}
}

func utf16ToString(b []byte, bigEndian bool) string {
	if len(b) < 2 {
		return ""
	}
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := 0; i < len(u); i++ {
		if bigEndian {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	return string(utf16.Decode(u))
}

func firstYear(s string) string {
	s = strings.TrimSpace(s)
	// "2005", "2005-06-15", "2005/06/15"
	for i := 0; i+3 < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' &&
			s[i+1] >= '0' && s[i+1] <= '9' &&
			s[i+2] >= '0' && s[i+2] <= '9' &&
			s[i+3] >= '0' && s[i+3] <= '9' {
			return s[i : i+4]
		}
	}
	return s // fallback
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
