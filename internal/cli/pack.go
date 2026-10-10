package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"conversor/internal/engines"
	"conversor/internal/i18n"
)

func packCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack",
		Short: i18n.T("Install or inspect optional tools: documents (LibreOffice), media (FFmpeg), heic (libheif); network only on install"),
	}
	var names []string
	for _, p := range engines.Packs {
		names = append(names, p.Name)
	}
	packArg := func(args []string) (engines.Pack, error) {
		p, ok := engines.PackByName(args[0])
		if !ok {
			return p, errors.New(i18n.T("unknown pack %q; use %s", args[0], strings.Join(names, ", ")))
		}
		return p, nil
	}
	status := &cobra.Command{
		Use:   "status [" + strings.Join(names, "|") + "]",
		Short: i18n.T("Show which optional tools are installed, without contacting a server"),
		Args:  maximumArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			list := engines.Packs
			if len(args) == 1 {
				p, err := packArg(args)
				if err != nil {
					return err
				}
				list = []engines.Pack{p}
			}
			for _, p := range list {
				where := p.Find()
				if p.Name == "documents" {
					if w, err := engines.DocumentsStatus(); err == nil {
						where = w
					}
				}
				if where == "" {
					fmt.Fprintln(stdout, i18n.T("%s: not installed; %s needs %s. Run `conversor pack install %s`", p.Name, p.Describe(), p.Tool, p.Name))
				} else {
					fmt.Fprintln(stdout, i18n.T("%s: installed (%s)", p.Name, where))
				}
			}
			return nil
		},
	}
	var source, sha string
	install := &cobra.Command{
		Use:   "install <" + strings.Join(names, "|") + ">",
		Short: i18n.T("Install with winget (Windows) or Homebrew (macOS), or print the Linux command; documents also takes --source with --sha256"),
		Args:  exactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := packArg(args)
			if err != nil {
				return err
			}
			if (source == "") != (sha == "") {
				return errors.New(i18n.T("--source and --sha256 go together"))
			}
			if source != "" && p.Name != "documents" {
				return errors.New(i18n.T("--source works only for the documents pack"))
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			var lastMB int64 = -1
			progress := func(pp engines.PackProgress) {
				switch pp.Stage {
				case "download":
					if mb := pp.Bytes / (8 << 20); mb != lastMB {
						lastMB = mb
						if pp.TotalBytes > 0 {
							fmt.Fprintln(stderr, i18n.T("Downloaded %d of %d MB", pp.Bytes>>20, pp.TotalBytes>>20))
						} else {
							fmt.Fprintln(stderr, i18n.T("Downloaded %d MB", pp.Bytes>>20))
						}
					}
				case "verify":
					fmt.Fprintln(stderr, i18n.T("Verifying SHA-256…"))
				case "extract":
					fmt.Fprintln(stderr, i18n.T("Installing document pack…"))
				case "system":
					fmt.Fprintln(stderr, i18n.T("Running %s", pp.Message))
				}
			}
			var path string
			if source != "" {
				path, err = engines.InstallDocumentsFrom(ctx, source, sha, progress)
			} else {
				path, err = engines.InstallSystem(ctx, p, progress)
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, i18n.T("%s: installed (%s)", p.Name, path))
			return nil
		},
	}
	install.Flags().StringVar(&source, "source", "", i18n.T("URL or path of a LibreOffice .zip or .tar.gz for the documents pack; requires --sha256"))
	install.Flags().StringVar(&sha, "sha256", "", i18n.T("SHA-256 of that archive; required with --source"))
	remove := &cobra.Command{
		Use:   "remove documents",
		Short: i18n.T("Remove a document pack installed with --source; system installs are left alone"),
		Args:  exactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if args[0] != "documents" {
				return errors.New(i18n.T("only a documents pack installed with --source can be removed here; uninstall %s with the tool that installed it", args[0]))
			}
			if err := engines.RemoveDocumentPack(); err != nil {
				return err
			}
			fmt.Fprintln(stdout, i18n.T("documents: removed"))
			return nil
		},
	}
	cmd.AddCommand(status, install, remove)
	return cmd
}
