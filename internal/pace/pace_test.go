package pace

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestLastDayAndPastReset(t *testing.T) {
	resets := epoch(local(2026, 9, 24, 21))
	if r := Compute(92, resets, now, nil); r.Kind != LastDay || r.ResetsAt != resets {
		t.Errorf("got %+v, want LastDay", r)
	}
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
