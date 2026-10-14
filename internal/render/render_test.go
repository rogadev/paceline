package render

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	// Embedded zone data, so the daylight saving test runs where the OS has
	// none, such as Windows.
	_ "time/tzdata"

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
// The usage segments' wants were later rewritten to the "s N%" session,
// the "Today: <bar> N/M left" day, and the "w N/100 left" week with its days
// left; every other byte is still 1.0's. They record the countdown display,
// so every usage segment counts down here.
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
			// The fixtures predate the session segment being off by default,
			// and none of their configs set it, so it stays on to keep the
			// session format pinned.
			cfg.Segments.Session = true
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
	got := Render(decode(t, `{"model":{"display_name":"Opus"},"rate_limits":{"seven_day":{"used_percentage":90}},
		"workspace":{"current_dir":"/r/app"}}`), ctx())
	if strings.Contains(got, "\x1b") {
		t.Errorf("escape in NO_COLOR output: %q", got)
	}
	if got != "Opus "+middleDot+" app "+middleDot+" w 90/100" {
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
	if !strings.Contains(got, " Today: ") || !strings.HasPrefix(got, "app") {
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

// withSession returns c with the session segment turned on.
func withSession(c Context) Context {
	c.Config.Segments.Session = true
	return c
}

// The session segment is off by default; segments.session turns it on,
// in the same format.
func TestSessionIsOffByDefault(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":16}}}`
	if got := Render(decode(t, in), ctx()); got != "" {
		t.Errorf("default: got %q, want no session segment", got)
	}
	if got, want := Render(decode(t, in), withSession(ctx())), "s 16%"; got != want {
		t.Errorf("session on: got %q, want %q", got, want)
	}
}

// Session counts up from 0% by default: the number is what's used. Counting
// down from 100%, it is what's left. The week reads as points of 100.
func TestSessionAndWeekCountInEitherDirection(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":16},"seven_day":{"used_percentage":4}}}`
	tests := []struct {
		name          string
		down, verbose bool
		want          string
	}{
		{name: "up", want: "s 16% " + middleDot + " w 4/100"},
		{name: "up verbose", verbose: true, want: "session 16% used " + middleDot + " week 4/100"},
		{name: "down", down: true, want: "s 84% " + middleDot + " w 96/100 left"},
		{name: "down verbose", down: true, verbose: true, want: "session 84% left " + middleDot + " week 96/100 left"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ctx()
			if tt.down {
				c = countDown()
			}
			c.Config.Verbose = tt.verbose
			if got := Render(decode(t, in), withSession(c)); got != tt.want {
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
		{name: "up", c: ctx(), want: red + "s 99%\x1b[0m" + sep + red + "w 100/100\x1b[0m"},
		{name: "down", c: countDown(), want: red + "s 1%\x1b[0m" + sep + red + "w 0/100 left\x1b[0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.NoColor = false
			if got := Render(decode(t, in), withSession(tt.c)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// bar spells a NoColor today bar in ASCII: '#' is a full cell, '.' a drained
// one, '~' the stretch below the daily base, and '|' the base marker.
func bar(cells string) string {
	glyphs := strings.NewReplacer("#", "\u2588", ".", "\u2591", "~", "\u2592", "|", "\u2502")
	return "\u2595" + glyphs.Replace(cells) + "\u258f"
}

const tmrw = " \u2192 "

// weekAt is a seven_day payload resetting days whole days after the test
// day's midnight, with used% of the week used now.
func weekAt(days int, used string) string {
	resets := time.Date(2026, 9, 24+days, 0, 0, 0, 0, time.UTC).Unix()
	return `{"rate_limits":{"seven_day":{"used_percentage":` + used + `,"resets_at":` + strconv.FormatInt(resets, 10) + `}}}`
}

// todayAt renders the today segment alone, for a day that started with
// start% of the week used, the reset days whole days after midnight, and
// used% used now. The budget is (100 - start) / days.
func todayAt(t *testing.T, c Context, start float64, days int, used string) string {
	t.Helper()
	c.Config.Segments.Week = false
	resets := time.Date(2026, 9, 24+days, 0, 0, 0, 0, time.UTC).Unix()
	snap := &pace.Snapshot{Date: "20260924", ResetsAt: float64(resets), UsedAtStart: start}
	c.ReadSnapshot = func() *pace.Snapshot { return snap }
	return Render(decode(t, weekAt(days, used)), c)
}

// Today drains: full cells are what's left of the budget, against a marker
// at the even daily base of 14 points. A budget below the base keeps the
// missing stretch as a faint red band; a budget above it starts full past
// the marker, and spending removes that surplus first. Over budget, red
// cells count the overage from the left and tomorrow's budget follows. On
// the last day, the time the week runs out at today's pace follows instead.
func TestTodayBarDrains(t *testing.T) {
	tests := []struct {
		name       string
		start      float64
		days       int
		used       string
		want, down string
	}{
		{name: "below base", start: 52, days: 4, used: "59",
			want: bar("#####.......~~|") + " 7/12", down: bar("#####.......~~|") + " 5/12 left"},
		{name: "below base, start of day", start: 52, days: 4, used: "52",
			want: bar("############~~|") + " 0/12", down: bar("############~~|") + " 12/12 left"},
		{name: "surplus, start of day", start: 28, days: 4, used: "28",
			want: bar("##############|####") + " 0/18 +4 saved", down: bar("##############|####") + " 18/18 left +4 saved"},
		{name: "surplus, partly spent", start: 28, days: 4, used: "30",
			want: bar("##############|##") + " 2/18 +2 saved", down: bar("##############|##") + " 16/18 left +2 saved"},
		{name: "surplus, all spent", start: 28, days: 4, used: "32",
			want: bar("##############|") + " 4/18", down: bar("##############|") + " 14/18 left"},
		{name: "surplus gone, base draining", start: 28, days: 4, used: "38",
			want: bar("########......|") + " 10/18", down: bar("########......|") + " 8/18 left"},
		{name: "at budget", start: 52, days: 4, used: "64",
			want: bar("............~~|") + " 12/12", down: bar("............~~|") + " 0/12 left"},
		{name: "over budget", start: 52, days: 4, used: "67",
			want: bar("###.........~~|") + " 15/12" + tmrw + "11 tmrw", down: bar("###.........~~|") + " 3 over/12" + tmrw + "11 tmrw"},
		// 12.3 spent rounds to 12, but it is over, so it reads 13.
		{name: "just over budget", start: 52, days: 4, used: "64.3",
			want: bar("#...........~~|") + " 13/12" + tmrw + "12 tmrw", down: bar("#...........~~|") + " 1 over/12" + tmrw + "12 tmrw"},
		// The same overspend costs tomorrow more late in the week, when
		// fewer days are left to absorb it.
		{name: "over early in the week", start: 28, days: 6, used: "43",
			want: bar("###.........~~|") + " 15/12" + tmrw + "11 tmrw", down: bar("###.........~~|") + " 3 over/12" + tmrw + "11 tmrw"},
		{name: "over late in the week", start: 76, days: 2, used: "91",
			want: bar("###.........~~|") + " 15/12" + tmrw + "9 tmrw", down: bar("###.........~~|") + " 3 over/12" + tmrw + "9 tmrw"},
		// A reading past 100% counts as 100%: the last 5 points are spent, not
		// overspent.
		{name: "past the limit on the last day", start: 95, days: 1, used: "100.4",
			want: bar(".....~~~~~~~~~|") + " 5/5", down: bar(".....~~~~~~~~~|") + " 0/5 left"},
		{name: "overage past the marker", start: 52, days: 4, used: "82",
			want: bar("##############|####") + " 30/12" + tmrw + "6 tmrw", down: bar("##############|####") + " 18 over/12" + tmrw + "6 tmrw"},
		// On the last day, today's pace projects when the week runs out:
		// 12 points in the 10 hours since midnight leaves 8 points, 6h40m
		// away. The day started at midnight here: the snapshot has no start.
		{name: "last day, runs out before the reset", start: 80, days: 1, used: "92",
			want: bar("########......|") + " 12/20 out ~4:40pm", down: bar("########......|") + " 8/20 left out ~4:40pm"},
		// At 1 point in 10 hours, the week outlasts the reset.
		{name: "last day, lasts until the reset", start: 80, days: 1, used: "81",
			want: bar("##############|#####") + " 1/20 +5 saved", down: bar("##############|#####") + " 19/20 left +5 saved"},
		// Past 28 cells the bar scales down, marker included.
		{name: "surplus capped at 28 cells", start: 32, days: 1, used: "33",
			want: bar("######|"+strings.Repeat("#", 22)) + " 1/68 +53 saved", down: bar("######|"+strings.Repeat("#", 22)) + " 67/68 left +53 saved"},
		// The week is used up, so there is no tomorrow worth showing.
		{name: "overage capped at 28 cells", start: 65, days: 7, used: "100",
			want: bar(strings.Repeat("#", 13)+"|"+strings.Repeat("#", 15)) + " 35/5",
			down: bar(strings.Repeat("#", 13)+"|"+strings.Repeat("#", 15)) + " 30 over/5"},
		// 28 cells fit as they are; 29 scale to 28, with the marker after
		// cell round(14 * 28/29) = 14.
		{name: "exactly 28 cells", start: 72, days: 1, used: "72",
			want: bar(strings.Repeat("#", 14)+"|"+strings.Repeat("#", 14)) + " 0/28 +14 saved",
			down: bar(strings.Repeat("#", 14)+"|"+strings.Repeat("#", 14)) + " 28/28 left +14 saved"},
		{name: "29 cells scale to 28", start: 71, days: 1, used: "71",
			want: bar(strings.Repeat("#", 14)+"|"+strings.Repeat("#", 14)) + " 0/29 +15 saved",
			down: bar(strings.Repeat("#", 14)+"|"+strings.Repeat("#", 14)) + " 29/29 left +15 saved"},
		// Overage cells cover the faint band where they reach it.
		{name: "overage into the faint band", start: 52, days: 4, used: "77",
			want: bar(strings.Repeat("#", 13)+"~|") + " 25/12" + tmrw + "8 tmrw",
			down: bar(strings.Repeat("#", 13)+"~|") + " 13 over/12" + tmrw + "8 tmrw"},
		{name: "overage over the whole faint band", start: 52, days: 4, used: "78",
			want: bar(strings.Repeat("#", 14)+"|") + " 26/12" + tmrw + "7 tmrw",
			down: bar(strings.Repeat("#", 14)+"|") + " 14 over/12" + tmrw + "7 tmrw"},
		// With the week used up, today's budget is 0: the whole base is the
		// faint band, and any spend is over.
		{name: "zero budget", start: 100, days: 4, used: "100",
			want: bar("~~~~~~~~~~~~~~|") + " 0/0", down: bar("~~~~~~~~~~~~~~|") + " 0/0 left"},
		{name: "zero budget, reading past the limit", start: 100, days: 4, used: "100.4",
			want: bar("~~~~~~~~~~~~~~|") + " 0/0", down: bar("~~~~~~~~~~~~~~|") + " 0/0 left"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, verbose := range []bool{false, true} {
				c := ctx()
				c.Config.Verbose = verbose
				if got, want := todayAt(t, c, tt.start, tt.days, tt.used), "Today: "+tt.want; got != want {
					t.Errorf("up (verbose %v):\n got  %q\n want %q", verbose, got, want)
				}
				c = countDown()
				c.Config.Verbose = verbose
				if got, want := todayAt(t, c, tt.start, tt.days, tt.used), "Today: "+tt.down; got != want {
					t.Errorf("down (verbose %v):\n got  %q\n want %q", verbose, got, want)
				}
			}
		})
	}
}

// In color, what's left is cyan and drained cells are dim; the stretch below
// the base is a faint red. The numbers take the headroom color, or red over
// budget, where the overage cells and tomorrow's budget are red too.
func TestTodayBarColors(t *testing.T) {
	const (
		dim, cyan, green = "\x1b[2m", "\x1b[36m", "\x1b[32m"
		red, faint, off  = "\x1b[38;2;185;47;47m", "\x1b[38;2;136;66;66m", "\x1b[0m"
	)
	ends := func(cells string) string { return dim + "\u2595" + off + cells + dim + "\u258f" + off }
	run := func(code, glyph string, n int) string { return code + strings.Repeat(glyph, n) + off }
	tests := []struct {
		name  string
		start float64
		days  int
		used  string
		want  string
	}{
		{name: "below base", start: 52, days: 4, used: "59",
			want: "Today: " + ends(run(cyan, "\u2588", 5)+run(dim, "\u2591", 7)+run(faint, "\u2592", 2)+"\u2502") +
				" " + green + "7/12" + off},
		{name: "surplus", start: 28, days: 4, used: "28",
			want: "Today: " + ends(run(cyan, "\u2588", 14)+"\u2502"+run(cyan, "\u2588", 4)) +
				" " + green + "0/18" + off + " " + green + "+4 saved" + off},
		{name: "runs out on the last day", start: 80, days: 1, used: "92",
			want: "Today: " + ends(run(cyan, "\u2588", 8)+run(dim, "\u2591", 6)+"\u2502") +
				" " + green + "12/20" + off + " " + red + "out ~4:40pm" + off},
		{name: "zero budget", start: 100, days: 4, used: "100",
			want: "Today: " + ends(run(faint, "\u2592", 14)+"\u2502") + " " + red + "0/0" + off},
		{name: "over budget", start: 52, days: 4, used: "67",
			want: "Today: " + ends(run(red, "\u2588", 3)+run(dim, "\u2591", 9)+run(faint, "\u2592", 2)+"\u2502") +
				" " + red + "15/12" + off + " " + red + "\u2192 11 tmrw" + off},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ctx()
			c.NoColor = false
			if got := todayAt(t, c, tt.start, tt.days, tt.used); got != tt.want {
				t.Errorf("\n got  %q\n want %q", got, tt.want)
			}
		})
	}
}

// The week reads as points of 100, then the days until the reset. From three
// days out the week figure turns bold, and on the final day it says so.
func TestWeekShowsDaysLeft(t *testing.T) {
	tests := []struct {
		name          string
		days          int
		down, verbose bool
		want          string
	}{
		{name: "four days", days: 4, want: "w 59/100 " + middleDot + " 4d left"},
		{name: "three days", days: 3, want: "w 59/100 " + middleDot + " 3d left"},
		{name: "last day", days: 1, want: "w 59/100 " + middleDot + " last day"},
		{name: "down", days: 4, down: true, want: "w 41/100 left " + middleDot + " 4d left"},
		{name: "verbose", days: 4, verbose: true, want: "week 59/100 " + middleDot + " 4d left"},
		{name: "verbose down", days: 4, down: true, verbose: true, want: "week 41/100 left " + middleDot + " 4d left"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ctx()
			if tt.down {
				c = countDown()
			}
			c.Config.Verbose = tt.verbose
			c.Config.Segments.Today = false
			if got := Render(decode(t, weekAt(tt.days, "59")), c); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWeekTurnsBoldThreeDaysOut(t *testing.T) {
	const sep = " \x1b[2m" + middleDot + "\x1b[0m "
	tests := []struct {
		days int
		want string
	}{
		{days: 4, want: "\x1b[32mw 59/100\x1b[0m" + sep + "\x1b[2m4d left\x1b[0m"},
		{days: 3, want: "\x1b[32m\x1b[1mw 59/100\x1b[0m\x1b[0m" + sep + "\x1b[2m3d left\x1b[0m"},
	}
	for _, tt := range tests {
		c := ctx()
		c.NoColor = false
		c.Config.Segments.Today = false
		if got := Render(decode(t, weekAt(tt.days, "59")), c); got != tt.want {
			t.Errorf("%d days: got %q, want %q", tt.days, got, tt.want)
		}
	}
}

// Days left round to the nearest day for the label and the bold, while the
// last day is a calendar day: a reset by the next midnight.
func TestWeekWithFractionalDaysLeft(t *testing.T) {
	const sep = " \x1b[2m" + middleDot + "\x1b[0m "
	plain, bold := "\x1b[32mw 59/100\x1b[0m", "\x1b[32m\x1b[1mw 59/100\x1b[0m\x1b[0m"
	midnight := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		resets time.Time
		want   string
	}{
		{name: "3.4 days", resets: midnight.Add(time.Duration(3.4 * 24 * float64(time.Hour))), want: bold + sep + "\x1b[2m3d left\x1b[0m"},
		{name: "3.6 days", resets: midnight.Add(time.Duration(3.6 * 24 * float64(time.Hour))), want: plain + sep + "\x1b[2m4d left\x1b[0m"},
		{name: "1.2 days", resets: midnight.Add(time.Duration(1.2 * 24 * float64(time.Hour))), want: bold + sep + "\x1b[2m1d left\x1b[0m"},
		{name: "partial last day", resets: midnight.Add(21 * time.Hour), want: bold + sep + "\x1b[2mlast day\x1b[0m"},
	}
	for _, tt := range tests {
		c := ctx()
		c.NoColor = false
		c.Config.Segments.Today = false
		in := `{"rate_limits":{"seven_day":{"used_percentage":59,"resets_at":` + strconv.FormatInt(tt.resets.Unix(), 10) + `}}}`
		if got := Render(decode(t, in), c); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

// The week needs the day count, so a week-only line still anchors the day.
func TestWeekAloneWritesTheDaySnapshot(t *testing.T) {
	c := ctx()
	c.Config.Segments.Today = false
	var written *pace.Snapshot
	c.WriteSnapshot = func(s pace.Snapshot) { written = &s }
	if got, want := Render(decode(t, weekAt(4, "59")), c), "w 59/100 "+middleDot+" 4d left"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if written == nil || written.UsedAtStart != 59 || written.Date != "20260924" || written.StartedAt != float64(c.Now.Unix()) {
		t.Errorf("snapshot written: %+v", written)
	}
}

// A day that starts with the week past 100% anchors at 100%: today reads 0/0,
// all faint band and not over, and the snapshot it writes is valid.
func TestDayStartingPastTheLimitReadsZeroOfZero(t *testing.T) {
	c := ctx()
	c.Config.Segments.Week = false
	var written *pace.Snapshot
	c.WriteSnapshot = func(s pace.Snapshot) { written = &s }
	if got, want := Render(decode(t, weekAt(4, "100.4")), c), "Today: "+bar("~~~~~~~~~~~~~~|")+" 0/0"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if written == nil || !written.Valid() || written.UsedAtStart != 100 {
		t.Errorf("snapshot written: %+v", written)
	}
}

// On a 25-hour fall-back day, a reset at the next midnight is still the last
// day: the week says so, and today shows when the week runs out, not
// tomorrow's budget. 23.5 points in the 11 hours since midnight leaves 0.5
// points, 14 minutes away.
func TestLastDayAcrossDaylightSaving(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	c := ctx()
	c.Now = time.Date(2026, 11, 1, 10, 0, 0, 0, ny)
	resets := time.Date(2026, 11, 2, 0, 0, 0, 0, ny).Unix()
	snap := &pace.Snapshot{Date: "20261101", ResetsAt: float64(resets), UsedAtStart: 76}
	c.ReadSnapshot = func() *pace.Snapshot { return snap }
	in := `{"rate_limits":{"seven_day":{"used_percentage":99.5,"resets_at":` + strconv.FormatInt(resets, 10) + `}}}`
	want := "Today: " + bar("#.............|") + " 24/23 out ~10:14am " + middleDot + " w 100/100 " + middleDot + " last day"
	if got := Render(decode(t, in), c); got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// Without a reset time there is no day count to show.
func TestWeekWithoutResetHasNoDaysLeft(t *testing.T) {
	if got, want := Render(decode(t, `{"rate_limits":{"seven_day":{"used_percentage":59}}}`), ctx()), "w 59/100"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The line reads from the session to today's allowance out to the week
// around it, then how long the week has left.
func TestUsageSegmentOrder(t *testing.T) {
	c := withSession(ctx())
	resets := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC).Unix()
	snap := &pace.Snapshot{Date: "20260924", ResetsAt: float64(resets), UsedAtStart: 52}
	c.ReadSnapshot = func() *pace.Snapshot { return snap }
	in := `{"rate_limits":{"five_hour":{"used_percentage":18},"seven_day":{"used_percentage":59,"resets_at":` +
		strconv.FormatInt(resets, 10) + `}}}`
	want := "s 18% " + middleDot + " Today: " + bar("#####.......~~|") + " 7/12 " + middleDot + " w 59/100 " + middleDot + " 4d left"
	if got := Render(decode(t, in), c); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Each usage segment takes its own direction.
func TestCountDirectionIsPerSegment(t *testing.T) {
	c := withSession(ctx())
	resets := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC).Unix()
	snap := &pace.Snapshot{Date: "20260924", ResetsAt: float64(resets), UsedAtStart: 52}
	c.ReadSnapshot = func() *pace.Snapshot { return snap }
	c.Config.Segments.Week = false
	in := `{"rate_limits":{"five_hour":{"used_percentage":16},"seven_day":{"used_percentage":59,"resets_at":` +
		strconv.FormatInt(resets, 10) + `}}}`
	tests := []struct {
		name string
		down config.CountDown
		want string
	}{
		{name: "session down", down: config.CountDown{Session: true}, want: "s 84% " + middleDot + " Today: " + bar("#####.......~~|") + " 7/12"},
		{name: "today down", down: config.CountDown{Today: true}, want: "s 16% " + middleDot + " Today: " + bar("#####.......~~|") + " 5/12 left"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.Config.CountDown = tt.down
			if got := Render(decode(t, in), c); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
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
			if got := Render(decode(t, in), withSession(tt.c)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// A reading past 100% shows 100% used or 0% left, never more or negative.
func TestUsageOverTheLimitIsCapped(t *testing.T) {
	in := `{"rate_limits":{"five_hour":{"used_percentage":100.5,"resets_at":1790251200},"seven_day":{"used_percentage":100.5}}}`
	if got, want := Render(decode(t, in), withSession(ctx())), "s 100% (resets 12pm) "+middleDot+" w 100/100"; got != want {
		t.Errorf("up: got %q, want %q", got, want)
	}
	if got, want := Render(decode(t, in), withSession(countDown())), "s 0% (resets 12pm) "+middleDot+" w 0/100 left"; got != want {
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
