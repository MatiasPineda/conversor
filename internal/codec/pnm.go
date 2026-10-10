package codec

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"strconv"
)

// DecodePNM reads a Netpbm image: PBM, PGM or PPM, ASCII (P1-P3) or binary
// (P4-P6), 8 or 16 bits per sample.
func DecodePNM(r io.Reader) (image.Image, error) {
	br := bufio.NewReader(r)
	magic := make([]byte, 2)
	if _, err := io.ReadFull(br, magic); err != nil || magic[0] != 'P' || magic[1] < '1' || magic[1] > '6' {
		return nil, errors.New("pnm: not a Netpbm file")
	}
	kind := magic[1] - '0'
	width, err := pnmInt(br)
	if err != nil {
		return nil, err
	}
	height, err := pnmInt(br)
	if err != nil {
		return nil, err
	}
	maxval := 1
	if kind != 1 && kind != 4 {
		if maxval, err = pnmInt(br); err != nil {
			return nil, err
		}
	}
	if width <= 0 || height <= 0 || maxval <= 0 || maxval > 65535 || width > 1<<16 || height > 1<<16 {
		return nil, fmt.Errorf("pnm: bad header %dx%d max %d", width, height, maxval)
	}
	if kind >= 4 {
		// Exactly one whitespace byte separates the header from the raster.
		if _, err := br.ReadByte(); err != nil {
			return nil, err
		}
	}
	channels := 1
	if kind == 3 || kind == 6 {
		channels = 3
	}
	deep := maxval > 255
	sample := func() (int, error) {
		if kind <= 3 {
			return pnmInt(br)
		}
		if deep {
			hi, err := br.ReadByte()
			if err != nil {
				return 0, err
			}
			lo, err := br.ReadByte()
			return int(hi)<<8 | int(lo), err
		}
		b, err := br.ReadByte()
		return int(b), err
	}
	scale := func(v int) uint16 { return uint16(min(v, maxval) * 65535 / maxval) }

	if kind == 1 || kind == 4 {
		img := image.NewGray(image.Rect(0, 0, width, height))
		for y := range height {
			if kind == 4 {
				line := make([]byte, (width+7)/8)
				if _, err := io.ReadFull(br, line); err != nil {
					return nil, err
				}
				for x := range width {
					if line[x/8]>>(7-x%8)&1 == 0 {
						img.Pix[y*img.Stride+x] = 255
					}
				}
				continue
			}
			for x := range width {
				v, err := pbmBit(br)
				if err != nil {
					return nil, err
				}
				if v == 0 {
					img.Pix[y*img.Stride+x] = 255
				}
			}
		}
		return img, nil
	}
	if channels == 1 {
		if deep {
			img := image.NewGray16(image.Rect(0, 0, width, height))
			for i := range width * height {
				v, err := sample()
				if err != nil {
					return nil, err
				}
				img.SetGray16(i%width, i/width, color.Gray16{scale(v)})
			}
			return img, nil
		}
		img := image.NewGray(image.Rect(0, 0, width, height))
		for i := range width * height {
			v, err := sample()
			if err != nil {
				return nil, err
			}
			img.Pix[i/width*img.Stride+i%width] = uint8(scale(v) >> 8)
		}
		return img, nil
	}
	if deep {
		img := image.NewNRGBA64(image.Rect(0, 0, width, height))
		for i := range width * height {
			var c [3]uint16
			for k := range 3 {
				v, err := sample()
				if err != nil {
					return nil, err
				}
				c[k] = scale(v)
			}
			img.SetNRGBA64(i%width, i/width, color.NRGBA64{c[0], c[1], c[2], 0xffff})
		}
		return img, nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := range width * height {
		o := i * 4
		for k := range 3 {
			v, err := sample()
			if err != nil {
				return nil, err
			}
			img.Pix[o+k] = uint8(scale(v) >> 8)
		}
		img.Pix[o+3] = 255
	}
	return img, nil
}

// pnmInt reads the next ASCII number, skipping whitespace and # comments.
func pnmInt(br *bufio.Reader) (int, error) {
	var digits []byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			if len(digits) > 0 && err == io.EOF {
				break
			}
			return 0, err
		}
		switch {
		case b == '#' && len(digits) == 0:
			if _, err := br.ReadString('\n'); err != nil {
				return 0, err
			}
		case b >= '0' && b <= '9':
			digits = append(digits, b)
		case len(digits) > 0:
			br.UnreadByte()
			return strconv.Atoi(string(digits))
		case b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f':
		default:
			return 0, fmt.Errorf("pnm: unexpected byte %q", b)
		}
	}
	return strconv.Atoi(string(digits))
}

// pbmBit reads one ASCII PBM bit; bits need no separating whitespace.
func pbmBit(br *bufio.Reader) (int, error) {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		switch b {
		case '0', '1':
			return int(b - '0'), nil
		case '#':
			if _, err := br.ReadString('\n'); err != nil {
				return 0, err
			}
		}
	}
}

// EncodePPM writes a binary 8-bit PPM (P6). PPM has no alpha; callers
// flatten transparency first.
func EncodePPM(w io.Writer, m image.Image) error {
	b := m.Bounds()
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "P6\n%d %d\n255\n", b.Dx(), b.Dy())
	src := ToNRGBA(m)
	for y := range b.Dy() {
		for x := range b.Dx() {
			c := src.NRGBAAt(x, y)
			bw.Write([]byte{c.R, c.G, c.B})
		}
	}
	return bw.Flush()
}
