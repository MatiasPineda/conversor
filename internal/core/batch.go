package core

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// BatchItem is a file found by ExpandInputs.
type BatchItem struct {
	Input string
	// Explicit is set for files named directly rather than found in a
	// folder. Callers report errors for explicit files and quietly skip
	// folder files that can't convert.
	Explicit bool
	// Root is the folder argument this file was found under, so callers can
	// mirror subfolders in an output directory. Empty for explicit files.
	Root string
}

// RelativeDir is the file's folder relative to the folder it was found
// under: empty for explicit files and files at the top of a folder.
func (b BatchItem) RelativeDir() string {
	if b.Root == "" {
		return ""
	}
	rel, err := filepath.Rel(b.Root, filepath.Dir(b.Input))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return rel
}

// ExpandInputs expands folders into the files they contain, sorted by
// path. Hidden files and folders are skipped; subfolders only with
// recursive. Symlinked folders are not followed, so a link loop can't
// recurse forever.
func ExpandInputs(paths []string, recursive bool) ([]BatchItem, error) {
	var out []BatchItem
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			out = append(out, BatchItem{Input: path, Explicit: true})
			continue
		}
		var found []string
		if err := walk(path, recursive, &found); err != nil {
			return nil, err
		}
		sort.Strings(found)
		for _, f := range found {
			out = append(out, BatchItem{Input: f, Root: path})
		}
	}
	return out, nil
}

func walk(dir string, recursive bool, out *[]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if recursive {
				if err := walk(path, recursive, out); err != nil {
					return err
				}
			}
			continue
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			*out = append(*out, path)
		}
	}
	return nil
}

// EventKind is what happened to a job in a batch.
type EventKind int

const (
	Started EventKind = iota
	Progressed
	Finished
)

// Event is the progress of one job in a batch.
type Event struct {
	Kind EventKind
	// Fraction is set for Progressed: 0 to 1, or negative when unknown.
	Fraction float64
	// Outputs and Err are set for Finished.
	Outputs []string
	Err     error
}

// Result is how one job in a batch ended.
type Result struct {
	Outputs []string
	Err     error
}

// IsVideoJob reports whether a job reads or writes video. Video jobs are
// already multithreaded inside the encoder; running several at once mostly
// thrashes memory.
func IsVideoJob(job *Job) bool {
	if job.To.Category == Video {
		return true
	}
	f := ByPath(job.Input)
	return f != nil && f.Category == Video
}

// RunBatch runs jobs with at most concurrency at a time, and at most one
// video job at a time. Results come back in job order. After ctx is
// cancelled, jobs that haven't started return ErrCancelled. onEvent may be
// called from several goroutines at once.
func RunBatch(ctx context.Context, r *Registry, jobs []Job, concurrency int, onEvent func(i int, e Event)) []Result {
	results := make([]Result, len(jobs))
	var next atomic.Int64
	var videoLane sync.Mutex
	workers := min(max(concurrency, 1), max(len(jobs), 1))
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				i := int(next.Add(1) - 1)
				if i >= len(jobs) {
					return
				}
				job := &jobs[i]
				video := IsVideoJob(job)
				if video {
					videoLane.Lock()
				}
				var res Result
				if ctx.Err() != nil {
					res.Err = ErrCancelled
				} else {
					onEvent(i, Event{Kind: Started})
					res.Outputs, res.Err = r.Run(ctx, job, func(p float64) {
						onEvent(i, Event{Kind: Progressed, Fraction: p})
					})
				}
				if video {
					videoLane.Unlock()
				}
				onEvent(i, Event{Kind: Finished, Outputs: res.Outputs, Err: res.Err})
				results[i] = res
			}
		})
	}
	wg.Wait()
	return results
}
