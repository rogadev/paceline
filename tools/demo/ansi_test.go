package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseANSI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Run
	}{
		{"empty", "", nil},
		{"plain text", "Opus 5.5", []Run{{Text: "Opus 5.5"}}},
		{"basic color then reset", "\x1b[36mOpus\x1b[0m rest", []Run{{Text: "Opus", Color: "cyan"}, {Text: " rest"}}},
		{"dim", "a \x1b[2m\u00b7\x1b[0m b", []Run{{Text: "a "}, {Text: "\u00b7", Dim: true}, {Text: " b"}}},
		{
			"same style merges across a reset",
			"\x1b[32m\u25b0\u25b0\x1b[0m\x1b[32m\u25b0\x1b[0m",
			[]Run{{Text: "\u25b0\u25b0\u25b0", Color: "green"}},
		},
		{"plain neighbors merge", "a\x1b[0mb", []Run{{Text: "ab"}}},
		{"truecolor", "\x1b[38;2;185;47;47mt over 4%\x1b[0m", []Run{{Text: "t over 4%", Color: "#b92f2f"}}},
		{"truecolor is lowercase and zero-padded", "\x1b[38;2;0;10;255mx", []Run{{Text: "x", Color: "#000aff"}}},
		{"bright colors", "\x1b[91mx\x1b[97my", []Run{{Text: "x", Color: "brightRed"}, {Text: "y", Color: "brightWhite"}}},
		{"39 drops only the color", "\x1b[2;33mx\x1b[39my", []Run{{Text: "x", Color: "yellow", Dim: true}, {Text: "y", Dim: true}}},
		{"bold and 22", "\x1b[1;31mx\x1b[22my", []Run{{Text: "x", Color: "red", Bold: true}, {Text: "y", Color: "red"}}},
		{"empty parameter list resets", "\x1b[1mx\x1b[my", []Run{{Text: "x", Bold: true}, {Text: "y"}}},
		{"empty parameter means 0", "\x1b[32;mx", []Run{{Text: "x"}}},
		{"codes before any text leave no empty run", "\x1b[32m\x1b[0mx", []Run{{Text: "x"}}},
		{"truecolor then more codes", "\x1b[38;2;1;2;3;1mx", []Run{{Text: "x", Color: "#010203", Bold: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseANSI(tt.in)
			if err != nil {
				t.Fatalf("ParseANSI(%q): %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseANSI(%q)\n got  %+v\n want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseANSIRejectsWhatItDoesNotKnow(t *testing.T) {
	tests := map[string]string{
		"OSC title":                  "\x1b]0;pwned\x07",
		"non-SGR CSI":                "\x1b[2J",
		"underline":                  "\x1b[4mx",
		"background color":           "\x1b[41mx",
		"256-color":                  "\x1b[38;5;196mx",
		"short truecolor":            "\x1b[38;2;1;2mx",
		"truecolor over 255":         "\x1b[38;2;300;0;0mx",
		"unterminated CSI":           "x\x1b[32",
		"lone escape":                "x\x1b",
		"private parameter":          "\x1b[?25l",
		"carriage return":            "safe\rEVIL",
		"newline":                    "l1\nl2",
		"C1 control":                 "a\u009bb",
		"invalid UTF-8":              "a\xffb",
		"escape after good sequence": "\x1b[32mok\x1b[0m\x1b[5m",
	}
	for name, in := range tests {
		if runs, err := ParseANSI(in); err == nil {
			t.Errorf("%s: ParseANSI(%q) = %+v, want an error", name, in, runs)
		}
	}
}

// Every color name the parser can emit must be a palette key, or a consumer
// would have no color to draw it with.
func TestColorNamesArePaletteKeys(t *testing.T) {
	data, err := json.Marshal(palette)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]string
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	for _, name := range append(ansiColors[:], brightColors[:]...) {
		v, ok := keys[name]
		if !ok {
			t.Errorf("color %q is not a palette key", name)
			continue
		}
		if len(v) != 7 || !strings.HasPrefix(v, "#") || strings.ToLower(v) != v {
			t.Errorf("palette %s = %q, want lowercase #rrggbb", name, v)
		}
	}
}

func TestPlainText(t *testing.T) {
	if got := plainText([]Run{{Text: "a"}, {Text: "b", Dim: true}, {Text: "c", Color: "red"}}); got != "abc" {
		t.Errorf("plainText = %q", got)
	}
	if got := plainText(nil); got != "" {
		t.Errorf("plainText(nil) = %q", got)
	}
}
