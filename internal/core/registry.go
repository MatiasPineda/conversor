package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"conversor/internal/i18n"
)

// maxHops caps route length: longer conversions are almost always lossy
// detours.
const maxHops = 3

// losslessHops are intermediate formats that lose nothing, preferred when
// routes tie on length.
var losslessHops = []string{"png", "tiff", "wav", "flac", "mkv", "pdf", "odt", "ods", "odp"}

// Hop is an engine and the step it performs.
type Hop struct {
	Engine Engine
	Step   Step
}

// Plan is the engines chosen for each hop of a conversion.
type Plan []Hop

// Describe renders the plan as "heic -[image]-> png -[pdf]-> pdf".
func (p Plan) Describe() string {
	var b strings.Builder
	for i, h := range p {
		if i == 0 {
			b.WriteString(h.Step.From.ID)
		}
		fmt.Fprintf(&b, " -[%s]-> %s", h.Engine.ID(), h.Step.To.ID)
	}
	return b.String()
}

// UnavailableEngine is an engine that was skipped, with the reason.
type UnavailableEngine struct {
	ID, Reason string
}

// Registry knows every engine that can run here and routes conversions
// through them.
type Registry struct {
	engines []Engine
	// edges maps a source format id to the best hop for each target id.
	edges       map[string]map[string]Hop
	unavailable []UnavailableEngine
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{edges: map[string]map[string]Hop{}}
}

// Register adds an engine. Engines that can't run on this machine are
// recorded as unavailable instead.
func (r *Registry) Register(e Engine) *Registry {
	if reason := e.Unavailable(); reason != "" {
		r.unavailable = append(r.unavailable, UnavailableEngine{e.ID(), reason})
		return r
	}
	for _, step := range e.Steps() {
		targets := r.edges[step.From.ID]
		if targets == nil {
			targets = map[string]Hop{}
			r.edges[step.From.ID] = targets
		}
		if cur, ok := targets[step.To.ID]; !ok || e.Priority() > cur.Engine.Priority() {
			targets[step.To.ID] = Hop{e, step}
		}
	}
	r.engines = append(r.engines, e)
	return r
}

// Engines lists the engines that can run here, in registration order.
func (r *Registry) Engines() []Engine { return r.engines }

// Unavailable lists the engines that were skipped.
func (r *Registry) Unavailable() []UnavailableEngine { return r.unavailable }

// Plan finds the shortest chain of engines from one format to another.
func (r *Registry) Plan(from, to *Format) (Plan, error) {
	if from == to {
		return nil, NoRoute(from.ID, to.ID)
	}
	type prevHop struct {
		node string
		hop  Hop
	}
	prev := map[string]prevHop{}
	depth := map[string]int{from.ID: 0}
	queue := []string{from.ID}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if node == to.ID {
			break
		}
		d := depth[node]
		if d == maxHops {
			continue
		}
		// Map order is random, so equal-length routes would be picked at
		// random. Visit neighbours in a fixed, preferred order.
		next := make([]string, 0, len(r.edges[node]))
		for target := range r.edges[node] {
			next = append(next, target)
		}
		edges := r.edges[node]
		sort.Slice(next, func(i, j int) bool {
			a, b := edges[next[i]], edges[next[j]]
			if a.Engine.Priority() != b.Engine.Priority() {
				return a.Engine.Priority() > b.Engine.Priority()
			}
			la, lb := slices.Contains(losslessHops, next[i]), slices.Contains(losslessHops, next[j])
			if la != lb {
				return la
			}
			return next[i] < next[j]
		})
		for _, target := range next {
			hop := edges[target]
			// Only a direct step may turn a still file into video or audio,
			// so an SVG never offers MP4 by way of an animated GIF.
			if d > 0 && hop.Step.To.Category.IsMedia() && !from.Category.IsMedia() {
				continue
			}
			if _, seen := depth[target]; !seen {
				depth[target] = d + 1
				prev[target] = prevHop{node, hop}
				queue = append(queue, target)
			}
		}
	}
	var plan Plan
	for node := to.ID; node != from.ID; {
		p, ok := prev[node]
		if !ok {
			return nil, NoRoute(from.ID, to.ID)
		}
		plan = append(plan, p.hop)
		node = p.node
	}
	slices.Reverse(plan)
	return plan, nil
}

// Targets lists every format a file of from can become, sorted by category
// then name.
func (r *Registry) Targets(from *Format) []*Format {
	var out []*Format
	for _, to := range Formats {
		if _, err := r.Plan(from, to); err == nil {
			out = append(out, to)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// MenuTargets lists the few targets a right-click menu offers for from,
// most wanted first. Everything else stays one click away in Quick convert,
// so a HEIC photo is offered JPEG, PNG and WebP rather than ICO or QOI.
// Only reachable targets are listed; a format with none of its preferred
// targets gets the first few it can reach.
func (r *Registry) MenuTargets(from *Format) []*Format {
	const menuSize = 4
	var preferred []string
	switch {
	case from.ID == "gif":
		preferred = []string{"mp4", "webp", "png"}
	case from.Category == Image:
		preferred = []string{"jpeg", "png", "webp"}
	case from.Category == Vector:
		preferred = []string{"png", "jpeg", "pdf"}
	case from.Category == Video:
		preferred = []string{"mp4", "mov", "gif", "mp3"}
	case from.Category == Audio:
		preferred = []string{"mp3", "m4a", "wav"}
	case from.Category == PDF:
		preferred = []string{"png", "jpeg", "docx"}
	case from.Category == Document:
		preferred = []string{"pdf", "docx", "txt"}
	case from.Category == Spreadsheet:
		preferred = []string{"pdf", "xlsx", "csv"}
	case from.Category == Presentation:
		preferred = []string{"pdf", "pptx"}
	}
	reachable := r.Targets(from)
	var picked []*Format
	for _, id := range preferred {
		for _, f := range reachable {
			if f.ID == id {
				picked = append(picked, f)
			}
		}
	}
	if len(picked) == 0 && len(reachable) > 0 {
		return reachable[:min(menuSize, len(reachable))]
	}
	return picked
}

// OutputKind says where a job's output goes.
type OutputKind int

const (
	// Beside puts output next to the input, renamed on collision.
	Beside OutputKind = iota
	// InDir puts output in Output.Path, renamed on collision.
	InDir
	// Exact writes exactly Output.Path (pages get -2, -3) and fails if it's taken.
	Exact
)

// Output is where a job's output goes.
type Output struct {
	Kind OutputKind
	Path string
}

// Job is one file to convert.
type Job struct {
	Input   string
	To      *Format
	Options Options
	Output  Output
}

// Run converts a job: routes it, runs every hop on every artifact the
// previous hop produced, and publishes the results without replacing any
// file. It returns the published paths, one per page for paged output. On
// error or cancellation nothing the job wrote is left behind.
func (r *Registry) Run(ctx context.Context, job *Job, progress Progress) ([]string, error) {
	if progress == nil {
		progress = func(float64) {}
	}
	if err := job.Options.Validate(); err != nil {
		return nil, err
	}
	from := ByPath(job.Input)
	if from == nil {
		return nil, UndetectedFormat(job.Input)
	}
	plan, err := r.Plan(from, job.To)
	if err != nil {
		return nil, err
	}
	dest, err := DestinationOf(job)
	if err != nil {
		return nil, err
	}
	// Staging lives in the destination so publishing is a same-volume
	// rename. It is removed on every return.
	staging, err := dest.Staging()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	type inFlight struct {
		path string
		page int
	}
	n := float64(len(plan))
	current := []inFlight{{job.Input, 0}}
	for i, hop := range plan {
		k := float64(len(current))
		var next []inFlight
		for j, file := range current {
			if ctx.Err() != nil {
				return nil, ErrCancelled
			}
			outDir := filepath.Join(staging, fmt.Sprintf("%d-%d", i, j))
			if err := os.Mkdir(outDir, 0o755); err != nil {
				return nil, err
			}
			base := float64(i) + float64(j)/k
			scaled := func(p float64) {
				if p < 0 {
					progress(-1)
					return
				}
				progress((base + min(max(p, 0), 1)/k) / n)
			}
			task := NewTask(ctx, hop.Step, &job.Options, scaled)
			produced, err := hop.Engine.Convert(task, file.path, outDir)
			if ctx.Err() != nil {
				return nil, ErrCancelled
			}
			if err != nil {
				return nil, err
			}
			if len(produced) == 0 {
				return nil, EngineFailed(hop.Engine.ID(), "%s", i18n.T("produced no output"))
			}
			for idx, path := range produced {
				if rel, err := filepath.Rel(outDir, path); err != nil || strings.HasPrefix(rel, "..") {
					return nil, EngineFailed(hop.Engine.ID(), "%s", i18n.T("produced no output"))
				}
				// Engines name artifacts 1.ext, 2.ext, ... by index, or by
				// page when they paginate, so a hop that writes one file
				// keeps its input's page.
				page := idx
				stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
				if v, err := strconv.Atoi(stem); err == nil && v > 0 {
					page = v - 1
				}
				next = append(next, inFlight{path, file.page + page})
			}
			progress((base + 1/k) / n)
		}
		current = next
	}
	if ctx.Err() != nil {
		return nil, ErrCancelled
	}
	files := make([]string, len(current))
	pages := make([]int, len(current))
	for i, f := range current {
		files[i], pages[i] = f.path, f.page
	}
	published, err := dest.Publish(files, pages)
	if err != nil {
		return nil, err
	}
	progress(1)
	return published, nil
}

// Destination is where a job's results land, by the rules every conversion
// follows: next to the input or in a folder, named after the input with the
// target's extension and renamed on collision, or at an exact path.
type Destination struct {
	Dir   string
	stem  string
	ext   string
	exact bool
}

// DestinationOf works out where job's results go.
func DestinationOf(job *Job) (*Destination, error) {
	switch job.Output.Kind {
	case Exact:
		// The caller's name wins, extension included ("photo.jpeg", or none
		// at all), so the first output lands exactly on the path.
		base := filepath.Base(job.Output.Path)
		ext := strings.TrimPrefix(filepath.Ext(base), ".")
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if stem == "" {
			return nil, OutputExists(job.Output.Path)
		}
		return &Destination{Dir: filepath.Dir(job.Output.Path), stem: stem, ext: ext, exact: true}, nil
	default:
		dir := filepath.Dir(job.Input)
		if job.Output.Kind == InDir {
			dir = job.Output.Path
		}
		base := filepath.Base(job.Input)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if stem == "" {
			return nil, UndetectedFormat(job.Input)
		}
		return &Destination{Dir: dir, stem: stem, ext: job.To.Extension()}, nil
	}
}

// Staging creates a hidden folder inside Dir to write results into before
// Publish, so publishing is a same-volume rename. The caller removes it.
func (d *Destination) Staging() (string, error) {
	return os.MkdirTemp(d.Dir, ".conversor-")
}

// Publish moves files from staging to their final names. pages[i] is the
// 0-based page files[i] came from; a single result is page 0.
func (d *Destination) Publish(files []string, pages []int) ([]string, error) {
	return publish(files, pages, d.Dir, d.stem, d.ext, d.exact)
}
