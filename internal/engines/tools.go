// Package engines holds the engines Conversor ships with. Each one wraps
// a tool or library and declares the conversions it supports; package core
// handles routing.
package engines

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"conversor/internal/core"
)

// Default returns a registry with every engine that can run on this machine.
func Default() *core.Registry { return registry(true) }

// WithoutDocuments is Default without LibreOffice, for while the document
// pack is being removed: nothing may start it from a folder being deleted.
func WithoutDocuments() *core.Registry { return registry(false) }

func registry(documents bool) *core.Registry {
	r := core.NewRegistry()
	r.Register(NewImageEngine())
	r.Register(NewSVGEngine())
	r.Register(NewHEIFEngine())
	r.Register(NewFFmpegEngine())
	r.Register(NewPDFEngine())
	if documents {
		r.Register(NewOfficeEngine())
	}
	return r
}

// exeDir is the running executable's directory, where release bundles keep
// their tools. Symlinks are resolved, so a linked CLI finds the real bundle.
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

// findTool finds a bundled or installed tool. It checks envVar, then the
// directory next to the running executable (where bundles keep tools), then
// PATH, then the places package managers put tools that a file manager's
// or Finder's minimal PATH misses, then extra well-known locations.
func findTool(names []string, envVar string, extra ...string) string {
	if envVar != "" {
		if p := os.Getenv(envVar); p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	isFile := func(p string) bool {
		info, err := os.Stat(p)
		return err == nil && !info.IsDir()
	}
	if dir := exeDir(); dir != "" {
		for _, name := range names {
			if p := filepath.Join(dir, name+suffix); isFile(p) {
				return p
			}
		}
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, dir := range toolDirs() {
		for _, name := range names {
			if p := filepath.Join(dir, name+suffix); isFile(p) {
				return p
			}
		}
	}
	for _, p := range extra {
		if isFile(p) {
			return p
		}
	}
	return ""
}

// toolDirs are where package managers link tools, which processes started
// by a file manager may not have on PATH (a fresh winget install, or any
// Homebrew tool under Finder).
func toolDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		var dirs []string
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			dirs = append(dirs, filepath.Join(d, "Microsoft", "WinGet", "Links"))
		}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, "scoop", "shims"))
		}
		if d := os.Getenv("ProgramData"); d != "" {
			dirs = append(dirs, filepath.Join(d, "chocolatey", "bin"))
		}
		return append(dirs, `C:\msys64\mingw64\bin`, `C:\msys64\ucrt64\bin`)
	case "darwin":
		return []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}
	default:
		dirs := []string{"/usr/local/bin", "/usr/bin", "/snap/bin", "/home/linuxbrew/.linuxbrew/bin"}
		if home != "" {
			dirs = append(dirs, filepath.Join(home, ".local", "bin"))
		}
		return dirs
	}
}

// drainGrace is how long a finished tool's leftover children may hold its
// output open before they are killed.
const drainGrace = time.Second

// stderrKeep is how much of a tool's stderr is kept for error messages.
const stderrKeep = 64 << 10

// runTool runs cmd to completion, feeding each stdout line to onLine (from
// another goroutine). It kills the tool and anything it spawned when the
// task is cancelled. A non-zero exit becomes an engine failure carrying the
// tail of stderr.
func runTool(t *core.Task, engine string, cmd *exec.Cmd, onLine func(string)) error {
	var stderr tailBuffer
	cmd.Stdin = nil
	cmd.Stdout = &lineWriter{onLine: onLine}
	cmd.Stderr = &stderr
	// Once the tool exits, a stray background process holding its output
	// open gets drainGrace before the pipes are closed, so it can't hang
	// the job.
	cmd.WaitDelay = drainGrace
	prepareCommand(cmd)
	if err := cmd.Start(); err != nil {
		return core.EngineFailed(engine, "%v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-t.Context.Done():
		killTree(cmd)
		<-done
		return core.ErrCancelled
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// Something the tool started still holds its output, so its process
		// group is still alive and its id can't have been reused.
		killTree(cmd)
	}
	cmd.Stdout.(*lineWriter).flush()
	if t.Cancelled() {
		return core.ErrCancelled
	}
	if err == nil || (errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState.Success()) {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return core.EngineFailed(engine, "%s", core.StderrTail(stderr.Bytes()))
	}
	return core.EngineFailed(engine, "%v", err)
}

// lineWriter splits what a tool writes into lines.
type lineWriter struct {
	onLine func(string)
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	if w.onLine == nil {
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.onLine(string(bytes.TrimRight(w.buf[:i], "\r")))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > 1<<20 {
		w.buf = nil
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if w.onLine != nil && len(w.buf) > 0 {
		w.onLine(string(w.buf))
		w.buf = nil
	}
}

// tailBuffer keeps the last stderrKeep bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > stderrKeep {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-stderrKeep:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf)
}

// ConfigDir is $CONVERSOR_CONFIG_DIR, else the platform config directory
// plus "conversor". The CLI and the desktop app share it.
func ConfigDir() string {
	if d := os.Getenv("CONVERSOR_CONFIG_DIR"); d != "" {
		return d
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "conversor")
}

// PresetsDir holds named presets, one <name>.toml each.
func PresetsDir() string {
	if d := ConfigDir(); d != "" {
		return filepath.Join(d, "presets")
	}
	return ""
}

// DataDir is $CONVERSOR_DATA_DIR, else the platform data directory plus
// "conversor": ~/.local/share on Linux, Application Support on macOS and
// %LOCALAPPDATA% on Windows.
func DataDir() string {
	if d := os.Getenv("CONVERSOR_DATA_DIR"); d != "" {
		return d
	}
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "conversor")
		}
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, "Library", "Application Support", "conversor")
		}
	default:
		if d := os.Getenv("XDG_DATA_HOME"); d != "" {
			return filepath.Join(d, "conversor")
		}
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, ".local", "share", "conversor")
		}
	}
	return ""
}

// CacheDir is where compiled WebAssembly modules are kept between runs.
func CacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "conversor")
	}
	return ""
}
