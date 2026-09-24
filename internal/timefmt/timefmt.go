// Package timefmt formats reset times and durations in local time.
package timefmt

import (
	"fmt"
	"time"
)

// StartOfDay is local midnight at the start of t's day.
func StartOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// DateKey is yyyymmdd for t's local day.
func DateKey(t time.Time) string {
	return t.Format("20060102")
}

// Clock formats epoch seconds as "9pm" or "3:40pm", prefixed with the weekday
// ("Thu 9pm") when the time is not on the same local day as now.
func Clock(epochSeconds float64, now time.Time) string {
	t := time.Unix(int64(epochSeconds), 0).In(now.Location())
	layout := "3pm"
	if t.Minute() != 0 {
		layout = "3:04pm"
	}
	s := t.Format(layout)
	if DateKey(t) != DateKey(now) {
		s = t.Format("Mon") + " " + s
	}
	return s
}

// Duration formats milliseconds as "1h5m", "12m", or "40s".
func Duration(ms float64) string {
	sec := int64(ms / 1000)
	h, m := sec/3600, (sec%3600)/60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}
