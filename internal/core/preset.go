package core

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"conversor/internal/i18n"
)

// Preset is a named set of options, optionally tied to a target format.
// Presets are TOML files:
//
//	to = "jpeg"
//	quality = 80
//	max_size = 2048
type Preset struct {
	// To is the target format id. A preset without one works with --to.
	To      string
	Options Options
}

type presetFile struct {
	To string `toml:"to,omitempty"`
	Options
}

// ParsePreset reads a preset from TOML text.
func ParsePreset(text string) (Preset, error) {
	var file presetFile
	dec := toml.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			keys := make([]string, 0, len(strict.Errors))
			for _, e := range strict.Errors {
				keys = append(keys, strings.Join(e.Key(), "."))
			}
			return Preset{}, InvalidOption("%s", i18n.T("unknown field %s", strings.Join(keys, ", ")))
		}
		var de *toml.DecodeError
		if errors.As(err, &de) {
			return Preset{}, InvalidOption("%s", de.Error())
		}
		return Preset{}, InvalidOption("%s", err.Error())
	}
	p := Preset{To: file.To, Options: file.Options}
	if p.To != "" && ByID(p.To) == nil {
		return Preset{}, UnknownFormat(p.To)
	}
	if err := p.Options.Validate(); err != nil {
		return Preset{}, err
	}
	return p, nil
}

// TOML serializes the preset in the form ParsePreset reads.
func (p Preset) TOML() string {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(presetFile{To: p.To, Options: p.Options}); err != nil {
		panic(err) // plain values always encode
	}
	return buf.String()
}

// LoadPreset reads and parses one preset file. Errors name the file.
func LoadPreset(path string) (Preset, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var p Preset
		if p, err = ParsePreset(string(data)); err == nil {
			return p, nil
		}
	}
	why := err.Error()
	var e *Error
	if errors.As(err, &e) && e.Kind == KindInvalidOption {
		why = e.Message
	}
	return Preset{}, InvalidOption("%s", i18n.T("preset %s: %s", path, why))
}

// PresetResult is one file from LoadPresetDir.
type PresetResult struct {
	Name   string
	Preset Preset
	Err    error
}

// LoadPresetDir loads every *.toml file in dir, sorted by name. Each file
// gets its own result, so one broken preset doesn't hide the others. A
// missing directory is empty, not an error.
func LoadPresetDir(dir string) ([]PresetResult, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []PresetResult
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok || e.IsDir() {
			continue
		}
		p, err := LoadPreset(filepath.Join(dir, e.Name()))
		out = append(out, PresetResult{Name: name, Preset: p, Err: err})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
