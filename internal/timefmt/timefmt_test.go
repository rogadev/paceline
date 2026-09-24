package timefmt

import (
	"testing"
	"time"
)

func local(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.Local)
}

func epoch(t time.Time) float64 { return float64(t.Unix()) }

func TestClock(t *testing.T) {
	now := local(2026, 9, 24, 10, 0)
	tests := []struct {
		at   time.Time
		want string
	}{
		{local(2026, 9, 24, 21, 0), "9pm"},
		{local(2026, 9, 24, 0, 0), "12am"},
		{local(2026, 9, 24, 12, 0), "12pm"},
		{local(2026, 9, 24, 15, 5), "3:05pm"},
		{local(2026, 9, 25, 8, 30), "Fri 8:30am"},
	}
	for _, tt := range tests {
		if got := Clock(epoch(tt.at), now); got != tt.want {
			t.Errorf("Clock(%v) = %q, want %q", tt.at, got, tt.want)
		}
	}
}

func TestDates(t *testing.T) {
	if got := DateKey(local(2026, 1, 5, 23, 0)); got != "20260105" {
		t.Errorf("DateKey = %q", got)
	}
	if got := StartOfDay(local(2026, 9, 24, 15, 30)); !got.Equal(local(2026, 9, 24, 0, 0)) {
		t.Errorf("StartOfDay = %v", got)
	}
}

func TestDuration(t *testing.T) {
	for ms, want := range map[float64]string{40_000: "40s", 16 * 60_000: "16m", 65 * 60_000: "1h5m"} {
		if got := Duration(ms); got != want {
			t.Errorf("Duration(%v) = %q, want %q", ms, got, want)
		}
	}
}
