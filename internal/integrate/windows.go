//go:build windows

package integrate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

// FileManagers lists the file managers Install can set up.
var FileManagers = []string{"explorer"}

const verb = "conversor"

func assocKey(ext string) string {
	return `Software\Classes\SystemFileAssociations\.` + ext + `\shell\` + verb
}

// menuLauncher is what the menu runs: conversor-app.exe when it is there,
// else conversorw.exe (the CLI without a console window), else the CLI.
func menuLauncher() (Launcher, []string, error) {
	l, err := FindLauncher()
	if err != nil || l.App {
		return l, nil, err
	}
	w := filepath.Join(filepath.Dir(l.Exe), "conversorw.exe")
	if _, err := os.Stat(w); err == nil {
		return Launcher{Exe: w}, nil, nil
	}
	return l, []string{i18n.T("conversorw.exe was not found next to conversor.exe, so a console window will flash on each conversion. Build it with: go build -ldflags=-H=windowsgui -o conversorw.exe ./cmd/conversor")}, nil
}

func commandLine(exe string, args []string) string {
	return fmt.Sprintf(`"%s" %s "%%1"`, exe, strings.Join(args, " "))
}

// Install adds a "Convert to" submenu to Explorer for every format some
// engine reads. It writes only under HKEY_CURRENT_USER, so it needs no
// administrator rights. On Windows 11 the menu is under "Show more options".
// With the desktop app it also adds a Start Menu shortcut.
func Install(r *core.Registry, only []string) (Result, error) {
	var res Result
	for _, fm := range only {
		if fm != "explorer" {
			return res, errors.New(i18n.T("unknown file manager %q; use %s", fm, "explorer"))
		}
	}
	l, notes, err := menuLauncher()
	if err != nil {
		return res, err
	}
	res.Notes = append(res.Notes, notes...)
	set := func(path string, values map[string]string) error {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer k.Close()
		for name, v := range values {
			if err := k.SetStringValue(name, v); err != nil {
				return err
			}
		}
		return nil
	}
	for _, e := range Menu(r) {
		for _, ext := range e.From.Extensions {
			if err := deleteTree(registry.CURRENT_USER, assocKey(ext)); err != nil && !errors.Is(err, registry.ErrNotExist) {
				return res, err
			}
			if err := set(assocKey(ext), map[string]string{"MUIVerb": i18n.T("Convert to"), "SubCommands": "", "Icon": l.Exe + ",0"}); err != nil {
				return res, err
			}
			for i, t := range e.Targets {
				sub := fmt.Sprintf(`%s\shell\%02d%s`, assocKey(ext), i, t.ID)
				// Offer the entry for selections of more than 15 files too.
				if err := set(sub, map[string]string{"MUIVerb": t.Name, "MultiSelectModel": "Player"}); err != nil {
					return res, err
				}
				if err := set(sub+`\command`, map[string]string{"": commandLine(l.Exe, l.Convert(t.ID))}); err != nil {
					return res, err
				}
			}
			if more := l.More(); more != nil {
				sub := assocKey(ext) + `\shell\99more`
				if err := set(sub, map[string]string{"MUIVerb": i18n.T("More options…"), "MultiSelectModel": "Player"}); err != nil {
					return res, err
				}
				if err := set(sub+`\command`, map[string]string{"": commandLine(l.Exe, more)}); err != nil {
					return res, err
				}
			}
			res.Done = append(res.Done, i18n.T("added the menu for .%s", ext))
		}
	}
	if l.App {
		if err := startMenuShortcut(l.Exe, true); err == nil {
			res.Done = append(res.Done, i18n.T("added Conversor to the Start menu"))
		}
	}
	res.Notes = append(res.Notes, i18n.T("Rerun this after moving or updating conversor."))
	return res, nil
}

func shortcutPath() string {
	return filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Conversor.lnk")
}

// startMenuShortcut creates or removes the Start Menu entry, through
// PowerShell's WScript.Shell (Go has no COM).
func startMenuShortcut(exe string, create bool) error {
	if !create {
		return os.Remove(shortcutPath())
	}
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	script := "$s=(New-Object -ComObject WScript.Shell).CreateShortcut(" + q(shortcutPath()) + "); $s.TargetPath=" + q(exe) +
		"; $s.WorkingDirectory=" + q(filepath.Dir(exe)) + "; $s.Description=" + q(i18n.T("Convert files locally")) + "; $s.Save()"
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd.Run()
}

// Uninstall removes every menu this tool added.
func Uninstall() (Result, error) {
	var res Result
	for _, f := range core.Formats {
		for _, ext := range f.Extensions {
			err := deleteTree(registry.CURRENT_USER, assocKey(ext))
			switch {
			case err == nil:
				res.Done = append(res.Done, i18n.T("removed the menu for .%s", ext))
			case !errors.Is(err, registry.ErrNotExist):
				return res, err
			}
		}
	}
	if startMenuShortcut("", false) == nil {
		res.Done = append(res.Done, i18n.T("removed Conversor from the Start menu"))
	}
	return res, nil
}

// Status lists the file types that have the menu.
func Status() []string {
	var out []string
	for _, f := range core.Formats {
		for _, ext := range f.Extensions {
			if k, err := registry.OpenKey(registry.CURRENT_USER, assocKey(ext), registry.READ); err == nil {
				k.Close()
				out = append(out, `HKCU\`+assocKey(ext))
			}
		}
	}
	return out
}

func deleteTree(root registry.Key, path string) error {
	k, err := registry.OpenKey(root, path, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	names, err := k.ReadSubKeyNames(-1)
	k.Close()
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := deleteTree(root, path+`\`+n); err != nil {
			return err
		}
	}
	return registry.DeleteKey(root, path)
}
