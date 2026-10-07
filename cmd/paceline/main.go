// Command paceline is a Claude Code status line that paces your usage limits
// and shows, at a glance, where every session stands.
//
// With no arguments it reads Claude Code's status JSON on stdin and prints
// the status line; install and uninstall edit settings.json.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/rogadev/paceline/internal/config"
	"github.com/rogadev/paceline/internal/gitinfo"
	"github.com/rogadev/paceline/internal/install"
	"github.com/rogadev/paceline/internal/payload"
	"github.com/rogadev/paceline/internal/progress"
	"github.com/rogadev/paceline/internal/render"
	"github.com/rogadev/paceline/pace"
)

// version is set at release time with -ldflags "-X main.version=1.2.3".
var version = ""

const (
	// Claude Code's payload is a few KB; anything far larger is not a real payload.
	maxStdinBytes = 1 << 20
	fallback      = "Claude Code"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// buildVersion prefers the release version, then the module version that
// `go install ...@v1.2.3` records, then "dev".
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return "dev"
}

func help() string {
	return fmt.Sprintf(`paceline %s: a Claude Code status line with a daily usage pace

Usage:
  paceline install [--force]   set paceline as your Claude Code status line
    [--regular | --verbose]    label style: "s 18%%" or "session 18%%" (asks if omitted)
  paceline uninstall           remove it and restore the previous status line
  paceline style [regular | verbose]
                               show or change the label style
  paceline --version
  paceline --help

With no arguments, paceline reads Claude Code's status JSON on stdin.
Config: %s
`, buildVersion(), filepath.Join(config.Dir(), "paceline.json"))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		renderFromStdin(stdin, stdout)
		return 0
	}
	switch args[0] {
	case "--version", "-v", "version":
		fmt.Fprintln(stdout, buildVersion())
	case "--help", "-h", "help":
		fmt.Fprint(stdout, help())
	case "install":
		if code := runInstall(slices.Contains(args[1:], "--force"), stdout, stderr); code != 0 {
			return code
		}
		current := config.Load(filepath.Join(config.Dir(), "paceline.json")).Verbose
		verbose, chosen := chooseStyle(args[1:], isTerminal(stdin), current, stdin, stdout)
		if !chosen {
			return 0
		}
		return runSetStyle(verbose, stdout, stderr)
	case "uninstall":
		return runUninstall(stdout, stderr)
	case "style":
		return runStyle(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Unknown command: %s\n\n%s", args[0], help())
		return 1
	}
	return 0
}

// renderFromStdin never fails: any problem prints the fallback label, so the
// status line is never left empty.
func renderFromStdin(stdin io.Reader, stdout io.Writer) {
	line := func() string {
		data, err := io.ReadAll(io.LimitReader(stdin, maxStdinBytes+1))
		if err != nil || len(data) > maxStdinBytes {
			return ""
		}
		p, err := payload.Decode(data)
		if err != nil {
			return ""
		}
		dir := config.Dir()
		statePath := pace.SnapshotPath(dir)
		_, noColor := os.LookupEnv("NO_COLOR")
		// The branch and the progress bar both need the git dir; find it once.
		gitDirs := map[string]string{}
		gitDir := func(dir string) string {
			if g, ok := gitDirs[dir]; ok {
				return g
			}
			g := gitinfo.FindGitDir(dir)
			gitDirs[dir] = g
			return g
		}
		return render.Render(p, render.Context{
			Now:           time.Now(),
			Config:        config.Load(filepath.Join(dir, "paceline.json")),
			NoColor:       noColor,
			ReadSnapshot:  func() *pace.Snapshot { return pace.ReadSnapshot(statePath) },
			WriteSnapshot: func(s pace.Snapshot) { _ = pace.WriteSnapshot(statePath, s) },
			GitBranch:     func(dir string) string { return gitinfo.BranchIn(gitDir(dir)) },
			Progress:      func(dir string) *progress.Run { return progress.Current(gitDir(dir), p.SessionID.V) },
		})
	}()
	if line == "" {
		line = fallback
	}
	fmt.Fprint(stdout, line)
}

// executablePath is the installed binary's real path. A `go run` binary lives
// in a temp build cache that is deleted on exit, so installing it would point
// settings.json at a file that no longer exists.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if strings.Contains(filepath.ToSlash(exe), "/go-build") {
		return "", errors.New("refusing to install a temporary `go run` build; build or install paceline first")
	}
	return exe, nil
}

// resolveExecutable is a variable so tests can install a path other than the
// test binary, which lives in the go-build cache.
var resolveExecutable = executablePath

func runInstall(force bool, stdout, stderr io.Writer) int {
	exe, err := resolveExecutable()
	if err == nil {
		var r install.Result
		if r, err = install.Install(config.Dir(), exe, force); err == nil {
			switch r.Status {
			case install.Unchanged:
				fmt.Fprintln(stdout, "paceline is already your status line.")
			default:
				fmt.Fprintf(stdout, "paceline %s. It appears on the next status line refresh.\n", r.Status)
				if r.Backup != "" {
					fmt.Fprintf(stdout, "Backup of your previous settings: %s\n", r.Backup)
				}
				if r.Replaced != nil {
					fmt.Fprintln(stdout, "Your previous status line was saved; 'paceline uninstall' restores it.")
				}
			}
			return 0
		}
	}
	fmt.Fprintln(stderr, err)
	return 1
}

// chooseStyle picks the label style for install: a flag wins, then the answer
// to a prompt, which defaults to the current style. chosen is false when
// there is nothing to record: a non-interactive install with no flag, or
// input that ends without an answer (stdin from /dev/null or NUL, which look
// like terminals), so a scripted install leaves paceline.json alone.
func chooseStyle(args []string, interactive, current bool, stdin io.Reader, stdout io.Writer) (verbose, chosen bool) {
	switch {
	case slices.Contains(args, "--verbose"):
		return true, true
	case slices.Contains(args, "--regular"):
		return false, true
	case !interactive:
		return current, false
	}
	def := "1"
	if current {
		def = "2"
	}
	fmt.Fprint(stdout, "Choose a label style (s = session, w = week, t = today):\n"+
		"  1) Regular   s 18% \u00b7 w 86% \u00b7 t 13% of 8%\n"+
		"  2) Verbose   session 18% \u00b7 week 86% \u00b7 today 13% of 8% budget\n"+
		"Style ["+def+"]: ")
	answer, err := bufio.NewReader(stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "1", "r", "regular":
		return false, true
	case "2", "v", "verbose":
		return true, true
	case "":
		return current, err == nil
	}
	return current, true
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// runStyle shows the label style, or changes it when given a name. Several
// names are accepted for each style, so "short" or "long" work as well.
func runStyle(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		style := "regular"
		if config.Load(filepath.Join(config.Dir(), "paceline.json")).Verbose {
			style = "verbose"
		}
		fmt.Fprintf(stdout, "Label style: %s. Change it with 'paceline style regular' or 'paceline style verbose'.\n", style)
		return 0
	}
	switch strings.ToLower(args[0]) {
	case "regular", "short", "normal", "compact":
		return runSetStyle(false, stdout, stderr)
	case "verbose", "long":
		return runSetStyle(true, stdout, stderr)
	}
	fmt.Fprintf(stderr, "Unknown style: %s. Choose regular or verbose.\n", args[0])
	return 1
}

func runSetStyle(verbose bool, stdout, stderr io.Writer) int {
	if err := install.SetVerbose(config.Dir(), verbose); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	style := "regular"
	if verbose {
		style = "verbose"
	}
	fmt.Fprintf(stdout, "Label style: %s. Change it any time with 'paceline style'.\n", style)
	return 0
}

func runUninstall(stdout, stderr io.Writer) int {
	r, err := install.Uninstall(config.Dir())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch {
	case r.Status == install.NotInstalled:
		fmt.Fprintln(stdout, "paceline is not your status line; nothing changed.")
	case r.Restored != nil:
		fmt.Fprintln(stdout, "paceline removed; previous status line restored.")
	default:
		fmt.Fprintln(stdout, "paceline removed.")
	}
	return 0
}
