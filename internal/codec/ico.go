package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"

	"golang.org/x/image/draw"
)

// DecodeICO reads a Windows icon and returns its largest image, which may
// be stored as PNG or as a bitmap with an AND mask.
func DecodeICO(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:]) != 0 || binary.LittleEndian.Uint16(data[2:]) > 2 {
		return nil, errors.New("ico: not an icon file")
	}
	count := int(binary.LittleEndian.Uint16(data[4:]))
	if count == 0 || len(data) < 6+16*count {
		return nil, errors.New("ico: no images")
	}
	best, bestArea, bestBpp := -1, -1, -1
	for i := range count {
		e := data[6+16*i:]
		w, h := int(e[0]), int(e[1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		bpp := int(binary.LittleEndian.Uint16(e[6:]))
		if w*h > bestArea || (w*h == bestArea && bpp > bestBpp) {
			best, bestArea, bestBpp = i, w*h, bpp
		}
	}
	e := data[6+16*best:]
	size := int(binary.LittleEndian.Uint32(e[8:]))
	offset := int(binary.LittleEndian.Uint32(e[12:]))
	if offset < 0 || size < 0 || offset+size > len(data) {
		return nil, errors.New("ico: entry out of range")
	}
	payload := data[offset : offset+size]
	if bytes.HasPrefix(payload, []byte("\x89PNG")) {
		return png.Decode(bytes.NewReader(payload))
	}
	return decodeDIB(payload, -1, true)
}

// EncodeICO writes an icon with one PNG image. Icons hold at most 256×256,
// so larger images are scaled down to fit.
func EncodeICO(w io.Writer, m image.Image) error {
	b := m.Bounds()
	if b.Dx() > 256 || b.Dy() > 256 {
		scale := 256 / float64(max(b.Dx(), b.Dy()))
		nw, nh := max(1, int(float64(b.Dx())*scale+0.5)), max(1, int(float64(b.Dy())*scale+0.5))
		dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), m, b, draw.Src, nil)
		m, b = dst, dst.Bounds()
	}
	var pngData bytes.Buffer
	// Icons must hold RGBA data, even when the source is RGB or 16-bit.
	if err := png.Encode(&pngData, ToNRGBA(m)); err != nil {
		return err
	}
	hdr := make([]byte, 6+16)
	binary.LittleEndian.PutUint16(hdr[2:], 1)
	binary.LittleEndian.PutUint16(hdr[4:], 1)
	hdr[6] = uint8(b.Dx() % 256) // 0 means 256
	hdr[7] = uint8(b.Dy() % 256)
	binary.LittleEndian.PutUint16(hdr[10:], 1)
	binary.LittleEndian.PutUint16(hdr[12:], 32)
	binary.LittleEndian.PutUint32(hdr[14:], uint32(pngData.Len()))
	binary.LittleEndian.PutUint32(hdr[18:], 22)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(pngData.Bytes())
	return err
}

// ToNRGBA copies m into an 8-bit, non-premultiplied image, or returns it
// when it already is one. Straight-alpha sources convert exactly; going
// through premultiplied color would lose detail in faint pixels.
func ToNRGBA(m image.Image) *image.NRGBA {
	b := m.Bounds()
	switch src := m.(type) {
	case *image.NRGBA:
		if src.Rect.Min == (image.Point{}) && src.Stride == 4*b.Dx() {
			return src
		}
	case *image.NRGBA64, *RGBAF32:
		deep := ToNRGBA64(src)
		out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		for i := range out.Pix {
			v := uint32(deep.Pix[2*i])<<8 | uint32(deep.Pix[2*i+1])
			out.Pix[i] = uint8((v*255 + 32767) / 65535)
		}
		return out
	}
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), m, b.Min, draw.Src)
	return out
}

// ToNRGBA64 copies m into a 16-bit, non-premultiplied image, exactly for
// straight-alpha sources.
func ToNRGBA64(m image.Image) *image.NRGBA64 {
	b := m.Bounds()
	out := image.NewNRGBA64(image.Rect(0, 0, b.Dx(), b.Dy()))
	switch src := m.(type) {
	case *image.NRGBA64:
		if src.Rect.Min == (image.Point{}) && src.Stride == 8*b.Dx() {
			return src
		}
		for y := range b.Dy() {
			copy(out.Pix[y*out.Stride:(y+1)*out.Stride], src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):])
		}
	case *image.NRGBA:
		for y := range b.Dy() {
			row := src.Pix[src.PixOffset(b.Min.X, b.Min.Y+y):]
			for i := range 4 * b.Dx() {
				o := y*out.Stride + 2*i
				out.Pix[o], out.Pix[o+1] = row[i], row[i]
			}
		}
	case *RGBAF32:
		for y := range b.Dy() {
			for x := range b.Dx() {
				out.SetNRGBA64(x, y, src.At(b.Min.X+x, b.Min.Y+y).(color.NRGBA64))
			}
		}
	default:
		draw.Draw(out, out.Bounds(), m, b.Min, draw.Src)
	}
	return out
}
