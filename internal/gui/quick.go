package gui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"conversor/internal/app"
	"conversor/internal/core"
	"conversor/internal/i18n"
)

// noPreset is the preset menu's first entry.
func noPreset() string { return i18n.T("No preset") }

// quickWindow is Quick convert: the window for files sent without a
// target, from "More options…" in a file manager menu, "Open with", Add
// files or the command line. It shows the files, lets the user pick a
// target and options, and follows the conversion until it is done.
type quickWindow struct {
	u       *UI
	win     fyne.Window
	files   []string
	targets app.Targets
	to      *core.Format
	preset  string
	// presetOptions are the preset's options, which the controls start from.
	presetOptions core.Options
	choices       app.QuickChoices
	saveDir       string
	jobs          []app.JobID

	body       *fyne.Container
	targetBox  *fyne.Container
	optionsBox *fyne.Container
	nameEntry  *widget.Entry
	errLabel   *widget.Label
	convertBtn *widget.Button
	progress   *fyne.Container
}

func newQuickWindow(u *UI, req app.Request) *quickWindow {
	q := &quickWindow{u: u, win: u.fy.NewWindow(i18n.T("Quick convert")), preset: req.Preset}
	q.win.Resize(fyne.NewSize(620, 600))
	reg := u.state.Registry()
	exp := app.Expand(reg, req.Files)
	q.files = exp.Files
	q.targets = app.CommonTargets(reg, q.files)
	q.saveDir = u.state.Settings().OutputDir
	q.errLabel = widget.NewLabel("")
	q.errLabel.Wrapping = fyne.TextWrapWord
	q.errLabel.Importance = widget.DangerImportance

	to, opts, err := u.state.Resolve(req.To, req.Preset)
	q.presetOptions = opts
	q.loadChoices()
	switch {
	case err != nil:
		q.setError(err.Error())
	case len(exp.Unreadable) > 0:
		q.setError(strings.Join(exp.Unreadable, "\n"))
	case len(q.files) == 0:
		q.setError(i18n.T("There are no files to convert here."))
	case to != nil && !slices.Contains(q.targets.Formats, to):
		q.setError(unreachable(to, len(q.files)))
	case to != nil:
		q.to = to
	}

	q.nameEntry = widget.NewEntry()
	q.nameEntry.OnChanged = func(string) { q.setError("") }
	q.targetBox = container.NewVBox()
	q.optionsBox = container.NewVBox()
	q.convertBtn = widget.NewButtonWithIcon(i18n.T("Convert"), theme.ConfirmIcon(), q.convert)
	q.convertBtn.Importance = widget.HighImportance
	cancel := widget.NewButton(i18n.T("Cancel"), func() { q.win.Close() })

	q.body = container.NewVBox(q.filesSummary(), widget.NewSeparator(), q.presetRow(), q.targetBox, q.optionsBox)
	footer := container.NewVBox(q.errLabel, container.NewHBox(layout.NewSpacer(), cancel, q.convertBtn))
	q.win.SetContent(container.NewBorder(nil, container.NewPadded(footer), nil, nil, container.NewPadded(container.NewVScroll(q.body))))
	q.win.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		if files := droppedPaths(uris); len(files) > 0 && len(q.jobs) == 0 {
			u.showQuick(app.Request{Files: append(slices.Clone(q.files), files...), To: req.To, Preset: q.preset})
			q.win.Close()
		}
	})
	q.rebuild()
	return q
}

func unreachable(to *core.Format, files int) string {
	if files == 1 {
		return i18n.T("This file can't become %s on this computer.", to.Name)
	}
	return i18n.T("Not every file can become %s on this computer.", to.Name)
}

func (q *quickWindow) setError(msg string) {
	if q.errLabel == nil {
		return
	}
	q.errLabel.SetText(msg)
	if msg == "" {
		q.errLabel.Hide()
	} else {
		q.errLabel.Show()
	}
}

// loadChoices starts the controls from the preset's options.
func (q *quickWindow) loadChoices() {
	o := q.presetOptions
	q.choices = app.QuickChoices{Quality: o.Quality, VideoCodec: o.VideoCodec, StripAudio: o.StripAudio, Background: o.Background}
	if q.to != nil {
		q.choices.Size = *app.SizeOption(&o, q.to)
	}
}

func (q *quickWindow) filesSummary() fyne.CanvasObject {
	var lines []fyne.CanvasObject
	switch len(q.files) {
	case 0:
	case 1:
		b := newBadge()
		b.set(q.files[0])
		name := widget.NewLabelWithStyle(filepath.Base(q.files[0]), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		name.Truncation = fyne.TextTruncateEllipsis
		size := widget.NewLabel(fileSize(q.files[0]) + " · " + filepath.Dir(q.files[0]))
		size.SizeName = theme.SizeNameCaptionText
		size.Truncation = fyne.TextTruncateEllipsis
		lines = append(lines, container.NewBorder(nil, nil, b.box, nil, container.NewVBox(name, size)))
	default:
		lines = append(lines, widget.NewLabelWithStyle(i18n.N(len(q.files), "%d file", "%d files"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
		for i, f := range q.files {
			if i == 5 {
				lines = append(lines, widget.NewLabel(i18n.T("and %d more", len(q.files)-5)))
				break
			}
			l := widget.NewLabel(filepath.Base(f))
			l.Truncation = fyne.TextTruncateEllipsis
			lines = append(lines, l)
		}
	}
	if n := len(q.targets.Unsupported); n > 0 {
		names := make([]string, 0, n)
		for _, f := range q.targets.Unsupported {
			names = append(names, filepath.Base(f))
		}
		warn := widget.NewLabel(i18n.T("Can't convert: %s", strings.Join(names, ", ")) + needsTool(q.targets.Unsupported))
		warn.Wrapping = fyne.TextWrapWord
		warn.Importance = widget.WarningImportance
		lines = append(lines, warn)
	}
	return container.NewVBox(lines...)
}

// needsTool names the missing pack when unsupported files need one.
func needsTool(files []string) string {
	for _, f := range files {
		if from := core.ByPath(f); from != nil {
			switch from.Category {
			case core.Document, core.Presentation, core.Spreadsheet:
				return ". " + i18n.T("Office files need LibreOffice: Settings › Tools.")
			case core.Video, core.Audio:
				return ". " + i18n.T("Video and audio need FFmpeg: Settings › Tools.")
			}
		}
	}
	return ""
}

func (q *quickWindow) presetRow() fyne.CanvasObject {
	presets := q.u.state.Presets()
	if len(presets) == 0 {
		return container.NewVBox()
	}
	names := []string{noPreset()}
	for _, p := range presets {
		if p.Err == nil {
			names = append(names, p.Name)
		}
	}
	sel := widget.NewSelect(names, func(name string) {
		if name == noPreset() {
			name = ""
		}
		if name == q.preset {
			return
		}
		q.preset = name
		q.presetOptions = core.Options{}
		if name != "" {
			to, opts, err := q.u.state.Resolve("", name)
			if err != nil {
				q.setError(err.Error())
				return
			}
			q.presetOptions = opts
			if to != nil {
				if slices.Contains(q.targets.Formats, to) {
					q.to = to
				} else {
					q.setError(unreachable(to, len(q.files)))
				}
			}
		}
		q.loadChoices()
		q.rebuild()
	})
	if q.preset != "" {
		sel.SetSelected(q.preset)
	} else {
		sel.SetSelected(noPreset())
	}
	return container.NewBorder(nil, nil, widget.NewLabel(i18n.T("Preset")), nil, sel)
}

// rebuild redraws the target grid and the options for the chosen target.
func (q *quickWindow) rebuild() {
	if len(q.jobs) > 0 {
		return
	}
	video := false
	for _, f := range q.files {
		if from := core.ByPath(f); from != nil && from.Category == core.Video {
			video = true
		}
	}
	var objs []fyne.CanvasObject
	objs = append(objs, widget.NewLabelWithStyle(i18n.T("Convert to"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	if len(q.targets.Formats) == 0 && len(q.files) > 0 {
		objs = append(objs, widget.NewLabel(i18n.T("Nothing these files can all become.")))
	}
	var group *fyne.Container
	var last core.Category = -1
	for _, f := range q.targets.Formats {
		if f.Category != last {
			last = f.Category
			cat := widget.NewLabel(f.Category.Label())
			cat.SizeName = theme.SizeNameCaptionText
			group = container.NewGridWrap(fyne.NewSize(120, 56))
			objs = append(objs, cat, group)
		}
		target := f
		b := widget.NewButton(f.Name+"\n"+app.Blurb(f, video), func() {
			if q.to != target {
				q.to = target
				o := q.presetOptions
				q.choices.Size = *app.SizeOption(&o, target)
			}
			q.setError("")
			q.rebuild()
		})
		if q.to == f {
			b.Importance = widget.HighImportance
		}
		group.Add(b)
	}
	q.targetBox.Objects = objs
	q.targetBox.Refresh()
	q.buildOptions()
	if q.to == nil || len(q.files) == len(q.targets.Unsupported) {
		q.convertBtn.Disable()
	} else {
		q.convertBtn.Enable()
	}
}

func (q *quickWindow) buildOptions() {
	to := q.to
	q.optionsBox.Objects = nil
	if to == nil {
		q.optionsBox.Refresh()
		return
	}
	form := widget.NewForm()
	if app.QualityApplies(to) {
		segments := app.QualitySegments()
		labels := make([]string, len(segments))
		for i, s := range segments {
			labels[i] = s.Label
		}
		radio := widget.NewRadioGroup(labels, nil)
		radio.Horizontal = true
		key := app.QualityKey(q.choices.Quality)
		for _, s := range segments {
			if s.Key == key {
				radio.Selected = s.Label
			}
		}
		if key == "preset" {
			radio.Options = append(radio.Options, i18n.T("Preset (%d)", *q.choices.Quality))
			radio.Selected = radio.Options[len(radio.Options)-1]
		}
		radio.OnChanged = func(label string) {
			for _, s := range segments {
				if s.Label == label {
					q.choices.Quality = s.Quality
				}
			}
		}
		form.Append(i18n.T("Quality"), radio)
	}
	if label, choices, ok := app.SizeChoices(to); ok {
		var names []string
		selected := ""
		for _, c := range choices {
			names = append(names, c.Label)
			if (c.Value == nil) == (q.choices.Size == nil) && (c.Value == nil || *c.Value == *q.choices.Size) {
				selected = c.Label
			}
		}
		if selected == "" && q.choices.Size != nil {
			selected = sizeLabel(to, *q.choices.Size)
			names = append(names, selected)
		}
		sel := widget.NewSelect(names, func(name string) {
			for _, c := range choices {
				if c.Label == name {
					q.choices.Size = c.Value
				}
			}
		})
		sel.SetSelected(selected)
		form.Append(label, sel)
	}
	if app.CodecApplies(to) {
		names := []string{i18n.T("H.264 (plays everywhere)"), i18n.T("HEVC (smaller files)")}
		sel := widget.NewSelect(names, func(name string) {
			if strings.HasPrefix(name, "HEVC") {
				q.choices.VideoCodec = core.Ptr(core.HEVC)
			} else {
				q.choices.VideoCodec = nil
			}
		})
		if q.choices.VideoCodec != nil && *q.choices.VideoCodec == core.HEVC {
			sel.SetSelected(names[1])
		} else {
			sel.SetSelected(names[0])
		}
		form.Append(i18n.T("Codec"), sel)
	}
	if app.AudioApplies(to) {
		check := widget.NewCheck(i18n.T("Keep audio"), func(on bool) { q.choices.StripAudio = !on })
		check.Checked = !q.choices.StripAudio
		form.Append(i18n.T("Audio"), check)
	}
	if app.BackgroundApplies(to, q.files) {
		def := app.DefaultBackground(q.u.state.Registry(), to, q.files)
		auto := i18n.T("Automatic")
		if def != nil {
			auto = i18n.T("Automatic (%s)", def.Name())
		}
		names := []string{auto}
		var values []*core.Background
		values = append(values, nil)
		for _, b := range core.BackgroundChoices {
			if b.Transparent && !to.KeepsTransparency() {
				continue
			}
			names = append(names, b.Name())
			values = append(values, &b)
		}
		shown := app.ShownBackground(to, q.choices.Background)
		selected := auto
		if shown != nil {
			selected = shown.Name()
			if !slices.Contains(names, selected) {
				names = append(names, selected)
				values = append(values, shown)
			}
		}
		sel := widget.NewSelect(names, func(name string) {
			i := slices.Index(names, name)
			if i >= 0 {
				q.choices.Background = values[i]
			}
		})
		sel.SetSelected(selected)
		form.Append(i18n.T("Background"), sel)
	}
	where := widget.NewLabel(i18n.T("Next to the original"))
	if q.saveDir != "" {
		where.SetText(app.DisplayPath(q.saveDir))
	}
	where.Truncation = fyne.TextTruncateEllipsis
	change := widget.NewButton(i18n.T("Change…"), func() {
		q.u.pickFolder(q.win, q.saveDir, func(dir string) { q.saveDir = dir; q.buildOptions() })
	})
	reset := widget.NewButton(i18n.T("Beside"), func() { q.saveDir = ""; q.buildOptions() })
	if q.saveDir == "" {
		reset.Hide()
	}
	form.Append(i18n.T("Save to"), container.NewBorder(nil, nil, nil, container.NewHBox(reset, change), where))
	if len(q.files) == 1 && len(q.targets.Unsupported) == 0 {
		q.nameEntry.SetText(app.DefaultName(q.files[0], to))
		form.Append(i18n.T("File name"), q.nameEntry)
	}
	q.optionsBox.Objects = []fyne.CanvasObject{widget.NewSeparator(), form}
	q.optionsBox.Refresh()
}

func sizeLabel(to *core.Format, v int) string {
	switch to.Category {
	case core.Video:
		return fmt.Sprintf("%dp", v)
	case core.Audio:
		return fmt.Sprintf("%d kbit/s", v)
	}
	return fmt.Sprintf("%d px", v)
}

// output is where the results go, or why the typed name can't be used.
func (q *quickWindow) output() (core.Output, string) {
	if len(q.files) == 1 && q.to != nil {
		name, why := app.OutputName(q.nameEntry.Text, q.to.Extension())
		if why != "" {
			return core.Output{}, why
		}
		if name != app.DefaultName(q.files[0], q.to) {
			dir := q.saveDir
			if dir == "" {
				dir = filepath.Dir(q.files[0])
			}
			return core.Output{Kind: core.Exact, Path: filepath.Join(dir, name)}, ""
		}
	}
	if q.saveDir != "" {
		return core.Output{Kind: core.InDir, Path: q.saveDir}, ""
	}
	return core.Output{}, ""
}

func (q *quickWindow) convert() {
	if q.to == nil || len(q.jobs) > 0 {
		return
	}
	var files []string
	for _, f := range q.files {
		if !slices.Contains(q.targets.Unsupported, f) {
			files = append(files, f)
		}
	}
	opts := app.ConversionOptions(q.presetOptions, q.to, q.choices, files)
	out, why := q.output()
	if why != "" {
		q.setError(why)
		return
	}
	ids, err := q.u.state.ConvertTo(files, q.to, opts, out, false)
	if err != nil {
		q.setError(err.Error())
		return
	}
	q.jobs = ids
	q.showProgress()
}

// showProgress replaces the choices with each job's progress.
func (q *quickWindow) showProgress() {
	q.progress = container.NewVBox()
	q.body.Objects = []fyne.CanvasObject{
		widget.NewLabelWithStyle(i18n.T("Converting to %s", q.to.Name), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		q.progress,
	}
	q.body.Refresh()
	q.convertBtn.SetText(i18n.T("Done"))
	q.convertBtn.SetIcon(nil)
	q.convertBtn.OnTapped = func() { q.win.Close() }
	q.refresh()
}

func (q *quickWindow) refresh() {
	if q.progress == nil {
		return
	}
	var rows []fyne.CanvasObject
	busy := false
	for _, id := range q.jobs {
		e, ok := q.u.state.Jobs.Get(id)
		if !ok {
			continue
		}
		name := widget.NewLabel(filepath.Base(e.Input))
		name.Truncation = fyne.TextTruncateEllipsis
		var status fyne.CanvasObject
		var action fyne.CanvasObject = layout.NewSpacer()
		switch {
		case e.Status == app.Queued:
			status = widget.NewLabel(i18n.T("Waiting"))
			busy = true
		case e.Status == app.Running && e.Progress < 0:
			status = widget.NewProgressBarInfinite()
			busy = true
		case e.Status == app.Running:
			bar := widget.NewProgressBar()
			bar.SetValue(e.Progress)
			status = bar
			busy = true
		case e.Outcome == app.Done:
			l := widget.NewLabel(i18n.T("Done"))
			l.Importance = widget.SuccessImportance
			status = l
			outs := e.Outputs
			action = widget.NewButtonWithIcon(i18n.T("Show"), theme.FolderOpenIcon(), func() { app.Reveal(outs[0]) })
		case e.Outcome == app.Failed:
			l := widget.NewLabel(e.Err)
			l.Wrapping = fyne.TextWrapWord
			l.Importance = widget.DangerImportance
			status = l
		default:
			status = widget.NewLabel(i18n.T("Cancelled"))
		}
		if e.Status != app.Finished {
			id := e.ID
			action = widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() { q.u.state.Jobs.Cancel(id) })
		}
		rows = append(rows, container.NewBorder(nil, nil, nil, action, container.NewVBox(name, status)))
	}
	q.progress.Objects = rows
	q.progress.Refresh()
	if busy {
		q.convertBtn.SetText(i18n.T("Close"))
	} else {
		q.convertBtn.SetText(i18n.T("Done"))
	}
}
