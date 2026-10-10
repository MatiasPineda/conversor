# Using conversor

```
conversor <files or folders> --to <format> [options]
```

Output lands next to each input, named after it with the new extension. Paged output (a PDF to PNG) becomes `doc.png`, `doc-2.png`, `doc-3.png`. If a name is taken, the result gets a suffix (`photo (1).jpg`). Nothing is ever replaced, including the input. While a job runs, its files live in a hidden `.conversor-*` folder beside the output and appear only when the job finishes. A failed or cancelled job leaves nothing behind.

## Options

| Flag | What it does |
|---|---|
| `-t`, `--to <format>` | Target format id or extension (`jpeg`, `jpg`, `mp4`, …). Required unless the preset sets one. |
| `-o`, `--out-dir <dir>` | Write here instead of next to each input. Files found in subfolders keep their relative folder. |
| `-p`, `--preset <name or path>` | Load options from a preset (see below). Flags given on the command line win. |
| `-q`, `--quality <1-100>` | Lossy quality for JPEG (default 90), WebP (90), AVIF (80), HEIC (90) and video. |
| `--max-size <px>` | Longest edge of image output. Only scales down. |
| `--pages <range>` | PDF pages: `2`, `1-3` or `4-`. |
| `--dpi <18-1200>` | Resolution for PDF and SVG rendering. PDFs default to 2000 px wide (at most 4000 tall); SVG to its own size. |
| `--video-height <px>` | Maximum video height. Only scales down, keeps the aspect ratio and even dimensions. |
| `--audio-bitrate <8-640>` | Audio bitrate in kbit/s. |
| `--video-codec <h264\|hevc>` | Encoder for MP4, MOV and MKV. H.264 plays everywhere; HEVC makes smaller files. |
| `--no-audio` | Leave the audio out of video output. |
| `--background <color>` | What transparency becomes: `transparent`, `white`, `black` or `#rrggbb`. By default formats that can store transparency keep it and JPEG/PPM get white. |
| `-j`, `--jobs <n>` | Files converted at once (default: one per CPU core). Video always runs one at a time. |
| `-r`, `--recursive` | Include subfolders of folder inputs. |
| `--json` | One JSON event per line on stdout (see below). |

In folders, files that can't become the target, or already are it, are skipped quietly. Files named directly always run, so their errors show.

## Commands

```bash
conversor formats [--json]           # every format id, name and category
conversor targets photo.heic         # what a file can become here
conversor targets photo.heic --menu  # the few the right-click menu offers
conversor engines                    # which engines run here, and why others don't
conversor presets                    # saved presets
conversor pack status|install|remove # optional tools (see below)
conversor integrate install|uninstall|status
conversor skill                      # a SKILL.md describing the CLI for AI agents (also --skill)
```

## What converts to what

| From | To | Engine |
|---|---|---|
| JPEG, PNG, WebP, HEIC, AVIF, GIF, TIFF, BMP, ICO, TGA, PPM/PGM/PBM, QOI, OpenEXR | Any of those except HEIC | built in |
| any image above | HEIC | `heif-enc` (`pack install heic`), or `sips` on macOS |
| SVG | PNG, then any image | resvg or rsvg-convert if installed, otherwise built in (no text) |
| PDF | PNG, JPEG, then any image (one file per page) | PDFium, built in |
| MP4, MOV, WebM, MKV, AVI, GIF | each other; a still frame as an image; audio (not from GIF) | FFmpeg (`pack install media`) |
| MP3, WAV, FLAC, AAC, M4A, OGG, Opus | each other | FFmpeg |
| DOCX, DOC, ODT, RTF, TXT, HTML | each other, PDF | LibreOffice (`pack install documents`) |
| PPTX, PPT, ODP | each other, PDF | LibreOffice |
| XLSX, XLS, ODS, CSV | each other, PDF | LibreOffice |

When no engine converts directly, Conversor chains up to three, preferring lossless in-between formats (PNG, TIFF, WAV, FLAC, MKV, PDF, ODT/ODS/ODP). A still image never becomes video or audio through a chain. Images keep their transparency where the target can store it. EXIF rotation is applied, so photos come out upright even in formats that drop the tag.

## Presets

A preset is a TOML file in the presets folder: `~/.config/conversor/presets/` on Linux, `~/Library/Application Support/conversor/presets/` on macOS, `%APPDATA%\conversor\presets\` on Windows. Set `CONVERSOR_CONFIG_DIR` to use another folder.

```toml
# web.toml: conversor photos/ -p web
to = "webp"
quality = 80
max_size = 2048
background = "white"
```

The keys are `to`, `quality`, `max_size`, `video_height`, `audio_bitrate`, `pages`, `dpi`, `video_codec`, `background` and `strip_audio`. Unknown keys and out-of-range values are errors that name the file.

## JSON output

With `--json`, stdout gets one object per line:

```json
{"event":"started","index":0,"input":"a.png"}
{"event":"progress","index":0,"fraction":0.5}
{"event":"done","index":0,"input":"a.png","outputs":["a.webp"]}
{"event":"failed","index":1,"input":"b.txt","kind":"no_route","message":"no way to convert txt to webp"}
{"event":"summary","done":1,"failed":1,"cancelled":0}
```

`fraction` is `null` when the engine can't tell. `kind` is one of `unsupported_input`, `no_route`, `invalid_option`, `engine_missing`, `engine_failed`, `output_exists`, `cancelled` or `io`.

Exit codes: `0` success, `1` a conversion failed or the request was invalid, `130` cancelled with Ctrl-C (a second Ctrl-C exits at once).

## Optional tools (packs)

| Pack | Tool | Needed for |
|---|---|---|
| `media` | FFmpeg | video, audio, frames from video; also speeds up AVIF output |
| `documents` | LibreOffice | Word, Excel, PowerPoint, OpenDocument, RTF, TXT, HTML, CSV |
| `heic` | heif-enc (libheif) | HEIC output (not needed on macOS) |

`conversor pack install <pack>` runs winget on Windows and Homebrew on macOS. On Linux it prints the package manager command, since that needs root. `pack status` never touches the network. For a portable LibreOffice, `pack install documents --source <url or file> --sha256 <hash>` downloads a `.zip` or `.tar.gz`, checks the hash, and unpacks it into Conversor's data folder. `pack remove documents` deletes it again.

Tools are found through `CONVERSOR_FFMPEG`, `CONVERSOR_FFPROBE`, `CONVERSOR_SOFFICE`, `CONVERSOR_HEIF_ENC`, `CONVERSOR_RESVG` and `CONVERSOR_RSVG`, then next to the conversor binary, then PATH. After that it checks where winget, Scoop, Chocolatey and Homebrew put tools, which matters when a file manager launches Conversor with a short PATH.

## Language

Messages, help and menus are in Spanish or English: the computer's language when it is one of those, Spanish otherwise. Linux and macOS terminals go by `LANGUAGE`, `LC_ALL`, `LC_MESSAGES` and `LANG`; apps on macOS by System Settings › General › Language & Region; Windows by its display language. `CONVERSOR_LANG=en` or `es` overrides it. `--json` output and `conversor skill` are always English, and format ids, flags and preset keys never change.

Right-click menu labels are written in the language active when `integrate install` runs; install again after switching.

## Right-click menus

`conversor integrate install` adds a **Convert to** menu offering each file type's popular targets: JPEG, PNG and WebP for photos; MP4, MOV, GIF and MP3 for video; PDF for documents. Each entry converts in place and shows a desktop notification when it's done.

- **Windows**: Explorer, under HKEY_CURRENT_USER (no admin needed). On Windows 11 it sits under *Show more options*. Keep `conversorw.exe` next to `conversor.exe` so no console window flashes.
- **Linux**: Nautilus (needs `python3-nautilus`), Dolphin, Nemo and Thunar. Name some (`conversor integrate install dolphin`) to choose. Restart the file manager afterwards.
- **macOS**: Finder Quick Actions, one per target, under *Quick Actions* in the right-click menu.

Rerun `install` after moving or updating the binary. `uninstall` removes only what `install` wrote; your own Thunar actions and registry entries stay.
