package engines

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"conversor/internal/i18n"
)

// The document pack is LibreOffice as an add-on, for converting Word,
// Excel, PowerPoint and OpenDocument files. Status checks never touch the
// network; only an explicit install downloads anything.

// packDir is where the per-user document pack lives.
func packDir() string {
	if d := DataDir(); d != "" {
		return filepath.Join(d, "packs", "documents")
	}
	return ""
}

type packManifest struct {
	// Soffice is the LibreOffice launcher, relative to the pack folder.
	Soffice string `json:"soffice"`
	Source  string `json:"source"`
	SHA256  string `json:"sha256"`
}

// InstalledDocumentPack is the pack's soffice, or "" when no pack is
// installed.
func InstalledDocumentPack() string {
	dir := packDir()
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "pack.json"))
	if err != nil {
		return ""
	}
	var m packManifest
	if json.Unmarshal(data, &m) != nil || m.Soffice == "" || strings.Contains(m.Soffice, "..") {
		return ""
	}
	p := filepath.Join(dir, filepath.FromSlash(m.Soffice))
	if info, err := os.Stat(p); err != nil || info.IsDir() {
		return ""
	}
	return p
}

// DocumentsStatus says where LibreOffice comes from, or why there is none.
func DocumentsStatus() (string, error) {
	if p := InstalledDocumentPack(); p != "" {
		return i18n.T("document pack at %s", p), nil
	}
	if p := findSoffice(); p != "" {
		return i18n.T("system LibreOffice at %s", p), nil
	}
	return "", errors.New(i18n.T("LibreOffice is not installed"))
}

// PackProgress reports an install's progress.
type PackProgress struct {
	Stage      string // "download", "verify", "extract", "system"
	Bytes      int64
	TotalBytes int64 // 0 when unknown
	Message    string
}

// InstallDocumentsFrom downloads a LibreOffice archive (.zip or .tar.gz)
// from source, checks its SHA-256 and unpacks it as the document pack. A
// partial download resumes where it stopped.
func InstallDocumentsFrom(ctx context.Context, source, wantSHA string, progress func(PackProgress)) (string, error) {
	dir := packDir()
	if dir == "" {
		return "", errors.New(i18n.T("no data directory on this system"))
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	unlock, err := lockPack(filepath.Dir(dir))
	if err != nil {
		return "", err
	}
	defer unlock()

	partial := dir + ".download"
	if err := download(ctx, source, partial, progress); err != nil {
		return "", err
	}
	progress(PackProgress{Stage: "verify"})
	got, err := fileSHA256(partial)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(got, strings.TrimSpace(wantSHA)) {
		os.Remove(partial)
		return "", errors.New(i18n.T("checksum mismatch: the download has SHA-256 %s, expected %s", got, wantSHA))
	}
	progress(PackProgress{Stage: "extract"})
	staging := dir + ".new"
	os.RemoveAll(staging)
	if err := extract(partial, staging, source); err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	soffice, err := findInTree(staging)
	if err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	rel, _ := filepath.Rel(staging, soffice)
	manifest, _ := json.MarshalIndent(packManifest{Soffice: filepath.ToSlash(rel), Source: source, SHA256: got}, "", "  ")
	if err := os.WriteFile(filepath.Join(staging, "pack.json"), manifest, 0o644); err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	os.RemoveAll(dir)
	if err := os.Rename(staging, dir); err != nil {
		return "", err
	}
	os.Remove(partial)
	return filepath.Join(dir, rel), nil
}

// Pack is an optional engine dependency Conversor can install with the
// platform's package manager.
type Pack struct {
	// Purpose is what the pack adds, in English; Describe translates it.
	Name, Tool, Purpose string
	// Winget and Brew are package ids; "" means not available there.
	Winget, Brew string
	// Linux maps a package manager to the command that installs the pack.
	Linux [][2]string
	// Find locates the installed tool, or returns "".
	Find func() string
	// Manual is where to get it when no package manager can.
	Manual string
}

// Packs are the optional dependencies, in the order status lists them.
var Packs = []Pack{
	{
		Name: "documents", Tool: "LibreOffice", Purpose: "Word, Excel, PowerPoint and OpenDocument files",
		Winget: "TheDocumentFoundation.LibreOffice", Brew: "--cask libreoffice",
		Linux: [][2]string{
			{"apt-get", "sudo apt-get install -y libreoffice-core libreoffice-writer libreoffice-calc libreoffice-impress"},
			{"dnf", "sudo dnf install -y libreoffice-core libreoffice-writer libreoffice-calc libreoffice-impress"},
			{"pacman", "sudo pacman -S --needed libreoffice-fresh"},
			{"zypper", "sudo zypper install -y libreoffice"},
		},
		Find:   findSoffice,
		Manual: "https://www.libreoffice.org/download/",
	},
	{
		Name: "media", Tool: "FFmpeg", Purpose: "video, audio, and frames from video",
		Winget: "Gyan.FFmpeg", Brew: "ffmpeg",
		Linux: [][2]string{
			{"apt-get", "sudo apt-get install -y ffmpeg"},
			{"dnf", "sudo dnf install -y ffmpeg-free"},
			{"pacman", "sudo pacman -S --needed ffmpeg"},
			{"zypper", "sudo zypper install -y ffmpeg"},
		},
		Find:   FFmpegPath,
		Manual: "https://ffmpeg.org/download.html",
	},
	{
		Name: "heic", Tool: "heif-enc (libheif)", Purpose: "HEIC output (HEIC input works without it)",
		Brew: "libheif",
		Linux: [][2]string{
			{"apt-get", "sudo apt-get install -y libheif-examples"},
			{"dnf", "sudo dnf install -y libheif-tools"},
			{"pacman", "sudo pacman -S --needed libheif"},
			{"zypper", "sudo zypper install -y libheif1 heif-examples"},
		},
		Find: func() string {
			if runtime.GOOS == "darwin" {
				return "/usr/bin/sips"
			}
			return findTool([]string{"heif-enc"}, "CONVERSOR_HEIF_ENC")
		},
		Manual: "MSYS2 (pacman -S mingw-w64-ucrt-x86_64-libheif) provides heif-enc.exe on Windows",
	},
}

// PackByName finds a pack.
func PackByName(name string) (Pack, bool) {
	for _, p := range Packs {
		if p.Name == name {
			return p, true
		}
	}
	return Pack{}, false
}

// Describe is what the pack adds, in the user's language.
func (p Pack) Describe() string {
	switch p.Name {
	case "documents":
		return i18n.T("Word, Excel, PowerPoint and OpenDocument files")
	case "media":
		return i18n.T("video, audio, and frames from video")
	case "heic":
		return i18n.T("HEIC output (HEIC input works without it)")
	}
	return p.Purpose
}

func (p Pack) manual() string {
	if p.Name == "heic" {
		return i18n.T("MSYS2 (pacman -S mingw-w64-ucrt-x86_64-libheif) provides heif-enc.exe on Windows")
	}
	return p.Manual
}

// InstallSystem installs a pack with the platform's package manager where
// one can run without a terminal prompt: winget on Windows, Homebrew on
// macOS. On Linux it returns the command to run, since it needs root.
func InstallSystem(ctx context.Context, p Pack, progress func(PackProgress)) (string, error) {
	if found := p.Find(); found != "" {
		return found, nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		winget, err := exec.LookPath("winget")
		if p.Winget == "" || err != nil {
			return "", errors.New(i18n.T("install %s yourself: %s", p.Tool, p.manual()))
		}
		cmd = exec.CommandContext(ctx, winget, "install", "--id", p.Winget, "--exact",
			"--accept-package-agreements", "--accept-source-agreements")
	case "darwin":
		brew := findTool([]string{"brew"}, "")
		if p.Brew == "" || brew == "" {
			return "", errors.New(i18n.T("Homebrew (https://brew.sh) is not installed; install it, or get %s from %s", p.Tool, p.manual()))
		}
		cmd = exec.CommandContext(ctx, brew, append([]string{"install"}, strings.Fields(p.Brew)...)...)
	default:
		for _, pm := range p.Linux {
			if _, err := exec.LookPath(pm[0]); err == nil {
				return "", errors.New(i18n.T("installing %s needs root; run this, then try again:\n  %s", p.Tool, pm[1]))
			}
		}
		return "", errors.New(i18n.T("install %s with your package manager (%s), then try again", p.Tool, p.manual()))
	}
	progress(PackProgress{Stage: "system", Message: strings.Join(cmd.Args, " ")})
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("%s failed", filepath.Base(cmd.Path)), err)
	}
	if found := p.Find(); found != "" {
		return found, nil
	}
	return "", errors.New(i18n.T("the installer finished, but %s was not found; open a new terminal and try again", p.Tool))
}

// RemoveDocumentPack removes the per-user pack. A system LibreOffice is
// left alone.
func RemoveDocumentPack() error {
	dir := packDir()
	if dir == "" {
		return nil
	}
	unlock, err := lockPack(filepath.Dir(dir))
	if err != nil {
		return err
	}
	defer unlock()
	for _, p := range []string{dir, dir + ".new", dir + ".download"} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

// lockPack keeps two installs or removals from running at once. A lock
// older than an hour is from a crashed run and is taken over.
func lockPack(dir string) (func(), error) {
	path := filepath.Join(dir, "documents.lock")
	for range 2 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.Remove(path)
			continue
		}
		return nil, errors.New(i18n.T("another conversor is installing or removing the document pack; try again when it's done"))
	}
	return nil, errors.New(i18n.T("could not lock the document pack folder"))
}

func download(ctx context.Context, source, dest string, progress func(PackProgress)) error {
	if !strings.HasPrefix(source, "https://") && !strings.HasPrefix(source, "http://") {
		// A local file.
		return copyFile(source, dest)
	}
	var have int64
	if info, err := os.Stat(dest); err == nil {
		have = info.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("download failed"), err)
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		have = 0
		flags |= os.O_TRUNC
	case http.StatusRequestedRangeNotSatisfiable:
		return nil // already complete
	default:
		return fmt.Errorf("%s: HTTP %s", i18n.T("download failed"), resp.Status)
	}
	f, err := os.OpenFile(dest, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	total := int64(0)
	if resp.ContentLength > 0 {
		total = have + resp.ContentLength
	}
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			have += int64(n)
			progress(PackProgress{Stage: "download", Bytes: have, TotalBytes: total})
		}
		if rerr == io.EOF {
			return f.Close()
		}
		if rerr != nil {
			return fmt.Errorf("%s: %w; %s", i18n.T("download interrupted"), rerr, i18n.T("run the install again to resume"))
		}
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extract unpacks a zip or gzipped tar into dir, refusing entries that
// would land outside it.
func extract(archive, dir, name string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	safe := func(entry string) (string, error) {
		p := filepath.Join(dir, filepath.FromSlash(entry))
		if rel, err := filepath.Rel(dir, p); err != nil || strings.HasPrefix(rel, "..") {
			return "", errors.New(i18n.T("archive entry %q points outside the pack", entry))
		}
		return p, nil
	}
	if zr, err := zip.OpenReader(archive); err == nil {
		defer zr.Close()
		for _, f := range zr.File {
			p, err := safe(f.Name)
			if err != nil {
				return err
			}
			if f.FileInfo().IsDir() {
				os.MkdirAll(p, 0o755)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = writeFile(p, rc, f.Mode())
			rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return errors.New(i18n.T("%s is neither a zip nor a .tar.gz archive", filepath.Base(name)))
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := safe(h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(p, 0o755)
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := writeFile(p, tr, h.FileInfo().Mode()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) || strings.Contains(h.Linkname, "..") {
				continue
			}
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.Symlink(h.Linkname, p)
		}
	}
}

func writeFile(path string, r io.Reader, mode fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// findInTree finds LibreOffice's launcher in an unpacked archive.
func findInTree(root string) (string, error) {
	want := map[string]bool{"soffice": true, "soffice.exe": true, "soffice.com": false}
	found := ""
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil
		}
		if want[strings.ToLower(d.Name())] {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", errors.New(i18n.T("the archive has no LibreOffice (soffice) in it"))
	}
	return found, nil
}
