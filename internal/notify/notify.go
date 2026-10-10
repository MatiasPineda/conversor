// Package notify shows desktop notifications with whatever the platform
// offers: notify-send or gdbus on Linux, osascript on macOS and a toast
// through PowerShell on Windows. Failures are ignored; a notification is
// never worth failing a conversion over.
package notify

import (
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Send shows a notification and waits briefly for the tool to finish.
func Send(title, body string) {
	cmd := command(title, body)
	if cmd == nil {
		return
	}
	hide(cmd)
	if cmd.Start() != nil {
		return
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

func command(title, body string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		script := "display notification " + appleString(body) + " with title " + appleString(title)
		return exec.Command("osascript", "-e", script)
	case "windows":
		return exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", toastScript(title, body))
	default:
		if p, err := exec.LookPath("notify-send"); err == nil {
			return exec.Command(p, "--app-name=Conversor", "--icon=document-save", title, body)
		}
		if p, err := exec.LookPath("gdbus"); err == nil {
			return exec.Command(p, "call", "--session", "--dest=org.freedesktop.Notifications",
				"--object-path=/org/freedesktop/Notifications", "--method=org.freedesktop.Notifications.Notify",
				"Conversor", "0", "document-save", title, body, "[]", "{}", "5000")
		}
	}
	return nil
}

func appleString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func psString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// toastScript shows a Windows toast under PowerShell's app id, which every
// Windows 10 and 11 install has registered.
func toastScript(title, body string) string {
	xml := "<toast><visual><binding template=\"ToastGeneric\"><text>" + xmlEscape(title) +
		"</text><text>" + xmlEscape(body) + "</text></binding></visual></toast>"
	return `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null;
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null;
$x = New-Object Windows.Data.Xml.Dom.XmlDocument; $x.LoadXml(` + psString(xml) + `);
$id = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe';
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($id).Show([Windows.UI.Notifications.ToastNotification]::new($x))`
}
