package gui

import (
	"errors"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"conversor/internal/app"
	"conversor/internal/core"
	"conversor/internal/i18n"
)

type page int

const (
	pageActivity page = iota
	pageAutomations
)

// recentRows is how many history rows Activity shows.
const recentRows = 200

// activityRow is a running job or a finished conversion from history.
type activityRow struct {
	live  bool
	entry app.Entry
	rec   app.Record
}

// mainWindow shows Activity (running jobs and history in one list) and
// Automations.
type mainWindow struct {
	u       *UI
	win     fyne.Window
	visible bool
	page    page

	rows        []activityRow
	list        *widget.List
	empty       *fyne.Container
	activity    *fyne.Container
	automations *fyne.Container
	rulesBox    *fyne.Container
	content     *fyne.Container
	navActivity *widget.Button
	navAuto     *widget.Button
}

func newMainWindow(u *UI) *mainWindow {
	m := &mainWindow{u: u, win: u.fy.NewWindow("Conversor")}
	m.win.Resize(fyne.NewSize(760, 520))
	m.win.SetMaster()

	m.list = widget.NewList(func() int { return len(m.rows) }, m.newRow, m.updateRow)
	add := widget.NewButtonWithIcon(i18n.T("Add files…"), theme.ContentAddIcon(), func() {
		u.pickFiles(m.win, func(files []string) { u.addFiles(files, m.win) })
	})
	add.Importance = widget.HighImportance
	clearBtn := widget.NewButtonWithIcon(i18n.T("Clear"), theme.ContentClearIcon(), func() {
		confirm(i18n.T("Clear activity?"), i18n.T("This forgets finished conversions. The files stay where they are."), i18n.T("Clear"),
			u.state.ClearHistory, m.win)
	})
	title := widget.NewLabelWithStyle(i18n.T("Activity"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	m.empty = container.NewCenter(container.NewVBox(
		widget.NewLabelWithStyle(i18n.T("Drop files here to convert them"), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle(i18n.T("Photos become JPEG, other images PNG, video MP4, audio MP3 and documents PDF.\nChange that in Settings, or use Add files to pick."), fyne.TextAlignCenter, fyne.TextStyle{}),
	))
	m.activity = container.NewBorder(container.NewHBox(title, layout.NewSpacer(), clearBtn, add), nil, nil, nil,
		container.NewStack(m.list, m.empty))

	m.rulesBox = container.NewVBox()
	addRule := widget.NewButtonWithIcon(i18n.T("Add rule"), theme.ContentAddIcon(), func() { m.editRule(-1) })
	m.automations = container.NewBorder(
		container.NewVBox(
			container.NewHBox(widget.NewLabelWithStyle(i18n.T("Automations"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), layout.NewSpacer(), addRule),
			lowLabel(i18n.T("Each rule watches one folder and converts new files as they appear. Files already there are left alone.")),
		), nil, nil, nil, container.NewVScroll(m.rulesBox))

	m.navActivity = widget.NewButtonWithIcon(i18n.T("Activity"), theme.HistoryIcon(), func() { m.showPage(pageActivity) })
	m.navAuto = widget.NewButtonWithIcon(i18n.T("Automations"), theme.ViewRefreshIcon(), func() { m.showPage(pageAutomations) })
	for _, b := range []*widget.Button{m.navActivity, m.navAuto} {
		b.Alignment = widget.ButtonAlignLeading
	}
	settings := widget.NewButtonWithIcon(i18n.T("Settings"), theme.SettingsIcon(), func() { u.showSettings("") })
	settings.Alignment = widget.ButtonAlignLeading
	sidebar := container.NewBorder(container.NewVBox(m.navActivity, m.navAuto), settings, nil, nil)
	m.content = container.NewStack(m.activity)
	m.win.SetContent(container.NewBorder(nil, nil, container.NewPadded(sidebar), nil, container.NewPadded(m.content)))

	m.win.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		if files := droppedPaths(uris); len(files) > 0 {
			u.addFiles(files, m.win)
		}
	})
	m.win.SetCloseIntercept(func() {
		m.visible = false
		m.win.Hide()
		u.maybeQuit()
	})
	m.showPage(pageActivity)
	m.refresh()
	return m
}

func (m *mainWindow) show() {
	m.visible = true
	m.u.quitting = false
	m.win.Show()
	m.win.RequestFocus()
}

func (m *mainWindow) showPage(p page) {
	m.page = p
	m.navActivity.Importance = widget.LowImportance
	m.navAuto.Importance = widget.LowImportance
	switch p {
	case pageAutomations:
		m.content.Objects = []fyne.CanvasObject{m.automations}
		m.navAuto.Importance = widget.MediumImportance
	default:
		m.content.Objects = []fyne.CanvasObject{m.activity}
		m.navActivity.Importance = widget.MediumImportance
	}
	m.navActivity.Refresh()
	m.navAuto.Refresh()
	m.content.Refresh()
	m.refresh()
	m.show()
}

func (m *mainWindow) refresh() {
	var rows []activityRow
	for _, e := range m.u.state.Jobs.Snapshot() {
		if e.Status != app.Finished {
			rows = append(rows, activityRow{live: true, entry: e})
		}
	}
	for _, r := range m.u.state.History(recentRows) {
		rows = append(rows, activityRow{rec: r})
	}
	m.rows = rows
	if len(rows) == 0 {
		m.empty.Show()
	} else {
		m.empty.Hide()
	}
	m.list.Refresh()
	m.refreshRules()
}

// rowParts are the widgets of one Activity row.
type rowParts struct {
	row      *fyne.Container
	badge    *badge
	title    *widget.Label
	status   *widget.Label
	progress *widget.ProgressBar
	primary  *widget.Button
	details  *widget.Button
	remove   *widget.Button
}

func (m *mainWindow) newRow() fyne.CanvasObject {
	p := &rowParts{
		badge:    newBadge(),
		title:    widget.NewLabel(""),
		status:   widget.NewLabel(""),
		progress: widget.NewProgressBar(),
		primary:  widget.NewButton("", nil),
		details:  widget.NewButtonWithIcon("", theme.InfoIcon(), nil),
		remove:   widget.NewButtonWithIcon("", theme.ContentClearIcon(), nil),
	}
	p.title.Truncation = fyne.TextTruncateEllipsis
	p.title.TextStyle.Bold = true
	p.status.Truncation = fyne.TextTruncateEllipsis
	p.status.SizeName = theme.SizeNameCaptionText
	p.details.Importance = widget.LowImportance
	p.remove.Importance = widget.LowImportance
	// Spacers center the text against the badge while it keeps the full
	// width (a centering container would shrink truncating labels to "…").
	text := container.NewVBox(layout.NewSpacer(), p.title, p.status, p.progress, layout.NewSpacer())
	buttons := container.NewHBox(p.details, p.primary, p.remove)
	row := container.NewBorder(nil, nil, p.badge.box, container.NewCenter(buttons), text)
	p.row = row
	w := &rowWidget{content: row, parts: p}
	w.ExtendBaseWidget(w)
	return w
}

// rowWidget is one list row; it carries its parts so updateRow can find
// them.
type rowWidget struct {
	widget.BaseWidget
	content fyne.CanvasObject
	parts   *rowParts
}

func (r *rowWidget) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(r.content) }

func (m *mainWindow) updateRow(i widget.ListItemID, obj fyne.CanvasObject) {
	if i >= len(m.rows) {
		return
	}
	p := obj.(*rowWidget).parts
	m.fillRow(m.rows[i], p)
	// Button texts changed width; lay the row out again.
	p.row.Refresh()
}

func (m *mainWindow) fillRow(r activityRow, p *rowParts) {
	p.details.Hide()
	p.remove.Hide()
	p.progress.Hide()
	if r.live {
		e := r.entry
		p.badge.set(e.Input)
		p.title.SetText(formatLabel(e.Input, e.To))
		switch {
		case e.Status == app.Queued:
			p.status.SetText(i18n.T("Waiting"))
		case e.Progress < 0:
			p.status.SetText(i18n.T("Converting…"))
		default:
			text := i18n.T("Converting… %d%%", int(e.Progress*100))
			if left, ok := e.Remaining(time.Now()); ok {
				text += " · " + timeLeft(left)
			}
			p.status.SetText(text)
			p.progress.SetValue(e.Progress)
			p.progress.Show()
		}
		p.primary.SetText(i18n.T("Stop"))
		p.primary.SetIcon(theme.MediaStopIcon())
		id := e.ID
		p.primary.OnTapped = func() { m.u.state.Jobs.Cancel(id) }
		return
	}
	rec := r.rec
	p.badge.set(rec.Input)
	to := core.ByID(rec.To)
	if to == nil {
		to = &core.Format{ID: rec.To, Name: rec.To}
	}
	p.title.SetText(formatLabel(rec.Input, to))
	at := when(rec.FinishedAt, time.Now())
	id := rec.ID
	p.remove.OnTapped = func() { m.u.state.ForgetHistory(id) }
	p.remove.Show()
	switch rec.Outcome {
	case app.Done:
		status := i18n.T("Done") + " · " + at
		if len(rec.Outputs) > 1 {
			status += " · " + i18n.N(len(rec.Outputs), "%d file", "%d files")
		} else if len(rec.Outputs) == 1 {
			if s := fileSize(rec.Outputs[0]); s != "" {
				status += " · " + s
			} else {
				status += " · " + i18n.T("moved or deleted")
			}
		}
		p.status.SetText(status)
		p.primary.SetText(i18n.T("Show"))
		p.primary.SetIcon(theme.FolderOpenIcon())
		outputs := rec.Outputs
		p.primary.OnTapped = func() {
			if len(outputs) > 0 {
				if err := app.Reveal(outputs[0]); err != nil {
					showError(err, m.win)
				}
			}
		}
	default:
		if rec.Outcome == app.Failed {
			p.status.SetText(i18n.T("Failed") + " · " + at + " · " + rec.Error)
			msg := rec.Error
			name := filepath.Base(rec.Input)
			p.details.OnTapped = func() {
				showError(errors.New(i18n.T("%s could not be converted:\n\n%s", name, msg)), m.win)
			}
			p.details.Show()
		} else {
			p.status.SetText(i18n.T("Cancelled") + " · " + at)
		}
		p.primary.SetText(i18n.T("Retry"))
		p.primary.SetIcon(theme.ViewRefreshIcon())
		input, toID, setup := rec.Input, rec.To, rec.Setup
		p.primary.OnTapped = func() {
			if _, err := m.u.state.Retry(input, toID, setup); err != nil {
				showError(err, m.win)
			}
		}
	}
}

// refreshRules redraws the Automations page.
func (m *mainWindow) refreshRules() {
	rules := m.u.state.Settings().Automations
	var objs []fyne.CanvasObject
	for i, rule := range rules {
		check := widget.NewCheck(rule.String(), func(on bool) {
			m.u.state.UpdateSettings(func(s *app.Settings) {
				if i < len(s.Automations) {
					s.Automations[i].Enabled = on
				}
			})
		})
		check.Checked = rule.Enabled
		line := widget.NewLabel(rule.SourceLine() + " · " + rule.Detail())
		line.SizeName = theme.SizeNameCaptionText
		line.Truncation = fyne.TextTruncateEllipsis
		edit := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() { m.editRule(i) })
		del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
			confirm(i18n.T("Delete rule?"), i18n.T("“%s” stops watching for new files.", rule.String()), i18n.T("Delete"), func() {
				m.u.state.UpdateSettings(func(s *app.Settings) {
					if i < len(s.Automations) {
						s.Automations = append(s.Automations[:i], s.Automations[i+1:]...)
					}
				})
			}, m.win)
		})
		edit.Importance, del.Importance = widget.LowImportance, widget.LowImportance
		objs = append(objs, container.NewBorder(nil, nil, nil, container.NewHBox(edit, del), container.NewVBox(check, line)), widget.NewSeparator())
	}
	if len(rules) == 0 {
		objs = append(objs, widget.NewLabel(i18n.T("No rules yet. Add one to convert screenshots, recordings or downloads as they arrive.")))
	}
	m.rulesBox.Objects = objs
	m.rulesBox.Refresh()
}

var watchKinds = []app.WatchKind{app.WatchScreenshot, app.WatchRecording, app.WatchFolder}

func watchLabel(k app.WatchKind) string {
	switch k {
	case app.WatchScreenshot:
		return i18n.T("Screenshots")
	case app.WatchRecording:
		return i18n.T("Screen recordings")
	}
	return i18n.T("A folder")
}

func watchLabels() []string {
	var out []string
	for _, k := range watchKinds {
		out = append(out, watchLabel(k))
	}
	return out
}

func watchKindOf(label string) app.WatchKind {
	for _, k := range watchKinds {
		if watchLabel(k) == label {
			return k
		}
	}
	return app.WatchFolder
}

// editRule opens the rule form; index -1 adds a rule.
func (m *mainWindow) editRule(index int) {
	rules := m.u.state.Settings().Automations
	rule := app.Automation{Name: i18n.T("Downloads"), To: "jpeg", Enabled: true, Watch: app.WatchFolder, Source: "Downloads"}
	if index >= 0 && index < len(rules) {
		rule = rules[index]
	}
	name := widget.NewEntry()
	name.SetText(rule.Name)
	folder := widget.NewEntry()
	folder.SetText(rule.Folder)
	folder.SetPlaceHolder(i18n.T("The system's location"))
	browse := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		m.u.pickFolder(m.win, folder.Text, func(dir string) { folder.SetText(dir) })
	})
	watch := widget.NewSelect(watchLabels(), nil)
	watch.SetSelected(watchLabel(rule.Watch))
	var formats []string
	for _, f := range core.Formats {
		formats = append(formats, f.Name)
	}
	to := widget.NewSelect(formats, nil)
	if f := core.ByID(rule.To); f != nil {
		to.SetSelected(f.Name)
	}
	copyCheck := widget.NewCheck(i18n.T("Copy the result to the clipboard"), nil)
	copyCheck.Checked = rule.CopyToClipboard
	items := []*widget.FormItem{
		widget.NewFormItem(i18n.T("Name"), name),
		widget.NewFormItem(i18n.T("Watch"), watch),
		widget.NewFormItem(i18n.T("Folder"), container.NewBorder(nil, nil, nil, browse, folder)),
		widget.NewFormItem(i18n.T("Convert to"), to),
		widget.NewFormItem("", copyCheck),
	}
	title := i18n.T("Edit rule")
	if index < 0 {
		title = i18n.T("Add rule")
	}
	d := dialog.NewForm(title, i18n.T("Save"), i18n.T("Cancel"), items, func(ok bool) {
		if !ok {
			return
		}
		r := rule
		r.Name, r.Watch, r.Folder, r.CopyToClipboard = name.Text, watchKindOf(watch.Selected), folder.Text, copyCheck.Checked
		for _, f := range core.Formats {
			if f.Name == to.Selected {
				r.To = f.ID
			}
		}
		if r.Folder == "" && r.Watch == app.WatchFolder {
			r.Source = r.Name
		}
		m.u.state.UpdateSettings(func(s *app.Settings) {
			if index >= 0 && index < len(s.Automations) {
				s.Automations[index] = r
			} else {
				s.Automations = append(s.Automations, r)
			}
		})
	}, m.win)
	d.Resize(fyne.NewSize(460, 320))
	d.Show()
}
