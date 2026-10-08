package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Run is a stretch of status-line text drawn in one style. Color is a
// palette key ("red", "brightRed", ...) or a truecolor "#rrggbb"; empty means
// the default foreground.
type Run struct {
	Text  string `json:"text"`
	Color string `json:"color,omitempty"`
	Dim   bool   `json:"dim,omitempty"`
	Bold  bool   `json:"bold,omitempty"`
}

// ansiColors are the palette keys for SGR 30-37; SGR 90-97 use brightColors.
var (
	ansiColors   = [8]string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}
	brightColors = [8]string{"brightBlack", "brightRed", "brightGreen", "brightYellow", "brightBlue", "brightMagenta", "brightCyan", "brightWhite"}
)

type sgrState struct {
	color     string
	dim, bold bool
}

// ParseANSI splits a rendered status line into styled runs, merging
// neighbors with the same style. It understands only the SGR codes the
// renderer emits, and fails on anything else rather than drop it, so a new
// code in the renderer cannot slip into the demo unnoticed.
func ParseANSI(s string) ([]Run, error) {
	var (
		runs []Run
		st   sgrState
		text strings.Builder
	)
	flush := func() {
		if text.Len() == 0 {
			return
		}
		r := Run{Text: text.String(), Color: st.color, Dim: st.dim, Bold: st.bold}
		text.Reset()
		if n := len(runs); n > 0 && runs[n-1].Color == r.Color && runs[n-1].Dim == r.Dim && runs[n-1].Bold == r.Bold {
			runs[n-1].Text += r.Text
			return
		}
		runs = append(runs, r)
	}
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 >= len(s) || s[i+1] != '[' {
				return nil, fmt.Errorf("byte %d: escape sequence is not a CSI", i)
			}
			j := i + 2
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';') {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("byte %d: unterminated escape sequence", i)
			}
			if s[j] != 'm' {
				return nil, fmt.Errorf("byte %d: unsupported CSI sequence %q", i, s[i:j+1])
			}
			flush()
			if err := st.apply(s[i+2 : j]); err != nil {
				return nil, fmt.Errorf("byte %d: %w", i, err)
			}
			i = j + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, fmt.Errorf("byte %d: invalid UTF-8", i)
		}
		if unicode.IsControl(r) {
			return nil, fmt.Errorf("byte %d: control character %U", i, r)
		}
		text.WriteString(s[i : i+size])
		i += size
	}
	flush()
	return runs, nil
}

// apply updates the state for one SGR parameter list, such as "38;2;1;2;3".
func (st *sgrState) apply(params string) error {
	codes := strings.Split(params, ";")
	for i := 0; i < len(codes); i++ {
		n, err := sgrNumber(codes[i])
		if err != nil {
			return err
		}
		switch {
		case n == 0:
			*st = sgrState{}
		case n == 1:
			st.bold = true
		case n == 2:
			st.dim = true
		case n == 22:
			st.bold, st.dim = false, false
		case n >= 30 && n <= 37:
			st.color = ansiColors[n-30]
		case n == 39:
			st.color = ""
		case n >= 90 && n <= 97:
			st.color = brightColors[n-90]
		case n == 38:
			if len(codes) < i+5 || codes[i+1] != "2" {
				return fmt.Errorf("SGR %q: only 38;2;r;g;b is supported", params)
			}
			var rgb [3]int
			for k := range rgb {
				v, err := sgrNumber(codes[i+2+k])
				if err != nil {
					return err
				}
				if v > 255 {
					return fmt.Errorf("SGR %q: color component %d is over 255", params, v)
				}
				rgb[k] = v
			}
			st.color = fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
			i += 4
		default:
			return fmt.Errorf("SGR %q: unsupported code %d", params, n)
		}
	}
	return nil
}

// sgrNumber parses one SGR parameter; an empty one means 0, as in "\x1b[m".
func sgrNumber(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("SGR parameter %q is not a number", s)
	}
	return n, nil
}

// plainText joins the runs' text: the status line with its styling removed.
func plainText(runs []Run) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}
