// Package core knows which formats exist, which engines can convert between
// them, and how to chain engines when no single one covers a conversion. It
// has no native dependencies; the engines live in package engines.
package core

import (
	"path/filepath"
	"slices"
	"strings"

	"conversor/internal/i18n"
)

// Category groups formats by what they hold.
type Category int

const (
	Image Category = iota
	Vector
	Video
	Audio
	PDF
	Document
	Presentation
	Spreadsheet
)

// String is the lowercase id used in JSON output.
func (c Category) String() string {
	return [...]string{"image", "vector", "video", "audio", "pdf", "document", "presentation", "spreadsheet"}[c]
}

// Label is the name people see.
func (c Category) Label() string {
	switch c {
	case Image:
		return i18n.T("Image")
	case Vector:
		return i18n.T("Vector")
	case Video:
		return i18n.T("Video")
	case Audio:
		return i18n.T("Audio")
	case PDF:
		return "PDF"
	case Document:
		return i18n.T("Document")
	case Presentation:
		return i18n.T("Presentation")
	}
	return i18n.T("Spreadsheet")
}

// IsMedia reports whether files of the category play over time.
func (c Category) IsMedia() bool { return c == Video || c == Audio }

// Format is a file format Conversor knows about.
type Format struct {
	// ID is the stable identifier used by the CLI and presets, e.g. "jpeg".
	ID       string
	Name     string
	Category Category
	// Extensions without the dot. The first one is used for output files.
	Extensions []string
	MIME       string
}

// Extension is the extension output files get.
func (f *Format) Extension() string { return f.Extensions[0] }

// KeepsTransparency reports whether image output in this format can store
// transparency. Images going to a format that can't (JPEG, PPM) are
// flattened onto a background color; see Background.
func (f *Format) KeepsTransparency() bool {
	switch f.ID {
	case "png", "webp", "avif", "gif", "tiff", "bmp", "ico", "tga", "qoi", "exr", "heic":
		return true
	}
	return false
}

func format(id, name string, cat Category, exts []string, mime string) *Format {
	return &Format{ID: id, Name: name, Category: cat, Extensions: exts, MIME: mime}
}

// Formats lists every known format, grouped by category.
var Formats = []*Format{
	// Images
	format("jpeg", "JPEG", Image, []string{"jpg", "jpeg", "jfif"}, "image/jpeg"),
	format("png", "PNG", Image, []string{"png"}, "image/png"),
	format("webp", "WebP", Image, []string{"webp"}, "image/webp"),
	format("heic", "HEIC", Image, []string{"heic", "heif"}, "image/heic"),
	format("avif", "AVIF", Image, []string{"avif"}, "image/avif"),
	format("gif", "GIF", Image, []string{"gif"}, "image/gif"),
	format("tiff", "TIFF", Image, []string{"tiff", "tif"}, "image/tiff"),
	format("bmp", "BMP", Image, []string{"bmp"}, "image/bmp"),
	format("ico", "ICO", Image, []string{"ico"}, "image/x-icon"),
	format("tga", "TGA", Image, []string{"tga"}, "image/x-tga"),
	format("ppm", "PPM", Image, []string{"ppm", "pgm", "pbm", "pnm"}, "image/x-portable-anymap"),
	format("qoi", "QOI", Image, []string{"qoi"}, "image/qoi"),
	format("exr", "OpenEXR", Image, []string{"exr"}, "image/x-exr"),
	// Vector
	format("svg", "SVG", Vector, []string{"svg"}, "image/svg+xml"),
	// Video
	format("mp4", "MP4", Video, []string{"mp4", "m4v"}, "video/mp4"),
	format("mov", "MOV", Video, []string{"mov"}, "video/quicktime"),
	format("webm", "WebM", Video, []string{"webm"}, "video/webm"),
	format("mkv", "MKV", Video, []string{"mkv"}, "video/x-matroska"),
	format("avi", "AVI", Video, []string{"avi"}, "video/x-msvideo"),
	// Audio
	format("mp3", "MP3", Audio, []string{"mp3"}, "audio/mpeg"),
	format("wav", "WAV", Audio, []string{"wav"}, "audio/wav"),
	format("flac", "FLAC", Audio, []string{"flac"}, "audio/flac"),
	format("aac", "AAC", Audio, []string{"aac"}, "audio/aac"),
	format("m4a", "M4A", Audio, []string{"m4a"}, "audio/mp4"),
	format("ogg", "OGG", Audio, []string{"ogg", "oga"}, "audio/ogg"),
	format("opus", "Opus", Audio, []string{"opus"}, "audio/opus"),
	// PDF
	format("pdf", "PDF", PDF, []string{"pdf"}, "application/pdf"),
	// Office
	format("docx", "DOCX", Document, []string{"docx"}, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"),
	format("doc", "DOC", Document, []string{"doc"}, "application/msword"),
	format("odt", "ODT", Document, []string{"odt"}, "application/vnd.oasis.opendocument.text"),
	format("rtf", "RTF", Document, []string{"rtf"}, "application/rtf"),
	format("txt", "TXT", Document, []string{"txt"}, "text/plain"),
	format("html", "HTML", Document, []string{"html", "htm"}, "text/html"),
	format("pptx", "PPTX", Presentation, []string{"pptx"}, "application/vnd.openxmlformats-officedocument.presentationml.presentation"),
	format("ppt", "PPT", Presentation, []string{"ppt"}, "application/vnd.ms-powerpoint"),
	format("odp", "ODP", Presentation, []string{"odp"}, "application/vnd.oasis.opendocument.presentation"),
	format("xlsx", "XLSX", Spreadsheet, []string{"xlsx"}, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"),
	format("xls", "XLS", Spreadsheet, []string{"xls"}, "application/vnd.ms-excel"),
	format("ods", "ODS", Spreadsheet, []string{"ods"}, "application/vnd.oasis.opendocument.spreadsheet"),
	format("csv", "CSV", Spreadsheet, []string{"csv"}, "text/csv"),
}

// ByID finds a format by id or, failing that, by extension ("jpg" works).
// Case doesn't matter.
func ByID(id string) *Format {
	id = strings.ToLower(id)
	for _, f := range Formats {
		if f.ID == id {
			return f
		}
	}
	return byExtension(id)
}

// ByPath detects a file's format from its extension.
func ByPath(path string) *Format {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "" {
		return nil
	}
	return byExtension(strings.ToLower(ext))
}

func byExtension(ext string) *Format {
	for _, f := range Formats {
		if slices.Contains(f.Extensions, ext) {
			return f
		}
	}
	return nil
}

// MustID is ByID for ids known at compile time.
func MustID(id string) *Format {
	f := ByID(id)
	if f == nil {
		panic("unknown format " + id)
	}
	return f
}
