# Conversor

Convert images, video, audio, PDFs and office documents on your own computer. Pick a file, pick a format, and the result lands next to the original. Nothing is uploaded: every conversion runs on your machine.

Conversor is a desktop app (Windows, macOS, Linux) and a command-line tool. Both speak Spanish and English: they follow your computer's language and use Spanish when it is neither.

- Around 40 formats: photos and images, SVG, PDF, video, audio, Word/Excel/PowerPoint and OpenDocument
- "Convert to" in the right-click menu of Windows Explorer, macOS Finder and the Linux file managers (Nautilus, Dolphin, Nemo, Thunar)
- Quick convert: choose quality, size, codec, background and where to save
- Automations: watch a folder and convert what lands in it (screenshots to PNG on the clipboard, screen recordings to MP4, downloaded HEIC photos to JPEG)
- Presets you can reuse from the app and the command line
- Never overwrites anything: a taken name becomes `photo (1).jpg`, and a failed or cancelled conversion leaves nothing behind
- Batches run in parallel

## Everything runs on your computer

There is no server, account, telemetry or auto-update. Conversions are done by code built into Conversor or by free tools installed on your machine (FFmpeg, LibreOffice), and files never leave the computer.

The only time Conversor touches the network is when you ask it to install one of those optional tools: it then runs `winget` (Windows) or Homebrew (macOS), which download from their usual sources. `conversor pack install documents --source <url>` also downloads, from the URL you give it.

## What's in a build

A build for one platform is a folder with up to three programs:

| File | What it is | Who needs it |
|---|---|---|
| `conversor-app` (`conversor-app.exe` on Windows) | The desktop app: windows, tray icon, Quick convert, automations | Anyone who wants a window to work in |
| `conversor` (`conversor.exe`) | The command-line tool, for a terminal or scripts | Optional for app users; the app shares its presets and settings |
| `conversorw.exe` (Windows only) | The same command-line tool built without a console window | Only used by the right-click menu when the app isn't installed, so no black window flashes |

None of them needs installing in the usual sense: put the folder anywhere and run it. Keep the files together, since the right-click menu looks for its siblings.

Builds come in two processor flavors: **amd64** (Intel and AMD, most PCs and older Macs) and **arm64** (Apple Silicon Macs, Windows on ARM laptops such as Snapdragon ones, Raspberry Pi). Pick the one that matches the computer.

## Install

There are no published downloads yet. Building takes one command with [Go 1.26 or newer](https://go.dev/dl/) (see [Build from source](#build-from-source)), and the result can be copied to other computers.

### Windows (10 or 11)

**App:** copy the `windows-amd64` folder (with `conversor-app.exe`, `conversor.exe` and `conversorw.exe`) somewhere permanent, such as `C:\Users\<you>\Apps\Conversor`, and run `conversor-app.exe`. The welcome window offers to add the right-click menu and the optional tools. Windows SmartScreen may warn that the app is unrecognized, since it isn't signed: choose *More info › Run anyway*.

**Command line only:** copy `conversor.exe` and `conversorw.exe` to a folder on your `PATH`, then run `conversor integrate install` once if you want the right-click menu.

The right-click menu goes under *Show more options* on Windows 11. It is written to your user account only, so no administrator rights are needed.

### macOS (12 or newer)

**App:** build it on a Mac with `make app-macos`, which produces `Conversor.app` with the command-line tool inside. Drag it to `Applications`. It isn't signed, so the first time, right-click it and choose *Open*.

**Command line only:** copy `conversor` from `darwin-arm64` (Apple Silicon) or `darwin-amd64` (Intel) to a folder on your `PATH`, such as `/usr/local/bin`. If it was downloaded, clear the quarantine flag first: `xattr -d com.apple.quarantine conversor`.

The right-click entries appear under *Quick Actions* in Finder.

### Linux

**Command line:** copy `conversor` from `linux-amd64` or `linux-arm64` to `~/.local/bin`. It is a single static file with no dependencies.

**App:** build it with `make install-app`, which needs the OpenGL and X11 development headers first:

```bash
sudo apt-get install gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev   # Debian/Ubuntu
make install-app    # conversor and conversor-app into ~/.local/bin
```

It runs on X11 and on Wayland (through XWayland). The tray icon needs a desktop that shows StatusNotifier icons: KDE, Cinnamon, XFCE, or GNOME with the AppIndicator extension. Run `conversor integrate install` (or Settings › Right-click menu) and restart the file manager; Nautilus also needs `python3-nautilus`.

### Optional tools

Images, PDFs and SVG work out of the box. Three kinds of conversion need a free tool, which Conversor can install for you from Settings › Tools or the command line:

| Tool | Adds | Command |
|---|---|---|
| FFmpeg | Video and audio; also makes AVIF output about 20× faster | `conversor pack install media` |
| LibreOffice | Word, Excel, PowerPoint, OpenDocument, RTF, TXT, HTML, CSV | `conversor pack install documents` |
| libheif (`heif-enc`) | Saving as HEIC (opening HEIC needs nothing; macOS needs nothing either) | `conversor pack install heic` |

Windows uses winget and macOS uses Homebrew. On Linux it prints the `apt`/`dnf`/`pacman`/`zypper` command, since installing needs root.

## Using it

**In the app:** drop files on the window (photos become JPEG, other images PNG, video MP4, audio MP3 and documents PDF; change those in Settings), or right-click files and choose *Convert to*. *More options…* opens Quick convert, where you pick the format, quality, size and destination.

**On the command line:**

```bash
conversor clip.mov --to mp4
conversor photos/ --to webp -r                       # a whole folder, with subfolders
conversor contract.pdf --to png --pages 1-3          # one image per page
conversor photo.heic --to jpeg -q 80 --max-size 2048
conversor clip.mov --to mp4 --video-height 720 --video-codec hevc --no-audio
conversor targets photo.heic                         # what this file can become
conversor --help
```

## What converts to what

| From | To | Needs |
|---|---|---|
| JPEG, PNG, WebP, HEIC, AVIF, GIF, TIFF, BMP, ICO, TGA, PPM/PGM/PBM, QOI, OpenEXR | Any of those | nothing (HEIC output: libheif, or a Mac) |
| SVG | PNG and any image above | nothing (install `resvg` for text in SVGs) |
| PDF | PNG, JPEG and any image above, one file per page | nothing |
| MP4, MOV, WebM, MKV, AVI, GIF | Each other, a still frame as an image, or audio | FFmpeg |
| MP3, WAV, FLAC, AAC, M4A, OGG, Opus | Each other | FFmpeg |
| DOCX, DOC, ODT, RTF, TXT, HTML | Each other, PDF | LibreOffice |
| PPTX, PPT, ODP | Each other, PDF | LibreOffice |
| XLSX, XLS, ODS, CSV | Each other, PDF | LibreOffice |

When no single tool can do a conversion, Conversor chains up to three (SVG → PNG → JPEG, PDF → PNG → WebP), preferring lossless steps in between. Photos are rotated upright from their EXIF orientation, and transparency is kept wherever the target format can store it (JPEG gets a white background unless you pick another color).

## How much computer it needs

Any 64-bit computer from the last decade runs it. Rough figures, measured on a 4-core laptop with a 12-megapixel photo and a 10-page PDF:

| | |
|---|---|
| Disk | About 27 MB for the command-line tool and 42 MB for the app, plus a 21 MB cache the PDF engine builds on first use. FFmpeg adds roughly 100–200 MB and LibreOffice roughly 1 GB, if you install them. |
| Memory | JPEG output about 130 MB; WebP about 400 MB; a PDF about 200 MB; the app sitting idle about 200 MB. AVIF output without FFmpeg is the outlier at about 2.3 GB. A batch converts one file per processor core at once, so a big batch can use several times these figures. |
| Time | JPEG 0.6 s, WebP 2 s, a 10-page PDF under 1 s, AVIF 13 s without FFmpeg. Video depends on its length and on FFmpeg, and runs one file at a time. |

## Limitations

Things it doesn't do:

- **No editing**: it can't crop, rotate, trim video, merge files or join images into one PDF. One file in, one file out (or one per page).
- **No PDF to Word, and no OCR**: PDFs become images only. Text from scans isn't recognized.
- **No image to video or audio**, and no images to SVG (tracing).
- **Animated GIF and WebP become still images** when converted to an image format (the first frame). Animated GIF to MP4/WebM works, with FFmpeg.
- **No password-protected or DRM-protected files.**
- **No mobile version**, and no web version.

Caveats:

- Office documents convert as faithfully as LibreOffice does. Fonts that aren't installed get substituted, so complex layouts can shift.
- The built-in SVG renderer has no text support; install `resvg` (or `rsvg-convert`) for SVGs with text.
- OpenEXR files with PIZ, PXR24, B44 or DWA compression need FFmpeg.
- The builds aren't signed, so Windows SmartScreen and macOS Gatekeeper warn the first time.
- The Linux and Windows apps can be built from Linux; the macOS app must be built on a Mac.
- Right-click menu labels use the language active when the menu was installed; install it again after changing the computer's language.
- Spanish and English only. Set `CONVERSOR_LANG=es` or `en` to override the computer's language.

## Build from source

Requires Go 1.26 or newer.

```bash
make test            # vet and every test
make install         # the CLI into ~/.local/bin
make dist            # the CLI for every platform, into dist/<os>-<arch>/
make app             # the desktop app for this machine (needs cgo and OpenGL headers)
make app-windows     # the Windows app, from Linux (needs gcc-mingw-w64-x86-64)
make app-macos       # Conversor.app (on a Mac)
```

`make dist` and `make app-windows` together produce the folders described in [What's in a build](#whats-in-a-build).

## More

- [docs/usage.md](docs/usage.md): every command-line option, presets, JSON output, language and right-click menus
- [docs/app.md](docs/app.md): the desktop app in detail
- [docs/architecture.md](docs/architecture.md): how it works inside

## License

AGPL-3.0; see [LICENSE](LICENSE). If you give someone a copy of the programs, give them the source too, or tell them where to get it.
