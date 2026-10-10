package core

import (
	"context"
	"fmt"
	"path/filepath"
)

// Step is a single conversion an engine can perform.
type Step struct {
	From, To *Format
}

// Progress reports a fraction from 0 to 1, or a negative value when the
// engine can't tell how far it is.
type Progress func(fraction float64)

// Task is everything an engine needs for one step besides the input file.
type Task struct {
	// Context is cancelled when the job is stopped.
	Context  context.Context
	Step     Step
	Options  *Options
	progress Progress
}

// NewTask builds a Task. A nil progress ignores reports.
func NewTask(ctx context.Context, step Step, options *Options, progress Progress) *Task {
	if progress == nil {
		progress = func(float64) {}
	}
	return &Task{Context: ctx, Step: step, Options: options, progress: progress}
}

// Progress reports how far the step is, from 0 to 1.
func (t *Task) Progress(fraction float64) { t.progress(min(max(fraction, 0), 1)) }

// Indeterminate reports that the engine can't tell how far it is.
func (t *Task) Indeterminate() { t.progress(-1) }

// Cancelled reports whether the job was stopped.
func (t *Task) Cancelled() bool { return t.Context.Err() != nil }

// Check returns ErrCancelled once the job is stopped. Engines call it
// between units of work.
func (t *Task) Check() error {
	if t.Cancelled() {
		return ErrCancelled
	}
	return nil
}

// Artifact is where to write artifact index (0-based) inside outDir.
func (t *Task) Artifact(outDir string, index int) string {
	return filepath.Join(outDir, fmt.Sprintf("%d.%s", index+1, t.Step.To.Extension()))
}

// Engine converts files with one tool or library.
type Engine interface {
	// ID is a short identifier, e.g. "ffmpeg".
	ID() string
	// Priority decides between two engines that can do the same
	// conversion; higher wins.
	Priority() int
	// Unavailable says why the engine can't run on this machine (missing
	// binary or library), or "" when it can.
	Unavailable() string
	// Steps lists every direct conversion the engine supports.
	Steps() []Step
	// Convert converts input and writes the results into outDir, which is
	// empty and private to this call. It returns every file written, in
	// order (one per page for paged output), named with Task.Artifact.
	Convert(t *Task, input, outDir string) ([]string, error)
}

// Steps lists a step for every from/to pair of distinct format ids.
func Steps(from, to []string) []Step {
	var out []Step
	for _, a := range from {
		for _, b := range to {
			if a != b {
				out = append(out, Step{From: MustID(a), To: MustID(b)})
			}
		}
	}
	return out
}
