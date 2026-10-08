package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestEmbeddedFontIsOneCellPerCharacter(t *testing.T) {
	face, err := embeddedFont()
	if err != nil {
		t.Fatal(err)
	}
	if face.unitsPerEm != 2048 {
		t.Errorf("units per em = %d, want Cascadia Mono's 2048", face.unitsPerEm)
	}
	if len(face.advances) == 0 {
		t.Fatal("the font maps no characters")
	}
	for r, adv := range face.advances {
		if adv != 1200 {
			t.Errorf("%U advances %d units, want 1200", r, adv)
		}
	}
	for _, r := range []rune{'a', '%', '\u00b7', '\u25b0', '\u25b1', '\u2713', '\u25cf', '\u2026'} {
		if _, ok := face.advances[r]; !ok {
			t.Errorf("the font has no %U %q", r, r)
		}
	}
	// The writer draws these as shapes because the font has no glyph for them.
	for _, r := range []rune{modeArrow, spinnerStar} {
		if _, ok := face.advances[r]; ok {
			t.Errorf("the font now has %U; draw it from the font instead of as a shape", r)
		}
	}
}

func TestParseWOFFRejectsBrokenFiles(t *testing.T) {
	// A table whose offset points past the end of the file.
	outside := bytes.Clone(fontFile[:44+20])
	binary.BigEndian.PutUint16(outside[12:], 1)
	binary.BigEndian.PutUint32(outside[44+4:], uint32(len(fontFile)))
	binary.BigEndian.PutUint32(outside[44+8:], 10)
	binary.BigEndian.PutUint32(outside[44+12:], 10)

	for name, tc := range map[string]struct {
		data []byte
		want string
	}{
		"empty":                  {nil, "not a WOFF file"},
		"a TrueType file":        {append([]byte{0, 1, 0, 0}, fontFile[4:]...), "not a WOFF file"},
		"cut in the directory":   {fontFile[:50], "cut short"},
		"table outside the file": {outside, "outside the file"},
	} {
		_, err := parseWOFF(tc.data)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}
