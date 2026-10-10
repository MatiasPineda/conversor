package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"conversor/internal/core"
	"conversor/internal/engines"
)

// keepHistory is how many finished conversions history keeps.
const keepHistory = 1000

// Outcome is how a conversion ended.
type Outcome string

const (
	Done      Outcome = "done"
	Failed    Outcome = "failed"
	Cancelled Outcome = "cancelled"
)

// Setup is how a conversion was asked to run, so Retry can run it the
// same way.
type Setup struct {
	Options core.Options `json:"options"`
	Output  core.Output  `json:"output"`
}

// Record is one finished conversion.
type Record struct {
	ID int64 `json:"id"`
	// FinishedAt is Unix seconds.
	FinishedAt int64    `json:"finished_at"`
	Input      string   `json:"input"`
	To         string   `json:"to"`
	Outcome    Outcome  `json:"outcome"`
	Outputs    []string `json:"outputs,omitempty"`
	Error      string   `json:"error,omitempty"`
	Setup      Setup    `json:"setup"`
}

// History keeps finished conversions in a JSON-lines file in the data
// directory, so the Activity list survives restarts.
type History struct {
	mu      sync.Mutex
	path    string
	records []Record // oldest first
	nextID  int64
}

// HistoryPath is where history lives.
func HistoryPath() string {
	if d := engines.DataDir(); d != "" {
		return filepath.Join(d, "history.jsonl")
	}
	return ""
}

// OpenHistory loads history from path; "" keeps it in memory only. Lines
// that don't parse are dropped.
func OpenHistory(path string) (*History, error) {
	h := &History{path: path, nextID: 1}
	if path == "" {
		return h, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.ID > 0 {
			h.records = append(h.records, r)
			h.nextID = max(h.nextID, r.ID+1)
		}
	}
	return h, nil
}

// Add stores a finished conversion and returns its id. Older records past
// the limit are dropped.
func (h *History) Add(r Record) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	r.ID = h.nextID
	h.nextID++
	h.records = append(h.records, r)
	if len(h.records) > keepHistory {
		h.records = slices.Clone(h.records[len(h.records)-keepHistory:])
		h.saveLocked()
		return r.ID
	}
	if h.path != "" {
		if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err == nil {
			if f, err := os.OpenFile(h.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				line, _ := json.Marshal(r)
				f.Write(append(line, '\n'))
				f.Close()
			}
		}
	}
	return r.ID
}

// Recent lists the newest limit records, newest first.
func (h *History) Recent(limit int) []Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Record
	for i := len(h.records) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, h.records[i])
	}
	return out
}

// Get finds a record.
func (h *History) Get(id int64) (Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.ID == id {
			return r, true
		}
	}
	return Record{}, false
}

// Delete removes records.
func (h *History) Delete(ids ...int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = slices.DeleteFunc(h.records, func(r Record) bool { return slices.Contains(ids, r.ID) })
	h.saveLocked()
}

// Clear removes every record.
func (h *History) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = nil
	h.saveLocked()
}

func (h *History) saveLocked() {
	if h.path == "" {
		return
	}
	var buf bytes.Buffer
	for _, r := range h.records {
		line, _ := json.Marshal(r)
		buf.Write(append(line, '\n'))
	}
	writeAtomic(h.path, buf.Bytes())
}
