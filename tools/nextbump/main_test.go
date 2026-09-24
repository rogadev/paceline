package main

import (
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := map[string]Bump{
		"fix: handle CRLF in HEAD":                                   Patch,
		"perf(git): read fewer bytes":                                Patch,
		"feat: add a cost segment":                                   Minor,
		"feat(render): add a cost segment":                           Minor,
		"feat!: rename config keys":                                  Major,
		"fix(config)!: drop legacy thresholds":                       Major,
		"refactor: split\n\nBREAKING CHANGE: Render takes a Context": Major,
		"chore: tidy\n\nBREAKING-CHANGE: Go 1.26 required":           Major,
		"docs: mention BREAKING CHANGE: policy":                      None,
		"docs: explain versioning":                                   None,
		"test: cover worktrees":                                      None,
		"Merge pull request #1 from rogadev/dev":                     None,
		"feat:missing space":                                         None,
		"":                                                           None,
	}
	for msg, want := range tests {
		if got := Classify(msg); got != want {
			t.Errorf("Classify(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestHighest(t *testing.T) {
	tests := []struct {
		msgs []string
		want Bump
	}{
		{[]string{"docs: a", "fix: b"}, Patch},
		{[]string{"fix: a", "feat: b", "docs: c"}, Minor},
		{[]string{"feat: a", "fix!: b"}, Major},
		{[]string{"docs: a", "chore: b"}, None},
		{nil, None},
	}
	for _, tt := range tests {
		if got := Highest(tt.msgs); got != tt.want {
			t.Errorf("Highest(%q) = %v, want %v", tt.msgs, got, tt.want)
		}
	}
}

func TestNext(t *testing.T) {
	tests := []struct {
		current string
		bump    Bump
		want    string
	}{
		{"1.2.3", Patch, "1.2.4"},
		{"1.2.3", Minor, "1.3.0"},
		{"1.2.3", Major, "2.0.0"},
		{"", Patch, "1.0.0"},
	}
	for _, tt := range tests {
		if got, ok := Next(tt.current, tt.bump); !ok || got != tt.want {
			t.Errorf("Next(%q, %v) = %q", tt.current, tt.bump, got)
		}
	}
	if _, ok := Next("1.2.3", None); ok {
		t.Error("Next with no bump reported a release")
	}
}

func TestReport(t *testing.T) {
	got := report("1.0.0", []string{"feat: go rewrite", "docs: readme"})
	for _, want := range []string{"releases **1.1.0** (minor bump from 1.0.0)", "- `minor` feat: go rewrite", "- `none` docs: readme"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	if got := report("", []string{"docs: a"}); !strings.HasPrefix(got, "No release: none of the 1 commit(s)") {
		t.Errorf("got %q", got)
	}
}
