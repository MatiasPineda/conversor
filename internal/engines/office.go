package engines

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

var (
	documents     = []string{"docx", "doc", "odt", "rtf", "txt", "html"}
	presentations = []string{"pptx", "ppt", "odp"}
	spreadsheets  = []string{"xlsx", "xls", "ods", "csv"}
)

// OfficeEngine converts Office documents with headless LibreOffice, from
// the document pack, a system install, or PATH.
type OfficeEngine struct {
	soffice string
}

func NewOfficeEngine() *OfficeEngine {
	return &OfficeEngine{soffice: findSoffice()}
}

// findSoffice looks for LibreOffice: $CONVERSOR_SOFFICE, the document pack,
// next to the executable, PATH, then where installers put it (which isn't
// on PATH on Windows or macOS).
func findSoffice() string {
	if p := os.Getenv("CONVERSOR_SOFFICE"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p := InstalledDocumentPack(); p != "" {
		return p
	}
	var extra []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if d := os.Getenv(env); d != "" {
				extra = append(extra, filepath.Join(d, "LibreOffice", "program", "soffice.exe"))
				if env == "LOCALAPPDATA" {
					extra = append(extra, filepath.Join(d, "Programs", "LibreOffice", "program", "soffice.exe"))
				}
			}
		}
	case "darwin":
		extra = append(extra, "/Applications/LibreOffice.app/Contents/MacOS/soffice")
		if h, err := os.UserHomeDir(); err == nil {
			extra = append(extra, filepath.Join(h, "Applications", "LibreOffice.app", "Contents", "MacOS", "soffice"))
		}
	default:
		extra = append(extra, "/opt/libreoffice/program/soffice", "/snap/bin/libreoffice", "/var/lib/flatpak/exports/bin/org.libreoffice.LibreOffice")
	}
	return findTool([]string{"soffice", "libreoffice"}, "", extra...)
}

func (*OfficeEngine) ID() string    { return "libreoffice" }
func (*OfficeEngine) Priority() int { return 0 }

func (e *OfficeEngine) Unavailable() string {
	if e.soffice == "" {
		return i18n.T("LibreOffice not found; install it or run `conversor pack install documents`")
	}
	return ""
}

func (*OfficeEngine) Steps() []core.Step {
	withPDF := func(set []string) []string { return append(slices.Clone(set), "pdf") }
	return slices.Concat(
		core.Steps(documents, withPDF(documents)),
		core.Steps(presentations, withPDF(presentations)),
		core.Steps(spreadsheets, withPDF(spreadsheets)),
	)
}

// Detail says which LibreOffice runs.
func (e *OfficeEngine) Detail() string { return e.soffice }

// officeFilter is the --convert-to argument for a step. HTML embeds images
// as data URIs; otherwise they land in companion files a single output
// can't carry. Writer's default lookup misses some export filters.
func officeFilter(step core.Step) string {
	switch step.To.ID {
	case "docx":
		return "docx:Office Open XML Text"
	case "doc":
		return "doc:MS Word 97"
	case "rtf":
		return "rtf:Rich Text Format"
	case "txt":
		return "txt:Text (encoded):UTF8"
	case "html":
		if step.From.Category == core.Spreadsheet {
			return "html:HTML (StarCalc):EmbedImages"
		}
		return "html:HTML (StarWriter):EmbedImages"
	}
	return step.To.ID
}

// fileURL is a file:// URL for a directory, valid with Windows drive
// letters, spaces and non-ASCII names.
func fileURL(dir string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func (e *OfficeEngine) Convert(t *core.Task, input, outDir string) ([]string, error) {
	if e.soffice == "" {
		return nil, core.EngineMissing("libreoffice", i18n.T("not installed"))
	}
	t.Indeterminate()
	// A private profile lets several conversions run at once; an invalid
	// one would make concurrent jobs share the default profile and hand
	// work to another process.
	profile, err := os.MkdirTemp("", "conversor-lo-profile-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(profile)
	tmpOut, err := os.MkdirTemp("", "conversor-lo-out-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpOut)
	abs, err := filepath.Abs(input)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(e.soffice, "-env:UserInstallation="+fileURL(profile),
		"--headless", "--norestore", "--convert-to", officeFilter(t.Step), "--outdir", tmpOut, abs)
	if err := runTool(t, "libreoffice", cmd, nil); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(tmpOut)
	if err != nil {
		return nil, err
	}
	var produced []string
	main := ""
	for _, en := range entries {
		produced = append(produced, en.Name())
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(en.Name()), "."))
		if main == "" && slices.Contains(t.Step.To.Extensions, ext) {
			main = en.Name()
		}
	}
	// LibreOffice exits 0 even when it could not load the file.
	if main == "" {
		return nil, core.EngineFailed("libreoffice", "%s", i18n.T("LibreOffice produced no output; the file may be damaged or protected"))
	}
	if len(produced) > 1 {
		return nil, core.EngineFailed("libreoffice", "%s", i18n.T("LibreOffice split the %s into several files, which can't be saved as one", t.Step.To.Name))
	}
	out := t.Artifact(outDir, 0)
	if err := copyFile(filepath.Join(tmpOut, main), out); err != nil {
		return nil, err
	}
	return []string{out}, nil
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o644)
}
