package chartgen

import (
	"fmt"
	"sort"
	"strings"
)

type ChartNote struct {
	Tick int64
	Lane int
}

// SectionMarker is a song-form label written into [Events] (Clone Hero / Moonscraper).
type SectionMarker struct {
	Tick  int64
	Label string
}

// SongMeta fills [Song] and is also used for song.ini.
type SongMeta struct {
	Name    string
	Artist  string
	Album   string
	Year    string
	Genre   string
	Charter string
}

func formatChart(notes []ChartNote, res int, bpm float64, meta SongMeta, songOffsetSec float64, fullFile bool, sections []SectionMarker) string {
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].Tick == notes[j].Tick {
			return notes[i].Lane > notes[j].Lane
		}
		return notes[i].Tick < notes[j].Tick
	})

	var sb strings.Builder
	if fullFile {
		name := meta.Name
		if name == "" {
			name = "Drum Export"
		}
		genre := meta.Genre
		if genre == "" {
			genre = "rock"
		}
		micro := int(bpm*1000.0 + 0.5)
		if micro <= 0 {
			micro = 120000
		}
		sb.WriteString("[Song]\n{\n")
		sb.WriteString(fmt.Sprintf("  Name = %q\n", name))
		if meta.Artist != "" {
			sb.WriteString(fmt.Sprintf("  Artist = %q\n", meta.Artist))
		}
		if meta.Album != "" {
			sb.WriteString(fmt.Sprintf("  Album = %q\n", meta.Album))
		}
		if meta.Year != "" {
			sb.WriteString(fmt.Sprintf("  Year = \", %s\"\n", meta.Year))
		}
		sb.WriteString(fmt.Sprintf("  Offset = %g\n", songOffsetSec))
		sb.WriteString(fmt.Sprintf("  Resolution = %d\n", res))
		sb.WriteString("  Player2 = bass\n")
		sb.WriteString("  Difficulty = 0\n")
		sb.WriteString("  PreviewStart = 0\n")
		sb.WriteString("  PreviewEnd = 0\n")
		sb.WriteString(fmt.Sprintf("  Genre = %q\n", genre))
		sb.WriteString("  MediaType = \"cd\"\n")
		sb.WriteString("  MusicStream = \"song.wav\"\n")
		sb.WriteString("  DrumStream = \"drums.wav\"\n")
		sb.WriteString("}\n")
		sb.WriteString("[SyncTrack]\n{\n")
		sb.WriteString("  0 = TS 4\n")
		sb.WriteString(fmt.Sprintf("  0 = B %d\n", micro))
		sb.WriteString("}\n")
		sb.WriteString("[Events]\n{\n")
		if len(sections) > 0 {
			secs := append([]SectionMarker(nil), sections...)
			sort.SliceStable(secs, func(i, j int) bool {
				if secs[i].Tick == secs[j].Tick {
					return secs[i].Label < secs[j].Label
				}
				return secs[i].Tick < secs[j].Tick
			})
			for _, s := range secs {
				label := strings.TrimSpace(s.Label)
				if label == "" {
					continue
				}
				tick := s.Tick
				if tick < 0 {
					tick = 0
				}
				sb.WriteString(fmt.Sprintf("  %d = E \"section %s\"\n", tick, label))
			}
		}
		sb.WriteString("}\n")
	}

	sb.WriteString("[ExpertDrums]\n{\n")
	for _, n := range notes {
		sb.WriteString(fmt.Sprintf("  %d = N %d 0\n", n.Tick, n.Lane))
	}
	sb.WriteString("}\n")
	return sb.String()
}
