package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rogadev/paceline/internal/config"
	"github.com/rogadev/paceline/internal/gitinfo"
	"github.com/rogadev/paceline/internal/mcp"
	"github.com/rogadev/paceline/internal/progress"
)

const instructions = `paceline shows Claude Code usage and a long job's progress in the status line.
Usage: call get_today_budget before starting a long or expensive task, or a batch of subagents, to see how much of today's usage budget is left, and get_usage for the five-hour session and weekly limits. Both are read-only and any agent may call them.
Progress: use the progress tools for work with three or more steps that will run a while, such as a batch of issues or a multi-task build.
Plan first: before starting, call progress_start once with every step you expect, in order. Give each step a weight (its size relative to the others, for example 1 for a small fix and 5 for a feature) and the stages it will pass through (for example ["design", "build", "review", "commit"]). With weights and stages, the bar shows an accurate percentage that moves within a step.
Then report as you go: progress_step when a step starts, changes stage, or ends; progress_add_steps if the plan grows; progress_finish at the end.
Only the agent that owns the plan reports progress; subagents do not call the progress tools.`

// cwdProp is the optional cwd argument shared by every progress tool.
const cwdProp = `"cwd": {"type": "string", "description": "Directory inside the repository the run belongs to. Defaults to the server's working directory, which is normally the project."}`

const stepsSchema = `{
  "type": "array", "minItems": 1, "maxItems": 50,
  "items": {
    "type": "object",
    "required": ["id", "label"],
    "properties": {
      "id": {"type": "string", "description": "Unique id used to update the step later, such as an issue number or task number."},
      "label": {"type": "string", "description": "Short text shown while the step is active, such as \"#41\" or \"auth\". At most 40 characters; keep it under 12."},
      "weight": {"type": "number", "minimum": 0, "maximum": 1000, "description": "Planned size relative to the other steps. Omit for 1."},
      "stages": {"type": "array", "maxItems": 12, "items": {"type": "string"}, "description": "Stages the step will pass through, in order, such as [\"design\", \"build\", \"review\", \"commit\"]."}
    }
  }
}`

// sessionEnv names the variable Claude Code sets to its session id in the
// processes it starts, this server among them.
const sessionEnv = "CLAUDE_CODE_SESSION_ID"

// toolset holds what the tools need from the environment, so tests can point
// them at a temporary repo, a temporary config directory, a fixed clock, and
// a session of their choosing.
type toolset struct {
	getwd func() (string, error)
	now   func() time.Time
	// session is the Claude Code session whose run the progress tools write,
	// or "" for the shared run when the server is started some other way.
	session string
	// configDir is Claude Code's config directory, which holds the usage feed
	// and the day anchor the usage tools read.
	configDir string
}

func (t toolset) gitDir(cwd string) (string, error) {
	if cwd == "" {
		var err error
		if cwd, err = t.getwd(); err != nil {
			return "", err
		}
	}
	gitDir := gitinfo.FindGitDir(cwd)
	if gitDir == "" {
		return "", fmt.Errorf("%s is not inside a git repository; pass cwd to name one", cwd)
	}
	return gitDir, nil
}

// decode reads tool arguments into v and resolves the run's git directory.
func (t toolset) decode(args json.RawMessage, v any) (string, error) {
	var where struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(args, v); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	_ = json.Unmarshal(args, &where)
	return t.gitDir(where.Cwd)
}

// update loads the current run, applies change, and saves it. A change that
// fails, or leaves the run invalid, leaves the file untouched.
func (t toolset) update(gitDir string, change func(*progress.Run) error) (string, error) {
	r, err := progress.Load(gitDir, t.session)
	if err != nil {
		return "", err
	}
	if err := change(r); err != nil {
		return "", err
	}
	return t.save(gitDir, r)
}

func (t toolset) save(gitDir string, r *progress.Run) (string, error) {
	if err := progress.Save(gitDir, t.session, r, t.now()); err != nil {
		return "", err
	}
	return summary(r), nil
}

// summary tells the agent where the run stands after a change.
func summary(r *progress.Run) string {
	s := fmt.Sprintf("%s: %s, %d of %d steps settled", r.Name, r.Phase, r.SettledCount(), len(r.Steps))
	if r.Planned() {
		s += fmt.Sprintf(", %d%% of planned work", int(r.Percent()))
	}
	return s + "."
}

func schema(required string, props ...string) json.RawMessage {
	body := `{"type": "object", "required": [` + required + `], "properties": {`
	for _, p := range append(props, cwdProp) {
		body += p + ","
	}
	return json.RawMessage(body[:len(body)-1] + "}}")
}

func (t toolset) tools() []mcp.Tool {
	return append([]mcp.Tool{
		{
			Name: "progress_start",
			Description: "Start a progress bar for a long job, replacing any earlier run in this session. " +
				"Call once, after planning, with every step you expect in order; every step starts pending. " +
				"Give steps weights and stages so the bar's percentage is accurate.",
			InputSchema: schema(`"name", "steps"`,
				`"name": {"type": "string", "description": "Short name for the run, shown before the bar, such as \"loop\" or \"orc\"."}`,
				`"steps": `+stepsSchema),
			Call: func(args json.RawMessage) (string, error) {
				var a struct {
					Name  string             `json:"name"`
					Steps []progress.NewStep `json:"steps"`
				}
				gitDir, err := t.decode(args, &a)
				if err != nil {
					return "", err
				}
				progress.Prune(gitDir, t.session, t.now())
				return t.save(gitDir, progress.Start(a.Name, a.Steps))
			},
		},
		{
			Name: "progress_step",
			Description: "Update one step: mark it active when you start it, move it to its next stage, " +
				"or mark it done, skipped, or blocked when it ends. Activating a step returns any other active step to pending.",
			InputSchema: schema(`"id"`,
				`"id": {"type": "string", "description": "The step's id from progress_start."}`,
				`"status": {"type": "string", "enum": ["active", "done", "skipped", "blocked", "pending"], "description": "New status. Omit when only the stage changes."}`,
				`"stage": {"type": "string", "description": "The stage the step is in now, one of its planned stages. Implies active."}`),
			Call: func(args json.RawMessage) (string, error) {
				var a struct{ ID, Status, Stage string }
				gitDir, err := t.decode(args, &a)
				if err != nil {
					return "", err
				}
				return t.update(gitDir, func(r *progress.Run) error { return r.SetStep(a.ID, a.Status, a.Stage) })
			},
		},
		{
			Name: "progress_label",
			Description: "Set the bar's status text for work that is not a step, such as \"review 2/3\" or \"planning\". " +
				"An empty label returns to showing the active step.",
			InputSchema: schema(`"label"`,
				`"label": {"type": "string", "description": "Status text, at most 40 characters. Empty to clear."}`),
			Call: func(args json.RawMessage) (string, error) {
				var a struct{ Label string }
				gitDir, err := t.decode(args, &a)
				if err != nil {
					return "", err
				}
				return t.update(gitDir, func(r *progress.Run) error { r.SetLabel(a.Label); return nil })
			},
		},
		{
			Name:        "progress_add_steps",
			Description: "Append steps to the current run when the plan grows, such as a fix pass after review or a refilled batch slot.",
			InputSchema: schema(`"steps"`, `"steps": `+stepsSchema),
			Call: func(args json.RawMessage) (string, error) {
				var a struct {
					Steps []progress.NewStep `json:"steps"`
				}
				gitDir, err := t.decode(args, &a)
				if err != nil {
					return "", err
				}
				return t.update(gitDir, func(r *progress.Run) error { return r.AddSteps(a.Steps) })
			},
		},
		{
			Name: "progress_finish",
			Description: "End the run: \"done\" when the job finished, \"halted\" when it stopped early and needs a person. " +
				"A done run disappears from the status line 30 minutes later.",
			InputSchema: schema(`"outcome"`,
				`"outcome": {"type": "string", "enum": ["done", "halted"]}`),
			Call: func(args json.RawMessage) (string, error) {
				var a struct{ Outcome string }
				gitDir, err := t.decode(args, &a)
				if err != nil {
					return "", err
				}
				return t.update(gitDir, func(r *progress.Run) error { return r.Finish(a.Outcome) })
			},
		},
	}, t.usageTools()...)
}

var errNoArgs = errors.New("paceline-mcp takes no arguments besides --version; Claude Code starts it over stdio")

func defaultToolset() toolset {
	t := toolset{getwd: os.Getwd, now: time.Now, configDir: config.Dir()}
	if s := os.Getenv(sessionEnv); progress.ValidSession(s) {
		t.session = s
	}
	return t
}
