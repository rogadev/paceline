package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/rogadev/paceline/internal/config"
	"github.com/rogadev/paceline/internal/feed"
	"github.com/rogadev/paceline/internal/mcp"
	"github.com/rogadev/paceline/internal/render"
	"github.com/rogadev/paceline/pace"
	"github.com/rogadev/paceline/timefmt"
)

// staleAfter matches paceline-tray's feed.DefaultMaxAge: a reading older than
// this may no longer match what Claude Code would report.
const staleAfter = 10 * time.Minute

// whenToCall opens both usage tools' descriptions, so Claude checks the budget
// before work that could use up a lot of it.
const whenToCall = "Call before starting a long or expensive task, or a batch of subagents, "

// noArgs is the input schema of a tool that takes no arguments.
var noArgs = json.RawMessage(`{"type": "object", "properties": {}}`)

// Kinds of get_today_budget answer.
const (
	kindBudget  = "budget"
	kindLastDay = "last_day"
	kindNone    = "none"
)

var paceNames = map[pace.Direction]string{pace.Up: "up", pace.Down: "down", pace.Even: "even"}

// pacePhrases compare today's budget to an even share of the week. "Up"
// means the budget is generous, not that spending is fast.
var pacePhrases = map[pace.Direction]string{
	pace.Up:   "larger than an even share of the week (100%/7 a day)",
	pace.Down: "smaller than an even share of the week (100%/7 a day)",
	pace.Even: "about an even share of the week (100%/7 a day)",
}

// freshness says how old a feed reading is. It is embedded in every answer, so
// its fields appear at the answer's top level.
type freshness struct {
	AgeSeconds int64 `json:"ageSeconds"`
	Stale      bool  `json:"stale"`
}

func newFreshness(writtenAt int64, now time.Time) freshness {
	age := now.Unix() - writtenAt
	return freshness{AgeSeconds: age, Stale: age < 0 || age > int64(staleAfter/time.Second)}
}

// lead is the line a stale answer starts with, or "" for a fresh one.
func (f freshness) lead() string {
	switch {
	case f.AgeSeconds < 0:
		return fmt.Sprintf("This reading is dated %s in the future, so your clock may have changed and it may be out of date.\n",
			timefmt.Duration(float64(-f.AgeSeconds)*1000))
	case f.Stale:
		return fmt.Sprintf("This reading is %s old, so it may be out of date; paceline updates it each time the status line refreshes.\n",
			timefmt.Duration(float64(f.AgeSeconds)*1000))
	}
	return ""
}

type windowAnswer struct {
	UsedPct     int    `json:"usedPct"`
	LeftPct     int    `json:"leftPct"`
	ResetsAt    string `json:"resetsAt"`
	ResetsClock string `json:"resetsClock"`
	ResetPassed bool   `json:"resetPassed"`
}

// usageAnswer is get_usage's fields. A window the feed lacks is null.
type usageAnswer struct {
	Session *windowAnswer `json:"session"`
	Week    *windowAnswer `json:"week"`
	freshness
}

type budgetAnswer struct {
	Kind            string `json:"kind"`
	BudgetPct       int    `json:"budgetPct"` // weekly percentage points available today
	SpentPct        int    `json:"spentPct"`  // weekly percentage points spent today
	UsedPct         int    `json:"usedPct"`   // percent of today's budget used
	LeftPct         int    `json:"leftPct"`   // percent of today's budget left
	Over            bool   `json:"over"`
	Pace            string `json:"pace"`
	WeekResetsAt    string `json:"weekResetsAt"`
	WeekResetsClock string `json:"weekResetsClock"`
	freshness
}

type lastDayAnswer struct {
	Kind            string `json:"kind"`
	WeekLeftPct     int    `json:"weekLeftPct"`
	WeekResetsAt    string `json:"weekResetsAt"`
	WeekResetsClock string `json:"weekResetsClock"`
	freshness
}

type noBudgetAnswer struct {
	Kind string `json:"kind"`
	freshness
}

func (t toolset) usageTools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name: "get_usage",
			Description: whenToCall + "or when asked about Claude usage limits, to check how much of the five-hour session " +
				"and the weekly limit is used and left and when each resets. Read-only.",
			InputSchema: noArgs,
			Call: func(json.RawMessage) (string, error) {
				return t.answer(usageFor)
			},
		},
		{
			Name: "get_today_budget",
			Description: whenToCall + "to check how much of today's Claude usage budget is left. " +
				"Returns today's share of the weekly limit as the status line shows it: percent of today's budget used and left, " +
				"whether it is over, and whether today's budget is larger or smaller than an even share of the week. Read-only.",
			InputSchema: noArgs,
			Call: func(json.RawMessage) (string, error) {
				return t.answer(func(r feed.Reading, f freshness, now time.Time) (string, any) {
					// Read only: the status line owns the anchor, and a tool call
					// must not fix today's starting point.
					return todayFor(r, f, now, pace.ReadSnapshot(pace.SnapshotPath(t.configDir)))
				})
			},
		},
	}
}

// answer reads the usage feed and returns build's summary, led by the feed's
// age when it is stale, with build's fields as a JSON object on the last line.
// A missing feed is a plain answer; an unusable one is an error.
func (t toolset) answer(build func(r feed.Reading, f freshness, now time.Time) (string, any)) (string, error) {
	r, err := feed.Read(feed.Path(t.configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return t.noFeed(), nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read the usage feed: %w; the status line rewrites it on its next refresh while the feed is on", err)
	}
	now := t.now()
	f := newFreshness(r.WrittenAt, now)
	summary, fields := build(r, f, now)
	data, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return f.lead() + summary + "\n" + string(data), nil
}

// noFeed explains why there is no feed file, telling the off setting apart
// from a feed that is on but not yet written.
func (t toolset) noFeed() string {
	if config.Load(filepath.Join(t.configDir, "paceline.json")).Feed {
		return "The usage feed is on but has no reading yet. It appears after the status line next refreshes with usage numbers."
	}
	return "The usage feed is off, so paceline has no usage reading. Run `paceline feed on`, then ask again after the status line next refreshes."
}

func usageFor(r feed.Reading, f freshness, now time.Time) (string, any) {
	a := usageAnswer{Session: windowFor(r.FiveHour, now), Week: windowFor(r.SevenDay, now), freshness: f}
	return windowSummary("Session", a.Session) + " " + windowSummary("Week", a.Week), a
}

// windowFor rounds a feed window the way the status line shows it, or returns
// nil for a window the feed lacks.
func windowFor(w *feed.Window, now time.Time) *windowAnswer {
	if w == nil {
		return nil
	}
	used := render.Round(w.UsedPct)
	return &windowAnswer{
		UsedPct:     used,
		LeftPct:     100 - used,
		ResetsAt:    rfc3339(w.ResetsAt, now),
		ResetsClock: timefmt.Clock(float64(w.ResetsAt), now),
		ResetPassed: w.ResetsAt <= now.Unix(),
	}
}

func windowSummary(name string, w *windowAnswer) string {
	switch {
	case w == nil:
		return name + ": no reading in the feed."
	case w.ResetPassed:
		return fmt.Sprintf("%s: reset at %s, so this reading (%d%% used) is out of date until Claude Code reports new usage.", name, w.ResetsClock, w.UsedPct)
	}
	return fmt.Sprintf("%s: %d%% left (%d%% used), resets %s.", name, w.LeftPct, w.UsedPct, w.ResetsClock)
}

// todayFor works out today's budget from the feed's weekly window and the
// status line's anchor, exactly as the status line's today segment does.
func todayFor(r feed.Reading, f freshness, now time.Time, snap *pace.Snapshot) (string, any) {
	week := r.SevenDay
	if week == nil {
		return "The feed has no weekly reading, so today's budget is unknown.", noBudgetAnswer{Kind: kindNone, freshness: f}
	}
	clock := timefmt.Clock(float64(week.ResetsAt), now)
	if week.ResetsAt <= now.Unix() {
		return fmt.Sprintf("The weekly limit reset at %s, so this reading is out of date and today's budget is unknown until Claude Code reports new usage.", clock),
			noBudgetAnswer{Kind: kindNone, freshness: f}
	}

	// With the reset still ahead, Compute returns LastDay or Budget, never None.
	res := pace.Compute(week.UsedPct, anchorResetsAt(week.ResetsAt, snap), now, snap)
	if res.Kind == pace.LastDay {
		left := 100 - render.Round(week.UsedPct)
		return fmt.Sprintf("Last day before the weekly reset at %s: all %d%% left in the week is today's.", clock, left),
			lastDayAnswer{Kind: kindLastDay, WeekLeftPct: left, WeekResetsAt: rfc3339(week.ResetsAt, now), WeekResetsClock: clock, freshness: f}
	}

	a := budgetAnswer{
		Kind:            kindBudget,
		BudgetPct:       render.Round(res.Budget),
		SpentPct:        render.Round(res.Spent),
		UsedPct:         render.Round(res.PctUsed),
		Over:            res.Over,
		Pace:            paceNames[res.Pace],
		WeekResetsAt:    rfc3339(week.ResetsAt, now),
		WeekResetsClock: clock,
		freshness:       f,
	}
	if res.Budget > 0 {
		a.LeftPct = max(0, 100-a.UsedPct)
	}
	return budgetSummary(a, res), a
}

func budgetSummary(a budgetAnswer, res pace.Result) string {
	if res.Budget <= 0 {
		return fmt.Sprintf("No budget today: the weekly limit was used up at the start of the day; the week resets %s.", a.WeekResetsClock)
	}
	detail := fmt.Sprintf(" Today's budget is %d%% of the week, and you have spent %d%% of the week today. That budget is %s; the week resets %s.",
		a.BudgetPct, a.SpentPct, pacePhrases[res.Pace], a.WeekResetsClock)
	if a.Over {
		return fmt.Sprintf("Over today's budget: %d%% used.", a.UsedPct) + detail
	}
	return fmt.Sprintf("%d%% of today's budget left (%d%% used).", a.LeftPct, a.UsedPct) + detail
}

// anchorResetsAt is the reset time to give pace.Compute. The status line saves
// the anchor with Claude Code's raw reset time, which can carry a fraction the
// feed truncates; passing the feed's whole second instead would read as a new
// week and re-anchor today's budget at current usage.
func anchorResetsAt(feedResetsAt int64, snap *pace.Snapshot) float64 {
	if snap != nil && int64(snap.ResetsAt) == feedResetsAt {
		return snap.ResetsAt
	}
	return float64(feedResetsAt)
}

// rfc3339 formats Unix seconds in now's time zone.
func rfc3339(sec int64, now time.Time) string {
	return time.Unix(sec, 0).In(now.Location()).Format(time.RFC3339)
}
