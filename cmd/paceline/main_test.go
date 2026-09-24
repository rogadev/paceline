package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// runCLI calls run with an isolated CLAUDE_CONFIG_DIR and color off.
func runCLI(t *testing.T, stdin string, args ...string) (code int, stdout, stderr, configDir string) {
	t.Helper()
	configDir = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("NO_COLOR", "1")
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String(), configDir
}

func TestRendersFromStdin(t *testing.T) {
	code, out, _, _ := runCLI(t, `{"model":{"display_name":"Opus 5.5 (1M context)"}}`)
	if code != 0 || out != "Opus 5.5" {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestWritesDaySnapshot(t *testing.T) {
	resets := time.Now().Add(4 * 24 * time.Hour).Unix()
	in := `{"rate_limits":{"seven_day":{"used_percentage":10,"resets_at":` + jsonNum(resets) + `}}}`
	_, out, _, dir := runCLI(t, in)
	if !strings.Contains(out, "today's") {
		t.Errorf("stdout %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "paceline-day.json")); err != nil {
		t.Error("no snapshot written")
	}
}

func jsonNum(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestFallsBackOnBadInput(t *testing.T) {
	for _, in := range []string{"", "not json", `{"model":`, "[]", `{"pad":"` + strings.Repeat("x", 2<<20) + `"}`} {
		if code, out, _, _ := runCLI(t, in); code != 0 || out != fallback {
			t.Errorf("input %.20q: code %d, stdout %q", in, code, out)
		}
	}
}

func TestVersionHelpAndUnknown(t *testing.T) {
	if _, out, _, _ := runCLI(t, "", "--version"); strings.TrimSpace(out) != "dev" {
		t.Errorf("--version = %q", out)
	}
	if _, out, _, _ := runCLI(t, "", "--help"); !strings.Contains(out, "paceline install") {
		t.Errorf("--help = %q", out)
	}
	if code, _, errOut, _ := runCLI(t, "", "frobnicate"); code != 1 || !strings.Contains(errOut, "Unknown command") {
		t.Errorf("unknown command: code %d, stderr %q", code, errOut)
	}
}

func TestInstallRefusesGoRunBuilds(t *testing.T) {
	// `go test` binaries live in the go-build cache, just like `go run` ones.
	if code, _, errOut, _ := runCLI(t, "", "install"); code != 1 || !strings.Contains(errOut, "go run") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}

func TestUninstallMessages(t *testing.T) {
	if code, out, _, _ := runCLI(t, "", "uninstall"); code != 0 || !strings.Contains(out, "nothing changed") {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

// TestEndToEnd builds the real binary and drives it the way Claude Code and a
// user would: install, render, uninstall.
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "paceline")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-ldflags", "-X main.version=9.9.9", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	configDir := t.TempDir()
	env := append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir, "NO_COLOR=1")
	cli := func(stdin string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, _ := cli("", "--version"); strings.TrimSpace(out) != "9.9.9" {
		t.Errorf("--version = %q", out)
	}
	if out, err := cli("", "install"); err != nil || !strings.Contains(out, "installed") {
		t.Fatalf("install: %v %s", err, out)
	}
	settings, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		StatusLine struct{ Command string } `json:"statusLine"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		t.Fatal(err)
	}
	if want := `"` + filepath.ToSlash(bin) + `"`; !strings.EqualFold(s.StatusLine.Command, want) {
		t.Errorf("command = %s, want %s", s.StatusLine.Command, want)
	}
	if out, _ := cli(`{"model":{"display_name":"Opus 5.5"},"workspace":{"current_dir":"/r/app"}}`); out != "Opus 5.5 "+string(rune(0x00b7))+" app" {
		t.Errorf("render = %q", out)
	}
	if out, err := cli("", "uninstall"); err != nil || !strings.Contains(out, "removed") {
		t.Fatalf("uninstall: %v %s", err, out)
	}
}

func TestInstallAndUninstallMessages(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	orig := resolveExecutable
	t.Cleanup(func() { resolveExecutable = orig })
	resolveExecutable = func() (string, error) { return "/opt/bin/paceline", nil }

	settings := filepath.Join(configDir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"statusLine":{"command":"bash other.sh"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cli := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := run(args, strings.NewReader(""), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	if code, _, errOut := cli("install"); code != 1 || !strings.Contains(errOut, "--force") {
		t.Errorf("install over another status line: code %d, stderr %q", code, errOut)
	}
	code, out, _ := cli("install", "--force")
	if code != 0 || !strings.Contains(out, "installed") || !strings.Contains(out, "Backup") || !strings.Contains(out, "previous status line was saved") {
		t.Errorf("install --force: code %d, stdout %q", code, out)
	}
	if _, out, _ := cli("install"); !strings.Contains(out, "already your status line") {
		t.Errorf("second install: %q", out)
	}
	if _, out, _ := cli("uninstall"); !strings.Contains(out, "previous status line restored") {
		t.Errorf("uninstall with restore: %q", out)
	}

	if err := os.WriteFile(settings, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cli("install")
	if _, out, _ := cli("uninstall"); strings.TrimSpace(out) != "paceline removed." {
		t.Errorf("plain uninstall: %q", out)
	}

	if err := os.WriteFile(settings, []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := cli("uninstall"); code != 1 || !strings.Contains(errOut, "refusing to edit") {
		t.Errorf("uninstall on broken settings: code %d, stderr %q", code, errOut)
	}
}

func TestBuildVersionPrefersReleaseVersion(t *testing.T) {
	orig := version
	t.Cleanup(func() { version = orig })
	version = "1.2.3"
	if got := buildVersion(); got != "1.2.3" {
		t.Errorf("buildVersion = %q", got)
	}
}
