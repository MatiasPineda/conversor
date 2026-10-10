package codec

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"sort"
)

// RGBAF32 is an image of float32 RGBA samples with straight (not
// premultiplied) alpha, as OpenEXR holds. Values may lie outside 0..1;
// At clamps them.
type RGBAF32 struct {
	Pix    []float32
	Stride int // floats per row
	Rect   image.Rectangle
}

// NewRGBAF32 returns a transparent black float image.
func NewRGBAF32(r image.Rectangle) *RGBAF32 {
	return &RGBAF32{Pix: make([]float32, 4*r.Dx()*r.Dy()), Stride: 4 * r.Dx(), Rect: r}
}

func (m *RGBAF32) ColorModel() color.Model { return color.NRGBA64Model }
func (m *RGBAF32) Bounds() image.Rectangle { return m.Rect }

// PixOffset is the index of the first sample of pixel (x, y).
func (m *RGBAF32) PixOffset(x, y int) int {
	return (y-m.Rect.Min.Y)*m.Stride + (x-m.Rect.Min.X)*4
}

func (m *RGBAF32) At(x, y int) color.Color {
	if !(image.Point{x, y}.In(m.Rect)) {
		return color.NRGBA64{}
	}
	o := m.PixOffset(x, y)
	q := func(v float32) uint16 {
		if !(v > 0) { // also catches NaN
			return 0
		}
		if v >= 1 {
			return 0xffff
		}
		return uint16(v*65535 + 0.5)
	}
	return color.NRGBA64{q(m.Pix[o]), q(m.Pix[o+1]), q(m.Pix[o+2]), q(m.Pix[o+3])}
}

// Opaque reports whether every alpha sample is at least 1.
func (m *RGBAF32) Opaque() bool {
	for i := 3; i < len(m.Pix); i += 4 {
		if m.Pix[i] < 1 {
			return false
		}
	}
	return true
}

// ToRGBAF32 converts any image to float samples in 0..1.
func ToRGBAF32(m image.Image) *RGBAF32 {
	if f, ok := m.(*RGBAF32); ok {
		return f
	}
	b := m.Bounds()
	out := NewRGBAF32(image.Rect(0, 0, b.Dx(), b.Dy()))
	src := ToNRGBA64(m)
	for i := 0; i < len(out.Pix); i++ {
		out.Pix[i] = float32(uint16(src.Pix[2*i])<<8|uint16(src.Pix[2*i+1])) / 65535
	}
	return out
}

// ErrEXRUnsupported marks EXR files this decoder can't read (tiled, deep,
// multi-part, or PIZ/PXR24/B44/DWA compression). Callers may fall back to
// another decoder.
var ErrEXRUnsupported = errors.New("exr: unsupported variant")

const (
	exrNone = 0
	exrRLE  = 1
	exrZIPS = 2
	exrZIP  = 3
)

type exrChannel struct {
	name      string
	pixelType int32 // 0 uint, 1 half, 2 float
	xs, ys    int32
}

func (c exrChannel) size() int {
	if c.pixelType == 1 {
		return 2
	}
	return 4
}

// DecodeEXR reads a single-part scanline OpenEXR image compressed with
// NONE, RLE, ZIPS or ZIP. R, G, B, A and Y (luminance) channels are used.
func DecodeEXR(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 || binary.LittleEndian.Uint32(data) != 20000630 {
		return nil, errors.New("exr: not an OpenEXR file")
	}
	flags := binary.LittleEndian.Uint32(data[4:]) >> 8
	if flags&(0x2|0x8|0x10) != 0 { // tiled, deep, multi-part
		return nil, fmt.Errorf("%w: tiled, deep or multi-part", ErrEXRUnsupported)
	}
	pos := 8
	cstr := func() (string, error) {
		end := bytes.IndexByte(data[pos:], 0)
		if end < 0 {
			return "", errors.New("exr: truncated header")
		}
		s := string(data[pos : pos+end])
		pos += end + 1
		return s, nil
	}
	var channels []exrChannel
	compression := -1
	var dw [4]int32
	haveDW := false
	for {
		name, err := cstr()
		if err != nil {
			return nil, err
		}
		if name == "" {
			break
		}
		typ, err := cstr()
		if err != nil {
			return nil, err
		}
		if pos+4 > len(data) {
			return nil, errors.New("exr: truncated header")
		}
		size := int(binary.LittleEndian.Uint32(data[pos:]))
		pos += 4
		if size < 0 || pos+size > len(data) {
			return nil, errors.New("exr: truncated header")
		}
		val := data[pos : pos+size]
		pos += size
		switch {
		case name == "channels" && typ == "chlist":
			for p := 0; p < len(val) && val[p] != 0; {
				end := bytes.IndexByte(val[p:], 0)
				if end < 0 || p+end+17 > len(val) {
					return nil, errors.New("exr: bad channel list")
				}
				ch := exrChannel{name: string(val[p : p+end])}
				p += end + 1
				ch.pixelType = int32(binary.LittleEndian.Uint32(val[p:]))
				ch.xs = int32(binary.LittleEndian.Uint32(val[p+8:]))
				ch.ys = int32(binary.LittleEndian.Uint32(val[p+12:]))
				p += 16
				channels = append(channels, ch)
			}
		case name == "compression" && len(val) == 1:
			compression = int(val[0])
		case name == "dataWindow" && len(val) == 16:
			for i := range 4 {
				dw[i] = int32(binary.LittleEndian.Uint32(val[4*i:]))
			}
			haveDW = true
		}
	}
	if !haveDW || len(channels) == 0 || compression < 0 {
		return nil, errors.New("exr: missing required attributes")
	}
	linesPer := 0
	switch compression {
	case exrNone, exrRLE, exrZIPS:
		linesPer = 1
	case exrZIP:
		linesPer = 16
	default:
		return nil, fmt.Errorf("%w: compression %d", ErrEXRUnsupported, compression)
	}
	for _, ch := range channels {
		if ch.xs != 1 || ch.ys != 1 || ch.pixelType < 0 || ch.pixelType > 2 {
			return nil, fmt.Errorf("%w: subsampled channels", ErrEXRUnsupported)
		}
	}
	// Channels are stored in alphabetical order.
	sort.Slice(channels, func(i, j int) bool { return channels[i].name < channels[j].name })

	width := int(dw[2]-dw[0]) + 1
	height := int(dw[3]-dw[1]) + 1
	if width <= 0 || height <= 0 || width > 1<<16 || height > 1<<16 {
		return nil, errors.New("exr: bad data window")
	}
	chunks := (height + linesPer - 1) / linesPer
	if pos+8*chunks > len(data) {
		return nil, errors.New("exr: truncated offset table")
	}
	offsets := make([]int, chunks)
	for i := range offsets {
		offsets[i] = int(binary.LittleEndian.Uint64(data[pos+8*i:]))
	}

	bytesPerLine := 0
	for _, ch := range channels {
		bytesPerLine += ch.size() * width
	}
	img := NewRGBAF32(image.Rect(0, 0, width, height))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 1
	}
	hasLuma := false
	for _, ch := range channels {
		hasLuma = hasLuma || ch.name == "Y"
	}
	for _, off := range offsets {
		if off < 0 || off+8 > len(data) {
			return nil, errors.New("exr: bad chunk offset")
		}
		y0 := int(int32(binary.LittleEndian.Uint32(data[off:]))) - int(dw[1])
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		if size < 0 || off+8+size > len(data) || y0 < 0 || y0 >= height {
			return nil, errors.New("exr: bad chunk")
		}
		lines := min(linesPer, height-y0)
		want := lines * bytesPerLine
		block := data[off+8 : off+8+size]
		if size < want {
			if block, err = exrDecompress(block, compression, want); err != nil {
				return nil, err
			}
		}
		if len(block) < want {
			return nil, errors.New("exr: short chunk")
		}
		p := 0
		for l := range lines {
			row := (y0 + l) * img.Stride
			for _, ch := range channels {
				dst := -1
				switch ch.name {
				case "R":
					dst = 0
				case "G":
					dst = 1
				case "B":
					dst = 2
				case "A":
					dst = 3
				case "Y":
					dst = 4
				}
				for x := range width {
					var v float32
					switch ch.pixelType {
					case 0:
						v = float32(binary.LittleEndian.Uint32(block[p:])) / math.MaxUint32
					case 1:
						v = halfToFloat(binary.LittleEndian.Uint16(block[p:]))
					case 2:
						v = math.Float32frombits(binary.LittleEndian.Uint32(block[p:]))
					}
					p += ch.size()
					switch {
					case dst == 4 && hasLuma:
						o := row + x*4
						img.Pix[o], img.Pix[o+1], img.Pix[o+2] = v, v, v
					case dst >= 0 && dst < 4:
						img.Pix[row+x*4+dst] = v
					}
				}
			}
		}
	}
	return img, nil
}

func exrDecompress(src []byte, compression, want int) ([]byte, error) {
	var raw []byte
	switch compression {
	case exrZIP, exrZIPS:
		zr, err := zlib.NewReader(bytes.NewReader(src))
		if err != nil {
			return nil, fmt.Errorf("exr: %w", err)
		}
		raw = make([]byte, want)
		if _, err := io.ReadFull(zr, raw); err != nil {
			return nil, fmt.Errorf("exr: %w", err)
		}
	case exrRLE:
		raw = make([]byte, 0, want)
		for i := 0; i < len(src) && len(raw) < want; {
			n := int(int8(src[i]))
			i++
			if n < 0 {
				if i-n > len(src) {
					return nil, errors.New("exr: bad RLE data")
				}
				raw = append(raw, src[i:i-n]...)
				i -= n
			} else {
				if i >= len(src) {
					return nil, errors.New("exr: bad RLE data")
				}
				for range n + 1 {
					raw = append(raw, src[i])
				}
				i++
			}
		}
		if len(raw) < want {
			return nil, errors.New("exr: short RLE data")
		}
	default:
		return nil, ErrEXRUnsupported
	}
	// Undo the predictor, then the byte interleaving.
	for i := 1; i < len(raw); i++ {
		raw[i] = raw[i-1] + raw[i] - 128
	}
	out := make([]byte, len(raw))
	half := (len(raw) + 1) / 2
	for i := range out {
		if i%2 == 0 {
			out[i] = raw[i/2]
		} else {
			out[i] = raw[half+i/2]
		}
	}
	return out, nil
}

// EncodeEXR writes a scanline OpenEXR image with float32 R, G, B and A
// channels, ZIP-compressed.
func EncodeEXR(w io.Writer, m image.Image) error {
	src := ToRGBAF32(m)
	width, height := src.Rect.Dx(), src.Rect.Dy()
	var hdr bytes.Buffer
	le := func(v any) { binary.Write(&hdr, binary.LittleEndian, v) }
	attr := func(name, typ string, size int) {
		hdr.WriteString(name)
		hdr.WriteByte(0)
		hdr.WriteString(typ)
		hdr.WriteByte(0)
		le(int32(size))
	}
	le(uint32(20000630))
	le(uint32(2))
	names := []string{"A", "B", "G", "R"}
	attr("channels", "chlist", len(names)*18+1)
	for _, n := range names {
		hdr.WriteString(n)
		hdr.WriteByte(0)
		le(int32(2)) // FLOAT
		hdr.Write([]byte{0, 0, 0, 0})
		le(int32(1))
		le(int32(1))
	}
	hdr.WriteByte(0)
	attr("compression", "compression", 1)
	hdr.WriteByte(exrZIP)
	box := []int32{0, 0, int32(width - 1), int32(height - 1)}
	attr("dataWindow", "box2i", 16)
	le(box)
	attr("displayWindow", "box2i", 16)
	le(box)
	attr("lineOrder", "lineOrder", 1)
	hdr.WriteByte(0)
	attr("pixelAspectRatio", "float", 4)
	le(float32(1))
	attr("screenWindowCenter", "v2f", 8)
	le([]float32{0, 0})
	attr("screenWindowWidth", "float", 4)
	le(float32(1))
	hdr.WriteByte(0)

	const linesPer = 16
	chunks := (height + linesPer - 1) / linesPer
	tableAt := hdr.Len()
	hdr.Write(make([]byte, 8*chunks))
	order := []int{3, 2, 1, 0} // A, B, G, R
	var body bytes.Buffer
	offsets := make([]uint64, chunks)
	for c := range chunks {
		y0 := c * linesPer
		lines := min(linesPer, height-y0)
		raw := make([]byte, 0, lines*width*16)
		for y := y0; y < y0+lines; y++ {
			row := y * src.Stride
			for _, ch := range order {
				for x := range width {
					raw = binary.LittleEndian.AppendUint32(raw, math.Float32bits(src.Pix[row+x*4+ch]))
				}
			}
		}
		block := exrCompressZIP(raw)
		if len(block) >= len(raw) {
			block = raw
		}
		offsets[c] = uint64(tableAt + 8*chunks + body.Len())
		binary.Write(&body, binary.LittleEndian, int32(y0))
		binary.Write(&body, binary.LittleEndian, int32(len(block)))
		body.Write(block)
	}
	out := hdr.Bytes()
	for i, o := range offsets {
		binary.LittleEndian.PutUint64(out[tableAt+8*i:], o)
	}
	if _, err := w.Write(out); err != nil {
		return err
	}
	_, err := w.Write(body.Bytes())
	return err
}

func exrCompressZIP(raw []byte) []byte {
	half := (len(raw) + 1) / 2
	t := make([]byte, len(raw))
	for i, b := range raw {
		if i%2 == 0 {
			t[i/2] = b
		} else {
			t[half+i/2] = b
		}
	}
	for i := len(t) - 1; i > 0; i-- {
		t[i] = t[i] - t[i-1] + 128
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(t)
	zw.Close()
	return buf.Bytes()
}

func halfToFloat(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	mant := uint32(h) & 0x3ff
	switch {
	case exp == 0 && mant == 0:
		return math.Float32frombits(sign)
	case exp == 0:
		// Subnormal: normalize.
		for mant&0x400 == 0 {
			mant <<= 1
			exp--
		}
		exp++
		mant &= 0x3ff
	case exp == 31:
		return math.Float32frombits(sign | 0xff<<23 | mant<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | mant<<13)
}
