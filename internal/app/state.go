package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"conversor/internal/core"
	"conversor/internal/engines"
	"conversor/internal/i18n"
)

// Hooks are what the app does outside itself, so tests can watch them.
type Hooks struct {
	// Notify shows a system notification.
	Notify func(title, body string)
	// Reveal shows a file in the file manager.
	Reveal func(path string)
	// Copy puts a finished file on the clipboard.
	Copy func(path string)
}

// Paths are where the app keeps its files; "" keeps a thing in memory.
type Paths struct {
	Settings, History, Presets string
}

// DefaultPaths are the real ones.
func DefaultPaths() Paths {
	return Paths{Settings: SettingsPath(), History: HistoryPath(), Presets: engines.PresetsDir()}
}

// State is the app's shared state: the registry, settings, presets, the
// job queue and history. Windows read it and change it through its
// methods; it calls OnChange listeners after every change.
type State struct {
	mu        sync.Mutex
	paths     Paths
	settings  Settings
	registry  *core.Registry
	history   *History
	Jobs      *Jobs
	watch     *WatchState
	hooks     Hooks
	listeners []func()
	// batch counts finished jobs since the queue was last idle, for the
	// summary notification.
	batchDone, batchFailed int
	// inFlight counts submitted jobs that haven't finished.
	inFlight int
	// Background is set while no window has focus, so finishing a batch
	// shows a notification.
	Background bool
}

// NewState loads settings and history and starts an idle queue.
func NewState(paths Paths, registry *core.Registry, hooks Hooks) (*State, error) {
	settings, serr := LoadSettings(paths.Settings)
	history, herr := OpenHistory(paths.History)
	s := &State{paths: paths, settings: settings, registry: registry, history: history, watch: NewWatchState(), hooks: hooks}
	if s.hooks.Notify == nil {
		s.hooks.Notify = func(string, string) {}
	}
	if s.hooks.Reveal == nil {
		s.hooks.Reveal = func(string) {}
	}
	if s.hooks.Copy == nil {
		s.hooks.Copy = func(string) {}
	}
	s.Jobs = NewJobs(registry, settings.Workers(), s.changed, s.finished)
	return s, errors.Join(serr, herr)
}

// OnChange registers a listener. Listeners run on whatever goroutine made
// the change; UI code hands the work to its own thread.
func (s *State) OnChange(f func()) {
	s.mu.Lock()
	s.listeners = append(s.listeners, f)
	s.mu.Unlock()
}

func (s *State) changed() {
	s.mu.Lock()
	ls := slices.Clone(s.listeners)
	s.mu.Unlock()
	for _, f := range ls {
		f()
	}
}

// Registry is the registry conversions use now.
func (s *State) Registry() *core.Registry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registry
}

// RebuildRegistry looks for engines again, after a pack was installed.
func (s *State) RebuildRegistry() {
	r := engines.Default()
	s.mu.Lock()
	s.registry = r
	s.mu.Unlock()
	s.Jobs.SetRegistry(r)
	s.changed()
}

// Settings returns a copy of the settings.
func (s *State) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.settings
	st.Automations = slices.Clone(st.Automations)
	return st
}

// UpdateSettings changes settings and saves them.
func (s *State) UpdateSettings(change func(*Settings)) error {
	s.mu.Lock()
	change(&s.settings)
	st := s.settings
	s.mu.Unlock()
	s.Jobs.SetLimit(st.Workers())
	err := st.Save(s.paths.Settings)
	s.changed()
	return err
}

// History lists recent finished conversions, newest first.
func (s *State) History(limit int) []Record { return s.history.Recent(limit) }

// ForgetHistory removes records from history.
func (s *State) ForgetHistory(ids ...int64) {
	s.history.Delete(ids...)
	s.changed()
}

// ClearHistory empties history and drops finished jobs from the list.
func (s *State) ClearHistory() {
	s.history.Clear()
	s.Jobs.Forget()
	s.changed()
}

func (s *State) finished(e Entry) {
	rec := Record{FinishedAt: time.Now().Unix(), Input: e.Input, To: e.To.ID, Outcome: e.Outcome,
		Outputs: e.Outputs, Error: e.Err, Setup: e.Setup}
	s.history.Add(rec)
	st := s.Settings()
	if e.Outcome == Done {
		if e.CopyResult && len(e.Outputs) > 0 {
			s.hooks.Copy(e.Outputs[0])
		}
		if st.RevealWhenDone && len(e.Outputs) > 0 {
			s.hooks.Reveal(e.Outputs[0])
		}
	}
	s.mu.Lock()
	switch e.Outcome {
	case Done:
		s.batchDone++
	case Failed:
		s.batchFailed++
	}
	var title, body string
	s.inFlight--
	idle := s.inFlight <= 0
	if idle {
		title, body = batchSummary(s.batchDone, s.batchFailed)
		s.batchDone, s.batchFailed = 0, 0
	}
	background := s.Background
	s.mu.Unlock()
	if idle && title != "" && st.Notifications && background {
		s.hooks.Notify(title, body)
	}
}

// batchSummary is the notification for a finished batch, or "" when
// everything was cancelled.
func batchSummary(done, failed int) (string, string) {
	switch {
	case done == 0 && failed == 0:
		return "", ""
	case failed == 0:
		return i18n.T("Conversion finished"), i18n.N(done, "Converted %d file.", "Converted %d files.")
	case done == 0:
		return i18n.T("Conversion failed"), i18n.N(failed, "%d file could not be converted.", "%d files could not be converted.")
	}
	return i18n.T("Conversion finished with errors"), i18n.T("Converted: %d. Failed: %d.", done, failed)
}

// Expanded is files to convert, plus a message for each folder that could
// not be read.
type Expanded struct {
	Files      []string
	Unreadable []string
}

// Expand turns folders into the files directly inside them, skipping
// hidden files and files no engine reads (documents are kept, so the user
// hears that LibreOffice is missing). Files named directly are always kept.
func Expand(r *core.Registry, paths []string) Expanded {
	var out Expanded
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out.Files = append(out.Files, p)
		}
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			add(p)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			out.Unreadable = append(out.Unreadable, i18n.T("Couldn't read %s: %v", filepath.Base(p), err))
			continue
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			f := core.ByPath(e.Name())
			if f == nil {
				continue
			}
			doc := f.Category == core.Document || f.Category == core.Presentation || f.Category == core.Spreadsheet
			if len(r.Targets(f)) > 0 || doc {
				add(filepath.Join(p, e.Name()))
			}
		}
	}
	return out
}

// Targets are the formats every file in a selection can become, plus the
// files that can't be converted at all.
type Targets struct {
	Formats     []*core.Format
	Unsupported []string
}

// CommonTargets works out Targets for files.
func CommonTargets(r *core.Registry, files []string) Targets {
	var out Targets
	first := true
	for _, f := range files {
		var ts []*core.Format
		if from := core.ByPath(f); from != nil {
			ts = r.Targets(from)
		}
		switch {
		case len(ts) == 0:
			out.Unsupported = append(out.Unsupported, f)
		case first:
			out.Formats, first = ts, false
		default:
			out.Formats = slices.DeleteFunc(out.Formats, func(x *core.Format) bool { return !slices.Contains(ts, x) })
		}
	}
	return out
}

// DefaultTarget is what a file converts to when dropped or added: the
// default for its kind, if the file can reach it.
func (s *State) DefaultTarget(file string) *core.Format {
	from := core.ByPath(file)
	if from == nil {
		return nil
	}
	to := core.ByID(s.Settings().Defaults.Get(KindOf(file, from)))
	if to == nil || to == from || !slices.Contains(s.Registry().Targets(from), to) {
		return nil
	}
	return to
}

// Resolve works out a request's target and options from --to and a preset.
func (s *State) Resolve(to, preset string) (*core.Format, core.Options, error) {
	var opts core.Options
	var target *core.Format
	if preset != "" {
		p, err := s.LoadPreset(preset)
		if err != nil {
			return nil, opts, err
		}
		opts = p.Options
		if p.To != "" {
			target = core.ByID(p.To)
		}
	}
	if to != "" {
		target = core.ByID(to)
		if target == nil {
			return nil, opts, errors.New(i18n.T("unknown format: %s", to))
		}
	}
	return target, opts, nil
}

// Convert queues files for conversion with the default output.
func (s *State) Convert(files []string, to *core.Format, opts core.Options) ([]JobID, error) {
	return s.ConvertTo(files, to, opts, s.Settings().Output(), false)
}

// ConvertTo queues files with an explicit output.
func (s *State) ConvertTo(files []string, to *core.Format, opts core.Options, out core.Output, copyResult bool) ([]JobID, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if out.Kind == core.InDir {
		if err := os.MkdirAll(out.Path, 0o755); err != nil {
			return nil, err
		}
	}
	// Counted before submitting, so a job that finishes at once can't
	// make the batch look done early.
	s.mu.Lock()
	s.inFlight += len(files)
	s.mu.Unlock()
	var ids []JobID
	for _, f := range files {
		ids = append(ids, s.Jobs.Submit(core.Job{Input: f, To: to, Options: opts, Output: out}, copyResult))
	}
	return ids, nil
}

// Added is what AddFiles did.
type Added struct {
	Jobs []JobID
	// Ask lists files with no default format they can reach; the caller
	// opens Quick convert for them.
	Ask    []string
	Errors []string
}

// AddFiles converts files (and the files directly inside folders) to their
// default formats right away.
func (s *State) AddFiles(paths []string) Added {
	exp := Expand(s.Registry(), paths)
	added := Added{Errors: exp.Unreadable}
	type group struct {
		to    *core.Format
		files []string
	}
	var groups []group
	for _, f := range exp.Files {
		to := s.DefaultTarget(f)
		if to == nil {
			added.Ask = append(added.Ask, f)
			continue
		}
		i := slices.IndexFunc(groups, func(g group) bool { return g.to == to })
		if i < 0 {
			groups = append(groups, group{to: to})
			i = len(groups) - 1
		}
		groups[i].files = append(groups[i].files, f)
	}
	for _, g := range groups {
		ids, err := s.Convert(g.files, g.to, core.Options{})
		if err != nil {
			added.Errors = append(added.Errors, err.Error())
			break
		}
		added.Jobs = append(added.Jobs, ids...)
	}
	return added
}

// ConvertRequest runs a request that named its target or preset, in place
// and without a window. It fails, with the reason, when a window must ask:
// the target is unknown or unreachable, or a file can't be converted.
func (s *State) ConvertRequest(req Request) ([]JobID, error) {
	to, opts, err := s.Resolve(req.To, req.Preset)
	if err != nil {
		return nil, err
	}
	if to == nil {
		return nil, errors.New(i18n.T("pick a format"))
	}
	exp := Expand(s.Registry(), req.Files)
	if len(exp.Unreadable) > 0 {
		return nil, errors.New(strings.Join(exp.Unreadable, "\n"))
	}
	targets := CommonTargets(s.Registry(), exp.Files)
	if len(targets.Unsupported) > 0 || !slices.Contains(targets.Formats, to) {
		return nil, errors.New(i18n.T("not every file can become %s", to.Name))
	}
	return s.Convert(exp.Files, to, opts)
}

// Retry runs a finished conversion again the way it ran.
func (s *State) Retry(input, to string, setup Setup) (JobID, error) {
	f := core.ByID(to)
	if f == nil {
		return 0, errors.New(i18n.T("unknown format: %s", to))
	}
	ids, err := s.ConvertTo([]string{input}, f, setup.Options, setup.Output, false)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return ids[0], nil
}

// PollAutomations converts new files the enabled rules are waiting for.
func (s *State) PollAutomations() []error {
	rules := s.Settings().Automations
	var errs []error
	for _, r := range s.watch.DrainReady(rules) {
		rule := rules[r.Rule]
		to, from := core.ByID(rule.To), core.ByPath(r.Path)
		if to == nil || from == nil || !slices.Contains(s.Registry().Targets(from), to) {
			continue
		}
		// Automations always write beside the original.
		if _, err := s.ConvertTo([]string{r.Path}, to, core.Options{}, core.Output{}, rule.CopyToClipboard); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// Busy reports whether conversions are queued or running.
func (s *State) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight > 0
}

// RecentOutputs are the last few converted files, newest first, for the
// tray menu.
func (s *State) RecentOutputs(n int) []string {
	var out []string
	for _, r := range s.history.Recent(50) {
		for _, p := range r.Outputs {
			if len(out) == n {
				return out
			}
			if _, err := os.Stat(p); err == nil && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// Presets lists saved presets.
func (s *State) Presets() []core.PresetResult {
	if s.paths.Presets == "" {
		return nil
	}
	all, _ := core.LoadPresetDir(s.paths.Presets)
	return all
}

// LoadPreset loads a preset by name, or by path.
func (s *State) LoadPreset(name string) (core.Preset, error) {
	if strings.HasSuffix(name, ".toml") || strings.ContainsAny(name, `/\`) {
		return core.LoadPreset(name)
	}
	if s.paths.Presets == "" {
		return core.Preset{}, errors.New(i18n.T("presets aren't available"))
	}
	path := filepath.Join(s.paths.Presets, name+".toml")
	if _, err := os.Stat(path); err != nil {
		return core.Preset{}, errors.New(i18n.T("no preset named %q", name))
	}
	return core.LoadPreset(path)
}

// PresetNameProblem says why a preset name can't be a file name, or "".
func PresetNameProblem(name string) string {
	switch {
	case strings.TrimSpace(name) == "":
		return i18n.T("Give the preset a name.")
	case strings.ContainsAny(name, `/\:*?"<>|`) || strings.HasPrefix(name, "."):
		return i18n.T("Use letters, numbers, spaces, dashes or underscores.")
	}
	return ""
}

// SavePreset writes a preset; oldName, when set and different, is removed
// (a rename).
func (s *State) SavePreset(name, oldName string, p core.Preset) error {
	if why := PresetNameProblem(name); why != "" {
		return errors.New(why)
	}
	if err := p.Options.Validate(); err != nil {
		return err
	}
	if s.paths.Presets == "" {
		return errors.New(i18n.T("presets aren't available"))
	}
	if err := writeAtomic(filepath.Join(s.paths.Presets, name+".toml"), []byte(p.TOML())); err != nil {
		return err
	}
	if oldName != "" && oldName != name {
		os.Remove(filepath.Join(s.paths.Presets, oldName+".toml"))
	}
	s.changed()
	return nil
}

// DeletePreset removes a preset.
func (s *State) DeletePreset(name string) error {
	if s.paths.Presets == "" {
		return nil
	}
	err := os.Remove(filepath.Join(s.paths.Presets, name+".toml"))
	s.changed()
	return err
}
