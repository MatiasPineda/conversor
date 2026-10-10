# CLAUDE.md

Conversor: a local file converter written in Go (CLI + Fyne desktop app). AGPL-3.0. Personal project, separate from go-utils.

- Read `docs/architecture.md` first: layout, routing, engines, and the design choices.
- `make test` runs vet and every test; FFmpeg tests skip without ffmpeg on PATH.
- Everything except `cmd/conversor-app` must build with `CGO_ENABLED=0` for linux, windows and darwin (`make dist`); OS-specific code goes in build-tagged files.
- Keep the preset keys, JSON event shape, error kinds and output naming (`doc-2.png`, `photo (1).jpg`) stable; scripts and saved presets depend on them.
- Update `docs/usage.md` in the same change as any user-visible flag, command or format.
- The desktop app is `cmd/conversor-app` (Fyne, needs cgo). Keep window-free logic in `internal/app` and widgets in `internal/gui`; `internal/gui` must stay cgo-free so `CGO_ENABLED=0 go test ./internal/gui` runs headless. Update `docs/app.md` with app changes.
- User-visible text is Spanish and English. Write it in English as a literal inside `i18n.T` or `i18n.N` and add the Spanish to `internal/i18n/es.go` (Latin American, "tú", the standard UI terms: Configuración, Preajuste, menú contextual). `go test ./internal/i18n` fails on a missing, unused or mismatched translation. JSON output, error kinds, format ids, flags and preset keys stay English.
