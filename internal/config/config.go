// Package config loads paceline.json and merges it onto the defaults.
package config

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
)

const maxConfigBytes = 64 * 1024

// Segments turns individual status-line segments on or off.
type Segments struct {
	Model, Effort, FastMode, Project, Branch, Session, Week, Today, Context, Cache, Duration bool
}

// Thresholds are the percentages where colors and warnings change.
type Thresholds struct {
	HeadroomGreen, HeadroomYellow, ContextWarn, ContextCritical float64
}

// Config is paceline's merged configuration.
type Config struct {
	Segments   Segments
	Thresholds Thresholds
	// QuietEfforts are effort levels considered everyday; others get a label.
	QuietEfforts []string
	// ProjectSlots pins project folder names to color slots 0-11.
	ProjectSlots map[string]int
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Segments: Segments{
			Model: true, Effort: true, FastMode: true, Project: true, Branch: true,
			Session: true, Week: true, Today: true, Context: true, Cache: true, Duration: true,
		},
		Thresholds:   Thresholds{HeadroomGreen: 30, HeadroomYellow: 15, ContextWarn: 70, ContextCritical: 85},
		QuietEfforts: []string{"low", "medium"},
		ProjectSlots: map[string]int{},
	}
}

// Dir is Claude Code's config directory, honoring CLAUDE_CONFIG_DIR.
func Dir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// fileShape mirrors paceline.json with raw values, so each key can be
// validated on its own and a bad key never discards the good ones.
type fileShape struct {
	Segments     map[string]json.RawMessage `json:"segments"`
	Thresholds   map[string]json.RawMessage `json:"thresholds"`
	QuietEfforts json.RawMessage            `json:"quietEfforts"`
	ProjectSlots map[string]json.RawMessage `json:"projectSlots"`
}

// Merge applies a paceline.json document to the defaults. Only known keys
// with the right type and range are taken, so a malformed file degrades to
// defaults instead of breaking rendering.
func Merge(data []byte) Config {
	c := Default()
	var f fileShape
	if json.Unmarshal(data, &f) != nil {
		return c
	}

	segments := map[string]*bool{
		"model": &c.Segments.Model, "effort": &c.Segments.Effort, "fastMode": &c.Segments.FastMode,
		"project": &c.Segments.Project, "branch": &c.Segments.Branch, "session": &c.Segments.Session,
		"week": &c.Segments.Week, "today": &c.Segments.Today, "context": &c.Segments.Context,
		"cache": &c.Segments.Cache, "duration": &c.Segments.Duration,
	}
	for key, raw := range f.Segments {
		var v bool
		if target, ok := segments[key]; ok && json.Unmarshal(raw, &v) == nil {
			*target = v
		}
	}

	thresholds := map[string]*float64{
		"headroomGreen": &c.Thresholds.HeadroomGreen, "headroomYellow": &c.Thresholds.HeadroomYellow,
		"contextWarn": &c.Thresholds.ContextWarn, "contextCritical": &c.Thresholds.ContextCritical,
	}
	for key, raw := range f.Thresholds {
		var v float64
		if target, ok := thresholds[key]; ok && json.Unmarshal(raw, &v) == nil && v >= 0 && v <= 100 {
			*target = v
		}
	}

	var efforts []string
	if len(f.QuietEfforts) > 0 && json.Unmarshal(f.QuietEfforts, &efforts) == nil && efforts != nil {
		c.QuietEfforts = efforts
	}

	for name, raw := range f.ProjectSlots {
		var v float64
		if json.Unmarshal(raw, &v) == nil && v == math.Trunc(v) && v >= 0 && v < 12 {
			c.ProjectSlots[name] = int(v)
		}
	}
	return c
}

// Load reads and merges the config file at path. A missing, unreadable, or
// oversized file yields the defaults.
func Load(path string) Config {
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxConfigBytes {
		return Default()
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: the user's own config file.
	if err != nil {
		return Default()
	}
	return Merge(data)
}
