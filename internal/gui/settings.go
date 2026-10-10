package gui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"conversor/internal/app"
	"conversor/internal/core"
	"conversor/internal/engines"
	"conversor/internal/i18n"
	"conversor/internal/integrate"
)

type settingsWindow struct {
	u    *UI
	win  fyne.Window
	tabs *container.AppTabs

	presetList  *widget.List
	presetNames []string
	presetForm  *presetForm
	toolsBox    *fyne.Container
	menuStatus  *widget.Label
	installing  map[string]bool
}

func newSettingsWindow(u *UI) *settingsWindow {
	s := &settingsWindow{u: u, win: u.fy.NewWindow(i18n.T("Settings")), installing: map[string]bool{}}
	s.win.Resize(fyne.NewSize(680, 540))
	s.tabs = container.NewAppTabs(
		container.NewTabItemWithIcon(i18n.T("General"), theme.SettingsIcon(), s.general()),
		container.NewTabItemWithIcon(i18n.T("Presets"), theme.DocumentIcon(), s.presets()),
		container.NewTabItemWithIcon(i18n.T("Tools"), theme.StorageIcon(), s.tools()),
		container.NewTabItemWithIcon(i18n.T("Right-click menu"), theme.MenuIcon(), s.menus()),
	)
	s.win.SetContent(s.tabs)
	return s
}

// The tabs, in order, by the names selectTab takes.
var settingsTabs = []string{"general", "presets", "tools", "menu"}

// selectTab shows a tab by its name in settingsTabs; "" keeps the current
// one.
func (s *settingsWindow) selectTab(name string) {
	if i := slices.Index(settingsTabs, name); i >= 0 {
		s.tabs.SelectIndex(i)
	}
}

func (s *settingsWindow) refresh() {
	s.refreshPresets()
	s.refreshTools()
}

func section(title string, objs ...fyne.CanvasObject) fyne.CanvasObject {
	head := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	return container.NewVBox(append([]fyne.CanvasObject{head}, objs...)...)
}

func (s *settingsWindow) general() fyne.CanvasObject {
	st := s.u.state.Settings()
	update := func(f func(*app.Settings)) {
		if err := s.u.state.UpdateSettings(f); err != nil {
			showError(err, s.win)
		}
	}

	where := widget.NewLabel("")
	var reset *widget.Button
	showWhere := func(dir string) {
		if dir == "" {
			where.SetText(i18n.T("Next to each original"))
			reset.Hide()
		} else {
			where.SetText(app.DisplayPath(dir))
			reset.Show()
		}
	}
	reset = widget.NewButton(i18n.T("Beside originals"), func() {
		update(func(x *app.Settings) { x.OutputDir = "" })
		showWhere("")
	})
	change := widget.NewButton(i18n.T("Choose…"), func() {
		s.u.pickFolder(s.win, s.u.state.Settings().OutputDir, func(dir string) {
			update(func(x *app.Settings) { x.OutputDir = dir })
			showWhere(dir)
		})
	})
	showWhere(st.OutputDir)

	counts := []string{i18n.T("Auto")}
	for i := 1; i <= 16; i++ {
		counts = append(counts, strconv.Itoa(i))
	}
	jobs := widget.NewSelect(counts, func(v string) {
		n, _ := strconv.Atoi(v)
		update(func(x *app.Settings) { x.Concurrency = n })
	})
	if st.Concurrency > 0 {
		jobs.SetSelected(strconv.Itoa(st.Concurrency))
	} else {
		jobs.SetSelected(i18n.T("Auto"))
	}

	notify := widget.NewCheck(i18n.T("Notify me when conversions finish in the background"), func(on bool) {
		update(func(x *app.Settings) { x.Notifications = on })
	})
	notify.Checked = st.Notifications
	reveal := widget.NewCheck(i18n.T("Show each converted file in the file manager"), func(on bool) {
		update(func(x *app.Settings) { x.RevealWhenDone = on })
	})
	reveal.Checked = st.RevealWhenDone
	keep := widget.NewCheck(i18n.T("Keep running in the background (tray icon)"), func(on bool) {
		update(func(x *app.Settings) { x.KeepRunning = on })
		s.u.refreshTray()
	})
	keep.Checked = st.KeepRunning
	if !s.u.trayOK {
		keep.SetText(i18n.T("Keep running in the background (this desktop has no tray icons)"))
		keep.Disable()
	}

	defaults := widget.NewForm()
	kinds := []struct {
		label string
		get   func(*app.Defaults) *string
		of    core.Category
		photo bool
	}{
		{i18n.T("Photos (JPEG, HEIC, WebP, AVIF)"), func(d *app.Defaults) *string { return &d.Photos }, core.Image, true},
		{i18n.T("Other images"), func(d *app.Defaults) *string { return &d.Images }, core.Image, false},
		{i18n.T("Video"), func(d *app.Defaults) *string { return &d.Video }, core.Video, false},
		{i18n.T("Audio"), func(d *app.Defaults) *string { return &d.Audio }, core.Audio, false},
		{i18n.T("Documents"), func(d *app.Defaults) *string { return &d.Documents }, core.Document, false},
	}
	for _, k := range kinds {
		var names []string
		var ids []string
		for _, f := range defaultChoices(k.of) {
			names = append(names, f.Name)
			ids = append(ids, f.ID)
		}
		sel := widget.NewSelect(names, nil)
		cur := *k.get(&st.Defaults)
		for i, id := range ids {
			if id == cur {
				sel.SetSelected(names[i])
			}
		}
		get := k.get
		sel.OnChanged = func(name string) {
			for i, n := range names {
				if n == name {
					update(func(x *app.Settings) { *get(&x.Defaults) = ids[i] })
				}
			}
		}
		defaults.Append(k.label, sel)
	}

	about := widget.NewLabel(fmt.Sprintf("Conversor %s · AGPL-3.0", s.u.Version))
	about.Importance = widget.LowImportance
	return container.NewVScroll(container.NewPadded(container.NewVBox(
		section(i18n.T("Save converted files"), container.NewBorder(nil, nil, nil, container.NewHBox(reset, change), where)),
		widget.NewSeparator(),
		section(i18n.T("Conversions at once"), container.NewBorder(nil, nil, nil, layout.NewSpacer(), jobs),
			lowLabel(i18n.T("Video always converts one file at a time."))),
		widget.NewSeparator(),
		section(i18n.T("Behavior"), notify, reveal, keep),
		widget.NewSeparator(),
		section(i18n.T("Dropped and added files become"), defaults),
		widget.NewSeparator(),
		about,
	)))
}

// defaultChoices are the formats offered as a kind's default target.
func defaultChoices(c core.Category) []*core.Format {
	var out []*core.Format
	for _, f := range core.Formats {
		switch c {
		case core.Document:
			if f.Category == core.Document || f.Category == core.PDF {
				out = append(out, f)
			}
		default:
			if f.Category == c {
				out = append(out, f)
			}
		}
	}
	return out
}

// lowLabel is secondary text: smaller, wrapped, still readable.
func lowLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.SizeName = theme.SizeNameCaptionText
	l.Wrapping = fyne.TextWrapWord
	return l
}

// presetForm edits one preset.
type presetForm struct {
	editing                                        string
	name, quality, maxSize, height, bitrate, pages *widget.Entry
	dpi                                            *widget.Entry
	to, codec, background                          *widget.Select
	strip                                          *widget.Check
	form                                           *widget.Form
}

func (s *settingsWindow) presets() fyne.CanvasObject {
	f := &presetForm{
		name: widget.NewEntry(), quality: widget.NewEntry(), maxSize: widget.NewEntry(), height: widget.NewEntry(),
		bitrate: widget.NewEntry(), pages: widget.NewEntry(), dpi: widget.NewEntry(),
		strip: widget.NewCheck(i18n.T("Leave out audio"), nil),
	}
	s.presetForm = f
	targets := []string{i18n.T("Any (use --to)")}
	for _, x := range core.Formats {
		targets = append(targets, x.Name)
	}
	f.to = widget.NewSelect(targets, nil)
	f.codec = widget.NewSelect([]string{i18n.T("Default (H.264)"), "H.264", "HEVC"}, nil)
	backgrounds := []string{i18n.T("Default")}
	for _, b := range core.BackgroundChoices {
		backgrounds = append(backgrounds, b.Name())
	}
	f.background = widget.NewSelect(backgrounds, nil)
	f.quality.SetPlaceHolder("1-100")
	f.maxSize.SetPlaceHolder(i18n.T("pixels"))
	f.height.SetPlaceHolder(i18n.T("pixels, even"))
	f.bitrate.SetPlaceHolder("kbit/s")
	f.pages.SetPlaceHolder(i18n.T("2, 1-3 or 4-"))
	f.dpi.SetPlaceHolder("18-1200")
	f.form = widget.NewForm(
		widget.NewFormItem(i18n.T("Name"), f.name),
		widget.NewFormItem(i18n.T("Convert to"), f.to),
		widget.NewFormItem(i18n.T("Quality"), f.quality),
		widget.NewFormItem(i18n.T("Longest edge"), f.maxSize),
		widget.NewFormItem(i18n.T("Video height"), f.height),
		widget.NewFormItem(i18n.T("Audio bitrate"), f.bitrate),
		widget.NewFormItem(i18n.T("Video codec"), f.codec),
		widget.NewFormItem(i18n.T("Background"), f.background),
		widget.NewFormItem(i18n.T("PDF pages"), f.pages),
		widget.NewFormItem("DPI", f.dpi),
		widget.NewFormItem("", f.strip),
	)
	s.loadPreset("", core.Preset{})

	s.presetList = widget.NewList(func() int { return len(s.presetNames) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) { o.(*widget.Label).SetText(s.presetNames[i]) })
	s.presetList.OnSelected = func(i widget.ListItemID) {
		name := s.presetNames[i]
		p, err := s.u.state.LoadPreset(name)
		if err != nil {
			showError(err, s.win)
			return
		}
		s.loadPreset(name, p)
	}
	s.refreshPresets()

	newBtn := widget.NewButtonWithIcon(i18n.T("New"), theme.ContentAddIcon(), func() {
		s.presetList.UnselectAll()
		s.loadPreset("", core.Preset{})
	})
	save := widget.NewButtonWithIcon(i18n.T("Save"), theme.DocumentSaveIcon(), s.savePreset)
	save.Importance = widget.HighImportance
	del := widget.NewButtonWithIcon(i18n.T("Delete"), theme.DeleteIcon(), func() {
		if f.editing == "" {
			return
		}
		name := f.editing
		confirm(i18n.T("Delete preset?"), i18n.T("“%s” will be gone from the app and the command line.", name), i18n.T("Delete"), func() {
			s.u.state.DeletePreset(name)
			s.presetList.UnselectAll()
			s.loadPreset("", core.Preset{})
		}, s.win)
	})
	hint := lowLabel(i18n.T("Presets are TOML files shared with the conversor command (conversor -p NAME). Empty fields leave the setting to the engine."))
	left := container.NewBorder(nil, newBtn, nil, nil, s.presetList)
	right := container.NewBorder(nil, container.NewVBox(hint, container.NewHBox(layout.NewSpacer(), del, save)), nil, nil, container.NewVScroll(f.form))
	split := container.NewHSplit(left, right)
	split.Offset = 0.28
	return container.NewPadded(split)
}

func (s *settingsWindow) refreshPresets() {
	if s.presetList == nil {
		return
	}
	s.presetNames = nil
	for _, p := range s.u.state.Presets() {
		s.presetNames = append(s.presetNames, p.Name)
	}
	s.presetList.Refresh()
}

func (s *settingsWindow) loadPreset(name string, p core.Preset) {
	f := s.presetForm
	f.editing = name
	f.name.SetText(name)
	f.to.SetSelected(f.to.Options[0])
	if t := core.ByID(p.To); t != nil {
		f.to.SetSelected(t.Name)
	}
	num := func(e *widget.Entry, v *int) {
		e.SetText("")
		if v != nil {
			e.SetText(strconv.Itoa(*v))
		}
	}
	o := p.Options
	num(f.quality, o.Quality)
	num(f.maxSize, o.MaxSize)
	num(f.height, o.VideoHeight)
	num(f.bitrate, o.AudioBitrate)
	num(f.dpi, o.DPI)
	f.pages.SetText("")
	if o.Pages != nil {
		f.pages.SetText(o.Pages.String())
	}
	f.codec.SetSelected(i18n.T("Default (H.264)"))
	if o.VideoCodec != nil {
		f.codec.SetSelected(o.VideoCodec.Name())
	}
	f.background.SetSelected(i18n.T("Default"))
	if o.Background != nil {
		if !slices.Contains(f.background.Options, o.Background.Name()) {
			f.background.Options = append(f.background.Options, o.Background.Name())
		}
		f.background.SetSelected(o.Background.Name())
	}
	f.strip.SetChecked(o.StripAudio)
}

func (s *settingsWindow) savePreset() {
	f := s.presetForm
	var p core.Preset
	var problems []string
	num := func(label string, e *widget.Entry) *int {
		t := strings.TrimSpace(e.Text)
		if t == "" {
			return nil
		}
		n, err := strconv.Atoi(t)
		if err != nil {
			problems = append(problems, i18n.T("%s must be a number.", label))
			return nil
		}
		return &n
	}
	o := &p.Options
	o.Quality = num(i18n.T("Quality"), f.quality)
	o.MaxSize = num(i18n.T("Longest edge"), f.maxSize)
	o.VideoHeight = num(i18n.T("Video height"), f.height)
	o.AudioBitrate = num(i18n.T("Audio bitrate"), f.bitrate)
	o.DPI = num("DPI", f.dpi)
	if t := strings.TrimSpace(f.pages.Text); t != "" {
		r, err := core.ParsePageRange(t)
		if err != nil {
			problems = append(problems, i18n.T("PDF pages: use 2, 1-3 or 4-."))
		} else {
			o.Pages = &r
		}
	}
	switch f.codec.Selected {
	case "H.264":
		o.VideoCodec = core.Ptr(core.H264)
	case "HEVC":
		o.VideoCodec = core.Ptr(core.HEVC)
	}
	if sel := f.background.Selected; sel != "" && sel != i18n.T("Default") {
		for _, b := range core.BackgroundChoices {
			if b.Name() == sel {
				o.Background = &b
			}
		}
		// A color from the preset file shows as #RRGGBB.
		if b, err := core.ParseBackground(sel); o.Background == nil && err == nil {
			o.Background = &b
		}
	}
	o.StripAudio = f.strip.Checked
	for _, x := range core.Formats {
		if x.Name == f.to.Selected {
			p.To = x.ID
		}
	}
	if len(problems) > 0 {
		showError(errors.New(strings.Join(problems, "\n")), s.win)
		return
	}
	name := strings.TrimSpace(f.name.Text)
	if err := s.u.state.SavePreset(name, f.editing, p); err != nil {
		showError(err, s.win)
		return
	}
	f.editing = name
	s.refreshPresets()
}

func (s *settingsWindow) tools() fyne.CanvasObject {
	s.toolsBox = container.NewVBox()
	s.refreshTools()
	rescan := widget.NewButtonWithIcon(i18n.T("Look again"), theme.ViewRefreshIcon(), func() { s.u.state.RebuildRegistry() })
	return container.NewVScroll(container.NewPadded(container.NewVBox(
		lowLabel(i18n.T("Images, PDFs and SVG need nothing extra. These free tools add the rest; installing uses winget on Windows and Homebrew on macOS. Nothing else ever reaches the network.")),
		s.toolsBox,
		container.NewHBox(layout.NewSpacer(), rescan),
	)))
}

func (s *settingsWindow) refreshTools() {
	if s.toolsBox == nil {
		return
	}
	var objs []fyne.CanvasObject
	for _, p := range engines.Packs {
		where := p.Find()
		status := widget.NewLabel("")
		status.Wrapping = fyne.TextWrapWord
		var action fyne.CanvasObject = layout.NewSpacer()
		switch {
		case s.installing[p.Name]:
			status.SetText(i18n.T("Installing…"))
			action = widget.NewProgressBarInfinite()
		case where != "":
			status.SetText(i18n.T("Installed: %s", where))
			status.Importance = widget.SuccessImportance
		default:
			status.SetText(i18n.T("Not installed"))
			pack := p
			btn := widget.NewButtonWithIcon(i18n.T("Install"), theme.DownloadIcon(), func() { s.install(pack) })
			btn.Importance = widget.HighImportance
			action = btn
		}
		title := widget.NewLabelWithStyle(p.Tool+" — "+p.Describe(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		title.Wrapping = fyne.TextWrapWord
		objs = append(objs, container.NewBorder(nil, nil, nil, container.NewCenter(action), container.NewVBox(title, status)), widget.NewSeparator())
	}
	var lines []string
	for _, u := range s.u.state.Registry().Unavailable() {
		lines = append(lines, u.ID+": "+u.Reason)
	}
	if len(lines) > 0 {
		objs = append(objs, lowLabel(i18n.T("Not available here:")+"\n"+strings.Join(lines, "\n")))
	}
	s.toolsBox.Objects = objs
	s.toolsBox.Refresh()
}

func (s *settingsWindow) install(p engines.Pack) {
	s.installing[p.Name] = true
	s.refreshTools()
	go func() {
		_, err := engines.InstallSystem(context.Background(), p, func(engines.PackProgress) {})
		fyne.Do(func() {
			delete(s.installing, p.Name)
			if err != nil {
				showError(err, s.win)
			}
			s.u.state.RebuildRegistry()
			s.refreshTools()
		})
	}()
}

func (s *settingsWindow) menus() fyne.CanvasObject {
	s.menuStatus = widget.NewLabel("")
	s.menuStatus.Wrapping = fyne.TextWrapWord
	show := func() {
		if found := integrate.Status(); len(found) > 0 {
			s.menuStatus.SetText(i18n.N(len(found), "Installed (%d entry).", "Installed (%d entries)."))
		} else {
			s.menuStatus.SetText(i18n.T("Not installed."))
		}
	}
	show()
	install := widget.NewButtonWithIcon(i18n.T("Install"), theme.ConfirmIcon(), func() {
		res, err := integrate.Install(s.u.state.Registry(), nil)
		show()
		if err != nil {
			showError(err, s.win)
			return
		}
		if len(res.Notes) > 0 {
			showInfo(i18n.T("Right-click menu"), strings.Join(res.Notes, "\n\n"), s.win)
		}
	})
	install.Importance = widget.HighImportance
	remove := widget.NewButtonWithIcon(i18n.T("Remove"), theme.DeleteIcon(), func() {
		if _, err := integrate.Uninstall(); err != nil {
			showError(err, s.win)
		}
		show()
	})
	return container.NewPadded(container.NewVBox(
		section(i18n.T("“Convert to” in your file manager"),
			lowLabel(i18n.T("Adds a Convert to menu with each file type's popular formats, plus More options… for this window. Explorer on Windows (under Show more options on Windows 11), Finder Quick Actions on macOS, and Nautilus, Dolphin, Nemo or Thunar on Linux. Install again after moving the app.")),
			s.menuStatus,
			container.NewHBox(layout.NewSpacer(), remove, install)),
	))
}
