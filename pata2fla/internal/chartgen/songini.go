package chartgen

import (
	"fmt"
	"os"
	"strings"
)

// Rainbow charter badge for Clone Hero / Moonscraper song.ini (HTML-like markup).
const charterBadge = `<b><color=#ffadad>p</color><color=#ffc8a2>a</color><color=#f9eaab>t</color><color=#d8ffb8>a</color><color=#a2fee0>2</color><color=#90deff>f</color><color=#acbdff>l</color><color=#dab3ff>a</color></b>`

// WriteSongIni writes Clone Hero song.ini (UTF-8 with BOM for Windows Cyrillic).
func WriteSongIni(path string, meta SongMeta, songLengthMs int, delayMs int) error {
	name := meta.Name
	if name == "" {
		name = "Unknown"
	}
	var b strings.Builder
	b.WriteString("[song]\n")
	b.WriteString(fmt.Sprintf("name = %s\n", name))
	b.WriteString(fmt.Sprintf("artist = %s\n", meta.Artist))
	b.WriteString(fmt.Sprintf("album = %s\n", meta.Album))
	b.WriteString(fmt.Sprintf("genre = %s\n", meta.Genre))
	b.WriteString(fmt.Sprintf("year = %s\n", meta.Year))
	b.WriteString(fmt.Sprintf("song_length = %d\n", songLengthMs))
	b.WriteString(fmt.Sprintf("delay = %d\n", delayMs))
	b.WriteString("diff_band = -1\n")
	b.WriteString("diff_guitar = -1\n")
	b.WriteString("diff_bass = -1\n")
	b.WriteString("diff_drums = 3\n")
	b.WriteString("diff_drums_real = 3\n")
	b.WriteString("diff_vocals = -1\n")
	b.WriteString("pro_drums = True\n")
	b.WriteString("five_lane_drums = False\n")
	b.WriteString("charter = " + charterBadge + "\n")
	b.WriteString("icon = IconPata2Fla\n")

	// UTF-8 BOM so Windows does not treat the file as CP1251
	out := append([]byte{0xEF, 0xBB, 0xBF}, []byte(b.String())...)
	return os.WriteFile(path, out, 0644)
}
