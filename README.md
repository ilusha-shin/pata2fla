<img width="1280" height="320" alt="background2" src="https://github.com/user-attachments/assets/3604f744-73de-4064-9602-1c1f2d2508b4" />


## RU
Десктопный инструмент: выделяет барабаны из трека, распознаёт удары с помощью нейросетей и правил, экспортирует MIDI и пакеты Clone Hero (notes.chart, song.ini, stems).

## Требования

| Компонент | Примечания |
|------|--------|
| **Go** | **Обязателен** для сборки: скрипт `setup_ui` выполняет `go mod tidy` и `go build`. Готовые бинарные файлы **не входят** в репозиторий. |
| **Интернет** | При первой настройке скачиваются ONNX Runtime и модель разделения аудиодорожки на отдельные источники. |
| **ОС** | Windows, Linux или macOS |
| **ОЗУ** | Рекомендуется **около 16 ГБ или больше** для обработки длинных треков. Разделение аудио на отдельные дорожки - самый ресурсоёмкий этап. |
| **Видеокарта** | Не требуется: используется CPU и ONNX Runtime. |

После успешной настройки в каталоге проекта появятся локальные исполняемые файлы и файлы среды выполнения. Они остаются на компьютере (обычно добавлены в `.gitignore`) и не входят в состав репозитория на GitHub.

**Загружаемые и устанавливаемые компоненты для работы приложения**

Устанавливаются через `setup_ui.bat` / `setup_ui.sh` либо вручную помещаются в каталог `models/`.

- **ONNX Runtime** - `onnxruntime.dll` (Windows) / `libonnxruntime.so` (Linux) / `libonnxruntime.dylib` (macOS).
- **`stemsplit_drums.onnx`** - модель выделения ударных в отдельную аудиодорожку.
- **`adtof.onnx`** (+ метаданные, если они поставляются) - модель обнаружения атак звуков пяти классов.
- **Модели уточнения распознавания** - `hh_best.onnx`, `cy_best.onnx`, `tt_best.onnx`, `cy_arbiter.onnx` и `models_meta.json`.

Рядом с приложением создаются каталоги: `input/`, `output/`, `models/`, `assets/` (необязательно - для заголовка, логотипа и иконки).

## Поддерживаемые форматы

| | Форматы |
|--|---------|
| **Входные данные** | **WAV**, **MP3**, **FLAC** (декодирование встроено). Полный микс или отдельная дорожка ударных с флагом `--drums-only`. |
| **Выходные данные** | **MIDI** (ударные на канале 10 General MIDI); пакет для Clone Hero: `notes.chart`, `song.ini`, `song.wav`, `drums.wav`, необязательная иконка. |

> Не поддерживаются в качестве входных форматов, например, OGG и M4A. Сначала перекодируйте такие файлы в WAV, FLAC или MP3.

## Модели и источники

**Разделение ударных на отдельную аудиодорожку**

- `stemsplit_drums.onnx` (HT-Demucs FT drums, ONNX).

**Распознавание ударных**

- ADTOF (Zehren и соавторы): [ADTOF](https://github.com/MZehren/ADTOF).
- Порт на PyTorch, использованный для получения весов `adtof.onnx`: [ADTOF-pytorch](https://github.com/xavriley/ADTOF-pytorch).

## Возможности

- **Входные форматы:** WAV / FLAC / MP3; полный микс или отдельная дорожка ударных (`--drums-only`).
- **Разделение аудио:** HT-Demucs FT drums (ONNX).
- **Обнаружение атак звуков:** ADTOF, 5 классов - BD (бас-барабан), SD (малый барабан), HH (хай-хэт), TT (томы), CY (тарелки); ONNX.
- **Уточнение распознавания:** анализ затухания на нескольких интервалах - 40/100/200 мс; специализированные модели для: хай-хэта (`hh`), тарелок (`cy`) и томов (`tt`), арбитр (`cy_arbiter`); дополнительная обработка партий райда и повторяющихся ритмических рисунков.
- **NMS (подавление немаксимумов):** не более двух ударов руками одновременно, проверка допустимых комбинаций; удары бас-барабана обрабатываются отдельно.
- **Выходные данные:** MIDI в формате General MIDI и пакет Clone Hero (`notes.chart`, `song.ini`, `song.wav`, `drums.wav`, иконка).
- **Поля `song.ini`:** исполнитель, название, альбом, год и жанр. Поддерживается кириллица (UTF-8 с BOM).
- **Графический интерфейс:** темп от 20 до 350 BPM с точностью до десятых; квантование; журнал выполнения в реальном времени, включая прогресс обработки отдельных фрагментов при разделении аудио.

## EN
A desktop tool that separates drums from audio tracks, detects drum hits using neural networks and rule-based methods, and exports MIDI files and Clone Hero packages (`notes.chart`, `song.ini`, stems).

## Requirements

| Item | Notes |
|------|--------|
| **Go** | **Required** to build (`setup_ui` runs `go mod tidy` and `go build`). Prebuilt binaries are **not** shipped in the repo. |
| **Internet** | First setup downloads ONNX Runtime and the stem-separation model. |
| **OS** | Windows, Linux, or macOS |
| **RAM** | ~16 GB+ recommended for long tracks (stem separation is the heavy step). |
| **GPU** | Not required (CPU / ONNX Runtime). |

After a successful setup you get local binaries and runtime files under the project directory; they stay on the machine (typically gitignored) and are not part of the GitHub tree.

**Downloaded / installed for runtime** (via `setup_ui.bat` / `setup_ui.sh` or manual place into `models/`):

- **ONNX Runtime** - `onnxruntime.dll` (Windows) / `libonnxruntime.so` (Linux) / `libonnxruntime.dylib` (macOS)
- **`stemsplit_drums.onnx`** - drum stem separation
- **`adtof.onnx`** (+ meta if shipped) - 5-class onset detection
- **Refine models** - `hh_best.onnx`, `cy_best.onnx`, `tt_best.onnx`, `cy_arbiter.onnx` + `models_meta.json`

Folders created next to the app: `input/`, `output/`, `models/`, `assets/` (optional header/logo/icon).

## Supported formats

| | Formats |
|--|---------|
| **Input** | **WAV**, **MP3**, **FLAC** (native decode). Full mix, or an isolated drum stem with `--drums-only`. |
| **Output** | **MIDI** (GM channel 10 drums); Clone Hero pack: `notes.chart`, `song.ini`, `song.wav`, `drums.wav`, optional icon. |

> Not supported as input: e.g. OGG, M4A (re-encode to WAV/FLAC/MP3 first).

## Models and Sources

**Drum stem separation**

- `stemsplit_drums.onnx` (HT-Demucs FT drums, ONNX)

**Drum transcription**

- ADTOF (Zehren et al.): [ADTOF](https://github.com/MZehren/ADTOF)  
- PyTorch port used for `adtof.onnx` weights: [ADTOF-pytorch](https://github.com/xavriley/ADTOF-pytorch)

## Features

- Input: **WAV / FLAC / MP3**; full mix or stem (`--drums-only`)
- Stem separation: HT-Demucs FT drums (ONNX)
- Onset detection: ADTOF 5-class - BD / SD / HH / TT / CY (ONNX)
- Refinement: multi-decay 40/100/200 ms, `specialists` (hh / cy / tt), `cy_arbiter`, sticky ride, ostinato post-pass
- NMS: max 2 hands, valid combinations; kick handled separately
- Output: GM MIDI + Clone Hero pack (`notes.chart`, `song.ini`, `song.wav`, `drums.wav`, icon)
- `song.ini` tags: artist / title / album / year / genre (Cyrillic OK, UTF-8 BOM)
- GUI: BPM 20–350 (one decimal place), quantize, live logs (including stem chunk progress)

