package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMergeFallsBackToDefaults(t *testing.T) {
	for _, in := range []string{"", "null", "42", `"x"`, "[]", "{nope"} {
		if got := Merge([]byte(in)); !reflect.DeepEqual(got, Default()) {
			t.Errorf("Merge(%q) = %+v", in, got)
		}
	}
}

func TestMergeTakesOnlyValidValues(t *testing.T) {
	c := Merge([]byte(`{
		"segments": {"model": false, "duration": "no", "unknown": false},
		"thresholds": {"headroomGreen": 40, "headroomYellow": 500, "contextWarn": "70"},
		"quietEfforts": ["low"],
		"projectSlots": {"api": 3, "web": 12, "bad": 1.5, "neg": -1, "str": "4"}
	}`))
	if c.Segments.Model || !c.Segments.Duration {
		t.Errorf("segments = %+v", c.Segments)
	}
	if c.Thresholds.HeadroomGreen != 40 || c.Thresholds.HeadroomYellow != 15 || c.Thresholds.ContextWarn != 70 {
		t.Errorf("thresholds = %+v", c.Thresholds)
	}
	if !reflect.DeepEqual(c.QuietEfforts, []string{"low"}) {
		t.Errorf("quietEfforts = %v", c.QuietEfforts)
	}
	if !reflect.DeepEqual(c.ProjectSlots, map[string]int{"api": 3}) {
		t.Errorf("projectSlots = %v", c.ProjectSlots)
	}
}

func TestMergeKeepsDefaultsForWrongTypedSections(t *testing.T) {
	c := Merge([]byte(`{"segments": [1], "quietEfforts": [1, 2], "projectSlots": "x"}`))
	if !reflect.DeepEqual(c, Default()) {
		t.Errorf("got %+v", c)
	}
}

func TestDefaultsAreIndependentCopies(t *testing.T) {
	a := Default()
	a.QuietEfforts[0] = "changed"
	a.ProjectSlots["x"] = 1
	if b := Default(); b.QuietEfforts[0] != "low" || len(b.ProjectSlots) != 0 {
		t.Error("Default shares state between calls")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "paceline.json")
	if err := os.WriteFile(good, []byte(`{"segments": {"duration": false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if Load(good).Segments.Duration {
		t.Error("config file not applied")
	}
	if !Load(filepath.Join(dir, "missing.json")).Segments.Model {
		t.Error("missing file did not yield defaults")
	}
	big := filepath.Join(dir, "big.json")
	content := `{"segments": {"model": false}, "pad": "` + strings.Repeat("x", 70_000) + `"}`
	if err := os.WriteFile(big, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Load(big).Segments.Model {
		t.Error("oversized file was read")
	}
}

func TestDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom")
	if Dir() != "/custom" {
		t.Errorf("Dir = %q", Dir())
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if !strings.HasSuffix(Dir(), ".claude") {
		t.Errorf("Dir = %q", Dir())
	}
}
