package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"conversor/internal/app"
	"conversor/internal/core"
)

type copier struct{}

func (copier) ID() string          { return "copy" }
func (copier) Priority() int       { return 0 }
func (copier) Unavailable() string { return "" }
func (copier) Steps() []core.Step {
	return core.Steps([]string{"png", "heic"}, []string{"jpeg", "webp", "png"})
}
func (copier) Convert(t *core.Task, input, outDir string) ([]string, error) {
	data, err := os.ReadFile(input)
	if err != nil {
		return nil, err
	}
	out := t.Artifact(outDir, 0)
	return []string{out}, os.WriteFile(out, data, 0o644)
}

func setup(t *testing.T, firstRunDone bool) (*UI, *app.State, string) {
	t.Helper()
	fy := test.NewTempApp(t)
	dir := t.TempDir()
	r := core.NewRegistry()
	r.Register(copier{})
	st, err := app.NewState(app.Paths{Settings: filepath.Join(dir, "settings.toml"), History: filepath.Join(dir, "h.jsonl"), Presets: filepath.Join(dir, "presets")}, r, app.Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	if firstRunDone {
		st.UpdateSettings(func(s *app.Settings) { s.FirstRunDone = true; s.Automations = nil })
	}
	u := New(fy, st, "test")
	u.refreshMu.Lock()
	u.later = nil
	u.refreshMu.Unlock()
	return u, st, dir
}

func file(t *testing.T, dir, name string) string {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("pixels"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitIdle(t *testing.T, st *app.State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for st.Busy() {
		if time.Now().After(deadline) {
			t.Fatal("never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// find returns the first button whose text starts with prefix.
func find(t *testing.T, root fyne.CanvasObject, prefix string) *widget.Button {
	t.Helper()
	var found *widget.Button
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		if found != nil || o == nil {
			return
		}
		switch x := o.(type) {
		case *widget.Button:
			if strings.HasPrefix(x.Text, prefix) {
				found = x
			}
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		case fyne.Widget:
			r := test.TempWidgetRenderer(t, x)
			for _, c := range r.Objects() {
				walk(c)
			}
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("no button %q", prefix)
	}
	return found
}

func TestFirstRunOnFreshInstall(t *testing.T) {
	u, st, _ := setup(t, false)
	u.Start(app.Request{})
	if u.firstRun == nil {
		t.Fatal("a fresh install shows the welcome window")
	}
	test.Tap(find(t, u.firstRun.Content(), "Start converting"))
	if !st.Settings().FirstRunDone || !u.main.visible {
		t.Fatal("Start converting finishes setup and opens the main window")
	}
}

func TestQuickConvertPicksTargetOptionsAndName(t *testing.T) {
	u, st, dir := setup(t, true)
	src := file(t, dir, "photo.png")
	u.Start(app.Request{Files: []string{src}})
	if len(u.quicks) != 1 {
		t.Fatal("files without a target open Quick convert")
	}
	var q *quickWindow
	for q = range u.quicks {
	}
	if !q.convertBtn.Disabled() {
		t.Fatal("Convert waits for a target")
	}
	test.Tap(find(t, q.targetBox, "JPEG"))
	if q.to == nil || q.to.ID != "jpeg" || q.convertBtn.Disabled() {
		t.Fatal("picking JPEG enables Convert")
	}
	if q.nameEntry.Text != "photo.jpg" {
		t.Fatalf("name %q", q.nameEntry.Text)
	}
	q.nameEntry.SetText("renamed")
	test.Tap(q.convertBtn)
	waitIdle(t, st)
	if _, err := os.Stat(filepath.Join(dir, "renamed.jpg")); err != nil {
		t.Fatal("the typed name wins")
	}
	q.refresh()
	if q.convertBtn.Text != "Done" {
		t.Fatal(q.convertBtn.Text)
	}
	// Activity lists it from history.
	u.main.refresh()
	if len(u.main.rows) != 1 || u.main.rows[0].rec.Outcome != app.Done {
		t.Fatalf("%+v", u.main.rows)
	}
}

func TestBadNameIsRefused(t *testing.T) {
	u, _, dir := setup(t, true)
	u.Start(app.Request{Files: []string{file(t, dir, "a.png")}})
	var q *quickWindow
	for q = range u.quicks {
	}
	test.Tap(find(t, q.targetBox, "JPEG"))
	q.nameEntry.SetText("sub/dir")
	test.Tap(q.convertBtn)
	if len(q.jobs) != 0 || !strings.Contains(q.errLabel.Text, "not a folder path") {
		t.Fatalf("jobs %v, error %q", q.jobs, q.errLabel.Text)
	}
}

func TestAutoStartConvertsWithoutAWindow(t *testing.T) {
	u, st, dir := setup(t, true)
	u.Start(app.Request{})
	u.Handle(app.Request{Files: []string{file(t, dir, "b.png")}, To: "webp"})
	waitIdle(t, st)
	if len(u.quicks) != 0 {
		t.Fatal("a request with a reachable target converts silently")
	}
	if _, err := os.Stat(filepath.Join(dir, "b.webp")); err != nil {
		t.Fatal(err)
	}
	// An unreachable target asks in a window instead.
	u.Handle(app.Request{Files: []string{file(t, dir, "c.png")}, To: "mp3"})
	if len(u.quicks) != 1 {
		t.Fatal("unreachable target should open Quick convert")
	}
	for q := range u.quicks {
		if !strings.Contains(q.errLabel.Text, "MP3") {
			t.Fatal(q.errLabel.Text)
		}
	}
}

func TestSettingsSavesAPreset(t *testing.T) {
	u, st, _ := setup(t, true)
	u.Start(app.Request{})
	u.showSettings("presets")
	f := u.settings.presetForm
	f.name.SetText("web")
	f.to.SetSelected("WebP")
	f.quality.SetText("80")
	f.maxSize.SetText("2048")
	u.settings.savePreset()
	p, err := st.LoadPreset("web")
	if err != nil || p.To != "webp" || *p.Options.Quality != 80 || *p.Options.MaxSize != 2048 {
		t.Fatalf("%v %+v", err, p)
	}
	if len(u.settings.presetNames) != 1 {
		t.Fatal("list not refreshed")
	}
	for _, tab := range []string{"general", "tools", "menu"} {
		u.showSettings(tab)
	}
}

func TestWhen(t *testing.T) {
	now := time.Date(2026, 3, 1, 15, 0, 0, 0, time.Local)
	for at, want := range map[time.Time]string{
		now.Add(-time.Hour):      "2:00 PM",
		now.Add(-20 * time.Hour): "Yesterday",
		now.AddDate(0, 0, -5):    "Feb 24",
		now.AddDate(-1, 0, 0):    "Mar 1, 2025",
	} {
		if got := when(at.Unix(), now); got != want {
			t.Fatalf("%v: %q, want %q", at, got, want)
		}
	}
}
