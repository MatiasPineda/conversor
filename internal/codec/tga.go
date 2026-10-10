package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
)

// DecodeTGA reads a Truevision TGA image: color-mapped, true-color or
// grayscale, uncompressed or RLE, with any origin.
func DecodeTGA(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < 18 {
		return nil, errors.New("tga: truncated header")
	}
	idLen := int(data[0])
	mapType := data[1]
	imgType := data[2]
	mapFirst := int(binary.LittleEndian.Uint16(data[3:]))
	mapLen := int(binary.LittleEndian.Uint16(data[5:]))
	mapBits := int(data[7])
	width := int(binary.LittleEndian.Uint16(data[12:]))
	height := int(binary.LittleEndian.Uint16(data[14:]))
	bpp := int(data[16])
	desc := data[17]
	alphaBits := int(desc & 15)
	rightToLeft := desc&0x10 != 0
	topToBottom := desc&0x20 != 0

	rle := imgType >= 9
	base := imgType & 7
	if base != 1 && base != 2 && base != 3 {
		return nil, fmt.Errorf("tga: unsupported image type %d", imgType)
	}
	if width == 0 || height == 0 {
		return nil, errors.New("tga: empty image")
	}
	pos := 18 + idLen

	// Color map.
	var palette []color.NRGBA
	if mapType == 1 {
		entry := (mapBits + 7) / 8
		if pos+mapLen*entry > len(data) {
			return nil, errors.New("tga: truncated color map")
		}
		for i := range mapLen {
			palette = append(palette, tgaPixel(data[pos+i*entry:], mapBits, mapBits == 32))
		}
		pos += mapLen * entry
	}

	bytesPer := (bpp + 7) / 8
	if bytesPer == 0 || bytesPer > 4 {
		return nil, fmt.Errorf("tga: unsupported %d bits per pixel", bpp)
	}
	n := width * height
	raw := make([]byte, 0, n*bytesPer)
	if !rle {
		if pos+n*bytesPer > len(data) {
			return nil, errors.New("tga: truncated pixels")
		}
		raw = data[pos : pos+n*bytesPer]
	} else {
		for len(raw) < n*bytesPer {
			if pos >= len(data) {
				return nil, errors.New("tga: truncated RLE data")
			}
			h := data[pos]
			pos++
			count := int(h&0x7f) + 1
			if h&0x80 != 0 {
				if pos+bytesPer > len(data) {
					return nil, errors.New("tga: truncated RLE data")
				}
				for range count {
					raw = append(raw, data[pos:pos+bytesPer]...)
				}
				pos += bytesPer
			} else {
				if pos+count*bytesPer > len(data) {
					return nil, errors.New("tga: truncated RLE data")
				}
				raw = append(raw, data[pos:pos+count*bytesPer]...)
				pos += count * bytesPer
			}
		}
		raw = raw[:n*bytesPer]
	}

	// Treat a 32-bit image whose alpha is entirely zero as opaque: many
	// writers leave it unset.
	useAlpha := bpp == 32 || (bpp == 16 && alphaBits > 0)
	if useAlpha && base == 2 {
		zero := true
		for i := bytesPer - 1; i < len(raw); i += bytesPer {
			if bpp == 32 && raw[i] != 0 || bpp == 16 && raw[i]&0x80 != 0 {
				zero = false
				break
			}
		}
		if zero {
			useAlpha = false
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := range n {
		x, y := i%width, i/width
		if rightToLeft {
			x = width - 1 - x
		}
		if !topToBottom {
			y = height - 1 - y
		}
		p := raw[i*bytesPer:]
		var c color.NRGBA
		switch base {
		case 1:
			idx := int(p[0])
			if bytesPer == 2 {
				idx = int(binary.LittleEndian.Uint16(p))
			}
			idx -= mapFirst
			if idx >= 0 && idx < len(palette) {
				c = palette[idx]
			}
		case 2:
			c = tgaPixel(p, bpp, useAlpha)
		case 3:
			c = color.NRGBA{p[0], p[0], p[0], 255}
			if bytesPer == 2 {
				c.A = p[1]
			}
		}
		img.SetNRGBA(x, y, c)
	}
	return img, nil
}

func tgaPixel(p []byte, bits int, alpha bool) color.NRGBA {
	switch bits {
	case 15, 16:
		v := binary.LittleEndian.Uint16(p)
		c := color.NRGBA{
			R: uint8((v >> 10 & 31) * 255 / 31),
			G: uint8((v >> 5 & 31) * 255 / 31),
			B: uint8((v & 31) * 255 / 31),
			A: 255,
		}
		if alpha && bits == 16 && v&0x8000 == 0 {
			c.A = 0
		}
		return c
	case 24:
		return color.NRGBA{p[2], p[1], p[0], 255}
	case 32:
		a := p[3]
		if !alpha {
			a = 255
		}
		return color.NRGBA{p[2], p[1], p[0], a}
	}
	return color.NRGBA{A: 255}
}

// EncodeTGA writes an uncompressed, top-left origin TGA: 32-bit BGRA when
// the image has transparency, 24-bit BGR otherwise.
func EncodeTGA(w io.Writer, m image.Image) error {
	b := m.Bounds()
	if b.Dx() > 0xffff || b.Dy() > 0xffff {
		return errors.New("tga: image too large")
	}
	alpha := !IsOpaque(m)
	bpp := 24
	desc := byte(0x20)
	if alpha {
		bpp, desc = 32, 0x28
	}
	hdr := make([]byte, 18)
	hdr[2] = 2
	binary.LittleEndian.PutUint16(hdr[12:], uint16(b.Dx()))
	binary.LittleEndian.PutUint16(hdr[14:], uint16(b.Dy()))
	hdr[16] = byte(bpp)
	hdr[17] = desc
	out := make([]byte, 0, 18+b.Dx()*b.Dy()*bpp/8+26)
	out = append(out, hdr...)
	src := ToNRGBA(m)
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := src.NRGBAAt(x, y)
			out = append(out, c.B, c.G, c.R)
			if alpha {
				out = append(out, c.A)
			}
		}
	}
	// TGA 2.0 footer: no extension or developer areas.
	out = append(out, make([]byte, 8)...)
	out = append(out, "TRUEVISION-XFILE.\x00"...)
	_, err := w.Write(out)
	return err
}
