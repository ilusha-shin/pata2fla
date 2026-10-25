package adtof

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drumai/pata2fla/internal/appdir"
	"github.com/drumai/pata2fla/internal/midi"
)

// pitchToClass maps the General MIDI drum notes written by ADTOF-pytorch to the 5 classes.
func pitchToClass(p int) string {
	switch p {
	case 35, 36:
		return "kick"
	case 37, 38, 39, 40:
		return "snare"
	case 41, 43, 45, 47, 48, 50:
		return "tom"
	case 42, 44, 46:
		return "hihat"
	case 49, 51, 52, 53, 55, 57, 59:
		return "cymbal"
	}
	return ""
}

// findPython returns a Python 3 interpreter: $LUTOMUR_PYTHON, then python / py / python3.
func findPython() (string, error) {
	if env := strings.TrimSpace(os.Getenv("LUTOMUR_PYTHON")); env != "" {
		return env, nil
	}
	for _, c := range []string{"python", "py", "python3"} {
		p, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		out, err := exec.Command(p, "--version").CombinedOutput() // the Windows Store stub fails here
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "Python 3") {
			return p, nil
		}
	}
	return "", fmt.Errorf("Python 3 not found")
}

// runLocal runs the locally installed ADTOF-pytorch on the drum stem WAV (python adtof_local.py)
// and converts the MIDI it writes to onsets.
func (d *Detector) runLocal(mono []float32, sr int) ([]RawOnset, error) {
	if d.StemPath == "" {
		return nil, fmt.Errorf("local ADTOF needs the drum stem file")
	}
	py, err := findPython()
	if err != nil {
		return nil, err
	}
	script, ok := appdir.Resolve("adtof_local.py")
	if !ok {
		return nil, fmt.Errorf("adtof_local.py was not found next to the program")
	}
	tmp, err := os.MkdirTemp("", "lutomur_adtof_")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	outMid := filepath.Join(tmp, "adtof.mid")

	fmt.Println("   [i] Using the local ADTOF-pytorch installation (Python)")
	absStem, _ := filepath.Abs(d.StemPath)
	cmd := exec.Command(py, script, "--audio", absStem, "--out", outMid)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUNBUFFERED=1")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("local ADTOF failed: %v", err)
	}

	notes, err := midi.ReadNotes(outMid)
	if err != nil {
		return nil, fmt.Errorf("cannot read the MIDI written by ADTOF: %v", err)
	}
	cfg := DefaultConfig()
	p99 := loudnessReference(mono)
	var onsets []RawOnset
	counts := map[string]int{}
	for _, n := range notes {
		class := pitchToClass(n.Pitch)
		if class == "" {
			continue
		}
		vel := n.Velocity
		if vel <= 0 {
			vel = 90
		}
		// ADTOF-pytorch writes a flat velocity; take the dynamics from the stem itself
		i0 := int(n.TimeSec * float64(sr))
		i1 := i0 + int(0.03*float64(sr))
		if i1 > len(mono) {
			i1 = len(mono)
		}
		peak := 0.0
		for i := i0; i < i1 && i >= 0; i++ {
			if v := math.Abs(float64(mono[i])); v > peak {
				peak = v
			}
		}
		rel := math.Min(1.0, peak/p99)
		vel = int(math.Round(30 + 97*rel))
		if vel < 1 {
			vel = 1
		}
		if vel > 127 {
			vel = 127
		}
		onsets = append(onsets, RawOnset{TimeSec: n.TimeSec, Pitch: cfg.Pitches[class], Velocity: vel, Class: class})
		counts[class]++
	}
	fmt.Printf("   ADTOF (local) onsets: %d  %v\n", len(onsets), counts)
	if len(onsets) == 0 {
		return nil, fmt.Errorf("local ADTOF found no drum hits")
	}
	return onsets, nil
}
