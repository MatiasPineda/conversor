package engines

import (
	"bytes"
	"encoding/binary"
	"image"

	"conversor/internal/codec"
)

// Display orientation for still images. Viewers show pixels after the EXIF
// orientation tag, and the tag is then lost: if it isn't applied, a WebP
// (which doesn't keep EXIF the way it's encoded here) comes out on its
// side. Decoding applies the tag and output never carries one, so a later
// hop can't rotate the picture twice.

// exifOrientation finds the EXIF orientation (1-8) in a JPEG, PNG, WebP or
// TIFF file, or 1 when there is none.
func exifOrientation(data []byte) int {
	var tiff []byte
	switch {
	case len(data) > 4 && data[0] == 0xff && data[1] == 0xd8:
		tiff = jpegExif(data)
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		tiff = pngChunk(data, "eXIf")
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		tiff = riffChunk(data, "EXIF")
	case bytes.HasPrefix(data, []byte("II*\x00")) || bytes.HasPrefix(data, []byte("MM\x00*")):
		tiff = data
	}
	tiff = bytes.TrimPrefix(tiff, []byte("Exif\x00\x00"))
	if o := tiffOrientation(tiff); o >= 1 && o <= 8 {
		return o
	}
	return 1
}

func jpegExif(data []byte) []byte {
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xff {
			return nil
		}
		marker := data[i+1]
		if marker == 0xd8 || (marker >= 0xd0 && marker <= 0xd7) || marker == 0x01 {
			i += 2
			continue
		}
		if marker == 0xda || marker == 0xd9 { // start of scan, end of image
			return nil
		}
		n := int(binary.BigEndian.Uint16(data[i+2:]))
		if n < 2 || i+2+n > len(data) {
			return nil
		}
		seg := data[i+4 : i+2+n]
		if marker == 0xe1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return seg
		}
		i += 2 + n
	}
	return nil
}

func pngChunk(data []byte, name string) []byte {
	for i := 8; i+12 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[i:]))
		typ := string(data[i+4 : i+8])
		if n < 0 || i+12+n > len(data) {
			return nil
		}
		if typ == name {
			return data[i+8 : i+8+n]
		}
		if typ == "IDAT" || typ == "IEND" {
			// eXIf may also follow IDAT; keep looking but stop at the end.
			if typ == "IEND" {
				return nil
			}
		}
		i += 12 + n
	}
	return nil
}

func riffChunk(data []byte, name string) []byte {
	for i := 12; i+8 <= len(data); {
		n := int(binary.LittleEndian.Uint32(data[i+4:]))
		if n < 0 || i+8+n > len(data) {
			return nil
		}
		if string(data[i:i+4]) == name {
			return data[i+8 : i+8+n]
		}
		i += 8 + n + n%2
	}
	return nil
}

// tiffOrientation reads tag 0x0112 from IFD0 of a TIFF structure.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 0
	}
	count := int(bo.Uint16(t[ifd:]))
	for k := range count {
		e := ifd + 2 + 12*k
		if e+12 > len(t) {
			return 0
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			return int(bo.Uint16(t[e+8:]))
		}
	}
	return 0
}

// orient applies EXIF orientation o to an image of one of the working
// types (NRGBA, NRGBA64 or RGBAF32).
func orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	swap := o >= 5
	ow, oh := w, h
	if swap {
		ow, oh = h, w
	}
	// src maps an output pixel to the stored pixel it shows.
	src := func(x, y int) (int, int) {
		switch o {
		case 2: // mirrored horizontally
			return w - 1 - x, y
		case 3: // rotated 180°
			return w - 1 - x, h - 1 - y
		case 4: // mirrored vertically
			return x, h - 1 - y
		case 5: // transposed
			return y, x
		case 6: // rotated 90° clockwise to display
			return y, h - 1 - x
		case 7: // transversed
			return w - 1 - y, h - 1 - x
		case 8: // rotated 90° counter-clockwise to display
			return w - 1 - y, x
		}
		return x, y
	}
	switch m := img.(type) {
	case *image.NRGBA:
		out := image.NewNRGBA(image.Rect(0, 0, ow, oh))
		for y := range oh {
			for x := range ow {
				sx, sy := src(x, y)
				copy(out.Pix[out.PixOffset(x, y):][:4], m.Pix[m.PixOffset(b.Min.X+sx, b.Min.Y+sy):][:4])
			}
		}
		return out
	case *image.NRGBA64:
		out := image.NewNRGBA64(image.Rect(0, 0, ow, oh))
		for y := range oh {
			for x := range ow {
				sx, sy := src(x, y)
				copy(out.Pix[out.PixOffset(x, y):][:8], m.Pix[m.PixOffset(b.Min.X+sx, b.Min.Y+sy):][:8])
			}
		}
		return out
	case *codec.RGBAF32:
		out := codec.NewRGBAF32(image.Rect(0, 0, ow, oh))
		for y := range oh {
			for x := range ow {
				sx, sy := src(x, y)
				copy(out.Pix[out.PixOffset(x, y):][:4], m.Pix[m.PixOffset(b.Min.X+sx, b.Min.Y+sy):][:4])
			}
		}
		return out
	}
	return orient(codec.ToNRGBA(img), o)
}
