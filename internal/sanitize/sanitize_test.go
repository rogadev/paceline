package sanitize

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	esc = "\x1b"
	bel = "\x07"
)

// Real terminal-injection payloads. A cloned repo controls its folder and
// branch names, so every one of these must come out inert.
var attacks = map[string]string{
	"clear screen":              esc + "[2J" + esc + "[H",
	"set window title (OSC 0)":  esc + "]0;pwned" + bel,
	"hyperlink (OSC 8)":         esc + "]8;;https://evil.example" + esc + "\\click" + esc + "]8;;" + esc + "\\",
	"clipboard write (OSC 52)":  esc + "]52;c;cm0gLXJmIH4=" + bel,
	"8-bit CSI":                 "\u009b2J",
	"bidi override":             "\u202etxt.exe",
	"zero-width space":          "a\u200bb",
	"carriage return overwrite": "safe\rEVIL",
	"newline":                   "line1\nline2",
	"invalid UTF-8":             "ok\xff\xfe" + esc,
}

// hasUnsafe reports whether s still holds an unsafe character or invalid UTF-8.
func hasUnsafe(s string) bool {
	return strings.ContainsFunc(s, unsafe) || !utf8.ValidString(s)
}

func TestTextNeutralizesAttacks(t *testing.T) {
	for name, attack := range attacks {
		t.Run(name, func(t *testing.T) {
			if got := Text(attack, 64); hasUnsafe(got) {
				t.Fatalf("Text(%q) = %q, still contains unsafe characters", attack, got)
			}
		})
	}
}

func TestTextKeepsOrdinaryUnicode(t *testing.T) {
	in := "caf\u00e9-\u65e5\u672c"
	if got := Text(in, 64); got != in {
		t.Fatalf("Text(%q) = %q", in, got)
	}
}

func TestTextCapsLengthByRune(t *testing.T) {
	got := Text(strings.Repeat("\U0001f600", 100), 10)
	if n := utf8.RuneCountInString(got); n != 10 {
		t.Fatalf("got %d runes, want 10", n)
	}
	if !strings.HasSuffix(got, ellipsis) || !utf8.ValidString(got) {
		t.Fatalf("got %q", got)
	}
	if got := Text("exactly", 7); got != "exactly" {
		t.Fatalf("a string at the limit was cut: %q", got)
	}
}
