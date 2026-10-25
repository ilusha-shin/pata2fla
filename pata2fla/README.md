# pata2fla

**RU** · **EN** (see below)

AI-пайплайн транскрипции **ударных** из трека и упаковки в набор для **Clone Hero** (pro-drums).

Аудио → изоляция drums (HT-Demucs ONNX) → ADTOF (онсеты, 5 классов) → decay / CNN specialists + arbiter → NMS (правила рук) → MIDI → `notes.chart` + `song.ini` + `song.wav` / `drums.wav`.

Runtime: **Go + ONNX Runtime**, без Python в бою. CLI и GUI (Gioui, без CGO).

---

## Возможности

- Вход: **WAV / FLAC / MP3** (нативно); полный микс или уже изолированный stem (`--drums-only`)
- Стем: HT-Demucs FT drums (ONNX)
- Онсеты: ADTOF 5-class (BD / SD / HH / TT / CY) (ONNX)
- Уточнение: multi-decay 40/100/200 ms, specialists (hh / cy / tt) (ONNX), cy_arbiter (ONNX), sticky ride, ostinato post-pass
- NMS: не больше 2 «рук», допустимые пары, kick отдельно
- Выход: MIDI (GM), Clone Hero pack (`notes.chart`, `song.ini`, `song.wav`, `drums.wav`, иконка)
- Теги song.ini: artist / title / album / year / genre (в т.ч. кириллица, UTF-8 BOM)
- GUI: BPM 20–350 (одна цифра после точки), quantize, логи

## Быстрый старт

```bat
setup_ui.bat
run.bat --input="path\to\song.flac" --out="path\to\output" --bpm=120 --quantize=32
```

Или GUI: `run_ui.bat` / `pata2fla_ui.exe`.

Подробнее: [README_UI.md](README_UI.md), [README_CHART.md](README_CHART.md).

## CLI (кратко)

| Флаг | Описание |
|------|----------|
| `-i` / `--input` | Входной аудиофайл |
| `-o` / `--out` | Папка вывода |
| `--bpm` | Темп 20–350 (например `90` или `95.3`) |
| `--quantize` | Сетка chart: `16` / `24` (дефолт) / `32` |
| `--drums-only` | Вход уже drum-stem |
| `--no-chart` | Только MIDI + stem, без CH-пакета |
| `--models` | Папка с ONNX + `models_meta.json` |

## Структура (ориентир)

```
Pata2Fla/
├── go.mod / go.sum
├── assets/          # background, logo, IconPata2Fla.png
├── cmd/             # pata2fla_cli, pata2fla_gui with main.go
├── input/           # default input folder
├── models/          # *.onnx, models_meta.json, adtof.onnx
├── internal/        # adtof, audio, chartgen, decay, refine, nms, sections, tempo, ...
├── output/          # default output folder
├── setup.bat / setup_ui.bat / run.bat / run_ui.bat / setup.ps1 / run.ps1
├── setup.sh / setup_ui.sh / run.sh / run_ui.sh
└── README*.md
```

## Оценка моделей

Ниже приведены метрики, рассчитанные на тестовом наборе данных. Для каждого класса указаны Precision (точность), Recall (полнота) и F1-score (F1-мера). Support — количество тестовых примеров для каждого класса.

Классификатор Hi-Hat — `hh_best.onnx`

| Class | Precision | Recall | F1-score | Support |
|-------|-----------|--------|----------|---------|
| Closed hi-hat | 0.850 | 0.849 | 0.849 | 1,205 |
| Open hi-hat | 0.861 | 0.848 | 0.854 | 1,143 |
| Pedal hi-hat | 0.863 | 0.876 | 0.869 | 1,220 |
| **Macro F1** | — | — | **0.857** | — |

Классификатор Cymbal — `cy_best.onnx`

| Class | Precision | Recall | F1-score | Support |
|-------|-----------|--------|----------|---------|
| Crash | 0.946 | 0.978 | 0.962 | 1,208 |
| Ride | 0.978 | 0.947 | 0.962 | 1,254 |
| **Macro F1** | — | — | **0.962** | — |

Классификатор Tom — `tt_best.onnx`

| Class | Precision | Recall | F1-score | Support |
|-------|-----------|--------|----------|---------|
| High tom | 0.894 | 0.934 | 0.914 | 943 |
| Mid tom | 0.899 | 0.886 | 0.892 | 1,207 |
| Low tom | 0.898 | 0.880 | 0.889 | 1,165 |
| **Macro F1** | — | — | **0.898** | — |

Арбитр Cymbal and Hi-Hat — `cy_arbiter.onnx`

| Class | Precision | Recall | F1-score | Support |
|-------|-----------|--------|----------|---------|
| Closed hi-hat | 0.811 | 0.840 | 0.825 | 1,322 |
| Open hi-hat | 0.863 | 0.809 | 0.835 | 1,258 |
| Pedal hi-hat | 0.726 | 0.677 | 0.701 | 1,404 |
| Ride | 0.759 | 0.810 | 0.784 | 1,386 |
| Crash | 0.815 | 0.836 | 0.825 | 1,287 |
| **Macro F1** | — | — | **0.794** | — |

## Ограничения (честно)

- Это **drums-first** инструмент, не полный multi-instrument auto-charter и не замена Moonscraper.
- Ride / crash / open-hat / pedal-hat на грязном stem всё ещё могут путаться — post-edit в редакторе нормален.
- Секции `[Events]` эвристические; тонкую форму песни лучше править руками.

---

# pata2fla (English)

Drum-focused transcription pipeline and **Clone Hero** pro-drums packager.

Audio → drum stem (HT-Demucs ONNX) → ADTOF onsets (ONNX) → decay + specialist CNNs (ONNX) + arbiter (ONNX) → hand-aware NMS → MIDI → `notes.chart` + `song.ini` + wavs.

**Go + ONNX Runtime** at inference time (no Python required to run). CLI and Gioui GUI (no CGO).


## Features

- Input: **WAV / FLAC / MP3** (native support); full mix or an already isolated stem (`--drums-only`)
- Stem separation: HT-Demucs FT drums (ONNX)
- Onset detection: ADTOF 5-class (BD / SD / HH / TT / CY) (ONNX)
- Refinement: multi-decay 40/100/200 ms, specialists (hh / cy / tt) (ONNX), cy_arbiter (ONNX), sticky ride, ostinato post-pass
- NMS: maximum of 2 “hands,” valid note combinations, kick handled separately
- Output: MIDI (GM), Clone Hero pack (`notes.chart`, `song.ini`, `song.wav`, `drums.wav`, icon)
- `song.ini` tags: artist / title / album / year / genre (including Cyrillic, UTF-8 BOM)
- GUI: BPM 20–350 (one decimal place), quantization, logs

## Quick Start

```bat
setup_ui.bat
run.bat --input="path\to\song.flac" --out="path\to\output" --bpm=120 --quantize=32
```

Or use the GUI: `run_ui.bat` / `pata2fla_ui.exe`.

See [README_UI.md](README_UI.md) and [README_CHART.md](README_CHART.md) for details.

## CLI (Brief Overview)

| Flag | Description |
|------|-------------|
| `-i` / `--input` | Input audio file |
| `-o` / `--out` | Output directory |
| `--bpm` | Tempo, 20–350 (e.g., `90` or `95.3`) |
| `--quantize` | Chart grid: `16` / `24` (default) / `32` |
| `--drums-only` | Input is already a drum stem |
| `--no-chart` | Generate MIDI + stem only, without a Clone Hero pack |
| `--models` | Directory containing ONNX models and `models_meta.json` |

## Project Structure (Overview)

```text
Pata2Fla/
├── go.mod / go.sum
├── assets/          # background, logo, IconPata2Fla.png
├── cmd/             # pata2fla_cli, pata2fla_gui with main.go
├── input/           # default input directory
├── models/          # *.onnx, models_meta.json, adtof.onnx
├── internal/        # adtof, audio, chartgen, decay, refine, nms, sections, tempo, ...
├── output/          # default output directory
├── setup.bat / setup_ui.bat / run.bat / run_ui.bat / setup.ps1 / run.ps1
├── setup.sh / setup_ui.sh / run.sh / run_ui.sh
└── README*.md
```

## Model Evaluation

The following metrics were measured on the evaluation dataset. Precision, recall, and F1-score are reported for each class. **Support** indicates the number of evaluated samples per class.

Hi-Hat Classifier — `hh_best.onnx`

| Class | Precision | Recall | F1-score | Support |
|-------|-----------|--------|----------|---------|
| Closed hi-hat | 0.850 | 0.849 | 0.849 | 1,205 |
| Open hi-hat | 0.861 | 0.848 | 0.854 | 1,143 |
| Pedal hi-hat | 0.863 | 0.876 | 0.869 | 1,220 |
| **Macro F1** | — | — | **0.857** | — |

Cymbal Classifier — `cy_best.onnx`

| Class | Precision | Recall | F1-score | Support |
|-------|-----------|--------|----------|---------|
| Crash | 0.946 | 0.978 | 0.962 | 1,208 |
| Ride | 0.978 | 0.947 | 0.962 | 1,254 |
| **Macro F1** | — | — | **0.962** | — |

Tom Classifier — `tt_best.onnx`

| Class | Precision | Recall | F1-score | Support |
|-------|-----------|--------|----------|---------|
| High tom | 0.894 | 0.934 | 0.914 | 943 |
| Mid tom | 0.899 | 0.886 | 0.892 | 1,207 |
| Low tom | 0.898 | 0.880 | 0.889 | 1,165 |
| **Macro F1** | — | — | **0.898** | — |

Cymbal and Hi-Hat Arbiter — `cy_arbiter.onnx`

| Class | Precision | Recall | F1-score | Support |
|-------|-----------|--------|----------|---------|
| Closed hi-hat | 0.811 | 0.840 | 0.825 | 1,322 |
| Open hi-hat | 0.863 | 0.809 | 0.835 | 1,258 |
| Pedal hi-hat | 0.726 | 0.677 | 0.701 | 1,404 |
| Ride | 0.759 | 0.810 | 0.784 | 1,386 |
| Crash | 0.815 | 0.836 | 0.825 | 1,287 |
| **Macro F1** | — | — | **0.794** | — |


## Limitations (Transparency)

- This is a **drums-first** tool, not a full multi-instrument auto-charter or a replacement for Moonscraper.
- Ride / crash / open-hat / pedal-hat may still be confused when working with noisy stems. Manual post-editing in a chart editor is expected.
- `[Events]` sections are generated heuristically; for precise song structure, manual editing is recommended.
