//go:build linux || freebsd || openbsd || netbsd

package integrate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

const (
	marker       = "# conversor-generated: linux-integration-v1"
	thunarBegin  = "<!-- conversor-generated: begin -->"
	thunarEnd    = "<!-- conversor-generated: end -->"
	actionPrefix = "conversor-"
)

func dataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share")
}

func configHome() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config")
}

// FileManagers lists the file managers Install can set up.
var FileManagers = []string{"nautilus", "dolphin", "nemo", "thunar"}

func installed(fm string) bool {
	_, err := exec.LookPath(fm)
	return err == nil
}

// Install writes menus for the named file managers, or every one found
// when none are named.
func Install(r *core.Registry, only []string) (Result, error) {
	l, err := FindLauncher()
	if err != nil {
		return Result{}, err
	}
	return InstallWith(r, only, l)
}

// InstallWith is Install with an explicit program for the menus to run.
func InstallWith(r *core.Registry, only []string, l Launcher) (Result, error) {
	var res Result
	menu := Menu(r)
	targets := only
	if len(targets) == 0 {
		for _, fm := range FileManagers {
			if installed(fm) {
				targets = append(targets, fm)
			}
		}
		if len(targets) == 0 {
			return res, errors.New(i18n.T("no supported file manager found (Nautilus, Dolphin, Nemo or Thunar); name one to install anyway"))
		}
	}
	for _, fm := range targets {
		var err error
		switch fm {
		case "nautilus":
			err = installNautilus(menu, l, &res)
		case "dolphin":
			err = installDolphin(menu, l, &res)
		case "nemo":
			err = installNemo(menu, l, &res)
		case "thunar":
			err = installThunar(menu, l, &res)
		default:
			err = errors.New(i18n.T("unknown file manager %q; use %s", fm, strings.Join(FileManagers, ", ")))
		}
		if err != nil {
			return res, fmt.Errorf("%s: %w", fm, err)
		}
	}
	if l.App {
		if err := installDesktopEntry(l, &res); err != nil {
			return res, err
		}
	}
	res.Notes = append(res.Notes, i18n.T("Restart your file manager to see the menu (e.g. `nautilus -q`). Rerun this after updating conversor."))
	return res, nil
}

// iconSVG is the app icon for the desktop entry.
const iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect x="4" y="4" width="56" height="56" rx="14" fill="#2f6feb"/><path d="M18 26h22l-6-6M46 38H24l6 6" fill="none" stroke="#fff" stroke-width="5" stroke-linecap="round" stroke-linejoin="round"/></svg>
`

// installDesktopEntry lists the app among applications, so launchers and
// "Open With Other Application" reach it. It claims no file types, so a
// double-click never opens it.
func installDesktopEntry(l Launcher, res *Result) error {
	icon := filepath.Join(dataHome(), "icons", "hicolor", "scalable", "apps", "conversor.svg")
	if err := writeMarked(icon, "<!-- "+strings.TrimPrefix(marker, "# ")+" -->\n"+iconSVG, 0o644, res); err != nil {
		return err
	}
	entry := marker + "\n[Desktop Entry]\nType=Application\nName=Conversor\nComment=" + i18n.T("Convert images, video, audio, PDFs and documents") + "\n" +
		"Exec=" + desktopArg(l.Exe) + " %F\nIcon=conversor\nTerminal=false\nCategories=Utility;Graphics;AudioVideo;\nStartupWMClass=conversor\n"
	return writeMarked(filepath.Join(dataHome(), "applications", "conversor.desktop"), entry, 0o644, res)
}

func (l Launcher) execLine(args []string, files string) string {
	parts := []string{desktopArg(l.Exe)}
	parts = append(parts, args...)
	return strings.Join(append(parts, files), " ")
}

// Uninstall removes every menu file this tool wrote, and nothing else.
func Uninstall() (Result, error) {
	var res Result
	remove := func(dir, prefix string) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), prefix) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if data, err := os.ReadFile(p); err == nil && bytes.Contains(data, []byte(marker)) {
				if os.Remove(p) == nil {
					res.Done = append(res.Done, i18n.T("removed %s", p))
				}
			}
		}
	}
	remove(filepath.Join(dataHome(), "nautilus-python", "extensions"), "conversor")
	remove(filepath.Join(dataHome(), "kio", "servicemenus"), actionPrefix)
	remove(filepath.Join(dataHome(), "kservices5", "ServiceMenus"), actionPrefix)
	remove(filepath.Join(dataHome(), "nemo", "actions"), actionPrefix)
	remove(filepath.Join(dataHome(), "applications"), "conversor")
	remove(filepath.Join(dataHome(), "icons", "hicolor", "scalable", "apps"), "conversor")
	uca := filepath.Join(configHome(), "Thunar", "uca.xml")
	if data, err := os.ReadFile(uca); err == nil {
		if cleaned := stripThunar(data); !bytes.Equal(cleaned, data) {
			if err := os.WriteFile(uca, cleaned, 0o644); err != nil {
				return res, err
			}
			res.Done = append(res.Done, i18n.T("removed conversor actions from %s", uca))
		}
	}
	return res, nil
}

// Status lists the menu files that are installed.
func Status() []string {
	var out []string
	for _, dir := range []string{
		filepath.Join(dataHome(), "nautilus-python", "extensions"),
		filepath.Join(dataHome(), "kio", "servicemenus"),
		filepath.Join(dataHome(), "nemo", "actions"),
	} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "conversor") {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(configHome(), "Thunar", "uca.xml")); err == nil && bytes.Contains(data, []byte(thunarBegin)) {
		out = append(out, filepath.Join(configHome(), "Thunar", "uca.xml"))
	}
	return out
}

// desktopArg quotes one argument for a desktop entry's Exec key.
func desktopArg(s string) string {
	if !strings.ContainsAny(s, " \t\n\"'\\><~|&;$*?#()`") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", `$`, `\\$`)
	return `"` + r.Replace(s) + `"`
}

func writeMarked(path, content string, mode fs.FileMode, res *Result) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return err
	}
	res.Done = append(res.Done, i18n.T("wrote %s", path))
	return nil
}

// installNautilus writes a nautilus-python extension that offers each
// file type's targets in a "Convert to" submenu.
func installNautilus(menu []Entry, l Launcher, res *Result) error {
	var mapping strings.Builder
	for _, e := range menu {
		var ids []string
		for _, t := range e.Targets {
			ids = append(ids, fmt.Sprintf("(%q, %q)", t.ID, t.Name))
		}
		for _, ext := range e.From.Extensions {
			fmt.Fprintf(&mapping, "    %q: [%s],\n", ext, strings.Join(ids, ", "))
		}
	}
	script := marker + `
# Adds a "Convert to" submenu to Nautilus (GNOME Files). Needs nautilus-python
# (python3-nautilus on Debian/Ubuntu, nautilus-python on Fedora/Arch).
import os
import subprocess
from urllib.parse import unquote, urlparse

from gi.repository import GObject, Nautilus

CONVERSOR = ` + fmt.Sprintf("%q", l.Exe) + `
CONVERT_ARGS = ` + pyList(l.Convert("TARGET")) + `
MORE_ARGS = ` + pyList(l.More()) + `
CONVERT_LABEL = ` + fmt.Sprintf("%q", i18n.T("Convert to")) + `
MORE_LABEL = ` + fmt.Sprintf("%q", i18n.T("More options…")) + `
TARGETS = {
` + mapping.String() + `}


def _path(info):
    uri = info.get_uri()
    if not uri.startswith("file://"):
        return None
    return unquote(urlparse(uri).path)


class ConversorMenu(GObject.GObject, Nautilus.MenuProvider):
    def get_file_items(self, *args):
        files = args[-1]
        paths = [_path(f) for f in files]
        if not paths or None in paths:
            return []
        exts = {os.path.splitext(p)[1][1:].lower() for p in paths}
        if len(exts) != 1:
            return []
        targets = TARGETS.get(exts.pop())
        if not targets:
            return []
        top = Nautilus.MenuItem(name="Conversor::Convert", label=CONVERT_LABEL)
        sub = Nautilus.Menu()
        top.set_submenu(sub)
        for target, name in targets:
            item = Nautilus.MenuItem(name="Conversor::" + target, label=name)
            args = [target if a == "TARGET" else a for a in CONVERT_ARGS]
            item.connect("activate", self._run, args, paths)
            sub.append_item(item)
        if MORE_ARGS:
            item = Nautilus.MenuItem(name="Conversor::more", label=MORE_LABEL)
            item.connect("activate", self._run, MORE_ARGS, paths)
            sub.append_item(item)
        return [top]

    def _run(self, _item, args, paths):
        subprocess.Popen([CONVERSOR, *args, *paths],
                         stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                         stderr=subprocess.DEVNULL, start_new_session=True)
`
	path := filepath.Join(dataHome(), "nautilus-python", "extensions", "conversor.py")
	if err := writeMarked(path, script, 0o644, res); err != nil {
		return err
	}
	if _, err := os.Stat("/usr/lib/python3/dist-packages/gi/overrides/Nautilus.py"); err != nil && !nautilusPythonPresent() {
		res.Notes = append(res.Notes, i18n.T("Nautilus needs nautilus-python for the menu: sudo apt install python3-nautilus (Debian/Ubuntu) or nautilus-python (Fedora/Arch)."))
	}
	return nil
}

func nautilusPythonPresent() bool {
	for _, dir := range []string{"/usr/lib/nautilus/extensions-4", "/usr/lib64/nautilus/extensions-4", "/usr/lib/x86_64-linux-gnu/nautilus/extensions-4", "/usr/lib/nautilus/extensions-3.0", "/usr/lib/x86_64-linux-gnu/nautilus/extensions-3.0"} {
		if matches, _ := filepath.Glob(filepath.Join(dir, "*python*")); len(matches) > 0 {
			return true
		}
	}
	return false
}

// pyList renders a Python list of strings.
func pyList(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = fmt.Sprintf("%q", a)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

var unsafeName = regexp.MustCompile(`[^a-z0-9-]`)

// installDolphin writes one KDE service menu per source format, each with
// a "Convert to" submenu.
func installDolphin(menu []Entry, l Launcher, res *Result) error {
	for _, dir := range []string{filepath.Join(dataHome(), "kio", "servicemenus")} {
		for _, e := range menu {
			var actions []string
			var body strings.Builder
			for _, t := range e.Targets {
				id := unsafeName.ReplaceAllString(t.ID, "")
				actions = append(actions, id)
				fmt.Fprintf(&body, "\n[Desktop Action %s]\nName=%s\nIcon=document-save-as\nExec=%s\n", id, t.Name, l.execLine(l.Convert(t.ID), "%F"))
			}
			if more := l.More(); more != nil {
				actions = append(actions, "more")
				fmt.Fprintf(&body, "\n[Desktop Action more]\nName=%s\nIcon=configure\nExec=%s\n", i18n.T("More options…"), l.execLine(more, "%F"))
			}
			mimes := []string{e.From.MIME}
			content := marker + "\n[Desktop Entry]\nType=Service\nMimeType=" + strings.Join(mimes, ";") + ";\nActions=" + strings.Join(actions, ";") +
				";\nX-KDE-Submenu=" + i18n.T("Convert to") + "\nX-KDE-Priority=TopLevel\n" + body.String()
			path := filepath.Join(dir, actionPrefix+e.From.ID+".desktop")
			// KDE 6 runs service menus only when they are executable.
			if err := writeMarked(path, content, 0o755, res); err != nil {
				return err
			}
		}
	}
	return nil
}

// installNemo writes one Nemo action per target, shown for the file types
// that offer it.
func installNemo(menu []Entry, l Launcher, res *Result) error {
	targets, sources := ByTarget(menu)
	var allExts []string
	for _, t := range targets {
		var exts []string
		for _, f := range sources[t.ID] {
			for _, ext := range f.Extensions {
				exts = append(exts, ext, strings.ToUpper(ext))
			}
		}
		for _, e := range exts {
			if !slices.Contains(allExts, e) {
				allExts = append(allExts, e)
			}
		}
		content := marker + "\n[Nemo Action]\nName=" + i18n.T("Convert to %s", t.Name) + "\nComment=" + i18n.T("Convert with Conversor") + "\n" +
			"Exec=" + l.execLine(l.Convert(t.ID), "%F") + "\nIcon-Name=document-save-as\nSelection=notnone\nExtensions=" +
			strings.Join(exts, ";") + ";\nQuote=double\n"
		path := filepath.Join(dataHome(), "nemo", "actions", actionPrefix+t.ID+".nemo_action")
		if err := writeMarked(path, content, 0o644, res); err != nil {
			return err
		}
	}
	if more := l.More(); more != nil {
		content := marker + "\n[Nemo Action]\nName=" + i18n.T("Convert with options…") + "\nComment=" + i18n.T("Open Quick convert") + "\n" +
			"Exec=" + l.execLine(more, "%F") + "\nIcon-Name=configure\nSelection=notnone\nExtensions=" + strings.Join(allExts, ";") + ";\nQuote=double\n"
		if err := writeMarked(filepath.Join(dataHome(), "nemo", "actions", actionPrefix+"zz-more.nemo_action"), content, 0o644, res); err != nil {
			return err
		}
	}
	return nil
}

func xmlText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// stripThunar removes a previous install's actions from uca.xml.
func stripThunar(data []byte) []byte {
	for {
		start := bytes.Index(data, []byte(thunarBegin))
		end := bytes.Index(data, []byte(thunarEnd))
		if start < 0 || end < start {
			return data
		}
		end += len(thunarEnd)
		if end < len(data) && data[end] == '\n' {
			end++
		}
		data = append(data[:start:start], data[end:]...)
	}
}

// installThunar adds one custom action per target to Thunar's uca.xml,
// keeping the user's own actions.
func installThunar(menu []Entry, l Launcher, res *Result) error {
	path := filepath.Join(configHome(), "Thunar", "uca.xml")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		data = []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<actions>\n</actions>\n")
	} else if err != nil {
		return err
	}
	data = stripThunar(data)
	targets, sources := ByTarget(menu)
	var b strings.Builder
	b.WriteString(thunarBegin + "\n")
	shellLine := func(args []string) string {
		parts := []string{shellQuote(l.Exe)}
		return strings.Join(append(append(parts, args...), "%F"), " ")
	}
	var allPatterns []string
	for _, t := range targets {
		var patterns []string
		var image, audio, video, text, other bool
		for _, f := range sources[t.ID] {
			for _, ext := range f.Extensions {
				patterns = append(patterns, "*."+ext, "*."+strings.ToUpper(ext))
			}
			switch f.Category {
			case core.Image, core.Vector:
				image = true
			case core.Audio:
				audio = true
			case core.Video:
				video = true
			case core.Document:
				text, other = true, true
			default:
				other = true
			}
		}
		var conds strings.Builder
		for _, c := range []struct {
			on  bool
			tag string
		}{{image, "image-files"}, {audio, "audio-files"}, {video, "video-files"}, {text, "text-files"}, {other, "other-files"}} {
			if c.on {
				conds.WriteString("<" + c.tag + "/>")
			}
		}
		for _, p := range patterns {
			if !slices.Contains(allPatterns, p) {
				allPatterns = append(allPatterns, p)
			}
		}
		fmt.Fprintf(&b, "<action><icon>document-save-as</icon><name>%s</name><submenu>%s</submenu><unique-id>conversor-%s</unique-id><command>%s</command><description>%s</description><range></range><patterns>%s</patterns>%s</action>\n",
			xmlText(i18n.T("Convert to %s", t.Name)), xmlText(i18n.T("Convert to")), t.ID, xmlText(shellLine(l.Convert(t.ID))), xmlText(i18n.T("Convert with Conversor")), xmlText(strings.Join(patterns, ";")), conds.String())
	}
	if more := l.More(); more != nil {
		fmt.Fprintf(&b, "<action><icon>configure</icon><name>%s</name><submenu>%s</submenu><unique-id>conversor-more</unique-id><command>%s</command><description>%s</description><range></range><patterns>%s</patterns><image-files/><audio-files/><video-files/><text-files/><other-files/></action>\n",
			xmlText(i18n.T("More options…")), xmlText(i18n.T("Convert to")), xmlText(shellLine(more)), xmlText(i18n.T("Open Quick convert")), xmlText(strings.Join(allPatterns, ";")))
	}
	b.WriteString(thunarEnd + "\n")
	at := bytes.LastIndex(data, []byte("</actions>"))
	if at < 0 {
		return errors.New(i18n.T("%s has no <actions> element", path))
	}
	out := append(append(append([]byte{}, data[:at]...), b.String()...), data[at:]...)
	return writeMarked(path, string(out), 0o644, res)
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t\n\"'\\><~|&;$*?#()`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
