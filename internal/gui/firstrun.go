package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"conversor/internal/app"
	"conversor/internal/engines"
	"conversor/internal/i18n"
	"conversor/internal/integrate"
)

// showFirstRun is the welcome window: what the app does, the right-click
// menu, and the optional tools. Closing it early shows it again next time.
func (u *UI) showFirstRun() {
	w := u.fy.NewWindow(i18n.T("Welcome to Conversor"))
	u.firstRun = w
	w.Resize(fyne.NewSize(560, 460))

	menuStatus := widget.NewLabel("")
	refreshMenu := func() {
		if len(integrate.Status()) > 0 {
			menuStatus.SetText(i18n.T("Installed."))
		} else {
			menuStatus.SetText("")
		}
	}
	refreshMenu()
	menuBtn := widget.NewButtonWithIcon(i18n.T("Add to right-click menu"), theme.ConfirmIcon(), func() {
		if _, err := integrate.Install(u.state.Registry(), nil); err != nil {
			showError(err, w)
		}
		refreshMenu()
	})

	var missing []string
	for _, p := range engines.Packs {
		if p.Find() == "" {
			missing = append(missing, i18n.T("%s for %s", p.Tool, p.Describe()))
		}
	}
	tools := lowLabel(i18n.T("Everything is ready: images, PDFs, SVG, video, audio and documents."))
	toolsBtn := widget.NewButtonWithIcon(i18n.T("Open Tools"), theme.StorageIcon(), func() { u.showSettings("tools") })
	if len(missing) > 0 {
		text := i18n.T("Images, PDFs and SVG work now. Optional free tools add more:") + "\n"
		for _, m := range missing {
			text += "• " + m + "\n"
		}
		tools.SetText(text)
	} else {
		toolsBtn.Hide()
	}

	start := widget.NewButtonWithIcon(i18n.T("Start converting"), theme.NavigateNextIcon(), func() {
		u.state.UpdateSettings(func(s *app.Settings) { s.FirstRunDone = true })
		u.firstRun = nil
		w.Close()
		u.main.show()
	})
	start.Importance = widget.HighImportance
	w.SetOnClosed(func() {
		if u.firstRun == w {
			u.firstRun = nil
			u.maybeQuit()
		}
	})

	w.SetContent(container.NewPadded(container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), start), nil, nil,
		container.NewVBox(
			widget.NewLabelWithStyle(i18n.T("Convert files on this computer"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			lowLabel(i18n.T("Drop files on the window, right-click them in your file manager, or set an automation to convert screenshots and downloads as they arrive. Nothing is uploaded; results land next to the originals and never replace a file.")),
			widget.NewSeparator(),
			section(i18n.T("Right-click menu"), lowLabel(i18n.T("Adds “Convert to” for every file type this app reads.")), container.NewHBox(menuBtn, menuStatus)),
			widget.NewSeparator(),
			section(i18n.T("Optional tools"), tools, container.NewHBox(toolsBtn)),
		))))
	w.Show()
}
