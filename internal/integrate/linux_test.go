//go:build linux

package integrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"conversor/internal/core"
)

type fake struct{ steps [][2]string }

func (f fake) ID() string          { return "fake" }
func (f fake) Priority() int       { return 0 }
func (f fake) Unavailable() string { return "" }
func (f fake) Steps() []core.Step {
	var out []core.Step
	for _, s := range f.steps {
		out = append(out, core.Step{From: core.MustID(s[0]), To: core.MustID(s[1])})
	}
	return out
}
func (f fake) Convert(*core.Task, string, string) ([]string, error) { return nil, nil }

func TestInstallAndUninstallEveryFileManager(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	// The user's own Thunar action must survive both install and uninstall.
	uca := filepath.Join(home, "config", "Thunar", "uca.xml")
	os.MkdirAll(filepath.Dir(uca), 0o755)
	mine := "<action><name>Mine</name><unique-id>1</unique-id></action>"
	os.WriteFile(uca, []byte("<?xml version=\"1.0\"?>\n<actions>\n"+mine+"\n</actions>\n"), 0o644)

	r := core.NewRegistry()
	r.Register(fake{[][2]string{{"heic", "jpeg"}, {"heic", "png"}, {"png", "jpeg"}, {"mov", "mp4"}}})
	res, err := Install(r, FileManagers)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Done) == 0 {
		t.Fatal("nothing written")
	}
	read := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	py := read("data/nautilus-python/extensions/conversor.py")
	if !strings.Contains(py, `"heic": [("jpeg", "JPEG"), ("png", "PNG")]`) || !strings.Contains(py, `"heif":`) {
		t.Fatal(py)
	}
	dolphin := read("data/kio/servicemenus/conversor-heic.desktop")
	if !strings.Contains(dolphin, "MimeType=image/heic;") || !strings.Contains(dolphin, "--notify --to jpeg -- %F") {
		t.Fatal(dolphin)
	}
	if info, _ := os.Stat(filepath.Join(home, "data/kio/servicemenus/conversor-heic.desktop")); info.Mode()&0o100 == 0 {
		t.Fatal("KDE 6 service menus must be executable")
	}
	nemo := read("data/nemo/actions/conversor-jpeg.nemo_action")
	if !strings.Contains(nemo, "Extensions=") || !strings.Contains(nemo, "heic;HEIC;") || !strings.Contains(nemo, "png;PNG;") {
		t.Fatal(nemo)
	}
	thunar := read("config/Thunar/uca.xml")
	if !strings.Contains(thunar, mine) || strings.Count(thunar, "<unique-id>conversor-") != 3 {
		t.Fatal(thunar)
	}
	// Installing twice doesn't duplicate Thunar actions.
	Install(r, []string{"thunar"})
	if got := read("config/Thunar/uca.xml"); strings.Count(got, "<unique-id>conversor-") != 3 {
		t.Fatal(got)
	}
	if len(Status()) < 4 {
		t.Fatal(Status())
	}

	if _, err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	if left := Status(); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	if got := read("config/Thunar/uca.xml"); !strings.Contains(got, mine) || strings.Contains(got, "conversor") {
		t.Fatal(got)
	}
}

func TestDesktopArgQuoting(t *testing.T) {
	if got := desktopArg("/usr/bin/conversor"); got != "/usr/bin/conversor" {
		t.Fatal(got)
	}
	if got := desktopArg(`/home/a b/$x`); got != `"/home/a b/\\$x"` {
		t.Fatal(got)
	}
}

func TestAppLauncherAddsMoreOptionsAndADesktopEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	r := core.NewRegistry()
	r.Register(fake{[][2]string{{"heic", "jpeg"}}})
	l := Launcher{Exe: "/opt/conversor/conversor-app", App: true}
	if _, err := InstallWith(r, FileManagers, l); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if d := read("data/kio/servicemenus/conversor-heic.desktop"); !strings.Contains(d, "Exec=/opt/conversor/conversor-app open --to jpeg -- %F") || !strings.Contains(d, "[Desktop Action more]") {
		t.Fatal(d)
	}
	if py := read("data/nautilus-python/extensions/conversor.py"); !strings.Contains(py, `MORE_ARGS = ["open", "--"]`) {
		t.Fatal(py)
	}
	if n := read("data/nemo/actions/conversor-zz-more.nemo_action"); !strings.Contains(n, "conversor-app open -- %F") {
		t.Fatal(n)
	}
	if th := read("config/Thunar/uca.xml"); !strings.Contains(th, "<unique-id>conversor-more</unique-id>") {
		t.Fatal(th)
	}
	if e := read("data/applications/conversor.desktop"); !strings.Contains(e, "Exec=/opt/conversor/conversor-app %F") || strings.Contains(e, "MimeType") {
		t.Fatal(e)
	}
	Uninstall()
	if _, err := os.Stat(filepath.Join(home, "data/applications/conversor.desktop")); err == nil {
		t.Fatal("desktop entry left behind")
	}
}
