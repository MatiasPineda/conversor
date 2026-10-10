// Package integrate installs "Convert to" menus in file managers: Nautilus,
// Dolphin, Nemo and Thunar on Linux, Explorer on Windows and Finder Quick
// Actions on macOS. Entries run the desktop app when it is installed, else
// the CLI with --notify, so the result shows as a desktop notification.
package integrate

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"conversor/internal/core"
)

// Entry is what the menu offers for one source format.
type Entry struct {
	From    *core.Format
	Targets []*core.Format
}

// Menu lists, for every format some engine reads, the popular targets the
// right-click menu offers.
func Menu(r *core.Registry) []Entry {
	var out []Entry
	for _, f := range core.Formats {
		if t := r.MenuTargets(f); len(t) > 0 {
			out = append(out, Entry{f, t})
		}
	}
	return out
}

// ByTarget groups source formats by target, for file managers whose menu
// entries each list the files they apply to.
func ByTarget(menu []Entry) ([]*core.Format, map[string][]*core.Format) {
	sources := map[string][]*core.Format{}
	var targets []*core.Format
	for _, e := range menu {
		for _, t := range e.Targets {
			if _, ok := sources[t.ID]; !ok {
				targets = append(targets, t)
			}
			sources[t.ID] = append(sources[t.ID], e.From)
		}
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Category != targets[j].Category {
			return targets[i].Category < targets[j].Category
		}
		return targets[i].Name < targets[j].Name
	})
	return targets, sources
}

// Executable is the conversor binary the menus run: the running one, with
// symlinks resolved so the menus keep working if a link moves.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// Launcher is what a menu entry runs: the desktop app when it is installed
// next to the CLI (it shows progress, batches a multi-file selection into
// one job list and offers Quick convert), else the CLI with --notify.
type Launcher struct {
	// Exe is the program to run.
	Exe string
	// App is set when Exe is conversor-app.
	App bool
}

// Convert is the argument list that converts files to target (the files
// follow).
func (l Launcher) Convert(target string) []string {
	if l.App {
		return []string{"open", "--to", target, "--"}
	}
	return []string{"--notify", "--to", target, "--"}
}

// More is the argument list for "More options…" (Quick convert), or nil
// without the app.
func (l Launcher) More() []string {
	if l.App {
		return []string{"open", "--"}
	}
	return nil
}

// FindLauncher looks for conversor-app next to the running binary.
func FindLauncher() (Launcher, error) {
	exe, err := Executable()
	if err != nil {
		return Launcher{}, err
	}
	base := strings.TrimSuffix(filepath.Base(exe), ".exe")
	if base == "conversor-app" || (runtime.GOOS == "darwin" && base == "Conversor") {
		return Launcher{exe, true}, nil
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	names := []string{"conversor-app" + suffix}
	if runtime.GOOS == "darwin" {
		// `fyne package` names the app's binary after the app.
		names = append(names, "Conversor")
	}
	for _, n := range names {
		if a := filepath.Join(filepath.Dir(exe), n); isFile(a) {
			return Launcher{a, true}, nil
		}
	}
	if runtime.GOOS == "darwin" {
		for _, dir := range []string{"/Applications", filepath.Join(os.Getenv("HOME"), "Applications")} {
			for _, n := range names {
				if a := filepath.Join(dir, "Conversor.app", "Contents", "MacOS", n); isFile(a) {
					return Launcher{a, true}, nil
				}
			}
		}
	}
	return Launcher{exe, false}, nil
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// Result describes what Install or Uninstall touched.
type Result struct {
	// Done lists what was written or removed, for the user.
	Done []string
	// Notes are hints, such as a file manager that needs a restart.
	Notes []string
}
