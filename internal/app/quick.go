package app

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

// The rules behind the Quick convert window: which controls a target
// shows and what they set. They live here, apart from the widgets, so they
// can be tested.

// QualitySegment is one choice of the Quality control.
type QualitySegment struct {
	Key, Label string
	// Quality is the option it sets; nil (Balanced) leaves it to the engine.
	Quality *int
}

// QualitySegments are Smaller, Balanced and Best.
func QualitySegments() []QualitySegment {
	return []QualitySegment{
		{"smaller", i18n.T("Smaller"), core.Ptr(60)},
		{"balanced", i18n.T("Balanced"), nil},
		{"best", i18n.T("Best"), core.Ptr(95)},
	}
}

// QualityKey is the segment for a quality value, or "preset" for a value a
// preset set that no segment matches.
func QualityKey(q *int) string {
	for _, s := range QualitySegments() {
		if (s.Quality == nil && q == nil) || (s.Quality != nil && q != nil && *s.Quality == *q) {
			return s.Key
		}
	}
	return "preset"
}

// QualityApplies reports whether the Quality control does anything for to.
func QualityApplies(to *core.Format) bool {
	return to.Category == core.Video || slices.Contains([]string{"jpeg", "avif", "webp", "heic"}, to.ID)
}

// SizeChoice is one choice of the size control; Value nil is Original.
type SizeChoice struct {
	Value *int
	Label string
}

// SizeChoices is the size control for to: its label and choices. ok is
// false when the target has no size control.
func SizeChoices(to *core.Format) (label string, choices []SizeChoice, ok bool) {
	switch {
	case to.Category == core.Video:
		return i18n.T("Resolution"), []SizeChoice{{nil, i18n.T("Original")}, {core.Ptr(2160), "2160p"}, {core.Ptr(1080), "1080p"}, {core.Ptr(720), "720p"}, {core.Ptr(480), "480p"}}, true
	case to.Category == core.Image && to.ID != "ico":
		return i18n.T("Size"), []SizeChoice{{nil, i18n.T("Original")}, {core.Ptr(4096), "4096 px"}, {core.Ptr(2048), "2048 px"}, {core.Ptr(1024), "1024 px"}, {core.Ptr(512), "512 px"}}, true
	case to.Category == core.Audio && to.ID != "wav" && to.ID != "flac":
		return i18n.T("Bitrate"), []SizeChoice{{nil, i18n.T("Original")}, {core.Ptr(320), "320 kbit/s"}, {core.Ptr(192), "192 kbit/s"}, {core.Ptr(128), "128 kbit/s"}}, true
	}
	return "", nil, false
}

// SizeOption is the option the size control sets for to.
func SizeOption(o *core.Options, to *core.Format) **int {
	switch to.Category {
	case core.Video:
		return &o.VideoHeight
	case core.Audio:
		return &o.AudioBitrate
	}
	return &o.MaxSize
}

// CodecApplies reports whether the Codec control does anything for to.
func CodecApplies(to *core.Format) bool {
	return to.ID == "mp4" || to.ID == "mov" || to.ID == "mkv"
}

// AudioApplies reports whether to is video that can carry audio.
func AudioApplies(to *core.Format) bool {
	return to.Category == core.Video && to.ID != "gif"
}

// BackgroundApplies reports whether the Background control applies: image
// targets, except GIF from video, which takes no background color.
func BackgroundApplies(to *core.Format, files []string) bool {
	if to.Category != core.Image {
		return false
	}
	if to.ID == "gif" {
		for _, f := range files {
			if from := core.ByPath(f); from != nil && from.Category == core.Video {
				return false
			}
		}
	}
	return true
}

// DefaultBackground is what the Background control shows with nothing
// picked: what the engines do, or nil when the files would differ. Formats
// without transparency get white, and so does anything rendered from PDF,
// like a PDF viewer shows it.
func DefaultBackground(r *core.Registry, to *core.Format, files []string) *core.Background {
	var first *core.Background
	for _, f := range files {
		from := core.ByPath(f)
		if from == nil {
			continue
		}
		bg := core.Transparent
		viaPDF := from.ID == "pdf"
		if plan, err := r.Plan(from, to); err == nil {
			for _, h := range plan {
				viaPDF = viaPDF || h.Step.From.ID == "pdf"
			}
		}
		if !to.KeepsTransparency() || viaPDF {
			bg = core.White
		}
		if first == nil {
			first = &bg
		} else if *first != bg {
			return nil
		}
	}
	return first
}

// ShownBackground is the background to convert with: Transparent picked
// for a format that keeps it, then a switch to one that can't (JPEG), falls
// back to the default white instead of failing.
func ShownBackground(to *core.Format, picked *core.Background) *core.Background {
	if picked != nil && picked.Transparent && !to.KeepsTransparency() {
		return nil
	}
	return picked
}

// QuickChoices are the Quick convert controls.
type QuickChoices struct {
	Quality    *int
	Size       *int
	VideoCodec *core.VideoCodec
	StripAudio bool
	Background *core.Background
}

// ConversionOptions are the preset's options replaced by every control the
// target shows. Balanced and Original clear what a preset set.
func ConversionOptions(preset core.Options, to *core.Format, c QuickChoices, files []string) core.Options {
	o := preset
	if QualityApplies(to) {
		o.Quality = c.Quality
	}
	if _, _, ok := SizeChoices(to); ok {
		*SizeOption(&o, to) = c.Size
	}
	if CodecApplies(to) {
		o.VideoCodec = c.VideoCodec
	}
	if AudioApplies(to) {
		o.StripAudio = c.StripAudio
	}
	// Where the control is hidden (video to GIF), a preset's color would
	// only make the conversion fail, with nothing in the window to clear it.
	if BackgroundApplies(to, files) {
		o.Background = ShownBackground(to, c.Background)
	} else {
		o.Background = nil
	}
	return o
}

// OutputName checks a typed file name and adds the extension if missing.
func OutputName(typed, extension string) (string, string) {
	name := strings.TrimSpace(typed)
	if name == "" {
		return "", i18n.T("Type a name for the file.")
	}
	forbidden := "/\\\x00"
	if runtime.GOOS == "windows" {
		forbidden += `:<>"|?*`
	}
	for _, c := range name {
		switch {
		case c == '/' || c == '\\':
			return "", i18n.T("Use a file name, not a folder path.")
		case unicode.IsControl(c):
			return "", i18n.T("The name has a character files can't use.")
		case strings.ContainsRune(forbidden, c):
			return "", i18n.T("File names can't contain “%c”.", c)
		}
	}
	if strings.Trim(name, ".") == "" {
		return "", i18n.T("That isn't a file name.")
	}
	if !strings.EqualFold(strings.TrimPrefix(filepath.Ext(name), "."), extension) {
		name += "." + extension
	}
	return name, ""
}

// DefaultName is the output name for one file and a target.
func DefaultName(file string, to *core.Format) string {
	base := filepath.Base(file)
	return strings.TrimSuffix(base, filepath.Ext(base)) + "." + to.Extension()
}

// Blurb is a few words on what a target is good for.
func Blurb(to *core.Format, videoInput bool) string {
	if videoInput && to.Category == core.Audio {
		return i18n.T("Audio only")
	}
	switch to.ID {
	case "mp4":
		return i18n.T("Most players")
	case "webm":
		return i18n.T("For the web")
	case "gif":
		if videoInput {
			return i18n.T("Short loops")
		}
		return i18n.T("Simple graphics")
	case "mkv":
		return i18n.T("Keeps tracks")
	case "mov":
		return i18n.T("For editing")
	case "avi":
		return i18n.T("Older players")
	case "mp3":
		return i18n.T("Any player")
	case "wav", "bmp":
		return i18n.T("Uncompressed")
	case "flac", "png":
		return i18n.T("Lossless")
	case "aac", "m4a":
		return i18n.T("Small, clear")
	case "jpeg":
		return i18n.T("Photos")
	case "webp":
		return i18n.T("Small, sharp")
	case "avif":
		return i18n.T("Smallest")
	case "heic":
		return i18n.T("Apple photos")
	case "tiff":
		return i18n.T("Print, archive")
	case "ico":
		return i18n.T("App icons")
	case "exr":
		return i18n.T("HDR")
	case "tga":
		return i18n.T("Textures")
	case "ppm":
		return i18n.T("Raw pixels")
	case "qoi":
		return i18n.T("Fast, lossless")
	case "pdf":
		return i18n.T("Print, share")
	case "docx", "doc", "odt":
		return i18n.T("Editable text")
	case "txt":
		return i18n.T("Just the words")
	case "html":
		return i18n.T("Web page")
	case "xlsx", "xls", "ods":
		return i18n.T("Spreadsheet")
	case "csv":
		return i18n.T("Plain data")
	case "pptx", "ppt", "odp":
		return i18n.T("Slides")
	case "rtf":
		return i18n.T("Any word processor")
	}
	return to.Category.Label()
}
