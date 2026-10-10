//go:build darwin

package integrate

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"conversor/internal/core"
	"conversor/internal/i18n"
)

// FileManagers lists the file managers Install can set up.
var FileManagers = []string{"finder"}

const suffix = " (conversor).workflow"

func servicesDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "Services")
}

// utis are the Uniform Type Identifiers a category's files conform to.
func utis(c core.Category) []string {
	switch c {
	case core.Image:
		return []string{"public.image"}
	case core.Vector:
		return []string{"public.svg-image"}
	case core.Video:
		return []string{"public.movie"}
	case core.Audio:
		return []string{"public.audio"}
	case core.PDF:
		return []string{"com.adobe.pdf"}
	case core.Document:
		return []string{"public.text", "org.openxmlformats.wordprocessingml.document", "com.microsoft.word.doc", "org.oasis-open.opendocument.text", "public.rtf", "public.html"}
	case core.Presentation:
		return []string{"org.openxmlformats.presentationml.presentation", "com.microsoft.powerpoint.ppt", "org.oasis-open.opendocument.presentation"}
	case core.Spreadsheet:
		return []string{"org.openxmlformats.spreadsheetml.sheet", "com.microsoft.excel.xls", "org.oasis-open.opendocument.spreadsheet", "public.comma-separated-values-text"}
	}
	return nil
}

func plistString(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Install writes one Finder Quick Action per target ("Convert to JPEG"),
// shown for the kinds of files that offer it. They appear under Quick
// Actions in Finder's right-click menu; System Settings > Extensions >
// Finder can hide the ones you don't use.
func Install(r *core.Registry, only []string) (Result, error) {
	var res Result
	for _, fm := range only {
		if fm != "finder" {
			return res, errors.New(i18n.T("unknown file manager %q; use %s", fm, "finder"))
		}
	}
	l, err := FindLauncher()
	if err != nil {
		return res, err
	}
	if _, err := Uninstall(); err != nil {
		return res, err
	}
	targets, sources := ByTarget(Menu(r))
	type action struct {
		name  string
		args  []string
		types []string
	}
	var actions []action
	allTypes := map[string]bool{}
	for _, t := range targets {
		seen := map[string]bool{}
		var types []string
		for _, f := range sources[t.ID] {
			for _, u := range utis(f.Category) {
				if !seen[u] {
					seen[u] = true
					types = append(types, u)
				}
			}
		}
		for _, u := range types {
			allTypes[u] = true
		}
		actions = append(actions, action{i18n.T("Convert to %s", t.Name), l.Convert(t.ID), types})
	}
	if more := l.More(); more != nil {
		var types []string
		for u := range allTypes {
			types = append(types, u)
		}
		actions = append(actions, action{i18n.T("Convert with Conversor…"), more, types})
	}
	for _, a := range actions {
		name, types := a.name, a.types
		dir := filepath.Join(servicesDir(), name+suffix, "Contents")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return res, err
		}
		var sendTypes strings.Builder
		for _, u := range types {
			sendTypes.WriteString("<string>" + u + "</string>")
		}
		info := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>NSServices</key><array><dict>
<key>NSMenuItem</key><dict><key>default</key><string>` + plistString(name) + `</string></dict>
<key>NSMessage</key><string>runWorkflowAsService</string>
<key>NSRequiredContext</key><dict><key>NSApplicationIdentifier</key><string>com.apple.finder</string></dict>
<key>NSSendFileTypes</key><array>` + sendTypes.String() + `</array>
</dict></array></dict></plist>
`
		script := fmt.Sprintf(`%q %s "$@"`, l.Exe, strings.Join(a.args, " "))
		wflow := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>AMApplicationBuild</key><string>523</string>
<key>AMApplicationVersion</key><string>2.10</string>
<key>AMDocumentVersion</key><string>2</string>
<key>actions</key><array><dict><key>action</key><dict>
<key>AMAccepts</key><dict><key>Container</key><string>List</string><key>Optional</key><true/><key>Types</key><array><string>com.apple.cocoa.string</string></array></dict>
<key>AMActionVersion</key><string>2.0.3</string>
<key>AMApplication</key><array><string>Automator</string></array>
<key>AMParameterProperties</key><dict><key>COMMAND_STRING</key><dict/><key>CheckedForUserDefaultShell</key><dict/><key>inputMethod</key><dict/><key>shell</key><dict/><key>source</key><dict/></dict>
<key>AMProvides</key><dict><key>Container</key><string>List</string><key>Types</key><array><string>com.apple.cocoa.string</string></array></dict>
<key>ActionBundlePath</key><string>/System/Library/Automator/Run Shell Script.action</string>
<key>ActionName</key><string>Run Shell Script</string>
<key>ActionParameters</key><dict>
<key>COMMAND_STRING</key><string>` + plistString(script) + `</string>
<key>CheckedForUserDefaultShell</key><true/>
<key>inputMethod</key><integer>1</integer>
<key>shell</key><string>/bin/bash</string>
<key>source</key><string></string>
</dict>
<key>BundleIdentifier</key><string>com.apple.RunShellScript</string>
<key>CFBundleVersion</key><string>2.0.3</string>
<key>CanShowSelectedItemsWhenRun</key><false/>
<key>CanShowWhenRun</key><true/>
<key>Category</key><array><string>AMCategoryUtilities</string></array>
<key>Class Name</key><string>RunShellScriptAction</string>
<key>InputUUID</key><string>` + uuid() + `</string>
<key>Keywords</key><array><string>Shell</string><string>Script</string></array>
<key>OutputUUID</key><string>` + uuid() + `</string>
<key>UUID</key><string>` + uuid() + `</string>
<key>UnlocalizedApplications</key><array><string>Automator</string></array>
<key>isViewVisible</key><integer>1</integer>
</dict><key>isViewVisible</key><integer>1</integer></dict></array>
<key>connectors</key><dict/>
<key>workflowMetaData</key><dict>
<key>inputTypeIdentifier</key><string>com.apple.Automator.fileSystemObject</string>
<key>outputTypeIdentifier</key><string>com.apple.Automator.nothing</string>
<key>presentationMode</key><integer>15</integer>
<key>processesInput</key><false/>
<key>serviceInputTypeIdentifier</key><string>com.apple.Automator.fileSystemObject</string>
<key>serviceOutputTypeIdentifier</key><string>com.apple.Automator.nothing</string>
<key>serviceProcessesInput</key><false/>
<key>systemImageName</key><string>NSActionTemplate</string>
<key>useAutomaticInputType</key><false/>
<key>workflowTypeIdentifier</key><string>com.apple.Automator.servicesMenu</string>
</dict></dict></plist>
`
		if err := os.WriteFile(filepath.Join(dir, "Info.plist"), []byte(info), 0o644); err != nil {
			return res, err
		}
		if err := os.WriteFile(filepath.Join(dir, "document.wflow"), []byte(wflow), 0o644); err != nil {
			return res, err
		}
		res.Done = append(res.Done, i18n.T("wrote %s", filepath.Dir(dir)))
	}
	// Ask macOS to pick up the new services.
	exec.Command("/System/Library/CoreServices/pbs", "-flush").Run()
	res.Notes = append(res.Notes, i18n.T("Find them under Quick Actions when you right-click a file. Rerun this after moving or updating conversor."))
	return res, nil
}

// Uninstall removes the Quick Actions this tool wrote.
func Uninstall() (Result, error) {
	var res Result
	entries, _ := os.ReadDir(servicesDir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			p := filepath.Join(servicesDir(), e.Name())
			if err := os.RemoveAll(p); err != nil {
				return res, err
			}
			res.Done = append(res.Done, i18n.T("removed %s", p))
		}
	}
	return res, nil
}

// Status lists the installed Quick Actions.
func Status() []string {
	var out []string
	entries, _ := os.ReadDir(servicesDir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			out = append(out, filepath.Join(servicesDir(), e.Name()))
		}
	}
	return out
}
