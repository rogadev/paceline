// Package pace computes today's share of the weekly limit, as a countdown.
//
// The budget is anchored at the first render of each local day (a snapshot
// in paceline-day.json) so it stays fixed while you work instead of drifting
// with every refresh:
//
//	budget = weekly % left at day start / days from local midnight to reset
//
// Unspent budget rolls over by spreading across the remaining days, since the
// next day's budget divides whatever the week still has.
package pace

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/rogadev/paceline/internal/timefmt"
)

const (
	evenPace        = 100.0 / 7
	maxSnapshotSize = 4096
)

// Snapshot records weekly usage at the start of a day.
type Snapshot struct {
	Date        string  `json:"date"`
	ResetsAt    float64 `json:"resetsAt"`
	UsedAtStart float64 `json:"usedAtStart"`
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
	None    Kind = iota // reset already passed: show nothing
	LastDay             // the final partial day before the reset
	Budget              // a normal day with a budget
)

// Direction compares today's budget to an even pace of 100% / 7 per day.
type Direction int

// Directions.
const (
	Even Direction = iota
	Up
	Down
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
	Pace            Direction
	Snapshot        Snapshot
	SnapshotChanged bool // the caller should persist Snapshot
}

// Compute works out today's budget. snap is the stored snapshot, or nil.
func Compute(usedPct, resetsAt float64, now time.Time, snap *Snapshot) Result {
	midnight := timefmt.StartOfDay(now)
	daysLeft := time.Unix(int64(resetsAt), 0).Sub(midnight).Hours() / 24
	if daysLeft <= 0 {
		return Result{Kind: None}
	}
	// Final partial day: everything left in the week is today's.
	if daysLeft <= 1 {
		return Result{Kind: LastDay, ResetsAt: resetsAt}
	}

	date := timefmt.DateKey(now)
	stale := !snap.Valid() ||
		snap.Date != date ||
		snap.ResetsAt != resetsAt ||
		usedPct < snap.UsedAtStart // the window reset under us
	current := Snapshot{Date: date, ResetsAt: resetsAt, UsedAtStart: usedPct}
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
		Snapshot:        current,
		SnapshotChanged: stale,
	}
	if budget > 0 {
		r.PctLeft = 100 * (budget - spent) / budget
		r.PctUsed = 100 * spent / budget
	}
	switch ratio := budget / evenPace; {
	case ratio >= 1.25:
		r.Pace = Up
	case ratio <= 0.8:
		r.Pace = Down
	}
	return r
}

// ReadSnapshot loads a snapshot, returning nil for a missing, oversized,
// corrupt, or invalid file.
func ReadSnapshot(path string) *Snapshot {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is paceline's own state file.
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
