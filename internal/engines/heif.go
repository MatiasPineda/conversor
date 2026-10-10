package engines

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

// HEIFEngine writes HEIC with an installed encoder: macOS's own codecs
// through sips, or heif-enc from libheif elsewhere. HEVC encoding has no
// pure-Go implementation, so HEIC output needs one of them; HEIC input
// needs nothing (the image engine decodes it).
type HEIFEngine struct {
	tool string
	sips bool
}

func NewHEIFEngine() *HEIFEngine {
	if runtime.GOOS == "darwin" {
		if p := findTool([]string{"sips"}, "CONVERSOR_SIPS", "/usr/bin/sips"); p != "" {
			return &HEIFEngine{tool: p, sips: true}
		}
	}
	return &HEIFEngine{tool: findTool([]string{"heif-enc"}, "CONVERSOR_HEIF_ENC")}
}

func (e *HEIFEngine) ID() string {
	if e.sips {
		return "imageio"
	}
	return "libheif"
}

func (*HEIFEngine) Priority() int { return 10 }

func (e *HEIFEngine) Unavailable() string {
	if e.tool == "" {
		return i18n.T("heif-enc not found; run `conversor pack install heic` for HEIC output")
	}
	return ""
}

func (*HEIFEngine) Steps() []core.Step {
	var inputs []string
	for _, id := range imageInputs {
		if id != "heic" {
			inputs = append(inputs, id)
		}
	}
	return core.Steps(inputs, []string{"heic"})
}

func (e *HEIFEngine) Convert(t *core.Task, input, outDir string) ([]string, error) {
	// Decode here so the encoder gets upright, sized pixels with the chosen
	// background, whatever the input format.
	img, err := openImage(t, input)
	if err != nil {
		return nil, err
	}
	prepared := filepath.Join(outDir, "prepared.png")
	if err := encodeImage(fit(img, t.Options.MaxSize), "png", t.Options, prepared, nil); err != nil {
		return nil, err
	}
	t.Indeterminate()
	out := t.Artifact(outDir, 0)
	q := strconv.Itoa(core.Get(t.Options.Quality, 90))
	var cmd *exec.Cmd
	if e.sips {
		cmd = exec.Command(e.tool, "-s", "format", "heic", "-s", "formatOptions", q, prepared, "--out", out)
	} else {
		cmd = exec.Command(e.tool, "-q", q, "-o", out, prepared)
	}
	if err := runTool(t, e.ID(), cmd, nil); err != nil {
		return nil, err
	}
	return []string{out}, nil
}
