package pace

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	// Embedded zone data, so the daylight saving tests run where the OS has
	// none, such as Windows.
	_ "time/tzdata"
)

func local(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, time.Local)
}

func epoch(t time.Time) float64 { return float64(t.Unix()) }

var now = local(2026, 9, 24, 10)

func TestBudgetDividesHeadroomByDaysLeft(t *testing.T) {
	r := Compute(0, epoch(local(2026, 10, 1, 0)), now, nil)
	if r.Kind != Budget || math.Abs(r.Budget-100.0/7) > 1e-9 || r.Pace != Even || r.PctLeft != 100 {
		t.Fatalf("got %+v", r)
	}
}

func TestBudgetStaysFixedWithinADay(t *testing.T) {
	resets := epoch(local(2026, 9, 27, 0))
	first := Compute(4, resets, now, nil)
	if !first.SnapshotChanged {
		t.Fatal("first render should take a snapshot")
	}
	later := Compute(17, resets, now, &first.Snapshot)
	if later.SnapshotChanged || later.Budget != first.Budget || later.Spent != 13 {
		t.Fatalf("got %+v", later)
	}
}

func TestOvershootReportsPercentUsed(t *testing.T) {
	resets := epoch(local(2026, 10, 1, 0))
	snap := &Snapshot{Date: "20260924", ResetsAt: resets, UsedAtStart: 0}
	r := Compute(20, resets, now, snap)
	if !r.Over || math.Round(r.PctUsed) != 140 {
		t.Fatalf("got %+v", r)
	}
}

// Tomorrow is what the week has left now, spread over the days after today,
// so overspending late in the week costs tomorrow more than early on. The
// last day has no tomorrow: the reset brings a fresh week.
func TestTomorrowSpreadsWhatIsLeftOverTheRemainingDays(t *testing.T) {
	tests := []struct {
		name     string
		resets   time.Time
		start    float64
		used     float64
		daysLeft float64
		tomorrow float64
	}{
		{name: "early in the week", resets: local(2026, 9, 30, 0), start: 28, used: 43, daysLeft: 6, tomorrow: 57.0 / 5},
		{name: "late in the week", resets: local(2026, 9, 26, 0), start: 76, used: 91, daysLeft: 2, tomorrow: 9},
		{name: "four days left", resets: local(2026, 9, 28, 0), start: 52, used: 67, daysLeft: 4, tomorrow: 11},
		// Tomorrow is the last, partial day: it gets everything left.
		{name: "a day and a half left", resets: local(2026, 9, 25, 12), start: 60, used: 70, daysLeft: 1.5, tomorrow: 30},
		{name: "last day", resets: local(2026, 9, 25, 0), start: 95, used: 99, daysLeft: 1, tomorrow: 0},
	}
	for _, tt := range tests {
		snap := &Snapshot{Date: "20260924", ResetsAt: epoch(tt.resets), UsedAtStart: tt.start}
		r := Compute(tt.used, epoch(tt.resets), now, snap)
		if math.Abs(r.DaysLeft-tt.daysLeft) > 1e-9 || math.Abs(r.Tomorrow-tt.tomorrow) > 1e-9 {
			t.Errorf("%s: days left %v, tomorrow %v; want %v and %v", tt.name, r.DaysLeft, r.Tomorrow, tt.daysLeft, tt.tomorrow)
		}
	}
}

// The last day is a calendar day: on a 25-hour fall-back day or a 23-hour
// spring-forward day, a reset at the next local midnight is still the last
// day, with no tomorrow.
func TestLastDayIsACalendarDay(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, ny) }
	tests := []struct {
		name     string
		now      time.Time
		resets   time.Time
		lastDay  bool
		daysLeft float64
	}{
		{name: "fall back", now: at(2026, 11, 1, 10), resets: at(2026, 11, 2, 0), lastDay: true, daysLeft: 25.0 / 24},
		{name: "spring forward", now: at(2027, 3, 14, 10), resets: at(2027, 3, 15, 0), lastDay: true, daysLeft: 1},
		{name: "fall back, two days left", now: at(2026, 11, 1, 10), resets: at(2026, 11, 3, 0), daysLeft: 49.0 / 24},
		{name: "partial last day", now: at(2026, 11, 4, 10), resets: at(2026, 11, 4, 21), lastDay: true, daysLeft: 1},
		{name: "just past the next midnight", now: at(2026, 11, 4, 10), resets: at(2026, 11, 5, 1), daysLeft: 25.0 / 24},
	}
	for _, tt := range tests {
		snap := &Snapshot{Date: tt.now.Format("20060102"), ResetsAt: epoch(tt.resets), UsedAtStart: 70}
		r := Compute(76, epoch(tt.resets), tt.now, snap)
		if r.LastDay != tt.lastDay || math.Abs(r.DaysLeft-tt.daysLeft) > 1e-9 {
			t.Errorf("%s: last day %v, days left %v; want %v and %v", tt.name, r.LastDay, r.DaysLeft, tt.lastDay, tt.daysLeft)
		}
		if wantTomorrow := 24 / max(tt.daysLeft-1, 1); tt.lastDay && r.Tomorrow != 0 || !tt.lastDay && math.Abs(r.Tomorrow-wantTomorrow) > 1e-9 {
			t.Errorf("%s: tomorrow %v", tt.name, r.Tomorrow)
		}
	}
}

// A week already at 100% leaves nothing for tomorrow, never a negative
// budget, even when the reading is past 100%.
func TestTomorrowIsNeverNegative(t *testing.T) {
	resets := epoch(local(2026, 9, 28, 0))
	r := Compute(100.5, resets, now, &Snapshot{Date: "20260924", ResetsAt: resets, UsedAtStart: 52})
	if !r.Over || r.Tomorrow != 0 {
		t.Errorf("got %+v, want over with no tomorrow", r)
	}
}

// On a 25-hour last day the budget divides by more than one day, so a day
// can go over budget with the week still short of 100%, and the run-out
// time still shows.
func TestOverBudgetLastDayStillRunsOut(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, ny)
	resets := epoch(time.Date(2026, 11, 2, 0, 0, 0, 0, ny))
	started := epoch(time.Date(2026, 11, 1, 8, 0, 0, 0, ny))
	r := Compute(99.5, resets, now, &Snapshot{Date: "20261101", ResetsAt: resets, UsedAtStart: 76, StartedAt: started})
	// 23.5 points in 2 hours leaves 0.5 points, 153 seconds away.
	if !r.Over || !r.LastDay || r.RunsOutAt != epoch(now)+153 {
		t.Errorf("got %+v, want over on the last day, running out at %v", r, epoch(now)+153)
	}
}

// A reading outside 0-100% is clamped, so a day that starts past the limit
// anchors a valid snapshot with a 0 budget and nothing spent, not an
// overspent day whose snapshot is rejected and rewritten on every refresh.
func TestUsageOutsideTheRangeIsClamped(t *testing.T) {
	resets := epoch(local(2026, 9, 28, 0))
	r := Compute(100.4, resets, now, nil)
	if !r.Snapshot.Valid() || r.Snapshot.UsedAtStart != 100 || r.Budget != 0 || r.Spent != 0 || r.Over {
		t.Errorf("past 100%%: got %+v", r)
	}
	if again := Compute(100.4, resets, now, &r.Snapshot); again.SnapshotChanged {
		t.Error("past 100%: the snapshot was rewritten on the next refresh")
	}
	if r := Compute(-1, resets, now, nil); r.Snapshot.UsedAtStart != 0 || r.Spent != 0 {
		t.Errorf("below 0%%: got %+v", r)
	}
}

func TestUnspentBudgetSpreadsOverRemainingDays(t *testing.T) {
	r := Compute(5, epoch(local(2026, 10, 1, 0)), local(2026, 9, 25, 9), nil)
	if math.Round(r.Budget) != 16 {
		t.Fatalf("budget = %v, want about 16", r.Budget)
	}
}

func TestPaceDirection(t *testing.T) {
	if r := Compute(4, epoch(local(2026, 9, 27, 9)), now, nil); r.Pace != Up {
		t.Errorf("light week: pace = %v, want Up", r.Pace)
	}
	if r := Compute(88, epoch(local(2026, 9, 26, 9)), now, nil); r.Pace != Down {
		t.Errorf("heavy week: pace = %v, want Down", r.Pace)
	}
}

func TestResnapshot(t *testing.T) {
	resets := epoch(local(2026, 9, 28, 0))
	yesterday := &Snapshot{Date: "20260923", ResetsAt: resets, UsedAtStart: 10}
	today := &Snapshot{Date: "20260924", ResetsAt: resets, UsedAtStart: 10}
	tests := map[string]Result{
		"new day":          Compute(20, resets, now, yesterday),
		"new window":       Compute(20, resets+60, now, today),
		"usage went down":  Compute(5, resets, now, today),
		"invalid snapshot": Compute(20, resets, now, &Snapshot{Date: "2026-09-24"}),
	}
	for name, r := range tests {
		if !r.SnapshotChanged {
			t.Errorf("%s: kept a stale snapshot", name)
		}
	}
}

func TestLastDayCountsDownFromEverythingLeft(t *testing.T) {
	resets := epoch(local(2026, 9, 24, 21))
	r := Compute(92, resets, now, nil)
	if r.Kind != Budget || r.Budget != 8 || r.PctLeft != 100 {
		t.Errorf("got %+v, want an 8-point budget with 100%% left", r)
	}
	if r := Compute(96, resets, now, &Snapshot{Date: "20260924", ResetsAt: resets, UsedAtStart: 92}); r.PctLeft != 50 || r.Over {
		t.Errorf("got %+v, want 50%% left", r)
	}
}

func TestPastReset(t *testing.T) {
	if r := Compute(50, epoch(local(2026, 9, 23, 0)), now, nil); r.Kind != None {
		t.Errorf("got %+v, want None", r)
	}
}

func TestFullyUsedWeekDoesNotDivideByZero(t *testing.T) {
	r := Compute(100, epoch(local(2026, 9, 28, 0)), now, nil)
	if r.PctLeft != 0 || math.IsNaN(r.PctUsed) || math.IsInf(r.PctUsed, 0) {
		t.Fatalf("got %+v", r)
	}
}

func TestSnapshotFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "day.json")
	want := Snapshot{Date: "20260924", ResetsAt: 1790838000, UsedAtStart: 5}
	if err := WriteSnapshot(path, want); err != nil {
		t.Fatal(err)
	}
	if got := ReadSnapshot(path); got == nil || *got != want {
		t.Fatalf("got %+v", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

// A new snapshot records when the day started, so today's pace has a start.
func TestNewSnapshotRecordsItsStart(t *testing.T) {
	r := Compute(20, epoch(local(2026, 9, 28, 0)), now, nil)
	if r.Snapshot.StartedAt != epoch(now) {
		t.Errorf("startedAt = %v, want %v", r.Snapshot.StartedAt, epoch(now))
	}
	kept := Compute(25, epoch(local(2026, 9, 28, 0)), now.Add(time.Hour), &r.Snapshot)
	if kept.SnapshotChanged || kept.Snapshot.StartedAt != epoch(now) {
		t.Errorf("a later render moved the start: %+v", kept.Snapshot)
	}
}

// RunsOutAt projects when the week reaches 100% at the pace since the day
// started, and only while that is before the reset.
func TestRunsOutAtProjectsTodaysPace(t *testing.T) {
	resets := epoch(local(2026, 9, 25, 0))
	at := func(h, m int) float64 { return epoch(time.Date(2026, 9, 24, h, m, 0, 0, time.Local)) }
	tests := []struct {
		name      string
		startedAt float64
		start     float64
		used      float64
		want      float64
	}{
		// 4 points in 2 hours leaves 6 points, 3 hours away.
		{name: "before the reset", startedAt: at(8, 0), start: 90, used: 94, want: at(13, 0)},
		{name: "after the reset", startedAt: at(8, 0), start: 90, used: 91, want: 0},
		{name: "exactly 30 minutes in", startedAt: at(9, 30), start: 90, used: 92, want: at(12, 0)},
		{name: "under 30 minutes in", startedAt: at(9, 45), start: 90, used: 94, want: 0},
		{name: "nothing spent", startedAt: at(8, 0), start: 90, used: 90, want: 0},
		{name: "week used up", startedAt: at(8, 0), start: 90, used: 100, want: 0},
		// An older snapshot has no start, so the day starts at midnight: 5
		// points in 10 hours leaves 5 points, 10 hours away.
		{name: "no start recorded", startedAt: 0, start: 90, used: 95, want: at(20, 0)},
		{name: "start on another day", startedAt: at(8, 0) - 24*3600, start: 90, used: 95, want: at(20, 0)},
		{name: "start in the future", startedAt: at(11, 0), start: 90, used: 95, want: at(20, 0)},
	}
	for _, tt := range tests {
		snap := &Snapshot{Date: "20260924", ResetsAt: resets, UsedAtStart: tt.start, StartedAt: tt.startedAt}
		if r := Compute(tt.used, resets, now, snap); math.Abs(r.RunsOutAt-tt.want) > 1e-6 {
			t.Errorf("%s: runs out at %v, want %v", tt.name, r.RunsOutAt, tt.want)
		}
	}

	// 4 points in 2 hours leaves 22 points, 11 hours away: exactly at a 9pm
	// reset, which is no warning.
	ninePM := epoch(local(2026, 9, 24, 21))
	snap := &Snapshot{Date: "20260924", ResetsAt: ninePM, UsedAtStart: 74, StartedAt: at(8, 0)}
	if r := Compute(78, ninePM, now, snap); r.RunsOutAt != 0 {
		t.Errorf("a projection at the reset: runs out at %v, want 0", r.RunsOutAt)
	}

	// Before the last day there is no run-out time, however fast the pace.
	later := epoch(local(2026, 9, 26, 0))
	snap = &Snapshot{Date: "20260924", ResetsAt: later, UsedAtStart: 60, StartedAt: at(8, 0)}
	if r := Compute(90, later, now, snap); r.RunsOutAt != 0 {
		t.Errorf("two days out: runs out at %v, want 0", r.RunsOutAt)
	}
}

func TestSnapshotFileKeepsTheStartAndReadsOlderFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "day.json")
	want := Snapshot{Date: "20260924", ResetsAt: 1790838000, UsedAtStart: 5, StartedAt: 1790841600}
	if err := WriteSnapshot(path, want); err != nil {
		t.Fatal(err)
	}
	if got := ReadSnapshot(path); got == nil || *got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if err := os.WriteFile(path, []byte(`{"date":"20260924","resetsAt":1790838000,"usedAtStart":5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadSnapshot(path); got == nil || got.StartedAt != 0 || got.UsedAtStart != 5 {
		t.Errorf("an older file without startedAt: got %+v", got)
	}
}

func TestSnapshotFileRejectsBadState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "day.json")
	for _, text := range []string{
		"{not json",
		`{"date":"2026-09-24","resetsAt":1,"usedAtStart":5}`,
		`{"date":"20260924","resetsAt":"1","usedAtStart":5}`,
		`{"date":"20260924","resetsAt":1,"usedAtStart":500}`,
		`{"date":"20260924","resetsAt":1,"usedAtStart":5,"pad":"` + strings.Repeat("x", 5000) + `"}`,
	} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ReadSnapshot(path); got != nil {
			t.Errorf("accepted %.40q", text)
		}
	}
	if ReadSnapshot(filepath.Join(dir, "missing.json")) != nil {
		t.Error("missing file returned a snapshot")
	}
	if WriteSnapshot(filepath.Join(dir, "no", "such", "dir.json"), Snapshot{}) == nil {
		t.Error("write into a missing directory reported success")
	}
}

func TestSnapshotFileFailedRenameRemovesTemp(t *testing.T) {
	// A non-empty directory at the target makes the rename fail on every OS.
	path := filepath.Join(t.TempDir(), "day.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if WriteSnapshot(path, Snapshot{}) == nil {
		t.Error("rename onto a non-empty directory reported success")
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %s (stat err %v)", tmp, err)
	}
}

func TestSnapshotPath(t *testing.T) {
	dir := filepath.Join("home", ".claude")
	if got, want := SnapshotPath(dir), filepath.Join(dir, "paceline-day.json"); got != want {
		t.Errorf("SnapshotPath(%q) = %q, want %q", dir, got, want)
	}
}
