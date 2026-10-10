// Package codec holds the image codecs the standard library and x/image
// lack or only half cover: BMP (all common variants, written with a V5
// header so alpha survives), ICO, TGA, PNM, QOI and OpenEXR, plus a GIF
// palette quantizer and a float image type.
package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math/bits"
)

const (
	biRGB            = 0
	biRLE8           = 1
	biRLE4           = 2
	biBitfields      = 3
	biAlphaBitfields = 6
)

// DecodeBMP reads a Windows or OS/2 bitmap: 1, 4, 8, 16, 24 and 32 bits per
// pixel, uncompressed, RLE4, RLE8 or bitfields, bottom-up or top-down.
func DecodeBMP(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < 14 || data[0] != 'B' || data[1] != 'M' {
		return nil, errors.New("bmp: not a BMP file")
	}
	offset := int(binary.LittleEndian.Uint32(data[10:]))
	img, err := decodeDIB(data[14:], offset-14, false)
	return img, err
}

// decodeDIB decodes a device-independent bitmap: an info header, optional
// masks and palette, then pixels. pixelOffset is where the pixels start
// relative to the header (negative: right after the palette). In an ICO the
// height counts the AND mask too, and the mask gives transparency.
func decodeDIB(b []byte, pixelOffset int, ico bool) (image.Image, error) {
	if len(b) < 12 {
		return nil, errors.New("bmp: truncated header")
	}
	hlen := int(binary.LittleEndian.Uint32(b))
	if hlen > len(b) || hlen < 12 {
		return nil, errors.New("bmp: bad header size")
	}
	var width, height, bpp, compression, colors int
	if hlen == 12 { // BITMAPCOREHEADER
		width = int(binary.LittleEndian.Uint16(b[4:]))
		height = int(int16(binary.LittleEndian.Uint16(b[6:])))
		bpp = int(binary.LittleEndian.Uint16(b[10:]))
	} else {
		if hlen < 40 {
			return nil, errors.New("bmp: bad header size")
		}
		width = int(int32(binary.LittleEndian.Uint32(b[4:])))
		height = int(int32(binary.LittleEndian.Uint32(b[8:])))
		bpp = int(binary.LittleEndian.Uint16(b[14:]))
		compression = int(binary.LittleEndian.Uint32(b[16:]))
		colors = int(binary.LittleEndian.Uint32(b[32:]))
	}
	topDown := height < 0
	if topDown {
		height = -height
	}
	if ico {
		height /= 2
	}
	if width <= 0 || height <= 0 || width > 1<<16 || height > 1<<16 {
		return nil, fmt.Errorf("bmp: bad size %dx%d", width, height)
	}

	// Channel masks for 16 and 32 bits.
	var masks [4]uint32
	hasAlphaMask := false
	pos := hlen
	switch bpp {
	case 16:
		masks = [4]uint32{0x7c00, 0x03e0, 0x001f, 0}
	case 32:
		masks = [4]uint32{0xff0000, 0xff00, 0xff, 0}
	}
	if compression == biBitfields || compression == biAlphaBitfields {
		n := 3
		if compression == biAlphaBitfields {
			n = 4
		}
		if hlen >= 52 {
			// V2+ headers carry the masks inside the header.
			n = 3
			if hlen >= 56 {
				n = 4
			}
			for i := range n {
				masks[i] = binary.LittleEndian.Uint32(b[40+4*i:])
			}
		} else {
			if len(b) < pos+4*n {
				return nil, errors.New("bmp: truncated masks")
			}
			for i := range n {
				masks[i] = binary.LittleEndian.Uint32(b[pos+4*i:])
			}
			pos += 4 * n
		}
		hasAlphaMask = masks[3] != 0
	} else if hlen >= 56 && bpp == 32 && compression == biRGB {
		// V3+ headers may set an alpha mask even without bitfields.
		if a := binary.LittleEndian.Uint32(b[52:]); a == 0xff000000 {
			masks[3], hasAlphaMask = a, true
		}
	}
	if bpp == 32 && compression == biRGB && !hasAlphaMask && ico {
		masks[3], hasAlphaMask = 0xff000000, true
	}

	// Palette.
	var palette color.Palette
	if bpp <= 8 {
		entry := 4
		if hlen == 12 {
			entry = 3
		}
		if colors == 0 || colors > 1<<bpp {
			colors = 1 << bpp
		}
		for i := range colors {
			at := pos + i*entry
			if at+3 > len(b) {
				break
			}
			palette = append(palette, color.NRGBA{b[at+2], b[at+1], b[at], 255})
		}
		pos += colors * entry
		for len(palette) < 1<<bpp {
			palette = append(palette, color.NRGBA{0, 0, 0, 255})
		}
	}
	if pixelOffset >= pos && pixelOffset < len(b) {
		pos = pixelOffset
	}
	if pos > len(b) {
		return nil, errors.New("bmp: truncated")
	}
	px := b[pos:]

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	row := func(y int) int {
		if topDown {
			return y
		}
		return height - 1 - y
	}
	switch {
	case compression == biRLE8 || compression == biRLE4:
		if err := decodeRLE(px, img, palette, compression == biRLE4, row); err != nil {
			return nil, err
		}
	case bpp <= 8:
		stride := (width*bpp + 31) / 32 * 4
		for y := range height {
			start := y * stride
			if start+(width*bpp+7)/8 > len(px) {
				return nil, errors.New("bmp: truncated pixels")
			}
			line := px[start:]
			for x := range width {
				bit := x * bpp
				idx := int(line[bit/8]>>(8-bpp-bit%8)) & (1<<bpp - 1)
				img.Set(x, row(y), palette[idx])
			}
		}
	case bpp == 24:
		stride := (width*3 + 3) &^ 3
		for y := range height {
			start := y * stride
			if start+width*3 > len(px) {
				return nil, errors.New("bmp: truncated pixels")
			}
			o := img.PixOffset(0, row(y))
			for x := range width {
				p := px[start+x*3:]
				img.Pix[o+x*4], img.Pix[o+x*4+1], img.Pix[o+x*4+2], img.Pix[o+x*4+3] = p[2], p[1], p[0], 255
			}
		}
	case bpp == 16 || bpp == 32:
		bytesPer := bpp / 8
		stride := (width*bytesPer + 3) &^ 3
		allZeroAlpha := hasAlphaMask
		for y := range height {
			start := y * stride
			if start+width*bytesPer > len(px) {
				return nil, errors.New("bmp: truncated pixels")
			}
			o := img.PixOffset(0, row(y))
			for x := range width {
				var v uint32
				if bpp == 16 {
					v = uint32(binary.LittleEndian.Uint16(px[start+x*2:]))
				} else {
					v = binary.LittleEndian.Uint32(px[start+x*4:])
				}
				for c := range 3 {
					img.Pix[o+x*4+c] = maskValue(v, masks[c])
				}
				a := uint8(255)
				if hasAlphaMask {
					a = maskValue(v, masks[3])
					if a != 0 {
						allZeroAlpha = false
					}
				}
				img.Pix[o+x*4+3] = a
			}
		}
		// Many writers leave the alpha byte zero; that means opaque.
		if allZeroAlpha {
			for i := 3; i < len(img.Pix); i += 4 {
				img.Pix[i] = 255
			}
		}
	default:
		return nil, fmt.Errorf("bmp: unsupported %d bits per pixel", bpp)
	}

	if ico && !(bpp == 32 && hasAlphaMask) {
		// The AND mask follows the color pixels: 1 bit per pixel, 1 is transparent.
		colorStride := (width*bpp + 31) / 32 * 4
		maskStart := colorStride * height
		maskStride := (width + 31) / 32 * 4
		if maskStart+maskStride*height <= len(px) {
			for y := range height {
				line := px[maskStart+y*maskStride:]
				for x := range width {
					if line[x/8]>>(7-x%8)&1 == 1 {
						img.Pix[img.PixOffset(x, row(y))+3] = 0
					}
				}
			}
		}
	}
	return img, nil
}

// maskValue extracts the channel under mask from v, scaled to 8 bits.
func maskValue(v, mask uint32) uint8 {
	if mask == 0 {
		return 0
	}
	shift := bits.TrailingZeros32(mask)
	width := bits.OnesCount32(mask)
	c := (v & mask) >> shift
	if width >= 8 {
		return uint8(c >> (width - 8))
	}
	maxv := uint32(1)<<width - 1
	return uint8((c*255 + maxv/2) / maxv)
}

func decodeRLE(px []byte, img *image.NRGBA, palette color.Palette, four bool, row func(int) int) error {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	// Pixels RLE skips over are transparent black, as Windows shows them.
	x, y := 0, 0
	set := func(idx int) {
		if x < w && y < h {
			img.Set(x, row(y), palette[idx%len(palette)])
		}
		x++
	}
	for i := 0; i+1 < len(px); {
		n, v := int(px[i]), int(px[i+1])
		i += 2
		if n > 0 {
			for k := range n {
				if four {
					if k%2 == 0 {
						set(v >> 4)
					} else {
						set(v & 15)
					}
				} else {
					set(v)
				}
			}
			continue
		}
		switch v {
		case 0: // end of line
			x, y = 0, y+1
		case 1: // end of bitmap
			return nil
		case 2: // delta
			if i+1 >= len(px) {
				return nil
			}
			x += int(px[i])
			y += int(px[i+1])
			i += 2
		default: // absolute run of v pixels
			count := v
			nbytes := count
			if four {
				nbytes = (count + 1) / 2
			}
			if i+nbytes > len(px) {
				return errors.New("bmp: truncated RLE data")
			}
			for k := range count {
				if four {
					b := px[i+k/2]
					if k%2 == 0 {
						set(int(b >> 4))
					} else {
						set(int(b & 15))
					}
				} else {
					set(int(px[i+k]))
				}
			}
			i += (nbytes + 1) &^ 1
		}
		if y >= h {
			return nil
		}
	}
	return nil
}

// EncodeBMP writes 24-bit BMP for opaque images and 32-bit BMP with a
// BITMAPV5HEADER for images with transparency. Apple's ImageIO refuses V4
// headers, so V5 is what opens everywhere with alpha intact.
func EncodeBMP(w io.Writer, m image.Image) error {
	b := m.Bounds()
	width, height := b.Dx(), b.Dy()
	alpha := !IsOpaque(m)
	var buf bytes.Buffer
	if !alpha {
		stride := (width*3 + 3) &^ 3
		size := 14 + 40 + stride*height
		writeFileHeader(&buf, size, 14+40)
		hdr := make([]byte, 40)
		binary.LittleEndian.PutUint32(hdr[0:], 40)
		binary.LittleEndian.PutUint32(hdr[4:], uint32(width))
		binary.LittleEndian.PutUint32(hdr[8:], uint32(height))
		binary.LittleEndian.PutUint16(hdr[12:], 1)
		binary.LittleEndian.PutUint16(hdr[14:], 24)
		binary.LittleEndian.PutUint32(hdr[20:], uint32(stride*height))
		binary.LittleEndian.PutUint32(hdr[24:], 2835) // 72 dpi
		binary.LittleEndian.PutUint32(hdr[28:], 2835)
		buf.Write(hdr)
		line := make([]byte, stride)
		src := ToNRGBA(m)
		for y := height - 1; y >= 0; y-- {
			for x := range width {
				c := src.NRGBAAt(x, y)
				line[x*3], line[x*3+1], line[x*3+2] = c.B, c.G, c.R
			}
			buf.Write(line)
		}
	} else {
		const v5 = 124
		stride := width * 4
		size := 14 + v5 + stride*height
		writeFileHeader(&buf, size, 14+v5)
		hdr := make([]byte, v5)
		binary.LittleEndian.PutUint32(hdr[0:], v5)
		binary.LittleEndian.PutUint32(hdr[4:], uint32(width))
		binary.LittleEndian.PutUint32(hdr[8:], uint32(height))
		binary.LittleEndian.PutUint16(hdr[12:], 1)
		binary.LittleEndian.PutUint16(hdr[14:], 32)
		binary.LittleEndian.PutUint32(hdr[16:], biBitfields)
		binary.LittleEndian.PutUint32(hdr[20:], uint32(stride*height))
		binary.LittleEndian.PutUint32(hdr[24:], 2835)
		binary.LittleEndian.PutUint32(hdr[28:], 2835)
		binary.LittleEndian.PutUint32(hdr[40:], 0x00ff0000)
		binary.LittleEndian.PutUint32(hdr[44:], 0x0000ff00)
		binary.LittleEndian.PutUint32(hdr[48:], 0x000000ff)
		binary.LittleEndian.PutUint32(hdr[52:], 0xff000000)
		copy(hdr[56:], "BGRs")                      // LCS_sRGB
		binary.LittleEndian.PutUint32(hdr[108:], 4) // LCS_GM_IMAGES
		buf.Write(hdr)
		line := make([]byte, stride)
		src := ToNRGBA(m)
		for y := height - 1; y >= 0; y-- {
			for x := range width {
				c := src.NRGBAAt(x, y)
				line[x*4], line[x*4+1], line[x*4+2], line[x*4+3] = c.B, c.G, c.R, c.A
			}
			buf.Write(line)
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func writeFileHeader(buf *bytes.Buffer, size, offset int) {
	h := make([]byte, 14)
	h[0], h[1] = 'B', 'M'
	binary.LittleEndian.PutUint32(h[2:], uint32(size))
	binary.LittleEndian.PutUint32(h[10:], uint32(offset))
	buf.Write(h)
}

// IsOpaque reports whether every pixel of m is fully opaque.
func IsOpaque(m image.Image) bool {
	if o, ok := m.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	b := m.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := m.At(x, y).RGBA(); a != 0xffff {
				return false
			}
		}
	}
	return true
}
