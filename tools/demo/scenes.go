package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/rogadev/paceline/internal/config"
	"github.com/rogadev/paceline/internal/payload"
	"github.com/rogadev/paceline/internal/progress"
	"github.com/rogadev/paceline/internal/render"
	"github.com/rogadev/paceline/pace"
)

// Version is the scenes.json format version. Other repos read the file, so
// bump it whenever a field changes meaning or goes away.
const Version = 1

// Columns is the mock terminal's width.
const Columns = 110

// Layouts.
const (
	// LayoutTerminal is one Claude Code window: reply, spinner, prompt, mode
	// line, and one status line.
	LayoutTerminal = "terminal"
	// LayoutWindows is several status lines, one per window, and nothing else.
	LayoutWindows = "windows"
)

// Document is the whole of scenes.json.
type Document struct {
	Version int     `json:"version"`
	Columns int     `json:"columns"`
	Palette Palette `json:"palette"`
	Scenes  []Scene `json:"scenes"`
}

// Scene is one part of the demo.
type Scene struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Caption    string `json:"caption"`
	Layout     string `json:"layout"`
	DurationMs int    `json:"durationMs"`
	Steps      []Step `json:"steps"`
}

// Step is one frame of a scene.
type Step struct {
	DurationMs int      `json:"durationMs"`
	Now        string   `json:"now"`
	Reply      string   `json:"reply,omitempty"`
	Spinner    *Spinner `json:"spinner,omitempty"`
	// Prompt is set in the terminal layout, even when empty, and nil in the
	// windows layout.
	Prompt      *string      `json:"prompt,omitempty"`
	ModeLine    string       `json:"modeLine,omitempty"`
	Subagents   []Subagent   `json:"subagents,omitempty"`
	StatusLines []StatusLine `json:"statusLines"`
}

// Spinner is Claude Code's working indicator.
type Spinner struct {
	Verb     string `json:"verb"`
	Activity string `json:"activity"`
}

// Subagent is one row under the mode line. The first is always the main
// agent, which has no task.
type Subagent struct {
	Name string `json:"name"`
	Task string `json:"task,omitempty"`
}

// StatusLine is one render.Render result, raw and parsed.
type StatusLine struct {
	ANSI  string `json:"ansi"`
	Plain string `json:"plain"`
	Runs  []Run  `json:"runs"`
}

// Shared scene inputs. See the scene script for why the week resets here.
const (
	model    = "Opus 5.5"
	homeDir  = "/home/dev/"
	modeLine = "\u23f5\u23f5 bypass permissions on (shift+tab to cycle)"
)

var weekResets = utc(11, 21, 0)

// utc is a time on a day in October 2026, in UTC so output never depends on
// the machine's zone.
func utc(day, hour, minute int) time.Time {
	return time.Date(2026, time.October, day, hour, minute, 0, 0, time.UTC)
}

// sceneDef is a scene's inputs; BuildDocument turns it into a Scene.
type sceneDef struct {
	id, title, caption, layout string
	snapshot                   pace.Snapshot
	steps                      []stepDef
}

type stepDef struct {
	ms        int
	now       time.Time
	reply     string
	spinner   *Spinner
	prompt    string
	modeLine  string
	subagents []Subagent
	windows   []window
}

// window is one Claude Code session's status-line inputs.
type window struct {
	project, branch, effort string
	fastMode                bool
	sessionUsed             float64
	sessionResets           time.Time
	weekUsed                float64
	ctx                     float64
	durationMs              float64
	cache                   *promptCache
	run                     *progress.Run
}

type promptCache struct {
	expiresAt     time.Time
	recacheTokens float64
}

// payloadJSON is the slice of Claude Code's status JSON the scenes fill in.
type payloadJSON struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Effort *struct {
		Level string `json:"level"`
	} `json:"effort,omitempty"`
	FastMode  bool   `json:"fast_mode,omitempty"`
	Cwd       string `json:"cwd"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
		ProjectDir string `json:"project_dir"`
	} `json:"workspace"`
	RateLimits struct {
		FiveHour limitJSON `json:"five_hour"`
		SevenDay limitJSON `json:"seven_day"`
	} `json:"rate_limits"`
	ContextWindow struct {
		UsedPercentage float64 `json:"used_percentage"`
	} `json:"context_window"`
	PromptCache *struct {
		ExpiresAt           int64   `json:"expires_at"`
		RecacheTokensIfCold float64 `json:"recache_tokens_if_cold"`
	} `json:"prompt_cache,omitempty"`
	Cost struct {
		TotalDurationMs float64 `json:"total_duration_ms"`
	} `json:"cost"`
}

type limitJSON struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
}

func (w window) dir() string { return homeDir + w.project }

// payload builds the window's status JSON and decodes it the way paceline
// decodes Claude Code's input.
func (w window) payload() (*payload.Payload, error) {
	var p payloadJSON
	p.Model.DisplayName = model
	if w.effort != "" {
		p.Effort = &struct {
			Level string `json:"level"`
		}{w.effort}
	}
	p.FastMode = w.fastMode
	p.Cwd = w.dir()
	p.Workspace.CurrentDir = w.dir()
	p.Workspace.ProjectDir = w.dir()
	p.RateLimits.FiveHour = limitJSON{w.sessionUsed, w.sessionResets.Unix()}
	p.RateLimits.SevenDay = limitJSON{w.weekUsed, weekResets.Unix()}
	p.ContextWindow.UsedPercentage = w.ctx
	if w.cache != nil {
		p.PromptCache = &struct {
			ExpiresAt           int64   `json:"expires_at"`
			RecacheTokensIfCold float64 `json:"recache_tokens_if_cold"`
		}{w.cache.expiresAt.Unix(), w.cache.recacheTokens}
	}
	p.Cost.TotalDurationMs = w.durationMs
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return payload.Decode(data)
}

// render calls the real renderer with fixed inputs: Now, the default config,
// the scene's snapshot (never written back, so every step stands alone), and
// the window's branch and progress run.
func (w window) render(now time.Time, snap pace.Snapshot) (string, error) {
	p, err := w.payload()
	if err != nil {
		return "", err
	}
	if w.run != nil {
		if err := w.run.Validate(); err != nil {
			return "", fmt.Errorf("progress run: %w", err)
		}
	}
	dir := w.dir()
	line := render.Render(p, render.Context{
		Now:    now,
		Config: config.Default(),
		ReadSnapshot: func() *pace.Snapshot {
			s := snap
			return &s
		},
		WriteSnapshot: func(pace.Snapshot) {},
		GitBranch: func(d string) string {
			if d == dir {
				return w.branch
			}
			return ""
		},
		Progress: func(d string) *progress.Run {
			if d != dir || w.run == nil {
				return nil
			}
			r := *w.run
			return &r
		},
	})
	if line == "" {
		return "", errors.New("the renderer printed nothing")
	}
	return line, nil
}

// BuildDocument renders every step of every scene.
func BuildDocument() (*Document, error) {
	doc := &Document{Version: Version, Columns: Columns, Palette: palette}
	for _, def := range sceneDefs() {
		scene, err := buildScene(def)
		if err != nil {
			return nil, fmt.Errorf("scene %s: %w", def.id, err)
		}
		doc.Scenes = append(doc.Scenes, scene)
	}
	return doc, nil
}

func buildScene(def sceneDef) (Scene, error) {
	scene := Scene{ID: def.id, Title: def.title, Caption: def.caption, Layout: def.layout}
	if len(def.steps) == 0 {
		return scene, errors.New("no steps")
	}
	for i, sd := range def.steps {
		step := Step{
			DurationMs: sd.ms,
			Now:        sd.now.UTC().Format(time.RFC3339),
			Reply:      sd.reply,
			Spinner:    sd.spinner,
			Subagents:  sd.subagents,
		}
		switch def.layout {
		case LayoutTerminal:
			if len(sd.windows) != 1 {
				return scene, fmt.Errorf("step %d: the terminal layout needs one window, got %d", i+1, len(sd.windows))
			}
			prompt := sd.prompt
			step.Prompt = &prompt
			step.ModeLine = sd.modeLine
		case LayoutWindows:
			if len(sd.windows) == 0 {
				return scene, fmt.Errorf("step %d: no windows", i+1)
			}
		default:
			return scene, fmt.Errorf("unknown layout %q", def.layout)
		}
		for _, w := range sd.windows {
			line, err := w.render(sd.now, def.snapshot)
			if err != nil {
				return scene, fmt.Errorf("step %d, %s: %w", i+1, w.project, err)
			}
			runs, err := ParseANSI(line)
			if err != nil {
				return scene, fmt.Errorf("step %d, %s: %w", i+1, w.project, err)
			}
			step.StatusLines = append(step.StatusLines, StatusLine{ANSI: line, Plain: plainText(runs), Runs: runs})
		}
		scene.DurationMs += sd.ms
		scene.Steps = append(scene.Steps, step)
	}
	return scene, nil
}

// snapshot is a day's budget anchor for the week that resets at weekResets.
func snapshot(day int, usedAtStart float64) pace.Snapshot {
	return pace.Snapshot{
		Date:        utc(day, 0, 0).Format("20060102"),
		ResetsAt:    float64(weekResets.Unix()),
		UsedAtStart: usedAtStart,
	}
}

// friday is the snapshot every scene but the last shares.
var friday = snapshot(9, 19.5)

// usageRow is one row of a scene's table in the script.
type usageRow struct {
	ms                         int
	now                        time.Time
	session, week, ctx, millis float64
}

func spin(verb, activity string) *Spinner {
	if activity == "" {
		return nil
	}
	return &Spinner{Verb: verb, Activity: activity}
}

// loopRun is a progress run with one step per id, in the given statuses.
func loopRun(now time.Time, phase string, ids []string, statuses ...string) *progress.Run {
	r := &progress.Run{Version: progress.Version, Name: "loop", Phase: phase, UpdatedAt: now.Add(-time.Minute)}
	for i, id := range ids {
		r.Steps = append(r.Steps, progress.Step{ID: id, Label: id, Status: statuses[i]})
	}
	return r
}

// agentsModeLine is the mode line with n subagents running.
func agentsModeLine(n int) string {
	if n == 0 {
		return modeLine
	}
	return modeLine + " \u00b7 /tasks to see subagents \u00b7 \u2190 " + strconv.Itoa(n) + " agents"
}

// terminalScene builds a terminal-layout scene where only the usage numbers
// and the spinner's activity change from step to step.
func terminalScene(def sceneDef, w window, rows []usageRow, reply, verb string, activities []string, prompt string) sceneDef {
	for i, row := range rows {
		win := w
		win.sessionUsed, win.weekUsed, win.ctx, win.durationMs = row.session, row.week, row.ctx, row.millis
		def.steps = append(def.steps, stepDef{
			ms: row.ms, now: row.now, reply: reply, spinner: spin(verb, activities[i]),
			prompt: prompt, modeLine: modeLine, windows: []window{win},
		})
	}
	return def
}

// sceneDefs is the scene script, encoded. Every value comes from it.
func sceneDefs() []sceneDef {
	return []sceneDef{
		freshDay(), workingDay(), overBudget(), sessionLow(),
		cacheCold(), agentJob(), severalWindows(), lastDay(),
	}
}

func freshDay() sceneDef {
	return terminalScene(
		sceneDef{
			id: "fresh-day", title: "Fresh day", layout: LayoutTerminal, snapshot: friday,
			caption: "Each morning, today's budget is set from what's left in the week, and it starts full.",
		},
		window{project: "acme-portal", branch: "dev", sessionResets: utc(9, 13, 0)},
		[]usageRow{
			{2500, utc(9, 9, 2), 2, 19.5, 8, 120000},
			{2500, utc(9, 9, 6), 4, 19.9, 11, 360000},
		},
		"Found it. The date picker formats in UTC. Switching it to the user's timezone now.",
		"Crafting", []string{"12s \u00b7 \u2193 0.8k tokens", "41s \u00b7 \u2193 2.6k tokens"},
		"also check the invoice dates while you're in there",
	)
}

func workingDay() sceneDef {
	return terminalScene(
		sceneDef{
			id: "working-day", title: "Working through the day", layout: LayoutTerminal, snapshot: friday,
			caption: "As you work, every number counts down, and today turns yellow, then red, as its budget runs low.",
		},
		window{project: "field-app", branch: "feat/offline", sessionResets: utc(9, 17, 0)},
		[]usageRow{
			{1800, utc(9, 12, 10), 20, 25.5, 22, 2400000},
			{1800, utc(9, 13, 20), 45, 33.5, 38, 6600000},
			{1800, utc(9, 14, 30), 62, 40.5, 51, 10800000},
			{2400, utc(9, 15, 40), 68, 44.5, 60, 15000000},
		},
		"Offline sync is in. Moving the queue logic into its own module next.",
		"Refactoring", []string{
			"2m 10s \u00b7 \u2193 6.8k tokens", "5m 02s \u00b7 \u2193 14.1k tokens",
			"8m 47s \u00b7 \u2193 22.9k tokens", "11m 30s \u00b7 \u2193 31.4k tokens",
		},
		"go ahead and do the conflict handling too",
	)
}

func overBudget() sceneDef {
	return terminalScene(
		sceneDef{
			id: "over-budget", title: "Over budget", layout: LayoutTerminal, snapshot: friday,
			caption: "Past today's budget, it shows how far over you are, in red.",
		},
		window{project: "billing-api", branch: "fix/webhooks", sessionResets: utc(9, 20, 0)},
		[]usageRow{
			{1800, utc(9, 16, 10), 52, 48.5, 41, 7800000},
			{1800, utc(9, 16, 25), 57, 51.1, 44, 8700000},
			{2600, utc(9, 16, 40), 61, 52.6, 47, 9600000},
		},
		"Webhook retries now back off properly. Adding a test for the duplicate event case.",
		"Debugging", []string{
			"52s \u00b7 \u2193 3.1k tokens", "1m 37s \u00b7 \u2193 5.9k tokens", "2m 20s \u00b7 \u2193 8.4k tokens",
		},
		"log the event id when a duplicate is skipped",
	)
}

func sessionLow() sceneDef {
	return terminalScene(
		sceneDef{
			id: "session-low", title: "Session running low", layout: LayoutTerminal, snapshot: friday,
			caption: "When the five-hour session runs low, it shows when it resets, and a filling context window gets a warning.",
		},
		window{project: "acme-portal", branch: "dev", effort: "high", sessionResets: utc(9, 15, 40)},
		[]usageRow{
			{1800, utc(9, 13, 10), 66, 38, 62, 9900000},
			{1800, utc(9, 13, 50), 74, 40, 72, 12300000},
			{2600, utc(9, 14, 30), 88, 42, 86, 14700000},
		},
		"The flaky test is a race in the session refresh. Tracing where the token gets swapped.",
		"Investigating", []string{
			"1m 12s \u00b7 \u2193 9.2k tokens", "2m 58s \u00b7 \u2193 18.4k tokens", "4m 31s \u00b7 \u2193 27.7k tokens",
		},
		"add a regression test for the token refresh once it's fixed",
	)
}

func cacheCold() sceneDef {
	def := sceneDef{
		id: "cache-cold", title: "Cache went cold", layout: LayoutTerminal, snapshot: friday,
		caption: "After a long pause the prompt cache expires, so it warns that the next message costs about twice as much.",
	}
	w := window{
		project: "docs-site", branch: "main", sessionResets: utc(9, 19, 0),
		cache: &promptCache{expiresAt: utc(9, 15, 5), recacheTokens: 82000},
	}
	rows := []usageRow{
		{2200, utc(9, 15, 0), 37, 30, 34, 2820000},
		{2800, utc(9, 15, 12), 37, 30, 34, 3540000},
	}
	prompts := []string{"", "add a search box to the docs sidebar"}
	for i, row := range rows {
		win := w
		win.sessionUsed, win.weekUsed, win.ctx, win.durationMs = row.session, row.week, row.ctx, row.millis
		def.steps = append(def.steps, stepDef{
			ms: row.ms, now: row.now, reply: "The docs build is green. Let me know what to tackle next.",
			prompt: prompts[i], modeLine: modeLine, windows: []window{win},
		})
	}
	return def
}

func agentJob() sceneDef {
	def := sceneDef{
		id: "agent-job", title: "Agent job", layout: LayoutTerminal, snapshot: friday,
		caption: "An agent working through a batch reports its progress, one cell per step, until the job is done.",
	}
	ids := []string{"#326", "#327", "#328", "#329", "#330"}
	const done, active, pending = progress.Settled, progress.Active, progress.Pending
	lead := Subagent{Name: "main"}
	steps := []struct {
		row       usageRow
		phase     string
		statuses  []string
		subagents []Subagent
		reply     string
		activity  string
	}{
		{
			usageRow{1800, utc(9, 11, 0), 30, 34, 31, 5400000},
			progress.Running, []string{done, done, active, pending, pending},
			[]Subagent{lead, {"ui-implementer", "Fix round 1 #328 t2"}, {"api-implementer", "Build #328 t1"}, {"quality-reviewer", "Review #327"}, {"test", "Run suite #327"}},
			"#327 passed review. Committing, then picking up #328.", "6m 12s \u00b7 \u2193 24.9k tokens",
		},
		{
			usageRow{1800, utc(9, 11, 20), 35, 35, 38, 6600000},
			progress.Running, []string{done, done, done, active, pending},
			[]Subagent{lead, {"ui-implementer", "Build #329 t1"}, {"quality-reviewer", "Review #328"}, {"scope-reviewer", "Check #328 contract"}, {"test", "Run suite #328"}},
			"#328 passed review. Committing, then picking up #329.", "9m 40s \u00b7 \u2193 38.2k tokens",
		},
		{
			usageRow{1800, utc(9, 11, 40), 39, 36, 44, 7800000},
			progress.Running, []string{done, done, done, done, active},
			[]Subagent{lead, {"api-implementer", "Build #330 t1"}, {"quality-reviewer", "Review #329"}, {"test", "Run suite #329"}},
			"#329 passed review. Committing, then picking up #330.", "13m 05s \u00b7 \u2193 51.7k tokens",
		},
		{
			usageRow{2600, utc(9, 12, 0), 43, 37, 49, 9000000},
			progress.Done, []string{done, done, done, done, done},
			[]Subagent{lead},
			"All five issues are done and committed.", "",
		},
	}
	for _, s := range steps {
		w := window{
			project: "acme-portal", branch: "dev", sessionResets: utc(9, 17, 0),
			sessionUsed: s.row.session, weekUsed: s.row.week, ctx: s.row.ctx, durationMs: s.row.millis,
			run: loopRun(s.row.now, s.phase, ids, s.statuses...),
		}
		def.steps = append(def.steps, stepDef{
			ms: s.row.ms, now: s.row.now, reply: s.reply, spinner: spin("Orchestrating", s.activity),
			prompt: "work through the open issues on the board", modeLine: agentsModeLine(len(s.subagents) - 1),
			subagents: s.subagents, windows: []window{w},
		})
	}
	return def
}

func severalWindows() sceneDef {
	def := sceneDef{
		id: "several-windows", title: "Several windows", layout: LayoutWindows, snapshot: friday,
		caption: "Every window shares the same limits, and each one shows its own project in its own color.",
	}
	ids := []string{"#41", "#42", "#43"}
	const done, active, pending = progress.Settled, progress.Active, progress.Pending
	steps := []struct {
		ms              int
		now             time.Time
		session, week   float64
		acme, field     float64
		billing, pline  float64
		paceRunStatuses []string
	}{
		{2600, utc(9, 14, 0), 58, 38.5, 4200000, 1500000, 600000, 300000, []string{done, active, pending}},
		{2600, utc(9, 14, 20), 61, 39.5, 5400000, 2700000, 1800000, 1500000, []string{done, done, active}},
	}
	for _, s := range steps {
		shared := window{sessionUsed: s.session, sessionResets: utc(9, 17, 0), weekUsed: s.week}
		acme, field, billing, pl := shared, shared, shared, shared
		acme.project, acme.branch, acme.ctx, acme.durationMs = "acme-portal", "dev", 22, s.acme
		field.project, field.branch, field.effort, field.ctx, field.durationMs = "field-app", "feat/offline", "high", 74, s.field
		billing.project, billing.branch, billing.fastMode, billing.ctx, billing.durationMs = "billing-api", "fix/webhooks", true, 40, s.billing
		pl.project, pl.branch, pl.ctx, pl.durationMs = "paceline", "dev", 15, s.pline
		pl.run = loopRun(s.now, progress.Running, ids, s.paceRunStatuses...)
		def.steps = append(def.steps, stepDef{ms: s.ms, now: s.now, windows: []window{acme, field, billing, pl}})
	}
	return def
}

func lastDay() sceneDef {
	return terminalScene(
		sceneDef{
			id: "last-day", title: "Last day", layout: LayoutTerminal, snapshot: snapshot(11, 32),
			caption: "On the last day before the weekly reset, today's budget is everything left in the week.",
		},
		window{project: "field-app", branch: "main", sessionResets: utc(11, 14, 0)},
		[]usageRow{
			{1800, utc(11, 10, 0), 3, 32.5, 9, 300000},
			{1800, utc(11, 11, 30), 31, 45, 27, 5700000},
			{2600, utc(11, 13, 0), 57, 58, 41, 11100000},
		},
		"Copy tweaks are done across all six screens.",
		"Polishing", []string{
			"17s \u00b7 \u2193 0.9k tokens", "1m 48s \u00b7 \u2193 7.3k tokens", "3m 02s \u00b7 \u2193 12.6k tokens",
		},
		"make the empty states match the new illustrations",
	)
}
