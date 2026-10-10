// Command conversor converts images, video, audio, PDFs and documents on
// this computer. See docs/usage.md.
package main

import (
	"os"

	"conversor/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
