package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const exe = "/opt/paceline/bin/paceline"

func setup(t *testing.T, settings string) (dir, settingsPath string) {
	t.Helper()
	dir = t.TempDir()
	settingsPath = filepath.Join(dir, "settings.json")
	if settings != "" {
		if err := os.WriteFile(settingsPath, []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, settingsPath
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func statusLineOf(t *testing.T, path string) map[string]any {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal([]byte(read(t, path)), &s); err != nil {
		t.Fatal(err)
	}
	sl, _ := s["statusLine"].(map[string]any)
	return sl
}

func TestBuildCommand(t *testing.T) {
	good := map[string]string{
		exe:                                 `"/opt/paceline/bin/paceline"`,
		`C:\Users\me\go\bin\paceline.exe`:   `"C:/Users/me/go/bin/paceline.exe"`,
		"/Users/Jane Doe/go/bin/paceline":   `"/Users/Jane Doe/go/bin/paceline"`,
		"/home/u/.local/bin/paceline-1.2.3": `"/home/u/.local/bin/paceline-1.2.3"`,
	}
	for in, want := range good {
		if got, err := BuildCommand(in); err != nil || got != want {
			t.Errorf("BuildCommand(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		`/tmp/x"; rm -rf ~; echo "/paceline`,
		"/tmp/$(curl evil)/paceline",
		"/tmp/`id`/paceline",
		`C:\Users\%USERPROFILE%\paceline.exe`,
		`C:\Users\a!b!\paceline.exe`,
		"/tmp/a\nb/paceline",
	} {
		if _, err := BuildCommand(in); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Errorf("BuildCommand(%q) accepted an unsafe path", in)
		}
	}
}

func TestInstallCreatesSettings(t *testing.T) {
	dir, path := setup(t, "")
	r, err := Install(dir, exe, false)
	if err != nil || r.Status != Installed || r.Backup != "" {
		t.Fatalf("Install = %+v, %v", r, err)
	}
	if got := read(t, path); got != "{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"\\\"/opt/paceline/bin/paceline\\\"\",\n    \"padding\": 0\n  }\n}\n" {
		t.Errorf("settings.json =\n%s", got)
	}
}

func TestInstallCreatesMissingConfigDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh", ".claude")
	if _, err := Install(dir, exe, false); err != nil {
		t.Fatal(err)
	}
}

func TestInstallPreservesOrderValuesAndBacksUp(t *testing.T) {
	original := `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls)"], "note": "a < b && c > d"},
  "hooks": {},
  "big": 12345678901234567890,
  "zeta": true
}`
	dir, path := setup(t, original)
	r, err := Install(dir, exe, false)
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	order := []string{`"model"`, `"permissions"`, `"hooks"`, `"big"`, `"zeta"`, `"statusLine"`}
	last := -1
	for _, key := range order {
		i := strings.Index(got, key)
		if i <= last {
			t.Fatalf("key %s out of order in\n%s", key, got)
		}
		last = i
	}
	for _, kept := range []string{`"a < b && c > d"`, "12345678901234567890"} {
		if !strings.Contains(got, kept) {
			t.Errorf("value %s was rewritten:\n%s", kept, got)
		}
	}
	if read(t, r.Backup) != original {
		t.Error("backup does not match the original bytes")
	}
}

func TestInstallIsIdempotentAndUpdatesItsOwnEntry(t *testing.T) {
	dir, path := setup(t, "{}")
	if _, err := Install(dir, exe, false); err != nil {
		t.Fatal(err)
	}
	if r, _ := Install(dir, exe, false); r.Status != Unchanged {
		t.Errorf("second install: %s", r.Status)
	}
	r, err := Install(dir, "/new/paceline", false)
	if err != nil || r.Status != Updated {
		t.Fatalf("moved install: %+v, %v", r, err)
	}
	if cmd := statusLineOf(t, path)["command"]; cmd != `"/new/paceline"` {
		t.Errorf("command = %v", cmd)
	}
}

func TestInstallRefusesToReplaceAnotherStatusLine(t *testing.T) {
	original := `{"statusLine": {"type": "command", "command": "bash ~/my-line.sh"}}`
	dir, path := setup(t, original)
	if _, err := Install(dir, exe, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v", err)
	}
	if read(t, path) != original {
		t.Error("settings changed despite the refusal")
	}
}

func TestForceInstallThenUninstallRestores(t *testing.T) {
	dir, path := setup(t, `{"statusLine": {"type": "command", "command": "bash ~/my-line.sh"}, "theme": "dark"}`)
	r, err := Install(dir, exe, true)
	if err != nil || !strings.Contains(string(r.Replaced), "my-line.sh") {
		t.Fatalf("Install = %+v, %v", r, err)
	}
	u, err := Uninstall(dir)
	if err != nil || u.Status != Uninstalled || !strings.Contains(string(u.Restored), "my-line.sh") {
		t.Fatalf("Uninstall = %+v, %v", u, err)
	}
	if cmd := statusLineOf(t, path)["command"]; cmd != "bash ~/my-line.sh" {
		t.Errorf("restored command = %v", cmd)
	}
	if !strings.Contains(read(t, path), `"theme": "dark"`) {
		t.Error("other settings lost")
	}
	if _, err := os.Stat(filepath.Join(dir, "paceline-install.json")); !os.IsNotExist(err) {
		t.Error("install record left behind")
	}
}

func TestNeverOverwritesUnparseableSettings(t *testing.T) {
	for _, bad := range []string{`{ "model": "opus", }`, `[1,2]`, `null`, `{"a":1} {"b":2}`, `{"a":`} {
		dir, path := setup(t, bad)
		if _, err := Install(dir, exe, false); err == nil || !strings.Contains(err.Error(), "refusing to edit") {
			t.Errorf("%q: err = %v", bad, err)
		}
		if read(t, path) != bad {
			t.Errorf("%q was modified", bad)
		}
	}
}

func TestEmptySettingsFileIsEmptySettings(t *testing.T) {
	dir, path := setup(t, "  \n")
	if _, err := Install(dir, exe, false); err != nil {
		t.Fatal(err)
	}
	if statusLineOf(t, path) == nil {
		t.Error("no statusLine written")
	}
}

func TestUnsafePathLeavesSettingsAlone(t *testing.T) {
	dir, path := setup(t, `{"model": "opus"}`)
	if _, err := Install(dir, "/tmp/$(id)/paceline", false); err == nil {
		t.Fatal("unsafe path accepted")
	}
	if read(t, path) != `{"model": "opus"}` {
		t.Error("settings changed")
	}
}

func TestNoTempFilesLeft(t *testing.T) {
	dir, _ := setup(t, "{}")
	if _, err := Install(dir, exe, false); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("temp files left: %v", matches)
	}
}

func TestWritesThroughSymlinkedSettings(t *testing.T) {
	dir, path := setup(t, "")
	real := filepath.Join(t.TempDir(), "real-settings.json")
	if err := os.WriteFile(real, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path); err != nil {
		t.Skip("symlinks need extra privileges on this platform")
	}
	if _, err := Install(dir, exe, false); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	if statusLineOf(t, real) == nil {
		t.Error("the link target was not updated")
	}
}

func TestSettingsAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir, path := setup(t, "")
	if _, err := Install(dir, exe, false); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestUninstall(t *testing.T) {
	dir, path := setup(t, `{"model": "opus"}`)
	if _, err := Install(dir, exe, false); err != nil {
		t.Fatal(err)
	}
	if r, err := Uninstall(dir); err != nil || r.Status != Uninstalled || r.Restored != nil {
		t.Fatalf("Uninstall = %+v, %v", r, err)
	}
	if got := read(t, path); got != "{\n  \"model\": \"opus\"\n}\n" {
		t.Errorf("settings.json = %q", got)
	}
}

func TestUninstallLeavesOtherStatusLinesAlone(t *testing.T) {
	original := `{"statusLine": {"command": "bash ~/my-line.sh"}}`
	dir, path := setup(t, original)
	if r, _ := Uninstall(dir); r.Status != NotInstalled {
		t.Errorf("status = %s", r.Status)
	}
	if read(t, path) != original {
		t.Error("another tool's status line was touched")
	}
	empty := t.TempDir()
	if r, _ := Uninstall(empty); r.Status != NotInstalled {
		t.Errorf("no settings: status = %s", r.Status)
	}
}

func TestUninstallIgnoresTamperedRecord(t *testing.T) {
	for _, rec := range []string{`{"previousStatusLine": "rm -rf ~"}`, `{"previousStatusLine": {"command": 42}}`, `nope`} {
		dir, path := setup(t, "{}")
		if _, err := Install(dir, exe, false); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "paceline-install.json"), []byte(rec), 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := Uninstall(dir)
		if err != nil || r.Restored != nil {
			t.Errorf("%s: restored %s, %v", rec, r.Restored, err)
		}
		if statusLineOf(t, path) != nil {
			t.Errorf("%s: statusLine left in settings", rec)
		}
	}
}

func TestIsPaceline(t *testing.T) {
	cases := map[string]bool{
		`{"command": "\"/go/bin/paceline\""}`: true,
		`{"command": "bash x.sh"}`:            false,
		`{"command": 42}`:                     false,
		`"paceline"`:                          false,
	}
	for raw, want := range cases {
		if got := IsPaceline(json.RawMessage(raw)); got != want {
			t.Errorf("IsPaceline(%s) = %v", raw, got)
		}
	}
}
