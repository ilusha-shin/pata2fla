# pata2fla GUI Интерфейс

**RU** · **EN**

## Русский

Десктопный UI на **Gioui** (Pure Go, без CGO / GCC(Windows)): транскрипция ударных и сборка папки Clone Hero.

### Запуск

1. `setup_ui.bat` (Windows) или `setup_ui.sh` (Linux / macOS) — модули Go, onnxruntime, проверка models / assets  
2. `run_ui.bat` / `run_ui.sh` или `pata2fla_ui.exe`  
3. Указать входной файл, BPM, quantize → **Старт**

CLI без GUI:

```bat
pata2fla.exe -i "track.flac" -o output --bpm 120 --quantize 24
```

### Окно

- Размер по умолчанию **1100×790**, минимум **880×640**
- Шапка: фон + лого (`assets/`), высота хедера ~160–220 dp, лого до ~90% высоты хедера
- Поля: input / output, BPM, quantize
- Лог процесса внизу

### BPM

- Диапазон **20–350**
- Целое или **одна** цифра после точки: `90`, `95.3`
- Нельзя: отрицательные, `90.75`, вторая точка
- Пустое в GUI → подставляется разумный дефолт (120 в CLI-парсере)

### Аудио

- Форматы в фильтре: **WAV, FLAC, MP3**
- Пустой файл / длительность менее 0.5 с / более 20 мин → ошибка, пайплайн не запускается
- Нет онсетов ADTOF → ошибка (пустой chart не пишется)

### Поведение UI

- Пока идёт транскрипция, повторный старт блокируется
- Закрытие окна во время работы: предупреждение; процесс лучше дождаться или снять в диспетчере задач
- После успеха — папка вывода с chart / ini / wav / midi / icon

### Ассеты

```
assets/
  background.png   				   # опционально, шапка
  logo.png                         # опционально
  logo1.png                        # опционально
  IconPata2Fla.png                 # иконка для song.ini (charter icon)
```

---

## English

Desktop UI built with **Gioui** (pure Go, no CGO(Windows)): run the drum pipeline and produce a Clone Hero song folder.

### Run

1. `setup_ui.bat` / `setup_ui.sh` — Go modules, onnxruntime, models / assets check  
2. `run_ui.bat` / `pata2fla_gui.exe`  
3. Pick audio, set BPM and quantize → Start  

CLI: `pata2fla.exe -i track.flac -o output --bpm 120 --quantize 24`

### Window

- Default size 1100×790, minimum 880×640  
- Header art / logo from `assets/`  
- Log console at the bottom  

### BPM

- Range 20–350; optional single decimal (`95.3`)  
- Rejects negatives and values with more than one decimal digit  

### Audio guards

- WAV / FLAC / MP3 in the file filter  
- Rejects empty files and durations under 0.5 s or over 20 min  
- Fails if ADTOF finds no hits  

### Assets

```
assets/
  background.png   				   # optional, header
  logo.png                         # optional
  logo1.png                        # optional
  IconPata2Fla.png                 # icon for song.ini (charter icon)
```
