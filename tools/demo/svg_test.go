package main

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func drawDoc(t *testing.T, doc *Document) string {
	t.Helper()
	svg, err := renderSVG(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(svg)
}

func TestSVGFailsOnALineTooLong(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*Document, string)
		want string
	}{
		"reply":     {func(d *Document, s string) { d.Scenes[0].Steps[1].Reply = s[2:] }, "reply"},
		"caption":   {func(d *Document, s string) { d.Scenes[2].Caption = s }, "caption"},
		"prompt":    {func(d *Document, s string) { p := s[2:]; d.Scenes[3].Steps[0].Prompt = &p }, "prompt"},
		"mode line": {func(d *Document, s string) { d.Scenes[0].Steps[0].ModeLine = s[2:] }, "mode line"},
		"status line": {func(d *Document, s string) {
			d.Scenes[6].Steps[0].StatusLines[2].Runs = append(d.Scenes[6].Steps[0].StatusLines[2].Runs, Run{Text: s})
		}, "status line 3"},
	} {
		// One column too many fails; exactly full fits.
		doc := buildDoc(t)
		tc.edit(doc, strings.Repeat("x", Columns+1))
		_, err := renderSVG(doc)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "110-column") {
			t.Errorf("%s one column over: err = %v", name, err)
		}
	}
	doc := buildDoc(t)
	doc.Scenes[0].Steps[1].Reply = strings.Repeat("x", Columns-2)
	doc.Scenes[2].Caption = strings.Repeat("x", Columns)
	if _, err := renderSVG(doc); err != nil {
		t.Errorf("lines that exactly fill the terminal: %v", err)
	}
}

func TestSVGFailsOnACharacterTheFontLacks(t *testing.T) {
	doc := buildDoc(t)
	doc.Scenes[4].Caption += " \u0416"
	_, err := renderSVG(doc)
	if err == nil {
		t.Fatal("rendered a character the embedded font lacks")
	}
	for _, want := range []string{"U+0416", "caption", "re-run the subsetting command", "--unicodes=U+0020,", "U+0416"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestSVGFailsOnACharacterWiderThanACell(t *testing.T) {
	face, err := embeddedFont()
	if err != nil {
		t.Fatal(err)
	}
	face.advances['x'] = 2400
	doc := buildDoc(t)
	doc.Scenes[0].Steps[0].Reply += " x"
	if _, err := drawSVG(doc, face, fontFile); err == nil || !strings.Contains(err.Error(), "not one 1200-unit cell") {
		t.Errorf("err = %v", err)
	}
}

func TestSVGStructure(t *testing.T) {
	doc := buildDoc(t)
	svg := drawDoc(t, doc)
	steps, total := 0, 0
	for _, sc := range doc.Scenes {
		steps += len(sc.Steps)
		total += sc.DurationMs
	}
	counts := map[string]int{
		`role="img"`:                   1,
		`<title id="title">`:           1,
		`<desc id="desc">`:             1,
		`aria-labelledby="title desc"`: 1,
		`<g class="s a`:                len(doc.Scenes),
		`<g class="s a on"`:            1,
		`<g class="p a`:                steps,
		`<circle class="d a`:           len(doc.Scenes),
		`<circle class="d a on"`:       1,
		"@media (prefers-reduced-motion:reduce){.a{animation:none}}":                              1,
		".a{animation-duration:" + strconv.Itoa(total) + "ms;animation-iteration-count:infinite}": 1,
		"@font-face": 1,
		base64.StdEncoding.EncodeToString(fontFile): 1,
	}
	for want, n := range counts {
		if got := strings.Count(svg, want); got != n {
			t.Errorf("%q appears %d times, want %d", want, got, n)
		}
	}
	// The first step of the first scene is the one reduced motion holds.
	if !strings.Contains(svg, "<g class=\"s a on\" style=\"animation-name:k0\">") ||
		!strings.Contains(svg, "<g class=\"p a on\" style=\"animation-name:k1\">") {
		t.Error("the first scene or its first step is not marked on")
	}
	for _, banned := range []string{"<script", "foreignObject", "textLength", "href=", "url(http", "@import", "\r"} {
		if strings.Contains(svg, banned) {
			t.Errorf("the SVG contains %q", banned)
		}
	}
	if n, withX := strings.Count(svg, "<tspan"), strings.Count(svg, `<tspan x="`); n == 0 || n != withX {
		t.Errorf("%d of %d tspans have an explicit x", withX, n)
	}
}

func TestSVGDescriptionListsScenesInOrder(t *testing.T) {
	doc := buildDoc(t)
	desc := description(doc)
	at := 0
	for _, sc := range doc.Scenes {
		i := strings.Index(desc[at:], sc.Title)
		if i < 0 {
			t.Fatalf("description %q lacks %q after byte %d", desc, sc.Title, at)
		}
		at += i + len(sc.Title)
	}
	if !strings.Contains(desc, "count down what's left") {
		t.Errorf("description %q does not say the numbers count down what's left", desc)
	}
}

func TestTimelinePlaysEveryStepInOrder(t *testing.T) {
	doc := buildDoc(t)
	tl, err := buildTimeline(doc)
	if err != nil {
		t.Fatal(err)
	}
	at := 0
	for i, sc := range doc.Scenes {
		if tl.scenes[i].start != at {
			t.Errorf("scene %s starts at %d, want %d", sc.ID, tl.scenes[i].start, at)
		}
		for k, st := range sc.Steps {
			if want := (interval{at, at + st.DurationMs}); tl.steps[i][k] != want {
				t.Errorf("scene %s step %d: %v, want %v", sc.ID, k+1, tl.steps[i][k], want)
			}
			at += st.DurationMs
		}
		if tl.scenes[i].end != at {
			t.Errorf("scene %s ends at %d, want %d", sc.ID, tl.scenes[i].end, at)
		}
	}
	if tl.totalMs != at {
		t.Errorf("loop is %dms, scenes add up to %dms", tl.totalMs, at)
	}

	doc.Scenes[1].DurationMs++
	if _, err := buildTimeline(doc); err == nil {
		t.Error("a scene whose steps do not add up to its duration was accepted")
	}
}

var (
	keyframesRule = regexp.MustCompile(`@keyframes (k\d+)\{(.*)\}`)
	keyframeStop  = regexp.MustCompile(`([\d.]+)%\{(opacity|fill):([^}]+)\}`)
)

func TestSVGKeyframesSpanTheLoop(t *testing.T) {
	doc := buildDoc(t)
	svg := drawDoc(t, doc)
	rules := keyframesRule.FindAllStringSubmatch(svg, -1)
	steps := 0
	for _, sc := range doc.Scenes {
		steps += len(sc.Steps)
	}
	if want := 2*len(doc.Scenes) + steps; len(rules) != want {
		t.Fatalf("%d keyframe rules, want one per scene, step, and dot (%d)", len(rules), want)
	}
	for _, rule := range rules {
		stops := keyframeStop.FindAllStringSubmatch(rule[2], -1)
		last := -1.0
		for _, s := range stops {
			p, err := strconv.ParseFloat(s[1], 64)
			if err != nil || p <= last || p > 100 {
				t.Errorf("%s: stop %s%% out of order", rule[1], s[1])
			}
			last = p
		}
		if stops[0][1] != "0" || stops[len(stops)-1][1] != "100" {
			t.Errorf("%s does not run from 0%% to 100%%: %s", rule[1], rule[2])
		}
	}
	// The last step shows until the very end of the loop.
	tl, err := buildTimeline(doc)
	if err != nil {
		t.Fatal(err)
	}
	lastScene := tl.steps[len(tl.steps)-1]
	lastStep := lastScene[len(lastScene)-1]
	if want := percent(lastStep.start, tl.totalMs) + "%{opacity:1}100%{opacity:0}}"; !strings.Contains(svg, want) {
		t.Errorf("no keyframes end with %q", want)
	}
}

func TestSVGIsByteStable(t *testing.T) {
	doc := buildDoc(t)
	first, second := drawDoc(t, doc), drawDoc(t, buildDoc(t))
	if first != second {
		t.Error("two renders differ")
	}
	if !strings.HasSuffix(first, "</svg>\n") || strings.HasSuffix(first, "\n\n") {
		t.Error("want exactly one trailing newline after </svg>")
	}
	if !bytes.Contains([]byte(first), []byte("\u25cf")) {
		t.Error("glyphs are not raw UTF-8")
	}
}

func TestPieces(t *testing.T) {
	for _, tc := range []struct {
		col  int
		text string
		want []piece
	}{
		{0, "a b", []piece{{0, "a b"}}},
		{2, " (dev) ", []piece{{3, "(dev)"}}},
		{0, "name  task", []piece{{0, "name"}, {6, "task"}}},
		{5, "   ", nil},
		{0, "\u23f5 x", []piece{{0, "\u23f5 x"}}},
	} {
		got := pieces(tc.col, tc.text)
		if len(got) != len(tc.want) {
			t.Errorf("pieces(%d, %q) = %v, want %v", tc.col, tc.text, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("pieces(%d, %q) = %v, want %v", tc.col, tc.text, got, tc.want)
			}
		}
	}
}
