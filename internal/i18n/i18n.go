// Package i18n translates what Conversor shows people. Messages are written
// in English in the code and looked up in a Spanish catalog (es.go). The
// language is the computer's when it is Spanish or English, and Spanish
// otherwise; CONVERSOR_LANG=es or en overrides it.
//
// Machine-readable output (JSON events, error kinds, format ids, preset
// keys) is never translated.
package i18n

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// The supported languages.
const (
	Spanish = "es"
	English = "en"
)

var (
	mu      sync.RWMutex
	current string
)

// Lang is the language in use: Spanish or English.
func Lang() string {
	mu.RLock()
	l := current
	mu.RUnlock()
	if l != "" {
		return l
	}
	mu.Lock()
	defer mu.Unlock()
	if current == "" {
		current = Detect()
	}
	return current
}

// Set switches the language; anything but English means Spanish.
func Set(lang string) {
	mu.Lock()
	current = Match(lang)
	mu.Unlock()
}

// Detect is the language to use here: $CONVERSOR_LANG, else the system's.
func Detect() string {
	if v := os.Getenv("CONVERSOR_LANG"); v != "" {
		return Match(v)
	}
	return Match(systemLanguage())
}

// Match maps a locale name ("en_US.UTF-8", "es-CL", "fr") to English when
// it is English and to Spanish for everything else.
func Match(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	if l == "en" || strings.HasPrefix(l, "en_") || strings.HasPrefix(l, "en-") || strings.HasPrefix(l, "en.") {
		return English
	}
	return Spanish
}

// fromEnv reads the POSIX locale variables in the order gettext does.
// LANGUAGE is a colon-separated preference list; only its first entry
// counts. "C" and "POSIX" name no language.
func fromEnv() string {
	for _, name := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		v, _, _ := strings.Cut(os.Getenv(name), ":")
		base, _, _ := strings.Cut(v, ".")
		if base != "" && base != "C" && base != "POSIX" {
			return v
		}
	}
	return ""
}

// T translates msg, then formats it with args like fmt.Sprintf. Without
// args the text is returned as is, so a literal % needs no escaping.
func T(msg string, args ...any) string {
	if Lang() == Spanish {
		if s, ok := es[msg]; ok {
			msg = s
		}
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// N picks the singular or plural message for n and formats it with n
// first, then args: N(3, "%d file", "%d files") is "3 archivos".
func N(n int, one, other string, args ...any) string {
	msg := other
	if n == 1 {
		msg = one
	}
	return T(msg, append([]any{n}, args...)...)
}

// Decimal formats v with prec decimals and the language's decimal mark.
func Decimal(v float64, prec int) string {
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if Lang() == Spanish {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}
