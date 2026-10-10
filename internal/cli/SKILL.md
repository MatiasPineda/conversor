---
name: conversor
description: Convert images, video, audio, PDFs and documents locally with the conversor CLI. Use when an agent needs to convert files on the user's machine without uploading them, list formats or engines, or run a batch from a script.
---

# conversor

Local file conversion. Right-click or run the CLI; files stay on disk. Nothing is uploaded. Routes through engines on this machine (built-in image codecs, PDFium, FFmpeg, LibreOffice, resvg, libheif). Multi-hop routes are at most three steps. A still image cannot become video or audio through a multi-hop route.

Print this file: `conversor --skill` or `conversor skill`.

## Install check

```sh
command -v conversor
conversor --version
conversor engines
```

`conversor engines` lists each ready backend and each unavailable one with its reason. A "no route" error usually means an engine is missing (FFmpeg for video and audio, LibreOffice for documents, heif-enc for HEIC output), not a bad flag.

## Convert

```
conversor <files or folders> --to <fmt> [options]
```

`--to` is required unless `--preset` names a preset that sets `to`. Output lands next to each input unless `--out-dir` is set. With `--out-dir`, files found in subfolders keep their relative folder.

{{FLAGS}}
## Commands

{{COMMANDS}}
## Formats

Ids are what `--to` and `conversor formats` use. Extensions select the input format (no content sniffing). Availability depends on `conversor engines`; Office formats need LibreOffice (`conversor pack install documents`). Raster-to-SVG is not offered.

{{FORMATS}}
## Examples

```sh
conversor photo.png --to webp
conversor photos/ --to webp -r
conversor lease.pdf --to png --pages 1-3
conversor clip.mov --to mp4 --video-height 720
conversor photo.png --to jpeg -q 60 --max-size 64 --background black
conversor clip.mov --to mp4 --no-audio --video-codec hevc
conversor photos/ -r --to webp -j 4 --json --out-dir out/
conversor targets photo.heic
conversor targets photo.heic --menu
conversor formats --json
conversor pack status documents
```

## Exit codes and errors

- `0`: success (including `--skill`, `--help`, and list commands)
- `1`: a conversion failed, or the run was rejected before it started (no `--to`, unknown format, nothing to convert, bad flag value)
- `130`: cancelled (`SIGINT`; a second interrupt exits immediately)

`--json` prints one event per line on stdout: `started`, `progress`, `done`, `failed`, then `summary`. A `failed` event has a stable `kind`: `unsupported_input`, `no_route`, `invalid_option`, `engine_missing`, `engine_failed`, `output_exists`, `cancelled`, `io`.

Folder inputs skip files that cannot reach the target (or already are it). Files named directly always run, so their errors show.

## Tips for agents

- Non-interactive: pass every option on the command line. The CLI does not prompt.
- Prefer `--json` when parsing progress. Human `input -> output` lines go to stderr unless `--json`.
- Outputs never overwrite existing files; a taken name gets a numbered suffix. Use `--out-dir` to keep results apart from the inputs.
- Discover before converting: `conversor engines`, `conversor formats`, `conversor targets path/to/file`.
- `conversor pack install documents` is the only command that may use the network. `pack status` is offline.
- Presets are TOML (`to`, `quality`, `max_size`, …) under the config presets directory, or a path to a `.toml` file. List them with `conversor presets`. Point `CONVERSOR_CONFIG_DIR` at a private directory instead of the user's real presets.
