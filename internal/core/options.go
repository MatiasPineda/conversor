package core

import (
	"fmt"
	"strconv"
	"strings"

	"conversor/internal/i18n"
)

// Options are the settings for a conversion. Every field is optional (nil
// means unset); engines ignore the ones that don't apply to their output.
type Options struct {
	// Quality is lossy quality from 1 to 100 for JPEG, WebP, AVIF, HEIC and video.
	Quality *int `toml:"quality,omitempty" json:"quality,omitempty"`
	// MaxSize is the longest edge in pixels for image output. Larger images
	// are scaled down.
	MaxSize *int `toml:"max_size,omitempty" json:"max_size,omitempty"`
	// VideoHeight is the maximum output video height in pixels. Scales down
	// only, keeps the aspect ratio, and rounds both dimensions down to even.
	VideoHeight *int `toml:"video_height,omitempty" json:"video_height,omitempty"`
	// AudioBitrate is in kbit/s.
	AudioBitrate *int `toml:"audio_bitrate,omitempty" json:"audio_bitrate,omitempty"`
	// Pages to render from a PDF, e.g. "1-3", "2" or "4-".
	Pages *PageRange `toml:"pages,omitempty" json:"pages,omitempty"`
	// DPI is the resolution for rendering PDF and SVG pages.
	DPI *int `toml:"dpi,omitempty" json:"dpi,omitempty"`
	// VideoCodec is the encoder for MP4, MOV and MKV output. Unset means H.264.
	VideoCodec *VideoCodec `toml:"video_codec,omitempty" json:"video_codec,omitempty"`
	// Background is what transparent areas of an image become. Unset keeps
	// transparency where the output format can store it and uses white
	// where it can't.
	Background *Background `toml:"background,omitempty" json:"background,omitempty"`
	// StripAudio leaves the audio out of video output.
	StripAudio bool `toml:"strip_audio,omitempty" json:"strip_audio,omitempty"`
}

// Ptr returns a pointer to v, for filling Options.
func Ptr[T any](v T) *T { return &v }

// Validate checks every set field's range.
func (o *Options) Validate() error {
	if o.Quality != nil && (*o.Quality < 1 || *o.Quality > 100) {
		return InvalidOption("%s", i18n.T("quality must be between 1 and 100"))
	}
	if (o.MaxSize != nil && *o.MaxSize < 1) || (o.VideoHeight != nil && *o.VideoHeight < 1) {
		return InvalidOption("%s", i18n.T("sizes must be at least 1 pixel"))
	}
	if o.VideoHeight != nil && *o.VideoHeight%2 == 1 {
		return InvalidOption("%s", i18n.T("video height must be even"))
	}
	if o.AudioBitrate != nil && (*o.AudioBitrate < 8 || *o.AudioBitrate > 640) {
		return InvalidOption("%s", i18n.T("audio bitrate must be between 8 and 640 kbit/s"))
	}
	if o.DPI != nil && (*o.DPI < 18 || *o.DPI > 1200) {
		return InvalidOption("%s", i18n.T("dpi must be between 18 and 1200"))
	}
	return nil
}

// Or fills every unset field from base.
func (o Options) Or(base Options) Options {
	or := func(a, b *int) *int {
		if a != nil {
			return a
		}
		return b
	}
	if o.Pages == nil {
		o.Pages = base.Pages
	}
	if o.VideoCodec == nil {
		o.VideoCodec = base.VideoCodec
	}
	if o.Background == nil {
		o.Background = base.Background
	}
	o.Quality = or(o.Quality, base.Quality)
	o.MaxSize = or(o.MaxSize, base.MaxSize)
	o.VideoHeight = or(o.VideoHeight, base.VideoHeight)
	o.AudioBitrate = or(o.AudioBitrate, base.AudioBitrate)
	o.DPI = or(o.DPI, base.DPI)
	o.StripAudio = o.StripAudio || base.StripAudio
	return o
}

// Get returns *p, or def when p is nil.
func Get[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// VideoCodec is a video encoder the user can pick for MP4, MOV and MKV.
type VideoCodec int

const (
	// H264 (AVC) plays nearly everywhere.
	H264 VideoCodec = iota
	// HEVC (H.265) makes smaller files for newer players.
	HEVC
)

// VideoCodecs lists every codec choice.
var VideoCodecs = []VideoCodec{H264, HEVC}

// ID is what presets and the CLI use: h264 or hevc.
func (c VideoCodec) ID() string { return [...]string{"h264", "hevc"}[c] }

// Name is the name people know it by.
func (c VideoCodec) Name() string { return [...]string{"H.264", "HEVC"}[c] }

func (c VideoCodec) String() string { return c.ID() }

// ParseVideoCodec reads h264, h.264, avc, hevc, h265 or h.265.
func ParseVideoCodec(s string) (VideoCodec, error) {
	switch strings.ToLower(s) {
	case "h264", "h.264", "avc":
		return H264, nil
	case "hevc", "h265", "h.265":
		return HEVC, nil
	}
	return 0, InvalidOption("%s", i18n.T("video codec %q; use h264 or hevc", s))
}

func (c VideoCodec) MarshalText() ([]byte, error) { return []byte(c.ID()), nil }

func (c *VideoCodec) UnmarshalText(b []byte) error {
	v, err := ParseVideoCodec(string(b))
	*c = v
	return err
}

// Background is what transparent areas of an image become: kept
// transparent, or flattened onto a solid color. Written transparent, white,
// black or #rrggbb.
type Background struct {
	Transparent bool
	// Color is the RGB color when not Transparent.
	Color [3]uint8
}

var (
	// White is the background formats without transparency get by default.
	White = Background{Color: [3]uint8{255, 255, 255}}
	Black = Background{Color: [3]uint8{0, 0, 0}}
	// Transparent keeps transparency.
	Transparent = Background{Transparent: true}
	// BackgroundChoices are what the app offers; any color works from the
	// CLI and presets.
	BackgroundChoices = []Background{Transparent, White, Black}
)

// ID is what presets and the CLI use: transparent, white, black or #rrggbb.
func (b Background) ID() string {
	switch {
	case b.Transparent:
		return "transparent"
	case b == White:
		return "white"
	case b == Black:
		return "black"
	}
	return fmt.Sprintf("#%02x%02x%02x", b.Color[0], b.Color[1], b.Color[2])
}

// Name is what people see: Transparent, White, Black or #RRGGBB.
func (b Background) Name() string {
	switch {
	case b.Transparent:
		return i18n.T("Transparent")
	case b == White:
		return i18n.T("White")
	case b == Black:
		return i18n.T("Black")
	}
	return strings.ToUpper(b.ID())
}

func (b Background) String() string { return b.ID() }

// ParseBackground reads transparent, white, black, #rgb or #rrggbb.
func ParseBackground(s string) (Background, error) {
	bad := InvalidOption("%s", i18n.T("background %q; use transparent, white, black or a color like #ff8800", s))
	lower := strings.ToLower(strings.TrimSpace(s))
	switch lower {
	case "transparent":
		return Transparent, nil
	case "white":
		return White, nil
	case "black":
		return Black, nil
	}
	hex := strings.TrimPrefix(lower, "#")
	var digits []uint8
	for _, c := range hex {
		d, err := strconv.ParseUint(string(c), 16, 8)
		if err != nil {
			return Background{}, bad
		}
		digits = append(digits, uint8(d))
	}
	switch len(digits) {
	case 3:
		return Background{Color: [3]uint8{digits[0] * 17, digits[1] * 17, digits[2] * 17}}, nil
	case 6:
		return Background{Color: [3]uint8{digits[0]*16 + digits[1], digits[2]*16 + digits[3], digits[4]*16 + digits[5]}}, nil
	}
	return Background{}, bad
}

func (b Background) MarshalText() ([]byte, error) { return []byte(b.ID()), nil }

func (b *Background) UnmarshalText(text []byte) error {
	v, err := ParseBackground(string(text))
	*b = v
	return err
}

// PageRange is an inclusive, 1-based page range. Last == 0 runs to the end.
type PageRange struct {
	First int
	Last  int
}

// Contains reports whether 0-based page index is in the range.
func (r PageRange) Contains(index int) bool {
	page := index + 1
	return page >= r.First && (r.Last == 0 || page <= r.Last)
}

func (r PageRange) String() string {
	switch {
	case r.Last == r.First:
		return strconv.Itoa(r.First)
	case r.Last == 0:
		return fmt.Sprintf("%d-", r.First)
	}
	return fmt.Sprintf("%d-%d", r.First, r.Last)
}

// ParsePageRange reads 2, 1-3 or 4-.
func ParsePageRange(s string) (PageRange, error) {
	bad := InvalidOption("%s", i18n.T("page range %q; use 2, 1-3 or 4-", s))
	num := func(t string) (int, bool) {
		n, err := strconv.Atoi(strings.TrimSpace(t))
		return n, err == nil && n > 0
	}
	var r PageRange
	a, b, ranged := strings.Cut(s, "-")
	first, ok := num(a)
	if !ok {
		return r, bad
	}
	r.First = first
	switch {
	case !ranged:
		r.Last = first
	case strings.TrimSpace(b) == "":
		r.Last = 0
	default:
		last, ok := num(b)
		if !ok || last < first {
			return r, bad
		}
		r.Last = last
	}
	return r, nil
}

func (r PageRange) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

func (r *PageRange) UnmarshalText(b []byte) error {
	v, err := ParsePageRange(string(b))
	*r = v
	return err
}
