// Command nextbump previews which part of the version a merge will bump,
// using the same rules semantic-release applies on main (see
// release.config.js).
//
//	go run ./tools/nextbump [from] [to]    default: <latest tag>..HEAD
//
// In GitHub Actions the result is also written to the job summary, so every
// dev -> main pull request shows "this merge releases 1.3.0 (minor)".
//
// This is a development tool, not part of the paceline binary.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Bump is a semantic-version change, ordered from none to major.
type Bump int

// Bumps.
const (
	None Bump = iota
	Patch
	Minor
	Major
)

func (b Bump) String() string {
	return [...]string{"none", "patch", "minor", "major"}[b]
}

var (
	header         = regexp.MustCompile(`^(\w+)(?:\([^)]*\))?(!)?: \S`)
	breakingFooter = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE: `)
)

// Classify returns the bump for one full commit message.
func Classify(message string) Bump {
	subject, body, _ := strings.Cut(message, "\n")
	m := header.FindStringSubmatch(subject)
	switch {
	case m == nil:
		return None
	case m[2] == "!" || breakingFooter.MatchString(body):
		return Major
	case m[1] == "feat":
		return Minor
	case m[1] == "fix" || m[1] == "perf":
		return Patch
	}
	return None
}

// Highest returns the largest bump among messages.
func Highest(messages []string) Bump {
	best := None
	for _, m := range messages {
		best = max(best, Classify(m))
	}
	return best
}

// Next applies b to "x.y.z". A first release is 1.0.0 regardless of type.
// ok is false when there is nothing to release.
func Next(current string, b Bump) (next string, ok bool) {
	if b == None {
		return "", false
	}
	if current == "" {
		return "1.0.0", true
	}
	var v [3]int
	for i, part := range strings.SplitN(current, ".", 3) {
		v[i], _ = strconv.Atoi(part)
	}
	switch b {
	case Major:
		v = [3]int{v[0] + 1, 0, 0}
	case Minor:
		v = [3]int{v[0], v[1] + 1, 0}
	default:
		v[2]++
	}
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]), true
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return strings.TrimSpace(string(out)), err
}

// report builds the markdown preview.
func report(current string, messages []string) string {
	b := Highest(messages)
	var lines []string
	if next, ok := Next(current, b); ok {
		from := current
		if from == "" {
			from = "nothing yet"
		}
		lines = append(lines, fmt.Sprintf("This merge releases **%s** (%s bump from %s).", next, b, from))
	} else {
		lines = append(lines, fmt.Sprintf("No release: none of the %d commit(s) are feat, fix, perf, or breaking.", len(messages)))
	}
	lines = append(lines, "")
	for _, m := range messages {
		subject, _, _ := strings.Cut(m, "\n")
		lines = append(lines, fmt.Sprintf("- `%s` %s", Classify(m), subject))
	}
	return strings.Join(lines, "\n")
}

// latestTag is the highest version tag in the repo. Not `git describe`: tags
// sit on main's merge commits, which are not ancestors of dev, so describe
// run from dev would miss them.
func latestTag() string {
	tags, _ := git("tag", "--list", "v[0-9]*", "--sort=-v:refname")
	first, _, _ := strings.Cut(tags, "\n")
	return first
}

func main() {
	tag := latestTag()
	from, to := tag, "HEAD"
	if len(os.Args) > 1 {
		from = os.Args[1]
	}
	if len(os.Args) > 2 {
		to = os.Args[2]
	}
	rangeSpec := to
	if from != "" {
		rangeSpec = from + ".." + to
	}
	const sep = "\x1e"
	raw, err := git("log", "--no-merges", "--format=%B"+sep, rangeSpec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "git log failed:", err)
		os.Exit(1)
	}
	var messages []string
	for _, m := range strings.Split(raw, sep) {
		if m = strings.TrimSpace(m); m != "" {
			messages = append(messages, m)
		}
	}
	text := report(strings.TrimPrefix(tag, "v"), messages)
	fmt.Println(text)
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: path set by GitHub Actions.
		if err == nil {
			fmt.Fprintf(f, "## Release preview\n\n%s\n", text)
			_ = f.Close()
		}
	}
}
