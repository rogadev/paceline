package render

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rogadev/paceline/internal/config"
	"github.com/rogadev/paceline/internal/payload"
	"github.com/rogadev/paceline/internal/progress"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/progress.json with the current output")

// progressCase renders one progress file on its own: every other segment is
// off, so the want holds only the progress segment, ANSI codes included.
type progressCase struct {
	Name    string          `json:"name"`
	Now     time.Time       `json:"now"`
	Config  json.RawMessage `json:"config"`
	NoColor bool            `json:"noColor,omitempty"`
	File    json.RawMessage `json:"file"`
	Want    string          `json:"want"`
}

const progressGolden = "testdata/progress.json"

func TestProgressGolden(t *testing.T) {
	data, err := os.ReadFile(progressGolden)
	if err != nil {
		t.Fatal(err)
	}
	var cases []progressCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	p, err := payload.Decode([]byte(`{"cwd": "/r/repo"}`))
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			gitDir := t.TempDir()
			if len(c.File) > 0 && string(c.File) != "null" {
				if err := os.MkdirAll(filepath.Dir(progress.Path(gitDir, "")), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(progress.Path(gitDir, ""), c.File, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.Merge(c.Config)
			cfg.Segments = config.Segments{Progress: cfg.Segments.Progress}
			got := Render(p, Context{
				Now: c.Now, Config: cfg, NoColor: c.NoColor,
				Progress: func(dir string) *progress.Run {
					if dir != "/r/repo" {
						t.Errorf("Progress called with %q, want the session's cwd", dir)
					}
					return progress.Read(gitDir, "")
				},
			})
			if *updateGolden {
				cases[i].Want = got
				return
			}
			if got != c.Want {
				t.Errorf("\n got  %q\n want %q", got, c.Want)
			}
		})
	}
	if *updateGolden {
		out, err := json.MarshalIndent(cases, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(progressGolden, append(out, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProgressNeedsADirectory(t *testing.T) {
	c := ctx()
	c.Progress = func(string) *progress.Run {
		t.Error("Progress should not be called without a working directory")
		return nil
	}
	if got := Render(&payload.Payload{}, c); got != "" {
		t.Errorf("got %q", got)
	}
}
