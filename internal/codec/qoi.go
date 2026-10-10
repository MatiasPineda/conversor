package codec

import (
	"bufio"
	"encoding/binary"
	"errors"
	"image"
	"io"
)

// QOI, the Quite OK Image format: https://qoiformat.org/qoi-specification.pdf

const (
	qoiOpIndex = 0x00
	qoiOpDiff  = 0x40
	qoiOpLuma  = 0x80
	qoiOpRun   = 0xc0
	qoiOpRGB   = 0xfe
	qoiOpRGBA  = 0xff
	qoiMask2   = 0xc0
)

func qoiHash(c [4]byte) int {
	return (int(c[0])*3 + int(c[1])*5 + int(c[2])*7 + int(c[3])*11) % 64
}

// DecodeQOI reads a QOI image.
func DecodeQOI(r io.Reader) (image.Image, error) {
	br := bufio.NewReader(r)
	hdr := make([]byte, 14)
	if _, err := io.ReadFull(br, hdr); err != nil || string(hdr[:4]) != "qoif" {
		return nil, errors.New("qoi: not a QOI file")
	}
	width := int(binary.BigEndian.Uint32(hdr[4:]))
	height := int(binary.BigEndian.Uint32(hdr[8:]))
	if width == 0 || height == 0 || width > 1<<16 || height > 1<<16 {
		return nil, errors.New("qoi: bad size")
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	var index [64][4]byte
	px := [4]byte{0, 0, 0, 255}
	run := 0
	for o := 0; o < len(img.Pix); o += 4 {
		if run > 0 {
			run--
		} else {
			b1, err := br.ReadByte()
			if err != nil {
				return nil, errors.New("qoi: truncated")
			}
			switch {
			case b1 == qoiOpRGB:
				var rgb [3]byte
				if _, err := io.ReadFull(br, rgb[:]); err != nil {
					return nil, errors.New("qoi: truncated")
				}
				px[0], px[1], px[2] = rgb[0], rgb[1], rgb[2]
			case b1 == qoiOpRGBA:
				if _, err := io.ReadFull(br, px[:]); err != nil {
					return nil, errors.New("qoi: truncated")
				}
			case b1&qoiMask2 == qoiOpIndex:
				px = index[b1]
			case b1&qoiMask2 == qoiOpDiff:
				px[0] += (b1>>4)&3 - 2
				px[1] += (b1>>2)&3 - 2
				px[2] += b1&3 - 2
			case b1&qoiMask2 == qoiOpLuma:
				b2, err := br.ReadByte()
				if err != nil {
					return nil, errors.New("qoi: truncated")
				}
				vg := b1&0x3f - 32
				px[0] += vg - 8 + (b2>>4)&0x0f
				px[1] += vg
				px[2] += vg - 8 + b2&0x0f
			case b1&qoiMask2 == qoiOpRun:
				run = int(b1 & 0x3f)
			}
			index[qoiHash(px)] = px
		}
		copy(img.Pix[o:o+4], px[:])
	}
	return img, nil
}

// EncodeQOI writes a QOI image, RGBA when it has transparency, else RGB.
func EncodeQOI(w io.Writer, m image.Image) error {
	src := ToNRGBA(m)
	b := src.Bounds()
	channels := byte(3)
	if !IsOpaque(src) {
		channels = 4
	}
	bw := bufio.NewWriter(w)
	hdr := make([]byte, 14)
	copy(hdr, "qoif")
	binary.BigEndian.PutUint32(hdr[4:], uint32(b.Dx()))
	binary.BigEndian.PutUint32(hdr[8:], uint32(b.Dy()))
	hdr[12] = channels
	hdr[13] = 0 // sRGB with linear alpha
	bw.Write(hdr)
	var index [64][4]byte
	prev := [4]byte{0, 0, 0, 255}
	run := 0
	n := len(src.Pix) / 4
	for i := range n {
		var px [4]byte
		copy(px[:], src.Pix[i*4:i*4+4])
		if px == prev {
			run++
			if run == 62 || i == n-1 {
				bw.WriteByte(qoiOpRun | byte(run-1))
				run = 0
			}
			continue
		}
		if run > 0 {
			bw.WriteByte(qoiOpRun | byte(run-1))
			run = 0
		}
		h := qoiHash(px)
		switch {
		case index[h] == px:
			bw.WriteByte(qoiOpIndex | byte(h))
		case px[3] == prev[3]:
			index[h] = px
			vr := int8(px[0] - prev[0])
			vg := int8(px[1] - prev[1])
			vb := int8(px[2] - prev[2])
			vgr, vgb := vr-vg, vb-vg
			switch {
			case vr > -3 && vr < 2 && vg > -3 && vg < 2 && vb > -3 && vb < 2:
				bw.WriteByte(qoiOpDiff | byte(vr+2)<<4 | byte(vg+2)<<2 | byte(vb+2))
			case vgr > -9 && vgr < 8 && vg > -33 && vg < 32 && vgb > -9 && vgb < 8:
				bw.WriteByte(qoiOpLuma | byte(vg+32))
				bw.WriteByte(byte(vgr+8)<<4 | byte(vgb+8))
			default:
				bw.Write([]byte{qoiOpRGB, px[0], px[1], px[2]})
			}
		default:
			index[h] = px
			bw.Write([]byte{qoiOpRGBA, px[0], px[1], px[2], px[3]})
		}
		prev = px
	}
	bw.Write([]byte{0, 0, 0, 0, 0, 0, 0, 1})
	return bw.Flush()
}
