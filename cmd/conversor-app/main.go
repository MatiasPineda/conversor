// Command conversor-app is Conversor's desktop app: Activity, Quick
// convert, Settings, automations and the tray icon. One runs per user; a
// second launch hands its files to the running one and exits. It needs cgo
// (Fyne's OpenGL driver); the conversor CLI does not.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"

	"conversor/internal/app"
	"conversor/internal/cli"
	"conversor/internal/engines"
	"conversor/internal/gui"
)

func main() {
	cwd, _ := os.Getwd()
	req, err := app.ParseArgs(os.Args[1:], cwd)
	switch {
	case errors.Is(err, app.ErrHelp):
		fmt.Println(app.Usage())
		return
	case errors.Is(err, app.ErrVersion):
		fmt.Println("conversor-app", cli.Version)
		return
	case err != nil:
		fmt.Fprintln(os.Stderr, "conversor-app:", err)
		fmt.Fprintln(os.Stderr, app.Usage())
		os.Exit(2)
	}

	primary, err := app.Become(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "conversor-app:", err)
		os.Exit(1)
	}
	if primary == nil {
		return // the running app took the request
	}
	defer primary.Close()
	// Closing the terminal that launched the app must not kill it.
	signal.Ignore(syscall.SIGHUP)

	fy := fyneapp.NewWithID("io.github.matiaspineda.conversor")
	state, err := app.NewState(app.DefaultPaths(), engines.Default(), app.Hooks{
		Notify: func(title, body string) { fy.SendNotification(fyne.NewNotification(title, body)) },
		Reveal: func(path string) { app.Reveal(path) },
		Copy: func(path string) {
			if !app.CopyImage(path) {
				fyne.Do(func() { fy.Clipboard().SetContent(path) })
			}
		},
	})
	if err != nil {
		// Unreadable settings or history fall back to defaults.
		fmt.Fprintln(os.Stderr, "conversor-app:", err)
	}
	ui := gui.New(fy, state, cli.Version)
	primary.Listen(ui.HandleAsync)
	ui.Start(req)
	fy.Run()
}
