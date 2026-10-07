// Package feed writes the latest rate-limit usage from Claude Code's status
// payload to paceline-feed.json, so other local tools, such as paceline-tray,
// can read it without asking Anthropic, and reads it back for paceline's own
// tools.
//
// The file format is a public contract, enforced by paceline-tray's reader
// (internal/usage/feed): version 1, at most 4096 bytes, usedPct from 0 to
// 100, resetsAt and writtenAt as positive whole Unix seconds, and at least
// one window.
package feed

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rogadev/paceline/internal/payload"
)

const (
	formatVersion = 1
	fileName      = "paceline-feed.json"
	maxFileBytes  = 4096
	// rewriteAfter is how long an unchanged feed goes before writtenAt is
	// refreshed, so an active session does not rewrite it on every render.
	rewriteAfter = 30 * time.Second
	// maxUnixSeconds (about the year 2242) is the largest timestamp the
	// reader accepts.
	maxUnixSeconds = 1 << 33
)

// Window is one rate-limit window in the feed: the five-hour session or the
// seven-day week.
type Window struct {
	UsedPct  float64 `json:"usedPct"`
	ResetsAt int64   `json:"resetsAt"` // Unix seconds
}

// Reading is one feed file: the windows paceline last saw and when it saved
// them. Either window may be nil, but not both.
type Reading struct {
	Version   int     `json:"version"`
	FiveHour  *Window `json:"fiveHour,omitempty"`
	SevenDay  *Window `json:"sevenDay,omitempty"`
	WrittenAt int64   `json:"writtenAt"` // Unix seconds
}

// Path is where the feed lives inside configDir, Claude Code's config
// directory.
func Path(configDir string) string {
	return filepath.Join(configDir, fileName)
}

// Read loads the feed at path and checks it against the rules paceline-tray's
// reader applies, so every caller sees only a feed the tray would accept.
//
// A missing file returns an error matching fs.ErrNotExist. Any other problem
// (unreadable, over 4096 bytes, malformed, another version, a timestamp or
// usage out of range, or no window) returns an error that does not.
// Fractional timestamps are rejected; the writer never produces them.
func Read(path string) (Reading, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is paceline's own feed file.
	if err != nil {
		return Reading{}, fmt.Errorf("feed: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Reading{}, fmt.Errorf("feed: %w", err)
	}
	if len(data) > maxFileBytes {
		return Reading{}, fmt.Errorf("feed: file is larger than %d bytes", maxFileBytes)
	}
	var r Reading
	if err := json.Unmarshal(data, &r); err != nil {
		return Reading{}, fmt.Errorf("feed: %w", err)
	}
	switch {
	case r.Version != formatVersion:
		return Reading{}, fmt.Errorf("feed: version %d, want %d", r.Version, formatVersion)
	case !validTimestamp(r.WrittenAt):
		return Reading{}, errors.New("feed: missing or invalid writtenAt")
	case r.FiveHour == nil && r.SevenDay == nil:
		return Reading{}, errors.New("feed: no window")
	case !validWindow(r.FiveHour):
		return Reading{}, errors.New("feed: invalid fiveHour window")
	case !validWindow(r.SevenDay):
		return Reading{}, errors.New("feed: invalid sevenDay window")
	}
	return r, nil
}

// Write saves p's rate-limit windows to path as a version 1 feed stamped with
// now, through a temp file and rename so a reader never sees a half-written
// file. A window the reader would reject is left out.
//
// It writes nothing when no window is valid, so a payload without usage
// never replaces a good feed, and nothing when now is outside the range the
// reader accepts. It also skips the write when the file already holds the
// same windows and was written under 30 seconds ago.
//
// Errors are returned for tests; the status line ignores them, since the
// next render retries.
func Write(path string, p *payload.Payload, now time.Time) error {
	next := Reading{Version: formatVersion, WrittenAt: now.Unix()}
	if p.RateLimits != nil {
		next.FiveHour = toWindow(p.RateLimits.FiveHour)
		next.SevenDay = toWindow(p.RateLimits.SevenDay)
	}
	if next.FiveHour == nil && next.SevenDay == nil {
		return nil
	}
	if !validTimestamp(next.WrittenAt) {
		return nil
	}
	if prev, err := Read(path); err == nil && isRecentCopy(prev, next) {
		return nil
	}

	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// toWindow converts a payload limit to a feed window, or nil when the limit
// is missing or the reader would reject it. resetsAt is truncated to whole
// seconds, as every paceline-tray source does.
func toWindow(l *payload.Limit) *Window {
	if l == nil || !l.UsedPercentage.Set || !l.ResetsAt.Set {
		return nil
	}
	used, resets := l.UsedPercentage.V, l.ResetsAt.V
	// The float range check comes first: converting an out-of-range float to
	// int64 is not defined.
	if used < 0 || used > 100 || resets < 1 || resets > maxUnixSeconds {
		return nil
	}
	return &Window{UsedPct: used, ResetsAt: int64(resets)}
}

// validWindow reports whether w is absent or within the reader's ranges.
func validWindow(w *Window) bool {
	return w == nil || (w.UsedPct >= 0 && w.UsedPct <= 100 && validTimestamp(w.ResetsAt))
}

// validTimestamp reports whether sec is a Unix time the reader accepts.
func validTimestamp(sec int64) bool {
	return sec >= 1 && sec <= maxUnixSeconds
}

// isRecentCopy reports whether prev holds the same windows as next and was
// written under rewriteAfter before it. A prev from the future is not recent,
// so a clock change cannot freeze the feed.
func isRecentCopy(prev, next Reading) bool {
	age := next.WrittenAt - prev.WrittenAt
	return sameWindow(prev.FiveHour, next.FiveHour) &&
		sameWindow(prev.SevenDay, next.SevenDay) &&
		age >= 0 && age < int64(rewriteAfter/time.Second)
}

func sameWindow(a, b *Window) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
