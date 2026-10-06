package progress

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// NewStep is a step as a writer plans it, before it has a status.
type NewStep struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Weight float64  `json:"weight"`
	Stages []string `json:"stages"`
}

func (n NewStep) step() Step {
	return Step{ID: n.ID, Label: n.Label, Status: Pending, Weight: n.Weight, Stages: n.Stages}
}

// Load reads the progress file for a writer. Unlike Read it reports why a
// file is unusable, so a tool can tell the agent to start a new run.
func Load(gitDir, session string) (*Run, error) {
	r := Read(gitDir, session)
	if r != nil {
		return r, nil
	}
	if _, err := os.Stat(Path(gitDir, session)); errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("no run in progress; start one first")
	}
	return nil, errors.New("the progress file is unreadable or invalid; start a new run to replace it")
}

// Save validates r, stamps it with now, and writes it via a temp file and
// rename, so the status line never reads a half-written file.
func Save(gitDir, session string, r *Run, now time.Time) error {
	r.Version = Version
	r.UpdatedAt = now.UTC().Truncate(time.Second)
	if err := r.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxFileBytes {
		return errors.New("the run is too large to save; use fewer or shorter steps")
	}
	path := Path(gitDir, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	// A random temp name that must not already exist, so a symlink planted in
	// the repo's git dir can't redirect the write to another file.
	f, err := os.CreateTemp(filepath.Dir(path), "progress-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// rename retries briefly on a permission error: on Windows, replacing a file
// fails while a status line refresh has it open for reading.
func rename(from, to string) error {
	var err error
	for range 5 {
		if err = os.Rename(from, to); err == nil || !errors.Is(err, fs.ErrPermission) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

// Start begins a new run with every step pending, replacing any previous run.
func Start(name string, steps []NewStep) *Run {
	r := &Run{Version: Version, Name: name, Phase: Running}
	for _, n := range steps {
		r.Steps = append(r.Steps, n.step())
	}
	return r
}

func (r *Run) find(id string) (*Step, error) {
	i := slices.IndexFunc(r.Steps, func(s Step) bool { return s.ID == id })
	if i < 0 {
		return nil, fmt.Errorf("no step with id %q", id)
	}
	return &r.Steps[i], nil
}

// SetStep changes one step's status and, optionally, its current stage.
// Naming a stage implies the step is active. Only one step is active at a
// time, so activating a step returns any other active step to pending.
func (r *Run) SetStep(id, status, stage string) error {
	s, err := r.find(id)
	if err != nil {
		return err
	}
	if status == "" {
		if stage == "" {
			return errors.New("give a status, a stage, or both")
		}
		status = Active
	}
	if !slices.Contains(statuses, status) {
		return fmt.Errorf("status must be one of %v", statuses)
	}
	if stage != "" {
		if !slices.Contains(s.Stages, stage) {
			return fmt.Errorf("stage %q is not one of step %q's stages %v", stage, id, s.Stages)
		}
		if status != Active {
			return errors.New("only an active step has a current stage")
		}
		s.Stage = stage
	}
	s.Status = status
	if status == Active {
		for i := range r.Steps {
			if r.Steps[i].ID != id && r.Steps[i].Status == Active {
				r.Steps[i].Status = Pending
			}
		}
	} else {
		s.Stage = ""
	}
	// A step change after finishing means the run picked up again.
	r.Phase = Running
	return nil
}

// SetLabel sets the bar's status text, for stages that are not steps. An
// empty label returns to the default.
func (r *Run) SetLabel(label string) { r.Label = label }

// AddSteps appends steps to a run that grew.
func (r *Run) AddSteps(steps []NewStep) error {
	if len(steps) == 0 {
		return errors.New("give at least one step")
	}
	for _, n := range steps {
		r.Steps = append(r.Steps, n.step())
	}
	if len(r.Steps) > MaxSteps {
		return fmt.Errorf("a run holds at most %d steps", MaxSteps)
	}
	return nil
}

// Finish ends the run as done or halted. Finishing done marks the active step
// done; every other step keeps the status the writer gave it.
func (r *Run) Finish(outcome string) error {
	if outcome != Done && outcome != Halted {
		return fmt.Errorf("outcome must be %q or %q", Done, Halted)
	}
	r.Phase = outcome
	r.Label = ""
	for i := range r.Steps {
		if r.Steps[i].Status == Active && outcome == Done {
			r.Steps[i].Status = Settled
			r.Steps[i].Stage = ""
		}
	}
	return nil
}

// Prune removes other sessions' progress files not written for a day. Each
// session leaves its file behind when it ends; this keeps them from piling up.
func Prune(gitDir, keep string, now time.Time) {
	dir := filepath.Join(gitDir, dirName)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		session, ok := strings.CutPrefix(name, sessionPrefix)
		session, isJSON := strings.CutSuffix(session, sessionSuffix)
		if !ok || !isJSON || session == keep || !ValidSession(session) {
			continue
		}
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() && now.Sub(info.ModTime()) > sessionPruneAge {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
