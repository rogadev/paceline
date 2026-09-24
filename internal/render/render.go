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
	"github.com/rogadev/paceline/internal/pace"
	"github.com/rogadev/paceline/internal/payload"
	"github.com/rogadev/paceline/internal/sanitize"
	"github.com/rogadev/paceline/internal/timefmt"
)

const (
	middleDot = "\u00b7"
	hourglass = "\u23f3"
)

var (
	arrows       = map[pace.Direction]string{pace.Up: "\u25b2", pace.Even: "\u25cf", pace.Down: "\u25bc"}
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
}

// style wraps text in ANSI SGR codes unless color is off (NO_COLOR).
type style struct{ off bool }

func (s style) wrap(code, text string) string {
	if s.off {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s style) red(t string) string    { return s.wrap("31", t) }
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
			left := round(100 - fh.UsedPercentage.V)
			when := ""
			if float64(left) < th.HeadroomGreen && fh.ResetsAt.Set {
				when = " (resets " + timefmt.Clock(fh.ResetsAt.V, ctx.Now) + ")"
			}
			parts = append(parts, headroom(left, fmt.Sprintf("%d%% session%s", left, when)))
		}
		if sd := rl.SevenDay; sd != nil && sd.UsedPercentage.Set {
			if on.Week {
				left := round(100 - sd.UsedPercentage.V)
				parts = append(parts, headroom(left, fmt.Sprintf("%d%% week", left)))
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

	if c := p.Cost; on.Duration && c != nil && c.TotalDurationMs.Set && c.TotalDurationMs.V > 0 {
		parts = append(parts, st.dim(timefmt.Duration(c.TotalDurationMs.V)))
	}

	return strings.Join(parts, " "+st.dim(middleDot)+" ")
}

func project(p *payload.Payload, ctx Context, st style) string {
	var dir, projectDir string
	if p.Workspace != nil {
		dir, projectDir = p.Workspace.CurrentDir.V, p.Workspace.ProjectDir.V
	}
	if dir == "" {
		dir = p.Cwd.V
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

	arrow, budget := arrows[r.Pace], round(r.Budget)
	// Past the budget, flip from "% left" to "% used" so the overshoot shows (102%).
	if r.Over {
		return st.red(fmt.Sprintf("%s %d%% used of today's %d%% budget", arrow, round(r.PctUsed), budget))
	}
	left := round(r.PctLeft)
	return headroom(left, fmt.Sprintf("%s %d%% left of today's %d%% budget", arrow, left, budget))
}
