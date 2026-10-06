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
	if !strings.Contains(out, "t 0% of ") {
		t.Errorf("stdout %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "paceline-day.json")); err != nil {
		t.Error("no snapshot written")
	}
}

func jsonNum(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestRendersProgressFromTheRepo(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "src")
	file := filepath.Join(repo, ".git", "paceline", "progress.json")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	run := `{"version": 1, "name": "loop", "phase": "running", "updatedAt": "` + time.Now().UTC().Format(time.RFC3339) + `",
		"steps": [{"id": "1", "label": "#1", "status": "done"}, {"id": "2", "label": "#2", "status": "active"}]}`
	if err := os.WriteFile(file, []byte(run), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, _ := json.Marshal(sub)
	_, out, _, _ := runCLI(t, `{"cwd": `+string(cwd)+`}`)
	if !strings.HasSuffix(out, " "+string(rune(0xb7))+" loop "+strings.Repeat(string(rune(0x25b0)), 2)+" #2 1/2") {
		t.Errorf("stdout %q", out)
	}
}

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
	// install records the canonical path: macOS temp dirs sit behind the
	// /var -> /private/var symlink, and Windows runners use 8.3 short names.
	canonical, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"` + filepath.ToSlash(canonical) + `"`; !strings.EqualFold(s.StatusLine.Command, want) {
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

func TestStyleCommand(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	cli := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := run(args, strings.NewReader(""), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	if _, out, _ := cli("style"); !strings.Contains(out, "Label style: regular.") {
		t.Errorf("default style: %q", out)
	}
	if code, out, _ := cli("style", "LONG"); code != 0 || !strings.Contains(out, "verbose") {
		t.Errorf("style long: code %d, stdout %q", code, out)
	}
	if _, out, _ := cli("style"); !strings.Contains(out, "Label style: verbose.") {
		t.Errorf("after long: %q", out)
	}
	if code, _, _ := cli("style", "short"); code != 0 {
		t.Errorf("style short: code %d", code)
	}
	if _, out, _ := cli("style"); !strings.Contains(out, "Label style: regular.") {
		t.Errorf("after short: %q", out)
	}
	if code, _, errOut := cli("style", "huge"); code != 1 || !strings.Contains(errOut, "Unknown style: huge") {
		t.Errorf("unknown style: code %d, stderr %q", code, errOut)
	}

	if err := os.WriteFile(filepath.Join(configDir, "paceline.json"), []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := cli("style", "verbose"); code != 1 || !strings.Contains(errOut, "refusing to edit") {
		t.Errorf("broken config: code %d, stderr %q", code, errOut)
	}
}

func TestInstallRecordsStyleOnlyWhenChosen(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	orig := resolveExecutable
	t.Cleanup(func() { resolveExecutable = orig })
	resolveExecutable = func() (string, error) { return "/opt/bin/paceline", nil }
	configPath := filepath.Join(configDir, "paceline.json")

	var out, errOut bytes.Buffer
	if code := run([]string{"install"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("install: code %d, stderr %q", code, errOut.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("a non-interactive install without a flag wrote paceline.json")
	}

	out.Reset()
	if code := run([]string{"install", "--verbose"}, strings.NewReader(""), &out, &errOut); code != 0 || !strings.Contains(out.String(), "Label style: verbose") {
		t.Errorf("install --verbose: code %d, stdout %q", code, out.String())
	}
	if b, _ := os.ReadFile(configPath); !strings.Contains(string(b), `"verbose": true`) {
		t.Errorf("paceline.json = %s", b)
	}
}

func TestChooseStyle(t *testing.T) {
	cases := []struct {
		args                 []string
		interactive          bool
		answer               string
		verbose, chosen, ask bool
	}{
		{[]string{"--verbose"}, true, "", true, true, false},
		{[]string{"--regular"}, true, "2\n", false, true, false},
		{nil, false, "2\n", false, false, false},
		{nil, true, "\n", false, true, true},
		{nil, true, "2\n", true, true, true},
		{nil, true, " Verbose \n", true, true, true},
		{nil, true, "", false, true, true},
	}
	for _, c := range cases {
		var out bytes.Buffer
		verbose, chosen := chooseStyle(c.args, c.interactive, strings.NewReader(c.answer), &out)
		asked := strings.Contains(out.String(), "Choose a label style")
		if verbose != c.verbose || chosen != c.chosen || asked != c.ask {
			t.Errorf("args %v, interactive %v, answer %q: verbose %v, chosen %v, asked %v",
				c.args, c.interactive, c.answer, verbose, chosen, asked)
		}
	}
}
