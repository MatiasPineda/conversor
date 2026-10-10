package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Publishing moves finished artifacts from a job's staging directory to
// their final names. Each name is reserved with an exclusive create before
// the move, so a conversion never replaces an existing file, including its
// own input or the output of another job that picked the same name.

// maxSuffix is how many " (n)" suffixes to try before giving up.
const maxSuffix = 9999

// outputName is "photo.png", "photo-2.png" for page 2 and "photo (1).png"
// on collision. An empty ext gives a name without one.
func outputName(stem, ext string, page, dup int) string {
	name := stem
	if page > 0 {
		name += fmt.Sprintf("-%d", page+1)
	}
	if dup > 0 {
		name += fmt.Sprintf(" (%d)", dup)
	}
	if ext != "" {
		name += "." + ext
	}
	return name
}

// reserveAll reserves every path, or none of them.
func reserveAll(paths []string) (bool, error) {
	var reserved []string
	for _, p := range paths {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			for _, r := range reserved {
				os.Remove(r)
			}
			if errors.Is(err, fs.ErrExist) {
				return false, nil
			}
			return false, err
		}
		f.Close()
		reserved = append(reserved, p)
	}
	return true, nil
}

// publish moves artifacts into dir as stem.ext, stem-2.ext, ..., where
// pages holds each artifact's 0-based page number. With exact, a taken name
// is an error instead of getting a suffix.
func publish(artifacts []string, pages []int, dir, stem, ext string, exact bool) ([]string, error) {
	names := func(dup int) []string {
		out := make([]string, len(pages))
		for i, page := range pages {
			out[i] = filepath.Join(dir, outputName(stem, ext, page, dup))
		}
		return out
	}
	var targets []string
	for dup := 0; dup <= maxSuffix; dup++ {
		candidate := names(dup)
		ok, err := reserveAll(candidate)
		if err != nil {
			return nil, err
		}
		if ok {
			targets = candidate
			break
		}
		if exact {
			for _, p := range candidate {
				if _, err := os.Lstat(p); err == nil {
					return nil, OutputExists(p)
				}
			}
			return nil, OutputExists(filepath.Join(dir, stem))
		}
	}
	if targets == nil {
		return nil, OutputExists(filepath.Join(dir, outputName(stem, ext, 0, 0)))
	}
	for i, from := range artifacts {
		// The staging directory sits inside dir, so this is a same-volume
		// rename that replaces the empty placeholder.
		if err := os.Rename(from, targets[i]); err != nil {
			for _, t := range targets {
				os.Remove(t)
			}
			return nil, err
		}
	}
	return targets, nil
}
