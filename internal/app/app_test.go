package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"conversor/internal/core"
)

// copier "converts" by copying, optionally slowly.
type copier struct {
	steps [][2]string
	delay time.Duration
}

func (c copier) ID() string          { return "copy" }
func (c copier) Priority() int       { return 0 }
func (c copier) Unavailable() string { return "" }
func (c copier) Steps() []core.Step {
	var out []core.Step
	for _, s := range c.steps {
		out = append(out, core.Step{From: core.MustID(s[0]), To: core.MustID(s[1])})
	}
	return out
}
func (c copier) Convert(t *core.Task, input, outDir string) ([]string, error) {
	for range 10 {
		time.Sleep(c.delay / 10)
		if err := t.Check(); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return nil, err
	}
	out := t.Artifact(outDir, 0)
	return []string{out}, os.WriteFile(out, data, 0o644)
}

func registry(delay time.Duration) *core.Registry {
	r := core.NewRegistry()
	r.Register(copier{[][2]string{{"heic", "jpeg"}, {"heic", "png"}, {"png", "jpeg"}, {"png", "webp"}, {"mov", "mp4"}, {"mp4", "mov"}, {"wav", "mp3"}}, delay})
	return r
}

func touch(t *testing.T, path string) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitIdle(t *testing.T, j *Jobs) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for j.Active() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("jobs never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitBusy waits until the state has handled every finished job.
func waitBusy(t *testing.T, s *State) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.Busy() {
		if time.Now().After(deadline) {
			t.Fatal("jobs never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newState(t *testing.T, r *core.Registry, hooks Hooks) (*State, string) {
	dir := t.TempDir()
	s, err := NewState(Paths{Settings: filepath.Join(dir, "settings.toml"), History: filepath.Join(dir, "history.jsonl"), Presets: filepath.Join(dir, "presets")}, r, hooks)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestSettingsRoundTripAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	s, err := LoadSettings(path)
	if err != nil || !s.Notifications || len(s.Automations) != 3 || s.Defaults.Photos != "jpeg" {
		t.Fatalf("%v %+v", err, s)
	}
	s.Concurrency = 2
	s.Automations[0].Enabled = false
	s.Defaults.Video = "webm"
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := LoadSettings(path)
	if err != nil || back.Workers() != 2 || back.Automations[0].Enabled || back.Defaults.Video != "webm" {
		t.Fatalf("%v %+v", err, back)
	}
	// Unknown keys and bad defaults don't break loading.
	os.WriteFile(path, []byte("future_key = 1\n[defaults]\nphotos = \"nope\"\n"), 0o644)
	back, err = LoadSettings(path)
	if err != nil || back.Defaults.Photos != "jpeg" || back.Defaults.Audio != "mp3" {
		t.Fatalf("%v %+v", err, back.Defaults)
	}
}

func TestHistoryPersistsAndPrunes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	h, _ := OpenHistory(path)
	for i := range keepHistory + 5 {
		h.Add(Record{Input: "f" + string(rune('a'+i%26)), To: "png", Outcome: Done})
	}
	again, _ := OpenHistory(path)
	recent := again.Recent(3)
	if len(again.Recent(keepHistory*2)) != keepHistory || recent[0].ID != keepHistory+5 {
		t.Fatalf("%d records, newest %d", len(again.Recent(keepHistory*2)), recent[0].ID)
	}
	again.Delete(recent[0].ID)
	if third, _ := OpenHistory(path); third.Recent(1)[0].ID != recent[1].ID {
		t.Fatal("delete didn't persist")
	}
	// New ids keep counting up after a reload.
	if id := again.Add(Record{}); id <= recent[0].ID {
		t.Fatalf("id %d reused", id)
	}
}

func TestJobsLimitsAndVideoLane(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	maxRunning, maxVideo := 0, 0
	var j *Jobs
	j = NewJobs(registry(40*time.Millisecond), 3, func() {
		running, video := 0, 0
		for _, e := range j.Snapshot() {
			if e.Status == Running {
				running++
				if e.To.Category == core.Video {
					video++
				}
			}
		}
		mu.Lock()
		maxRunning, maxVideo = max(maxRunning, running), max(maxVideo, video)
		mu.Unlock()
	}, nil)
	for i := range 4 {
		j.Submit(core.Job{Input: touch(t, filepath.Join(dir, "v", string(rune('a'+i))+".mov")), To: core.MustID("mp4")}, false)
		j.Submit(core.Job{Input: touch(t, filepath.Join(dir, "i", string(rune('a'+i))+".png")), To: core.MustID("jpeg")}, false)
	}
	waitIdle(t, j)
	mu.Lock()
	defer mu.Unlock()
	if maxRunning > 3 || maxVideo > 1 {
		t.Fatalf("running %d at once, %d videos", maxRunning, maxVideo)
	}
	for _, e := range j.Snapshot() {
		if e.Outcome != Done || len(e.Outputs) != 1 {
			t.Fatalf("%+v", e)
		}
	}
}

func TestJobsCancelQueuedAndRunning(t *testing.T) {
	dir := t.TempDir()
	var finished []Entry
	var mu sync.Mutex
	j := NewJobs(registry(500*time.Millisecond), 1, nil, func(e Entry) { mu.Lock(); finished = append(finished, e); mu.Unlock() })
	a := j.Submit(core.Job{Input: touch(t, filepath.Join(dir, "a.png")), To: core.MustID("jpeg")}, false)
	b := j.Submit(core.Job{Input: touch(t, filepath.Join(dir, "b.png")), To: core.MustID("jpeg")}, false)
	j.Cancel(b)
	time.Sleep(50 * time.Millisecond)
	j.Cancel(a)
	waitIdle(t, j)
	mu.Lock()
	defer mu.Unlock()
	if len(finished) != 2 || finished[0].Outcome != Cancelled || finished[1].Outcome != Cancelled {
		t.Fatalf("%+v", finished)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatal("a cancelled job left files")
	}
	j.Forget()
	if len(j.Snapshot()) != 0 {
		t.Fatal("forget")
	}
}

func TestEntryRemaining(t *testing.T) {
	now := time.Now()
	e := Entry{Status: Running, Progress: 0.25, Started: now.Add(-10 * time.Second)}
	if d, ok := e.Remaining(now); !ok || d != 30*time.Second {
		t.Fatal(d, ok)
	}
	e.Progress = 0.01
	if _, ok := e.Remaining(now); ok {
		t.Fatal("too early to say")
	}
}

func TestAddFilesUsesDefaultsAndAsksForTheRest(t *testing.T) {
	var mu sync.Mutex
	var notes []string
	s, dir := newState(t, registry(0), Hooks{Notify: func(title, body string) { mu.Lock(); notes = append(notes, title+": "+body); mu.Unlock() }})
	s.Background = true
	photo := touch(t, filepath.Join(dir, "in", "IMG_1.heic"))
	shot := touch(t, filepath.Join(dir, "in", "Screenshot 2026.heic"))
	song := touch(t, filepath.Join(dir, "in", "song.wav"))
	odd := touch(t, filepath.Join(dir, "in", "notes.xyz"))
	added := s.AddFiles([]string{photo, shot, song, odd})
	if len(added.Jobs) != 3 || !slices.Equal(added.Ask, []string{odd}) {
		t.Fatalf("%+v", added)
	}
	waitBusy(t, s)
	for _, want := range []string{"IMG_1.jpg", "Screenshot 2026.png", "song.mp3"} {
		if _, err := os.Stat(filepath.Join(dir, "in", want)); err != nil {
			t.Fatalf("%s missing", want)
		}
	}
	mu.Lock()
	if len(notes) != 1 || notes[0] != "Conversion finished: Converted 3 files." {
		t.Fatalf("%v", notes)
	}
	mu.Unlock()
	if h := s.History(10); len(h) != 3 || h[0].Outcome != Done {
		t.Fatalf("%+v", h)
	}
	// Folders add the files directly inside them.
	exp := Expand(s.Registry(), []string{filepath.Join(dir, "in")})
	if len(exp.Files) < 4 {
		t.Fatalf("%v", exp.Files)
	}
}

func TestConvertRequestNeedsEveryFileToReachTheTarget(t *testing.T) {
	s, dir := newState(t, registry(0), Hooks{})
	png := touch(t, filepath.Join(dir, "a.png"))
	wav := touch(t, filepath.Join(dir, "b.wav"))
	if _, err := s.ConvertRequest(Request{Files: []string{png, wav}, To: "jpeg"}); err == nil {
		t.Fatal("wav can't become jpeg, so a window should ask")
	}
	if err := s.SavePreset("web", "", core.Preset{To: "webp", Options: core.Options{Quality: core.Ptr(70)}}); err != nil {
		t.Fatal(err)
	}
	ids, err := s.ConvertRequest(Request{Files: []string{png}, Preset: "web"})
	if err != nil || len(ids) != 1 {
		t.Fatal(err)
	}
	waitBusy(t, s)
	e, _ := s.Jobs.Get(ids[0])
	if e.To.ID != "webp" || *e.Setup.Options.Quality != 70 {
		t.Fatalf("%+v", e)
	}
	if err := s.SavePreset("bad/name", "", core.Preset{}); err == nil {
		t.Fatal("slash in a preset name")
	}
}

func TestAutomationsWaitForStableNewFiles(t *testing.T) {
	var mu sync.Mutex
	var copied []string
	s, dir := newState(t, registry(0), Hooks{Copy: func(p string) { mu.Lock(); copied = append(copied, p); mu.Unlock() }})
	shots := filepath.Join(dir, "Shots")
	touch(t, filepath.Join(shots, "old.png"))
	s.UpdateSettings(func(st *Settings) {
		st.Automations = []Automation{{Name: "Screenshots", To: "jpeg", Enabled: true, Watch: WatchScreenshot, Folder: shots, CopyToClipboard: true}}
	})
	s.PollAutomations() // primes: old.png is left alone
	newShot := touch(t, filepath.Join(shots, "Screenshot 1.png"))
	s.PollAutomations() // first sighting
	if s.Jobs.Active() != 0 || len(s.Jobs.Snapshot()) != 0 {
		t.Fatal("converted before the file settled")
	}
	s.PollAutomations() // same size twice: convert
	waitBusy(t, s)
	if _, err := os.Stat(filepath.Join(shots, "Screenshot 1.jpg")); err != nil {
		t.Fatal("not converted")
	}
	if _, err := os.Stat(filepath.Join(shots, "old.jpg")); err == nil {
		t.Fatal("an existing file was converted")
	}
	mu.Lock()
	if len(copied) != 1 {
		t.Fatalf("copied %v", copied)
	}
	mu.Unlock()
	// Its own output is never picked up again.
	s.PollAutomations()
	s.PollAutomations()
	if len(s.Jobs.Snapshot()) != 1 {
		t.Fatalf("%d jobs", len(s.Jobs.Snapshot()))
	}
	_ = newShot
}

func TestRulesSharingAFolderEachGetTheirFiles(t *testing.T) {
	s, dir := newState(t, registry(0), Hooks{})
	desk := filepath.Join(dir, "Desk")
	os.MkdirAll(desk, 0o755)
	s.UpdateSettings(func(st *Settings) {
		st.Automations = []Automation{
			{Name: "Screen recordings", To: "mp4", Enabled: true, Watch: WatchRecording, Folder: desk},
			{Name: "Screenshots", To: "jpeg", Enabled: true, Watch: WatchScreenshot, Folder: desk},
		}
	})
	s.PollAutomations()
	touch(t, filepath.Join(desk, "Screenshot 1.png"))
	touch(t, filepath.Join(desk, "Screen Recording 1.mov"))
	s.PollAutomations()
	s.PollAutomations()
	waitBusy(t, s)
	for _, want := range []string{"Screenshot 1.jpg", "Screen Recording 1.mp4"} {
		if _, err := os.Stat(filepath.Join(desk, want)); err != nil {
			t.Fatalf("%s missing", want)
		}
	}
}

func TestCatchAllFoldersOnlyTakeScreenshots(t *testing.T) {
	desktop := filepath.Join(t.TempDir(), "Desktop")
	rule := Automation{To: "png", Enabled: true, Watch: WatchScreenshot, Folder: desktop}
	if rule.matches(filepath.Join(desktop, "holiday.jpg"), desktop) {
		t.Fatal("a Desktop photo isn't a screenshot")
	}
	if !rule.matches(filepath.Join(desktop, "Screenshot 2026-10-10.jpg"), desktop) {
		t.Fatal("screenshot name")
	}
	if rule.matches(filepath.Join(desktop, "Screenshot.png.part"), desktop) {
		t.Fatal("partial download")
	}
	home, _ := os.UserHomeDir()
	if st := (Automation{Folder: home, Watch: WatchFolder}).Status(); st.Dir != "" {
		t.Fatal("home is too broad")
	}
}

func TestParseArgs(t *testing.T) {
	r, err := ParseArgs([]string{"open", "--to", "png", "a.heic", "file:///tmp/x%20y.png", "--", "--weird.heic"}, "/home/u")
	if err != nil || r.To != "png" || !slices.Equal(r.Files, []string{"/home/u/a.heic", "/tmp/x y.png", "/home/u/--weird.heic"}) || !r.AutoStart() {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := ParseArgs([]string{"--to", "nope", "a"}, "/"); err == nil {
		t.Fatal("unknown format")
	}
	if r, _ := ParseArgs([]string{"x.png"}, "/"); r.AutoStart() {
		t.Fatal("no target: ask in a window")
	}
	if r, _ := ParseArgs([]string{"settings"}, "/"); r.Page != "settings" {
		t.Fatal(r)
	}
}

func TestSecondLaunchHandsItsRequestOver(t *testing.T) {
	t.Setenv("CONVERSOR_RUNTIME_DIR", filepath.Join(t.TempDir(), "run"))
	p, err := Become(Request{})
	if err != nil || p == nil {
		t.Fatal("first launch should become the app", err)
	}
	defer p.Close()
	got := make(chan Request, 1)
	p.Listen(func(r Request) { got <- r })
	second, err := Become(Request{Files: []string{"/a.png"}, To: "jpeg"})
	if err != nil || second != nil {
		t.Fatal("second launch should hand over", err)
	}
	select {
	case r := <-got:
		if r.To != "jpeg" || r.Files[0] != "/a.png" {
			t.Fatal(r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request never arrived")
	}
}

func TestManyLaunchesAtOnceFindOneApp(t *testing.T) {
	t.Setenv("CONVERSOR_RUNTIME_DIR", filepath.Join(t.TempDir(), "run"))
	p, err := Become(Request{})
	if err != nil || p == nil {
		t.Fatal(err)
	}
	got := make(chan Request, 32)
	p.Listen(func(r Request) { got <- r })
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if second, err := Become(Request{Files: []string{fmt.Sprintf("/f%d.png", i)}}); err != nil || second != nil {
				t.Errorf("launch %d: %v %v", i, second, err)
			}
		})
	}
	wg.Wait()
	if len(got) != 20 {
		t.Fatalf("%d requests arrived", len(got))
	}
	// After the app quits, the next launch becomes the app.
	p.Close()
	next, err := Become(Request{})
	if err != nil || next == nil {
		t.Fatal("the lock should be free", err)
	}
	next.Close()
}

func TestQuickConvertRules(t *testing.T) {
	jpeg, mp4, gif, wav := core.MustID("jpeg"), core.MustID("mp4"), core.MustID("gif"), core.MustID("wav")
	preset := core.Options{Quality: core.Ptr(50), MaxSize: core.Ptr(100), Background: &core.Black}
	// Balanced and Original clear what the preset set; hidden controls keep it.
	o := ConversionOptions(preset, jpeg, QuickChoices{}, []string{"a.png"})
	if o.Quality != nil || o.MaxSize != nil || o.Background != nil {
		t.Fatalf("%+v", o)
	}
	o = ConversionOptions(preset, wav, QuickChoices{Size: core.Ptr(128)}, []string{"a.mp3"})
	if *o.Quality != 50 || o.AudioBitrate != nil {
		t.Fatalf("wav has no quality or bitrate control: %+v", o)
	}
	o = ConversionOptions(core.Options{}, mp4, QuickChoices{Size: core.Ptr(720), StripAudio: true}, []string{"a.mov"})
	if *o.VideoHeight != 720 || !o.StripAudio {
		t.Fatalf("%+v", o)
	}
	clear := core.Transparent
	if o := ConversionOptions(core.Options{}, jpeg, QuickChoices{Background: &clear}, []string{"a.png"}); o.Background != nil {
		t.Fatal("transparent JPEG falls back to the default")
	}
	if BackgroundApplies(gif, []string{"a.mov"}) || !BackgroundApplies(gif, []string{"a.png"}) {
		t.Fatal("gif background")
	}
	if QualityKey(core.Ptr(95)) != "best" || QualityKey(nil) != "balanced" || QualityKey(core.Ptr(42)) != "preset" {
		t.Fatal("quality keys")
	}
	for typed, want := range map[string]string{"holiday": "holiday.jpg", "holiday.JPG": "holiday.JPG", " x.jpeg ": "x.jpeg.jpg"} {
		if got, why := OutputName(typed, "jpg"); got != want || why != "" {
			t.Fatalf("%q: %q %q", typed, got, why)
		}
	}
	for _, bad := range []string{"", "a/b", "..", "\x01"} {
		if _, why := OutputName(bad, "jpg"); why == "" {
			t.Fatalf("%q accepted", bad)
		}
	}
}
