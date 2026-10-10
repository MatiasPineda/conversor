# The desktop app

`conversor-app` is the window-based side of Conversor. It uses the same engines, presets and output naming as the CLI.

## Windows

- **Activity**: running conversions, with progress, time left and Stop. Below them is everything that finished, kept across restarts: **Show** reveals the result in the file manager, **Retry** runs a failed or cancelled job again with the same options, ⓘ shows the full error, and ✕ forgets a row. **Add files…** converts each file to its kind's default format (Settings › General). Files without a default open Quick convert. Dropping files on the window does the same.
- **Quick convert** opens for files sent without a target: from *More options…* in the right-click menu, *Open with*, or `conversor-app open file…`. It lists what every selected file can become, grouped by kind, with a few words on each. The options depend on the target:
  - **Quality**: Smaller, Balanced (the engine's default) or Best, for JPEG, WebP, AVIF, HEIC and video
  - **Size**: longest edge for images, resolution for video, bitrate for audio
  - **Codec**: H.264 or HEVC, for MP4, MOV and MKV
  - **Keep audio**, for video
  - **Background**: what transparency becomes, for images
  - **Preset**: fills all of the above from a saved preset
  - **Save to**: next to the original, or a folder
  - **File name**, for a single file

  After **Convert** the window follows each file to the end.
- **Automations**: rules that watch one folder each and convert new files as they appear. Files already there when a rule is turned on are left alone, and a file waits until it stops growing (a recording in progress). The defaults are *Screenshots → PNG*, copied to the clipboard, and *Screen recordings → MP4*; a disabled *Downloads HEIC → JPEG* rule is there as an example. In catch-all folders (Desktop, Downloads, home) the screenshot and recording rules only take files named like screenshots or recordings. Screenshot folders: macOS's own setting, `Pictures/Screenshots` on Windows and GNOME, `Pictures` for KDE. Recordings: macOS's setting, `Videos/Captures` or `Videos/Screen Recordings` on Windows, `Videos/Screencasts` on GNOME.
- **Settings**
  - *General*: output folder, conversions at once, notifications, show results in the file manager, keep running in the tray, and the default format per kind (photos → JPEG, other images → PNG, video → MP4, audio → MP3, documents → PDF).
  - *Presets*: create, edit and delete presets. They're the same TOML files the CLI's `-p` reads.
  - *Tools*: install FFmpeg, LibreOffice or libheif with one click (winget or Homebrew), and see which engines are unavailable and why.
  - *Right-click menu*: add or remove *Convert to* in the file manager.
- **Tray icon**: the menu bar on macOS, the notification area on Windows, StatusNotifierItem on Linux. It turns orange while converting. Its menu has *Convert Files…*, the last five results (click to show), *Open*, *Settings…* and *Quit*. With *Keep running in the background* on, closing the windows leaves the app in the tray, so right-click conversions start instantly. With it off, the app quits after the last window closes, once running conversions finish.

The first launch shows a welcome window that offers the right-click menu and the optional tools.

## Language

The app is in Spanish or English, following the computer's language and falling back to Spanish (see [usage.md](usage.md#language)). `CONVERSOR_LANG=en` or `es` overrides it. Dialog buttons use the app's language; Fyne's own file picker, used only where the system has none, follows the system.

## One app per user

The first launch takes a lock in a private runtime folder and listens on a local socket. Later launches hand it their files and exit. File managers start one process per selected file, so a right-click on twenty files still makes one batch with one notification, and *More options…* opens one window for all of them.

```
conversor-app                              open the window
conversor-app open --to jpeg -- a.heic     convert in place, no window
conversor-app open --preset web -- a.png   same, with a preset
conversor-app open -- a.heic b.mov         Quick convert
conversor-app settings | automations
```

When `conversor-app` sits next to `conversor`, `conversor integrate install` (or Settings › Right-click menu) points the menus at the app instead of the CLI, adds *More options…*, and adds a launcher entry: a `.desktop` file on Linux, a Start Menu shortcut on Windows.

## Building

The app needs cgo for Fyne's OpenGL driver. Everything else stays cgo-free, including `internal/gui`, whose tests run on Fyne's headless driver.

| Target | Command | Needs |
|---|---|---|
| This Linux machine | `make app` | `sudo apt-get install libgl1-mesa-dev xorg-dev libxkbcommon-dev` |
| Windows, from Linux/WSL | `make app-windows` | `sudo apt-get install gcc-mingw-w64-x86-64` |
| macOS | `make app-macos` (on a Mac) | Xcode command line tools |

`make dist-app` builds the CLI for every platform, plus the app for this machine and for Windows if mingw is installed. Ship `conversor-app` next to `conversor` (and `conversor.exe`/`conversorw.exe` on Windows), then run Settings › Right-click menu once.

Linux builds use GLFW's X11 backend (`-tags x11`, set by the Makefile), so they need no Wayland headers and run on Wayland desktops through XWayland. On desktops without a tray host (stock GNOME without the AppIndicator extension, WSLg), *Keep running in the background* is disabled and closing the last window quits.

Under WSL2 the Linux build runs through WSLg, which is fine for trying it out. For daily use on Windows, run the Windows build.
