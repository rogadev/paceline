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
// "t N% of M%" form, all as % left; every other byte is still 1.0's.
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
	if got != "Opus "+middleDot+" app "+middleDot+" s 10%" {
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
	if !strings.Contains(got, " t 100% of ") || !strings.HasPrefix(got, "app") {
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

// Session and week count down from 100%: the number is what's left.
func TestSessionAndWeekShowWhatIsLeft(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":16},"seven_day":{"used_percentage":4}}}`
	tests := []struct {
		name    string
		verbose bool
		want    string
	}{
		{name: "regular", want: "s 84% " + middleDot + " w 96%"},
		{name: "verbose", verbose: true, want: "session 84% left " + middleDot + " week 96% left"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ctx()
			c.Config.Verbose = tt.verbose
			if got := Render(decode(t, in), c); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// Today counts down what's left of its budget, then reads as how far over it
// you are: never a negative number and never "over 0%".
func TestTodayCountsDownThenGoesOver(t *testing.T) {
	// 90% left over 8 days is an 11.25% budget, anchored at 10% used.
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
			c := ctx()
			c.Config.Verbose = verbose
			c.Config.Segments.Week = false
			snap := &pace.Snapshot{Date: "20260924", ResetsAt: 1790899200, UsedAtStart: 10}
			c.ReadSnapshot = func() *pace.Snapshot { return snap }
			in := `{"rate_limits":{"seven_day":{"used_percentage":` + tt.used + `,"resets_at":1790899200}}}`
			if got := Render(decode(t, in), c); got != want {
				t.Errorf("%s (verbose %v): got %q, want %q", tt.name, verbose, got, want)
			}
		}
	}
}

// Over budget is red, and the budget after it stays dim.
func TestTodayOverBudgetIsRed(t *testing.T) {
	c := ctx()
	c.NoColor = false
	c.Config.Segments.Week = false
	snap := &pace.Snapshot{Date: "20260924", ResetsAt: 1790899200, UsedAtStart: 10}
	c.ReadSnapshot = func() *pace.Snapshot { return snap }
	got := Render(decode(t, `{"rate_limits":{"seven_day":{"used_percentage":21.3,"resets_at":1790899200}}}`), c)
	if want := "[38;2;185;47;47mt over 1%[0m [2mof 11%[0m"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestVerboseSpellsOutLabels(t *testing.T) {
	c := ctx()
	c.Config.Verbose = true
	got := Render(decode(t, `{"rate_limits":{"five_hour":{"used_percentage":18},
		"seven_day":{"used_percentage":10,"resets_at":1790899200}}}`), c)
	if want := "session 82% left " + middleDot + " week 90% left " + middleDot + " today 100% left of 11% budget"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The reset time shows once less than headroomGreen is left, after what's left.
func TestSessionResetTimeFollowsUsage(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":80,"resets_at":1790251200}}}`
	if got, want := Render(decode(t, in), ctx()), "s 20% (resets 12pm)"; got != want {
		t.Errorf("regular: got %q, want %q", got, want)
	}
	c := ctx()
	c.Config.Verbose = true
	if got, want := Render(decode(t, in), c), "session 20% left (resets 12pm)"; got != want {
		t.Errorf("verbose: got %q, want %q", got, want)
	}
}

// A reading past 100% shows nothing left, never a negative number.
func TestUsageOverTheLimitShowsZeroLeft(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":100.5,"resets_at":1790251200},"seven_day":{"used_percentage":100.5}}}`
	if got, want := Render(decode(t, in), ctx()), "s 0% (resets 12pm) "+middleDot+" w 0%"; got != want {
		t.Errorf("got %q, want %q", got, want)
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
