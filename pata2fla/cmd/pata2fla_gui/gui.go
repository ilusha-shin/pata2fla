package main

import (
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/sqweek/dialog"

	"github.com/drumai/pata2fla/internal/pipeline"
)

var (
	colorBgDark       = color.NRGBA{R: 15, G: 23, B: 42, A: 255}   // slate-900
	colorCardBg       = color.NRGBA{R: 30, G: 41, B: 59, A: 255}   // slate-800
	colorCardBorder   = color.NRGBA{R: 51, G: 65, B: 85, A: 255}   // slate-700
	colorInputBg      = color.NRGBA{R: 15, G: 23, B: 42, A: 255}   // slate-950
	colorCyanAccent   = color.NRGBA{R: 6, G: 182, B: 212, A: 255}  // cyan-500
	colorPurpleAccent = color.NRGBA{R: 168, G: 85, B: 247, A: 255} // purple-500
	colorTextLight    = color.NRGBA{R: 248, G: 250, B: 252, A: 255} // slate-50
	colorTextMuted    = color.NRGBA{R: 148, G: 163, B: 184, A: 255} // slate-400
	colorTextDim      = color.NRGBA{R: 100, G: 116, B: 139, A: 255} // slate-500
	colorGreen        = color.NRGBA{R: 34, G: 197, B: 94, A: 255}  // emerald-500
	colorRed          = color.NRGBA{R: 239, G: 68, B: 68, A: 255}  // red-500
	colorConsoleBg    = color.NRGBA{R: 2, G: 6, B: 23, A: 255}     // slate-950
)

// =========================================================================
// HEADER & LOGO SIZE SETTINGS
// =========================================================================
const (
	// Базовая высота хедера (dp)
	HeaderHeightDp = 160

	// Максимальная высота хедера (dp)
	HeaderMaxHeightDp = 220

	// Ширина логотипа в процентах от ширины хедера (75 = 75%)
	LogoWidthPercent = 90

	// Жёсткий лимит высоты логотипа (0 = без лимита)
	LogoMaxHeightDp = 0
)

type QuantizeChoice struct {
	Index int
	Value int
	Label string
	Click widget.Clickable
}

type GUIState struct {
	mu sync.Mutex

	window *app.Window

	// Header images
	headerBgImg image.Image
	headerBgOp  paint.ImageOp
	hasHeaderBg bool

	logoImg image.Image
	logoOp  paint.ImageOp
	hasLogo bool

	// Input
	inputEditor        widget.Editor
	btnBrowseInput     widget.Clickable
	btnOpenInputFolder widget.Clickable

	// Output
	outputEditor        widget.Editor
	btnBrowseOutput     widget.Clickable
	btnOpenOutputFolder widget.Clickable

	// BPM
	bpmEditor widget.Editor

	// Quantize
	quantizeDropdownOpen bool
	btnQuantizeToggle    widget.Clickable
	quantizeChoices      []QuantizeChoice
	selectedQuantize     int
	drumsOnly            bool
	btnDrumsOnly         widget.Clickable

	// Start
	btnStart widget.Clickable

	// State
	running      bool
	progress     float32
	statusMsg    string
	logs         []string
	logList      widget.List
	btnClearLogs widget.Clickable
}

func loadFirstImage(candidates ...string) image.Image {
	for _, path := range candidates {
		if path == "" {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		img, _, err := image.Decode(f)
		_ = f.Close()
		if err == nil && img != nil {
			return img
		}
	}
	return nil
}

func newGUIState(w *app.Window) *GUIState {
	s := &GUIState{
		window:           w,
		selectedQuantize: 2, // 24
		statusMsg:        "Ready. Select an audio file and click START CONVERSION.",
		progress:         0.0,
		quantizeChoices: []QuantizeChoice{
			{Index: 0, Value: 0, Label: "off"},
			{Index: 1, Value: 16, Label: "1/16"},
			{Index: 2, Value: 24, Label: "1/24"},
			{Index: 3, Value: 32, Label: "1/32"},
		},
	}

	s.inputEditor.SingleLine = true
	s.inputEditor.Submit = true

	defaultInput := filepath.Join("input", "track.wav")
	if _, err := os.Stat(defaultInput); err == nil {
		s.inputEditor.SetText(defaultInput)
	} else if files, err := os.ReadDir("input"); err == nil {
		for _, f := range files {
			ext := strings.ToLower(filepath.Ext(f.Name()))
			if ext == ".wav" || ext == ".flac" || ext == ".mp3" {
				s.inputEditor.SetText(filepath.Join("input", f.Name()))
				break
			}
		}
	}
	if s.inputEditor.Text() == "" {
		s.inputEditor.SetText(filepath.Join("input", "track.wav"))
	}

	s.outputEditor.SingleLine = true
	s.outputEditor.Submit = true
	s.outputEditor.SetText("output")

	s.bpmEditor.SingleLine = true
	s.bpmEditor.Submit = true
	// Digits + decimal point only (float BPM e.g. 95.3).
	s.bpmEditor.Filter = "0123456789."
	s.bpmEditor.SetText("120")

	s.logList.List.Axis = layout.Vertical
	s.logList.List.ScrollToEnd = true

	// Background
	bgImg := loadFirstImage(
		filepath.Join("assets", "header_bg.png"),
		filepath.Join("assets", "header_bg.jpg"),
		filepath.Join("assets", "background.png"),
		filepath.Join("assets", "background.jpg"),
		filepath.Join("assets", "bg.png"),
		filepath.Join("assets", "bg.jpg"),
		"header_bg.png",
		"header_bg.jpg",
		"background.png",
		"background.jpg",
		"bg.png",
	)
	if bgImg != nil {
		s.headerBgImg = bgImg
		s.headerBgOp = paint.NewImageOp(bgImg)
		s.hasHeaderBg = true
	}

	// Logo
	logoImg := loadFirstImage(
		filepath.Join("assets", "pata_logo-removebg-preview(1).png"),
		filepath.Join("assets", "logo.jpg"),
		filepath.Join("assets", "pata_logo.png"),
		filepath.Join("assets", "logo.png"),
		"pata_logo-removebg-preview(1).png",
		"pata_logo.png",
		"logo.png",
		"logo.jpg",
	)
	if logoImg != nil {
		s.logoImg = logoImg
		s.logoOp = paint.NewImageOp(logoImg)
		s.hasLogo = true
	}

	return s
}

func (s *GUIState) browseInputFile() {
	path, err := dialog.File().
		Filter("Audio Files", "wav", "flac", "mp3").
		Filter("All Files", "*").
		Title("Select Input Audio File").
		Load()
	if err != nil {
		return
	}
	s.mu.Lock()
	s.inputEditor.SetText(path)
	s.mu.Unlock()
	s.window.Invalidate()
}

func (s *GUIState) browseOutputDir() {
	path, err := dialog.Directory().
		Title("Select Output Directory").
		Browse()
	if err != nil {
		return
	}
	s.mu.Lock()
	s.outputEditor.SetText(path)
	s.mu.Unlock()
	s.window.Invalidate()
}

func (s *GUIState) openFolder(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "."
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = filepath.Dir(dir)
	}
	_ = os.MkdirAll(dir, 0755)

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	_ = cmd.Start()
}


// sanitizeBPMInput keeps only a non-negative number with at most one decimal digit:
// OK: "", "90", "90.", "90.7"
// Not OK while typing: second '.', two+ digits after '.', '-'.
func sanitizeBPMInput(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	dot := false
	frac := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			if dot {
				if frac >= 1 {
					continue // max one digit after '.'
				}
				frac++
			}
			b.WriteRune(r)
		case r == '.' && !dot:
			dot = true
			b.WriteRune(r)
		default:
			// skip '-', second '.', letters, etc.
		}
	}
	return b.String()
}

func (s *GUIState) runPipeline() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}

	inputPath := strings.TrimSpace(s.inputEditor.Text())
	outDir := strings.TrimSpace(s.outputEditor.Text())
	bpmStr := strings.TrimSpace(s.bpmEditor.Text())

	if inputPath == "" {
		s.logs = append(s.logs, "[ERROR] Please specify an input audio file!")
		s.statusMsg = "Error: no audio file specified"
		s.mu.Unlock()
		s.window.Invalidate()
		return
	}
	if _, err := os.Stat(inputPath); err != nil {
		s.logs = append(s.logs, fmt.Sprintf("[ERROR] Input file not found: %s", inputPath))
		s.statusMsg = "Error: file not found"
		s.mu.Unlock()
		s.window.Invalidate()
		return
	}
	if outDir == "" {
		outDir = "output"
	}

	// Empty BPM → default 120. Explicit value must be in [20, 350].
	var bpmVal float64
	if bpmStr == "" {
		bpmVal = 120.0
	} else {
		bpmStr = sanitizeBPMInput(bpmStr)
		// Format: integer or exactly one digit after decimal (90 / 90.7), not 90.75 or 72.1.3
		if strings.Count(bpmStr, ".") > 1 {
			s.logs = append(s.logs, "[ERROR] Invalid BPM format. Use e.g. 90 or 90.7")
			s.statusMsg = "Error: invalid BPM format"
			s.mu.Unlock()
			s.window.Invalidate()
			return
		}
		if i := strings.IndexByte(bpmStr, '.'); i >= 0 && len(bpmStr)-i-1 > 1 {
			s.logs = append(s.logs, "[ERROR] BPM allows at most one digit after the decimal point (e.g. 90.7)")
			s.statusMsg = "Error: invalid BPM format"
			s.mu.Unlock()
			s.window.Invalidate()
			return
		}
		v, err := strconv.ParseFloat(bpmStr, 64)
		if err != nil {
			s.logs = append(s.logs, "[ERROR] Invalid BPM. Enter a number between 20 and 350 (e.g. 95.3).")
			s.statusMsg = "Error: invalid BPM"
			s.mu.Unlock()
			s.window.Invalidate()
			return
		}
		if v < 20 || v > 350 {
			s.logs = append(s.logs, fmt.Sprintf(
				"[ERROR] BPM %.3f is outside the allowed range 20–350", v))
			s.statusMsg = "Error: BPM out of range (20–350)"
			s.mu.Unlock()
			s.window.Invalidate()
			return
		}
		bpmVal = v
	}

	// Empty file early check (also enforced in Process).
	if st, err := os.Stat(inputPath); err == nil && st.Size() == 0 {
		s.logs = append(s.logs, "[ERROR] Input file is empty (0 bytes)")
		s.statusMsg = "Error: empty input file"
		s.mu.Unlock()
		s.window.Invalidate()
		return
	}

	quantVal := s.quantizeChoices[s.selectedQuantize].Value

	s.running = true
	s.progress = 0.05
	s.statusMsg = "Initializing AI drum transcription pipeline..."
	s.logs = append(s.logs, fmt.Sprintf("=== PATA2FLA STARTED: %s ===", filepath.Base(inputPath)))
	s.logs = append(s.logs, fmt.Sprintf(">> BPM: %.3f | Quantize: %d | drumsOnly=%v | Output: %s", bpmVal, quantVal, s.drumsOnly, outDir))
	s.logs = append(s.logs, "[i] Do not close the window until the pipeline finishes.")
	s.mu.Unlock()
	s.window.Invalidate()

	opt := pipeline.Options{
		Input:      inputPath,
		Out:        outDir,
		Models:     "models",
		BPM:        bpmVal,
		ChartQuant: quantVal,
		NoChart:    false,
		DrumsOnly:  s.drumsOnly,
	}

	logCallback := func(msg string) {
		s.mu.Lock()
		s.logs = append(s.logs, msg)
		s.statusMsg = msg
		switch {
		case strings.Contains(msg, "[Step 1]"):
			s.progress = 0.15
		case strings.Contains(msg, "[Step 2]"):
			s.progress = 0.35
		case strings.Contains(msg, "[Step 3]"):
			s.progress = 0.55
		case strings.Contains(msg, "[Step 4]"):
			s.progress = 0.70
		case strings.Contains(msg, "[Step 5]"):
			s.progress = 0.85
		case strings.Contains(msg, "[Step 6]"):
			s.progress = 0.92
		case strings.Contains(msg, "[Step 7]"):
			s.progress = 0.98
		}
		s.mu.Unlock()
		s.window.Invalidate()
	}

	procErr := pipeline.Process(opt, logCallback)

	s.mu.Lock()
	s.running = false
	if procErr != nil {
		s.statusMsg = fmt.Sprintf("Error: %v", procErr)
		s.logs = append(s.logs, fmt.Sprintf("[ERROR] %v", procErr))
	} else {
		s.progress = 1.0
		s.statusMsg = "Done! Clone Hero (.chart) and MIDI created successfully."
		s.logs = append(s.logs, "[SUCCESS] Transcription pipeline completed!")
		trackBase := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
		s.logs = append(s.logs, fmt.Sprintf(">> Results: %s", filepath.Join(outDir, trackBase)))
	}
	s.mu.Unlock()
	s.window.Invalidate()
}

func layoutCard(gtx layout.Context, content layout.Widget) layout.Dimensions {
	radius := unit.Dp(8)
	return widget.Border{
		Color:        colorCardBorder,
		CornerRadius: radius,
		Width:        unit.Dp(1),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, colorCardBg, clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, gtx.Dp(radius)).Op(gtx.Ops))
		return layout.UniformInset(unit.Dp(10)).Layout(gtx, content)
	})
}

func layoutInputBox(gtx layout.Context, th *material.Theme, ed *widget.Editor, hint string) layout.Dimensions {
	radius := unit.Dp(6)
	return widget.Border{
		Color:        colorCardBorder,
		CornerRadius: radius,
		Width:        unit.Dp(1),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, colorInputBg, clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, gtx.Dp(radius)).Op(gtx.Ops))
		return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			mEd := material.Editor(th, ed, hint)
			mEd.Color = colorTextLight
			mEd.HintColor = colorTextDim
			mEd.TextSize = unit.Sp(13)
			return mEd.Layout(gtx)
		})
	})
}

func layoutButton(gtx layout.Context, th *material.Theme, clk *widget.Clickable, txt string, bg, fg color.NRGBA) layout.Dimensions {
	btn := material.Button(th, clk, txt)
	btn.Background = bg
	btn.Color = fg
	btn.CornerRadius = unit.Dp(6)
	btn.Inset = layout.Inset{
		Top: unit.Dp(7), Bottom: unit.Dp(7),
		Left: unit.Dp(12), Right: unit.Dp(12),
	}
	btn.TextSize = unit.Sp(12)
	return btn.Layout(gtx)
}

// ===================== HEADER =====================

func (s *GUIState) layoutHeader(gtx layout.Context, th *material.Theme) layout.Dimensions {
	radius := unit.Dp(8)

	// Фиксированная комфортная высота
	calcHeight := gtx.Dp(unit.Dp(HeaderHeightDp))

	// Если есть фон — немного подстраиваем высоту под него, но в разумных пределах
	if s.hasHeaderBg && s.headerBgImg != nil {
		b := s.headerBgImg.Bounds()
		if b.Dx() > 0 && b.Dy() > 0 {
			ratio := float32(b.Dy()) / float32(b.Dx())
			bgHeight := int(float32(gtx.Constraints.Max.X) * ratio)
			// Берём среднее, чтобы не было слишком высоко/низко
			calcHeight = (calcHeight + bgHeight) / 2
		}
	}

	// Жёсткие лимиты
	minH := gtx.Dp(unit.Dp(HeaderHeightDp))
	maxH := gtx.Dp(unit.Dp(HeaderMaxHeightDp))
	if calcHeight < minH {
		calcHeight = minH
	}
	if calcHeight > maxH {
		calcHeight = maxH
	}

	headerSize := image.Point{X: gtx.Constraints.Max.X, Y: calcHeight}

	return widget.Border{
		Color:        colorCardBorder,
		CornerRadius: radius,
		Width:        unit.Dp(1),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Constraints{Min: headerSize, Max: headerSize}

		// Клип
		clipP := clip.UniformRRect(image.Rectangle{Max: headerSize}, gtx.Dp(radius)).Push(gtx.Ops)
		defer clipP.Pop()

		// Базовый фон
		paint.FillShape(gtx.Ops, colorCardBg, clip.Rect{Max: headerSize}.Op())

		// Фоновая картинка
		if s.hasHeaderBg {
			bg := widget.Image{
				Src:      s.headerBgOp,
				Fit:      widget.Cover,
				Position: layout.Center,
			}
			gBg := gtx
			gBg.Constraints = layout.Constraints{Min: headerSize, Max: headerSize}
			bg.Layout(gBg)

			// Лёгкий затемняющий слой
			paint.FillShape(gtx.Ops,
				color.NRGBA{R: 15, G: 23, B: 42, A: 70},
				clip.Rect{Max: headerSize}.Op())
		}

		// ===== ЛОГОТИП =====
		if s.hasLogo && s.logoImg != nil {
			b := s.logoImg.Bounds()
			if b.Dx() > 0 && b.Dy() > 0 {
				logoRatio := float32(b.Dy()) / float32(b.Dx())

				// 75% от ширины хедера
				maxLogoW := int(float32(headerSize.X) * float32(LogoWidthPercent) / 100)
				// 75% от высоты хедера
				maxLogoH := int(float32(headerSize.Y) * float32(LogoWidthPercent) / 100)

				// Вписываем с сохранением пропорций
				targetW := maxLogoW
				targetH := int(float32(targetW) * logoRatio)

				if targetH > maxLogoH {
					targetH = maxLogoH
					targetW = int(float32(targetH) / logoRatio)
				}

				// Опциональный жёсткий лимит
				if LogoMaxHeightDp > 0 {
					capH := gtx.Dp(unit.Dp(LogoMaxHeightDp))
					if targetH > capH {
						targetH = capH
						targetW = int(float32(targetH) / logoRatio)
					}
				}

				logoSize := image.Point{X: targetW, Y: targetH}

				// Центрируем
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gLogo := gtx
					gLogo.Constraints = layout.Constraints{
						Min: logoSize,
						Max: logoSize,
					}
					return widget.Image{
						Src:      s.logoOp,
						Fit:      widget.Contain,
						Position: layout.Center,
					}.Layout(gLogo)
				})
			}
		}

		// Fallback текст
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lbl := material.Label(th, unit.Sp(26), "🥁 PATA2FLA")
			lbl.Color = colorCyanAccent
			return lbl.Layout(gtx)
		})
	})
}

// ===================== CARDS =====================

func (s *GUIState) layoutInputCard(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layoutCard(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Label(th, unit.Sp(12), "INPUT AUDIO FILE (.wav, .flac, .mp3)")
				lbl.Color = colorCyanAccent
				return lbl.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layoutInputBox(gtx, th, &s.inputEditor, "e.g. input/track.wav or click Browse...")
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutButton(gtx, th, &s.btnBrowseInput, "Browse...", colorCyanAccent, color.NRGBA{R: 2, G: 6, B: 23, A: 255})
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutButton(gtx, th, &s.btnOpenInputFolder, "Input Folder", colorCardBorder, colorTextLight)
					}),
				)
			}),
		)
	})
}

func (s *GUIState) layoutOutputCard(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layoutCard(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				lbl := material.Label(th, unit.Sp(12), "OUTPUT DIRECTORY")
				lbl.Color = colorCyanAccent
				return lbl.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layoutInputBox(gtx, th, &s.outputEditor, "e.g. output")
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutButton(gtx, th, &s.btnBrowseOutput, "Browse...", colorCyanAccent, color.NRGBA{R: 2, G: 6, B: 23, A: 255})
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutButton(gtx, th, &s.btnOpenOutputFolder, "Output Folder", colorCardBorder, colorTextLight)
					}),
				)
			}),
		)
	})
}

func (s *GUIState) layoutSettingsCard(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layoutCard(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
			layout.Flexed(0.40, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Label(th, unit.Sp(12), "BPM")
						lbl.Color = colorCyanAccent
						return lbl.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gCopy := gtx
						gCopy.Constraints.Min.X = gtx.Dp(unit.Dp(100))
						gCopy.Constraints.Max.X = gtx.Dp(unit.Dp(140))
						return layoutInputBox(gCopy, th, &s.bpmEditor, "120")
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(24)}.Layout),
			layout.Flexed(0.60, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Label(th, unit.Sp(12), "QUANTIZE")
						lbl.Color = colorPurpleAccent
						return lbl.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						current := s.quantizeChoices[s.selectedQuantize]
						arrow := "▼"
						if s.quantizeDropdownOpen {
							arrow = "▲"
						}
						btnText := fmt.Sprintf("%d   %s", current.Value, arrow)
						btn := material.Button(th, &s.btnQuantizeToggle, btnText)
						btn.Background = colorPurpleAccent
						btn.Color = color.NRGBA{R: 2, G: 6, B: 23, A: 255}
						btn.CornerRadius = unit.Dp(6)
						btn.Inset = layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(18), Right: unit.Dp(18)}
						btn.TextSize = unit.Sp(13)
						gCopy := gtx
						gCopy.Constraints.Min.X = gtx.Dp(unit.Dp(110))
						gCopy.Constraints.Max.X = gtx.Dp(unit.Dp(130))
						return btn.Layout(gCopy)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !s.quantizeDropdownOpen {
							return layout.Dimensions{}
						}
						return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return widget.Border{
								Color: colorPurpleAccent, CornerRadius: unit.Dp(6), Width: unit.Dp(1),
							}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								paint.FillShape(gtx.Ops, colorInputBg, clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, gtx.Dp(6)).Op(gtx.Ops))
								var items []layout.FlexChild
								for i := range s.quantizeChoices {
									idx := i
									item := &s.quantizeChoices[idx]
									items = append(items, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										isSelected := idx == s.selectedQuantize
										bg := colorInputBg
										fg := colorTextLight
										if isSelected {
											bg = color.NRGBA{R: 88, G: 28, B: 135, A: 255}
											fg = colorPurpleAccent
										}
										txt := fmt.Sprintf("%d", item.Value)
										if isSelected {
											txt = fmt.Sprintf("%d   ✓", item.Value)
										}
										btn := material.Button(th, &item.Click, txt)
										btn.Background = bg
										btn.Color = fg
										btn.CornerRadius = unit.Dp(4)
										btn.Inset = layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7), Left: unit.Dp(14), Right: unit.Dp(14)}
										btn.TextSize = unit.Sp(13)
										gCopy := gtx
										gCopy.Constraints.Min.X = gtx.Dp(unit.Dp(110))
										gCopy.Constraints.Max.X = gtx.Dp(unit.Dp(130))
										return btn.Layout(gCopy)
									}))
									if idx < len(s.quantizeChoices)-1 {
										items = append(items, layout.Rigid(layout.Spacer{Height: unit.Dp(2)}.Layout))
									}
								}
								return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx, items...)
								})
							})
						})
					}),
				)
			}),
		)
	})
}

func (s *GUIState) layoutActionCard(gtx layout.Context, th *material.Theme) layout.Dimensions {
	s.mu.Lock()
	running := s.running
	progress := s.progress
	statusMsg := s.statusMsg
	s.mu.Unlock()

	return layoutCard(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				txt := "▶ START CONVERSION"
				bg := colorCyanAccent
				fg := color.NRGBA{R: 2, G: 6, B: 23, A: 255}
				if running {
					txt = "⏳ PROCESSING..."
					bg = colorCardBorder
					fg = colorTextDim
				}
				btn := material.Button(th, &s.btnStart, txt)
				btn.Background = bg
				btn.Color = fg
				btn.CornerRadius = unit.Dp(8)
				btn.Inset = layout.Inset{Top: unit.Dp(11), Bottom: unit.Dp(11), Left: unit.Dp(22), Right: unit.Dp(22)}
				btn.TextSize = unit.Sp(13)
				return btn.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								lbl := material.Label(th, unit.Sp(12), statusMsg)
								lbl.Color = colorTextLight
								return lbl.Layout(gtx)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								lbl := material.Label(th, unit.Sp(12), fmt.Sprintf("%.0f%%", progress*100))
								lbl.Color = colorCyanAccent
								return lbl.Layout(gtx)
							}),
						)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						bar := material.ProgressBar(th, progress)
						bar.Color = colorCyanAccent
						bar.TrackColor = colorInputBg
						return bar.Layout(gtx)
					}),
				)
			}),
		)
	})
}

func (s *GUIState) layoutConsole(gtx layout.Context, th *material.Theme) layout.Dimensions {
	s.mu.Lock()
	nLogs := len(s.logs)
	logsCopy := make([]string, len(s.logs))
	copy(logsCopy, s.logs)
	s.mu.Unlock()

	return layoutCard(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Label(th, unit.Sp(12), "REAL-TIME CONSOLE LOG")
						lbl.Color = colorCyanAccent
						return lbl.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lbl := material.Label(th, unit.Sp(11), fmt.Sprintf("(%d lines)", nLogs))
						lbl.Color = colorTextDim
						return lbl.Layout(gtx)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layoutButton(gtx, th, &s.btnClearLogs, "Clear Log", colorCardBorder, colorTextMuted)
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return widget.Border{
					Color: colorCardBorder, CornerRadius: unit.Dp(4), Width: unit.Dp(1),
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					paint.FillShape(gtx.Ops, colorConsoleBg, clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, gtx.Dp(4)).Op(gtx.Ops))
					return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if len(logsCopy) == 0 {
							lbl := material.Label(th, unit.Sp(12), "Logs will appear here once transcription starts...")
							lbl.Color = colorTextDim
							return lbl.Layout(gtx)
						}
						return material.List(th, &s.logList).Layout(gtx, len(logsCopy), func(gtx layout.Context, i int) layout.Dimensions {
							msg := logsCopy[i]
							lbl := material.Label(th, unit.Sp(11), msg)
							switch {
							case strings.HasPrefix(msg, ">> [Step"):
								lbl.Color = colorCyanAccent
							case strings.Contains(msg, "[OK]") || strings.Contains(msg, "[SUCCESS]"):
								lbl.Color = colorGreen
							case strings.Contains(msg, "[ERROR]") || strings.Contains(msg, "[!]"):
								lbl.Color = colorRed
							default:
								lbl.Color = colorTextMuted
							}
							return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, lbl.Layout)
						})
					})
				})
			}),
		)
	})
}

// ===================== EVENTS & MAIN LAYOUT =====================

func (s *GUIState) handleEvents(gtx layout.Context) {
	// BPM: digits only, optional single fractional digit (e.g. 90 or 90.7).
	if cur := s.bpmEditor.Text(); cur != "" {
		if clean := sanitizeBPMInput(cur); clean != cur {
			s.bpmEditor.SetText(clean)
		}
	}

	if s.btnBrowseInput.Clicked(gtx) {
		go s.browseInputFile()
	}
	if s.btnOpenInputFolder.Clicked(gtx) {
		s.openFolder(filepath.Dir(s.inputEditor.Text()))
	}
	if s.btnBrowseOutput.Clicked(gtx) {
		go s.browseOutputDir()
	}
	if s.btnOpenOutputFolder.Clicked(gtx) {
		s.openFolder(s.outputEditor.Text())
	}
	if s.btnDrumsOnly.Clicked(gtx) {
		s.drumsOnly = !s.drumsOnly
		s.window.Invalidate()
	}
	if s.btnQuantizeToggle.Clicked(gtx) {
		s.quantizeDropdownOpen = !s.quantizeDropdownOpen
		s.window.Invalidate()
	}
	for i := range s.quantizeChoices {
		if s.quantizeChoices[i].Click.Clicked(gtx) {
			s.selectedQuantize = i
			s.quantizeDropdownOpen = false
			s.window.Invalidate()
		}
	}
	if s.btnClearLogs.Clicked(gtx) {
		s.mu.Lock()
		s.logs = nil
		s.mu.Unlock()
		s.window.Invalidate()
	}
	if s.btnStart.Clicked(gtx) {
		go s.runPipeline()
	}
}

func (s *GUIState) layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	paint.Fill(gtx.Ops, colorBgDark)
	return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Spacing: layout.SpaceEnd}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutHeader(gtx, th)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutInputCard(gtx, th)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutOutputCard(gtx, th)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutSettingsCard(gtx, th)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return s.layoutActionCard(gtx, th)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return s.layoutConsole(gtx, th)
			}),
		)
	})
}

func run(w *app.Window) error {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th.Palette.Fg = colorTextLight
	th.Palette.Bg = colorBgDark
	th.Palette.ContrastBg = colorCyanAccent
	th.Palette.ContrastFg = color.NRGBA{R: 2, G: 6, B: 23, A: 255}

	state := newGUIState(w)
	var ops op.Ops

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			// Window is already closing. If pipeline is still running, wait for it
			// so we do not leave an orphan ONNX job; user was warned in the log.
			state.mu.Lock()
			running := state.running
			if running {
				state.logs = append(state.logs,
					"[WARN] Close requested while transcription is in progress.")
				state.logs = append(state.logs,
					"Transcription in progress — wait until finished or kill the process in Task Manager.")
				state.statusMsg = "Transcription in progress — wait or use Task Manager"
			}
			state.mu.Unlock()
			if running {
				for {
					time.Sleep(200 * time.Millisecond)
					state.mu.Lock()
					r := state.running
					state.mu.Unlock()
					if !r {
						break
					}
				}
			}
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			state.handleEvents(gtx)
			state.layout(gtx, th)
			e.Frame(gtx.Ops)
		}
	}
}

func runGUI() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("PATA2FLA - AI Drum Transcription & Clone Hero Generator"))
		w.Option(app.Size(unit.Dp(1100), unit.Dp(790)))
		w.Option(app.MinSize(unit.Dp(880), unit.Dp(640)))
		if err := run(w); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	app.Main()
}