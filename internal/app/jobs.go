package app

import (
	"context"
	"slices"
	"sync"
	"time"

	"conversor/internal/core"
)

// JobID names a job for this session.
type JobID int64

// Status is where a job is.
type Status int

const (
	Queued Status = iota
	Running
	Finished
)

// Entry is one job as the UI shows it.
type Entry struct {
	ID    JobID
	Input string
	To    *core.Format
	Setup Setup
	// Status and, once Finished, Outcome.
	Status  Status
	Outcome Outcome
	// Progress is 0 to 1, or negative when the engine can't tell.
	Progress float64
	Outputs  []string
	Err      string
	Started  time.Time
	// CopyResult puts the result on the clipboard (automations).
	CopyResult bool
}

// Remaining estimates the time left from the progress so far, once there
// is enough progress to say.
func (e Entry) Remaining(now time.Time) (time.Duration, bool) {
	if e.Status != Running || e.Progress < 0.05 || e.Progress >= 1 || e.Started.IsZero() {
		return 0, false
	}
	elapsed := now.Sub(e.Started)
	return time.Duration(float64(elapsed) * (1 - e.Progress) / e.Progress), true
}

// Jobs runs conversions in the background with the same limits as the
// CLI: a concurrency cap and one video at a time. It reports every change
// through onChange and every finished job through onFinish, both called
// without the lock held, from any goroutine.
type Jobs struct {
	mu       sync.Mutex
	registry *core.Registry
	limit    int
	entries  []*Entry
	jobs     map[JobID]core.Job
	pending  []JobID
	running  int
	video    bool
	cancels  map[JobID]context.CancelFunc
	nextID   JobID
	onChange func()
	onFinish func(Entry)
	// lastNotify throttles progress updates.
	lastNotify time.Time
}

// NewJobs starts an empty queue.
func NewJobs(r *core.Registry, limit int, onChange func(), onFinish func(Entry)) *Jobs {
	if onChange == nil {
		onChange = func() {}
	}
	if onFinish == nil {
		onFinish = func(Entry) {}
	}
	return &Jobs{registry: r, limit: max(limit, 1), jobs: map[JobID]core.Job{}, cancels: map[JobID]context.CancelFunc{},
		nextID: 1, onChange: onChange, onFinish: onFinish}
}

// SetRegistry makes jobs that start from now on use r. Running jobs keep
// the registry they started with.
func (j *Jobs) SetRegistry(r *core.Registry) {
	j.mu.Lock()
	j.registry = r
	j.mu.Unlock()
}

// SetLimit changes how many jobs run at once.
func (j *Jobs) SetLimit(n int) {
	j.mu.Lock()
	j.limit = max(n, 1)
	j.mu.Unlock()
	j.schedule()
}

// Submit queues a job.
func (j *Jobs) Submit(job core.Job, copyResult bool) JobID {
	j.mu.Lock()
	id := j.nextID
	j.nextID++
	j.entries = append(j.entries, &Entry{ID: id, Input: job.Input, To: job.To, Setup: Setup{job.Options, job.Output},
		Status: Queued, Progress: -1, CopyResult: copyResult})
	j.jobs[id] = job
	j.pending = append(j.pending, id)
	j.mu.Unlock()
	j.schedule()
	j.onChange()
	return id
}

func (j *Jobs) entry(id JobID) *Entry {
	for _, e := range j.entries {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// schedule starts as many queued jobs as the limits allow, in order; a
// video job waits for the video lane without holding back others.
func (j *Jobs) schedule() {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := 0; i < len(j.pending) && j.running < j.limit; {
		id := j.pending[i]
		job := j.jobs[id]
		video := core.IsVideoJob(&job)
		if video && j.video {
			i++
			continue
		}
		j.pending = slices.Delete(j.pending, i, i+1)
		j.running++
		if video {
			j.video = true
		}
		ctx, cancel := context.WithCancel(context.Background())
		j.cancels[id] = cancel
		e := j.entry(id)
		e.Status, e.Started = Running, time.Now()
		go j.run(ctx, id, job, video, j.registry)
	}
}

func (j *Jobs) run(ctx context.Context, id JobID, job core.Job, video bool, r *core.Registry) {
	outputs, err := r.Run(ctx, &job, func(p float64) {
		j.mu.Lock()
		if e := j.entry(id); e != nil {
			e.Progress = p
		}
		notify := time.Since(j.lastNotify) > 100*time.Millisecond
		if notify {
			j.lastNotify = time.Now()
		}
		j.mu.Unlock()
		if notify {
			j.onChange()
		}
	})
	j.mu.Lock()
	j.running--
	if video {
		j.video = false
	}
	delete(j.cancels, id)
	delete(j.jobs, id)
	var done Entry
	if e := j.entry(id); e != nil {
		e.Status = Finished
		switch {
		case err == nil:
			e.Outcome, e.Outputs, e.Progress = Done, outputs, 1
		case core.IsCancelled(err):
			e.Outcome = Cancelled
		default:
			e.Outcome, e.Err = Failed, err.Error()
		}
		done = *e
	}
	j.mu.Unlock()
	j.schedule()
	j.onFinish(done)
	j.onChange()
}

// Cancel stops a running job or drops a queued one. Either way it finishes
// as cancelled.
func (j *Jobs) Cancel(id JobID) {
	j.mu.Lock()
	if cancel, ok := j.cancels[id]; ok {
		j.mu.Unlock()
		cancel()
		return
	}
	i := slices.Index(j.pending, id)
	if i < 0 {
		j.mu.Unlock()
		return
	}
	j.pending = slices.Delete(j.pending, i, i+1)
	delete(j.jobs, id)
	var done Entry
	if e := j.entry(id); e != nil {
		e.Status, e.Outcome = Finished, Cancelled
		done = *e
	}
	j.mu.Unlock()
	j.onFinish(done)
	j.onChange()
}

// CancelAll stops everything.
func (j *Jobs) CancelAll() {
	for _, e := range j.Snapshot() {
		if e.Status != Finished {
			j.Cancel(e.ID)
		}
	}
}

// Forget drops finished jobs from the session list (history keeps them).
func (j *Jobs) Forget(ids ...JobID) {
	j.mu.Lock()
	j.entries = slices.DeleteFunc(j.entries, func(e *Entry) bool {
		return e.Status == Finished && (len(ids) == 0 || slices.Contains(ids, e.ID))
	})
	j.mu.Unlock()
	j.onChange()
}

// Snapshot copies every job this session, oldest first.
func (j *Jobs) Snapshot() []Entry {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Entry, len(j.entries))
	for i, e := range j.entries {
		out[i] = *e
	}
	return out
}

// Get copies one job.
func (j *Jobs) Get(id JobID) (Entry, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if e := j.entry(id); e != nil {
		return *e, true
	}
	return Entry{}, false
}

// Active counts queued and running jobs.
func (j *Jobs) Active() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.pending) + j.running
}
