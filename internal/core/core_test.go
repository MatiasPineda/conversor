package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFormatLookups(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Formats {
		if seen[f.ID] {
			t.Fatalf("duplicate id %s", f.ID)
		}
		seen[f.ID] = true
	}
	if ByID("JPG").ID != "jpeg" {
		t.Fatal("JPG should find jpeg")
	}
	if ByPath("a/IMG_1.HEIC").ID != "heic" {
		t.Fatal("HEIC extension")
	}
	if ByPath("noext") != nil {
		t.Fatal("no extension should find nothing")
	}
}

func TestPageRanges(t *testing.T) {
	r, _ := ParsePageRange("2-3")
	if r.Contains(0) || !r.Contains(1) || !r.Contains(2) || r.Contains(3) {
		t.Fatal("2-3")
	}
	r, _ = ParsePageRange("4-")
	if r.Contains(2) || !r.Contains(3) || !r.Contains(500) {
		t.Fatal("4-")
	}
	if r, _ := ParsePageRange("5"); r.String() != "5" {
		t.Fatal(r.String())
	}
	for _, bad := range []string{"0", "3-1", "a", "-2", ""} {
		if _, err := ParsePageRange(bad); err == nil {
			t.Fatalf("%q should fail", bad)
		}
	}
}

func TestPresets(t *testing.T) {
	p, err := ParsePreset("to = \"jpeg\"\nquality = 80\npages = \"1-2\"")
	if err != nil {
		t.Fatal(err)
	}
	if p.To != "jpeg" || *p.Options.Quality != 80 || p.Options.Pages.String() != "1-2" {
		t.Fatalf("%+v", p)
	}
	for _, bad := range []string{"quality = 0", "qualty = 50", "to = \"nope\"", "video_codec = \"vp8\"", "strip_audio = \"yes\"", "background = \"grey\""} {
		if _, err := ParsePreset(bad); err == nil {
			t.Fatalf("%q should fail", bad)
		}
	}
	back, err := ParsePreset(p.TOML())
	if err != nil || back.TOML() != p.TOML() {
		t.Fatalf("round trip: %v\n%s", err, p.TOML())
	}
	if (Preset{}).TOML() != "" {
		t.Fatalf("empty preset: %q", Preset{}.TOML())
	}
	merged := Options{Quality: Ptr(50)}.Or(p.Options)
	if *merged.Quality != 50 || merged.Pages != p.Options.Pages {
		t.Fatal("merge")
	}

	old, _ := ParsePreset("to = \"mp4\"\nquality = 70")
	if old.Options.VideoCodec != nil || old.Options.StripAudio {
		t.Fatal("defaults")
	}
	if s := old.TOML(); strings.Contains(s, "codec") || strings.Contains(s, "audio") || strings.Contains(s, "background") {
		t.Fatalf("defaults serialized: %s", s)
	}
	v, err := ParsePreset("to = \"mkv\"\nvideo_codec = \"hevc\"\nstrip_audio = true\nbackground = \"black\"")
	if err != nil || *v.Options.VideoCodec != HEVC || !v.Options.StripAudio || *v.Options.Background != Black {
		t.Fatalf("%v %+v", err, v)
	}
	if back, _ := ParsePreset(v.TOML()); back.TOML() != v.TOML() {
		t.Fatal("round trip")
	}
}

func TestBackgrounds(t *testing.T) {
	for text, want := range map[string]Background{
		"transparent": Transparent, "White": White, "BLACK": Black,
		"#ff8800": {Color: [3]uint8{255, 136, 0}}, "FF8800": {Color: [3]uint8{255, 136, 0}},
		"#08f": {Color: [3]uint8{0, 136, 255}},
	} {
		if got, err := ParseBackground(text); err != nil || got != want {
			t.Fatalf("%s: %v %v", text, got, err)
		}
	}
	for _, bad := range []string{"", "#12", "#1234567", "#gggggg", "grey", "#"} {
		if _, err := ParseBackground(bad); err == nil {
			t.Fatalf("%q should fail", bad)
		}
	}
	if (Background{Color: [3]uint8{255, 255, 255}}).ID() != "white" {
		t.Fatal("white id")
	}
	if (Background{Color: [3]uint8{1, 2, 255}}).Name() != "#0102FF" {
		t.Fatal("name")
	}
}

func TestPresetDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "good.toml"), []byte(`to = "png"`), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.toml"), []byte(`to = "zzz"`), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o644)
	all, err := LoadPresetDir(dir)
	if err != nil || len(all) != 2 || all[0].Name != "bad" || all[0].Err == nil || all[1].Err != nil {
		t.Fatalf("%v %+v", err, all)
	}
	if none, err := LoadPresetDir(filepath.Join(dir, "missing")); err != nil || none != nil {
		t.Fatal("missing dir")
	}
}

// fake copies its input for every step it declares.
type fake struct {
	id       string
	priority int
	steps    [][2]string
}

func (f *fake) ID() string          { return f.id }
func (f *fake) Priority() int       { return f.priority }
func (f *fake) Unavailable() string { return "" }
func (f *fake) Steps() []Step {
	var out []Step
	for _, s := range f.steps {
		out = append(out, Step{MustID(s[0]), MustID(s[1])})
	}
	return out
}
func (f *fake) Convert(t *Task, input, outDir string) ([]string, error) {
	data, err := os.ReadFile(input)
	if err != nil {
		return nil, err
	}
	out := t.Artifact(outDir, 0)
	return []string{out}, os.WriteFile(out, data, 0o644)
}

// pager writes pages artifacts, each the input bytes plus a page marker.
type pager struct {
	fake
	pages int
	delay time.Duration
}

func newPager(id, from, to string, pages int) *pager {
	return &pager{fake: fake{id: id, steps: [][2]string{{from, to}}}, pages: pages}
}

func (p *pager) Convert(t *Task, input, outDir string) ([]string, error) {
	data, err := os.ReadFile(input)
	if err != nil {
		return nil, err
	}
	var out []string
	for page := range p.pages {
		time.Sleep(p.delay)
		if err := t.Check(); err != nil {
			return nil, err
		}
		path := t.Artifact(outDir, page)
		if err := os.WriteFile(path, fmt.Appendf(slices.Clone(data), "|%s%d", p.id, page), 0o644); err != nil {
			return nil, err
		}
		out = append(out, path)
		t.Progress(float64(page+1) / float64(p.pages))
	}
	return out, nil
}

func listing(t *testing.T, dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func describe(t *testing.T, r *Registry, from, to string) string {
	plan, err := r.Plan(MustID(from), MustID(to))
	if err != nil {
		t.Fatal(err)
	}
	return plan.Describe()
}

func TestPicksDirectRouteAndPriority(t *testing.T) {
	r := NewRegistry()
	r.Register(&fake{"slow", 0, [][2]string{{"heic", "jpeg"}}})
	r.Register(&fake{"fast", 10, [][2]string{{"heic", "jpeg"}}})
	if got := describe(t, r, "heic", "jpeg"); got != "heic -[fast]-> jpeg" {
		t.Fatal(got)
	}
}

func TestChainsEngines(t *testing.T) {
	r := NewRegistry()
	r.Register(&fake{"img", 0, [][2]string{{"heic", "png"}}})
	r.Register(&fake{"pdf", 0, [][2]string{{"png", "pdf"}}})
	if got := describe(t, r, "heic", "pdf"); got != "heic -[img]-> png -[pdf]-> pdf" {
		t.Fatal(got)
	}
	if _, err := r.Plan(MustID("pdf"), MustID("heic")); Kind(err) != KindNoRoute {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range r.Targets(MustID("heic")) {
		ids = append(ids, f.ID)
	}
	if !slices.Equal(ids, []string{"png", "pdf"}) {
		t.Fatal(ids)
	}
}

func TestEqualRoutesAreDeterministic(t *testing.T) {
	for range 50 {
		r := NewRegistry()
		r.Register(&fake{"img", 0, [][2]string{{"exr", "jpeg"}, {"exr", "tiff"}, {"exr", "bmp"}}})
		r.Register(&fake{"heif", 0, [][2]string{{"jpeg", "heic"}, {"tiff", "heic"}, {"bmp", "heic"}}})
		if got := describe(t, r, "exr", "heic"); got != "exr -[img]-> tiff -[heif]-> heic" {
			t.Fatal(got)
		}
	}
}

func TestMenusOfferReachablePopularTargets(t *testing.T) {
	r := NewRegistry()
	r.Register(&fake{"img", 0, [][2]string{{"heic", "png"}, {"png", "jpeg"}, {"png", "webp"}, {"png", "gif"}, {"png", "ico"}, {"png", "heic"}}})
	r.Register(&fake{"x", 0, [][2]string{{"flac", "opus"}}})
	ids := func(from string) []string {
		var out []string
		for _, f := range r.MenuTargets(MustID(from)) {
			out = append(out, f.ID)
		}
		return out
	}
	if got := ids("heic"); !slices.Equal(got, []string{"jpeg", "png", "webp"}) {
		t.Fatal(got)
	}
	if got := ids("png"); !slices.Equal(got, []string{"jpeg", "webp"}) {
		t.Fatal(got)
	}
	if got := ids("flac"); !slices.Equal(got, []string{"opus"}) {
		t.Fatal(got)
	}
}

func TestStillsOnlyBecomeVideoDirectly(t *testing.T) {
	r := NewRegistry()
	r.Register(&fake{"svg", 0, [][2]string{{"svg", "png"}}})
	r.Register(&fake{"img", 0, [][2]string{{"png", "gif"}}})
	r.Register(&fake{"ffmpeg", 0, [][2]string{{"gif", "mp4"}, {"mp4", "webm"}}})
	if _, err := r.Plan(MustID("svg"), MustID("mp4")); err == nil {
		t.Fatal("svg -> mp4 should have no route")
	}
	if _, err := r.Plan(MustID("gif"), MustID("mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plan(MustID("gif"), MustID("webm")); err == nil {
		t.Fatal("gif -> webm via mp4 should have no route")
	}
}

func TestRunCarriesEveryPageThroughLaterHops(t *testing.T) {
	r := NewRegistry()
	r.Register(newPager("pdf", "pdf", "png", 3))
	r.Register(newPager("img", "png", "webp", 1))
	dir := t.TempDir()
	input := filepath.Join(dir, "doc.pdf")
	os.WriteFile(input, []byte("src"), 0o644)
	var mu sync.Mutex
	var progress []float64
	out, err := r.Run(context.Background(), &Job{Input: input, To: MustID("webp")}, func(p float64) {
		mu.Lock()
		progress = append(progress, p)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := listing(t, dir); !slices.Equal(got, []string{"doc-2.webp", "doc-3.webp", "doc.pdf", "doc.webp"}) {
		t.Fatal(got)
	}
	for i, path := range out {
		data, _ := os.ReadFile(path)
		if want := fmt.Sprintf("src|pdf%d|img0", i); string(data) != want {
			t.Fatalf("%s: %s, want %s", path, data, want)
		}
	}
	if progress[len(progress)-1] != 1 {
		t.Fatal("last progress should be 1")
	}
	for i := 1; i < len(progress); i++ {
		if progress[i] < progress[i-1] {
			t.Fatalf("progress went backwards: %v", progress)
		}
	}
}

func TestExistingFilesAndInputAreNeverReplaced(t *testing.T) {
	r := NewRegistry()
	r.Register(newPager("a", "png", "jpeg", 1))
	dir := t.TempDir()
	input := filepath.Join(dir, "x.png")
	os.WriteFile(input, []byte("in"), 0o644)
	os.WriteFile(filepath.Join(dir, "x.jpg"), []byte("keep"), 0o644)
	out, err := r.Run(context.Background(), &Job{Input: input, To: MustID("jpeg")}, nil)
	if err != nil || !slices.Equal(out, []string{filepath.Join(dir, "x (1).jpg")}) {
		t.Fatal(out, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "x.jpg")); string(data) != "keep" {
		t.Fatal("x.jpg replaced")
	}
	exact := &Job{Input: input, To: MustID("jpeg"), Output: Output{Exact, filepath.Join(dir, "x.jpg")}}
	if _, err := r.Run(context.Background(), exact, nil); Kind(err) != KindOutputExists {
		t.Fatal(err)
	}
	if got := listing(t, dir); !slices.Equal(got, []string{"x (1).jpg", "x.jpg", "x.png"}) {
		t.Fatal(got)
	}
}

func TestExactOutputKeepsTheRequestedName(t *testing.T) {
	r := NewRegistry()
	r.Register(newPager("a", "png", "jpeg", 1))
	dir := t.TempDir()
	input := filepath.Join(dir, "x.png")
	os.WriteFile(input, []byte("in"), 0o644)
	for _, name := range []string{"wanted.jpeg", "bare"} {
		job := &Job{Input: input, To: MustID("jpeg"), Output: Output{Exact, filepath.Join(dir, name)}}
		out, err := r.Run(context.Background(), job, nil)
		if err != nil || !slices.Equal(out, []string{filepath.Join(dir, name)}) {
			t.Fatal(out, err)
		}
	}
	if got := listing(t, dir); !slices.Equal(got, []string{"bare", "wanted.jpeg", "x.png"}) {
		t.Fatal(got)
	}
}

func TestSimultaneousJobsWithTheSameNameBothSurvive(t *testing.T) {
	r := NewRegistry()
	p := newPager("a", "png", "jpeg", 2)
	p.delay = 5 * time.Millisecond
	r.Register(p)
	dir := t.TempDir()
	outDir := filepath.Join(dir, "out")
	os.Mkdir(outDir, 0o755)
	var inputs []string
	for _, sub := range []string{"a", "b"} {
		d := filepath.Join(dir, sub)
		os.Mkdir(d, 0o755)
		input := filepath.Join(d, "same.png")
		os.WriteFile(input, []byte(sub), 0o644)
		inputs = append(inputs, input)
	}
	results := make([][]string, 2)
	var wg sync.WaitGroup
	for i, input := range inputs {
		wg.Go(func() {
			out, err := r.Run(context.Background(), &Job{Input: input, To: MustID("jpeg"), Output: Output{InDir, outDir}}, nil)
			if err != nil {
				t.Error(err)
			}
			results[i] = out
		})
	}
	wg.Wait()
	if got := listing(t, outDir); !slices.Equal(got, []string{"same (1).jpg", "same-2 (1).jpg", "same-2.jpg", "same.jpg"}) {
		t.Fatal(got)
	}
	// Each job's pages stay together under one suffix.
	var owners []byte
	for _, paths := range results {
		a, _ := os.ReadFile(paths[0])
		b, _ := os.ReadFile(paths[1])
		if a[0] != b[0] {
			t.Fatal("pages of one job split across suffixes")
		}
		owners = append(owners, a[0])
	}
	slices.Sort(owners)
	if string(owners) != "ab" {
		t.Fatal(string(owners))
	}
}

func TestCancellingMidJobLeavesNothingBehind(t *testing.T) {
	r := NewRegistry()
	p := newPager("a", "pdf", "png", 50)
	p.delay = 20 * time.Millisecond
	r.Register(p)
	dir := t.TempDir()
	input := filepath.Join(dir, "doc.pdf")
	os.WriteFile(input, []byte("src"), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(60*time.Millisecond, cancel)
	_, err := r.Run(ctx, &Job{Input: input, To: MustID("png")}, nil)
	if !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
	if got := listing(t, dir); !slices.Equal(got, []string{"doc.pdf"}) {
		t.Fatal(got)
	}
}

type broken struct{ fake }

func (b *broken) Convert(t *Task, input, outDir string) ([]string, error) {
	os.WriteFile(t.Artifact(outDir, 0), []byte("half"), 0o644)
	return nil, EngineFailed("broken", "boom")
}

func TestEngineErrorsCleanUpToo(t *testing.T) {
	r := NewRegistry()
	r.Register(newPager("a", "jpeg", "png", 2))
	r.Register(&broken{fake{id: "broken", steps: [][2]string{{"png", "gif"}}}})
	dir := t.TempDir()
	input := filepath.Join(dir, "x.jpg")
	os.WriteFile(input, []byte("in"), 0o644)
	_, err := r.Run(context.Background(), &Job{Input: input, To: MustID("gif")}, nil)
	if Kind(err) != KindEngineFailed {
		t.Fatal(err)
	}
	if got := listing(t, dir); !slices.Equal(got, []string{"x.jpg"}) {
		t.Fatal(got)
	}
}

func TestPublishNamesPagesAndCollisions(t *testing.T) {
	if outputName("a", "png", 0, 0) != "a.png" || outputName("a", "png", 1, 0) != "a-2.png" || outputName("a", "png", 2, 3) != "a-3 (3).png" {
		t.Fatal("names")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.png"), []byte("keep"), 0o644)
	os.WriteFile(filepath.Join(dir, "x-2 (1).png"), []byte("keep"), 0o644)
	stage := t.TempDir()
	var staged []string
	for i := range 2 {
		p := filepath.Join(stage, fmt.Sprintf("%d.tmp", i))
		os.WriteFile(p, fmt.Appendf(nil, "page %d", i), 0o644)
		staged = append(staged, p)
	}
	out, err := publish(staged, []int{0, 1}, dir, "x", "png", false)
	// x.png is taken, and x-2 (1).png blocks suffix 1 for the whole set.
	if err != nil || !slices.Equal(out, []string{filepath.Join(dir, "x (2).png"), filepath.Join(dir, "x-2 (2).png")}) {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x-2.png")); err == nil {
		t.Fatal("placeholder left behind")
	}
}

func TestExpandsFolders(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"b.png", "a.png", ".hidden.png", "sub/c.png", ".git/d.png"} {
		p := filepath.Join(d, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	names := func(items []BatchItem) []string {
		var out []string
		for _, i := range items {
			rel, _ := filepath.Rel(d, i.Input)
			if i.Explicit {
				rel += "!"
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return out
	}
	flat, _ := ExpandInputs([]string{d, filepath.Join(d, "b.png")}, false)
	if got := names(flat); !slices.Equal(got, []string{"a.png", "b.png", "b.png!"}) {
		t.Fatal(got)
	}
	deep, _ := ExpandInputs([]string{d}, true)
	if got := names(deep); !slices.Equal(got, []string{"a.png", "b.png", "sub/c.png"}) {
		t.Fatal(got)
	}
	var rel []string
	for _, i := range deep {
		rel = append(rel, i.RelativeDir())
	}
	if !slices.Equal(rel, []string{"", "", "sub"}) {
		t.Fatal(rel)
	}
}

func TestRunBatchKeepsOrderAndOneVideoAtATime(t *testing.T) {
	r := NewRegistry()
	r.Register(newPager("a", "png", "jpeg", 1))
	dir := t.TempDir()
	var jobs []Job
	for i := range 6 {
		input := filepath.Join(dir, fmt.Sprintf("%d.png", i))
		os.WriteFile(input, []byte{byte('0' + i)}, 0o644)
		jobs = append(jobs, Job{Input: input, To: MustID("jpeg")})
	}
	results := RunBatch(context.Background(), r, jobs, 3, func(int, Event) {})
	for i, res := range results {
		if res.Err != nil || filepath.Base(res.Outputs[0]) != fmt.Sprintf("%d.jpg", i) {
			t.Fatal(i, res)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, res := range RunBatch(ctx, r, jobs, 3, func(int, Event) {}) {
		if !IsCancelled(res.Err) {
			t.Fatal(res.Err)
		}
	}
}
