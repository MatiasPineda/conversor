package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"conversor/internal/core"
)

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

func TestSkillDocumentsEverything(t *testing.T) {
	root := NewRoot(&bytes.Buffer{}, &bytes.Buffer{})
	text := renderSkill(root)
	if !strings.HasPrefix(text, "---\nname: conversor\n") || !strings.Contains(text, "\ndescription:") {
		t.Fatal("frontmatter")
	}
	if strings.Contains(text, "@") || strings.Contains(text, "{{") {
		t.Fatal("skill has an @ or an unfilled placeholder")
	}
	for _, f := range core.Formats {
		if !strings.Contains(text, "`"+f.ID+"`") {
			t.Fatalf("format %s missing", f.ID)
		}
	}
	// Every visible flag of every command is documented...
	var walk func(*cobra.Command)
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("(?:^|[^-])--([a-z][a-z0-9-]*)").FindAllStringSubmatch(text, -1) {
		documented[m[1]] = true
	}
	walk = func(c *cobra.Command) {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if !f.Hidden && !documented[f.Name] {
				t.Errorf("--%s (%s) is not in the skill", f.Name, c.Name())
			}
		})
		for _, sub := range c.Commands() {
			if !sub.Hidden && sub.Name() != "help" {
				if !regexp.MustCompile(`conversor [a-z ]*\b` + sub.Name() + `\b`).MatchString(text) {
					t.Errorf("command %s is not in the skill", sub.Name())
				}
				walk(sub)
			}
		}
	}
	walk(root)
	// ...and every documented flag exists.
	all := map[string]bool{"help": true, "version": true}
	var collect func(*cobra.Command)
	collect = func(c *cobra.Command) {
		c.LocalFlags().VisitAll(func(f *pflag.Flag) { all[f.Name] = true })
		for _, sub := range c.Commands() {
			collect(sub)
		}
	}
	collect(root)
	for name := range documented {
		if !all[name] {
			t.Errorf("the skill documents --%s, which doesn't exist", name)
		}
	}
}

func TestConvertJSONEndToEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CONVERSOR_CONFIG_DIR", filepath.Join(dir, "cfg"))
	src := filepath.Join(dir, "in", "pic.png")
	os.MkdirAll(filepath.Join(dir, "in", "sub"), 0o755)
	f, _ := os.Create(src)
	png.Encode(f, image.NewNRGBA(image.Rect(0, 0, 8, 8)))
	f.Close()
	data, _ := os.ReadFile(src)
	os.WriteFile(filepath.Join(dir, "in", "sub", "deep.png"), data, 0o644)
	os.WriteFile(filepath.Join(dir, "in", "notes.txt"), []byte("hi"), 0o644)

	out, stderr, code := runCLI(t, filepath.Join(dir, "in"), "-r", "--to", "bmp", "--json", "-o", filepath.Join(dir, "out"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var events []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var e map[string]any
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("bad JSON line %q", sc.Text())
		}
		events = append(events, e)
	}
	last := events[len(events)-1]
	if last["event"] != "summary" || last["done"] != float64(2) || last["failed"] != float64(0) {
		t.Fatalf("summary %v", last)
	}
	for _, p := range []string{"out/pic.bmp", "out/sub/deep.bmp"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("%s missing", p)
		}
	}

	// A named file that can't convert fails with a stable kind and exit 1.
	out, _, code = runCLI(t, filepath.Join(dir, "in", "notes.txt"), "--to", "png", "--json")
	if code != 1 || !strings.Contains(out, `"kind":"no_route"`) && !strings.Contains(out, `"kind":"engine_missing"`) {
		t.Fatalf("exit %d: %s", code, out)
	}
	// Presets fill in --to and options; flags win.
	os.MkdirAll(filepath.Join(dir, "cfg", "presets"), 0o755)
	os.WriteFile(filepath.Join(dir, "cfg", "presets", "small.toml"), []byte("to = \"jpeg\"\nmax_size = 4\nquality = 50\n"), 0o644)
	_, stderr, code = runCLI(t, src, "-p", "small", "-q", "90")
	if code != 0 || !strings.Contains(stderr, "pic.jpg") {
		t.Fatalf("preset run: %d %s", code, stderr)
	}
	presets, _, _ := runCLI(t, "presets")
	if !strings.Contains(presets, "small") || !strings.Contains(presets, "jpeg") {
		t.Fatal(presets)
	}
}

func TestListCommands(t *testing.T) {
	out, _, code := runCLI(t, "formats", "--json")
	var list []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list) != len(core.Formats) {
		t.Fatalf("formats --json: %d", code)
	}
	out, _, _ = runCLI(t, "targets", "photo.heic", "--menu")
	if out != "jpeg\npng\nwebp\n" {
		t.Fatalf("menu targets %q", out)
	}
	if _, _, code := runCLI(t, "targets", "a.unknown"); code != 1 {
		t.Fatal("unknown format should fail")
	}
	if out, _, _ := runCLI(t, "--version"); !strings.HasPrefix(out, "conversor ") {
		t.Fatal(out)
	}
}

func TestBadValuesAreRejectedBeforeConverting(t *testing.T) {
	for _, args := range [][]string{
		{"x.png", "--to", "png", "-q", "0"},
		{"x.png", "--to", "jpeg", "--background", "transparent"},
		{"x.png", "--to", "png", "--pages", "3-1"},
		{"x.png", "--to", "nope"},
		{"x.png"},
	} {
		if _, stderr, code := runCLI(t, args...); code != 1 || !strings.HasPrefix(stderr, "Error: ") {
			t.Fatalf("%v: exit %d %q", args, code, stderr)
		}
	}
}
