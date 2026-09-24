// Package color holds paceline's color math: headroom thresholds and
// per-project colors.
package color

import (
	"crypto/md5" //nolint:gosec // G501: a stable hash for picking a color, not security.
	"math"
	"strings"
)

// Slots is the number of project colors.
const Slots = 12

const (
	projectLightness = 0.8
	projectMaxChroma = 0.14
)

// RGB is an sRGB color with 0-255 channels.
type RGB [3]uint8

// Level is a headroom color band.
type Level int

// Headroom levels, from most to least headroom.
const (
	Green Level = iota
	Yellow
	Red
)

// HeadroomLevel buckets a "% left" value using the green and yellow thresholds.
func HeadroomLevel(left, green, yellow float64) Level {
	switch {
	case left >= green:
		return Green
	case left >= yellow:
		return Yellow
	default:
		return Red
	}
}

func cube(x float64) float64 { return x * x * x }

// OklchToRGB converts an OKLCH color to sRGB. ok is false when the color is
// outside what sRGB can display. OKLCH is perceptually uniform: equal
// lightness looks equally bright across hues, which HSL does not manage.
func OklchToRGB(lightness, chroma, hueDegrees float64) (rgb RGB, ok bool) {
	h := hueDegrees * math.Pi / 180
	a, b := chroma*math.Cos(h), chroma*math.Sin(h)
	l := cube(lightness + 0.3963377774*a + 0.2158037573*b)
	m := cube(lightness - 0.1055613458*a - 0.0638541728*b)
	s := cube(lightness - 0.0894841775*a - 1.291485548*b)
	linear := [3]float64{
		4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.707614701*s,
	}
	for i, v := range linear {
		if v < -0.0001 || v > 1.0001 {
			return RGB{}, false
		}
		c := math.Min(1, math.Max(0, v))
		if c <= 0.0031308 {
			c *= 12.92
		} else {
			c = 1.055*math.Pow(c, 1/2.4) - 0.055
		}
		rgb[i] = uint8(math.Round(c * 255))
	}
	return rgb, true
}

// RelativeLuminance is the WCAG relative luminance of c.
func RelativeLuminance(c RGB) float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c[0]) + 0.7152*lin(c[1]) + 0.0722*lin(c[2])
}

// ContrastRatio is the WCAG contrast ratio between two colors.
func ContrastRatio(a, b RGB) float64 {
	hi, lo := RelativeLuminance(a), RelativeLuminance(b)
	if lo > hi {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// ProjectSlot picks a project's color slot: an override when one matches the
// name case-insensitively, otherwise an MD5 hash of the lowercased name so the
// slot is the same on every run and machine.
func ProjectSlot(name string, overrides map[string]int) int {
	for k, v := range overrides {
		if strings.EqualFold(k, name) {
			return ((v % Slots) + Slots) % Slots
		}
	}
	sum := md5.Sum([]byte(strings.ToLower(name))) //nolint:gosec // G401: see import.
	return int(sum[0]) % Slots
}

// SlotColor is the color for a slot: evenly spaced hues at a fixed high
// lightness, so every project reads clearly on a dark terminal. Chroma backs
// off per hue until the color fits in sRGB (blues and purples cannot be as
// vivid as other hues at this lightness).
func SlotColor(slot int) RGB {
	hue := 15 + float64(360/Slots*slot)
	for chroma := projectMaxChroma; chroma > 0; chroma -= 0.01 {
		if rgb, ok := OklchToRGB(projectLightness, chroma, hue); ok {
			return rgb
		}
	}
	rgb, _ := OklchToRGB(projectLightness, 0, hue)
	return rgb
}
