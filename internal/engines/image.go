package engines

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"slices"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/heic"
	"github.com/gen2brain/webp"
	"golang.org/x/image/draw"
	"golang.org/x/image/tiff"
	xwebp "golang.org/x/image/webp"

	"conversor/internal/codec"
	"conversor/internal/core"
	"conversor/internal/i18n"
)

// imageInputs are the formats the image engine decodes. HEIC and AVIF use
// libheif/libavif and dav1d compiled to WebAssembly, so they need nothing
// installed.
var imageInputs = []string{"png", "jpeg", "webp", "gif", "tiff", "bmp", "ico", "tga", "ppm", "qoi", "exr", "heic", "avif"}

// imageOutputs are the formats it encodes. HEIC has no pure-Go encoder; the
// heif engine writes it with an installed tool.
var imageOutputs = []string{"png", "jpeg", "webp", "gif", "tiff", "bmp", "ico", "tga", "ppm", "qoi", "exr", "avif"}

// ImageEngine converts between still image formats in-process, with no
// system libraries, so it works everywhere.
type ImageEngine struct{}

func NewImageEngine() *ImageEngine { return &ImageEngine{} }

func (*ImageEngine) ID() string          { return "image" }
func (*ImageEngine) Priority() int       { return 0 }
func (*ImageEngine) Unavailable() string { return "" }
func (*ImageEngine) Steps() []core.Step  { return core.Steps(imageInputs, imageOutputs) }

func (*ImageEngine) Convert(t *core.Task, input, outDir string) ([]string, error) {
	img, err := openImage(t, input)
	if err != nil {
		return nil, err
	}
	if err := t.Check(); err != nil {
		return nil, err
	}
	t.Progress(0.5)
	out := t.Artifact(outDir, 0)
	if err := encodeImageFrom(fit(img, t.Options.MaxSize), t.Step.From.ID, t.Step.To.ID, t.Options, out, nil); err != nil {
		return nil, err
	}
	return []string{out}, nil
}

func imageFailed(err error) error { return core.EngineFailed("image", "%v", err) }

// openImage decodes input the way a viewer shows it: EXIF orientation
// applied, as one of the working types (NRGBA, NRGBA64 or RGBAF32).
func openImage(t *core.Task, input string) (image.Image, error) {
	data, err := os.ReadFile(input)
	if err != nil {
		return nil, err
	}
	from := core.ByPath(input)
	if from == nil {
		return nil, core.UndetectedFormat(input)
	}
	img, err := decodeImage(t, data, from.ID)
	if err != nil {
		return nil, imageFailed(err)
	}
	img = working(img)
	switch from.ID {
	case "jpeg", "png", "webp", "tiff":
		img = orient(img, exifOrientation(data))
	}
	return img, nil
}

func decodeImage(t *core.Task, data []byte, id string) (image.Image, error) {
	r := bytes.NewReader(data)
	switch id {
	case "png":
		return png.Decode(r)
	case "jpeg":
		return jpeg.Decode(r)
	case "gif":
		// The first frame, as a still.
		return gif.Decode(r)
	case "webp":
		// x/image/webp decodes lossless WebP exactly; libwebp's decoder
		// here always returns YCbCr. libwebp still handles what x/image
		// can't, such as animations (first frame).
		if img, err := xwebp.Decode(r); err == nil {
			return img, nil
		}
		return webp.Decode(bytes.NewReader(data))
	case "tiff":
		return tiff.Decode(r)
	case "bmp":
		return codec.DecodeBMP(r)
	case "ico":
		return codec.DecodeICO(r)
	case "tga":
		return codec.DecodeTGA(r)
	case "ppm":
		return codec.DecodePNM(r)
	case "qoi":
		return codec.DecodeQOI(r)
	case "heic":
		// heic applies the HEIF rotation and mirroring itself.
		return heic.Decode(r)
	case "avif":
		return avif.Decode(r, avif.Options{AutoRotate: true})
	case "exr":
		img, err := codec.DecodeEXR(r)
		if errors.Is(err, codec.ErrEXRUnsupported) {
			// FFmpeg reads every EXR compression, including PIZ and DWA.
			if fallback, ferr := decodeWithFFmpeg(t, data, "exr"); ferr == nil {
				return fallback, nil
			}
		}
		return img, err
	}
	return nil, fmt.Errorf("no decoder for %s", id)
}

// working converts any decoded image to NRGBA, NRGBA64 (for 16-bit
// sources) or RGBAF32 (for float sources).
func working(img image.Image) image.Image {
	switch img.(type) {
	case *image.NRGBA, *image.NRGBA64, *codec.RGBAF32:
		if img.Bounds().Min == (image.Point{}) {
			return img
		}
	case *image.RGBA64, *image.Gray16:
		return codec.ToNRGBA64(img)
	}
	return codec.ToNRGBA(img)
}

// fit scales img down so its longest edge is at most max. It never scales up.
func fit(img image.Image, maxSize *int) image.Image {
	if maxSize == nil {
		return img
	}
	b := img.Bounds()
	m := *maxSize
	if b.Dx() <= m && b.Dy() <= m {
		return img
	}
	scale := float64(m) / float64(max(b.Dx(), b.Dy()))
	w := max(1, int(float64(b.Dx())*scale+0.5))
	h := max(1, int(float64(b.Dy())*scale+0.5))
	r := image.Rect(0, 0, w, h)
	switch img.(type) {
	case *image.NRGBA64, *codec.RGBAF32:
		dst := image.NewNRGBA64(r)
		draw.CatmullRom.Scale(dst, r, codec.ToNRGBA64(img), b, draw.Src, nil)
		return dst
	}
	dst := image.NewNRGBA(r)
	draw.CatmullRom.Scale(dst, r, img, b, draw.Src, nil)
	return dst
}

// applyBackground handles transparency for output in format to: a chosen
// color flattens it, Transparent keeps it, and with nothing chosen a format
// that can't store transparency (JPEG, PPM) gets white.
func applyBackground(img image.Image, to string, o *core.Options) (image.Image, error) {
	keeps := core.ByID(to) != nil && core.ByID(to).KeepsTransparency()
	switch {
	case o.Background != nil && !o.Background.Transparent:
		return flatten(img, o.Background.Color), nil
	case o.Background != nil && !keeps:
		return nil, core.InvalidOption("%s", noTransparency(to))
	case !keeps:
		return flatten(img, core.White.Color), nil
	}
	return img, nil
}

// noTransparency is the message for asking a format without transparency
// to keep it.
func noTransparency(to string) string {
	name := to
	if f := core.ByID(to); f != nil {
		name = f.Name
	}
	return i18n.T("%s can't store transparency; choose a background color such as white or black", name)
}

// flatten composites img over an opaque color. Opaque images pass through;
// 16-bit and float images keep their precision.
func flatten(img image.Image, c [3]uint8) image.Image {
	if codec.IsOpaque(img) {
		return img
	}
	switch m := img.(type) {
	case *codec.RGBAF32:
		out := codec.NewRGBAF32(m.Rect)
		copy(out.Pix, m.Pix)
		for i := 0; i < len(out.Pix); i += 4 {
			a := min(max(out.Pix[i+3], 0), 1)
			for k := range 3 {
				out.Pix[i+k] = out.Pix[i+k]*a + float32(c[k])/255*(1-a)
			}
			out.Pix[i+3] = 1
		}
		return out
	case *image.NRGBA64:
		out := codec.ToNRGBA64(m)
		out = &image.NRGBA64{Pix: slices.Clone(out.Pix), Stride: out.Stride, Rect: out.Rect}
		for i := 0; i < len(out.Pix); i += 8 {
			a := uint32(binary.BigEndian.Uint16(out.Pix[i+6:]))
			for k := range 3 {
				v := uint32(binary.BigEndian.Uint16(out.Pix[i+2*k:]))
				bg := uint32(c[k]) * 257
				binary.BigEndian.PutUint16(out.Pix[i+2*k:], uint16((v*a+bg*(65535-a)+32767)/65535))
			}
			binary.BigEndian.PutUint16(out.Pix[i+6:], 0xffff)
		}
		return out
	}
	src := codec.ToNRGBA(img)
	out := image.NewNRGBA(src.Rect)
	for i := 0; i < len(src.Pix); i += 4 {
		a := uint32(src.Pix[i+3])
		for k := range 3 {
			out.Pix[i+k] = uint8((uint32(src.Pix[i+k])*a + uint32(c[k])*(255-a) + 127) / 255)
		}
		out.Pix[i+3] = 255
	}
	return out
}

// pixelAspect is a frame's pixel aspect ratio, kept in JPEG output.
type pixelAspect struct{ num, den uint16 }

// encodeImage writes img as format to into path, after applyBackground. The
// file is flushed and closed explicitly, so a write error (a full disk)
// fails the job instead of publishing a truncated file.
func encodeImage(img image.Image, to string, o *core.Options, path string, aspect *pixelAspect) error {
	return encodeImageFrom(img, "", to, o, path, aspect)
}

// encodeImageFrom is encodeImage knowing the source format, which picks
// lossless or lossy WebP.
func encodeImageFrom(img image.Image, from, to string, o *core.Options, path string, aspect *pixelAspect) error {
	img, err := applyBackground(img, to, o)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	err = encodeTo(w, img, to, from, o, aspect)
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var ce *core.Error
		if errors.As(err, &ce) {
			return err
		}
		return imageFailed(err)
	}
	return nil
}

// lossySource lists formats whose pixels already went through lossy
// compression, so re-encoding them losslessly only wastes space.
var lossySource = map[string]bool{"jpeg": true, "heic": true, "avif": true}

func encodeTo(w *bufio.Writer, img image.Image, to, from string, o *core.Options, aspect *pixelAspect) error {
	quality := o.Quality
	switch to {
	case "png":
		if _, ok := img.(*codec.RGBAF32); ok {
			img = codec.ToNRGBA64(img)
		}
		return png.Encode(w, img)
	case "jpeg":
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: core.Get(quality, 90)}); err != nil {
			return err
		}
		_, err := w.Write(withJFIFAspect(buf.Bytes(), aspect))
		return err
	case "webp":
		// Photos stay lossy (lossless WebP of a JPEG is twice its size);
		// lossless sources stay lossless unless a quality is asked for.
		lossless := quality == nil && !lossySource[from]
		opts := webp.Options{Lossless: lossless, Quality: core.Get(quality, 90), Method: 4, Exact: true}
		if lossless {
			// For lossless, quality is effort; 75 matches cwebp.
			opts.Quality = 75
		}
		return webp.Encode(w, codec.ToNRGBA(img), opts)
	case "avif":
		return encodeAVIF(w, img, core.Get(quality, 80))
	case "gif":
		return gif.Encode(w, codec.Quantize(img), nil)
	case "tiff":
		if _, ok := img.(*codec.RGBAF32); ok {
			img = codec.ToNRGBA64(img)
		}
		return tiff.Encode(w, img, nil)
	case "bmp":
		return codec.EncodeBMP(w, img)
	case "ico":
		return codec.EncodeICO(w, img)
	case "tga":
		return codec.EncodeTGA(w, img)
	case "ppm":
		return codec.EncodePPM(w, img)
	case "qoi":
		return codec.EncodeQOI(w, img)
	case "exr":
		return codec.EncodeEXR(w, img)
	}
	return fmt.Errorf("no encoder for %s", to)
}

// withJFIFAspect adds a JFIF header recording a non-square pixel aspect
// ratio, so players and viewers stretch a video frame the way the video
// showed it. Square pixels need nothing.
func withJFIFAspect(jpg []byte, a *pixelAspect) []byte {
	if a == nil || a.num == a.den || len(jpg) < 2 {
		return jpg
	}
	app0 := []byte{0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(app0[12:], a.num)
	binary.BigEndian.PutUint16(app0[14:], a.den)
	out := make([]byte, 0, len(jpg)+len(app0))
	out = append(out, jpg[:2]...)
	out = append(out, app0...)
	return append(out, jpg[2:]...)
}

// backgroundPNG applies a chosen background color to the PNG at path in
// place, for engines that get PNG from an outside tool (FFmpeg, resvg).
// Anything but a color leaves the file alone, since PNG keeps transparency.
func backgroundPNG(path string, o *core.Options) error {
	if o.Background == nil || o.Background.Transparent {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return imageFailed(err)
	}
	return encodeImage(working(img), "png", o, path, nil)
}
