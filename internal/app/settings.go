// Package app is the desktop app's state without its windows: settings,
// history, the job queue, automation rules, requests from other launches,
// and the Quick convert rules. The Fyne UI in internal/gui draws it.
package app

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/pelletier/go-toml/v2"

	"conversor/internal/core"
	"conversor/internal/engines"
)

// Defaults are what files dropped on the app or added with "Add files"
// convert to, by kind.
type Defaults struct {
	Photos    string `toml:"photos"`
	Images    string `toml:"images"`
	Video     string `toml:"video"`
	Audio     string `toml:"audio"`
	Documents string `toml:"documents"`
}

// Settings are the app's preferences, stored as settings.toml in the config
// directory. Unknown keys are ignored, so an older app can read a newer
// file.
type Settings struct {
	// OutputDir is where converted files go; empty means next to each input.
	OutputDir string `toml:"output_dir,omitempty"`
	// Concurrency is how many conversions run at once; 0 means one per core.
	Concurrency int `toml:"concurrency,omitempty"`
	// Notifications shows a system notification when a batch finishes.
	Notifications bool `toml:"notifications"`
	// RevealWhenDone shows each finished file in the file manager.
	RevealWhenDone bool `toml:"reveal_when_done"`
	// KeepRunning shows the tray icon and keeps the app running after the
	// last window closes.
	KeepRunning bool `toml:"keep_running"`
	// FirstRunDone is set once the welcome window was finished.
	FirstRunDone bool         `toml:"first_run_done"`
	Defaults     Defaults     `toml:"defaults"`
	Automations  []Automation `toml:"automations"`
}

// DefaultSettings are what a fresh install starts with.
func DefaultSettings() Settings {
	return Settings{
		Notifications: true,
		KeepRunning:   true,
		Defaults:      Defaults{Photos: "jpeg", Images: "png", Video: "mp4", Audio: "mp3", Documents: "pdf"},
		Automations:   ExampleAutomations(),
	}
}

// SettingsPath is where settings live.
func SettingsPath() string {
	if d := engines.ConfigDir(); d != "" {
		return filepath.Join(d, "settings.toml")
	}
	return ""
}

// LoadSettings reads settings, filling anything missing with defaults. A
// missing file is a fresh install.
func LoadSettings(path string) (Settings, error) {
	s := DefaultSettings()
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	// Decode over the defaults, so keys the file lacks keep them; a file
	// that lists no automations keeps an empty list, though.
	s.Automations = nil
	if err := toml.NewDecoder(bytes.NewReader(data)).Decode(&s); err != nil {
		return DefaultSettings(), err
	}
	d := DefaultSettings().Defaults
	for _, f := range []struct {
		v   *string
		def string
	}{
		{&s.Defaults.Photos, d.Photos}, {&s.Defaults.Images, d.Images}, {&s.Defaults.Video, d.Video},
		{&s.Defaults.Audio, d.Audio}, {&s.Defaults.Documents, d.Documents},
	} {
		if core.ByID(*f.v) == nil {
			*f.v = f.def
		}
	}
	return s, nil
}

// Save writes settings atomically.
func (s Settings) Save(path string) error {
	if path == "" {
		return nil
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(s); err != nil {
		return err
	}
	return writeAtomic(path, buf.Bytes())
}

// Workers is how many conversions run at once.
func (s Settings) Workers() int {
	if s.Concurrency > 0 {
		return s.Concurrency
	}
	return runtime.NumCPU()
}

// Output is where conversions go by default.
func (s Settings) Output() core.Output {
	if s.OutputDir != "" {
		return core.Output{Kind: core.InDir, Path: s.OutputDir}
	}
	return core.Output{}
}

// Kind groups formats for the per-kind default targets.
type Kind int

const (
	KindNone Kind = iota
	KindPhotos
	KindImages
	KindVideo
	KindAudio
	KindDocuments
)

// IsPhotoFormat reports camera and web photo formats, which default to JPEG.
func IsPhotoFormat(f *core.Format) bool {
	switch f.ID {
	case "jpeg", "heic", "avif", "webp":
		return true
	}
	return false
}

// KindOf is the default-target kind for a file. A screenshot-named photo
// (HEIC from a phone screenshot) counts as an image, so it becomes PNG.
func KindOf(path string, f *core.Format) Kind {
	switch f.Category {
	case core.Image, core.Vector:
		if IsPhotoFormat(f) && !LooksLikeScreenshot(path) {
			return KindPhotos
		}
		return KindImages
	case core.Video:
		return KindVideo
	case core.Audio:
		return KindAudio
	case core.Document, core.Presentation, core.Spreadsheet:
		return KindDocuments
	}
	return KindNone
}

// Get is the default target id for a kind.
func (d Defaults) Get(k Kind) string {
	switch k {
	case KindPhotos:
		return d.Photos
	case KindImages:
		return d.Images
	case KindVideo:
		return d.Video
	case KindAudio:
		return d.Audio
	case KindDocuments:
		return d.Documents
	}
	return ""
}

// writeAtomic writes data to a temporary file beside path and renames it
// into place, so a crash never leaves a half-written file.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
