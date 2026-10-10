package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

// Folder watching for automation rules. Each enabled rule looks at one
// folder, never recursively: the screenshot location, the screen-recording
// folder, or a folder the rule names. Catch-all folders (Desktop, Downloads,
// home) only match files that look like screenshots or recordings, so a
// Desktop watch doesn't convert every image that lands there.

// WatchKind is what a rule watches for.
type WatchKind string

const (
	WatchScreenshot WatchKind = "screenshot"
	WatchRecording  WatchKind = "recording"
	WatchFolder     WatchKind = "folder"
)

// Automation is a rule such as "Screenshots become PNG".
type Automation struct {
	// Name is what the rule watches, e.g. "Screenshots". For a folder rule
	// it may also be a format id, limiting the rule to that format.
	Name string `toml:"name"`
	// To is the target format id.
	To string `toml:"to"`
	// Source names the folder for display, e.g. "Downloads".
	Source  string    `toml:"source"`
	Enabled bool      `toml:"enabled"`
	Watch   WatchKind `toml:"watch"`
	// Folder is an explicit folder; when set, the system location is ignored.
	Folder string `toml:"folder,omitempty"`
	// CopyToClipboard puts each result on the clipboard.
	CopyToClipboard bool `toml:"copy_to_clipboard"`
}

// ExampleAutomations are the rules a fresh install lists.
func ExampleAutomations() []Automation {
	return []Automation{
		{Name: i18n.T("Screenshots"), To: "png", Source: "Screenshots", Enabled: true, Watch: WatchScreenshot, CopyToClipboard: true},
		{Name: i18n.T("Screen recordings"), To: "mp4", Source: "Screen recordings", Enabled: true, Watch: WatchRecording},
		{Name: "HEIC", To: "jpeg", Source: "Downloads", Enabled: false, Watch: WatchFolder},
	}
}

// Detail describes what the rule does besides converting.
func (a Automation) Detail() string {
	if a.CopyToClipboard {
		return i18n.T("copy to clipboard")
	}
	return i18n.T("save beside original")
}

// LooksLikeScreenshot reports names macOS, Windows, Linux and common tools
// give still captures.
func LooksLikeScreenshot(path string) bool {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	for _, p := range []string{"screenshot", "screen shot", "cleanshot", "simulator screen", "captura de pantalla", "bildschirmfoto", "capture d"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// LooksLikeRecording reports names macOS, Windows and Linux give screen
// recordings.
func LooksLikeRecording(path string) bool {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	for _, p := range []string{"screen recording", "screenrecording", "recording ", "recording_", "screencast", "grabación de pantalla"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// WatchStatus says where a rule looks, or why it doesn't.
type WatchStatus struct {
	// Dir is set when the folder exists and is listed on each poll.
	Dir string
	// Reason is set otherwise.
	Reason string
}

// Status works out a rule's folder.
func (a Automation) Status() WatchStatus {
	pick := func(dir string, missing string) WatchStatus {
		switch {
		case dir == "":
			return WatchStatus{Reason: missing}
		case isTooBroad(dir):
			return WatchStatus{Reason: i18n.T("This folder is too broad to watch.")}
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return WatchStatus{Dir: dir}
		}
		return WatchStatus{Reason: i18n.T("%s isn't there yet.", DisplayPath(dir))}
	}
	if a.Folder != "" {
		return pick(expandTilde(a.Folder), "")
	}
	switch a.Watch {
	case WatchScreenshot:
		dir, clipboard := screenshotDir()
		if clipboard {
			return WatchStatus{Reason: i18n.T("Screenshots go to the clipboard, so there is no folder to watch.")}
		}
		return pick(dir, i18n.T("No screenshot folder is set."))
	case WatchRecording:
		return pick(recordingDir(), i18n.T("No screen-recording folder is set."))
	}
	return pick(sourceDir(a.Source), i18n.T("No folder for %s.", a.Source))
}

// SourceLine is the path shown under a rule, or why it is waiting.
func (a Automation) SourceLine() string {
	st := a.Status()
	if st.Dir != "" {
		return DisplayPath(st.Dir)
	}
	return st.Reason
}

func (a Automation) matches(path, dir string) bool {
	name := filepath.Base(path)
	lower := strings.ToLower(name)
	if strings.HasPrefix(name, ".") || strings.HasSuffix(lower, ".tmp") || strings.HasSuffix(lower, ".part") ||
		strings.HasSuffix(lower, ".crdownload") || strings.HasSuffix(lower, ".download") || strings.Contains(lower, ".conversor-") {
		return false
	}
	from, to := core.ByPath(path), core.ByID(a.To)
	if from == nil || to == nil || from == to {
		return false
	}
	switch a.Watch {
	case WatchScreenshot:
		return (from.Category == core.Image || from.Category == core.Vector) && (LooksLikeScreenshot(path) || !isCatchAll(dir))
	case WatchRecording:
		return from.Category == core.Video && (LooksLikeRecording(path) || !isCatchAll(dir))
	}
	if only := core.ByID(a.Name); only != nil {
		return from == only
	}
	return true
}

// WatchState remembers what each watched folder held, so files already
// there aren't converted when a rule is turned on, and files still being
// written (a recording) wait until they stop growing. Rules may share a
// folder (on macOS screenshots and recordings both land on the Desktop), so
// what a rule has handled is kept per rule.
type WatchState struct {
	// primed and handled are keyed by rule index and path.
	primed  map[string]bool
	handled map[string]bool
	// sizes are each file's size at the last poll.
	sizes map[string]int64
	// outputs are files rules wrote, which no rule converts again.
	outputs map[string]bool
}

func NewWatchState() *WatchState {
	return &WatchState{primed: map[string]bool{}, handled: map[string]bool{}, sizes: map[string]int64{}, outputs: map[string]bool{}}
}

// Ready is a file an automation should convert now.
type Ready struct {
	Rule int
	Path string
}

// DrainReady lists files that are new, have kept their size since the last
// poll and match an enabled rule.
func (w *WatchState) DrainReady(rules []Automation) []Ready {
	var ready []Ready
	stable := map[string]bool{}
	listed := map[string]map[string]int64{}
	for i, rule := range rules {
		if !rule.Enabled {
			continue
		}
		dir := rule.Status().Dir
		if dir == "" {
			continue
		}
		entries, ok := listed[dir]
		if !ok {
			var err error
			if entries, err = listFiles(dir); err != nil {
				continue
			}
			listed[dir] = entries
			for path, size := range entries {
				prev, known := w.sizes[path]
				stable[path] = known && prev == size && size > 0
				w.sizes[path] = size
			}
		}
		key := func(path string) string { return fmt.Sprint(i) + "\x00" + path }
		if !w.primed[key(dir)] {
			w.primed[key(dir)] = true
			for path := range entries {
				w.handled[key(path)] = true
			}
			continue
		}
		for path := range entries {
			if w.handled[key(path)] || w.outputs[path] || !stable[path] {
				continue
			}
			w.handled[key(path)] = true
			if rule.matches(path, dir) {
				if to := core.ByID(rule.To); to != nil {
					// The result lands in the same folder; never convert it.
					w.outputs[strings.TrimSuffix(path, filepath.Ext(path))+"."+to.Extension()] = true
				}
				ready = append(ready, Ready{i, path})
			}
		}
	}
	return ready
}

func listFiles(dir string) (map[string]int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out[filepath.Join(dir, e.Name())] = info.Size()
	}
	return out, nil
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func samePath(a, b string) bool {
	norm := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	if runtime.GOOS == "linux" {
		return norm(a) == norm(b)
	}
	return strings.EqualFold(norm(a), norm(b))
}

func isTooBroad(dir string) bool {
	clean := filepath.Clean(dir)
	return filepath.Dir(clean) == clean || (home() != "" && samePath(clean, home()))
}

func isCatchAll(dir string) bool {
	switch strings.ToLower(filepath.Base(dir)) {
	case "desktop", "downloads", "documents", "movies", "pictures", "videos", "music":
		return true
	}
	return home() != "" && samePath(dir, home())
}

func expandTilde(p string) string {
	if p == "~" {
		return home()
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok && home() != "" {
		return filepath.Join(home(), rest)
	}
	return p
}

// DisplayPath shortens the home folder to ~.
func DisplayPath(p string) string {
	h := home()
	if h != "" {
		if rel, err := filepath.Rel(h, p); err == nil && !strings.HasPrefix(rel, "..") {
			if rel == "." {
				return "~"
			}
			return "~" + string(filepath.Separator) + rel
		}
	}
	return p
}

// userDir finds an XDG user folder (Desktop, Pictures, ...) on Linux, or
// the conventional folder under home elsewhere.
func userDir(xdgKey, fallback string) string {
	if v := os.Getenv("CONVERSOR_TEST_HOME_DIRS"); v != "" {
		return filepath.Join(v, fallback)
	}
	if runtime.GOOS == "linux" {
		if out, err := exec.Command("xdg-user-dir", xdgKey).Output(); err == nil {
			if d := strings.TrimSpace(string(out)); d != "" && d != home() {
				return d
			}
		}
	}
	if home() == "" {
		return ""
	}
	return filepath.Join(home(), fallback)
}

func sourceDir(source string) string {
	source = strings.TrimSpace(source)
	switch strings.ToLower(source) {
	case "":
		return ""
	case "desktop", "escritorio":
		return userDir("DESKTOP", "Desktop")
	case "downloads", "descargas":
		return userDir("DOWNLOAD", "Downloads")
	case "documents", "documentos":
		return userDir("DOCUMENTS", "Documents")
	case "pictures", "imágenes", "imagenes":
		return userDir("PICTURES", "Pictures")
	case "movies", "videos", "vídeos", "películas", "peliculas":
		if runtime.GOOS == "darwin" {
			return userDir("VIDEOS", "Movies")
		}
		return userDir("VIDEOS", "Videos")
	}
	if strings.HasPrefix(source, "~") || filepath.IsAbs(source) {
		return expandTilde(source)
	}
	return ""
}

func envDir(name string) string {
	if v := os.Getenv(name); v != "" {
		return expandTilde(v)
	}
	return ""
}

// screenshotDir is where this system saves screenshots, and whether they
// go to the clipboard instead (a macOS setting).
func screenshotDir() (string, bool) {
	if d := envDir("CONVERSOR_SCREENSHOT_DIR"); d != "" {
		return d, false
	}
	if runtime.GOOS == "darwin" {
		if t := macScreencapture("target"); strings.EqualFold(t, "clipboard") {
			return "", true
		}
		if loc := macScreencapture("location"); loc != "" {
			return expandTilde(strings.Trim(loc, `"`)), false
		}
		return userDir("DESKTOP", "Desktop"), false
	}
	if d := filepath.Join(userDir("PICTURES", "Pictures"), "Screenshots"); isDir(d) {
		return d, false
	}
	if runtime.GOOS == "windows" {
		// OneDrive moves Pictures when backup is on.
		if od := os.Getenv("OneDrive"); od != "" {
			if d := filepath.Join(od, "Pictures", "Screenshots"); isDir(d) {
				return d, false
			}
		}
		return filepath.Join(userDir("PICTURES", "Pictures"), "Screenshots"), false
	}
	if runtime.GOOS == "linux" {
		// KDE's Spectacle saves to Pictures itself.
		return userDir("PICTURES", "Pictures"), false
	}
	return userDir("DESKTOP", "Desktop"), false
}

// recordingDir is where this system saves screen recordings.
func recordingDir() string {
	if d := envDir("CONVERSOR_RECORDING_DIR"); d != "" {
		return d
	}
	switch runtime.GOOS {
	case "darwin":
		for _, key := range []string{"videolocation", "videoLocation", "recordingLocation"} {
			if loc := macScreencapture(key); loc != "" {
				return expandTilde(strings.Trim(loc, `"`))
			}
		}
		if loc := macScreencapture("location"); loc != "" {
			return expandTilde(strings.Trim(loc, `"`))
		}
		return userDir("DESKTOP", "Desktop")
	case "windows":
		videos := userDir("VIDEOS", "Videos")
		for _, sub := range []string{"Screen Recordings", "Captures"} {
			if d := filepath.Join(videos, sub); isDir(d) {
				return d
			}
		}
		return filepath.Join(videos, "Captures")
	default:
		videos := userDir("VIDEOS", "Videos")
		if d := filepath.Join(videos, "Screencasts"); isDir(d) {
			return d
		}
		return videos
	}
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func macScreencapture(key string) string {
	out, err := exec.Command("defaults", "read", "com.apple.screencapture", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// String is how the Automations page names the rule.
func (a Automation) String() string {
	to := a.To
	if f := core.ByID(a.To); f != nil {
		to = f.Name
	}
	return fmt.Sprintf("%s ⟶ %s", a.Name, to)
}
