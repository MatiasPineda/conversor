package gui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"conversor/internal/app"
	"conversor/internal/core"
	"conversor/internal/i18n"
)

func save(t *testing.T, w fyne.Window, name string) {
	out := os.Getenv("CAPTURE_DIR")
	f, _ := os.Create(filepath.Join(out, name+".png"))
	defer f.Close()
	png.Encode(f, w.Canvas().Capture())
}

// TestCapture renders the main windows to PNGs for a visual check:
// CAPTURE_DIR=/tmp/shots go test -run TestCapture ./internal/gui
// CAPTURE_LANG=es renders them in Spanish.
func TestCapture(t *testing.T) {
	if os.Getenv("CAPTURE_DIR") == "" {
		t.Skip()
	}
	if l := os.Getenv("CAPTURE_LANG"); l != "" {
		i18n.Set(l)
		defer i18n.Set(i18n.English)
	}
	u, st, dir := setup(t, true)
	test.ApplyTheme(t, newTheme())
	a := file(t, dir, "holiday photo.png")
	b := file(t, dir, "IMG_2041.heic")
	st.Convert([]string{a}, core.MustID("jpeg"), core.Options{})
	waitIdle(t, st)
	st.Convert([]string{b}, core.MustID("webp"), core.Options{})
	waitIdle(t, st)
	u.Start(app.Request{})
	u.main.win.Resize(fyne.NewSize(760, 520))
	u.main.refresh()
	time.Sleep(50 * time.Millisecond)
	save(t, u.main.win, "main")
	u.main.showPage(pageAutomations)
	st.UpdateSettings(func(s *app.Settings) { s.Automations = app.ExampleAutomations() })
	u.main.refresh()
	save(t, u.main.win, "automations")
	u.Handle(app.Request{Files: []string{a}})
	for q := range u.quicks {
		q.win.Resize(fyne.NewSize(620, 640))
		test.Tap(find(t, q.targetBox, "WebP"))
		save(t, q.win, "quick")
	}
	u.showSettings("general")
	u.settings.win.Resize(fyne.NewSize(680, 560))
	save(t, u.settings.win, "settings-general")
	u.showSettings("presets")
	save(t, u.settings.win, "settings-presets")
	u.showSettings("tools")
	save(t, u.settings.win, "settings-tools")
}
