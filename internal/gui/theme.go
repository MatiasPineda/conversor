package gui

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"

	"conversor/internal/core"
)

// appTheme is Fyne's theme with Conversor's accent color.
type appTheme struct{ fyne.Theme }

func newTheme() fyne.Theme { return appTheme{theme.DefaultTheme()} }

var accent = color.NRGBA{0x2f, 0x6f, 0xeb, 0xff}

func (t appTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return accent
	case theme.ColorNameSelection:
		return color.NRGBA{0x2f, 0x6f, 0xeb, 0x40}
	}
	return t.Theme.Color(name, v)
}

//go:embed assets/icon.png
var iconPNG []byte

// appIcon is the window and tray icon: two arrows in a rounded square.
var appIcon = fyne.NewStaticResource("conversor.png", iconPNG)

// busyIcon is the tray icon while conversions run: the same, in orange.
var busyIcon = fyne.NewStaticResource("conversor-busy.png", recolor(iconPNG, color.NRGBA{0xe8, 0x91, 0x2d, 0xff}))

// recolor swaps the icon's blue for c, keeping the white arrows and the
// anti-aliased edges.
func recolor(data []byte, c color.NRGBA) []byte {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	b := src.Bounds()
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			// How white the pixel is, from the blue's red channel up to 255.
			f := float64(int(p.R)-int(accent.R)) / float64(255-int(accent.R))
			f = min(max(f, 0), 1)
			mix := func(v uint8) uint8 { return uint8(float64(v)*(1-f) + 255*f) }
			out.SetNRGBA(x, y, color.NRGBA{mix(c.R), mix(c.G), mix(c.B), p.A})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, out)
	return buf.Bytes()
}

// categoryColor tints a file's badge by kind.
func categoryColor(c core.Category) color.Color {
	switch c {
	case core.Image, core.Vector:
		return color.NRGBA{0x2e, 0x9e, 0x6a, 0xff}
	case core.Video:
		return color.NRGBA{0x8a, 0x4f, 0xd6, 0xff}
	case core.Audio:
		return color.NRGBA{0xd6, 0x4f, 0x8a, 0xff}
	case core.PDF:
		return color.NRGBA{0xd6, 0x45, 0x3d, 0xff}
	case core.Spreadsheet:
		return color.NRGBA{0x2b, 0x8a, 0x3e, 0xff}
	case core.Presentation:
		return color.NRGBA{0xe0, 0x7b, 0x24, 0xff}
	}
	return color.NRGBA{0x3d, 0x6f, 0xc4, 0xff}
}

// badge is a small colored tile showing a file's extension.
type badge struct {
	rect *canvas.Rectangle
	text *canvas.Text
	box  *fyne.Container
}

func newBadge() *badge {
	b := &badge{rect: canvas.NewRectangle(color.Gray{0x88}), text: canvas.NewText("", color.White)}
	b.rect.CornerRadius = 6
	b.text.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	b.text.TextSize = 10
	b.text.Alignment = fyne.TextAlignCenter
	sized := container.NewGridWrap(fyne.NewSize(44, 36), container.NewStack(b.rect, container.NewCenter(b.text)))
	b.box = container.NewVBox(layout.NewSpacer(), sized, layout.NewSpacer())
	return b
}

func (b *badge) set(path string) {
	ext := strings.ToUpper(strings.TrimPrefix(fileExt(path), "."))
	if len(ext) > 4 {
		ext = ext[:4]
	}
	b.text.Text = ext
	b.rect.FillColor = color.Gray{0x88}
	if f := core.ByPath(path); f != nil {
		b.rect.FillColor = categoryColor(f.Category)
	}
	b.rect.Refresh()
	b.text.Refresh()
}

func fileExt(path string) string {
	i := strings.LastIndexByte(path, '.')
	if i < 0 || strings.ContainsAny(path[i:], `/\`) {
		return ""
	}
	return path[i:]
}
