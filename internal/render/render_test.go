package render

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rogadev/paceline/internal/config"
	"github.com/rogadev/paceline/internal/pace"
	"github.com/rogadev/paceline/internal/payload"
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
	if got != "Opus \u00b7 app \u00b7 10% session" {
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
	if !strings.Contains(got, "today's") || !strings.HasPrefix(got, "app") {
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
