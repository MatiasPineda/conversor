package app

import (
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Reveal shows a file selected in the system file manager.
func Reveal(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", "/select,"+path)
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	default:
		uri := (&url.URL{Scheme: "file", Path: path}).String()
		if p, err := exec.LookPath("dbus-send"); err == nil {
			cmd = exec.Command(p, "--session", "--dest=org.freedesktop.FileManager1", "--type=method_call",
				"/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItems", "array:string:"+uri, "string:")
			if cmd.Run() == nil {
				return nil
			}
		}
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	hideWindow(cmd)
	// explorer.exe exits 1 even when it worked.
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// Open opens a file or folder with its default app.
func Open(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// CopyImage puts an image file on the clipboard as an image, where the
// platform has a way to: PowerShell on Windows, osascript on macOS, wl-copy
// or xclip on Linux. It reports false when it couldn't.
func CopyImage(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		script := "Add-Type -AssemblyName System.Windows.Forms,System.Drawing; " +
			"$i=[System.Drawing.Image]::FromFile('" + strings.ReplaceAll(path, "'", "''") + "'); " +
			"[System.Windows.Forms.Clipboard]::SetImage($i); $i.Dispose()"
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden", "-Command", script)
	case "darwin":
		class := map[string]string{".png": "PNGf", ".jpg": "JPEG", ".jpeg": "JPEG", ".tiff": "TIFF", ".tif": "TIFF", ".gif": "GIFf"}[ext]
		if class == "" {
			return false
		}
		quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path) + `"`
		cmd = exec.Command("osascript", "-e", "set the clipboard to (read (POSIX file "+quoted+") as «class "+class+"»)")
	default:
		mime := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif", ".bmp": "image/bmp"}[ext]
		if mime == "" {
			return false
		}
		if p, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("sh", "-c", `exec "$0" --type "$1" < "$2"`, p, mime, path)
		} else if p, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command(p, "-selection", "clipboard", "-t", mime, "-i", path)
		} else {
			return false
		}
	}
	hideWindow(cmd)
	return cmd.Run() == nil
}

func hideWindow(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		setHideWindow(cmd)
	}
}
