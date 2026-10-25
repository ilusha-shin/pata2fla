# Clone Hero export · Экспорт в Clone Hero

**RU** · **EN**

## Русский

После транскрипции (если не указан `--no-chart`) в папке трека появляется набор:

| Файл | Назначение |
|------|------------|
| `notes.chart` | Pro-drums 4-lane, ExpertDrums |
| `song.ini` | Метаданные, `pro_drums = True` |
| `song.wav` | Полный микс |
| `drums.wav` | Изолированный drum stem |
| `IconPata2Fla.png` | Иконка (если есть в `assets/`) |
| `midi/*.mid` | GM MIDI ударных |

### song.ini

- `name`, `artist`, `album`, `genre`, `year` — из тегов файла (ID3 / Vorbis / WAV), иначе имя из файла
- Кириллица: файл в **UTF-8 с BOM**
- `charter` — бейдж Pata2Fla (цветная разметка CH)
- `icon = IconPata2Fla` — рядом должен лежать `IconPata2Fla.png`
- `pro_drums = True`, `five_lane_drums = False`
- `diff_drums` / `diff_drums_real` = 3 (ориентир)

### Chart

- **Offset = 0** — ноты совпадают с аудио (без искусственных −2 s)
- Квантизация по умолчанию **1/24** (удобно для ровных 16-х и триолей); CLI/GUI: 16 / 24 / 32
- BPM задаётся пользователем (`--bpm` / поле GUI); от него строится сетка и секции
- `[Events]` — эвристические маркеры формы (Intro / Part_A / …); это подсказки, не идеальная разметка песни

### Рекомендуемый пост-процесс

1. Открыть папку трека в **Moonscraper** или **OCTAVE**
2. Проверить сетку и редкие ошибки ride / crash / hat
3. Поправить секции и сложности при необходимости
4. При необходимости открыть MIDI-файл в MuseScore и вручную исправить ноты
5. Играть в Clone Hero / YARG

### Иконка

Положите `assets/IconPata2Fla.png` рядом с приложением. При экспорте файл копируется в папку трека; в `song.ini` пишется `icon = IconPata2Fla`.

---

## English

After transcription, unless `--no-chart` is specified, the following files are generated in the track's output folder:

| File | Description |
|------|-------------|
| `notes.chart` | 4-lane Pro Drums chart, ExpertDrums |
| `song.ini` | Metadata, `pro_drums = True` |
| `song.wav` | Full mix |
| `drums.wav` | Isolated drum stem |
| `IconPata2Fla.png` | Icon (if available in `assets/`) |
| `midi/*.mid` | GM drum MIDI files |

### song.ini

- `name`, `artist`, `album`, `genre`, `year` — read from the audio file's tags (ID3 / Vorbis / WAV); falls back to the filename if unavailable
- Cyrillic support: the file is saved as **UTF-8 with BOM**
- `charter` — Pata2Fla badge (colored Clone Hero chart display)
- `icon = IconPata2Fla` — requires `IconPata2Fla.png` in the same folder
- `pro_drums = True`, `five_lane_drums = False`
- `diff_drums` / `diff_drums_real` = 3 (reference value)

### Chart

- **Offset = 0** — notes align with the audio (no artificial −2 s offset)
- Default quantization: **1/24** (suitable for straight 16th notes and triplets); CLI/GUI options: 16 / 24 / 32
- BPM is user-defined (`--bpm` / GUI field); it determines the note grid and section placement
- `[Events]` — heuristic song-structure markers (Intro / Part_A / …); these are suggestions, not a perfect representation of the song structure

### Recommended Post-Processing

1. Open the track's output folder in **Moonscraper** or **OCTAVE**.
2. Check the note grid and correct occasional ride / crash / hi-hat errors.
3. Adjust sections and difficulty settings as needed.
4. If needed, open the MIDI file in MuseScore and manually correct the notes.
5. Play in Clone Hero / YARG.

### Icon

Place `assets/IconPata2Fla.png` alongside the application. During export, the file is copied to the track's output folder, and `song.ini` is set to `icon = IconPata2Fla`.