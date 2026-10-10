package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"conversor/internal/i18n"
)

// localize translates the parts of the help and errors that cobra and
// pflag write themselves: the usage headings, the help command and flag,
// and flag parsing errors.
func localize(root *cobra.Command) {
	root.SetUsageTemplate(usageTemplate())
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return flagError(err) })
	root.InitDefaultHelpCmd()
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Name() == "help" {
			c.Short = i18n.T("Help about any command")
			c.Long = i18n.T("Help provides help for any command in the application.")
		}
		// The template adds a translated "[flags]" instead.
		c.DisableFlagsInUseLine = true
		c.InitDefaultHelpFlag()
		c.Flags().Lookup("help").Usage = i18n.T("Help for %s", c.Name())
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// usageTemplate is cobra's default template, without aliases and command
// groups (unused here), with translated headings.
func usageTemplate() string {
	return i18n.T("Usage:") + `{{if .Runnable}}
  {{.UseLine}}{{if .HasAvailableFlags}} ` + i18n.T("[flags]") + `{{end}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} ` + i18n.T("[command]") + `{{end}}{{if .HasExample}}

` + i18n.T("Examples:") + `
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

` + i18n.T("Available Commands:") + `{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

` + i18n.T("Flags:") + `
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

` + i18n.T("Global Flags:") + `
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

` + i18n.T(`Use "%s [command] --help" for more information about a command.`, "{{.CommandPath}}") + `{{end}}
`
}

// flagError translates pflag's parsing errors.
func flagError(err error) error {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "unknown flag: "); ok {
		return errors.New(i18n.T("unknown flag: %s", rest))
	}
	if rest, ok := strings.CutPrefix(msg, "unknown shorthand flag: "); ok {
		return errors.New(i18n.T("unknown shorthand flag: %s", rest))
	}
	if rest, ok := strings.CutPrefix(msg, "flag needs an argument: "); ok {
		return errors.New(i18n.T("flag needs an argument: %s", rest))
	}
	if rest, ok := strings.CutPrefix(msg, "invalid argument "); ok {
		return errors.New(i18n.T("invalid argument %s", rest))
	}
	return err
}

// noArgs, exactArgs and maximumArgs are cobra's argument checks with
// translated errors.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return errors.New(i18n.T("%s takes no arguments, got %q", cmd.CommandPath(), args[0]))
	}
	return nil
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return errors.New(i18n.T("%s needs %d argument(s), got %d", cmd.CommandPath(), n, len(args)))
		}
		return nil
	}
}

func maximumArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return errors.New(i18n.T("%s takes at most %d argument(s), got %d", cmd.CommandPath(), n, len(args)))
		}
		return nil
	}
}
