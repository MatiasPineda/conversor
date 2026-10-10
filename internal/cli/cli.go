// Package cli is Conversor's command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"conversor/internal/core"
	"conversor/internal/engines"
	"conversor/internal/i18n"
	"conversor/internal/notify"
)

// Version is set at build time with -ldflags "-X conversor/internal/cli.Version=...".
var Version = "dev"

// exitError ends the program with a status code after its message (if any)
// was printed.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

type convertFlags struct {
	to, outDir, preset string
	quality            int
	maxSize            int
	pages              string
	dpi                int
	videoHeight        int
	audioBitrate       int
	videoCodec         string
	noAudio            bool
	background         string
	jobs               int
	recursive          bool
	json               bool
	skill              bool
	notify             bool
}

// Execute runs the CLI and returns the process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	root := NewRoot(stdout, stderr)
	root.SetArgs(args)
	err := root.Execute()
	var exit exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.code
	}
	fmt.Fprintln(stderr, i18n.T("Error:"), err)
	return 1
}

// NewRoot builds the command tree.
func NewRoot(stdout, stderr io.Writer) *cobra.Command {
	var f convertFlags
	root := &cobra.Command{
		Use:     i18n.T("conversor [files or folders] --to <format>"),
		Short:   i18n.T("Convert files locally"),
		Long:    i18n.T("Convert images, video, audio, PDFs and documents on this computer. Nothing is uploaded."),
		Version: Version,
		Example: i18n.T(`  conversor clip.mov --to mp4
  conversor photos/ --to webp -r
  conversor lease.pdf --to png --pages 1-3`),
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.skill {
				return printSkill(stdout)
			}
			if len(args) == 0 && f.to == "" && f.preset == "" {
				return cmd.Help()
			}
			return runConvert(cmd, args, &f, stdout, stderr)
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("conversor {{.Version}}\n")
	root.Flags().BoolP("version", "V", false, i18n.T("Print version"))
	fl := root.Flags()
	fl.SortFlags = false
	fl.StringVarP(&f.to, "to", "t", "", i18n.T("Target format, e.g. png, mp4, pdf"))
	fl.StringVarP(&f.outDir, "out-dir", "o", "", i18n.T("Output directory (defaults to each input's directory)"))
	fl.StringVarP(&f.preset, "preset", "p", "", i18n.T("A preset name from `conversor presets`, or a path to a preset file"))
	fl.IntVarP(&f.quality, "quality", "q", 0, i18n.T("Lossy quality from 1 to 100 (JPEG, WebP, HEIC, AVIF, video)"))
	fl.IntVar(&f.maxSize, "max-size", 0, i18n.T("Longest edge of image output, in pixels"))
	fl.StringVar(&f.pages, "pages", "", i18n.T("PDF pages to render: 2, 1-3 or 4-"))
	fl.IntVar(&f.dpi, "dpi", 0, i18n.T("Resolution for PDF and SVG rendering"))
	fl.IntVar(&f.videoHeight, "video-height", 0, i18n.T("Maximum output video height in pixels (never enlarges)"))
	fl.IntVar(&f.audioBitrate, "audio-bitrate", 0, i18n.T("Audio bitrate in kbit/s"))
	fl.StringVar(&f.videoCodec, "video-codec", "", i18n.T("Video encoder for MP4, MOV and MKV: h264 (default) or hevc"))
	fl.BoolVar(&f.noAudio, "no-audio", false, i18n.T("Leave the audio out of video output"))
	fl.StringVar(&f.background, "background", "", i18n.T("What transparent areas become: white (default for JPG), black, transparent, or a color like #ff8800"))
	fl.IntVarP(&f.jobs, "jobs", "j", 0, i18n.T("How many files to convert at once (default: CPU count; video runs one at a time)"))
	fl.BoolVarP(&f.recursive, "recursive", "r", false, i18n.T("Include subfolders of folder inputs"))
	fl.BoolVar(&f.json, "json", false, i18n.T("Print one JSON event per line on stdout"))
	fl.BoolVar(&f.skill, "skill", false, i18n.T("Print a SKILL.md for AI coding agents and exit"))
	fl.BoolVar(&f.notify, "notify", false, i18n.T("Show a desktop notification when done (used by file manager menus)"))
	fl.MarkHidden("notify")
	fl.Lookup("max-size").Annotations = map[string][]string{"value": {"PX"}}
	for name, value := range map[string]string{"max-size": "PX", "video-height": "PX", "audio-bitrate": "KBPS", "video-codec": "CODEC", "background": "COLOR"} {
		fl.Lookup(name).Annotations = map[string][]string{"value": {value}}
	}

	root.AddCommand(formatsCmd(stdout), targetsCmd(stdout), enginesCmd(stdout), presetsCmd(stdout, stderr),
		packCmd(stdout, stderr), integrateCmd(stdout, stderr), skillCmd(stdout))
	root.CompletionOptions.DisableDefaultCmd = true
	localize(root)
	return root
}

func formatsCmd(stdout io.Writer) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "formats",
		Short: i18n.T("List every known format"),
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			if asJSON {
				type entry struct {
					ID         string   `json:"id"`
					Name       string   `json:"name"`
					Category   string   `json:"category"`
					Extensions []string `json:"extensions"`
					MIME       string   `json:"mime"`
				}
				var list []entry
				for _, f := range core.Formats {
					list = append(list, entry{f.ID, f.Name, f.Category.String(), f.Extensions, f.MIME})
				}
				out, _ := json.MarshalIndent(list, "", "  ")
				fmt.Fprintln(stdout, string(out))
				return nil
			}
			for _, f := range core.Formats {
				fmt.Fprintf(stdout, "%-6s %-13s %s\n", f.ID, f.Category.Label(), f.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, i18n.T("Print JSON (id, name, category, extensions, mime)"))
	return cmd
}

func targetsCmd(stdout io.Writer) *cobra.Command {
	var menu bool
	cmd := &cobra.Command{
		Use:   i18n.T("targets <file>"),
		Short: i18n.T("List the formats a file can be converted to"),
		Args:  exactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			from := core.ByPath(args[0])
			if from == nil {
				return errors.New(i18n.T("unknown format: %s", args[0]))
			}
			r := engines.Default()
			targets := r.Targets(from)
			if menu {
				targets = r.MenuTargets(from)
			}
			for _, f := range targets {
				fmt.Fprintln(stdout, f.ID)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&menu, "menu", false, i18n.T("Only the few popular targets right-click menus offer"))
	return cmd
}

func enginesCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "engines",
		Short: i18n.T("Show the engines available on this machine"),
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			r := engines.Default()
			for _, e := range r.Engines() {
				if d, ok := e.(interface{ Detail() string }); ok && d.Detail() != "" {
					fmt.Fprintf(stdout, "%s (%s)\n", e.ID(), d.Detail())
				} else {
					fmt.Fprintln(stdout, e.ID())
				}
			}
			for _, u := range r.Unavailable() {
				fmt.Fprintln(stdout, i18n.T("%s (unavailable: %s)", u.ID, u.Reason))
			}
			return nil
		},
	}
}

func presetsCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "presets",
		Short: i18n.T("List saved presets"),
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			dir := engines.PresetsDir()
			if dir == "" {
				return errors.New(i18n.T("no config directory on this system"))
			}
			all, err := core.LoadPresetDir(dir)
			if err != nil {
				return err
			}
			if len(all) == 0 {
				fmt.Fprintln(stderr, i18n.T("no presets in %s", dir))
			}
			for _, p := range all {
				switch {
				case p.Err != nil:
					fmt.Fprintf(stdout, "%-16s %s\n", p.Name, i18n.T("invalid: %v", p.Err))
				case p.Preset.To == "":
					fmt.Fprintf(stdout, "%-16s -\n", p.Name)
				default:
					fmt.Fprintf(stdout, "%-16s %s\n", p.Name, p.Preset.To)
				}
			}
			return nil
		},
	}
}

func skillCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "skill",
		Short: i18n.T("Print a SKILL.md for AI coding agents"),
		Args:  noArgs,
		RunE:  func(*cobra.Command, []string) error { return printSkill(stdout) },
	}
}

// LoadPreset finds a preset by name in the presets folder, or by path.
func LoadPreset(name string) (core.Preset, error) {
	if strings.HasSuffix(name, ".toml") || strings.ContainsRune(name, os.PathSeparator) || strings.Contains(name, "/") {
		return core.LoadPreset(name)
	}
	dir := engines.PresetsDir()
	if dir == "" {
		return core.Preset{}, errors.New(i18n.T("no config directory on this system"))
	}
	path := filepath.Join(dir, name+".toml")
	if _, err := os.Stat(path); err != nil {
		return core.Preset{}, errors.New(i18n.T("no preset named %q in %s (see `conversor presets`)", name, dir))
	}
	return core.LoadPreset(path)
}

// options builds Options from the flags that were set.
func (f *convertFlags) options(cmd *cobra.Command) (core.Options, error) {
	var o core.Options
	set := cmd.Flags().Changed
	if set("quality") {
		o.Quality = core.Ptr(f.quality)
	}
	if set("max-size") {
		o.MaxSize = core.Ptr(f.maxSize)
	}
	if set("video-height") {
		o.VideoHeight = core.Ptr(f.videoHeight)
	}
	if set("audio-bitrate") {
		o.AudioBitrate = core.Ptr(f.audioBitrate)
	}
	if set("dpi") {
		o.DPI = core.Ptr(f.dpi)
	}
	if set("pages") {
		r, err := core.ParsePageRange(f.pages)
		if err != nil {
			return o, err
		}
		o.Pages = &r
	}
	if set("video-codec") {
		c, err := core.ParseVideoCodec(f.videoCodec)
		if err != nil {
			return o, err
		}
		o.VideoCodec = &c
	}
	if set("background") {
		b, err := core.ParseBackground(f.background)
		if err != nil {
			return o, err
		}
		o.Background = &b
	}
	o.StripAudio = f.noAudio
	return o, nil
}

func runConvert(cmd *cobra.Command, files []string, f *convertFlags, stdout, stderr io.Writer) error {
	// JSON is for programs: its messages stay in English.
	if f.json {
		i18n.Set(i18n.English)
	}
	fail := func(err error) error {
		if f.notify {
			notify.Send(i18n.T("Conversion failed"), err.Error())
		}
		return err
	}
	var preset core.Preset
	if f.preset != "" {
		p, err := LoadPreset(f.preset)
		if err != nil {
			return fail(err)
		}
		preset = p
	}
	toID := f.to
	if toID == "" {
		toID = preset.To
	}
	if toID == "" {
		return fail(errors.New(i18n.T("pass --to <format>, or see `conversor --help`")))
	}
	if len(files) == 0 {
		return fail(errors.New(i18n.T("no input files")))
	}
	to := core.ByID(toID)
	if to == nil {
		return fail(errors.New(i18n.T("unknown format: %s", toID)))
	}
	options, err := f.options(cmd)
	if err != nil {
		return fail(err)
	}
	options = options.Or(preset.Options)
	if err := options.Validate(); err != nil {
		return fail(err)
	}
	if options.Background != nil && options.Background.Transparent && to.Category == core.Image && !to.KeepsTransparency() {
		return fail(errors.New(i18n.T("%s can't store transparency; choose a background color such as white or black", to.Name)))
	}

	registry := engines.Default()
	items, err := core.ExpandInputs(files, f.recursive)
	if err != nil {
		return fail(err)
	}
	// Files found in a folder are skipped quietly when they can't become
	// the target; files named directly are always attempted, so their
	// errors show.
	var jobs []core.Job
	for _, item := range items {
		if !item.Explicit {
			from := core.ByPath(item.Input)
			if from == nil || from == to {
				continue
			}
			if _, err := registry.Plan(from, to); err != nil {
				continue
			}
		}
		out := core.Output{}
		if f.outDir != "" {
			// Files found in subfolders keep their relative folder, so
			// same-named files from different folders don't collide.
			dir := filepath.Join(f.outDir, item.RelativeDir())
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fail(fmt.Errorf("%s: %w", i18n.T("creating %s", dir), err))
			}
			out = core.Output{Kind: core.InDir, Path: dir}
		}
		jobs = append(jobs, core.Job{Input: item.Input, To: to, Options: options, Output: out})
	}
	if len(jobs) == 0 {
		return fail(errors.New(i18n.T("nothing in those folders converts to %s", to.ID)))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interrupted := make(chan os.Signal, 2)
	signal.Notify(interrupted, os.Interrupt)
	defer signal.Stop(interrupted)
	go func() {
		<-interrupted
		cancel()
		<-interrupted
		os.Exit(130)
	}()

	concurrency := f.jobs
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
	}
	rep := newReporter(jobs, f.json, stdout, stderr)
	results := core.RunBatch(ctx, registry, jobs, concurrency, rep.event)
	var done, failed, cancelled int
	var firstErr error
	var outputs []string
	for _, r := range results {
		switch {
		case core.IsCancelled(r.Err):
			cancelled++
		case r.Err != nil:
			failed++
			if firstErr == nil {
				firstErr = r.Err
			}
		default:
			done++
			outputs = append(outputs, r.Outputs...)
		}
	}
	rep.summary(done, failed, cancelled)
	if f.notify {
		notifyResult(jobs, outputs, done, failed, firstErr)
	}
	if ctx.Err() != nil {
		return exitError{130}
	}
	if failed > 0 {
		return exitError{1}
	}
	return nil
}

func notifyResult(jobs []core.Job, outputs []string, done, failed int, firstErr error) {
	switch {
	case failed == 0 && len(jobs) == 1 && len(outputs) >= 1:
		notify.Send(i18n.T("Converted %s", filepath.Base(jobs[0].Input)), i18n.T("Saved %s", filepath.Base(outputs[0])))
	case failed == 0:
		notify.Send(i18n.N(done, "Converted %d file", "Converted %d files"), i18n.T("Saved next to the originals"))
	case done == 0 && failed == 1:
		notify.Send(i18n.T("Could not convert %s", filepath.Base(jobs[0].Input)), firstErr.Error())
	default:
		notify.Send(i18n.T("Converted: %d. Failed: %d.", done, failed), firstErr.Error())
	}
}

// reporter prints batch events: JSON lines on stdout with --json,
// otherwise a result line per file on stderr and a live percentage on a
// terminal.
type reporter struct {
	jobs           []core.Job
	json, tty      bool
	stdout, stderr io.Writer
	mu             sync.Mutex
	last           []int
	lineOpen       atomic.Bool
}

func newReporter(jobs []core.Job, asJSON bool, stdout, stderr io.Writer) *reporter {
	tty := false
	if f, ok := stderr.(*os.File); ok && !asJSON {
		tty = term.IsTerminal(int(f.Fd()))
	}
	last := make([]int, len(jobs))
	for i := range last {
		last[i] = -2
	}
	return &reporter{jobs: jobs, json: asJSON, tty: tty, stdout: stdout, stderr: stderr, last: last}
}

func (r *reporter) emit(v map[string]any) {
	data, _ := json.Marshal(v)
	r.stdout.Write(append(data, '\n'))
}

func (r *reporter) event(i int, e core.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	input := r.jobs[i].Input
	switch e.Kind {
	case core.Started:
		if r.json {
			r.emit(map[string]any{"event": "started", "index": i, "input": input})
		}
	case core.Progressed:
		pct := -1
		if e.Fraction >= 0 {
			pct = int(e.Fraction * 100)
		}
		if r.last[i] == pct {
			return
		}
		r.last[i] = pct
		if r.json {
			var fraction any
			if e.Fraction >= 0 {
				fraction = e.Fraction
			}
			r.emit(map[string]any{"event": "progress", "index": i, "fraction": fraction})
		} else if r.tty {
			shown := "..."
			if pct >= 0 {
				shown = fmt.Sprintf("%3d%%", pct)
			}
			fmt.Fprintf(r.stderr, "\r\x1b[2K%s: %s", input, shown)
		}
	case core.Finished:
		switch {
		case r.json && e.Err == nil:
			outs := e.Outputs
			if outs == nil {
				outs = []string{}
			}
			r.emit(map[string]any{"event": "done", "index": i, "input": input, "outputs": outs})
		case r.json:
			r.emit(map[string]any{"event": "failed", "index": i, "input": input, "kind": core.Kind(e.Err), "message": e.Err.Error()})
		case e.Err == nil:
			r.line(fmt.Sprintf("%s -> %s", input, strings.Join(e.Outputs, ", ")))
		default:
			r.line(fmt.Sprintf("%s: %v", input, e.Err))
		}
	}
}

func (r *reporter) line(text string) {
	if r.tty {
		fmt.Fprintf(r.stderr, "\r\x1b[2K%s\n", text)
	} else {
		fmt.Fprintln(r.stderr, text)
	}
}

func (r *reporter) summary(done, failed, cancelled int) {
	if r.json {
		r.emit(map[string]any{"event": "summary", "done": done, "failed": failed, "cancelled": cancelled})
		return
	}
	if failed > 0 || cancelled > 0 || len(r.jobs) > 1 {
		text := i18n.T("Converted: %d. Failed: %d.", done, failed)
		if cancelled > 0 {
			text += " " + i18n.T("Cancelled: %d.", cancelled)
		}
		fmt.Fprintln(r.stderr, text)
	}
}
