package engines

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"strconv"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"

	"conversor/internal/codec"
	"conversor/internal/core"
	"conversor/internal/i18n"
)

// SVGEngine renders SVG to PNG. It uses resvg or rsvg-convert when one is
// installed, since they handle all of SVG including text, and falls back to
// the built-in renderer, which draws shapes and paths but no text. Other
// raster targets go through the image engine as a second hop.
type SVGEngine struct {
	tool    string // path to resvg or rsvg-convert, or "" for built-in
	backend string // "resvg", "rsvg" or "built-in"
}

func NewSVGEngine() *SVGEngine {
	if p := findTool([]string{"resvg"}, "CONVERSOR_RESVG"); p != "" {
		return &SVGEngine{p, "resvg"}
	}
	if p := findTool([]string{"rsvg-convert"}, "CONVERSOR_RSVG"); p != "" {
		return &SVGEngine{p, "rsvg"}
	}
	return &SVGEngine{backend: "built-in"}
}

func (*SVGEngine) ID() string          { return "svg" }
func (*SVGEngine) Priority() int       { return 0 }
func (*SVGEngine) Unavailable() string { return "" }
func (*SVGEngine) Steps() []core.Step  { return core.Steps([]string{"svg"}, []string{"png"}) }

// Detail names the renderer in use.
func (e *SVGEngine) Detail() string {
	if e.tool != "" {
		return i18n.T("%s at %s", e.backend, e.tool)
	}
	return i18n.T("built-in renderer (no text); install resvg for full SVG support")
}

func (e *SVGEngine) Convert(t *core.Task, input, outDir string) ([]string, error) {
	// SVG user units are CSS pixels, 96 per inch.
	scale := 1.0
	if t.Options.DPI != nil {
		scale = float64(*t.Options.DPI) / 96
	}
	out := t.Artifact(outDir, 0)
	t.Indeterminate()
	var err error
	if e.tool == "" {
		err = renderBuiltin(input, out, scale, t.Options)
	} else {
		err = e.renderTool(t, input, out, scale)
		if err == nil && t.Options.MaxSize != nil {
			// Re-render at the size that fits rather than scaling pixels.
			var w, h int
			if w, h, err = pngSize(out); err == nil {
				if longest := max(w, h); longest > *t.Options.MaxSize {
					scale *= float64(*t.Options.MaxSize) / float64(longest)
					err = e.renderTool(t, input, out, scale)
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return []string{out}, nil
}

func (e *SVGEngine) renderTool(t *core.Task, input, out string, scale float64) error {
	zoom := strconv.FormatFloat(scale, 'f', -1, 64)
	var cmd *exec.Cmd
	bg := ""
	if b := t.Options.Background; b != nil && !b.Transparent {
		bg = fmt.Sprintf("#%02x%02x%02x", b.Color[0], b.Color[1], b.Color[2])
	}
	if e.backend == "resvg" {
		args := []string{"--zoom", zoom}
		if bg != "" {
			args = append(args, "--background", bg)
		}
		cmd = exec.Command(e.tool, append(args, input, out)...)
	} else {
		args := []string{"--zoom", zoom, "--output", out}
		if bg != "" {
			args = append(args, "--background-color", bg)
		}
		cmd = exec.Command(e.tool, append(args, input)...)
	}
	return runTool(t, "svg", cmd, nil)
}

func pngSize(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	return cfg.Width, cfg.Height, err
}

func renderBuiltin(input, out string, scale float64, o *core.Options) error {
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.IgnoreErrorMode)
	if err != nil {
		return core.EngineFailed("svg", "%v", err)
	}
	w, h := icon.ViewBox.W, icon.ViewBox.H
	if w <= 0 || h <= 0 {
		return core.EngineFailed("svg", "%s", i18n.T("SVG has no size"))
	}
	if o.MaxSize != nil {
		if longest := math.Max(w, h) * scale; longest > float64(*o.MaxSize) {
			scale *= float64(*o.MaxSize) / longest
		}
	}
	pw, ph := int(math.Ceil(w*scale)), int(math.Ceil(h*scale))
	if pw <= 0 || ph <= 0 || pw*ph > 1<<28 {
		return core.EngineFailed("svg", "%s", i18n.T("SVG size %dx%d is out of range", pw, ph))
	}
	img := image.NewRGBA(image.Rect(0, 0, pw, ph))
	icon.SetTarget(0, 0, float64(pw), float64(ph))
	scanner := rasterx.NewScannerGV(pw, ph, img, img.Bounds())
	icon.Draw(rasterx.NewDasher(pw, ph, scanner), 1)
	// A chosen background goes under the drawing; otherwise the canvas
	// stays transparent, and a later step to a format without
	// transparency flattens it.
	return encodeImage(codec.ToNRGBA(img), "png", o, out, nil)
}
