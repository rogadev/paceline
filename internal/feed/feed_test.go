package feed

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rogadev/paceline/internal/payload"
)

const (
	fiveHourLimit = `{"used_percentage":21,"resets_at":1790283000.7}`
	sevenDayLimit = `{"used_percentage":46.5,"resets_at":1790838000}`
	bothWindows   = `{"rate_limits":{"five_hour":` + fiveHourLimit + `,"seven_day":` + sevenDayLimit + `}}`
)

// t0 is a render time with a fraction of a second, so tests see truncation.
var t0 = time.Unix(1790277013, 600_000_000)

// wantBoth is the feed bothWindows produces at t0.
var wantBoth = Reading{
	Version:   1,
	FiveHour:  &Window{UsedPct: 21, ResetsAt: 1790283000},
	SevenDay:  &Window{UsedPct: 46.5, ResetsAt: 1790838000},
	WrittenAt: 1790277013,
}

func decode(t *testing.T, in string) *payload.Payload {
	t.Helper()
	p, err := payload.Decode([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// readFeed loads the feed at path with Read, after checking that
// paceline-tray would accept its bytes too.
func readFeed(t *testing.T, path string) Reading {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reason := trayRejects(data); reason != "" {
		t.Errorf("%s: %s", reason, data)
	}
	f, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// trayRejects applies paceline-tray's feed rules (internal/usage/feed) to
// data and returns why the tray would reject it, or "" when it would accept
// it.
func trayRejects(data []byte) string {
	type readerWindow struct{ UsedPct, ResetsAt float64 }
	var f struct {
		Version            int
		FiveHour, SevenDay *readerWindow
		WrittenAt          float64
	}
	inRange := func(sec float64) bool { return sec > 0 && sec <= 1<<33 }
	validWindow := func(w *readerWindow) bool {
		return w == nil || (inRange(w.ResetsAt) && w.UsedPct >= 0 && w.UsedPct <= 100)
	}
	switch {
	case len(data) > 4096:
		return fmt.Sprintf("feed is %d bytes, over the reader's 4096", len(data))
	case json.Unmarshal(data, &f) != nil:
		return "feed does not parse"
	case f.Version != 1 || !inRange(f.WrittenAt):
		return "reader rejects version or writtenAt"
	case f.FiveHour == nil && f.SevenDay == nil:
		return "reader rejects a feed with no window"
	case !validWindow(f.FiveHour) || !validWindow(f.SevenDay):
		return "reader rejects a window"
	}
	return ""
}

func TestReadReturnsWhatWriteWrote(t *testing.T) {
	path := Path(t.TempDir())
	if err := Write(path, decode(t, bothWindows), t0); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(path); err != nil || !reflect.DeepEqual(got, wantBoth) {
		t.Errorf("Read = %+v, %v; want %+v", got, err, wantBoth)
	}

	// The range edges, and a null window, are accepted as the tray accepts them.
	edges := `{"version":1,"fiveHour":null,"sevenDay":{"usedPct":100,"resetsAt":1},"writtenAt":8589934592}`
	if err := os.WriteFile(path, []byte(edges), 0o600); err != nil {
		t.Fatal(err)
	}
	want := Reading{Version: 1, SevenDay: &Window{UsedPct: 100, ResetsAt: 1}, WrittenAt: 1 << 33}
	if got, err := Read(path); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Read = %+v, %v; want %+v", got, err, want)
	}
}

func TestReadSizeLimit(t *testing.T) {
	valid := feedText(1, "1790283000", t0.Unix())
	for size, accepted := range map[int]bool{4096: true, 4097: false} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			// Trailing spaces are valid JSON, so only the size can reject the file.
			data := valid + strings.Repeat(" ", size-len(valid))
			if trayAccepts := trayRejects([]byte(data)) == ""; trayAccepts != accepted {
				t.Fatalf("the tray model accepts %d bytes: %v, want %v", size, trayAccepts, accepted)
			}
			path := Path(t.TempDir())
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Read(path)
			if accepted && (err != nil || !reflect.DeepEqual(got, wantBoth)) {
				t.Errorf("a %d-byte feed: Read = %+v, %v; want %+v", size, got, err, wantBoth)
			}
			if !accepted && err == nil {
				t.Errorf("a %d-byte feed was accepted", size)
			}
		})
	}
}

func TestReadMissingFile(t *testing.T) {
	if _, err := Read(Path(t.TempDir())); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing feed should match fs.ErrNotExist, got %v", err)
	}
}

func TestReadRejectsWhatTheTrayRejects(t *testing.T) {
	week := `"sevenDay":{"usedPct":46.5,"resetsAt":1790838000}`
	tests := map[string]string{
		"empty file":         ``,
		"not JSON":           `{not json`,
		"not an object":      `[1]`,
		"other version":      `{"version":2,` + week + `,"writtenAt":1790277013}`,
		"no version":         `{` + week + `,"writtenAt":1790277013}`,
		"no writtenAt":       `{"version":1,` + week + `}`,
		"writtenAt zero":     `{"version":1,` + week + `,"writtenAt":0}`,
		"writtenAt negative": `{"version":1,` + week + `,"writtenAt":-5}`,
		"writtenAt too late": `{"version":1,` + week + `,"writtenAt":8589934593}`,
		"no window":          `{"version":1,"writtenAt":1790277013}`,
		"both windows null":  `{"version":1,"fiveHour":null,"sevenDay":null,"writtenAt":1790277013}`,
		"usedPct negative":   `{"version":1,"sevenDay":{"usedPct":-1,"resetsAt":1790838000},"writtenAt":1790277013}`,
		"usedPct over 100":   `{"version":1,"sevenDay":{"usedPct":101,"resetsAt":1790838000},"writtenAt":1790277013}`,
		"resetsAt missing":   `{"version":1,"fiveHour":{"usedPct":21},` + week + `,"writtenAt":1790277013}`,
		"resetsAt zero":      `{"version":1,"fiveHour":{"usedPct":21,"resetsAt":0},` + week + `,"writtenAt":1790277013}`,
		"resetsAt too late":  `{"version":1,"fiveHour":{"usedPct":21,"resetsAt":8589934593},` + week + `,"writtenAt":1790277013}`,
		"empty window":       `{"version":1,"fiveHour":{},` + week + `,"writtenAt":1790277013}`,
		"oversized":          `{"version":1,` + week + `,"writtenAt":1790277013,"pad":"` + strings.Repeat("x", 4096) + `"}`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if trayRejects([]byte(data)) == "" {
				t.Fatalf("the tray model accepts this case, so it proves nothing about Read: %.200s", data)
			}
			path := Path(t.TempDir())
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Read(path)
			if err == nil {
				t.Fatalf("Read accepted a feed the tray rejects: %.200s", data)
			}
			if errors.Is(err, fs.ErrNotExist) {
				t.Errorf("an invalid feed reads as missing: %v", err)
			}
		})
	}
}

func TestReadUnreadableFileIsNotMissing(t *testing.T) {
	// A directory at the feed's path exists but cannot be read as a file.
	path := Path(t.TempDir())
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a directory at the feed path: err = %v, want an error that is not fs.ErrNotExist", err)
	}
}

func TestPath(t *testing.T) {
	dir := filepath.Join("home", ".claude")
	if got, want := Path(dir), filepath.Join(dir, "paceline-feed.json"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestWriteBothWindows(t *testing.T) {
	path := Path(t.TempDir())
	if err := Write(path, decode(t, bothWindows), t0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 4 || keys["version"] == nil || keys["fiveHour"] == nil || keys["sevenDay"] == nil || keys["writtenAt"] == nil {
		t.Errorf("keys: %s", data)
	}
	if got := readFeed(t, path); !reflect.DeepEqual(got, wantBoth) {
		t.Errorf("feed = %s", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestWriteNothingWithoutAValidWindow(t *testing.T) {
	invalid := `{"used_percentage":101,"resets_at":1790283000}`
	for name, in := range map[string]string{
		"no rate_limits":         `{"model":{"display_name":"Opus"}}`,
		"null rate_limits":       `{"rate_limits":null}`,
		"empty rate_limits":      `{"rate_limits":{}}`,
		"both windows invalid":   `{"rate_limits":{"five_hour":` + invalid + `,"seven_day":` + invalid + `}}`,
		"windows of wrong types": `{"rate_limits":{"five_hour":{"used_percentage":"21","resets_at":"1"},"seven_day":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := Write(Path(dir), decode(t, in), t0); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
				t.Errorf("created a feed file (stat err %v)", err)
			}

			seed := []byte(`{"version":1,"sevenDay":{"usedPct":3,"resetsAt":5},"writtenAt":7}`)
			if err := os.WriteFile(Path(dir), seed, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Write(Path(dir), decode(t, in), t0); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(Path(dir)); !bytes.Equal(got, seed) {
				t.Errorf("existing feed changed to %s", got)
			}
		})
	}
}

// feedText renders a feed with bothWindows' windows, the given version, and
// a five-hour resetsAt written as text, so a seed can be malformed.
func feedText(version int, fiveHourResets string, writtenAt int64) string {
	return fmt.Sprintf(`{"version":%d,"fiveHour":{"usedPct":21,"resetsAt":%s},"sevenDay":{"usedPct":46.5,"resetsAt":1790838000},"writtenAt":%d}`,
		version, fiveHourResets, writtenAt)
}

func TestWriteSkipsAnUnchangedRecentFeed(t *testing.T) {
	onlySevenDay := `{"rate_limits":{"seven_day":` + sevenDayLimit + `}}`
	changed := `{"rate_limits":{"five_hour":{"used_percentage":22,"resets_at":1790283000},"seven_day":` + sevenDayLimit + `}}`
	onlyFiveHour := `{"rate_limits":{"five_hour":` + fiveHourLimit + `}}`
	changedSevenDay := `{"rate_limits":{"five_hour":` + fiveHourLimit + `,"seven_day":{"used_percentage":47,"resets_at":1790838000}}}`
	movedReset := `{"rate_limits":{"five_hour":{"used_percentage":21,"resets_at":1790283001.7},"seven_day":` + sevenDayLimit + `}}`
	tests := []struct {
		name    string
		seed    string // the existing feed; empty means bothWindows written at t0
		in      string
		now     time.Time
		rewrite bool
	}{
		{name: "same windows at once", in: bothWindows, now: t0},
		{name: "same windows after 29s", in: bothWindows, now: t0.Add(29 * time.Second)},
		{name: "same windows after 30s", in: bothWindows, now: t0.Add(30 * time.Second), rewrite: true},
		{name: "changed usage", in: changed, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "changed seven-day usage", in: changedSevenDay, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "changed resetsAt only", in: movedReset, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "window disappears", in: onlySevenDay, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "seven-day window disappears", in: onlyFiveHour, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "single window unchanged", seed: fmt.Sprintf(`{"version":1,"fiveHour":{"usedPct":21,"resetsAt":1790283000},"writtenAt":%d}`, t0.Unix()),
			in: onlyFiveHour, now: t0.Add(5 * time.Second)},
		{name: "window appears", seed: `{"version":1,"sevenDay":{"usedPct":46.5,"resetsAt":1790838000},"writtenAt":1790277013}`,
			in: bothWindows, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "written in the future", seed: feedText(1, "1790283000", t0.Unix()+60),
			in: bothWindows, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "other version", seed: feedText(2, "1790283000", t0.Unix()),
			in: bothWindows, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "fractional resetsAt", seed: feedText(1, "1790283000.5", t0.Unix()),
			in: bothWindows, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "not JSON", seed: "{not json", in: bothWindows, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "oversized", seed: strings.TrimSuffix(feedText(1, "1790283000", t0.Unix()), "}") + `,"pad":"` + strings.Repeat("x", 4096) + `"}`,
			in: bothWindows, now: t0.Add(5 * time.Second), rewrite: true},
		{name: "clock before 1970", in: changed, now: time.Unix(0, 0)},
		{name: "clock past the reader's range", in: changed, now: time.Unix(1<<33+1, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := Path(t.TempDir())
			if tt.seed == "" {
				if err := Write(path, decode(t, bothWindows), t0); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(tt.seed), 0o600); err != nil {
				t.Fatal(err)
			}
			// An old modification time shows a rewrite even when the bytes match.
			old := time.Now().Add(-time.Hour).Truncate(time.Second)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if err := Write(path, decode(t, tt.in), tt.now); err != nil {
				t.Fatal(err)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			rewritten := !bytes.Equal(before, after) || !info.ModTime().Equal(old)
			if rewritten != tt.rewrite {
				t.Fatalf("rewritten = %v, want %v; feed %.200s", rewritten, tt.rewrite, after)
			}
			if tt.rewrite {
				if got := readFeed(t, path); got.WrittenAt != tt.now.Unix() {
					t.Errorf("writtenAt = %d, want %d", got.WrittenAt, tt.now.Unix())
				}
			}
		})
	}
}

func TestWriteLeavesOutInvalidWindows(t *testing.T) {
	tests := []struct {
		fiveHour string
		want     *Window
	}{
		{`null`, nil},
		{`{}`, nil},
		{`{"resets_at":1790283000}`, nil},
		{`{"used_percentage":21}`, nil},
		{`{"used_percentage":-1,"resets_at":1790283000}`, nil},
		{`{"used_percentage":101,"resets_at":1790283000}`, nil},
		{`{"used_percentage":"90","resets_at":1790283000}`, nil},
		{`{"used_percentage":21,"resets_at":"1790283000"}`, nil},
		{`{"used_percentage":21,"resets_at":0.5}`, nil},
		{`{"used_percentage":21,"resets_at":0}`, nil},
		{`{"used_percentage":21,"resets_at":-5}`, nil},
		{`{"used_percentage":21,"resets_at":8589934593}`, nil},
		{`{"used_percentage":0,"resets_at":1790283000}`, &Window{UsedPct: 0, ResetsAt: 1790283000}},
		{`{"used_percentage":100,"resets_at":1790283000}`, &Window{UsedPct: 100, ResetsAt: 1790283000}},
		{`{"used_percentage":21,"resets_at":1}`, &Window{UsedPct: 21, ResetsAt: 1}},
		{`{"used_percentage":21,"resets_at":8589934592}`, &Window{UsedPct: 21, ResetsAt: 1 << 33}},
	}
	for _, tt := range tests {
		t.Run(tt.fiveHour, func(t *testing.T) {
			path := Path(t.TempDir())
			in := `{"rate_limits":{"five_hour":` + tt.fiveHour + `,"seven_day":` + sevenDayLimit + `}}`
			if err := Write(path, decode(t, in), t0); err != nil {
				t.Fatal(err)
			}
			got := readFeed(t, path)
			if !reflect.DeepEqual(got.FiveHour, tt.want) || !reflect.DeepEqual(got.SevenDay, wantBoth.SevenDay) {
				t.Errorf("fiveHour = %+v, sevenDay = %+v", got.FiveHour, got.SevenDay)
			}
		})
	}
}

func TestWriteFailureLeavesNoTempFile(t *testing.T) {
	// A non-empty directory at the target makes the rename fail on every OS.
	dir := t.TempDir()
	path := Path(dir)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if Write(path, decode(t, bothWindows), t0) == nil {
		t.Error("writing over a non-empty directory reported success")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
	if Write(filepath.Join(dir, "no", "such", "feed.json"), decode(t, bothWindows), t0) == nil {
		t.Error("writing into a missing directory reported success")
	}
}
