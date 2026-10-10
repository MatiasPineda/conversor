// Package gui is the desktop app's windows, drawn with Fyne: the main
// window (Activity and Automations), Quick convert, Settings, the welcome
// window and the tray menu. It uses only Fyne's toolkit packages; the
// OpenGL driver is picked in cmd/conversor-app, so this package builds and
// tests without cgo.
package gui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/ncruces/zenity"

	"conversor/internal/app"
	"conversor/internal/core"
	"conversor/internal/i18n"
)

// UI owns the app's windows.
type UI struct {
	fy    fyne.App
	state *app.State
	// Version is shown in Settings.
	Version string

	main      *mainWindow
	settings  *settingsWindow
	firstRun  fyne.Window
	quicks    map[*quickWindow]bool
	trayOK    bool
	trayBusy  bool
	quitting  bool
	refreshMu sync.Mutex
	pending   bool
	// later runs a refresh on the UI thread after a short delay; tests
	// set it to nil and refresh by hand.
	later func(func())
	// batch collects Quick convert requests that arrive together: file
	// managers start one process per selected file.
	batch *app.Request
}

// New builds the UI. Call Start, then fyne.App.Run.
func New(fy fyne.App, state *app.State, version string) *UI {
	u := &UI{fy: fy, state: state, Version: version, quicks: map[*quickWindow]bool{}}
	u.later = func(f func()) { time.AfterFunc(80*time.Millisecond, func() { fyne.Do(f) }) }
	fy.SetIcon(appIcon)
	fy.Settings().SetTheme(newTheme())
	state.OnChange(u.changed)
	fy.Lifecycle().SetOnEnteredForeground(func() { state.Background = false })
	fy.Lifecycle().SetOnExitedForeground(func() { state.Background = true })
	return u
}

// Start shows what the first request asks for and begins watching
// automation folders.
func (u *UI) Start(req app.Request) {
	u.setupTray()
	u.main = newMainWindow(u)
	if u.later != nil {
		go u.watchAutomations()
	}
	st := u.state.Settings()
	switch {
	case req.Page != "" || len(req.Files) > 0:
		u.Handle(req)
	case !st.FirstRunDone:
		u.showFirstRun()
	default:
		u.main.show()
	}
}

// Handle acts on a request from the command line, a file manager menu or a
// second launch. It must run on the UI thread.
func (u *UI) Handle(req app.Request) {
	switch req.Page {
	case "settings":
		u.showSettings("")
		return
	case "automations":
		u.main.showPage(pageAutomations)
		return
	case "activity":
		u.main.showPage(pageActivity)
		return
	}
	if len(req.Files) == 0 {
		u.main.show()
		return
	}
	if req.AutoStart() {
		if _, err := u.state.ConvertRequest(req); err == nil {
			return
		}
		// A window asks what to do instead.
	}
	u.queueQuick(req)
}

// queueQuick opens Quick convert, merging requests for the same target and
// preset that arrive within a moment into one window.
func (u *UI) queueQuick(req app.Request) {
	if u.later == nil {
		u.showQuick(req)
		return
	}
	if u.batch != nil && (u.batch.To != req.To || u.batch.Preset != req.Preset) {
		u.flushQuick()
	}
	if u.batch != nil {
		u.batch.Files = append(u.batch.Files, req.Files...)
		return
	}
	u.batch = &req
	time.AfterFunc(400*time.Millisecond, func() { fyne.Do(u.flushQuick) })
}

func (u *UI) flushQuick() {
	if u.batch != nil {
		req := *u.batch
		u.batch = nil
		u.showQuick(req)
	}
}

// HandleAsync hands a request from another goroutine to the UI thread.
func (u *UI) HandleAsync(req app.Request) {
	fyne.Do(func() { u.Handle(req) })
}

// changed schedules one refresh of every window, however many changes
// arrive meanwhile.
func (u *UI) changed() {
	u.refreshMu.Lock()
	if u.pending || u.later == nil {
		u.refreshMu.Unlock()
		return
	}
	u.pending = true
	later := u.later
	u.refreshMu.Unlock()
	later(func() {
		u.refreshMu.Lock()
		u.pending = false
		u.refreshMu.Unlock()
		u.refresh()
	})
}

func (u *UI) refresh() {
	if u.main != nil {
		u.main.refresh()
	}
	for q := range u.quicks {
		q.refresh()
	}
	if u.settings != nil {
		u.settings.refresh()
	}
	u.refreshTray()
}

// watchAutomations polls the automation folders once a second.
func (u *UI) watchAutomations() {
	for range time.Tick(time.Second) {
		u.state.PollAutomations()
	}
}

// pickFiles asks for files with the system's file picker, falling back to
// Fyne's own where there is none (Linux without zenity or kdialog).
func (u *UI) pickFiles(parent fyne.Window, then func([]string)) {
	go func() {
		files, err := zenity.SelectFileMultiple(zenity.Title(i18n.T("Choose files to convert")))
		fyne.Do(func() {
			switch {
			case err == nil:
				then(files)
			case err == zenity.ErrCanceled:
			default:
				d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
					if err == nil && r != nil {
						r.Close()
						then([]string{r.URI().Path()})
					}
				}, parent)
				d.Show()
			}
		})
	}()
}

// pickFolder asks for a folder.
func (u *UI) pickFolder(parent fyne.Window, start string, then func(string)) {
	go func() {
		opts := []zenity.Option{zenity.Directory(), zenity.Title(i18n.T("Choose a folder"))}
		if start != "" {
			opts = append(opts, zenity.Filename(start+string(filepath.Separator)))
		}
		dir, err := zenity.SelectFile(opts...)
		fyne.Do(func() {
			switch {
			case err == nil:
				then(dir)
			case err == zenity.ErrCanceled:
			default:
				d := dialog.NewFolderOpen(func(l fyne.ListableURI, err error) {
					if err == nil && l != nil {
						then(l.Path())
					}
				}, parent)
				d.Show()
			}
		})
	}()
}

// addFiles converts files to their default formats and opens Quick
// convert for the ones that have none.
func (u *UI) addFiles(paths []string, parent fyne.Window) {
	added := u.state.AddFiles(paths)
	if len(added.Errors) > 0 {
		showError(errors.New(strings.Join(added.Errors, "\n")), parent)
	}
	if len(added.Ask) > 0 {
		u.showQuick(app.Request{Files: added.Ask})
	}
}

// droppedPaths turns dropped URIs into local paths.
func droppedPaths(uris []fyne.URI) []string {
	var out []string
	for _, uri := range uris {
		if uri.Scheme() == "file" {
			out = append(out, uri.Path())
		}
	}
	return out
}

func (u *UI) showQuick(req app.Request) {
	q := newQuickWindow(u, req)
	u.quicks[q] = true
	q.win.SetOnClosed(func() { delete(u.quicks, q); u.maybeQuit() })
	q.win.Show()
}

func (u *UI) showSettings(tab string) {
	if u.settings == nil {
		u.settings = newSettingsWindow(u)
		u.settings.win.SetOnClosed(func() { u.settings = nil; u.maybeQuit() })
	}
	u.settings.selectTab(tab)
	u.settings.win.Show()
	u.settings.win.RequestFocus()
}

// keepsRunning reports whether closing every window leaves the app in the
// tray.
func (u *UI) keepsRunning() bool {
	return u.trayOK && u.state.Settings().KeepRunning
}

// maybeQuit quits once no window is open, unless the tray keeps the app
// running. Running conversions finish first.
func (u *UI) maybeQuit() {
	if u.quitting || u.keepsRunning() || u.main.visible || len(u.quicks) > 0 || u.settings != nil || u.firstRun != nil {
		return
	}
	u.quitWhenIdle()
}

func (u *UI) quitWhenIdle() {
	u.quitting = true
	go func() {
		for u.state.Busy() {
			time.Sleep(200 * time.Millisecond)
		}
		fyne.Do(u.fy.Quit)
	}()
}

// Quit stops conversions and quits now.
func (u *UI) Quit() {
	u.state.Jobs.CancelAll()
	u.fy.Quit()
}

// desktopApp is the tray-capable app, or nil.
func (u *UI) desktopApp() desktop.App {
	d, _ := u.fy.(desktop.App)
	return d
}

// formatLabel is "photo.heic ⟶ JPEG". (U+2192 renders badly in Fyne's
// bundled font; the long arrow doesn't.)
func formatLabel(input string, to *core.Format) string {
	return filepath.Base(input) + " ⟶ " + to.Name
}

// humanSize is "1.2 MB".
func humanSize(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return i18n.Decimal(float64(n)/1000, 0) + " KB"
	case n < 1000*1000*1000:
		return i18n.Decimal(float64(n)/1e6, 1) + " MB"
	}
	return i18n.Decimal(float64(n)/1e9, 2) + " GB"
}

func fileSize(path string) string {
	if info, err := os.Stat(path); err == nil {
		return humanSize(info.Size())
	}
	return ""
}

// timeLeft is "About 2 min left".
func timeLeft(d time.Duration) string {
	switch {
	case d < 10*time.Second:
		return i18n.T("A few seconds left")
	case d < time.Minute:
		return i18n.T("About %d s left", int(d.Seconds()+0.5)/5*5)
	case d < time.Hour:
		return i18n.T("About %d min left", int(d.Minutes()+0.5))
	}
	return i18n.T("About %s h left", i18n.Decimal(d.Hours(), 1))
}

// when is "4:08 PM", "Yesterday" or "Sep 28".
func when(unix int64, now time.Time) string {
	t := time.Unix(unix, 0).In(now.Location())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case !t.Before(today):
		return i18n.Clock(t)
	case !t.Before(today.AddDate(0, 0, -1)):
		return i18n.T("Yesterday")
	}
	return i18n.Date(t, t.Year() != now.Year())
}
