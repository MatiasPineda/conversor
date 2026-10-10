//go:build !linux

package gui

// trayAvailable is true where the OS always has a tray (Windows, macOS).
func trayAvailable() bool { return true }
