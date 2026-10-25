// Command pata2fla — CLI entrypoint (no Gio).
package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drumai/pata2fla/internal/appdir"
	"github.com/drumai/pata2fla/internal/pipeline"
)

func cleanArg(s string) string {
	return strings.Trim(strings.TrimSpace(s), "\"'")
}

// parseBPM: 20–350, optional one decimal digit (90 / 90.7). Comma decimal accepted.
func parseBPM(s string) (float64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 120, nil
	}
	if strings.Count(s, ".") > 1 {
		return 0, fmt.Errorf("invalid BPM %q (use e.g. 90 or 90.7)", s)
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 > 1 {
		return 0, fmt.Errorf("BPM allows at most one digit after the decimal point (e.g. 90.7), got %q", s)
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, fmt.Errorf("BPM must be positive (20–350), got %q", s)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid BPM %q (use a number such as 90 or 95.3)", s)
	}
	if v < 20 || v > 350 {
		return 0, fmt.Errorf("BPM %.1f is out of range 20-350", v)
	}
	return math.Round(v*10) / 10, nil
}

func parseArgs(args []string) pipeline.Options {
	opt := pipeline.Options{
		Out:        filepath.Join(appdir.Dir(), "output"),
		Models:     "models",
		BPM:        120.0,
		ChartQuant: 24,
		NoChart:    false,
	}
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}

		raw := strings.TrimLeft(arg, "-")
		name := raw
		value := ""
		hasValue := false
		if eq := strings.Index(raw, "="); eq >= 0 {
			name = raw[:eq]
			value = raw[eq+1:]
			hasValue = true
		}
		name = strings.ToLower(name)

		switch name {
		case "drums-only", "drums_only", "skip-step1", "skip_step1":
			opt.DrumsOnly = true
		case "no-cnn", "no_cnn":
			opt.NoCnn = true
		case "allow-fallback", "allow_fallback":
			opt.AllowFallback = true
		case "no-chart", "no_chart":
			opt.NoChart = true
		case "gui", "cli":
			// ignored in CLI binary
		case "h", "help":
			printUsage()
			os.Exit(0)
		case "i", "input", "o", "out", "output", "m", "models", "b", "bpm", "threshold", "adtof", "quantize", "quant":
			if !hasValue {
				if i+1 < len(args) {
					i++
					value = args[i]
				} else {
					fmt.Printf("[WARN] option %s needs a value\n", arg)
					continue
				}
			}
			value = cleanArg(value)
			switch name {
			case "i", "input":
				opt.Input = value
			case "o", "out", "output":
				opt.Out = value
			case "m", "models":
				opt.Models = value
			case "b", "bpm":
				v, err := parseBPM(value)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
					os.Exit(1)
				}
				opt.BPM = v
			case "threshold":
				if v, err := strconv.ParseFloat(value, 64); err == nil {
					opt.Threshold = v
				}
			case "adtof":
				opt.AdtofMode = value
			case "quantize", "quant":
				if v, err := strconv.Atoi(value); err == nil && (v == 0 || v == 16 || v == 24 || v == 32) {
					opt.ChartQuant = v
				} else {
					fmt.Printf("[WARN] invalid --quantize %q (use 0,16,24,32)\n", value)
				}
			}
		default:
			fmt.Printf("[WARN] unknown option: %s\n", arg)
		}
	}

	if opt.Input == "" && len(positional) > 0 {
		opt.Input = cleanArg(positional[0])
	}
	if len(positional) > 1 {
		opt.Out = cleanArg(positional[1])
	}
	if len(positional) > 2 {
		v, err := parseBPM(cleanArg(positional[2]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
			os.Exit(1)
		}
		opt.BPM = v
	}

	return opt
}

func printUsage() {
	fmt.Println("PATA2FLA — AI Drum Transcription & Clone Hero Generator (CLI)")
	fmt.Println("Usage:")
	fmt.Println("  pata2fla -i song.flac -o output --bpm 120 --quantize 24")
	fmt.Println("  pata2fla --drums-only -i drums.wav -o output")
	fmt.Println("Options:")
	fmt.Println("  -i/--input PATH       input mix or drum stem")
	fmt.Println("  -o/--out DIR          output directory")
	fmt.Println("  --models DIR          models folder (default: models)")
	fmt.Println("  --bpm N               tempo 20–350, optional 1 decimal (default 120)")
	fmt.Println("  --quantize 16|24|32|0 chart grid (default 24)")
	fmt.Println("  --drums-only          input is already isolated drums")
	fmt.Println("  --no-chart            skip Clone Hero pack")
	fmt.Println("  --no-cnn              skip ONNX specialists")
	fmt.Println("GUI: build and run pata2fla_ui from ./cmd/pata2fla_ui")
}

func main() {
	opt := parseArgs(os.Args[1:])
	if opt.Input == "" {
		printUsage()
		os.Exit(1)
	}

	fmt.Println("===================================================================")
	fmt.Println("PATA2FLA CLI")
	fmt.Printf("input=%s\n", opt.Input)
	fmt.Printf("out=%s  bpm=%.2f  quantize=%d  drumsOnly=%v\n", opt.Out, opt.BPM, opt.ChartQuant, opt.DrumsOnly)
	fmt.Println("===================================================================")

	err := pipeline.Process(opt, func(msg string) {
		msg = strings.TrimRight(msg, "\r\n")
		if msg == "" {
			fmt.Println()
			return
		}
		fmt.Println(msg)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] %v\n", err)
		os.Exit(1)
	}
}
