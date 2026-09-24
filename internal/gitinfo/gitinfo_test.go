package gitinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeRepo makes a directory with just .git/HEAD; no git binary needed.
func fakeRepo(t *testing.T, head string) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".git", "HEAD"), head)
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBranch(t *testing.T) {
	tests := map[string]struct{ head, want string }{
		"branch":           {"ref: refs/heads/dev\n", "dev"},
		"CRLF and slashes": {"ref: refs/heads/feature/x\r\n", "feature/x"},
		"no newline":       {"ref: refs/heads/main", "main"},
		"detached":         {strings.Repeat("a", 7) + strings.Repeat("0", 33) + "\n", "detached aaaaaaa"},
		"garbage":          {"garbage\n", ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := Branch(fakeRepo(t, tt.head)); got != tt.want {
				t.Errorf("Branch = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBranchFromNestedFolder(t *testing.T) {
	root := fakeRepo(t, "ref: refs/heads/main\n")
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Branch(nested); got != "main" {
		t.Errorf("Branch = %q", got)
	}
}

func TestBranchTruncatesLongNames(t *testing.T) {
	got := Branch(fakeRepo(t, "ref: refs/heads/"+strings.Repeat("x", 80)+"\n"))
	if utf8.RuneCountInString(got) != maxBranchLength || !strings.HasSuffix(got, "\u2026") {
		t.Errorf("Branch = %q", got)
	}
}

func TestBranchReadsOnlyTheStartOfAHugeHead(t *testing.T) {
	got := Branch(fakeRepo(t, "ref: refs/heads/"+strings.Repeat("y", 10_000_000)))
	if utf8.RuneCountInString(got) > maxBranchLength {
		t.Errorf("Branch returned %d runes", utf8.RuneCountInString(got))
	}
}

func TestWorktrees(t *testing.T) {
	real := t.TempDir()
	mustWrite(t, filepath.Join(real, "HEAD"), "ref: refs/heads/wt-branch\n")
	abs := t.TempDir()
	mustWrite(t, filepath.Join(abs, ".git"), "gitdir: "+real+"\n")
	if got := Branch(abs); got != "wt-branch" {
		t.Errorf("absolute gitdir: Branch = %q", got)
	}

	rel := t.TempDir()
	mustWrite(t, filepath.Join(rel, "gd", "HEAD"), "ref: refs/heads/rel\n")
	mustWrite(t, filepath.Join(rel, ".git"), "gitdir: gd\n")
	if got := Branch(rel); got != "rel" {
		t.Errorf("relative gitdir: Branch = %q", got)
	}
}

func TestNotARepo(t *testing.T) {
	empty := t.TempDir()
	mustWrite(t, filepath.Join(empty, ".git"), "gitdir:\n")
	if got := FindGitDir(empty); got != "" {
		t.Errorf("empty gitdir: FindGitDir = %q", got)
	}
	noPrefix := t.TempDir()
	mustWrite(t, filepath.Join(noPrefix, ".git"), "/somewhere\n")
	if got := FindGitDir(noPrefix); got != "" {
		t.Errorf(".git file without gitdir: FindGitDir = %q", got)
	}
	noHead := t.TempDir()
	if err := os.Mkdir(filepath.Join(noHead, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Branch(noHead); got != "" {
		t.Errorf("missing HEAD: Branch = %q", got)
	}
}
