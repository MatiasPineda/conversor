# Conversor

Local file conversion for your own machine. Point it at a file, pick a format, and the result lands next to the original. Images, video, audio, PDFs and documents stay on disk: nothing is uploaded.

Written in Go. It ships as one self-contained binary for Windows, macOS and Linux, and has no license checks, accounts or cloud.

- 40 formats: images, SVG, video, audio, PDF, Word/Excel/PowerPoint/OpenDocument
- Multi-hop routes of up to three steps when no engine converts directly (SVG → PNG → JPEG, PDF → PNG → WebP)
- Never overwrites anything: a taken name becomes `photo (1).jpg`, and a failed or cancelled job leaves nothing behind
- Batches run in parallel, with only one video at a time
- TOML presets
- "Convert to" in the right-click menu of Explorer, Finder, Nautilus, Dolphin, Nemo and Thunar
- A desktop app: activity and history, Quick convert, a preset editor, folder-watching automations (screenshots → PNG on the clipboard) and a tray icon

## Install

Build it yourself (Go 1.26 or newer):

```bash
make install            # builds and copies to ~/.local/bin/conversor
make dist               # every platform, into dist/<os>-<arch>/
```

To hand a friend a copy, give them their platform's folder from `dist/`. On Windows that's both `conversor.exe` and `conversorw.exe`; the second is the console-less build the Explorer menu runs.

Images, PDFs and SVG work out of the box. Three kinds of conversion need a free tool that Conversor can install for you:

```bash
conversor pack status
conversor pack install media       # FFmpeg: video and audio
conversor pack install documents   # LibreOffice: Word, Excel, PowerPoint
conversor pack install heic        # libheif: HEIC *output* (HEIC input needs nothing)
```

Windows uses winget and macOS uses Homebrew. On Linux it prints the `apt`/`dnf`/`pacman` command, since installing needs root.

## Usage

```bash
conversor clip.mov --to mp4
conversor photos/ --to webp -r
conversor lease.pdf --to png --pages 1-3
conversor photo.heic --to jpeg -q 80 --max-size 2048
conversor clip.mov --to mp4 --video-height 720 --video-codec hevc --no-audio
```

Add the right-click menu:

```bash
conversor integrate install     # every file manager it finds
conversor integrate uninstall
```

## Desktop app

```bash
make app            # needs cgo + OpenGL headers; see docs/app.md
./conversor-app
```

The app adds a window, Quick convert, a preset editor, automations and a tray icon. See [docs/app.md](docs/app.md) for what it does and how to build it for each OS.

See [docs/usage.md](docs/usage.md) for every option, presets, JSON output and the conversion table, and [docs/architecture.md](docs/architecture.md) for how it works and the design choices behind it.

## License

AGPL-3.0; see [LICENSE](LICENSE). If you pass binaries on, pass the source along too, or say where to get it.
