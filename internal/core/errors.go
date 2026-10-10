package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"conversor/internal/i18n"
)

// ErrCancelled is returned by jobs that were stopped.
var ErrCancelled = errors.New("cancelled")

// Error kinds, stable names used in JSON output.
const (
	KindUnsupportedInput = "unsupported_input"
	KindNoRoute          = "no_route"
	KindInvalidOption    = "invalid_option"
	KindEngineMissing    = "engine_missing"
	KindEngineFailed     = "engine_failed"
	KindOutputExists     = "output_exists"
	KindCancelled        = "cancelled"
	KindIO               = "io"
)

// Error is a conversion failure with a machine-readable kind.
type Error struct {
	Kind    string
	Engine  string
	Message string
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindEngineFailed:
		return i18n.T("%s failed: %s", e.Engine, e.Message)
	case KindEngineMissing:
		return i18n.T("%s is not available: %s", e.Engine, e.Message)
	case KindInvalidOption:
		return i18n.T("invalid option: %s", e.Message)
	}
	return e.Message
}

// InvalidOption is the error for a bad setting.
func InvalidOption(format string, args ...any) error {
	return &Error{Kind: KindInvalidOption, Message: fmt.Sprintf(format, args...)}
}

// EngineFailed is the error for a tool or library that could not convert.
func EngineFailed(engine string, format string, args ...any) error {
	return &Error{Kind: KindEngineFailed, Engine: engine, Message: fmt.Sprintf(format, args...)}
}

// EngineMissing is the error for an engine that can't run here.
func EngineMissing(engine, reason string) error {
	return &Error{Kind: KindEngineMissing, Engine: engine, Message: reason}
}

// UnknownFormat is the error for a format id nobody knows.
func UnknownFormat(id string) error {
	return &Error{Kind: KindUnsupportedInput, Message: i18n.T("unknown format: %s", id)}
}

// UndetectedFormat is the error for a file whose format can't be told.
func UndetectedFormat(path string) error {
	return &Error{Kind: KindUnsupportedInput, Message: i18n.T("can't tell the format of %s", path)}
}

// NoRoute is the error for a conversion no chain of engines covers.
func NoRoute(from, to string) error {
	return &Error{Kind: KindNoRoute, Message: i18n.T("no way to convert %s to %s", from, to)}
}

// OutputExists is the error for an exact output path that is taken.
func OutputExists(path string) error {
	return &Error{Kind: KindOutputExists, Message: i18n.T("%s already exists", path)}
}

// Kind is err's stable name for JSON output.
func Kind(err error) string {
	if errors.Is(err, ErrCancelled) || errors.Is(err, context.Canceled) {
		return KindCancelled
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindIO
}

// IsCancelled reports whether err means the job was stopped.
func IsCancelled(err error) bool { return Kind(err) == KindCancelled }

// StderrTail is the last few lines of a tool's stderr, for error messages.
func StderrTail(stderr []byte) string {
	const maxLines, maxBytes = 12, 2000
	text := strings.ToValidUTF8(strings.TrimSpace(string(stderr)), "�")
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	tail := strings.Join(lines, "\n")
	if len(tail) > maxBytes {
		cut := len(tail) - maxBytes
		for cut < len(tail) && !utf8.RuneStart(tail[cut]) {
			cut++
		}
		tail = "…" + tail[cut:]
	}
	if tail == "" {
		return i18n.T("no error output")
	}
	return tail
}
