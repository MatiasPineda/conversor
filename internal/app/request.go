package app

import (
	"errors"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

// Request is what a launch asks the app to do. Requests come from the
// command line, file manager menus and "Open with"; a second launch hands
// its request to the running app.
type Request struct {
	Files  []string `json:"files,omitempty"`
	To     string   `json:"to,omitempty"`
	Preset string   `json:"preset,omitempty"`
	// Page opens a window instead of converting: "", "activity",
	// "automations" or "settings".
	Page string `json:"page,omitempty"`
}

// AutoStart reports whether the conversion may start without asking: a
// target or preset was named. Such a request converts in place with no
// window; a window opens only to ask if a file can't be converted.
func (r Request) AutoStart() bool {
	return (r.To != "" || r.Preset != "") && len(r.Files) > 0
}

// Usage is the app's command-line help.
func Usage() string {
	return i18n.T(`usage: conversor-app [files...]
       conversor-app open [--to <format>] [--preset <name>] [--] <files...>
       conversor-app settings | automations

Opens the Conversor window. With --to, or a preset that names a format,
the files convert in place right away and no window opens.`)
}

// ErrHelp and ErrVersion ask main to print help or the version.
var (
	ErrHelp    = errors.New("help")
	ErrVersion = errors.New("version")
)

// ParseArgs parses the arguments after the program name. Relative paths
// are resolved against cwd, since the running app may have another one.
func ParseArgs(args []string, cwd string) (Request, error) {
	var r Request
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			return r, ErrHelp
		case "-V", "--version":
			return r, ErrVersion
		case "open":
			args = args[1:]
		case "settings", "automations", "activity":
			if len(args) > 1 {
				return r, errors.New(i18n.T("%s takes no files", args[0]))
			}
			r.Page = args[0]
			return r, nil
		}
	}
	onlyFiles := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func(name string) (string, error) {
			if i+1 >= len(args) {
				return "", errors.New(i18n.T("%s needs a value", name))
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case onlyFiles:
			r.Files = append(r.Files, absolute(a, cwd))
		case a == "--":
			onlyFiles = true
		case a == "--to":
			r.To, err = value("--to")
		case a == "--preset":
			r.Preset, err = value("--preset")
		case strings.HasPrefix(a, "--to="):
			r.To = a[len("--to="):]
		case strings.HasPrefix(a, "--preset="):
			r.Preset = a[len("--preset="):]
		case strings.HasPrefix(a, "-") && len(a) > 1:
			return r, errors.New(i18n.T("unknown option %s", a))
		case strings.HasPrefix(a, "file://"):
			// Desktop entries may pass file:// URIs (%U).
			p, ferr := fileURI(a)
			if ferr != nil {
				return r, ferr
			}
			r.Files = append(r.Files, p)
		default:
			r.Files = append(r.Files, absolute(a, cwd))
		}
		if err != nil {
			return r, err
		}
	}
	if r.To != "" && core.ByID(r.To) == nil {
		return r, errors.New(i18n.T("unknown format: %s", r.To))
	}
	return r, nil
}

func absolute(p, cwd string) string {
	if filepath.IsAbs(p) || cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}

func fileURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || (u.Host != "" && u.Host != "localhost") || u.Path == "" {
		return "", errors.New(i18n.T("%s is not a local file", uri))
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
	}
	return filepath.FromSlash(p), nil
}
