package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SVGPath is where the animated graphic lives, relative to the repo root.
const SVGPath = "docs/paceline-states.svg"

// Layout, in CSS pixels. Text sits on a grid of Document.Columns cells whose
// width comes from the embedded font's advance width at fontSize.
const (
	fontSize     = 14.0
	rowHeight    = 20.0
	baseline     = 15.0 // from a row's top to its text baseline
	padX         = 24.0
	padTop       = 18.0
	headerGap    = 10.0 // above and below the rule under the header
	dotGap       = 18.0 // from the terminal's last row to the dots
	dotRadius    = 4.0
	dotSpacing   = 18.0
	padBottom    = 18.0
	frameRadius  = 10.0
	statusIndent = 2 // columns before a status line, mode line, or subagent row
	fadeMs       = 300
)

// syntheticBoldAllowance widens bold text when checking that it fits: the
// font has no Bold face, so browsers synthesize one, and Firefox draws it
// about 0.7% wider than the Regular advances.
const syntheticBoldAllowance = 1.05

// fontFamily is the name the SVG gives the embedded font. It is not the
// font's own name, so an installed Cascadia Mono can never stand in for it.
const fontFamily = "paceline-demo-mono"

// Characters Claude Code draws that the embedded font lacks. The writer draws
// each as a shape one cell wide instead.
const (
	modeArrow   = '\u23f5'
	spinnerStar = '\u273b'
)

// Claude Code's own text around the status line.
const (
	replyBullet  = "\u25cf "
	ellipsis     = "\u2026"
	promptMark   = "> "
	pendingAgent = "( ) "
)

// renderSVG draws the document as an animated SVG with the embedded font.
func renderSVG(doc *Document) ([]byte, error) {
	face, err := embeddedFont()
	if err != nil {
		return nil, err
	}
	return drawSVG(doc, face, fontFile)
}

// drawSVG draws the document with face, whose file fontData the SVG embeds.
// It fails when a line would not fit the terminal or uses a character the
// font lacks, so the graphic never overflows or falls back to another font.
func drawSVG(doc *Document, face *fontFace, fontData []byte) ([]byte, error) {
	colors, err := paletteColors(doc.Palette)
	if err != nil {
		return nil, err
	}
	scenes := make([][]screen, len(doc.Scenes))
	headers := make([]screen, len(doc.Scenes))
	for i, sc := range doc.Scenes {
		headers[i], scenes[i], err = colors.drawScene(sc)
		if err != nil {
			return nil, fmt.Errorf("scene %s: %w", sc.ID, err)
		}
	}
	g, err := newGrid(face, doc.Columns, append(slices.Concat(scenes...), headers...))
	if err != nil {
		return nil, err
	}
	tl, err := buildTimeline(doc)
	if err != nil {
		return nil, err
	}
	w := &svgWriter{grid: g, colors: colors, timeline: tl, classes: map[string]string{}}
	return w.write(doc, headers, scenes, fontData), nil
}

// colorSet maps every palette key to its "#rrggbb" value.
type colorSet map[string]string

func paletteColors(p Palette) (colorSet, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var c colorSet
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	for key, hex := range c {
		if !isHexColor(hex) {
			return nil, fmt.Errorf("palette %s is %q, not #rrggbb", key, hex)
		}
	}
	return c, nil
}

func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 16, 32)
	return err == nil
}

// run is the fill for a status-line run: dim is the palette's grey, no color
// is the foreground, and a color is a palette key or a truecolor "#rrggbb".
func (c colorSet) run(r Run) (string, error) {
	switch {
	case r.Dim && r.Color != "":
		return "", fmt.Errorf("run %q is both dim and colored (%q); a run takes one or the other", r.Text, r.Color)
	case r.Dim:
		return c["dim"], nil
	case r.Color == "":
		return c["foreground"], nil
	case isHexColor(r.Color):
		return r.Color, nil
	}
	hex, ok := c[r.Color]
	if !ok {
		return "", fmt.Errorf("run %q has color %q, which is not in the palette", r.Text, r.Color)
	}
	return hex, nil
}

// screen is one step of a scene, drawn as rows from the top of the terminal.
type screen []row

// row is one terminal row: blank, a full-width prompt rule, or text.
type row struct {
	rule   bool
	text   *textLine
	cursor bool // a block cursor after the text
}

// textLine is a row of text laid on the column grid.
type textLine struct {
	what string // names the line in errors
	segs []segment
	end  int // the column after the last character
}

// segment is text in one style starting at a column, or a single character
// the writer draws as a shape.
type segment struct {
	col   int
	text  string
	shape rune
	fill  string
	bold  bool
}

func newLine(what string, indent int) *textLine {
	return &textLine{what: what, end: indent}
}

// add appends text in one style. Each character takes one column, which
// newGrid checks against the font.
func (l *textLine) add(text, fill string, bold bool) *textLine {
	var b strings.Builder
	start := l.end
	flush := func() {
		if b.Len() > 0 {
			l.segs = append(l.segs, segment{col: start, text: b.String(), fill: fill, bold: bold})
			b.Reset()
		}
	}
	for _, r := range text {
		if r == modeArrow || r == spinnerStar {
			flush()
			l.segs = append(l.segs, segment{col: l.end, shape: r, fill: fill})
			l.end++
			continue
		}
		if b.Len() == 0 {
			start = l.end
		}
		b.WriteRune(r)
		l.end++
	}
	flush()
	return l
}

func (c colorSet) statusLine(what string, sl StatusLine) (*textLine, error) {
	l := newLine(what, statusIndent)
	for _, r := range sl.Runs {
		fill, err := c.run(r)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		l.add(r.Text, fill, r.Bold)
	}
	return l, nil
}

// drawScene returns the scene's header (title and caption) and a screen per
// step.
func (c colorSet) drawScene(sc Scene) (screen, []screen, error) {
	header := screen{
		{text: newLine("title", 0).add(sc.Title, c["brightWhite"], true)},
		{text: newLine("caption", 0).add(sc.Caption, c["foreground"], false)},
	}
	// A scene keeps the spinner's rows on every step if any step spins, so
	// the prompt box does not jump when the spinner stops.
	spins := slices.ContainsFunc(sc.Steps, func(st Step) bool { return st.Spinner != nil })
	steps := make([]screen, len(sc.Steps))
	for i, st := range sc.Steps {
		var err error
		switch sc.Layout {
		case LayoutTerminal:
			steps[i], err = c.terminalScreen(st, spins)
		case LayoutWindows:
			steps[i], err = c.windowsScreen(st)
		default:
			err = fmt.Errorf("unknown layout %q", sc.Layout)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return header, steps, nil
}

// terminalScreen is one Claude Code window: the reply, the spinner, the
// prompt box, the status line, the mode line, and any subagents.
func (c colorSet) terminalScreen(st Step, spins bool) (screen, error) {
	if st.Prompt == nil {
		return nil, errors.New("a terminal step needs a prompt, even an empty one")
	}
	if len(st.StatusLines) != 1 {
		return nil, fmt.Errorf("a terminal step needs one status line, got %d", len(st.StatusLines))
	}
	status, err := c.statusLine("status line", st.StatusLines[0])
	if err != nil {
		return nil, err
	}
	s := screen{{text: newLine("reply", 0).add(replyBullet, c["reply"], false).add(st.Reply, c["foreground"], false)}, {}}
	if spins {
		var spinner row
		if st.Spinner != nil {
			l := newLine("spinner", 0).add(string(spinnerStar)+" "+st.Spinner.Verb+ellipsis+" ", c["spinner"], false)
			spinner.text = l.add("("+st.Spinner.Activity+")", c["dim"], false)
		}
		s = append(s, spinner, row{})
	}
	s = append(s,
		row{rule: true},
		row{text: newLine("prompt", 0).add(promptMark+*st.Prompt, c["foreground"], false), cursor: true},
		row{rule: true},
		row{text: status},
		row{text: newLine("mode line", statusIndent).add(st.ModeLine, c["modeLine"], false)},
	)
	for i, a := range st.Subagents {
		l := newLine("subagent "+a.Name, statusIndent)
		if i == 0 {
			l.add(replyBullet, c["reply"], false).add(a.Name, c["foreground"], false)
		} else {
			l.add(pendingAgent, c["dim"], false).add(a.Name, c["foreground"], false).add("  "+a.Task, c["dim"], false)
		}
		s = append(s, row{text: l})
	}
	return s, nil
}

// windowsScreen stacks one pane per window: a prompt rule above its status
// line, with a blank row between panes.
func (c colorSet) windowsScreen(st Step) (screen, error) {
	if len(st.StatusLines) == 0 {
		return nil, errors.New("no status lines")
	}
	var s screen
	for i, sl := range st.StatusLines {
		status, err := c.statusLine(fmt.Sprintf("status line %d", i+1), sl)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			s = append(s, row{})
		}
		s = append(s, row{rule: true}, row{text: status})
	}
	return s, nil
}

// grid is the column grid at the embedded font's metrics.
type grid struct {
	columns     int
	cellAdvance int     // font units
	cell        float64 // pixels
	rows        int     // terminal rows, the most any step uses
}

// newGrid checks every line against the font and returns the grid. Every
// character must be in the font and one cell wide, and every line must end
// within the terminal's columns.
func newGrid(face *fontFace, columns int, screens []screen) (grid, error) {
	space, ok := face.advances[' ']
	if !ok || space == 0 {
		return grid{}, errors.New("the embedded font has no space to measure a cell by")
	}
	g := grid{columns: columns, cellAdvance: space, cell: fontSize * float64(space) / float64(face.unitsPerEm)}
	if err := checkCoverage(face, space, screens); err != nil {
		return grid{}, err
	}
	for _, s := range screens {
		g.rows = max(g.rows, len(s))
		for _, r := range s {
			if r.text == nil {
				continue
			}
			if err := g.fit(face, r.text); err != nil {
				return grid{}, err
			}
		}
	}
	return g, nil
}

// fit fails when the line's right edge, measured in the font's own advance
// widths, passes the last column.
func (g grid) fit(face *fontFace, l *textLine) error {
	limit := g.columns * g.cellAdvance
	right := 0
	for _, s := range l.segs {
		edge := s.col * g.cellAdvance
		if s.shape != 0 {
			edge += g.cellAdvance
		}
		width := 0
		for _, r := range s.text {
			width += face.advances[r]
		}
		if s.bold {
			width = int(math.Ceil(float64(width) * syntheticBoldAllowance))
		}
		right = max(right, edge+width)
	}
	if right > limit {
		cols := (right + g.cellAdvance - 1) / g.cellAdvance
		return fmt.Errorf("the %s is %d columns wide, over the %d-column terminal: %q", l.what, cols, g.columns, l.plain())
	}
	return nil
}

func (l *textLine) plain() string {
	var b strings.Builder
	for _, s := range l.segs {
		b.WriteString(strings.Repeat(" ", max(0, s.col-utf8.RuneCountInString(b.String()))))
		if s.shape != 0 {
			b.WriteRune(s.shape)
		}
		b.WriteString(s.text)
	}
	return strings.TrimSpace(b.String())
}

// checkCoverage fails on the first character, in code point order, that the
// font lacks or that is not one cell wide. The message lists every character
// the document needs, ready for the subsetting command.
func checkCoverage(face *fontFace, cell int, screens []screen) error {
	need := map[rune]string{}
	for _, s := range screens {
		for _, r := range s {
			if r.text == nil {
				continue
			}
			for _, seg := range r.text.segs {
				for _, ch := range seg.text {
					if _, seen := need[ch]; !seen {
						need[ch] = r.text.what
					}
				}
			}
		}
	}
	runes := slices.Sorted(maps.Keys(need))
	for _, r := range runes {
		adv, ok := face.advances[r]
		switch {
		case !ok:
			return fmt.Errorf("the embedded font has no glyph for %U %q (first used in a %s line); "+
				"re-run the subsetting command in docs/demo/README.md with --unicodes=%s", r, r, need[r], unicodeList(runes))
		case adv != cell:
			return fmt.Errorf("%U %q is %d font units wide, not one %d-unit cell, so it would break the column grid", r, r, adv, cell)
		}
	}
	return nil
}

// unicodeList formats runes the way pyftsubset's --unicodes takes them.
func unicodeList(runes []rune) string {
	parts := make([]string, len(runes))
	for i, r := range runes {
		parts[i] = fmt.Sprintf("U+%04X", r)
	}
	return strings.Join(parts, ",")
}

// interval is a stretch of the loop, in milliseconds from its start.
type interval struct{ start, end int }

// timeline places every scene and step in the loop: scenes play in order,
// and each scene's steps play in order inside it.
type timeline struct {
	totalMs int
	scenes  []interval
	steps   [][]interval
}

func buildTimeline(doc *Document) (timeline, error) {
	var tl timeline
	for _, sc := range doc.Scenes {
		start := tl.totalMs
		var steps []interval
		for i, st := range sc.Steps {
			if st.DurationMs <= 0 {
				return tl, fmt.Errorf("scene %s, step %d: duration %dms", sc.ID, i+1, st.DurationMs)
			}
			steps = append(steps, interval{tl.totalMs, tl.totalMs + st.DurationMs})
			tl.totalMs += st.DurationMs
		}
		if tl.totalMs-start != sc.DurationMs {
			return tl, fmt.Errorf("scene %s lasts %dms but its steps add up to %dms", sc.ID, sc.DurationMs, tl.totalMs-start)
		}
		if sc.DurationMs <= 2*fadeMs {
			return tl, fmt.Errorf("scene %s lasts %dms, too short to fade in and out", sc.ID, sc.DurationMs)
		}
		tl.scenes = append(tl.scenes, interval{start, tl.totalMs})
		tl.steps = append(tl.steps, steps)
	}
	if tl.totalMs == 0 {
		return tl, errors.New("no scenes")
	}
	return tl, nil
}

// keyframe is a property's value at a time in the loop.
type keyframe struct {
	ms    int
	value string
}

// sceneFrames fades in at the start of iv and out at its end.
func sceneFrames(iv interval, total int, off, on string) []keyframe {
	return []keyframe{
		{0, off}, {iv.start, off}, {iv.start + fadeMs, on},
		{iv.end - fadeMs, on}, {iv.end, off}, {total, off},
	}
}

// stepFrames shows a step during iv. Steps run with step-end timing, so each
// value holds until the next keyframe and the switch is instant.
func stepFrames(iv interval, total int) []keyframe {
	return []keyframe{{0, "0"}, {iv.start, "1"}, {iv.end, "0"}, {total, "0"}}
}

// svgWriter turns the drawn scenes into SVG markup.
type svgWriter struct {
	grid     grid
	colors   colorSet
	timeline timeline
	classes  map[string]string // fill -> class, numbered in first-use order
	order    []string          // fills in first-use order
	anims    []string          // keyframe rules, in the order elements use them
}

// class returns the CSS class for a fill, adding one the first time.
func (w *svgWriter) class(fill string) string {
	if c, ok := w.classes[fill]; ok {
		return c
	}
	c := "c" + strconv.Itoa(len(w.order))
	w.classes[fill] = c
	w.order = append(w.order, fill)
	return c
}

// animate adds a keyframe rule for one element and returns its name.
func (w *svgWriter) animate(prop string, frames []keyframe) string {
	name := "k" + strconv.Itoa(len(w.anims))
	var b strings.Builder
	fmt.Fprintf(&b, "@keyframes %s{", name)
	for i, f := range frames {
		// Of two keyframes at the same time, the later one wins.
		if i+1 < len(frames) && frames[i+1].ms == f.ms {
			continue
		}
		fmt.Fprintf(&b, "%s%%{%s:%s}", percent(f.ms, w.timeline.totalMs), prop, f.value)
	}
	b.WriteString("}")
	w.anims = append(w.anims, b.String())
	return name
}

func (w *svgWriter) width() float64 {
	return math.Ceil(2*padX + float64(w.grid.columns)*w.grid.cell)
}

func (w *svgWriter) terminalTop() float64 {
	return padTop + 2*rowHeight + 2*headerGap
}

func (w *svgWriter) dotsY() float64 {
	return w.terminalTop() + float64(w.grid.rows)*rowHeight + dotGap + dotRadius
}

func (w *svgWriter) height() float64 {
	return w.dotsY() + dotRadius + padBottom
}

func (w *svgWriter) colX(col int) float64 {
	return padX + float64(col)*w.grid.cell
}

func (w *svgWriter) write(doc *Document, headers []screen, scenes [][]screen, fontData []byte) []byte {
	var body strings.Builder
	for i := range scenes {
		w.writeScene(&body, i, headers[i], scenes[i])
	}
	w.writeDots(&body, len(scenes))

	var b strings.Builder
	width, height := num(w.width()), num(w.height())
	fmt.Fprintf(&b, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%s\" height=\"%s\" viewBox=\"0 0 %s %s\" role=\"img\" aria-labelledby=\"title desc\" xml:space=\"preserve\">\n",
		width, height, width, height)
	b.WriteString("<title id=\"title\">paceline in a Claude Code window</title>\n")
	fmt.Fprintf(&b, "<desc id=\"desc\">%s</desc>\n", escapeXML(description(doc)))
	w.writeStyle(&b, fontData)
	fmt.Fprintf(&b, "<rect x=\"0.5\" y=\"0.5\" width=\"%s\" height=\"%s\" rx=\"%s\" fill=\"%s\" stroke=\"%s\" stroke-opacity=\"0.4\"/>\n",
		num(w.width()-1), num(w.height()-1), num(frameRadius), w.colors["background"], w.colors["dim"])
	fmt.Fprintf(&b, "<rect x=\"1\" y=\"%s\" width=\"%s\" height=\"1\" fill=\"%s\" fill-opacity=\"0.4\"/>\n",
		num(padTop+2*rowHeight+headerGap), num(w.width()-2), w.colors["dim"])
	b.WriteString(body.String())
	b.WriteString("</svg>\n")
	return []byte(b.String())
}

// description is the graphic's text alternative: the scenes in order and
// what the numbers mean.
func description(doc *Document) string {
	titles := make([]string, len(doc.Scenes))
	for i, sc := range doc.Scenes {
		titles[i] = sc.Title
	}
	list := strings.Join(titles, ", ")
	if n := len(titles); n > 2 {
		list = strings.Join(titles[:n-1], ", ") + ", and " + titles[n-1]
	}
	return fmt.Sprintf("A Claude Code window with paceline's status line under the prompt, playing %d scenes in a loop: %s. "+
		"Session, week, and today count up what's used of each, from 0%%.", len(titles), list)
}

func (w *svgWriter) writeStyle(b *strings.Builder, fontData []byte) {
	b.WriteString("<style>\n")
	b.WriteString("/* Cascadia Mono 2407.24, (c) Microsoft Corporation, SIL Open Font License 1.1, subset to the characters drawn here. */\n")
	fmt.Fprintf(b, "@font-face{font-family:%q;src:url(data:font/woff;base64,%s) format(\"woff\")}\n",
		fontFamily, base64.StdEncoding.EncodeToString(fontData))
	fmt.Fprintf(b, "text{font-family:%q,monospace;font-size:%spx;fill:%s;font-kerning:none;font-variant-ligatures:none}\n",
		fontFamily, num(fontSize), w.colors["foreground"])
	b.WriteString(".b{font-weight:bold}\n")
	for _, fill := range w.order {
		fmt.Fprintf(b, ".%s{fill:%s}\n", w.classes[fill], fill)
	}
	fmt.Fprintf(b, ".a{animation-duration:%dms;animation-iteration-count:infinite}\n", w.timeline.totalMs)
	b.WriteString(".s,.d{animation-timing-function:linear}\n.p{animation-timing-function:step-end}\n")
	fmt.Fprintf(b, ".s,.p{opacity:0}\n.on{opacity:1}\n.d{fill:%s}\n.d.on{fill:%s}\n", w.colors["dim"], w.colors["spinner"])
	for _, a := range w.anims {
		b.WriteString(a + "\n")
	}
	b.WriteString("@media (prefers-reduced-motion:reduce){.a{animation:none!important}}\n")
	b.WriteString("</style>\n")
}

// writeScene writes the scene's group: its header, the rows every step
// shares, then a group per step with the rows that change.
func (w *svgWriter) writeScene(b *strings.Builder, i int, header screen, steps []screen) {
	on := ""
	if i == 0 {
		on = " on"
	}
	name := w.animate("opacity", sceneFrames(w.timeline.scenes[i], w.timeline.totalMs, "0", "1"))
	fmt.Fprintf(b, "<g class=\"s a%s\" style=\"animation-name:%s\">\n", on, name)
	for r, hr := range header {
		b.WriteString(w.rowMarkup(hr, padTop+float64(r)*rowHeight))
	}
	rows := 0
	for _, s := range steps {
		rows = max(rows, len(s))
	}
	markup := make([][]string, len(steps))
	for k, s := range steps {
		markup[k] = make([]string, rows)
		for r, sr := range s {
			markup[k][r] = w.rowMarkup(sr, w.terminalTop()+float64(r)*rowHeight)
		}
	}
	shared := make([]bool, rows)
	for r := range rows {
		shared[r] = true
		for k := range steps {
			shared[r] = shared[r] && markup[k][r] == markup[0][r]
		}
		if shared[r] {
			b.WriteString(markup[0][r])
		}
	}
	for k := range steps {
		stepOn := ""
		if k == 0 {
			stepOn = " on"
		}
		name := w.animate("opacity", stepFrames(w.timeline.steps[i][k], w.timeline.totalMs))
		fmt.Fprintf(b, "<g class=\"p a%s\" style=\"animation-name:%s\">\n", stepOn, name)
		for r := range rows {
			if !shared[r] {
				b.WriteString(markup[k][r])
			}
		}
		b.WriteString("</g>\n")
	}
	b.WriteString("</g>\n")
}

func (w *svgWriter) writeDots(b *strings.Builder, n int) {
	first := w.width()/2 - float64(n-1)*dotSpacing/2
	for i := range n {
		on := ""
		if i == 0 {
			on = " on"
		}
		name := w.animate("fill", sceneFrames(w.timeline.scenes[i], w.timeline.totalMs, w.colors["dim"], w.colors["spinner"]))
		fmt.Fprintf(b, "<circle class=\"d a%s\" style=\"animation-name:%s\" cx=\"%s\" cy=\"%s\" r=\"%s\"/>\n",
			on, name, num(first+float64(i)*dotSpacing), num(w.dotsY()), num(dotRadius))
	}
}

// rowMarkup draws one row whose top is at y. Text goes in one text element
// with a tspan per stretch of text, each at its column's absolute x, so no
// width error can build up along the line.
func (w *svgWriter) rowMarkup(r row, y float64) string {
	var b strings.Builder
	if r.rule {
		fmt.Fprintf(&b, "<rect class=\"%s\" x=\"%s\" y=\"%s\" width=\"%s\" height=\"1\"/>\n",
			w.class(w.colors["promptRule"]), num(padX), num(y+rowHeight/2-0.5), num(float64(w.grid.columns)*w.grid.cell))
	}
	if r.text == nil {
		return b.String()
	}
	var shapes []segment
	fmt.Fprintf(&b, "<text y=\"%s\">", num(y+baseline))
	for _, s := range r.text.segs {
		if s.shape != 0 {
			shapes = append(shapes, s)
			continue
		}
		classes := []string{}
		if s.fill != w.colors["foreground"] {
			classes = append(classes, w.class(s.fill))
		}
		if s.bold {
			classes = append(classes, "b")
		}
		attr := ""
		if len(classes) > 0 {
			attr = " class=\"" + strings.Join(classes, " ") + "\""
		}
		for _, p := range pieces(s.col, s.text) {
			fmt.Fprintf(&b, "<tspan x=\"%s\"%s>%s</tspan>", num(w.colX(p.col)), attr, escapeXML(p.text))
		}
	}
	b.WriteString("</text>\n")
	for _, s := range shapes {
		fmt.Fprintf(&b, "<path class=\"%s\" d=\"%s\"/>\n", w.class(s.fill), w.shapePath(s, y))
	}
	if r.cursor {
		fmt.Fprintf(&b, "<rect class=\"%s\" x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\"/>\n",
			w.class(w.colors["foreground"]), num(w.colX(r.text.end)), num(y+1.5), num(w.grid.cell), num(rowHeight-3))
	}
	return b.String()
}

// shapePath draws a character the font lacks inside its cell, centred on
// the height of the font's lowercase letters.
func (w *svgWriter) shapePath(s segment, y float64) string {
	x0, cw := w.colX(s.col), w.grid.cell
	cy := y + baseline - 0.36*fontSize
	if s.shape == modeArrow {
		h := 0.27 * fontSize
		return fmt.Sprintf("M%s %sL%s %sL%s %sZ",
			num(x0+0.18*cw), num(cy-h), num(x0+0.18*cw), num(cy+h), num(x0+0.88*cw), num(cy))
	}
	// The spinner star: six teardrop spokes, narrow at the centre and round
	// at the tip.
	cx, reach, half := x0+cw/2, 0.6*cw, 0.15*cw
	var b strings.Builder
	for k := range 6 {
		a := float64(k)*math.Pi/3 - math.Pi/2
		ux, uy := math.Cos(a), math.Sin(a)
		px, py := -uy, ux
		mid := reach - half
		fmt.Fprintf(&b, "M%s %sL%s %sA%s %s 0 1 0 %s %sZ",
			num(cx), num(cy),
			num(cx+ux*mid+px*half), num(cy+uy*mid+py*half),
			num(half), num(half),
			num(cx+ux*mid-px*half), num(cy+uy*mid-py*half))
	}
	return b.String()
}

// piece is text with no leading, trailing, or doubled spaces, so it renders
// the same whatever a browser does with white space.
type piece struct {
	col  int
	text string
}

// pieces splits text starting at col where it has spaces at its ends or two
// in a row; each piece is drawn at its own column.
func pieces(col int, text string) []piece {
	rs := []rune(text)
	var out []piece
	for i := 0; i < len(rs); {
		if rs[i] == ' ' {
			i++
			continue
		}
		j := i
		for j < len(rs) && (rs[j] != ' ' || j+1 < len(rs) && rs[j+1] != ' ') {
			j++
		}
		out = append(out, piece{col + i, string(rs[i:j])})
		i = j
	}
	return out
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeXML(s string) string { return xmlEscaper.Replace(s) }

// num formats a coordinate to two decimals without trailing zeros.
func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// percent is ms as a percentage of total, to three decimals.
func percent(ms, total int) string {
	s := strconv.FormatFloat(float64(ms)*100/float64(total), 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
