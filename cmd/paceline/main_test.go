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

// feedConfigDir points CLAUDE_CONFIG_DIR at a new directory, with config as
// its paceline.json when config is not empty, and turns color off.
func feedConfigDir(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("NO_COLOR", "1")
	if config != "" {
		if err := os.WriteFile(filepath.Join(dir, "paceline.json"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func renderStdin(stdin string) (code int, stdout string) {
	var out, errOut bytes.Buffer
	code = run(nil, strings.NewReader(stdin), &out, &errOut)
	return code, out.String()
}

// usagePayload reports 21% of the session and 46% of the week, with resets
// well inside each window and a fraction of a second on the session reset.
func usagePayload() string {
	now := time.Now()
	fiveHour := jsonNum(now.Add(3*time.Hour).Unix()) + ".5"
	sevenDay := jsonNum(now.Add(4 * 24 * time.Hour).Unix())
	return `{"rate_limits":{"five_hour":{"used_percentage":21,"resets_at":` + fiveHour +
		`},"seven_day":{"used_percentage":46,"resets_at":` + sevenDay + `}}}`
}

func TestWritesFeedWhenOn(t *testing.T) {
	dir := feedConfigDir(t, `{"feed": true}`)
	in := usagePayload()
	before := time.Now().Unix()
	if code, out := renderStdin(in); code != 0 || out == fallback {
		t.Fatalf("code %d, stdout %q", code, out)
	}
	after := time.Now().Unix()

	data, err := os.ReadFile(filepath.Join(dir, "paceline-feed.json"))
	if err != nil {
		t.Fatal(err)
	}
	type window struct{ UsedPct, ResetsAt float64 }
	var got struct {
		Version            int
		FiveHour, SevenDay *window
		WrittenAt          float64
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.FiveHour == nil || got.SevenDay == nil {
		t.Fatalf("feed %s", data)
	}
	if got.FiveHour.UsedPct != 21 || got.SevenDay.UsedPct != 46 {
		t.Errorf("usage in feed %s", data)
	}
	if got.FiveHour.ResetsAt != float64(int64(got.FiveHour.ResetsAt)) || got.FiveHour.ResetsAt < float64(before) {
		t.Errorf("fiveHour resetsAt is not whole seconds: %s", data)
	}
	if got.WrittenAt < float64(before) || got.WrittenAt > float64(after) {
		t.Errorf("writtenAt %v outside the render [%d, %d]", got.WrittenAt, before, after)
	}
}

func TestNoFeedWhenOff(t *testing.T) {
	for _, config := range []string{"", `{"feed": false}`, `{"verbose": true}`} {
		dir := feedConfigDir(t, config)
		renderStdin(usagePayload())
		if _, err := os.Stat(filepath.Join(dir, "paceline-feed.json")); !os.IsNotExist(err) {
			t.Errorf("config %q: feed file exists (stat err %v)", config, err)
		}
	}
}

func TestFeedKeptWithoutRateLimits(t *testing.T) {
	dir := feedConfigDir(t, `{"feed": true}`)
	path := filepath.Join(dir, "paceline-feed.json")
	seed := []byte(`{"version":1,"sevenDay":{"usedPct":3,"resetsAt":5},"writtenAt":7}`)
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, out := renderStdin(`{"model":{"display_name":"Opus 5.5"}}`); out != "Opus 5.5" {
		t.Errorf("stdout %q", out)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, seed) {
		t.Errorf("feed changed to %s", got)
	}
}

func TestFailedFeedWriteKeepsStatusLine(t *testing.T) {
	dir := feedConfigDir(t, `{"feed": true}`)
	path := filepath.Join(dir, "paceline-feed.json")
	// A non-empty directory at the feed path makes the write fail on every OS.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := renderStdin(usagePayload())
	if code != 0 || out == fallback || !strings.Contains(out, "s 21%") || !strings.Contains(out, "w 46%") {
		t.Errorf("code %d, stdout %q", code, out)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Errorf("feed path is no longer a directory (err %v)", err)
	}
}

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

func TestShowsOnlyThisSessionsRun(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".git", "paceline")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	run := func(name string) []byte {
		return []byte(`{"version": 1, "name": "` + name + `", "phase": "running", "updatedAt": "` + time.Now().UTC().Format(time.RFC3339) + `",
			"steps": [{"id": "1", "label": "#1", "status": "active"}]}`)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress-aaaa-1111.json"), run("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress-bbbb-2222.json"), run("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd, _ := json.Marshal(repo)
	for session, want := range map[string]string{"aaaa-1111": "mine", "bbbb-2222": "theirs", "cccc-3333": ""} {
		_, out, _, _ := runCLI(t, `{"cwd": `+string(cwd)+`, "session_id": "`+session+`"}`)
		got := ""
		for _, name := range []string{"mine", "theirs"} {
			if strings.Contains(out, " "+name+" ") {
				got += name
			}
		}
		if got != want {
			t.Errorf("session %s: shows %q, want %q (stdout %q)", session, got, want, out)
		}
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

func TestFeedCommand(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	configPath := filepath.Join(configDir, "paceline.json")
	cli := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := run(args, strings.NewReader(""), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	if code, out, _ := cli("feed"); code != 0 || !strings.Contains(out, "Usage feed: off.") || !strings.Contains(out, "paceline feed on") {
		t.Errorf("default feed: code %d, stdout %q", code, out)
	}
	if err := os.WriteFile(configPath, []byte(`{"verbose":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := cli("feed", "ON")
	if code != 0 || !strings.Contains(out, "Usage feed: on.") || !strings.Contains(out, filepath.Join(configDir, "paceline-feed.json")) {
		t.Errorf("feed on: code %d, stdout %q", code, out)
	}
	if got, err := os.ReadFile(configPath); err != nil || !strings.Contains(string(got), `"verbose": true`) || !strings.Contains(string(got), `"feed": true`) {
		t.Errorf("after feed on: err %v, paceline.json %s", err, got)
	}
	if _, out, _ := cli("feed"); !strings.Contains(out, "Usage feed: on.") {
		t.Errorf("after on: %q", out)
	}
	if code, out, _ := cli("feed", "off"); code != 0 || !strings.Contains(out, "Usage feed: off.") {
		t.Errorf("feed off: code %d, stdout %q", code, out)
	}
	if _, out, _ := cli("feed"); !strings.Contains(out, "Usage feed: off.") {
		t.Errorf("after off: %q", out)
	}
	if code, out, errOut := cli("feed", "maybe"); code != 1 || out != "" || !strings.Contains(errOut, "Unknown feed setting: maybe") {
		t.Errorf("unknown setting: code %d, stdout %q, stderr %q", code, out, errOut)
	}

	if err := os.WriteFile(configPath, []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := cli("feed", "on"); code != 1 || !strings.Contains(errOut, "refusing to edit") {
		t.Errorf("broken config: code %d, stderr %q", code, errOut)
	}
}

func TestHelpListsFeed(t *testing.T) {
	if _, out, _, _ := runCLI(t, "", "--help"); !strings.Contains(out, "paceline feed [on | off]") {
		t.Errorf("--help = %q", out)
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
		interactive, current bool
		answer               string
		verbose, chosen, ask bool
	}{
		{[]string{"--verbose"}, true, false, "", true, true, false},
		{[]string{"--regular"}, true, true, "2\n", false, true, false},
		{nil, false, true, "2\n", true, false, false},
		{nil, true, false, "\n", false, true, true},
		{nil, true, true, "\n", true, true, true},
		{nil, true, false, "2\n", true, true, true},
		{nil, true, true, "1\n", false, true, true},
		{nil, true, false, " Verbose \n", true, true, true},
		{nil, true, true, "huh\n", true, true, true},
		// End of input with no answer, as from /dev/null or NUL: record nothing.
		{nil, true, true, "", true, false, true},
	}
	for _, c := range cases {
		var out bytes.Buffer
		verbose, chosen := chooseStyle(c.args, c.interactive, c.current, strings.NewReader(c.answer), &out)
		asked := strings.Contains(out.String(), "Choose a label style")
		if verbose != c.verbose || chosen != c.chosen || asked != c.ask {
			t.Errorf("args %v, interactive %v, current %v, answer %q: verbose %v, chosen %v, asked %v",
				c.args, c.interactive, c.current, c.answer, verbose, chosen, asked)
		}
	}
	var out bytes.Buffer
	chooseStyle(nil, true, true, strings.NewReader(""), &out)
	if !strings.Contains(out.String(), "Style [2]: ") {
		t.Errorf("prompt does not default to the current style: %q", out.String())
	}
}
