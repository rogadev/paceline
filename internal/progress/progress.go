// Package progress reads and writes the progress file: a small JSON record of
// a long-running job's steps that the status line draws as a bar.
//
// The file lives at <git-dir>/paceline/progress.json, so each repo and
// worktree has its own and it is never committed. Any program may write it;
// paceline only reads it, and paceline-mcp writes it for agents. Everything in
// it is untrusted: the reader caps its size and rejects anything malformed,
// and callers must sanitize every string before printing it.
package progress

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"
	"unicode/utf8"
)

// Version is the file format version this package reads and writes.
const Version = 1

// Limits shared by the reader and the writer, so anything the writer accepts
// the reader can draw.
const (
	MaxSteps        = 50
	MaxStages       = 12
	MaxLabelRunes   = 40
	MaxWeight       = 1000
	maxFileBytes    = 32 * 1024
	dirName         = "paceline"
	fileName        = "progress.json"
	pausedAfter     = time.Hour
	quietHiddenFrom = 4 * time.Hour
	doneHiddenFrom  = 30 * time.Minute
)

// Run phases.
const (
	Running = "running"
	Done    = "done"
	Halted  = "halted"
)

// Step statuses.
const (
	Pending = "pending"
	Active  = "active"
	Settled = "done"
	Skipped = "skipped"
	Blocked = "blocked"
)

var (
	phases   = []string{Running, Done, Halted}
	statuses = []string{Pending, Active, Settled, Skipped, Blocked}
)

// Step is one unit of the run. Weight and Stages come from the plan: Weight is
// the step's size relative to the others (0 means 1), and Stages names what
// the step passes through, with Stage the one it is in now.
type Step struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Status string   `json:"status"`
	Weight float64  `json:"weight,omitempty"`
	Stages []string `json:"stages,omitempty"`
	Stage  string   `json:"stage,omitempty"`
}

// Run is the whole progress file.
type Run struct {
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	Phase     string    `json:"phase"`
	Label     string    `json:"label,omitempty"`
	Steps     []Step    `json:"steps"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Path is the progress file for a git directory.
func Path(gitDir string) string { return filepath.Join(gitDir, dirName, fileName) }

func validText(s string, required bool) error {
	switch {
	case required && s == "":
		return errors.New("must not be empty")
	case utf8.RuneCountInString(s) > MaxLabelRunes:
		return fmt.Errorf("must be at most %d characters", MaxLabelRunes)
	}
	return nil
}

// Validate checks everything the reader relies on. The writer runs it before
// every save, so an invalid change never reaches the file.
func (r *Run) Validate() error {
	if r.Version != Version {
		return fmt.Errorf("version must be %d", Version)
	}
	if err := validText(r.Name, true); err != nil {
		return fmt.Errorf("name %w", err)
	}
	if !slices.Contains(phases, r.Phase) {
		return fmt.Errorf("phase must be one of %v", phases)
	}
	if err := validText(r.Label, false); err != nil {
		return fmt.Errorf("label %w", err)
	}
	if len(r.Steps) == 0 || len(r.Steps) > MaxSteps {
		return fmt.Errorf("a run needs 1 to %d steps", MaxSteps)
	}
	if r.UpdatedAt.IsZero() {
		return errors.New("updatedAt is required")
	}
	seen := map[string]bool{}
	for _, s := range r.Steps {
		if err := s.validate(); err != nil {
			return fmt.Errorf("step %q: %w", s.ID, err)
		}
		if seen[s.ID] {
			return fmt.Errorf("step id %q is used twice", s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}

func (s *Step) validate() error {
	if err := validText(s.ID, true); err != nil {
		return fmt.Errorf("id %w", err)
	}
	if err := validText(s.Label, false); err != nil {
		return fmt.Errorf("label %w", err)
	}
	if !slices.Contains(statuses, s.Status) {
		return fmt.Errorf("status must be one of %v", statuses)
	}
	if s.Weight < 0 || s.Weight > MaxWeight {
		return fmt.Errorf("weight must be between 0 and %d", MaxWeight)
	}
	if len(s.Stages) > MaxStages {
		return fmt.Errorf("at most %d stages", MaxStages)
	}
	for _, stage := range s.Stages {
		if err := validText(stage, true); err != nil {
			return fmt.Errorf("stage %w", err)
		}
	}
	if s.Stage != "" && !slices.Contains(s.Stages, s.Stage) {
		return fmt.Errorf("stage %q is not one of its stages", s.Stage)
	}
	return nil
}

// Read loads the progress file in gitDir, or nil when it is missing,
// oversized, corrupt, or invalid. It never fails louder than that: the status
// line must render whatever the file holds.
func Read(gitDir string) *Run {
	if gitDir == "" {
		return nil
	}
	path := Path(gitDir)
	// Checked before opening, so a named pipe can't block the refresh.
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil
	}
	f, err := os.Open(path) //nolint:gosec // G304: the progress file in the repo's own git dir.
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	return parse(f)
}

// parse decodes and validates a run. The size checked before opening can lie
// (a symlink to a /proc file reports 0), so the read itself is capped too.
func parse(src io.Reader) *Run {
	data, err := io.ReadAll(io.LimitReader(src, maxFileBytes+1))
	if err != nil || len(data) > maxFileBytes {
		return nil
	}
	var r Run
	if json.Unmarshal(data, &r) != nil || r.Validate() != nil {
		return nil
	}
	return &r
}

// Visible reports whether the run belongs on the status line at now: a
// finished run is shown for a while, then hidden, and any other run is
// hidden once it has gone quiet long enough that its writer is gone.
func (r *Run) Visible(now time.Time) bool {
	if r.Phase == Done {
		return now.Sub(r.UpdatedAt) <= doneHiddenFrom
	}
	return now.Sub(r.UpdatedAt) <= quietHiddenFrom
}

// Paused reports whether a running run has gone quiet for so long that its
// writer was most likely stopped.
func (r *Run) Paused(now time.Time) bool {
	return r.Phase == Running && now.Sub(r.UpdatedAt) > pausedAfter
}

// Planned reports whether any step carries a weight or stages, which is what
// makes Percent more than a step count.
func (r *Run) Planned() bool {
	return slices.ContainsFunc(r.Steps, func(s Step) bool { return s.Weight > 0 || len(s.Stages) > 0 })
}

// ActiveIndex is the first active step, or -1.
func (r *Run) ActiveIndex() int {
	return slices.IndexFunc(r.Steps, func(s Step) bool { return s.Status == Active })
}

// SettledCount is how many steps are no longer pending or active.
func (r *Run) SettledCount() int {
	n := 0
	for _, s := range r.Steps {
		if s.Status != Pending && s.Status != Active {
			n++
		}
	}
	return n
}

// Percent is the weighted share of planned work finished, 0 to 100. Skipped
// and blocked steps leave the total, since this run will not do them. An
// active step counts the stages it has finished: a step in its third of four
// stages is half done.
func (r *Run) Percent() float64 {
	var total, finished float64
	for _, s := range r.Steps {
		if s.Status == Skipped || s.Status == Blocked {
			continue
		}
		w := s.Weight
		if w == 0 {
			w = 1
		}
		total += w
		switch s.Status {
		case Settled:
			finished += w
		case Active:
			if i := slices.Index(s.Stages, s.Stage); i > 0 {
				finished += w * float64(i) / float64(len(s.Stages))
			}
		}
	}
	if total == 0 {
		return 100
	}
	return 100 * finished / total
}
