package gui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"conversor/internal/i18n"
)

// Fyne's own dialogs label their buttons in the system language, which
// may be neither Spanish nor English; these use the app's.

func showError(err error, parent fyne.Window) {
	d := dialog.NewError(err, parent)
	d.SetDismissText(i18n.T("OK"))
	d.Show()
}

func showInfo(title, message string, parent fyne.Window) {
	d := dialog.NewInformation(title, message, parent)
	d.SetDismissText(i18n.T("OK"))
	d.Show()
}

// confirm asks before doing something; action names the confirm button.
func confirm(title, message, action string, then func(), parent fyne.Window) {
	d := dialog.NewConfirm(title, message, func(ok bool) {
		if ok {
			then()
		}
	}, parent)
	d.SetConfirmText(action)
	d.SetDismissText(i18n.T("Cancel"))
	d.Show()
}
