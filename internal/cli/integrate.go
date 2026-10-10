package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"conversor/internal/engines"
	"conversor/internal/i18n"
	"conversor/internal/integrate"
)

func integrateCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integrate",
		Short: i18n.T("Add or remove \"Convert to\" in your file manager's right-click menu"),
	}
	report := func(res integrate.Result) {
		for _, d := range res.Done {
			fmt.Fprintln(stdout, d)
		}
		for _, n := range res.Notes {
			fmt.Fprintln(stderr, n)
		}
	}
	install := &cobra.Command{
		Use:   "install [" + strings.Join(integrate.FileManagers, "|") + "...]",
		Short: i18n.T("Install the menu for the named file managers, or every one found"),
		RunE: func(_ *cobra.Command, args []string) error {
			res, err := integrate.Install(engines.Default(), args)
			report(res)
			return err
		},
	}
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: i18n.T("Remove every menu this tool installed"),
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			res, err := integrate.Uninstall()
			report(res)
			if err == nil && len(res.Done) == 0 {
				fmt.Fprintln(stdout, i18n.T("nothing to remove"))
			}
			return err
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: i18n.T("Show which menus are installed"),
		Args:  noArgs,
		RunE: func(*cobra.Command, []string) error {
			found := integrate.Status()
			if len(found) == 0 {
				fmt.Fprintln(stdout, i18n.T("no menus installed; run `conversor integrate install`"))
			}
			for _, p := range found {
				fmt.Fprintln(stdout, p)
			}
			return nil
		},
	}
	cmd.AddCommand(install, uninstall, status)
	return cmd
}
