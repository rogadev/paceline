// Package install adds or removes paceline as Claude Code's status line in
// settings.json.
//
// This edits a file the user owns, so it is deliberately conservative:
//   - an unparseable settings.json is never overwritten
//   - an existing status line from something else is only replaced with
//     --force, and is remembered so Uninstall can put it back
//   - a backup copy is written before the first change
//   - writes go through a temp file and rename, so a crash cannot truncate it
//   - top-level key order and every other value are preserved
//   - the command written is validated so an install path cannot inject shell
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const marker = "paceline"

// unsafePathChars could break out of the double-quoted path in the command or
// be expanded by a shell: bash expands $ ` \ and "; cmd.exe expands % and !.
const unsafePathChars = "\"`$\\%!\r\n"

// statusLine is the settings.json entry. A struct, not a map, so the keys
// are written in this order.
type statusLine struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Padding int    `json:"padding"`
}

// Status is what Install or Uninstall did.
type Status string

// Outcomes.
const (
	Installed    Status = "installed"
	Updated      Status = "updated"
	Unchanged    Status = "unchanged"
	Uninstalled  Status = "uninstalled"
	NotInstalled Status = "not-installed"
)

// Result reports what happened.
type Result struct {
	Status   Status
	Backup   string          // path of the settings backup, if one was written
	Replaced json.RawMessage // another tool's status line that was replaced
	Restored json.RawMessage // the status line Uninstall put back
}

// BuildCommand returns the statusLine command for the paceline binary at
// exePath, or an error if the path cannot be quoted safely.
func BuildCommand(exePath string) (string, error) {
	normalized := strings.ReplaceAll(exePath, `\`, "/")
	if strings.ContainsAny(normalized, unsafePathChars) {
		return "", fmt.Errorf("refusing to install: the install path contains characters that are unsafe in a shell command: %s", normalized)
	}
	return `"` + normalized + `"`, nil
}

// IsPaceline reports whether a raw statusLine value is paceline's.
func IsPaceline(raw json.RawMessage) bool {
	var sl struct {
		Command string `json:"command"`
	}
	return json.Unmarshal(raw, &sl) == nil && strings.Contains(sl.Command, marker)
}

func paths(claudeDir string) (settings, record string) {
	return filepath.Join(claudeDir, "settings.json"), filepath.Join(claudeDir, "paceline-install.json")
}

// readSettings loads settings.json. existed is false when the file is absent.
func readSettings(path string) (o *object, data []byte, existed bool, err error) {
	data, err = os.ReadFile(path) //nolint:gosec // G304: Claude Code's settings file.
	if errors.Is(err, os.ErrNotExist) {
		return &object{}, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	o, err = parseObject(data)
	if err != nil {
		return nil, nil, true, fmt.Errorf("refusing to edit %s: it is not a valid JSON object (%w); fix it and rerun", path, err)
	}
	return o, data, true, nil
}

// writeAtomic writes via a temp file and rename, following a symlinked
// settings file to its real target so the link itself survives.
func writeAtomic(path string, data []byte) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	tmp := fmt.Sprintf("%s.%d.tmp", target, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Install makes exePath Claude Code's status line.
func Install(claudeDir, exePath string, force bool) (Result, error) {
	settingsPath, recordPath := paths(claudeDir)
	command, err := BuildCommand(exePath)
	if err != nil {
		return Result{}, err
	}
	o, original, existed, err := readSettings(settingsPath)
	if err != nil {
		return Result{}, err
	}

	current, has := o.get("statusLine")
	ours := has && IsPaceline(current)
	if ours {
		var sl statusLine
		if json.Unmarshal(current, &sl) == nil && sl.Command == command {
			return Result{Status: Unchanged}, nil
		}
	}
	if has && !ours && !force {
		return Result{}, fmt.Errorf("a status line is already configured (%s); rerun with --force to replace it, and 'paceline uninstall' will restore it", current)
	}

	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		return Result{}, err
	}
	var r Result
	if existed {
		r.Backup = settingsPath + ".paceline-backup"
		if err := os.WriteFile(r.Backup, original, 0o600); err != nil {
			return Result{}, err
		}
	}
	if has && !ours {
		r.Replaced = current
		rec, err := json.Marshal(struct {
			PreviousStatusLine json.RawMessage `json:"previousStatusLine"`
		}{current})
		if err != nil {
			return Result{}, err
		}
		if err := writeAtomic(recordPath, rec); err != nil {
			return Result{}, err
		}
	}

	entry, err := marshalNoEscape(statusLine{Type: "command", Command: command})
	if err != nil {
		return Result{}, err
	}
	o.set("statusLine", entry)
	out, err := o.marshal()
	if err != nil {
		return Result{}, err
	}
	if err := writeAtomic(settingsPath, out); err != nil {
		return Result{}, err
	}
	r.Status = Installed
	if ours {
		r.Status = Updated
	}
	return r, nil
}

// Uninstall removes paceline and restores the status line it replaced. It
// never touches a status line that is not paceline's.
func Uninstall(claudeDir string) (Result, error) {
	settingsPath, recordPath := paths(claudeDir)
	o, _, existed, err := readSettings(settingsPath)
	if err != nil {
		return Result{}, err
	}
	current, has := o.get("statusLine")
	if !existed || !has || !IsPaceline(current) {
		return Result{Status: NotInstalled}, nil
	}

	var r Result
	if data, err := os.ReadFile(recordPath); err == nil { //nolint:gosec // G304: paceline's own record.
		var rec struct {
			PreviousStatusLine json.RawMessage `json:"previousStatusLine"`
		}
		var prev struct {
			Command *string `json:"command"`
		}
		// Restore only a well-formed object with a string command; a tampered
		// record is ignored rather than written into settings.
		if json.Unmarshal(data, &rec) == nil && json.Unmarshal(rec.PreviousStatusLine, &prev) == nil && prev.Command != nil {
			r.Restored = rec.PreviousStatusLine
		}
	}
	if r.Restored != nil {
		o.set("statusLine", r.Restored)
	} else {
		o.remove("statusLine")
	}
	out, err := o.marshal()
	if err != nil {
		return Result{}, err
	}
	if err := writeAtomic(settingsPath, out); err != nil {
		return Result{}, err
	}
	_ = os.Remove(recordPath)
	r.Status = Uninstalled
	return r, nil
}
