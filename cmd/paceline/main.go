// Command paceline is a Claude Code status line that paces your weekly usage
// limit across the days you have left.
//
// With no arguments it reads Claude Code's status JSON on stdin and prints
// the status line; install and uninstall edit settings.json.
package main

import (
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
	"github.com/rogadev/paceline/internal/pace"
	"github.com/rogadev/paceline/internal/payload"
	"github.com/rogadev/paceline/internal/render"
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
  paceline uninstall           remove it and restore the previous status line
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
		return runInstall(slices.Contains(args[1:], "--force"), stdout, stderr)
	case "uninstall":
		return runUninstall(stdout, stderr)
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
		statePath := filepath.Join(dir, "paceline-day.json")
		_, noColor := os.LookupEnv("NO_COLOR")
		return render.Render(p, render.Context{
			Now:           time.Now(),
			Config:        config.Load(filepath.Join(dir, "paceline.json")),
			NoColor:       noColor,
			ReadSnapshot:  func() *pace.Snapshot { return pace.ReadSnapshot(statePath) },
			WriteSnapshot: func(s pace.Snapshot) { _ = pace.WriteSnapshot(statePath, s) },
			GitBranch:     gitinfo.Branch,
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
