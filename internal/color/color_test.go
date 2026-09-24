package color

import "testing"

var terminalBackground = RGB{12, 12, 12}

func TestHeadroomLevel(t *testing.T) {
	tests := []struct {
		left float64
		want Level
	}{{30, Green}, {29, Yellow}, {15, Yellow}, {14, Red}, {-5, Red}}
	for _, tt := range tests {
		if got := HeadroomLevel(tt.left, 30, 15); got != tt.want {
			t.Errorf("HeadroomLevel(%v) = %v, want %v", tt.left, got, tt.want)
		}
	}
}

func TestOklchToRGB(t *testing.T) {
	if got, _ := OklchToRGB(1, 0, 0); got != (RGB{255, 255, 255}) {
		t.Errorf("white = %v", got)
	}
	if got, _ := OklchToRGB(0, 0, 0); got != (RGB{}) {
		t.Errorf("black = %v", got)
	}
	if _, ok := OklchToRGB(0.8, 0.4, 265); ok {
		t.Error("an out-of-gamut color was accepted")
	}
	// Regression: the PowerShell version rounded to 0 or 255 before gamma.
	got, _ := OklchToRGB(0.8, 0.1, 255)
	for _, c := range got {
		if c == 0 || c == 255 {
			t.Fatalf("channel clipped: %v", got)
		}
	}
}

// The JavaScript version's colors, so existing users keep their project colors.
var jsSlotColors = [Slots]RGB{
	{252, 160, 167}, {254, 164, 124}, {242, 175, 72}, {203, 194, 74},
	{150, 210, 115}, {81, 218, 167}, {42, 215, 215}, {73, 207, 252},
	{146, 193, 253}, {183, 181, 252}, {224, 161, 251}, {253, 151, 210},
}

func TestSlotColors(t *testing.T) {
	seen := map[RGB]bool{}
	minLum, maxLum := 1.0, 0.0
	for slot := range Slots {
		c := SlotColor(slot)
		if c != jsSlotColors[slot] {
			t.Errorf("slot %d = %v, want %v", slot, c, jsSlotColors[slot])
		}
		if r := ContrastRatio(c, terminalBackground); r < 9 {
			t.Errorf("slot %d contrast %.1f:1 on a dark terminal, want at least 9:1", slot, r)
		}
		seen[c] = true
		lum := RelativeLuminance(c)
		minLum, maxLum = min(minLum, lum), max(maxLum, lum)
	}
	if len(seen) != Slots {
		t.Errorf("only %d distinct colors", len(seen))
	}
	if maxLum/minLum >= 1.4 {
		t.Errorf("brightness varies too much across slots: %.2f", maxLum/minLum)
	}
}

func TestProjectSlot(t *testing.T) {
	if ProjectSlot("TechCentral", nil) != ProjectSlot("techcentral", nil) {
		t.Error("slot depends on case")
	}
	// Same hash as the JavaScript version.
	for name, want := range map[string]int{"techcentral": 7, "orc-pack": 5, "Roga": 9, "api": 6, "sandbox": 3} {
		if got := ProjectSlot(name, nil); got != want {
			t.Errorf("ProjectSlot(%q) = %d, want %d", name, got, want)
		}
	}
	overrides := map[string]int{"website": 4, "api": 13, "neg": -1}
	for name, want := range map[string]int{"Website": 4, "api": 1, "neg": 11} {
		if got := ProjectSlot(name, overrides); got != want {
			t.Errorf("ProjectSlot(%q, overrides) = %d, want %d", name, got, want)
		}
	}
}
