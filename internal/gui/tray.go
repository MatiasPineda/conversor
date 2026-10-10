package gui

import (
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"

	"conversor/internal/app"
	"conversor/internal/i18n"
)

// The tray icon: the menu bar item on macOS, the notification area icon on
// Windows and a StatusNotifierItem on Linux. While "Keep running in the
// background" is on, closing the last window leaves the app running, so
// file manager requests start at once.

// recentInTray is how many converted files the tray menu lists.
const recentInTray = 5

func (u *UI) setupTray() {
	d := u.desktopApp()
	if d == nil || !trayAvailable() {
		return
	}
	// The tray shows the app icon by default; it changes only while busy.
	u.trayOK = true
	u.refreshTray()
}

// trayLabel escapes a file name for menus, which read & or _ as mnemonics.
func trayLabel(s string) string {
	return strings.NewReplacer("&", "&&", "_", "__").Replace(s)
}

func (u *UI) refreshTray() {
	d := u.desktopApp()
	if d == nil || !u.trayOK {
		return
	}
	active := u.state.Jobs.Active()
	status := i18n.T("Ready")
	if active > 0 {
		status = i18n.N(active, "Converting %d file…", "Converting %d files…")
	}
	if busy := active > 0; busy != u.trayBusy {
		u.trayBusy = busy
		if busy {
			d.SetSystemTrayIcon(busyIcon)
		} else {
			d.SetSystemTrayIcon(appIcon)
		}
	}
	note := fyne.NewMenuItem(status, nil)
	note.Disabled = true
	items := []*fyne.MenuItem{
		note,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(i18n.T("Convert Files…"), func() {
			u.pickFiles(u.main.win, func(files []string) { u.addFiles(files, u.main.win) })
		}),
	}
	if recent := u.state.RecentOutputs(recentInTray); len(recent) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
		head := fyne.NewMenuItem(i18n.T("Show in folder"), nil)
		head.Disabled = true
		items = append(items, head)
		for _, p := range recent {
			path := p
			items = append(items, fyne.NewMenuItem(trayLabel(filepath.Base(path)), func() { app.Reveal(path) }))
		}
	}
	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem(i18n.T("Open Conversor"), func() { u.main.show() }),
		fyne.NewMenuItem(i18n.T("Settings…"), func() { u.showSettings("") }),
	)
	quit := fyne.NewMenuItem(i18n.T("Quit Conversor"), u.Quit)
	quit.IsQuit = true
	items = append(items, fyne.NewMenuItemSeparator(), quit)
	d.SetSystemTrayMenu(fyne.NewMenu("Conversor", items...))
}
