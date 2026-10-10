package cli

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

//go:embed SKILL.md
var skillTemplate string

// printSkill writes the skill, always in English: it is for AI agents. A
// reader that closes the pipe early, such as `conversor --skill | head`,
// ends the output quietly.
func printSkill(w io.Writer) error {
	prev := i18n.Lang()
	i18n.Set(i18n.English)
	text := renderSkill(NewRoot(io.Discard, io.Discard))
	i18n.Set(prev)
	_, err := io.WriteString(w, text)
	if errors.Is(err, syscall.EPIPE) {
		return nil
	}
	return err
}

func renderSkill(root *cobra.Command) string {
	text := strings.ReplaceAll(skillTemplate, "\r\n", "\n")
	return strings.NewReplacer(
		"{{FLAGS}}", flagsMarkdown(root.Flags()),
		"{{COMMANDS}}", commandsMarkdown(root, "conversor"),
		"{{FORMATS}}", formatsMarkdown(),
	).Replace(text)
}

func flagLine(f *pflag.Flag) string {
	if f.Hidden || f.Name == "help" || f.Name == "version" {
		return ""
	}
	names := "`--" + f.Name + "`"
	if f.Shorthand != "" {
		names += ", `-" + f.Shorthand + "`"
	}
	line := "- " + names
	if f.Value.Type() != "bool" {
		value := strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))
		if v, ok := f.Annotations["value"]; ok {
			value = v[0]
		}
		line += " `<" + value + ">`"
	}
	return line + ": " + strings.ReplaceAll(f.Usage, "`", "")
}

func flagsMarkdown(fs *pflag.FlagSet) string {
	var b strings.Builder
	fs.VisitAll(func(f *pflag.Flag) {
		if l := flagLine(f); l != "" {
			b.WriteString(l + "\n")
		}
	})
	b.WriteString("- `--help`, `-h`: Print help\n- `--version`, `-V`: Print version\n")
	return b.String()
}

func commandsMarkdown(cmd *cobra.Command, prefix string) string {
	var b strings.Builder
	for _, sub := range cmd.Commands() {
		if sub.Hidden || sub.Name() == "help" {
			continue
		}
		usage := prefix + " " + sub.Use
		fmt.Fprintf(&b, "### `%s`\n\n%s\n\n", usage, sub.Short)
		any := false
		sub.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if l := flagLine(f); l != "" {
				b.WriteString(l + "\n")
				any = true
			}
		})
		if any {
			b.WriteString("\n")
		}
		b.WriteString(commandsMarkdown(sub, prefix+" "+sub.Name()))
	}
	return b.String()
}

func formatsMarkdown() string {
	var b strings.Builder
	var last core.Category = -1
	for _, f := range core.Formats {
		if f.Category != last {
			if last >= 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "**%s**\n\n", f.Category.Label())
			last = f.Category
		}
		exts := make([]string, len(f.Extensions))
		for i, e := range f.Extensions {
			exts[i] = "." + e
		}
		fmt.Fprintf(&b, "- `%s` (%s): %s\n", f.ID, f.Name, strings.Join(exts, ", "))
	}
	return b.String()
}
