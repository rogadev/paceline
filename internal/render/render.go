// Package render builds the status line from a decoded payload.
package render

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rogadev/paceline/internal/color"
	"github.com/rogadev/paceline/internal/config"
	"github.com/rogadev/paceline/internal/payload"
	"github.com/rogadev/paceline/internal/progress"
	"github.com/rogadev/paceline/internal/sanitize"
	"github.com/rogadev/paceline/pace"
	"github.com/rogadev/paceline/timefmt"
)

const (
	middleDot = "\u00b7"
	hourglass = "\u23f3"
	cellFull  = "\u25b0"
	cellEmpty = "\u25b1"
	checkMark = "\u2713"
	ellipsis  = "\u2026"
	// maxCells keeps a long run from pushing the rest of the line off-screen.
	maxCells = 20
)

// mutedRed is Windows Terminal's default red (#C50F1F) with 15% less OKLCH
// chroma, so a warning that stays on screen is easier on the eyes.
var mutedRed = color.RGB{185, 47, 47}

var (
	modelSuffix  = regexp.MustCompile(`\s*\(.*\)$`)
	trailingSeps = regexp.MustCompile(`[\\/]+$`)
)

// Context is everything Render needs besides the payload. The functions are
// injected so tests can run without touching the filesystem.
type Context struct {
	Now           time.Time
	Config        config.Config
	NoColor       bool
	ReadSnapshot  func() *pace.Snapshot
	WriteSnapshot func(pace.Snapshot)
	GitBranch     func(dir string) string
	// Progress returns the progress run for the repo holding dir, or nil.
	Progress func(dir string) *progress.Run
}

// style wraps text in ANSI SGR codes unless color is off (NO_COLOR).
type style struct{ off bool }

func (s style) wrap(code, text string) string {
	if s.off {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s style) red(t string) string    { return s.rgb(mutedRed, t) }
func (s style) green(t string) string  { return s.wrap("32", t) }
func (s style) yellow(t string) string { return s.wrap("33", t) }
func (s style) cyan(t string) string   { return s.wrap("36", t) }
func (s style) dim(t string) string    { return s.wrap("2", t) }
func (s style) rgb(c color.RGB, t string) string {
	return s.wrap(fmt.Sprintf("38;2;%d;%d;%d", c[0], c[1], c[2]), t)
}

// leaf is the last path segment, for either separator style.
func leaf(p string) string {
	return path.Base(strings.ReplaceAll(trailingSeps.ReplaceAllString(p, ""), `\`, "/"))
}

// round rounds halves up, like JavaScript's Math.round, so output matches the
// 1.0 release exactly. math.Round rounds halves away from zero (-2.5 -> -3).
func round(v float64) int { return int(math.Floor(v + 0.5)) }

// Render returns the status line for p.
func Render(p *payload.Payload, ctx Context) string {
	if p == nil {
		p = &payload.Payload{}
	}
	on, th, st := ctx.Config.Segments, ctx.Config.Thresholds, style{off: ctx.NoColor}
	headroom := func(left int, text string) string {
		switch color.HeadroomLevel(float64(left), th.HeadroomGreen, th.HeadroomYellow) {
		case color.Green:
			return st.green(text)
		case color.Yellow:
			return st.yellow(text)
		default:
			return st.red(text)
		}
	}
	var parts []string

	// Model, minus any "(1M context)"-style suffix.
	if on.Model && p.Model != nil {
		if model := modelSuffix.ReplaceAllString(sanitize.Text(p.Model.DisplayName.V, 64), ""); model != "" {
			parts = append(parts, st.cyan(model))
		}
	}

	// Effort, only above the everyday levels.
	if on.Effort && p.Effort != nil {
		if effort := sanitize.Text(p.Effort.Level.V, 16); effort != "" && !slices.Contains(ctx.Config.QuietEfforts, effort) {
			parts = append(parts, st.dim(effort+" effort"))
		}
	}

	if on.FastMode && p.FastMode.V {
		parts = append(parts, st.yellow("fast mode"))
	}

	// Folder, colored by project. The color comes from the project root, so a
	// subfolder keeps its project's color.
	if on.Project {
		if segment := project(p, ctx, st); segment != "" {
			parts = append(parts, segment)
		}
	}

	// Usage limits: session, week, then today's share of the week.
	if rl := p.RateLimits; rl != nil {
		if fh := rl.FiveHour; on.Session && fh != nil && fh.UsedPercentage.Set {
			used := round(fh.UsedPercentage.V)
			left := 100 - used
			when := ""
			if float64(left) < th.HeadroomGreen && fh.ResetsAt.Set {
				when = " (resets " + timefmt.Clock(fh.ResetsAt.V, ctx.Now) + ")"
			}
			parts = append(parts, headroom(left, fmt.Sprintf("%s %d%%%s", label(ctx, "s", "session"), used, when)))
		}
		if sd := rl.SevenDay; sd != nil && sd.UsedPercentage.Set {
			if on.Week {
				used := round(sd.UsedPercentage.V)
				parts = append(parts, headroom(100-used, fmt.Sprintf("%s %d%%", label(ctx, "w", "week"), used)))
			}
			if on.Today && sd.ResetsAt.Set {
				if today := renderToday(sd.UsedPercentage.V, sd.ResetsAt.V, ctx, st, headroom); today != "" {
					parts = append(parts, today)
				}
			}
		}
	}

	// Context-window warning, silent while there is headroom.
	if on.Context && p.ContextWindow != nil && p.ContextWindow.UsedPercentage.Set {
		used := round(p.ContextWindow.UsedPercentage.V)
		switch {
		case float64(used) >= th.ContextCritical:
			parts = append(parts, st.red(fmt.Sprintf("ctx %d%%", used)))
		case float64(used) >= th.ContextWarn:
			parts = append(parts, st.yellow(fmt.Sprintf("ctx %d%%", used)))
		}
	}

	// Prompt-cache warning, only once the cache has gone cold.
	if pc := p.PromptCache; on.Cache && pc != nil && pc.ExpiresAt.Set && pc.ExpiresAt.V <= float64(ctx.Now.Unix()) {
		text := "cache cold: ~2x"
		if pc.RecacheTokensIfCold.Set {
			text = fmt.Sprintf("cache cold: %dk @ ~2x", round(pc.RecacheTokensIfCold.V/1000))
		}
		parts = append(parts, st.red(text))
	}

	if on.Progress && ctx.Progress != nil {
		if dir := workDir(p); dir != "" {
			if segment := renderProgress(ctx.Progress(dir), ctx.Now, st); segment != "" {
				parts = append(parts, segment)
			}
		}
	}

	// Duration always comes last, so it sits at the right edge.
	if c := p.Cost; on.Duration && c != nil && c.TotalDurationMs.Set && c.TotalDurationMs.V > 0 {
		parts = append(parts, st.dim(timefmt.Duration(c.TotalDurationMs.V)))
	}

	return strings.Join(parts, separator(st))
}

// label picks a usage segment's one-letter label, or its full word when the
// verbose style is on.
func label(ctx Context, short, long string) string {
	if ctx.Config.Verbose {
		return long
	}
	return short
}

// workDir is the session's current directory.
func workDir(p *payload.Payload) string {
	if p.Workspace != nil && p.Workspace.CurrentDir.V != "" {
		return p.Workspace.CurrentDir.V
	}
	return p.Cwd.V
}

func project(p *payload.Payload, ctx Context, st style) string {
	dir, projectDir := workDir(p), ""
	if p.Workspace != nil {
		projectDir = p.Workspace.ProjectDir.V
	}
	if dir == "" {
		return ""
	}
	if projectDir == "" {
		projectDir = dir
	}
	slot := color.ProjectSlot(leaf(projectDir), ctx.Config.ProjectSlots)
	segment := st.rgb(color.SlotColor(slot), sanitize.Text(leaf(dir), 64))
	if ctx.Config.Segments.Branch && ctx.GitBranch != nil {
		if branch := sanitize.Text(ctx.GitBranch(dir), 40); branch != "" {
			segment += " " + st.dim("(") + branch + st.dim(")")
		}
	}
	return segment
}

func renderToday(usedPct, resetsAt float64, ctx Context, st style, headroom func(int, string) string) string {
	var snap *pace.Snapshot
	if ctx.ReadSnapshot != nil {
		snap = ctx.ReadSnapshot()
	}
	r := pace.Compute(usedPct, resetsAt, ctx.Now, snap)
	switch r.Kind {
	case pace.None:
		return ""
	case pace.LastDay:
		return st.cyan(hourglass + " last day, resets " + timefmt.Clock(r.ResetsAt, ctx.Now))
	}
	if r.SnapshotChanged && ctx.WriteSnapshot != nil {
		ctx.WriteSnapshot(r.Snapshot)
	}

	// Usage reads as % used, like session and week, so the number only climbs
	// and the overshoot shows (118%). The budget is dim: it is fixed for the
	// day, so it is context, not a warning.
	budget := " " + st.dim(fmt.Sprintf("of %d%%%s", round(r.Budget), label(ctx, "", " budget")))
	today := fmt.Sprintf("%s %d%%", label(ctx, "t", "today"), round(r.PctUsed))
	if r.Over {
		return st.red(today) + budget
	}
	return headroom(round(r.PctLeft), today) + budget
}

func separator(st style) string { return " " + st.dim(middleDot) + " " }

// renderProgress draws a run as "name cells status": one cell per step,
// colored by its status, then the active step and how far the run has come.
func renderProgress(r *progress.Run, now time.Time, st style) string {
	if r == nil || !r.Visible(now) {
		return ""
	}
	parts := []string{sanitize.Text(r.Name, 16), progressCells(r, st)}
	switch r.Phase {
	case progress.Halted:
		parts = append(parts, st.red("halted"))
	case progress.Done:
		parts = append(parts, st.green(checkMark+" done"))
	default:
		parts = append(parts, progressStatus(r))
		if r.Paused(now) {
			parts[len(parts)-1] += st.dim(" (paused)")
		}
	}
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), " ")
}

// progressCells draws up to maxCells cells. A longer run shows a window
// around the active (or first unsettled) step, with an ellipsis on each side
// that was cut.
func progressCells(r *progress.Run, st style) string {
	steps := r.Steps
	start, end := 0, len(steps)
	if len(steps) > maxCells {
		focus := r.ActiveIndex()
		if focus < 0 {
			focus = slices.IndexFunc(steps, func(s progress.Step) bool { return s.Status == progress.Pending })
		}
		if focus < 0 {
			focus = len(steps) - 1
		}
		start = min(max(focus-maxCells/2, 0), len(steps)-maxCells)
		end = start + maxCells
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString(st.dim(ellipsis))
	}
	// Neighbors with the same status share one color code.
	for i := start; i < end; {
		j := i + 1
		for j < end && steps[j].Status == steps[i].Status {
			j++
		}
		b.WriteString(progressRun(steps[i].Status, j-i, st))
		i = j
	}
	if end < len(steps) {
		b.WriteString(st.dim(ellipsis))
	}
	return b.String()
}

// progressRun draws n cells for steps with the same status.
func progressRun(status string, n int, st style) string {
	switch status {
	case progress.Settled:
		return st.green(strings.Repeat(cellFull, n))
	case progress.Active:
		return st.cyan(strings.Repeat(cellFull, n))
	case progress.Skipped:
		return st.dim(strings.Repeat(cellFull, n))
	case progress.Blocked:
		return st.red(strings.Repeat(cellFull, n))
	}
	return st.dim(strings.Repeat(cellEmpty, n))
}

// progressStatus is the writer's label, or the active step and its stage.
// A planned run (steps with weights or stages) adds its weighted percentage,
// which moves within a step; any other run counts settled steps instead,
// unless the writer set a label of its own.
func progressStatus(r *progress.Run) string {
	text := sanitize.Text(r.Label, 24)
	if text == "" {
		if i := r.ActiveIndex(); i >= 0 {
			step := r.Steps[i]
			text = strings.TrimSpace(sanitize.Text(step.Label, 24) + " " + sanitize.Text(step.Stage, 16))
		}
	}
	switch {
	case r.Planned():
		// Never claim 100% while work remains.
		text += fmt.Sprintf(" %d%%", min(int(r.Percent()), 99))
	case r.Label == "":
		text += fmt.Sprintf(" %d/%d", r.SettledCount(), len(r.Steps))
	}
	return strings.TrimSpace(text)
}
