// Package gitinfo reads the current git branch from .git/HEAD directly.
//
// No git process is started: that keeps each refresh fast, and it means a
// repo's git config (hooks, fsmonitor, aliases) can never run code just
// because paceline rendered.
package gitinfo

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxHeadBytes    = 512
	maxBranchLength = 32
)

var (
	refPattern = regexp.MustCompile(`^ref:\s*refs/heads/(.+)$`)
	shaPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)
)

// firstLine reads the first line of a file, looking at most maxHeadBytes.
func firstLine(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: reading .git/HEAD is the point.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	line, err := bufio.NewReader(io.LimitReader(f, maxHeadBytes)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// FindGitDir returns the git directory for dir, walking up to the filesystem
// root, or "" outside a repo. In a worktree or submodule, .git is a file
// holding "gitdir: <path>".
func FindGitDir(dir string) string {
	current, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		dotGit := filepath.Join(current, ".git")
		if info, err := os.Stat(dotGit); err == nil {
			if info.IsDir() {
				return dotGit
			}
			if info.Mode().IsRegular() {
				line, err := firstLine(dotGit)
				target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
				if err != nil || !strings.HasPrefix(line, "gitdir:") || target == "" {
					return ""
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(current, target)
				}
				return target
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// Branch returns the branch name, "detached <sha7>", or "" outside a repo.
// The result is raw repo data: callers must sanitize it before printing.
func Branch(dir string) string {
	gitDir := FindGitDir(dir)
	if gitDir == "" {
		return ""
	}
	head, err := firstLine(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	if m := refPattern.FindStringSubmatch(head); m != nil {
		branch := strings.TrimSpace(m[1])
		if utf8.RuneCountInString(branch) > maxBranchLength {
			branch = string([]rune(branch)[:maxBranchLength-1]) + "\u2026"
		}
		return branch
	}
	if trimmed := strings.TrimSpace(head); shaPattern.MatchString(trimmed) {
		return "detached " + trimmed[:7]
	}
	return ""
}
