package engines

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"conversor/internal/codec"
	"conversor/internal/core"
)

func task(from, to string, o core.Options) *core.Task {
	return core.NewTask(context.Background(), core.Step{From: core.MustID(from), To: core.MustID(to)}, &o, nil)
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, e core.Engine, input, to string, o core.Options) []string {
	t.Helper()
	from := core.ByPath(input).ID
	out := t.TempDir()
	files, err := e.Convert(task(from, to, o), input, out)
	if err != nil {
		t.Fatalf("%s -> %s: %v", from, to, err)
	}
	return files
}

func decode(t *testing.T, path string) image.Image {
	t.Helper()
	img, err := openImage(task("png", "png", core.Options{}), path)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

func TestImageToEveryFormat(t *testing.T) {
	src := filepath.Join(t.TempDir(), "in.png")
	img := image.NewNRGBA(image.Rect(0, 0, 64, 48))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 20, 160, 96, 128
	}
	writePNG(t, src, img)
	e := NewImageEngine()
	for _, to := range imageOutputs {
		if to == "png" {
			continue
		}
		out := run(t, e, src, to, core.Options{})
		if filepath.Ext(out[0]) != "."+core.MustID(to).Extension() {
			t.Fatalf("%s: named %s", to, out[0])
		}
		back := decode(t, out[0])
		if back.Bounds().Dx() != 64 || back.Bounds().Dy() != 48 {
			t.Fatalf("%s: size %v", to, back.Bounds())
		}
	}
	small := run(t, e, src, "png", core.Options{MaxSize: core.Ptr(32)})
	if b := decode(t, small[0]).Bounds(); b.Dx() != 32 || b.Dy() != 24 {
		t.Fatalf("max size: %v", b)
	}
}

func TestBackgroundRules(t *testing.T) {
	src := filepath.Join(t.TempDir(), "clear.png")
	writePNG(t, src, image.NewNRGBA(image.Rect(0, 0, 4, 4))) // fully transparent
	e := NewImageEngine()
	px := func(path string) color.NRGBA { return codec.ToNRGBA(decode(t, path)).NRGBAAt(0, 0) }

	// JPEG has no alpha: white by default, or the chosen color.
	if c := px(run(t, e, src, "jpeg", core.Options{})[0]); c.R < 250 || c.G < 250 || c.B < 250 {
		t.Fatalf("default JPEG background %v", c)
	}
	black := core.Black
	if c := px(run(t, e, src, "jpeg", core.Options{Background: &black})[0]); c.R > 5 {
		t.Fatalf("black background %v", c)
	}
	// PNG keeps transparency unless a color is chosen.
	if c := px(run(t, e, src, "png", core.Options{})[0]); c.A != 0 {
		t.Fatalf("PNG lost transparency %v", c)
	}
	orange, _ := core.ParseBackground("#ff8800")
	if c := px(run(t, e, src, "webp", core.Options{Background: &orange})[0]); c != (color.NRGBA{255, 136, 0, 255}) {
		t.Fatalf("orange background %v", c)
	}
	// Asking JPEG to keep transparency is an error, not a silent white.
	clear := core.Transparent
	if _, err := e.Convert(task("png", "jpeg", core.Options{Background: &clear}), src, t.TempDir()); core.Kind(err) != core.KindInvalidOption {
		t.Fatal(err)
	}
}

// jpegWithOrientation inserts an EXIF APP1 segment with orientation o.
func jpegWithOrientation(jpg []byte, o uint16) []byte {
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00")
	binary.BigEndian.PutUint16(tiff[18:], o)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	out = append(out, payload...)
	return append(out, jpg[2:]...)
}

func TestJPEGOrientationIsBakedIn(t *testing.T) {
	// Red top-left, green top-right, blue bottom-left, yellow bottom-right.
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := range 24 {
		for x := range 32 {
			c := color.RGBA{240, 24, 24, 255}
			switch {
			case x >= 16 && y < 12:
				c = color.RGBA{24, 224, 24, 255}
			case x < 16 && y >= 12:
				c = color.RGBA{24, 24, 240, 255}
			case x >= 16:
				c = color.RGBA{224, 224, 24, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
	src := filepath.Join(t.TempDir(), "oriented.jpg")
	os.WriteFile(src, jpegWithOrientation(buf.Bytes(), 6), 0o644)
	if o := exifOrientation(jpegWithOrientation(buf.Bytes(), 6)); o != 6 {
		t.Fatalf("orientation %d", o)
	}
	out := run(t, NewImageEngine(), src, "webp", core.Options{})
	data, _ := os.ReadFile(out[0])
	if exifOrientation(data) != 1 {
		t.Fatal("output still carries a rotation")
	}
	got := codec.ToNRGBA(decode(t, out[0]))
	if got.Rect.Dx() != 24 || got.Rect.Dy() != 32 {
		t.Fatalf("size %v, want 24x32", got.Rect)
	}
	// After 90° clockwise, red is top-right and blue top-left.
	if c := got.NRGBAAt(18, 8); c.R < 200 {
		t.Fatalf("top-right %v, want red", c)
	}
	if c := got.NRGBAAt(6, 8); c.B < 180 {
		t.Fatalf("top-left %v, want blue", c)
	}
}

func TestHEICAndAVIFDecode(t *testing.T) {
	e := NewImageEngine()
	for _, name := range []string{"test8.heic", "test8.avif", "test_rot.avif"} {
		out := run(t, e, filepath.Join("testdata", name), "jpeg", core.Options{})
		if b := decode(t, out[0]).Bounds(); b.Empty() {
			t.Fatalf("%s: empty", name)
		}
	}
}

// minimalPDF builds a PDF with one 200×100 pt page per color.
func minimalPDF(colors []string) []byte {
	var b bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	b.WriteString("%PDF-1.4\n")
	n := len(colors)
	kids := make([]string, n)
	for i := range n {
		kids[i] = fmt.Sprintf("%d 0 R", 3+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	for i, c := range colors {
		content := c + " rg 0 0 100 100 re f"
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Contents %d 0 R >>", 4+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return b.Bytes()
}

func TestPDFPages(t *testing.T) {
	src := filepath.Join(t.TempDir(), "doc.pdf")
	os.WriteFile(src, minimalPDF([]string{"1 0 0", "0 1 0", "0 0 1"}), 0o644)
	e := NewPDFEngine()
	all := run(t, e, src, "png", core.Options{})
	if len(all) != 3 {
		t.Fatalf("%d pages", len(all))
	}
	first := codec.ToNRGBA(decode(t, all[0]))
	if first.Rect.Dx() != 2000 || first.Rect.Dy() != 1000 {
		t.Fatalf("default size %v", first.Rect)
	}
	// Left half is the colored square, right half white page.
	if c := first.NRGBAAt(100, 900); c != (color.NRGBA{255, 0, 0, 255}) {
		t.Fatalf("red square %v", c)
	}
	if c := first.NRGBAAt(1900, 100); c != (color.NRGBA{255, 255, 255, 255}) {
		t.Fatalf("white page %v", c)
	}
	// --pages 2-3 at 72 dpi names files after their pages.
	some := run(t, e, src, "jpeg", core.Options{Pages: &core.PageRange{First: 2, Last: 3}, DPI: core.Ptr(72)})
	if len(some) != 2 || filepath.Base(some[0]) != "2.jpg" || filepath.Base(some[1]) != "3.jpg" {
		t.Fatalf("%v", some)
	}
	if b := decode(t, some[0]).Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Fatalf("72 dpi size %v", b)
	}
	clear := core.Transparent
	tr := codec.ToNRGBA(decode(t, run(t, e, src, "png", core.Options{Background: &clear, MaxSize: core.Ptr(100)})[0]))
	if tr.Rect.Dx() != 100 || tr.NRGBAAt(90, 10).A != 0 || tr.NRGBAAt(10, 40).A != 255 {
		t.Fatalf("transparent page: %v %v %v", tr.Rect, tr.NRGBAAt(90, 10), tr.NRGBAAt(10, 40))
	}
	if _, err := e.Convert(task("pdf", "png", core.Options{Pages: &core.PageRange{First: 5, Last: 0}}), src, t.TempDir()); core.Kind(err) != core.KindInvalidOption {
		t.Fatal(err)
	}
}

func TestSVGBuiltin(t *testing.T) {
	src := filepath.Join(t.TempDir(), "art.svg")
	os.WriteFile(src, []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20" viewBox="0 0 40 20"><rect x="0" y="0" width="20" height="20" fill="#ff0000"/></svg>`), 0o644)
	e := &SVGEngine{backend: "built-in"}
	img := codec.ToNRGBA(decode(t, run(t, e, src, "png", core.Options{DPI: core.Ptr(192)})[0]))
	if img.Rect.Dx() != 80 || img.Rect.Dy() != 40 {
		t.Fatalf("size %v", img.Rect)
	}
	if c := img.NRGBAAt(10, 10); c.R != 255 || c.A != 255 {
		t.Fatalf("rect %v", c)
	}
	if c := img.NRGBAAt(70, 10); c.A != 0 {
		t.Fatalf("background should stay clear: %v", c)
	}
}

func TestFFmpegArgs(t *testing.T) {
	join := func(to string, o core.Options) string { return strings.Join(outputArgs(to, &o), " ") }
	for to, want := range map[string]string{
		"mp4":  "-c:v libx264 -preset medium -crf 20 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart",
		"mkv":  "-c:v libx264 -preset medium -crf 20 -pix_fmt yuv420p -c:a aac -b:a 192k",
		"webm": "-c:v libvpx-vp9 -crf 32 -b:v 0 -row-mt 1 -deadline good -cpu-used 4 -pix_fmt yuv420p -c:a libopus",
		"avi":  "-c:v mpeg4 -q:v 3 -c:a libmp3lame",
	} {
		if got := join(to, core.Options{}); got != want {
			t.Fatalf("%s:\n got %s\nwant %s", to, got, want)
		}
	}
	o := core.Options{Quality: core.Ptr(80), VideoHeight: core.Ptr(480), AudioBitrate: core.Ptr(96)}
	mp4 := join("mp4", o)
	if !strings.Contains(mp4, "-crf 20") || !strings.Contains(mp4, "-b:a 96k") || !strings.HasSuffix(mp4, "-vf "+videoScale(480)) {
		t.Fatal(mp4)
	}
	if gif := join("gif", o); !strings.Contains(gif, videoScale(480)+":flags") || strings.Contains(gif, "-vf scale") {
		t.Fatal(gif)
	}
	if mp3 := join("mp3", o); !strings.Contains(mp3, "-b:a 96k") || strings.Contains(mp3, "-q:a") {
		t.Fatal(mp3)
	}
	if strings.Contains(join("png", o), "scale") {
		t.Fatal("png frame should not scale by video height")
	}
	hevc := core.HEVC
	if a := join("mp4", core.Options{VideoCodec: &hevc}); !strings.Contains(a, "libx265") || !strings.Contains(a, "-tag:v hvc1") {
		t.Fatal(a)
	}
	if strings.Contains(join("mkv", core.Options{VideoCodec: &hevc}), "hvc1") {
		t.Fatal("mkv should not be tagged hvc1")
	}
	for _, to := range []string{"mp4", "mov", "mkv", "webm", "avi"} {
		a := join(to, core.Options{StripAudio: true})
		if !strings.Contains(a, "-an") || strings.Contains(a, "-c:a") {
			t.Fatalf("%s: %s", to, a)
		}
	}
	if strings.Contains(join("mp3", core.Options{StripAudio: true}), "-an") {
		t.Fatal("audio targets ignore strip_audio")
	}
	e := noAudioError(core.EngineFailed("ffmpeg", "Output file does not contain any stream"), "mp3")
	if !strings.Contains(e.Error(), "no audio track") {
		t.Fatal(e)
	}
}

func TestOfficeFilters(t *testing.T) {
	step := func(from, to string) core.Step { return core.Step{From: core.MustID(from), To: core.MustID(to)} }
	if f := officeFilter(step("xlsx", "html")); f != "html:HTML (StarCalc):EmbedImages" {
		t.Fatal(f)
	}
	if f := officeFilter(step("docx", "pdf")); f != "pdf" {
		t.Fatal(f)
	}
	if u := fileURL(`C:\Users\Ana María\tmp`); u != "file:///C:/Users/Ana%20Mar%C3%ADa/tmp/" && !strings.HasPrefix(u, "file:///") {
		t.Fatal(u)
	}
}

func TestDefaultRoutes(t *testing.T) {
	r := Default()
	for _, c := range [][3]string{
		{"heic", "jpeg", "heic -[image]-> jpeg"},
		{"svg", "jpeg", "svg -[svg]-> png -[image]-> jpeg"},
		{"pdf", "webp", "pdf -[pdfium]-> png -[image]-> webp"},
		{"exr", "png", "exr -[image]-> png"},
	} {
		plan, err := r.Plan(core.MustID(c[0]), core.MustID(c[1]))
		if err != nil || plan.Describe() != c[2] {
			t.Fatalf("%s -> %s: %v %v", c[0], c[1], plan.Describe(), err)
		}
	}
}

func TestRunToolCancelsAndReportsStderr(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	tk := task("png", "jpeg", core.Options{})
	var lines []string
	err := runTool(tk, "test", exec.Command("sh", "-c", "sleep 30 & echo done"), func(l string) { lines = append(lines, l) })
	if err != nil || len(lines) != 1 || lines[0] != "done" {
		t.Fatal(err, lines)
	}
	err = runTool(tk, "test", exec.Command("sh", "-c", "echo broken >&2; exit 3"), nil)
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := core.Options{}
	tk = core.NewTask(ctx, tk.Step, &o, nil)
	go cancel()
	if err := runTool(tk, "test", exec.Command("sh", "-c", "sleep 30"), nil); !core.IsCancelled(err) {
		t.Fatal(err)
	}
}
