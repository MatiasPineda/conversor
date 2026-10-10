package codec

import (
	"image"
	"image/color"
	"sort"
)

// Quantize reduces m to at most 256 colors for GIF. Pixels less than half
// opaque become a transparent palette entry. Opaque images are dithered;
// images with transparency map each pixel to its nearest color so the
// transparent areas stay clean.
func Quantize(m image.Image) *image.Paletted {
	src := ToNRGBA(m)
	b := src.Bounds()

	// Histogram over 5 bits per channel.
	type bin struct {
		count   int
		r, g, b int
		key     int
	}
	bins := map[int]*bin{}
	transparent := false
	for i := 0; i < len(src.Pix); i += 4 {
		if src.Pix[i+3] < 128 {
			transparent = true
			continue
		}
		r, g, bl := int(src.Pix[i]), int(src.Pix[i+1]), int(src.Pix[i+2])
		key := (r>>3)<<10 | (g>>3)<<5 | bl>>3
		e := bins[key]
		if e == nil {
			e = &bin{key: key}
			bins[key] = e
		}
		e.count++
		e.r += r
		e.g += g
		e.b += bl
	}
	limit := 256
	if transparent {
		limit = 255
	}
	all := make([]*bin, 0, len(bins))
	for _, e := range bins {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].key < all[j].key })

	// Median cut: split the box with the widest channel range, weighted by
	// pixel count, until there are enough boxes.
	boxes := [][]*bin{all}
	channel := func(e *bin, c int) int {
		switch c {
		case 0:
			return e.r / e.count
		case 1:
			return e.g / e.count
		}
		return e.b / e.count
	}
	rangeOf := func(box []*bin) (int, int) {
		best, bestRange := 0, -1
		for c := range 3 {
			lo, hi := 255, 0
			for _, e := range box {
				v := channel(e, c)
				lo, hi = min(lo, v), max(hi, v)
			}
			if hi-lo > bestRange {
				best, bestRange = c, hi-lo
			}
		}
		return best, bestRange
	}
	for len(boxes) < limit {
		pick, pickScore, pickChannel := -1, 0, 0
		for i, box := range boxes {
			if len(box) < 2 {
				continue
			}
			c, r := rangeOf(box)
			n := 0
			for _, e := range box {
				n += e.count
			}
			if score := r * n; score > pickScore || pick < 0 {
				pick, pickScore, pickChannel = i, score, c
			}
		}
		if pick < 0 {
			break
		}
		box := boxes[pick]
		sort.Slice(box, func(i, j int) bool { return channel(box[i], pickChannel) < channel(box[j], pickChannel) })
		total := 0
		for _, e := range box {
			total += e.count
		}
		split, acc := 1, 0
		for i, e := range box {
			acc += e.count
			if acc*2 >= total {
				split = min(max(i+1, 1), len(box)-1)
				break
			}
		}
		boxes[pick] = box[:split]
		boxes = append(boxes, box[split:])
	}

	var palette color.Palette
	if transparent {
		palette = append(palette, color.NRGBA{})
	}
	for _, box := range boxes {
		var n, r, g, bl int
		for _, e := range box {
			n += e.count
			r += e.r
			g += e.g
			bl += e.b
		}
		if n == 0 {
			continue
		}
		palette = append(palette, color.NRGBA{uint8(r / n), uint8(g / n), uint8(bl / n), 255})
	}
	if len(palette) == 0 {
		palette = append(palette, color.NRGBA{A: 255})
	}

	out := image.NewPaletted(image.Rect(0, 0, b.Dx(), b.Dy()), palette)
	if !transparent {
		dither(out, src, palette)
		return out
	}
	opaque := palette[1:]
	cache := map[int]uint8{}
	for y := range b.Dy() {
		for x := range b.Dx() {
			o := src.PixOffset(b.Min.X+x, b.Min.Y+y)
			if src.Pix[o+3] < 128 {
				out.SetColorIndex(x, y, 0)
				continue
			}
			key := int(src.Pix[o])<<16 | int(src.Pix[o+1])<<8 | int(src.Pix[o+2])
			idx, ok := cache[key]
			if !ok {
				if len(opaque) == 0 {
					idx = 0
				} else {
					idx = uint8(opaque.Index(color.NRGBA{src.Pix[o], src.Pix[o+1], src.Pix[o+2], 255}) + 1)
				}
				cache[key] = idx
			}
			out.SetColorIndex(x, y, idx)
		}
	}
	return out
}

// nearest finds palette colors for 5-bit-per-channel keys, caching each
// answer: images have far fewer distinct keys than pixels.
type nearest struct {
	palette color.Palette
	cache   [1 << 15]int16
}

func newNearest(p color.Palette) *nearest {
	n := &nearest{palette: p}
	for i := range n.cache {
		n.cache[i] = -1
	}
	return n
}

func (n *nearest) index(r, g, b int) int {
	key := (r>>3)<<10 | (g>>3)<<5 | b>>3
	if i := n.cache[key]; i >= 0 {
		return int(i)
	}
	best, bestD := 0, 1<<62
	for i, c := range n.palette {
		pc := c.(color.NRGBA)
		dr, dg, db := int(pc.R)-r, int(pc.G)-g, int(pc.B)-b
		if d := dr*dr*3 + dg*dg*4 + db*db*2; d < bestD {
			best, bestD = i, d
		}
	}
	n.cache[key] = int16(best)
	return best
}

// dither maps an opaque image onto palette with Floyd–Steinberg error
// diffusion.
func dither(out *image.Paletted, src *image.NRGBA, palette color.Palette) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	look := newNearest(palette)
	cur := make([][3]int, w+2)
	next := make([][3]int, w+2)
	clamp := func(v int) int { return min(max(v, 0), 255) }
	for y := range h {
		row := src.Pix[y*src.Stride:]
		for x := range w {
			r := clamp(int(row[x*4]) + cur[x+1][0]/16)
			g := clamp(int(row[x*4+1]) + cur[x+1][1]/16)
			b := clamp(int(row[x*4+2]) + cur[x+1][2]/16)
			idx := look.index(r, g, b)
			out.Pix[y*out.Stride+x] = uint8(idx)
			pc := palette[idx].(color.NRGBA)
			e := [3]int{r - int(pc.R), g - int(pc.G), b - int(pc.B)}
			for k := range 3 {
				cur[x+2][k] += e[k] * 7
				next[x][k] += e[k] * 3
				next[x+1][k] += e[k] * 5
				next[x+2][k] += e[k]
			}
		}
		cur, next = next, cur
		clear(next)
	}
}
