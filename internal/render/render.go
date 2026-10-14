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

	// Today's bar: budget left (or overage), budget spent, the part of the
	// daily base below a smaller budget, the base marker, and the bar's ends.
	barFull    = "\u2588"
	barDrained = "\u2591"
	barBelow   = "\u2592"
	barMarker  = "\u2502"
	barLeft    = "\u2595"
	barRight   = "\u258f"
	rightArrow = "\u2192"
	// maxBarCells is twice the daily base, so a big carryover late in the
	// week doesn't push the rest of the line off-screen.
	maxBarCells = 28
)

// dailyBase is the even daily budget in whole weekly points, where today's
// bar puts its marker.
var dailyBase = Round(pace.EvenPace)

// mutedRed is Windows Terminal's default red (#C50F1F) with 15% less OKLCH
// chroma, so a warning that stays on screen is easier on the eyes.
var mutedRed = color.RGB{185, 47, 47}

// faintRed is mutedRed at 60% brightness. It marks the part of the daily base
// that earlier overspending took from today's budget: where the day started,
// not a warning.
var faintRed = color.RGB{111, 28, 28}

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
func (s style) bold(t string) string   { return s.wrap("1", t) }
func (s style) rgb(c color.RGB, t string) string {
	return s.wrap(fmt.Sprintf("38;2;%d;%d;%d", c[0], c[1], c[2]), t)
}

// leaf is the last path segment, for either separator style.
func leaf(p string) string {
	return path.Base(strings.ReplaceAll(trailingSeps.ReplaceAllString(p, ""), `\`, "/"))
}

// Round rounds halves up, like JavaScript's Math.round, so output matches the
// 1.0 release exactly. math.Round rounds halves away from zero (-2.5 -> -3).
// Anything that reports a number the status line also shows rounds with it.
func Round(v float64) int { return int(math.Floor(v + 0.5)) }

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

	// Usage limits: session, today's share of the week, then the week. Each
	// counts up what's used, or down what's left when its countDirection is
	// "down". Color always follows what's left, so either way the segment
	// turns red near the limit.
	if rl := p.RateLimits; rl != nil {
		if fh := rl.FiveHour; on.Session && fh != nil && fh.UsedPercentage.Set {
			left, shown := usage(fh.UsedPercentage.V, ctx.Config.CountDown.Session, ctx)
			when := ""
			if float64(left) < th.HeadroomGreen && fh.ResetsAt.Set {
				when = " (resets " + timefmt.Clock(fh.ResetsAt.V, ctx.Now) + ")"
			}
			parts = append(parts, headroom(left, label(ctx, "s", "session")+" "+shown+when))
		}
		// Today comes before the week, and the days left follow it, so the
		// line reads from today's allowance out to the week around it. Both
		// are in weekly percentage points, so "7/12" and "w 58/100" add up.
		if sd := rl.SevenDay; sd != nil && sd.UsedPercentage.Set {
			today := pace.Result{Kind: pace.None}
			if (on.Today || on.Week) && sd.ResetsAt.Set {
				today = computeToday(sd.UsedPercentage.V, sd.ResetsAt.V, ctx)
			}
			if on.Today && today.Kind == pace.Budget {
				parts = append(parts, renderToday(today, ctx, st, headroom))
			}
			if on.Week {
				parts = append(parts, renderWeek(sd.UsedPercentage.V, today, ctx, st, headroom)...)
			}
		}
	}

	// Context-window warning, silent while there is headroom.
	if on.Context && p.ContextWindow != nil && p.ContextWindow.UsedPercentage.Set {
		used := Round(p.ContextWindow.UsedPercentage.V)
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
			text = fmt.Sprintf("cache cold: %dk @ ~2x", Round(pc.RecacheTokensIfCold.V/1000))
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

// usage returns what's left of the session limit, for its color, and
// the number to show: what's used, or what's left when counting down. A
// reading past 100% shows 100% used or 0% left, never more or negative.
func usage(usedPct float64, down bool, ctx Context) (left int, shown string) {
	used := Round(usedPct)
	left = max(0, 100-used)
	if down {
		return left, fmt.Sprintf("%d%%%s", left, label(ctx, "", " left"))
	}
	return left, fmt.Sprintf("%d%%%s", min(max(used, 0), 100), label(ctx, "", " used"))
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

// computeToday works out today's budget and saves the day snapshot when it
// changed. Render calls it whenever the today or the week segment is on, so
// the snapshot is written for a week-only line too: the week needs the day
// count, and computing it anchors the day.
func computeToday(usedPct, resetsAt float64, ctx Context) pace.Result {
	var snap *pace.Snapshot
	if ctx.ReadSnapshot != nil {
		snap = ctx.ReadSnapshot()
	}
	r := pace.Compute(usedPct, resetsAt, ctx.Now, snap)
	if r.Kind == pace.Budget && r.SnapshotChanged && ctx.WriteSnapshot != nil {
		ctx.WriteSnapshot(r.Snapshot)
	}
	return r
}

// renderToday draws today as "Today: <bar> 7/12": the bar, then the weekly
// points spent of today's budget. Counting down, the numbers show what's left
// ("5/12 left") or, past the budget, how far over ("3 over/12"). A budget
// above the even daily base adds what's left of that surplus ("+4 saved").
// Over budget, tomorrow's budget follows ("-> 11 tmrw"), unless the week has
// nothing left for it or it is the last day, when the reset brings a fresh
// week. On the last day, the time the week
// runs out at today's pace follows instead, when that comes before the reset.
func renderToday(r pace.Result, ctx Context, st style, headroom func(int, string) string) string {
	spent, budget := Round(r.Spent), Round(r.Budget)
	if r.Over {
		// A reading just past the budget can round to it; never show 12/12
		// in red.
		spent = max(spent, budget+1)
	}
	left := budget - spent
	var numbers string
	switch {
	case !ctx.Config.CountDown.Today:
		numbers = fmt.Sprintf("%d/%d", spent, budget)
	case r.Over:
		numbers = fmt.Sprintf("%d over/%d", -left, budget)
	default:
		numbers = fmt.Sprintf("%d/%d left", left, budget)
	}
	if r.Over {
		numbers = st.red(numbers)
	} else {
		numbers = headroom(Round(r.PctLeft), numbers)
	}
	parts := []string{"Today:", todayBar(budget, spent, st), numbers}
	switch tomorrow := Round(r.Tomorrow); {
	case left > dailyBase:
		parts = append(parts, st.green(fmt.Sprintf("+%d saved", left-dailyBase)))
	case r.Over && tomorrow > 0:
		parts = append(parts, st.red(fmt.Sprintf("%s %d tmrw", rightArrow, tomorrow)))
	}
	// pace sets RunsOutAt only on the last day.
	if r.RunsOutAt > 0 {
		parts = append(parts, st.red("out ~"+timefmt.Clock(r.RunsOutAt, ctx.Now)))
	}
	return strings.Join(parts, " ")
}

// todayBar draws today's budget at one cell per weekly point, against a
// marker at the even daily base. Full cells on the left are the budget left,
// drained cells the rest of the base, and a faint red stretch the part of the
// base below a smaller budget. A budget above the base starts with full cells
// past the marker, and spending removes those first, so the bar shortens.
// Over budget, the full cells are red and count the overage instead. The
// characters differ, not only the colors, so the bar reads without color. A
// bar longer than maxBarCells scales down to fit, marker included.
func todayBar(budget, spent int, st style) string {
	over := spent > budget
	full := budget - spent
	if over {
		full = spent - budget
	}
	length := max(dailyBase, full)
	scale := min(1, float64(maxBarCells)/float64(length))
	cells, marker := Round(float64(length)*scale), Round(float64(dailyBase)*scale)

	// kind classifies a cell by the weekly point at its middle.
	kind := func(i int) string {
		switch point := (float64(i) + 0.5) / scale; {
		case point < float64(full):
			return barFull
		case point < float64(min(budget, dailyBase)):
			return barDrained
		}
		return barBelow
	}
	paint := func(k string, n int) string {
		text := strings.Repeat(k, n)
		switch {
		case k == barFull && over:
			return st.red(text)
		case k == barFull:
			return st.cyan(text)
		case k == barBelow:
			return st.rgb(faintRed, text)
		}
		return st.dim(text)
	}

	var b strings.Builder
	b.WriteString(st.dim(barLeft))
	// Neighbors of the same kind share one color code; the marker splits a
	// run.
	for i := 0; i < cells; {
		j := i + 1
		for j < cells && j != marker && kind(j) == kind(i) {
			j++
		}
		b.WriteString(paint(kind(i), j-i))
		if j == marker {
			b.WriteString(barMarker)
		}
		i = j
	}
	b.WriteString(st.dim(barRight))
	return b.String()
}

// renderWeek draws the week as "w 58/100", then the days left until the
// reset. From three days out the week figure turns bold: late in the week a
// single day absorbs all the slack or debt, so today's budget can swing, and
// the week is the steadier reference.
func renderWeek(usedPct float64, today pace.Result, ctx Context, st style, headroom func(int, string) string) []string {
	used := min(max(Round(usedPct), 0), 100)
	left := 100 - used
	name := label(ctx, "w", "week")
	text := fmt.Sprintf("%s %d/100", name, used)
	if ctx.Config.CountDown.Week {
		text = fmt.Sprintf("%s %d/100 left", name, left)
	}
	if today.Kind != pace.Budget {
		return []string{headroom(left, text)}
	}
	if Round(today.DaysLeft) <= 3 {
		text = st.bold(text)
	}
	when := fmt.Sprintf("%dd left", Round(today.DaysLeft))
	if today.LastDay {
		when = "last day"
	}
	return []string{headroom(left, text), st.dim(when)}
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
