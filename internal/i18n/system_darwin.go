//go:build darwin

package i18n

import (
	"os/exec"
	"strings"
)

// systemLanguage is the locale from the environment when a terminal set
// one, else the first language in System Settings › General › Language &
// Region. Apps opened from Finder get no LANG.
func systemLanguage() string {
	if l := fromEnv(); l != "" {
		return l
	}
	out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return ""
	}
	return firstAppleLanguage(string(out))
}

// firstAppleLanguage reads the first entry of `defaults read -g
// AppleLanguages`, which prints a list like ( "es-CL", "en-US" ).
func firstAppleLanguage(out string) string {
	for _, line := range strings.Split(out, "\n") {
		l := strings.Trim(strings.TrimSpace(line), `",`)
		if l != "" && l != "(" && l != ")" {
			return l
		}
	}
	return ""
}
