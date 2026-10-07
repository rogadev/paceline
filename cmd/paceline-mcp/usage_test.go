package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rogadev/paceline/internal/config"
	"github.com/rogadev/paceline/internal/feed"
	"github.com/rogadev/paceline/internal/payload"
	"github.com/rogadev/paceline/internal/render"
	"github.com/rogadev/paceline/pace"
	"github.com/rogadev/paceline/timefmt"
)

var (
	// weekReset is Thu 9pm, 2.875 days after midnight on now's day, so the
	// budget kind is reached.
	weekReset    = time.Date(2026, 10, 8, 21, 0, 0, 0, time.UTC)
	sessionReset = time.Date(2026, 10, 6, 15, 30, 0, 0, time.UTC)
	today        = timefmt.DateKey(now)
)

// usageTools returns a toolset that reads from a fresh config directory.
func usageTools(t *testing.T) (toolset, string) {
	t.Helper()
	dir := t.TempDir()
	return toolset{now: func() time.Time { return now }, configDir: dir}, dir
}

// writeFile writes data to path, failing the test on error.
func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// feedJSON is a version 1 feed with the given windows (JSON objects, or ""
// to leave a window out) written at writtenAt.
func feedJSON(fiveHour, sevenDay string, writtenAt time.Time) string {
	s := `{"version":1`
	if fiveHour != "" {
		s += `,"fiveHour":` + fiveHour
	}
	if sevenDay != "" {
		s += `,"sevenDay":` + sevenDay
	}
	return s + fmt.Sprintf(`,"writtenAt":%d}`, writtenAt.Unix())
}

func windowJSON(used float64, resets time.Time) string {
	return fmt.Sprintf(`{"usedPct":%v,"resetsAt":%d}`, used, resets.Unix())
}

// answer splits a tool's text into its summary and the JSON object on its
// last line.
func answer(t *testing.T, text string) (string, map[string]any) {
	t.Helper()
	i := strings.LastIndex(text, "\n")
	if i < 0 {
		t.Fatalf("no JSON line in %q", text)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(text[i+1:]), &fields); err != nil {
		t.Fatalf("last line is not a JSON object: %v\n%s", err, text)
	}
	return text[:i], fields
}

// callOK calls a tool that must succeed and splits its answer.
func callOK(t *testing.T, ts toolset, name string) (string, map[string]any) {
	t.Helper()
	text, failed := call(t, ts, name, `{}`)
	if failed {
		t.Fatalf("%s failed: %s", name, text)
	}
	return answer(t, text)
}

func TestUsageToolsAreListed(t *testing.T) {
	var out, errOut bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	if code := run(nil, in, &out, &errOut, testTools("")); code != 0 {
		t.Fatalf("serve: code %d, stderr %q", code, errOut.String())
	}
	var resp struct {
		Result struct {
			Tools []struct{ Name, Description string }
		}
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"get_usage", "get_today_budget"} {
		i := slices.IndexFunc(resp.Result.Tools, func(tool struct{ Name, Description string }) bool { return tool.Name == name })
		if i < 0 {
			t.Errorf("tools/list has no %s", name)
			continue
		}
		if d := resp.Result.Tools[i].Description; !strings.Contains(d, "Call before starting a long or expensive task, or a batch of subagents") {
			t.Errorf("%s description does not say when to call it: %q", name, d)
		}
	}
}

func TestTodayBudgetMatchesTheStatusLine(t *testing.T) {
	// The status line saves the anchor with the payload's raw reset time,
	// fraction included; the feed truncates it.
	fractional := fmt.Sprintf("%d.7", weekReset.Unix())
	whole := strconv.FormatInt(weekReset.Unix(), 10)
	tests := []struct {
		name        string
		anchorStart float64 // usedAtStart, or -1 for no anchor
		used        float64
		resets      string
		pace        string
	}{
		{name: "under budget, fractional reset", anchorStart: 40, used: 46.5, resets: fractional, pace: "up"},
		{name: "over budget", anchorStart: 40, used: 70, resets: fractional, pace: "up"},
		{name: "below an even pace", anchorStart: 80, used: 82, resets: whole, pace: "down"},
		{name: "about an even pace", anchorStart: 60, used: 61.5, resets: whole, pace: "even"},
		{name: "week used up", anchorStart: 100, used: 100, resets: whole, pace: "down"},
		{name: "no anchor yet", anchorStart: -1, used: 46.5, resets: fractional, pace: "up"},
	}
	statusToday := regexp.MustCompile(`t (\d+)% of (\d+)%`)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, dir := usageTools(t)
			anchor := pace.SnapshotPath(dir)
			if tt.anchorStart >= 0 {
				resets, _ := strconv.ParseFloat(tt.resets, 64)
				if err := pace.WriteSnapshot(anchor, pace.Snapshot{Date: today, ResetsAt: resets, UsedAtStart: tt.anchorStart}); err != nil {
					t.Fatal(err)
				}
			}
			p, err := payload.Decode([]byte(fmt.Sprintf(`{"rate_limits":{"seven_day":{"used_percentage":%v,"resets_at":%s}}}`, tt.used, tt.resets)))
			if err != nil {
				t.Fatal(err)
			}
			if err := feed.Write(feed.Path(dir), p, now); err != nil {
				t.Fatal(err)
			}

			// The tool runs first, so a missing anchor is still missing for it.
			_, got := callOK(t, ts, "get_today_budget")

			line := render.Render(p, render.Context{
				Now:           now,
				Config:        config.Default(),
				NoColor:       true,
				ReadSnapshot:  func() *pace.Snapshot { return pace.ReadSnapshot(anchor) },
				WriteSnapshot: func(s pace.Snapshot) { _ = pace.WriteSnapshot(anchor, s) },
			})
			m := statusToday.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("the status line has no today segment: %q", line)
			}
			if got["kind"] != "budget" {
				t.Fatalf("kind = %v, want budget; answer %v", got["kind"], got)
			}
			if fmt.Sprint(got["usedPct"]) != m[1] || fmt.Sprint(got["budgetPct"]) != m[2] {
				t.Errorf("tool says %v%% of %v%%, status line says %s", got["usedPct"], got["budgetPct"], m[0])
			}
			if got["pace"] != tt.pace {
				t.Errorf("pace = %v, want %s", got["pace"], tt.pace)
			}
		})
	}
}

func TestTodayBudgetAnswers(t *testing.T) {
	sixAM := time.Date(2026, 10, 6, 6, 0, 0, 0, time.UTC)
	eightPM := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		anchor   string // anchor file contents, or "" for none
		sevenDay string
		kind     string
		summary  []string // phrases the summary must contain
		absent   []string // phrases it must not contain
		fields   map[string]any
	}{
		{
			name:     "under budget",
			anchor:   fmt.Sprintf(`{"date":%q,"resetsAt":%d,"usedAtStart":40}`, today, weekReset.Unix()),
			sevenDay: windowJSON(46.5, weekReset), kind: "budget",
			summary: []string{
				"69% of today's budget left (31% used).", "Today's budget is 21% of the week, and you have spent 7% of the week today.",
				"That budget is larger than an even share of the week (100%/7 a day); the week resets Thu 9pm.",
			},
			fields: map[string]any{
				"budgetPct": 21.0, "spentPct": 7.0, "usedPct": 31.0, "leftPct": 69.0, "over": false, "pace": "up",
				"weekResetsAt": "2026-10-08T21:00:00Z", "weekResetsClock": "Thu 9pm",
			},
		},
		{
			name:     "over budget",
			anchor:   fmt.Sprintf(`{"date":%q,"resetsAt":%d,"usedAtStart":40}`, today, weekReset.Unix()),
			sevenDay: windowJSON(70, weekReset), kind: "budget",
			summary: []string{"Over today's budget: 144% used.", "Today's budget is 21% of the week, and you have spent 30% of the week today."},
			absent:  []string{"left"},
			fields:  map[string]any{"usedPct": 144.0, "leftPct": 0.0, "over": true},
		},
		{
			name:     "smaller than an even share",
			anchor:   fmt.Sprintf(`{"date":%q,"resetsAt":%d,"usedAtStart":80}`, today, weekReset.Unix()),
			sevenDay: windowJSON(82, weekReset), kind: "budget",
			summary: []string{"71% of today's budget left (29% used).", "That budget is smaller than an even share of the week (100%/7 a day)"},
			fields:  map[string]any{"budgetPct": 7.0, "pace": "down"},
		},
		{
			name:     "about an even share",
			anchor:   fmt.Sprintf(`{"date":%q,"resetsAt":%d,"usedAtStart":60}`, today, weekReset.Unix()),
			sevenDay: windowJSON(61.5, weekReset), kind: "budget",
			summary: []string{"89% of today's budget left (11% used).", "That budget is about an even share of the week (100%/7 a day)"},
			fields:  map[string]any{"budgetPct": 14.0, "pace": "even"},
		},
		{
			name:     "week used up",
			anchor:   fmt.Sprintf(`{"date":%q,"resetsAt":%d,"usedAtStart":100}`, today, weekReset.Unix()),
			sevenDay: windowJSON(100, weekReset), kind: "budget",
			summary: []string{"No budget today", "the week resets Thu 9pm"},
			absent:  []string{"left"},
			fields:  map[string]any{"budgetPct": 0.0, "usedPct": 0.0, "leftPct": 0.0, "over": false},
		},
		{
			name:     "last day",
			sevenDay: windowJSON(46.5, eightPM), kind: "last_day",
			summary: []string{"Last day before the weekly reset at 8pm", "all 53% left in the week is today's"},
			fields:  map[string]any{"weekLeftPct": 53.0, "weekResetsClock": "8pm"},
		},
		{
			name:     "reset already passed",
			sevenDay: windowJSON(46.5, sixAM), kind: "none",
			summary: []string{"The weekly limit reset at 6am, so this reading is out of date and today's budget is unknown until Claude Code reports new usage."},
		},
		{
			name:     "reset at this very second",
			sevenDay: windowJSON(46.5, now), kind: "none",
			summary: []string{"The weekly limit reset at 12pm, so this reading is out of date"},
		},
		{
			name: "no weekly window", kind: "none",
			summary: []string{"no weekly reading"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, dir := usageTools(t)
			if tt.anchor != "" {
				writeFile(t, pace.SnapshotPath(dir), tt.anchor)
			}
			writeFile(t, feed.Path(dir), feedJSON(windowJSON(10, sessionReset), tt.sevenDay, now))
			summary, got := callOK(t, ts, "get_today_budget")
			if got["kind"] != tt.kind || got["ageSeconds"] != 0.0 || got["stale"] != false {
				t.Errorf("kind, age, stale = %v, %v, %v; want %s, 0, false", got["kind"], got["ageSeconds"], got["stale"], tt.kind)
			}
			for _, want := range tt.summary {
				if !strings.Contains(summary, want) {
					t.Errorf("summary lacks %q: %q", want, summary)
				}
			}
			for _, unwanted := range tt.absent {
				if strings.Contains(summary, unwanted) {
					t.Errorf("summary says %q: %q", unwanted, summary)
				}
			}
			for k, want := range tt.fields {
				if got[k] != want {
					t.Errorf("%s = %v, want %v", k, got[k], want)
				}
			}
		})
	}
}

func TestUsageToolsWithoutAFeed(t *testing.T) {
	for _, name := range []string{"get_usage", "get_today_budget"} {
		ts, dir := usageTools(t)
		text, failed := call(t, ts, name, `{}`)
		if failed || !strings.Contains(text, "feed is off") || !strings.Contains(text, "`paceline feed on`") {
			t.Errorf("%s with no feed: failed %v, %q", name, failed, text)
		}

		writeFile(t, filepath.Join(dir, "paceline.json"), `{"feed": true}`)
		text, failed = call(t, ts, name, `{}`)
		if failed || !strings.Contains(text, "no reading yet") || strings.Contains(text, "feed on`") {
			t.Errorf("%s with the feed on but no file: failed %v, %q", name, failed, text)
		}
	}
}

func TestUsageToolsWithAnInvalidFeed(t *testing.T) {
	for _, name := range []string{"get_usage", "get_today_budget"} {
		ts, dir := usageTools(t)
		writeFile(t, feed.Path(dir), `{"version":2,"sevenDay":{"usedPct":1,"resetsAt":1},"writtenAt":1}`)
		if text, failed := call(t, ts, name, `{}`); !failed || !strings.Contains(text, "could not read the usage feed") {
			t.Errorf("%s with an invalid feed: failed %v, %q", name, failed, text)
		}
	}
}

func TestUsageToolsLeadWithAStaleAge(t *testing.T) {
	tests := []struct {
		name  string
		age   time.Duration
		lead  string // "" when the reading is fresh
		stale bool
	}{
		{name: "fresh", age: 42 * time.Second},
		{name: "exactly ten minutes", age: 600 * time.Second},
		{name: "one second past ten minutes", age: 601 * time.Second, lead: "This reading is 10m old", stale: true},
		{name: "hours old", age: 3*time.Hour + 5*time.Minute, lead: "This reading is 3h5m old", stale: true},
		{name: "from the future", age: -2 * time.Minute, lead: "This reading is dated 2m in the future", stale: true},
	}
	for _, tt := range tests {
		for _, name := range []string{"get_usage", "get_today_budget"} {
			t.Run(tt.name+"/"+name, func(t *testing.T) {
				ts, dir := usageTools(t)
				writeFile(t, feed.Path(dir), feedJSON(windowJSON(21, sessionReset), windowJSON(46.5, weekReset), now.Add(-tt.age)))
				summary, got := callOK(t, ts, name)
				if got["stale"] != tt.stale || got["ageSeconds"] != tt.age.Seconds() {
					t.Errorf("stale, ageSeconds = %v, %v; want %v, %v", got["stale"], got["ageSeconds"], tt.stale, tt.age.Seconds())
				}
				switch {
				case tt.lead == "" && strings.HasPrefix(summary, "This reading is"):
					t.Errorf("a fresh reading leads with its age: %q", summary)
				case tt.lead != "" && !strings.HasPrefix(summary, tt.lead):
					t.Errorf("summary should start with %q: %q", tt.lead, summary)
				}
			})
		}
	}
}

func TestTodayBudgetNeverWritesTheAnchor(t *testing.T) {
	t.Run("no anchor stays absent", func(t *testing.T) {
		ts, dir := usageTools(t)
		writeFile(t, feed.Path(dir), feedJSON("", windowJSON(46.5, weekReset), now))
		before, _ := os.ReadDir(dir)
		callOK(t, ts, "get_today_budget")
		if _, err := os.Stat(pace.SnapshotPath(dir)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the anchor was created (stat err %v)", err)
		}
		if after, _ := os.ReadDir(dir); len(after) != len(before) {
			t.Errorf("files changed from %v to %v", before, after)
		}
	})
	t.Run("a stale anchor is left as it is", func(t *testing.T) {
		ts, dir := usageTools(t)
		writeFile(t, feed.Path(dir), feedJSON("", windowJSON(46.5, weekReset), now))
		anchor := pace.SnapshotPath(dir)
		yesterday := fmt.Sprintf(`{"date":%q,"resetsAt":%d,"usedAtStart":30}`, timefmt.DateKey(now.AddDate(0, 0, -1)), weekReset.Unix())
		writeFile(t, anchor, yesterday)
		old := time.Now().Add(-time.Hour).Truncate(time.Second)
		if err := os.Chtimes(anchor, old, old); err != nil {
			t.Fatal(err)
		}

		_, got := callOK(t, ts, "get_today_budget")
		if got["kind"] != "budget" || got["spentPct"] != 0.0 {
			t.Errorf("a stale anchor should re-anchor today's answer at current usage: %v", got)
		}
		data, _ := os.ReadFile(anchor)
		info, err := os.Stat(anchor)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != yesterday || !info.ModTime().Equal(old) {
			t.Errorf("the anchor changed: %s, modified %v", data, info.ModTime())
		}
	})
}

func TestUsageReportsBothWindows(t *testing.T) {
	ts, dir := usageTools(t)
	writeFile(t, feed.Path(dir), feedJSON(windowJSON(21, sessionReset), windowJSON(46.5, weekReset), now.Add(-42*time.Second)))
	summary, got := callOK(t, ts, "get_usage")
	for _, want := range []string{"Session: 79% left (21% used), resets 3:30pm.", "Week: 53% left (47% used), resets Thu 9pm."} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary lacks %q: %q", want, summary)
		}
	}
	wantWindows := map[string]map[string]any{
		"session": {"usedPct": 21.0, "leftPct": 79.0, "resetsAt": "2026-10-06T15:30:00Z", "resetsClock": "3:30pm", "resetPassed": false},
		"week":    {"usedPct": 47.0, "leftPct": 53.0, "resetsAt": "2026-10-08T21:00:00Z", "resetsClock": "Thu 9pm", "resetPassed": false},
	}
	for name, want := range wantWindows {
		w, ok := got[name].(map[string]any)
		if !ok {
			t.Errorf("%s = %v, want an object", name, got[name])
			continue
		}
		for k, v := range want {
			if w[k] != v {
				t.Errorf("%s.%s = %v, want %v", name, k, w[k], v)
			}
		}
	}
	if got["ageSeconds"] != 42.0 || got["stale"] != false {
		t.Errorf("ageSeconds, stale = %v, %v", got["ageSeconds"], got["stale"])
	}
}

func TestUsageReportsAMissingWindow(t *testing.T) {
	ts, dir := usageTools(t)
	writeFile(t, feed.Path(dir), feedJSON("", windowJSON(46.5, weekReset), now))
	summary, got := callOK(t, ts, "get_usage")
	if !strings.Contains(summary, "Session: no reading in the feed.") {
		t.Errorf("summary should report the missing session: %q", summary)
	}
	if v, ok := got["session"]; !ok || v != nil {
		t.Errorf("session = %v (present %v), want null", v, ok)
	}
	if _, ok := got["week"].(map[string]any); !ok {
		t.Errorf("week = %v, want an object", got["week"])
	}

	writeFile(t, feed.Path(dir), feedJSON(windowJSON(21, sessionReset), "", now))
	summary, got = callOK(t, ts, "get_usage")
	if !strings.Contains(summary, "Week: no reading in the feed.") || got["week"] != nil {
		t.Errorf("a missing week: %q, week = %v", summary, got["week"])
	}
}

func TestUsageReportsAPassedReset(t *testing.T) {
	for name, tt := range map[string]struct {
		reset time.Time
		clock string
	}{
		"an hour ago":      {reset: time.Date(2026, 10, 6, 11, 0, 0, 0, time.UTC), clock: "11am"},
		"this very second": {reset: now, clock: "12pm"},
	} {
		t.Run(name, func(t *testing.T) {
			ts, dir := usageTools(t)
			writeFile(t, feed.Path(dir), feedJSON(windowJSON(21, tt.reset), windowJSON(46.5, weekReset), now))
			summary, got := callOK(t, ts, "get_usage")
			want := "Session: reset at " + tt.clock + ", so this reading (21% used) is out of date until Claude Code reports new usage."
			if !strings.Contains(summary, want) || strings.Contains(summary, "79% left") {
				t.Errorf("summary should say the session reset passed, without a left figure: %q", summary)
			}
			if s, _ := got["session"].(map[string]any); s["resetPassed"] != true {
				t.Errorf("session = %v, want resetPassed true", got["session"])
			}
		})
	}
}

func TestUsageUsesTheLocalTimeZone(t *testing.T) {
	ts, dir := usageTools(t)
	zone := time.FixedZone("x", -7*3600)
	ts.now = func() time.Time { return now.In(zone) }
	writeFile(t, feed.Path(dir), feedJSON(windowJSON(21, sessionReset), windowJSON(46.5, weekReset), now))
	_, got := callOK(t, ts, "get_usage")
	session, _ := got["session"].(map[string]any)
	week, _ := got["week"].(map[string]any)
	if session["resetsAt"] != "2026-10-06T08:30:00-07:00" || session["resetsClock"] != "8:30am" {
		t.Errorf("session reset = %v, %v; want local -07:00 time and 8:30am", session["resetsAt"], session["resetsClock"])
	}
	if week["resetsAt"] != "2026-10-08T14:00:00-07:00" || week["resetsClock"] != "Thu 2pm" {
		t.Errorf("week reset = %v, %v; want local -07:00 time and Thu 2pm", week["resetsAt"], week["resetsClock"])
	}

	summary, budget := callOK(t, ts, "get_today_budget")
	if budget["kind"] != "budget" || budget["weekResetsAt"] != "2026-10-08T14:00:00-07:00" || budget["weekResetsClock"] != "Thu 2pm" {
		t.Errorf("today's budget = %v; want kind budget and a local -07:00 Thu 2pm reset", budget)
	}
	if !strings.Contains(summary, "the week resets Thu 2pm.") {
		t.Errorf("summary should give the local reset time: %q", summary)
	}
}
