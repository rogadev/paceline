package render

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rogadev/paceline/internal/config"
	"github.com/rogadev/paceline/internal/payload"
	"github.com/rogadev/paceline/internal/progress"
	"github.com/rogadev/paceline/pace"
)

// The parity fixtures were generated in UTC, so every test here runs in UTC.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}

// parityCase is one sequence of renders sharing a snapshot store, recorded
// from the 1.0 JavaScript renderer with colors on.
type parityCase struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
	Steps  []struct {
		Now     time.Time       `json:"now"`
		Payload json.RawMessage `json:"payload"`
		Want    string          `json:"want"`
	} `json:"steps"`
}

var fixtureBranches = map[string]string{
	"/r/techcentral":     "dev",
	"/r/techcentral/src": "dev",
	"/r/evil":            "main\x1b]0;pwned\x07\x1b[2J\u202e\u200b\r\n\u009b",
	"/r/long":            strings.Repeat("x", 40),
}

// TestParityWithJavaScript proves the Go port prints exactly what 1.0 printed,
// ANSI codes included, across every segment and edge case in the fixtures.
// The usage segments' wants were later rewritten to the "s N%", "w N%", and
// "t N% of M%" form, all as % left; every other byte is still 1.0's. They
// record the countdown display, so every usage segment counts down here.
func TestParityWithJavaScript(t *testing.T) {
	data, err := os.ReadFile("testdata/parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []parityCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var snap *pace.Snapshot
			cfg := config.Merge(c.Config)
			cfg.CountDown = config.CountDown{Session: true, Week: true, Today: true}
			for i, step := range c.Steps {
				p, err := payload.Decode(step.Payload)
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				got := Render(p, Context{
					Now:           step.Now,
					Config:        cfg,
					ReadSnapshot:  func() *pace.Snapshot { return snap },
					WriteSnapshot: func(s pace.Snapshot) { snap = &s },
					GitBranch:     func(dir string) string { return fixtureBranches[dir] },
				})
				if got != step.Want {
					t.Errorf("step %d:\n got  %q\n want %q", i, got, step.Want)
				}
			}
		})
	}
}

func ctx() Context {
	return Context{Now: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC), Config: config.Default(), NoColor: true}
}

func decode(t *testing.T, s string) *payload.Payload {
	t.Helper()
	p, err := payload.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoColorEmitsNoEscapes(t *testing.T) {
	got := Render(decode(t, `{"model":{"display_name":"Opus"},"rate_limits":{"five_hour":{"used_percentage":90}},
		"workspace":{"current_dir":"/r/app"}}`), ctx())
	if strings.Contains(got, "\x1b") {
		t.Errorf("escape in NO_COLOR output: %q", got)
	}
	if got != "Opus "+middleDot+" app "+middleDot+" s 90%" {
		t.Errorf("got %q", got)
	}
}

func TestSegmentToggles(t *testing.T) {
	c := ctx()
	c.Config.Segments.Model = false
	c.Config.Segments.Project = false
	if got := Render(decode(t, `{"model":{"display_name":"Opus"},"workspace":{"current_dir":"/r/app"}}`), c); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestNilPayloadAndMissingCallbacks(t *testing.T) {
	c := ctx()
	if got := Render(nil, c); got != "" {
		t.Errorf("nil payload: %q", got)
	}
	// No snapshot or branch functions: renders without them.
	got := Render(decode(t, `{"workspace":{"current_dir":"/r/app"},
		"rate_limits":{"seven_day":{"used_percentage":5,"resets_at":1790899200}}}`), c)
	if !strings.Contains(got, " t 0% of ") || !strings.HasPrefix(got, "app") {
		t.Errorf("got %q", got)
	}
}

// Every byte paceline prints from outside input must be printable. With color
// off, paceline emits no escapes itself, so any control character in the output
// would have come from the payload or the repo.
var control = regexp.MustCompile("[\x00-\x1f\x7f-\u009f\u200b-\u200f\u202a-\u202e\u2066-\u2069\ufeff]")

func TestHostileInputIsInert(t *testing.T) {
	attacks := []string{
		"\x1b[2J\x1b[H", "\x1b]0;pwned\x07", "\x1b]8;;https://evil.example\x1b\\click\x1b]8;;\x1b\\",
		"\x1b]52;c;cm0gLXJmIH4=\x07", "\u009b2J", "\u202etxt.exe", "a\u200bb", "safe\rEVIL", "l1\nl2",
	}
	for _, a := range attacks {
		raw, _ := json.Marshal(map[string]any{
			"model":     map[string]string{"display_name": "Opus" + a},
			"effort":    map[string]string{"level": "max" + a},
			"workspace": map[string]string{"current_dir": "/r/app" + a, "project_dir": "/r/app" + a},
		})
		c := ctx()
		c.GitBranch = func(string) string { return "main" + a }
		got := Render(decode(t, string(raw)), c)
		if control.MatchString(got) {
			t.Errorf("attack %q leaked: %q", a, got)
		}
	}
}

func TestLongFieldsAreBounded(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"model": map[string]string{"display_name": strings.Repeat("A", 100_000)}})
	if got := Render(decode(t, string(raw)), ctx()); len([]rune(got)) > 64 {
		t.Errorf("got %d runes", len([]rune(got)))
	}
}

// countDown returns ctx() with every usage segment counting down.
func countDown() Context {
	c := ctx()
	c.Config.CountDown = config.CountDown{Session: true, Week: true, Today: true}
	return c
}

// Session and week count up from 0% by default: the number is what's used.
// Counting down from 100%, it is what's left.
func TestSessionAndWeekCountInEitherDirection(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":16},"seven_day":{"used_percentage":4}}}`
	tests := []struct {
		name          string
		down, verbose bool
		want          string
	}{
		{name: "up", want: "s 16% " + middleDot + " w 4%"},
		{name: "up verbose", verbose: true, want: "session 16% used " + middleDot + " week 4% used"},
		{name: "down", down: true, want: "s 84% " + middleDot + " w 96%"},
		{name: "down verbose", down: true, verbose: true, want: "session 84% left " + middleDot + " week 96% left"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ctx()
			if tt.down {
				c = countDown()
			}
			c.Config.Verbose = tt.verbose
			if got := Render(decode(t, in), c); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// Each usage segment takes its own direction.
func TestCountDirectionIsPerSegment(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":16},"seven_day":{"used_percentage":10,"resets_at":1790899200}}}`
	tests := []struct {
		name string
		down config.CountDown
		want string
	}{
		{name: "session down", down: config.CountDown{Session: true}, want: "s 84% " + middleDot + " w 10% " + middleDot + " t 0% of 11%"},
		{name: "week down", down: config.CountDown{Week: true}, want: "s 16% " + middleDot + " w 90% " + middleDot + " t 0% of 11%"},
		{name: "today down", down: config.CountDown{Today: true}, want: "s 16% " + middleDot + " w 10% " + middleDot + " t 100% of 11%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ctx()
			c.Config.CountDown = tt.down
			if got := Render(decode(t, in), c); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// Session and week turn red near the limit in either direction: color
// follows what's left, not the number shown.
func TestSessionAndWeekTurnRedNearTheLimit(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":99},"seven_day":{"used_percentage":100}}}`
	red, sep := "\x1b[38;2;185;47;47m", " \x1b[2m"+middleDot+"\x1b[0m "
	tests := []struct {
		name string
		c    Context
		want string
	}{
		{name: "up", c: ctx(), want: red + "s 99%\x1b[0m" + sep + red + "w 100%\x1b[0m"},
		{name: "down", c: countDown(), want: red + "s 1%\x1b[0m" + sep + red + "w 0%\x1b[0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.NoColor = false
			if got := Render(decode(t, in), tt.c); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// todayAt renders the today segment alone at a weekly usage of used. 90%
// left over 8 days is an 11.25% budget, anchored at 10% used.
func todayAt(t *testing.T, c Context, used string) string {
	t.Helper()
	c.Config.Segments.Week = false
	snap := &pace.Snapshot{Date: "20260924", ResetsAt: 1790899200, UsedAtStart: 10}
	c.ReadSnapshot = func() *pace.Snapshot { return snap }
	return Render(decode(t, `{"rate_limits":{"seven_day":{"used_percentage":`+used+`,"resets_at":1790899200}}}`), c)
}

// Today counts up what's used of its budget and keeps going past 100%, with
// no "over".
func TestTodayCountsUpPastTheBudget(t *testing.T) {
	tests := []struct {
		name    string
		used    string
		regular string
		verbose string
	}{
		{name: "start of day", used: "10", regular: "t 0% of 11%", verbose: "today 0% used of 11% budget"},
		{name: "under budget", used: "16", regular: "t 53% of 11%", verbose: "today 53% used of 11% budget"},
		{name: "at budget", used: "21.25", regular: "t 100% of 11%", verbose: "today 100% used of 11% budget"},
		// 100.4% used rounds to 100, but it is over, so it reads 101%.
		{name: "just over budget", used: "21.3", regular: "t 101% of 11%", verbose: "today 101% used of 11% budget"},
		{name: "well over budget", used: "30", regular: "t 178% of 11%", verbose: "today 178% used of 11% budget"},
	}
	for _, tt := range tests {
		for _, verbose := range []bool{false, true} {
			want := tt.regular
			if verbose {
				want = tt.verbose
			}
			c := ctx()
			c.Config.Verbose = verbose
			if got := todayAt(t, c, tt.used); got != want {
				t.Errorf("%s (verbose %v): got %q, want %q", tt.name, verbose, got, want)
			}
		}
	}
}

// Counting down, today shows what's left of its budget, then reads as how far
// over it you are: never a negative number and never "over 0%".
func TestTodayCountsDownThenGoesOver(t *testing.T) {
	tests := []struct {
		name    string
		used    string
		regular string
		verbose string
	}{
		{name: "start of day", used: "10", regular: "t 100% of 11%", verbose: "today 100% left of 11% budget"},
		{name: "under budget", used: "16", regular: "t 47% of 11%", verbose: "today 47% left of 11% budget"},
		{name: "at budget", used: "21.25", regular: "t 0% of 11%", verbose: "today 0% left of 11% budget"},
		// 100.4% used rounds to 100, but it is over, so it reads "over 1%".
		{name: "just over budget", used: "21.3", regular: "t over 1% of 11%", verbose: "today over 1% of 11% budget"},
		{name: "well over budget", used: "30", regular: "t over 78% of 11%", verbose: "today over 78% of 11% budget"},
	}
	for _, tt := range tests {
		for _, verbose := range []bool{false, true} {
			want := tt.regular
			if verbose {
				want = tt.verbose
			}
			c := countDown()
			c.Config.Verbose = verbose
			if got := todayAt(t, c, tt.used); got != want {
				t.Errorf("%s (verbose %v): got %q, want %q", tt.name, verbose, got, want)
			}
		}
	}
}

// Over budget is red in either direction, and the budget after it stays dim.
func TestTodayOverBudgetIsRed(t *testing.T) {
	tests := []struct {
		name string
		c    Context
		want string
	}{
		{name: "up", c: ctx(), want: "\x1b[38;2;185;47;47mt 101%\x1b[0m \x1b[2mof 11%\x1b[0m"},
		{name: "down", c: countDown(), want: "\x1b[38;2;185;47;47mt over 1%\x1b[0m \x1b[2mof 11%\x1b[0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.NoColor = false
			if got := todayAt(t, tt.c, "21.3"); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVerboseSpellsOutLabels(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":18},
		"seven_day":{"used_percentage":10,"resets_at":1790899200}}}`
	c := ctx()
	c.Config.Verbose = true
	if got, want := Render(decode(t, in), c), "session 18% used "+middleDot+" week 10% used "+middleDot+" today 0% used of 11% budget"; got != want {
		t.Errorf("up: got %q, want %q", got, want)
	}
	c = countDown()
	c.Config.Verbose = true
	if got, want := Render(decode(t, in), c), "session 82% left "+middleDot+" week 90% left "+middleDot+" today 100% left of 11% budget"; got != want {
		t.Errorf("down: got %q, want %q", got, want)
	}
}

// The reset time shows once less than headroomGreen is left, after the number.
func TestSessionResetTimeFollowsUsage(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":80,"resets_at":1790251200}}}`
	tests := []struct {
		name    string
		c       Context
		verbose bool
		want    string
	}{
		{name: "up", c: ctx(), want: "s 80% (resets 12pm)"},
		{name: "up verbose", c: ctx(), verbose: true, want: "session 80% used (resets 12pm)"},
		{name: "down", c: countDown(), want: "s 20% (resets 12pm)"},
		{name: "down verbose", c: countDown(), verbose: true, want: "session 20% left (resets 12pm)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.Config.Verbose = tt.verbose
			if got := Render(decode(t, in), tt.c); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// A reading past 100% shows 100% used or 0% left, never more or negative.
func TestUsageOverTheLimitIsCapped(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":100.5,"resets_at":1790251200},"seven_day":{"used_percentage":100.5}}}`
	if got, want := Render(decode(t, in), ctx()), "s 100% (resets 12pm) "+middleDot+" w 100%"; got != want {
		t.Errorf("up: got %q, want %q", got, want)
	}
	if got, want := Render(decode(t, in), countDown()), "s 0% (resets 12pm) "+middleDot+" w 0%"; got != want {
		t.Errorf("down: got %q, want %q", got, want)
	}
}

// The duration stays at the right edge, after the progress bar.
func TestDurationIsLast(t *testing.T) {
	c := ctx()
	c.Progress = func(string) *progress.Run {
		return &progress.Run{Version: progress.Version, Name: "loop", Phase: progress.Running, UpdatedAt: c.Now,
			Steps: []progress.Step{{ID: "1", Label: "#1", Status: progress.Active}}}
	}
	got := Render(decode(t, `{"workspace":{"current_dir":"/r/app"},"cost":{"total_duration_ms":60000}}`), c)
	if !strings.HasSuffix(got, middleDot+" 1m") || !strings.Contains(got, "loop") {
		t.Errorf("got %q", got)
	}
}
