package codec

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"io"
	"math"
	"strings"
	"testing"
)

// gradient is a 37×23 image with varied colors and, optionally, alpha.
func gradient(alpha bool) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, 37, 23))
	for y := range 23 {
		for x := range 37 {
			a := uint8(255)
			if alpha {
				a = uint8(x * 255 / 36)
			}
			m.SetNRGBA(x, y, color.NRGBA{uint8(x * 7), uint8(y * 11), uint8((x ^ y) * 5), a})
		}
	}
	return m
}

func samePixels(t *testing.T, name string, want, got image.Image, alpha bool) {
	t.Helper()
	if want.Bounds().Size() != got.Bounds().Size() {
		t.Fatalf("%s: size %v, want %v", name, got.Bounds().Size(), want.Bounds().Size())
	}
	wn, gn := ToNRGBA(want), ToNRGBA(got)
	for y := range wn.Rect.Dy() {
		for x := range wn.Rect.Dx() {
			w, g := wn.NRGBAAt(x, y), gn.NRGBAAt(x, y)
			if !alpha {
				w.A = 255
			}
			if w.A == 0 && g.A == 0 {
				continue
			}
			if w != g {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v", name, x, y, g, w)
			}
		}
	}
}

func TestRoundTrips(t *testing.T) {
	type codec struct {
		name   string
		enc    func(io.Writer, image.Image) error
		dec    func(io.Reader) (image.Image, error)
		alpha  bool
		opaque bool // test an opaque image too
	}
	for _, c := range []codec{
		{"bmp", EncodeBMP, DecodeBMP, true, true},
		{"ico", EncodeICO, DecodeICO, true, false},
		{"tga", EncodeTGA, DecodeTGA, true, true},
		{"qoi", EncodeQOI, DecodeQOI, true, true},
		{"ppm", EncodePPM, DecodePNM, false, true},
		{"exr", EncodeEXR, DecodeEXR, true, false},
	} {
		for _, alpha := range []bool{true, false} {
			if (alpha && !c.alpha) || (!alpha && !c.opaque && c.alpha) {
				continue
			}
			src := gradient(alpha)
			var buf bytes.Buffer
			if err := c.enc(&buf, src); err != nil {
				t.Fatalf("%s encode: %v", c.name, err)
			}
			got, err := c.dec(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("%s decode: %v", c.name, err)
			}
			samePixels(t, c.name, src, got, alpha)
		}
	}
}

func TestBMPHeaderIsV5WithAlpha(t *testing.T) {
	var buf bytes.Buffer
	EncodeBMP(&buf, gradient(true))
	b := buf.Bytes()
	if binary.LittleEndian.Uint32(b[14:]) != 124 || int(binary.LittleEndian.Uint32(b[2:])) != len(b) {
		t.Fatal("expected a BITMAPV5HEADER and a correct file size")
	}
}

func TestBMPVariants(t *testing.T) {
	// A 4×2 RLE8 image: row 0 (bottom) is a run of 4 index-1 pixels; row 1
	// is an absolute run of indexes 0,1,0,1.
	hdr := make([]byte, 40)
	binary.LittleEndian.PutUint32(hdr[0:], 40)
	binary.LittleEndian.PutUint32(hdr[4:], 4)
	binary.LittleEndian.PutUint32(hdr[8:], 2)
	binary.LittleEndian.PutUint16(hdr[12:], 1)
	binary.LittleEndian.PutUint16(hdr[14:], 8)
	binary.LittleEndian.PutUint32(hdr[16:], biRLE8)
	binary.LittleEndian.PutUint32(hdr[32:], 2)
	palette := []byte{0, 0, 255, 0, 0, 255, 0, 0} // red, green (BGRx)
	pixels := []byte{4, 1, 0, 0, 0, 4, 0, 1, 0, 1, 0, 1}
	var file bytes.Buffer
	writeFileHeader(&file, 14+40+8+len(pixels), 14+40+8)
	file.Write(hdr)
	file.Write(palette)
	file.Write(pixels)
	img, err := DecodeBMP(bytes.NewReader(file.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	red, green := color.NRGBA{255, 0, 0, 255}, color.NRGBA{0, 255, 0, 0xff}
	want := [2][4]color.NRGBA{{red, green, red, green}, {green, green, green, green}}
	for y := range 2 {
		for x := range 4 {
			if got := img.At(x, y).(color.NRGBA); got != want[y][x] {
				t.Fatalf("(%d,%d) = %v, want %v", x, y, got, want[y][x])
			}
		}
	}

	// 16-bit 5-5-5, top-down, with a gap before the pixels.
	binary.LittleEndian.PutUint32(hdr[8:], uint32(0xffffffff)) // height -1
	binary.LittleEndian.PutUint16(hdr[14:], 16)
	binary.LittleEndian.PutUint32(hdr[16:], biRGB)
	binary.LittleEndian.PutUint32(hdr[32:], 0)
	file.Reset()
	writeFileHeader(&file, 14+40+4+8, 14+40+4)
	file.Write(hdr)
	file.Write([]byte{9, 9, 9, 9})
	file.Write([]byte{0x00, 0x7c, 0xe0, 0x03, 0x1f, 0x00, 0xff, 0x7f})
	img, err = DecodeBMP(bytes.NewReader(file.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for x, want := range []color.NRGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 255}} {
		if got := img.At(x, 0).(color.NRGBA); got != want {
			t.Fatalf("16-bit pixel %d = %v, want %v", x, got, want)
		}
	}
}

func TestPNMVariants(t *testing.T) {
	img, err := DecodePNM(strings.NewReader("P1\n# comment\n3 2\n010\n1 0 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if img.At(0, 0).(color.Gray).Y != 255 || img.At(1, 0).(color.Gray).Y != 0 {
		t.Fatal("P1 pixels")
	}
	img, err = DecodePNM(strings.NewReader("P3 2 1 65535\n65535 0 0  0 32768 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c := img.At(1, 0).(color.NRGBA64); c.R != 0 || c.G != 32768 {
		t.Fatalf("P3 16-bit: %v", c)
	}
	img, err = DecodePNM(bytes.NewReader([]byte("P5\n2 1\n255\n\x00\x80")))
	if err != nil || img.At(1, 0).(color.Gray).Y != 0x80 {
		t.Fatal("P5", err)
	}
}

func TestTGARLEBottomUp(t *testing.T) {
	hdr := make([]byte, 18)
	hdr[2] = 10 // RLE true-color
	binary.LittleEndian.PutUint16(hdr[12:], 3)
	binary.LittleEndian.PutUint16(hdr[14:], 2)
	hdr[16] = 24
	// Bottom row first: 3 blue (repeat), then raw red, green, red.
	data := append(hdr, 0x82, 255, 0, 0, 0x02, 0, 0, 255, 0, 255, 0, 0, 0, 255)
	img, err := DecodeTGA(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.At(0, 1).(color.NRGBA) != (color.NRGBA{0, 0, 255, 255}) || img.At(1, 0).(color.NRGBA) != (color.NRGBA{0, 255, 0, 255}) {
		t.Fatalf("%v %v", img.At(0, 1), img.At(1, 0))
	}
}

func TestEXRHalfAndPrecision(t *testing.T) {
	for h, want := range map[uint16]float32{0x3c00: 1, 0xc000: -2, 0x3555: 0.333251953125, 0x0001: 5.960464477539063e-08, 0x7bff: 65504} {
		if got := halfToFloat(h); got != want {
			t.Fatalf("half %#04x = %v, want %v", h, got, want)
		}
	}
	src := NewRGBAF32(image.Rect(0, 0, 3, 20))
	for i := range src.Pix {
		src.Pix[i] = float32(i) * 0.37
	}
	var buf bytes.Buffer
	if err := EncodeEXR(&buf, src); err != nil {
		t.Fatal(err)
	}
	got, err := DecodeEXR(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got.(*RGBAF32).Pix {
		if math.Abs(float64(v-src.Pix[i])) > 1e-6 {
			t.Fatalf("sample %d = %v, want %v", i, v, src.Pix[i])
		}
	}
}

func TestQuantizeKeepsTransparencyAndFewColors(t *testing.T) {
	src := gradient(true)
	p := Quantize(src)
	if len(p.Palette) > 256 {
		t.Fatal("too many colors")
	}
	if _, _, _, a := p.At(0, 0).RGBA(); a != 0 {
		t.Fatal("transparent pixel lost")
	}
	if _, _, _, a := p.At(36, 0).RGBA(); a != 0xffff {
		t.Fatal("opaque pixel lost")
	}
	solid := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := range solid.Pix {
		solid.Pix[i] = 200
	}
	if q := Quantize(solid); len(q.Palette) != 1 {
		t.Fatalf("one color became %d", len(q.Palette))
	}
}
