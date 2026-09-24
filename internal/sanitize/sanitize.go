// Package sanitize makes untrusted text safe to print to a terminal.
//
// Everything paceline prints that it did not write itself (model names,
// folder names, git branch names, effort levels) passes through Text first.
// A folder or branch name is attacker-controlled when you clone a repo, and
// raw control characters in terminal output can move the cursor, rewrite the
// screen, set the window title, or emit OSC 8 links.
package sanitize

import "strings"

const ellipsis = "\u2026"

// unsafe reports whether r is a C0 or C1 control, DEL, a zero-width
// character, a bidi override, or a byte-order mark. C1 controls matter because
// some terminals treat them as 8-bit escape introducers; bidi overrides and
// zero-width characters can hide text or visually reorder what follows.
func unsafe(r rune) bool {
	switch {
	case r <= 0x1f, r >= 0x7f && r <= 0x9f:
		return true
	case r >= 0x200b && r <= 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0xfeff, r == 0xfffd:
		// U+FFFD also stands in for invalid UTF-8, which is dropped rather than printed.
		return true
	}
	return false
}

// Text strips unsafe characters and caps the result at maxRunes runes,
// ending in an ellipsis when it was cut.
func Text(s string, maxRunes int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unsafe(r) {
			continue
		}
		if n == maxRunes {
			// One more rune remains: replace the last kept rune with an ellipsis.
			kept := []rune(b.String())
			return string(kept[:maxRunes-1]) + ellipsis
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
