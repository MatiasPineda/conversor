//go:build !windows && !darwin

package i18n

// systemLanguage is the locale from LANGUAGE, LC_ALL, LC_MESSAGES or LANG.
func systemLanguage() string { return fromEnv() }
