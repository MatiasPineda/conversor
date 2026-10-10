//go:build linux

package gui

import "github.com/godbus/dbus/v5"

// trayAvailable reports whether the desktop shows StatusNotifierItem icons.
// GNOME without the AppIndicator extension, and WSLg, don't, and then the
// app must not hide in a tray nobody can see.
func trayAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	var has bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&has)
	return err == nil && has
}
