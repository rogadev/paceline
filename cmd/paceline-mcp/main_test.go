package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rogadev/paceline/internal/progress"
)

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// repo makes a temporary repository with a subfolder to run from.
func repo(t *testing.T) (root, sub string) {
	t.Helper()
	root = t.TempDir()
	sub = filepath.Join(root, "src")
	for _, d := range []string{filepath.Join(root, ".git"), sub} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return root, sub
}

func testTools(dir string) toolset {
	return toolset{getwd: func() (string, error) { return dir, nil }, now: func() time.Time { return now }}
}

// call runs one tool by name and returns its text and whether it failed.
func call(t *testing.T, ts toolset, name, args string) (string, bool) {
	t.Helper()
	for _, tool := range ts.tools() {
		if tool.Name == name {
			text, err := tool.Call(json.RawMessage(args))
			if err != nil {
				return err.Error(), true
			}
			return text, false
		}
	}
	t.Fatalf("no tool %q", name)
	return "", true
}

func TestToolsDriveTheProgressFile(t *testing.T) {
	root, sub := repo(t)
	ts := testTools(sub)
	gitDir := filepath.Join(root, ".git")

	steps := []string{
		`progress_start`, `{"name": "orc", "steps": [
			{"id": "1", "label": "#1", "weight": 3, "stages": ["build", "review", "commit"]},
			{"id": "2", "label": "#2", "weight": 1}]}`,
		`progress_step`, `{"id": "1", "stage": "review"}`,
		`progress_label`, `{"label": "review 1/3"}`,
		`progress_add_steps`, `{"steps": [{"id": "fix", "label": "fix"}]}`,
		`progress_step`, `{"id": "1", "status": "done"}`,
	}
	var text string
	for i := 0; i < len(steps); i += 2 {
		var failed bool
		if text, failed = call(t, ts, steps[i], steps[i+1]); failed {
			t.Fatalf("%s failed: %s", steps[i], text)
		}
	}
	if text != "orc: running, 1 of 3 steps settled, 60% of planned work." {
		t.Errorf("summary = %q", text)
	}

	r := progress.Read(gitDir)
	if r == nil {
		t.Fatal("the status line reader could not parse the file the tools wrote")
	}
	if r.Label != "review 1/3" || len(r.Steps) != 3 || r.Steps[0].Status != progress.Settled || !r.UpdatedAt.Equal(now) {
		t.Errorf("file = %+v", r)
	}

	if text, failed := call(t, ts, "progress_finish", `{"outcome": "done"}`); failed || !strings.Contains(text, "done") {
		t.Errorf("finish: %s", text)
	}
	if r := progress.Read(gitDir); r == nil || r.Phase != progress.Done {
		t.Errorf("after finish: %+v", r)
	}
}

func TestInvalidCallsLeaveTheFileUnchanged(t *testing.T) {
	_, sub := repo(t)
	ts := testTools(sub)
	if text, failed := call(t, ts, "progress_start", `{"name": "orc", "steps": [{"id": "1", "label": "#1", "stages": ["build"]}]}`); failed {
		t.Fatal(text)
	}
	path := progress.Path(filepath.Join(filepath.Dir(sub), ".git"))
	before, _ := os.ReadFile(path)

	for name, c := range map[string][2]string{
		"unknown status":   {"progress_step", `{"id": "1", "status": "waiting"}`},
		"missing step":     {"progress_step", `{"id": "9", "status": "done"}`},
		"unplanned stage":  {"progress_step", `{"id": "1", "stage": "ship"}`},
		"long label":       {"progress_label", `{"label": "` + strings.Repeat("x", 41) + `"}`},
		"wrong arg type":   {"progress_step", `{"id": 1}`},
		"duplicate add":    {"progress_add_steps", `{"steps": [{"id": "1", "label": "again"}]}`},
		"no added steps":   {"progress_add_steps", `{"steps": []}`},
		"unknown outcome":  {"progress_finish", `{"outcome": "cancelled"}`},
		"start no steps":   {"progress_start", `{"name": "orc", "steps": []}`},
		"start long label": {"progress_start", `{"name": "orc", "steps": [{"id": "1", "label": "` + strings.Repeat("x", 41) + `"}]}`},
	} {
		if text, failed := call(t, ts, c[0], c[1]); !failed {
			t.Errorf("%s: want a tool error, got %q", name, text)
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Error("an invalid call changed the progress file")
	}
}

func TestCwdArgument(t *testing.T) {
	root, _ := repo(t)
	outside := testTools(t.TempDir())
	if text, failed := call(t, outside, "progress_label", `{"label": "x"}`); !failed || !strings.Contains(text, "not inside a git repository") {
		t.Errorf("outside a repo: %s", text)
	}
	args := `{"cwd": ` + strconvQuote(root) + `, "name": "orc", "steps": [{"id": "1", "label": "#1"}]}`
	if text, failed := call(t, outside, "progress_start", args); failed {
		t.Errorf("cwd should name the repo: %s", text)
	}
	if progress.Read(filepath.Join(root, ".git")) == nil {
		t.Error("the run was not written to the cwd repo")
	}
	if text, failed := call(t, testTools(root), "progress_step", `{"id": "1"}`); !failed || !strings.Contains(text, "status, a stage") {
		t.Errorf("a step call with nothing to change: %s", text)
	}
	broken := toolset{getwd: func() (string, error) { return "", errors.New("no cwd") }, now: time.Now}
	if _, failed := call(t, broken, "progress_label", `{"label": "x"}`); !failed {
		t.Error("a failing getwd should be a tool error")
	}
}

func TestStepBeforeStart(t *testing.T) {
	_, sub := repo(t)
	if text, failed := call(t, testTools(sub), "progress_step", `{"id": "1", "status": "done"}`); !failed || !strings.Contains(text, "start one first") {
		t.Errorf("a step with no run: %s", text)
	}
}

func TestToolSchemasAreValidJSON(t *testing.T) {
	tools := testTools("").tools()
	if len(tools) != 5 {
		t.Errorf("want 5 tools, got %d", len(tools))
	}
	for _, tool := range tools {
		var s struct {
			Type       string         `json:"type"`
			Required   []string       `json:"required"`
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &s); err != nil {
			t.Errorf("%s: schema is not valid JSON: %v", tool.Name, err)
			continue
		}
		if s.Type != "object" || s.Properties["cwd"] == nil || len(s.Required) == 0 {
			t.Errorf("%s: schema = %+v", tool.Name, s)
		}
		for _, r := range s.Required {
			if s.Properties[r] == nil {
				t.Errorf("%s: required %q has no property", tool.Name, r)
			}
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
}

func TestRun(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, nil, &out, &errOut, testTools("")); code != 0 || strings.TrimSpace(out.String()) == "" {
		t.Errorf("--version: code %d, out %q", code, out.String())
	}
	if code := run([]string{"serve"}, nil, &out, &errOut, testTools("")); code != 2 || !strings.Contains(errOut.String(), "no arguments") {
		t.Errorf("unknown argument: code %d, stderr %q", code, errOut.String())
	}

	out.Reset()
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	if code := run(nil, in, &out, &errOut, testTools("")); code != 0 || !strings.Contains(out.String(), "progress_start") {
		t.Errorf("serve: code %d, out %q", code, out.String())
	}
	huge := strings.NewReader(strings.Repeat("x", 2<<20))
	if code := run(nil, huge, &out, &errOut, testTools("")); code != 1 {
		t.Errorf("an oversized message should exit 1, got %d", code)
	}
}

func TestBuildVersion(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "1.2.3"
	if got := buildVersion(); got != "1.2.3" {
		t.Errorf("buildVersion = %q", got)
	}
	version = ""
	if got := buildVersion(); got == "" {
		t.Error("buildVersion should never be empty")
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
