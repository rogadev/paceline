package main

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	middleDot = "\u00b7"
	checkMark = "\u2713"
	// mutedRed is the renderer's red, as a truecolor run.
	mutedRed = "#b92f2f"
)

// sgr matches the escape sequences the renderer emits, to strip them
// independently of ParseANSI.
var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func buildDoc(t *testing.T) *Document {
	t.Helper()
	doc, err := BuildDocument()
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func sceneByID(t *testing.T, doc *Document, id string) Scene {
	t.Helper()
	for _, s := range doc.Scenes {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no scene %q", id)
	return Scene{}
}

// line is a terminal step's only status line.
func line(t *testing.T, step Step) StatusLine {
	t.Helper()
	if len(step.StatusLines) != 1 {
		t.Fatalf("step at %s has %d status lines, want 1", step.Now, len(step.StatusLines))
	}
	return step.StatusLines[0]
}

// runStarting is the first run whose text starts with prefix.
func runStarting(t *testing.T, sl StatusLine, prefix string) Run {
	t.Helper()
	for _, r := range sl.Runs {
		if strings.HasPrefix(r.Text, prefix) {
			return r
		}
	}
	t.Fatalf("no run starts with %q in %q", prefix, sl.Plain)
	return Run{}
}

func hasRunStarting(sl StatusLine, prefix string) bool {
	return slices.ContainsFunc(sl.Runs, func(r Run) bool { return strings.HasPrefix(r.Text, prefix) })
}

func TestDocumentShape(t *testing.T) {
	doc := buildDoc(t)
	if doc.Version != 1 || doc.Columns != 110 {
		t.Errorf("version %d, columns %d", doc.Version, doc.Columns)
	}
	var ids []string
	for _, s := range doc.Scenes {
		ids = append(ids, s.ID)
	}
	want := []string{"fresh-day", "working-day", "over-budget", "session-low", "cache-cold", "agent-job", "several-windows", "last-day"}
	if !slices.Equal(ids, want) {
		t.Fatalf("scene ids %v, want %v", ids, want)
	}
	for _, s := range doc.Scenes {
		if s.Title == "" || s.Caption == "" {
			t.Errorf("%s: missing title or caption", s.ID)
		}
		total := 0
		for i, step := range s.Steps {
			total += step.DurationMs
			now, err := time.Parse(time.RFC3339, step.Now)
			if err != nil || now.Location() != time.UTC || !strings.HasSuffix(step.Now, "Z") {
				t.Errorf("%s step %d: now %q is not RFC3339 UTC", s.ID, i+1, step.Now)
			}
			switch s.Layout {
			case LayoutTerminal:
				if len(step.StatusLines) != 1 || step.Prompt == nil || step.ModeLine == "" {
					t.Errorf("%s step %d: terminal step needs one status line, a prompt, and a mode line", s.ID, i+1)
				}
			case LayoutWindows:
				if step.Prompt != nil || step.ModeLine != "" || step.Reply != "" || step.Spinner != nil || step.Subagents != nil {
					t.Errorf("%s step %d: a windows step carries only status lines", s.ID, i+1)
				}
			default:
				t.Errorf("%s: layout %q", s.ID, s.Layout)
			}
			if len(step.Subagents) > 0 && (step.Subagents[0].Name != "main" || step.Subagents[0].Task != "") {
				t.Errorf("%s step %d: first subagent is %+v, want the main agent", s.ID, i+1, step.Subagents[0])
			}
			for _, sl := range step.StatusLines {
				if sl.Plain != sgr.ReplaceAllString(sl.ANSI, "") {
					t.Errorf("%s step %d: plain %q does not match the stripped ANSI", s.ID, i+1, sl.Plain)
				}
				if !strings.HasPrefix(sl.Plain, "Opus 5.5 "+middleDot+" ") {
					t.Errorf("%s step %d: %q does not start with the model", s.ID, i+1, sl.Plain)
				}
			}
		}
		if s.DurationMs != total || total == 0 {
			t.Errorf("%s: durationMs %d, steps sum to %d", s.ID, s.DurationMs, total)
		}
	}
}

func TestWorkingDayTodayGoesGreenYellowRed(t *testing.T) {
	var colors []string
	for _, step := range sceneByID(t, buildDoc(t), "working-day").Steps {
		c := runStarting(t, line(t, step), "t ").Color
		if len(colors) == 0 || colors[len(colors)-1] != c {
			colors = append(colors, c)
		}
	}
	if want := []string{"green", "yellow", mutedRed}; !slices.Equal(colors, want) {
		t.Errorf("today's colors %v, want %v", colors, want)
	}
}

func TestOverBudgetCountsPastTheBudgetInRed(t *testing.T) {
	for i, step := range sceneByID(t, buildDoc(t), "over-budget").Steps {
		sl := line(t, step)
		r := runStarting(t, sl, "t ")
		var used int
		if _, err := fmt.Sscanf(r.Text, "t %d%%", &used); err != nil || used <= 100 {
			t.Errorf("step %d: %q is not past 100%%", i+1, r.Text)
		}
		if r.Color != mutedRed {
			t.Errorf("step %d: %q is %q, want red", i+1, r.Text, r.Color)
		}
		if r := runStarting(t, sl, "of 28%"); !r.Dim {
			t.Errorf("step %d: the budget is not dim", i+1)
		}
	}
}

func TestSessionLowShowsResetAndContext(t *testing.T) {
	steps := sceneByID(t, buildDoc(t), "session-low").Steps
	wantCtx := []string{"", "yellow", mutedRed}
	for i, step := range steps {
		sl := line(t, step)
		resets := strings.Contains(sl.Plain, "(resets 3:40pm)")
		if resets != (i > 0) {
			t.Errorf("step %d: shows the reset time = %v: %q", i+1, resets, sl.Plain)
		}
		if !hasRunStarting(sl, "ctx ") {
			if wantCtx[i] != "" {
				t.Errorf("step %d: no ctx segment in %q", i+1, sl.Plain)
			}
			continue
		}
		if wantCtx[i] == "" {
			t.Errorf("step %d: ctx shown under the warning threshold: %q", i+1, sl.Plain)
		} else if c := runStarting(t, sl, "ctx ").Color; c != wantCtx[i] {
			t.Errorf("step %d: ctx color %q, want %q", i+1, c, wantCtx[i])
		}
	}
}

func TestCacheColdWarnsOnlyOnceCold(t *testing.T) {
	for i, step := range sceneByID(t, buildDoc(t), "cache-cold").Steps {
		sl := line(t, step)
		cold := hasRunStarting(sl, "cache cold: 82k @ ~2x")
		if cold != (i == 1) {
			t.Errorf("step %d: cache warning shown = %v: %q", i+1, cold, sl.Plain)
		}
		if cold && runStarting(t, sl, "cache cold").Color != mutedRed {
			t.Error("the cache warning is not red")
		}
		if step.Spinner != nil {
			t.Errorf("step %d: an idle session has no spinner", i+1)
		}
	}
}

func TestAgentJobEndsDone(t *testing.T) {
	steps := sceneByID(t, buildDoc(t), "agent-job").Steps
	for i, step := range steps {
		sl := line(t, step)
		last := i == len(steps)-1
		if done := strings.Contains(sl.Plain, checkMark+" done"); done != last {
			t.Errorf("step %d: shows done = %v: %q", i+1, done, sl.Plain)
		}
		if !strings.Contains(sl.Plain, " loop ") {
			t.Errorf("step %d: no progress segment: %q", i+1, sl.Plain)
		}
		agents := len(step.Subagents) - 1
		tail := strings.Contains(step.ModeLine, "/tasks")
		if tail != (agents > 0) || (agents > 0 && !strings.HasSuffix(step.ModeLine, " "+string(rune('0'+agents))+" agents")) {
			t.Errorf("step %d: mode line %q with %d agents", i+1, step.ModeLine, agents)
		}
	}
	final := line(t, steps[len(steps)-1])
	if !strings.HasSuffix(strings.TrimSuffix(final.Plain, " "+middleDot+" 2h30m"), checkMark+" done") {
		t.Errorf("the job does not end on done: %q", final.Plain)
	}
	if runStarting(t, final, checkMark).Color != "green" {
		t.Error("done is not green")
	}
	if steps[len(steps)-1].Spinner != nil {
		t.Error("the finished job still has a spinner")
	}
}

func TestSeveralWindowsShareLimitsInTheirOwnColors(t *testing.T) {
	projects := []string{"acme-portal", "field-app", "billing-api", "paceline"}
	for i, step := range sceneByID(t, buildDoc(t), "several-windows").Steps {
		if len(step.StatusLines) != len(projects) {
			t.Fatalf("step %d: %d status lines, want %d", i+1, len(step.StatusLines), len(projects))
		}
		colors := map[string]bool{}
		var usage []string
		for j, sl := range step.StatusLines {
			r := runStarting(t, sl, projects[j])
			if !strings.HasPrefix(r.Color, "#") {
				t.Errorf("step %d: %s has no project color", i+1, projects[j])
			}
			colors[r.Color] = true
			start := strings.Index(sl.Plain, " s ")
			end := strings.Index(sl.Plain, " of 28%")
			if start < 0 || end < start {
				t.Fatalf("step %d: no usage segments in %q", i+1, sl.Plain)
			}
			usage = append(usage, sl.Plain[start:end])
		}
		if len(colors) != len(projects) {
			t.Errorf("step %d: %d distinct project colors, want %d", i+1, len(colors), len(projects))
		}
		for _, u := range usage[1:] {
			if u != usage[0] {
				t.Errorf("step %d: windows disagree on the limits: %q vs %q", i+1, u, usage[0])
			}
		}
	}
}

func TestLastDayBudgetIsTheRestOfTheWeek(t *testing.T) {
	for i, step := range sceneByID(t, buildDoc(t), "last-day").Steps {
		if sl := line(t, step); !strings.Contains(sl.Plain, " of 68%") {
			t.Errorf("step %d: %q", i+1, sl.Plain)
		}
	}
}

func TestOutputIsDeterministic(t *testing.T) {
	first, err := outputs(buildDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	// A far-off local zone must not change anything: every time is UTC.
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	time.Local = time.FixedZone("UTC+13", 13*3600)
	second, err := outputs(buildDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("%d outputs, then %d", len(first), len(second))
	}
	for path, data := range first {
		if !bytes.Equal(data, second[path]) {
			t.Errorf("%s differs between two builds", path)
		}
	}
}

func TestScenesJSONEncoding(t *testing.T) {
	files, err := outputs(buildDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	data := files[ScenesPath]
	switch {
	case !bytes.HasPrefix(data, []byte("{\n  \"version\": 1,\n  \"columns\": 110,\n")):
		t.Errorf("unexpected start: %.60q", data)
	case !bytes.HasSuffix(data, []byte("}\n")) || bytes.HasSuffix(data, []byte("\n\n")):
		t.Error("want exactly one trailing newline")
	case bytes.Contains(data, []byte("\r")):
		t.Error("CR in the output")
	case !bytes.Contains(data, []byte(middleDot)):
		t.Error("glyphs are escaped instead of raw UTF-8")
	case bytes.Contains(data, []byte(`\`+"u003e")) || bytes.Contains(data, []byte(`\`+"u0026")):
		t.Error("HTML escaping is on")
	}
}

// TestCommittedOutputsAreCurrent fails when a renderer or scene change was
// not followed by regenerating the committed files.
func TestCommittedOutputsAreCurrent(t *testing.T) {
	files, err := outputs(buildDoc(t))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range files {
		got, err := readRepoFile(path)
		if err != nil {
			t.Errorf("%s: %v; run `go run ./tools/demo` from the repo root", path, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run `go run ./tools/demo` from the repo root and commit the result", path)
		}
	}
}
