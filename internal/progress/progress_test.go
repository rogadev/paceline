package progress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func writeFile(t *testing.T, gitDir, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(Path(gitDir, "")), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(gitDir, ""), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const validFile = `{
  "version": 1, "name": "loop", "phase": "running",
  "steps": [
    {"id": "41", "label": "#41", "status": "done"},
    {"id": "42", "label": "#42", "status": "active"},
    {"id": "43", "label": "#43", "status": "pending"}
  ],
  "updatedAt": "2026-10-06T11:00:00Z"
}`

func TestReadValidFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, validFile)
	r := Read(dir, "")
	if r == nil {
		t.Fatal("Read returned nil for a valid file")
	}
	if r.Name != "loop" || len(r.Steps) != 3 || r.Steps[1].Status != Active || r.ActiveIndex() != 1 || r.SettledCount() != 1 {
		t.Errorf("unexpected run: %+v", r)
	}
}

func TestReadRejectsUnusableFiles(t *testing.T) {
	step := `{"id": "1", "label": "a", "status": "pending"}`
	run := func(fields string) string {
		return `{"version": 1, "name": "x", "phase": "running", "updatedAt": "2026-10-06T11:00:00Z", ` + fields + `}`
	}
	tests := map[string]string{
		"corrupt":           `{"version": 1, "name":`,
		"wrong type":        `{"version": "1"}`,
		"future version":    strings.Replace(validFile, `"version": 1`, `"version": 2`, 1),
		"unknown phase":     strings.Replace(validFile, `"running"`, `"paused"`, 1),
		"unknown status":    strings.Replace(validFile, `"pending"`, `"waiting"`, 1),
		"no steps":          run(`"steps": []`),
		"no updatedAt":      `{"version": 1, "name": "x", "phase": "running", "steps": [` + step + `]}`,
		"no name":           strings.Replace(validFile, `"loop"`, `""`, 1),
		"duplicate ids":     run(`"steps": [` + step + `, ` + step + `]`),
		"empty id":          run(`"steps": [{"id": "", "label": "a", "status": "pending"}]`),
		"long label":        run(`"steps": [{"id": "1", "label": "` + strings.Repeat("x", 41) + `", "status": "pending"}]`),
		"negative weight":   run(`"steps": [{"id": "1", "label": "a", "status": "pending", "weight": -1}]`),
		"huge weight":       run(`"steps": [{"id": "1", "label": "a", "status": "pending", "weight": 1001}]`),
		"stage not planned": run(`"steps": [{"id": "1", "label": "a", "status": "active", "stages": ["build"], "stage": "ship"}]`),
		"empty stage name":  run(`"steps": [{"id": "1", "label": "a", "status": "active", "stages": [""]}]`),
		"too many stages":   run(`"steps": [{"id": "1", "label": "a", "status": "pending", "stages": ["a","b","c","d","e","f","g","h","i","j","k","l","m"]}]`),
		"long run label":    run(`"label": "` + strings.Repeat("x", 41) + `", "steps": [` + step + `]`),
		"too many steps":    run(`"steps": [` + strings.TrimSuffix(strings.Repeat(`{"id": "N", "label": "a", "status": "pending"},`, 51), ",") + `]`),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			// Give the too-many-steps case distinct ids, so the count is what fails.
			n := 0
			for strings.Contains(body, `"id": "N"`) {
				n++
				body = strings.Replace(body, `"id": "N"`, `"id": "`+strings.Repeat("i", n%40+1)+string(rune('a'+n/40))+`"`, 1)
			}
			writeFile(t, dir, body)
			if r := Read(dir, ""); r != nil {
				t.Errorf("Read accepted %s file: %+v", name, r)
			}
		})
	}
}

func TestReadMissingOversizedAndNonRegular(t *testing.T) {
	if Read("", "") != nil {
		t.Error("Read should be nil outside a repo")
	}
	dir := t.TempDir()
	if Read(dir, "") != nil {
		t.Error("Read should be nil with no file")
	}
	writeFile(t, dir, validFile[:len(validFile)-1]+strings.Repeat(" ", maxFileBytes)+"}")
	if Read(dir, "") != nil {
		t.Error("Read should reject a file over the size cap")
	}
	other := t.TempDir()
	if err := os.MkdirAll(Path(other, ""), 0o750); err != nil {
		t.Fatal(err)
	}
	if Read(other, "") != nil {
		t.Error("Read should reject a directory in place of the file")
	}
}

func TestVisibleAndPaused(t *testing.T) {
	tests := []struct {
		phase           string
		age             time.Duration
		visible, paused bool
	}{
		{Running, time.Hour, true, false},
		{Running, time.Hour + time.Second, true, true},
		{Running, 4 * time.Hour, true, true},
		{Running, 4*time.Hour + time.Second, false, true},
		{Running, -time.Hour, true, false}, // a clock behind the writer's
		{Done, 30 * time.Minute, true, false},
		{Done, 31 * time.Minute, false, false},
		{Halted, 4 * time.Hour, true, false},
		{Halted, 4*time.Hour + time.Second, false, false},
	}
	for _, tt := range tests {
		r := &Run{Phase: tt.phase, UpdatedAt: now.Add(-tt.age)}
		if got := r.Visible(now); got != tt.visible {
			t.Errorf("%s aged %v: Visible = %v, want %v", tt.phase, tt.age, got, tt.visible)
		}
		if got := r.Paused(now); got != tt.paused {
			t.Errorf("%s aged %v: Paused = %v, want %v", tt.phase, tt.age, got, tt.paused)
		}
	}
}

func TestPercent(t *testing.T) {
	stages := []string{"design", "build", "review", "commit"}
	tests := []struct {
		name    string
		steps   []Step
		want    float64
		planned bool
	}{
		{"unplanned counts steps", []Step{{Status: Settled}, {Status: Active}, {Status: Pending}, {Status: Pending}}, 25, false},
		{"weights", []Step{{Status: Settled, Weight: 3}, {Status: Pending, Weight: 1}}, 75, true},
		{"stage of active step", []Step{{Status: Active, Weight: 2, Stages: stages, Stage: "review"}, {Status: Pending, Weight: 2}}, 25, true},
		{"first stage counts nothing", []Step{{Status: Active, Stages: stages, Stage: "design"}}, 0, true},
		{"skipped and blocked leave the total", []Step{{Status: Settled}, {Status: Skipped, Weight: 9}, {Status: Blocked, Weight: 9}, {Status: Pending}}, 50, true},
		{"nothing left to do", []Step{{Status: Skipped}}, 100, false},
	}
	for _, tt := range tests {
		r := &Run{Steps: tt.steps}
		if got := r.Percent(); got != tt.want {
			t.Errorf("%s: Percent = %v, want %v", tt.name, got, tt.want)
		}
		if got := r.Planned(); got != tt.planned {
			t.Errorf("%s: Planned = %v, want %v", tt.name, got, tt.planned)
		}
	}
}

func plan() []NewStep {
	return []NewStep{
		{ID: "1", Label: "#1", Weight: 2, Stages: []string{"build", "review"}},
		{ID: "2", Label: "#2"},
	}
}

func TestWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := Start("orc", plan())
	if err := Save(dir, "", r, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SetStep("1", "", "review"); err != nil {
		t.Fatal(err)
	}
	if err := r.AddSteps([]NewStep{{ID: "fix", Label: "fix"}}); err != nil {
		t.Fatal(err)
	}
	r.SetLabel("review 1/3")
	if err := Save(dir, "", r, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	got := Read(dir, "")
	if got == nil {
		t.Fatal("Read could not parse what Save wrote")
	}
	if got.Name != "orc" || got.Label != "review 1/3" || len(got.Steps) != 3 || !got.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("round trip lost data: %+v", got)
	}
	if s := got.Steps[0]; s.Status != Active || s.Stage != "review" || s.Weight != 2 {
		t.Errorf("step 1 = %+v, want active in review", s)
	}
	if got.Steps[2].Status != Pending {
		t.Errorf("an added step should start pending, got %q", got.Steps[2].Status)
	}

	loaded, err := Load(dir, "")
	if err != nil || loaded.Name != "orc" {
		t.Errorf("Load = %+v, %v", loaded, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(Path(dir, "")))
	if len(entries) != 1 {
		t.Errorf("Save left temp files behind: %v", entries)
	}
}

func TestSetStep(t *testing.T) {
	r := Start("orc", plan())
	if err := r.SetStep("1", Active, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SetStep("2", Active, ""); err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Status != Pending || r.ActiveIndex() != 1 {
		t.Errorf("activating a step should clear the other active step: %+v", r.Steps)
	}
	if err := r.SetStep("1", "", "build"); err != nil || r.Steps[0].Status != Active || r.Steps[1].Status != Pending {
		t.Errorf("a stage alone should activate the step: %v, %+v", err, r.Steps)
	}
	if err := r.SetStep("1", Settled, ""); err != nil || r.Steps[0].Stage != "" {
		t.Errorf("ending a step should clear its stage: %v, %+v", err, r.Steps[0])
	}
	_ = r.Finish(Halted)
	if err := r.SetStep("2", Active, ""); err != nil || r.Phase != Running {
		t.Errorf("a step change after halting should resume the run: %v, phase %q", err, r.Phase)
	}

	for name, call := range map[string][3]string{
		"unknown id":        {"9", Active, ""},
		"unknown status":    {"1", "waiting", ""},
		"nothing to change": {"1", "", ""},
		"unplanned stage":   {"1", "", "ship"},
		"stage when done":   {"1", Settled, "build"},
	} {
		if err := r.SetStep(call[0], call[1], call[2]); err == nil {
			t.Errorf("%s: SetStep(%q, %q, %q) should fail", name, call[0], call[1], call[2])
		}
	}
}

func TestFinish(t *testing.T) {
	r := Start("orc", plan())
	_ = r.SetStep("1", Active, "")
	r.SetLabel("wrapping up")
	if err := r.Finish(Done); err != nil {
		t.Fatal(err)
	}
	if r.Phase != Done || r.Label != "" || r.Steps[0].Status != Settled || r.Steps[1].Status != Pending {
		t.Errorf("Finish(done) = %+v", r)
	}
	if err := r.Finish("cancelled"); err == nil {
		t.Error("Finish should reject an unknown outcome")
	}
}

func TestAddStepsLimits(t *testing.T) {
	r := Start("orc", plan())
	if err := r.AddSteps(nil); err == nil {
		t.Error("AddSteps should reject an empty list")
	}
	many := make([]NewStep, MaxSteps)
	if err := r.AddSteps(many); err == nil {
		t.Error("AddSteps should reject a run over the step cap")
	}
}

func TestSaveRejectsInvalidRunsAndKeepsTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "", Start("orc", plan()), now); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(Path(dir, ""))
	for name, r := range map[string]*Run{
		"no name":      Start("", plan()),
		"no steps":     Start("orc", nil),
		"long label":   Start("orc", []NewStep{{ID: "1", Label: strings.Repeat("x", 41)}}),
		"duplicate id": Start("orc", []NewStep{{ID: "1"}, {ID: "1"}}),
	} {
		if err := Save(dir, "", r, now); err == nil {
			t.Errorf("%s: Save should fail", name)
		}
	}
	after, _ := os.ReadFile(Path(dir, ""))
	if string(before) != string(after) {
		t.Error("a rejected Save changed the file")
	}
}

func TestSaveRejectsOversizedRun(t *testing.T) {
	steps := make([]NewStep, MaxSteps)
	for i := range steps {
		stages := make([]string, MaxStages)
		for j := range stages {
			stages[j] = strings.Repeat("s", MaxLabelRunes-2) + string(rune('a'+j))
		}
		steps[i] = NewStep{ID: strings.Repeat("i", 30) + string(rune('A'+i)), Label: strings.Repeat("l", MaxLabelRunes), Stages: stages}
	}
	if err := Save(t.TempDir(), "", Start("orc", steps), now); err == nil {
		t.Error("Save should refuse a run larger than the reader accepts")
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "start one") {
		t.Errorf("Load with no file: %v", err)
	}
	dir := t.TempDir()
	writeFile(t, dir, "{not json")
	if _, err := Load(dir, ""); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("Load with a corrupt file: %v", err)
	}
}

// TestReadersNeverSeePartialWrites polls the file during a burst of saves:
// every read must parse, because Save renames a complete temp file into place.
func TestReadersNeverSeePartialWrites(t *testing.T) {
	dir := t.TempDir()
	r := Start("orc", plan())
	if err := Save(dir, "", r, now); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var torn int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(Path(dir, ""))
			if err != nil {
				continue // Windows can refuse a read mid-rename.
			}
			var got Run
			if json.Unmarshal(data, &got) != nil {
				torn++
			}
		}
	}()
	for i := range 200 {
		status := []string{Active, Pending, Settled}[i%3]
		_ = r.SetStep("2", status, "")
		if err := Save(dir, "", r, now); err != nil {
			t.Log(err) // Windows can refuse a rename while the file is open.
		}
	}
	close(stop)
	wg.Wait()
	if torn > 0 {
		t.Errorf("a reader saw partial JSON %d times", torn)
	}
}

// A file whose reported size lies (a symlink to a /proc file reports 0) must
// still be read no further than the cap.
func TestParseCapsTheRead(t *testing.T) {
	if parse(strings.NewReader(validFile)) == nil {
		t.Fatal("parse rejected a valid run")
	}
	padded := validFile[:len(validFile)-1] + strings.Repeat(" ", maxFileBytes) + "}"
	if parse(strings.NewReader(padded)) != nil {
		t.Error("parse took input over the size cap")
	}
}

// A symlink planted at the old predictable temp name must not redirect the
// write to the file it points at.
func TestSaveIgnoresPlantedTempSymlink(t *testing.T) {
	gitDir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(Path(gitDir, "")), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, Path(gitDir, "")+"."+strconv.Itoa(os.Getpid())+".tmp"); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := Save(gitDir, "", Start("loop", []NewStep{{ID: "1", Label: "#1"}}), now); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Errorf("victim overwritten: %q", b)
	}
	if Read(gitDir, "") == nil {
		t.Error("the run was not saved")
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(Path(gitDir, "")), "progress-*.tmp"))
	if len(matches) != 0 {
		t.Errorf("temp files left: %v", matches)
	}
}

func TestSessionPath(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "paceline", "progress.json")
	if got := Path(dir, "6f348220-ec29-4b61-9ab7-15218ee26bc6"); got != filepath.Join(dir, "paceline", "progress-6f348220-ec29-4b61-9ab7-15218ee26bc6.json") {
		t.Errorf("session path = %s", got)
	}
	for _, bad := range []string{"", "../x", "a/b", `a`, "a.b", "a b", strings.Repeat("a", 65)} {
		if got := Path(dir, bad); got != shared {
			t.Errorf("session %q: path %s, want the shared file", bad, got)
		}
	}
}

func TestCurrentPrefersTheSessionsOwnRun(t *testing.T) {
	dir := t.TempDir()
	if Current(dir, "a1") != nil {
		t.Error("no files should mean no run")
	}
	if err := Save(dir, "a1", Start("mine", plan()), now); err != nil {
		t.Fatal(err)
	}
	if r := Current(dir, "a1"); r == nil || r.Name != "mine" {
		t.Errorf("own run: %+v", r)
	}
	if r := Current(dir, "b2"); r != nil {
		t.Errorf("another session's run leaked: %+v", r)
	}
	if err := Save(dir, "", Start("shared", plan()), now); err != nil {
		t.Fatal(err)
	}
	for session, want := range map[string]string{"a1": "mine", "b2": "shared", "": "shared", "../a1": "shared"} {
		if r := Current(dir, session); r == nil || r.Name != want {
			t.Errorf("session %q: got %+v, want %s", session, r, want)
		}
	}
}

func TestPruneRemovesOnlyOldSessionFiles(t *testing.T) {
	dir := t.TempDir()
	for _, session := range []string{"", "keep", "fresh", "stale"} {
		if err := Save(dir, session, Start("run", plan()), now); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-25 * time.Hour)
	for _, session := range []string{"", "keep", "stale"} {
		if err := os.Chtimes(Path(dir, session), old, old); err != nil {
			t.Fatal(err)
		}
	}
	Prune(dir, "keep", now)
	for session, kept := range map[string]bool{"": true, "keep": true, "fresh": true, "stale": false} {
		if _, err := os.Stat(Path(dir, session)); (err == nil) != kept {
			t.Errorf("session %q: kept = %v, want %v", session, err == nil, kept)
		}
	}
}
