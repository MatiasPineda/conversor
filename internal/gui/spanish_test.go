package gui

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"

	"conversor/internal/app"
	"conversor/internal/core"
	"conversor/internal/i18n"
)

func TestSpanishWindows(t *testing.T) {
	t.Cleanup(func() { i18n.Set(i18n.English) })
	i18n.Set(i18n.Spanish)
	u, st, dir := setup(t, false)
	u.Start(app.Request{})
	test.Tap(find(t, u.firstRun.Content(), "Empezar a convertir"))
	if !st.Settings().FirstRunDone {
		t.Fatal("the Spanish welcome window finishes setup")
	}

	u.Handle(app.Request{Files: []string{file(t, dir, "foto.png")}})
	var q *quickWindow
	for q = range u.quicks {
	}
	if q.win.Title() != "Conversión rápida" || q.convertBtn.Text != "Convertir" {
		t.Fatalf("%q %q", q.win.Title(), q.convertBtn.Text)
	}
	test.Tap(find(t, q.targetBox, "JPEG"))
	test.Tap(q.convertBtn)
	waitIdle(t, st)
	if _, err := os.Stat(filepath.Join(dir, "foto.jpg")); err != nil {
		t.Fatal(err)
	}

	// Backgrounds are picked by their Spanish names.
	u.showSettings("presets")
	f := u.settings.presetForm
	f.name.SetText("web")
	f.to.SetSelected("JPEG")
	f.background.SetSelected("Negro")
	u.settings.savePreset()
	p, err := st.LoadPreset("web")
	if err != nil || p.Options.Background == nil || *p.Options.Background != core.Black {
		t.Fatalf("%v %+v", err, p)
	}
	if u.settings.tabs.Selected().Text != "Preajustes" {
		t.Fatal(u.settings.tabs.Selected().Text)
	}
}
