// Package pace computes today's share of the weekly limit.
//
// The budget is anchored at the first render of each local day (a snapshot
// in paceline-day.json) so it stays fixed while you work instead of drifting
// with every refresh:
//
//	budget = weekly % left at day start / days from local midnight to reset
//
// Unspent budget rolls over by spreading across the remaining days, since the
// next day's budget divides whatever the week still has.
//
// Compute also reports the days left until the reset, tomorrow's budget if
// nothing more is spent today, and, on the last day, when the week runs out
// at today's pace.
//
// The exported API is a public contract: other modules, such as
// paceline-tray, import this package, so changing it is a breaking change.
package pace

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/rogadev/paceline/timefmt"
)

// EvenPace is an even share of the week: 100% / 7, the base daily budget
// that carryover raises and earlier overspending lowers.
const EvenPace = 100.0 / 7

const maxSnapshotSize = 4096

// minPaceWindow is how long a day must have run before RunsOutAt projects
// from its pace. Thirty minutes is a chosen value, not a measured one: in the
// first minutes a single burst of work swings the projection by hours, and by
// half an hour the pace is steady enough to be worth showing.
const minPaceWindow = 30 * time.Minute

// Snapshot records weekly usage at the start of a day.
type Snapshot struct {
	Date        string  `json:"date"`
	ResetsAt    float64 `json:"resetsAt"`
	UsedAtStart float64 `json:"usedAtStart"`
	// StartedAt is when the snapshot was taken, in unix seconds. Files
	// written before it was recorded read as 0, and the day starts at local
	// midnight instead.
	StartedAt float64 `json:"startedAt,omitempty"`
}

var dateKeyPattern = regexp.MustCompile(`^\d{8}$`)

// Valid reports whether s is well-formed, so a corrupt or hand-edited file is
// never trusted.
func (s *Snapshot) Valid() bool {
	return s != nil && dateKeyPattern.MatchString(s.Date) && s.UsedAtStart >= 0 && s.UsedAtStart <= 100
}

// Kind says which state today's segment is in.
type Kind int

// Result kinds.
const (
	None   Kind = iota // reset already passed: show nothing
	Budget             // a normal day with a budget
)

// Direction compares today's budget to an even pace of 100% / 7 per day.
type Direction int

// Directions.
const (
	Even Direction = iota // between 0.8x and 1.25x of an even pace
	Up                    // 1.25x an even pace or more
	Down                  // 0.8x an even pace or less
)

// Result is the computed state of today's segment.
type Result struct {
	Kind            Kind
	ResetsAt        float64
	Budget          float64 // weekly % points available today
	Spent           float64 // weekly % points spent today
	PctLeft         float64 // % of today's budget left
	PctUsed         float64 // % of today's budget used, may exceed 100
	Over            bool
	DaysLeft        float64 // days from local midnight to the reset, at least 1
	LastDay         bool    // the reset falls on or before the next local midnight
	Tomorrow        float64 // tomorrow's budget if nothing more is spent today; 0 on the last day
	RunsOutAt       float64 // unix seconds when the week reaches 100% at today's pace, or 0; see Compute
	Pace            Direction
	Snapshot        Snapshot
	SnapshotChanged bool // the caller should persist Snapshot
}

// Compute works out today's budget. snap is the stored snapshot, or nil.
//
// RunsOutAt is set on the last day only. It is 0 before the last day, with
// nothing spent, under 30 minutes after the day started, with the week
// already used up, or when the projection falls at or after the reset.
func Compute(usedPct, resetsAt float64, now time.Time, snap *Snapshot) Result {
	// A reading past 100% (or below 0) would anchor an invalid snapshot that
	// is rewritten on every refresh, and turn a used-up week into an
	// overspent day.
	usedPct = min(max(usedPct, 0), 100)
	midnight := timefmt.StartOfDay(now)
	reset := time.Unix(int64(resetsAt), 0)
	daysLeft := reset.Sub(midnight).Hours() / 24
	if daysLeft <= 0 {
		return Result{Kind: None}
	}
	// On the final partial day, everything left in the week is today's.
	daysLeft = max(daysLeft, 1)

	date := timefmt.DateKey(now)
	stale := !snap.Valid() ||
		snap.Date != date ||
		snap.ResetsAt != resetsAt ||
		usedPct < snap.UsedAtStart // the window reset under us
	current := Snapshot{Date: date, ResetsAt: resetsAt, UsedAtStart: usedPct, StartedAt: float64(now.Unix())}
	if !stale {
		current = *snap
	}

	budget := (100 - current.UsedAtStart) / daysLeft
	spent := usedPct - current.UsedAtStart
	r := Result{
		Kind:            Budget,
		ResetsAt:        resetsAt,
		Budget:          budget,
		Spent:           spent,
		Over:            spent > budget,
		DaysLeft:        daysLeft,
		LastDay:         isLastDay(reset, now),
		Snapshot:        current,
		SnapshotChanged: stale,
	}
	// Tomorrow divides what the week has left now by the days after today,
	// with the same final-day rule as today's budget. On the last day the
	// reset brings a fresh week instead, and what's worth knowing is when
	// the week runs out before it.
	if r.LastDay {
		r.RunsOutAt = runsOutAt(usedPct, spent, resetsAt, dayStart(current, midnight, now), now)
	} else {
		r.Tomorrow = max(0, 100-usedPct) / max(daysLeft-1, 1)
	}
	if budget > 0 {
		r.PctLeft = 100 * (budget - spent) / budget
		r.PctUsed = 100 * spent / budget
	}
	switch ratio := budget / EvenPace; {
	case ratio >= 1.25:
		r.Pace = Up
	case ratio <= 0.8:
		r.Pace = Down
	}
	return r
}

// isLastDay reports whether the reset falls on or before the next local
// midnight. It compares calendar days rather than counting hours, so a 23- or
// 25-hour day at a daylight saving change is still one day.
func isLastDay(reset, now time.Time) bool {
	return !reset.After(timefmt.StartOfDay(now.AddDate(0, 0, 1)))
}

// dayStart is when today's snapshot was taken, or local midnight for a
// snapshot without a start or with one outside today so far, such as a
// hand-edited file.
func dayStart(s Snapshot, midnight, now time.Time) time.Time {
	started := time.Unix(int64(s.StartedAt), 0)
	if started.Before(midnight) || started.After(now) {
		return midnight
	}
	return started
}

// runsOutAt projects when weekly usage reaches 100% if it keeps rising at the
// rate it has since start, in whole unix seconds. It returns 0 when there is
// nothing to project: nothing spent, under minPaceWindow since the start, the
// week already used up, or a projection at or past the reset.
func runsOutAt(usedPct, spent, resetsAt float64, start, now time.Time) float64 {
	elapsed := now.Sub(start)
	if spent <= 0 || usedPct >= 100 || elapsed < minPaceWindow {
		return 0
	}
	perSecond := spent / elapsed.Seconds()
	at := math.Round(float64(now.Unix()) + (100-usedPct)/perSecond)
	if at >= resetsAt {
		return 0
	}
	return at
}

// SnapshotPath is where the day snapshot lives inside configDir, the Claude
// config directory. Every reader and writer of the snapshot uses this path so
// they agree on one file.
func SnapshotPath(configDir string) string {
	return filepath.Join(configDir, "paceline-day.json")
}

// ReadSnapshot loads a snapshot, returning nil for a missing, oversized,
// corrupt, or invalid file.
func ReadSnapshot(path string) *Snapshot {
	f, err := os.Open(path) //nolint:gosec // G304: path is paceline's own state file.
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	// Reading one byte past the limit tells an oversized file apart without
	// reading all of it.
	data, err := io.ReadAll(io.LimitReader(f, maxSnapshotSize+1))
	if err != nil || len(data) > maxSnapshotSize {
		return nil
	}
	var s Snapshot
	if json.Unmarshal(data, &s) != nil || !s.Valid() {
		return nil
	}
	return &s
}

// WriteSnapshot saves s via a temp file and rename, so concurrent sessions
// never read a half-written file. Errors are returned for tests; callers may
// ignore them, since the next render retries.
func WriteSnapshot(path string, s Snapshot) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
