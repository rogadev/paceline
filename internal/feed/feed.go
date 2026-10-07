// Package feed writes the latest rate-limit usage from Claude Code's status
// payload to paceline-feed.json, so other local tools, such as paceline-tray,
// can read it without asking Anthropic.
//
// The file format is a public contract, enforced by paceline-tray's reader
// (internal/usage/feed): version 1, at most 4096 bytes, usedPct from 0 to
// 100, and resetsAt and writtenAt as positive whole Unix seconds.
package feed

import (
	"encoding/json"
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

type window struct {
	UsedPct  float64 `json:"usedPct"`
	ResetsAt int64   `json:"resetsAt"` // Unix seconds
}

type file struct {
	Version   int     `json:"version"`
	FiveHour  *window `json:"fiveHour,omitempty"`
	SevenDay  *window `json:"sevenDay,omitempty"`
	WrittenAt int64   `json:"writtenAt"` // Unix seconds
}

// Path is where the feed lives inside configDir, Claude Code's config
// directory.
func Path(configDir string) string {
	return filepath.Join(configDir, fileName)
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
	next := file{Version: formatVersion, WrittenAt: now.Unix()}
	if p.RateLimits != nil {
		next.FiveHour = toWindow(p.RateLimits.FiveHour)
		next.SevenDay = toWindow(p.RateLimits.SevenDay)
	}
	if next.FiveHour == nil && next.SevenDay == nil {
		return nil
	}
	if next.WrittenAt < 1 || next.WrittenAt > maxUnixSeconds {
		return nil
	}
	if prev, ok := read(path); ok && isRecentCopy(prev, next) {
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
func toWindow(l *payload.Limit) *window {
	if l == nil || !l.UsedPercentage.Set || !l.ResetsAt.Set {
		return nil
	}
	used, resets := l.UsedPercentage.V, l.ResetsAt.V
	if used < 0 || used > 100 || resets < 1 || resets > maxUnixSeconds {
		return nil
	}
	return &window{UsedPct: used, ResetsAt: int64(resets)}
}

// read loads the existing feed, reporting false for a missing, unreadable,
// oversized, malformed, or other-version file.
func read(path string) (file, bool) {
	f, err := os.Open(path) //nolint:gosec // G304: path is paceline's own feed file.
	if err != nil {
		return file{}, false
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil || len(data) > maxFileBytes {
		return file{}, false
	}
	var prev file
	if json.Unmarshal(data, &prev) != nil || prev.Version != formatVersion {
		return file{}, false
	}
	return prev, true
}

// isRecentCopy reports whether prev holds the same windows as next and was
// written under rewriteAfter before it. A prev from the future is not recent,
// so a clock change cannot freeze the feed.
func isRecentCopy(prev, next file) bool {
	age := next.WrittenAt - prev.WrittenAt
	return sameWindow(prev.FiveHour, next.FiveHour) &&
		sameWindow(prev.SevenDay, next.SevenDay) &&
		age >= 0 && age < int64(rewriteAfter/time.Second)
}

func sameWindow(a, b *window) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
