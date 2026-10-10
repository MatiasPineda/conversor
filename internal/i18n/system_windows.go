//go:build windows

package i18n

import "golang.org/x/sys/windows"

// systemLanguage is the first display language in Windows settings.
// Environment variables are ignored: Git Bash and MSYS set LANG to their
// own default, not the user's choice.
func systemLanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}
