package vt

import "strings"

// sgr applies Select Graphic Rendition parameters to the pen.
func (t *Terminal) sgr() {
	ps := t.p.params
	// SGR 0 keeps an open hyperlink (OSC 8 ends it).
	reset := Style{Link: t.pen.Link}
	if len(ps) == 0 {
		t.pen = reset
		return
	}
	for i := 0; i < len(ps); i++ {
		g := ps[i]
		v := 0
		if len(g) > 0 && g[0] > 0 {
			v = g[0]
		}
		switch {
		case v == 0:
			t.pen = reset
		case v == 1:
			t.pen.Attr |= Bold
		case v == 2:
			t.pen.Attr |= Faint
		case v == 3:
			t.pen.Attr |= Italic
		case v == 4:
			// 4:n selects the style (0 off, 1 single, 2 double, 3 curly,
			// 4 dotted, 5 dashed).
			st := ULSingle
			if len(g) > 1 && g[1] >= 0 {
				st = UnderlineStyle(min(g[1], 255))
			}
			t.pen.Attr = t.pen.Attr.withUnderline(st)
		case v == 5 || v == 6:
			t.pen.Attr |= Blink
		case v == 7:
			t.pen.Attr |= Inverse
		case v == 8:
			t.pen.Attr |= Hidden
		case v == 9:
			t.pen.Attr |= Strike
		case v == 21:
			t.pen.Attr = t.pen.Attr.withUnderline(ULDouble)
		case v == 22:
			t.pen.Attr &^= Bold | Faint
		case v == 23:
			t.pen.Attr &^= Italic
		case v == 24:
			t.pen.Attr = t.pen.Attr.withUnderline(ULNone)
		case v == 25:
			t.pen.Attr &^= Blink
		case v == 27:
			t.pen.Attr &^= Inverse
		case v == 28:
			t.pen.Attr &^= Hidden
		case v == 29:
			t.pen.Attr &^= Strike
		case v >= 30 && v <= 37:
			t.pen.FG = Indexed(uint8(v - 30))
		case v == 38 || v == 48 || v == 58:
			c, used := extColor(ps, i)
			i += used
			switch v {
			case 38:
				t.pen.FG = c
			case 48:
				t.pen.BG = c
			case 58:
				t.pen.UL = c
			}
		case v == 39:
			t.pen.FG = Default
		case v >= 40 && v <= 47:
			t.pen.BG = Indexed(uint8(v - 40))
		case v == 49:
			t.pen.BG = Default
		case v == 53:
			t.pen.Attr |= Overline
		case v == 55:
			t.pen.Attr &^= Overline
		case v == 59:
			t.pen.UL = Default
		case v >= 90 && v <= 97:
			t.pen.FG = Indexed(uint8(v - 90 + 8))
		case v >= 100 && v <= 107:
			t.pen.BG = Indexed(uint8(v - 100 + 8))
		}
	}
}

// extColor parses an extended color at group i: the colon form
// (38:5:n, 38:2::r:g:b, 38:2:r:g:b) inside the group, or the semicolon
// form (38;5;n, 38;2;r;g;b) over the following groups. used is the
// number of following groups consumed.
func extColor(ps [][]int, i int) (c Color, used int) {
	g := ps[i]
	val := func(x int) uint8 {
		if x < 0 {
			return 0
		}
		return uint8(min(x, 255))
	}
	if len(g) > 1 {
		switch g[1] {
		case 5:
			if len(g) > 2 {
				return Indexed(val(g[2])), 0
			}
		case 2:
			rgb := g[2:]
			if len(rgb) >= 4 { // with color space id
				rgb = rgb[1:]
			}
			if len(rgb) >= 3 {
				return RGB(val(rgb[0]), val(rgb[1]), val(rgb[2])), 0
			}
		}
		return Default, 0
	}
	first := func(j int) int {
		if j >= len(ps) || len(ps[j]) == 0 {
			return -1
		}
		return ps[j][0]
	}
	switch first(i + 1) {
	case 5:
		if i+2 < len(ps) {
			return Indexed(val(first(i + 2))), 2
		}
		return Default, len(ps) - i - 1
	case 2:
		if i+4 < len(ps) {
			return RGB(val(first(i+2)), val(first(i+3)), val(first(i+4))), 4
		}
		return Default, len(ps) - i - 1
	}
	return Default, 0
}

// Span is a run of cells with the same style in a line.
type Span struct {
	// Col is the first column, Width the number of columns.
	Col, Width int
	Text       string
	Style      Style
}

// Spans splits line i (see Line) into styled runs; trailing blank cells
// with the default style are left out. Wide characters count two
// columns; their right halves add no text.
func (t *Terminal) Spans(i int) []Span {
	l := t.Line(i)
	n := len(l.Cells)
	for n > 0 {
		c := l.Cells[n-1]
		if c.R != 0 || c.Width == 0 || c.Style != (Style{}) {
			break
		}
		n--
	}
	var out []Span
	var b strings.Builder
	start := 0
	var st Style
	flush := func(end int) {
		if end > start {
			out = append(out, Span{Col: start, Width: end - start, Text: b.String(), Style: st})
		}
		b.Reset()
		start = end
	}
	afterWide := false
	for col := 0; col < n; col++ {
		c := l.Cells[col]
		if c.Width == 0 {
			afterWide = col > 0 && l.Cells[col-1].Width == 2
			continue
		}
		if col == start {
			st = c.Style
		} else if c.Style != st || c.Width == 2 || afterWide {
			// Wide characters get their own span so the renderer can
			// place them on the grid.
			flush(col)
			st = c.Style
		}
		afterWide = false
		b.WriteString(t.Grapheme(c))
	}
	flush(n)
	return out
}

// LineText returns the text of line i without trailing blanks.
func (t *Terminal) LineText(i int) string {
	var b strings.Builder
	for _, s := range t.Spans(i) {
		b.WriteString(s.Text)
	}
	return strings.TrimRight(b.String(), " ")
}

// Text returns the text from (line1, col1) to (line2, col2) (exclusive
// end column) over scrollback and screen. Lines are separated by "\n"
// unless the first one wrapped; trailing blanks of each line are
// dropped.
func (t *Terminal) Text(line1, col1, line2, col2 int) string {
	if line2 < line1 || line2 == line1 && col2 < col1 {
		line1, col1, line2, col2 = line2, col2, line1, col1
	}
	line1 = max(0, line1)
	line2 = min(t.Total()-1, line2)
	var b strings.Builder
	for i := line1; i <= line2; i++ {
		l := t.Line(i)
		from, to := 0, len(l.Cells)
		if i == line1 {
			from = max(0, min(col1, to))
		}
		if i == line2 {
			to = max(from, min(col2, to))
		}
		var lb strings.Builder
		for c := from; c < to; c++ {
			if l.Cells[c].Width == 0 {
				continue
			}
			lb.WriteString(t.Grapheme(l.Cells[c]))
		}
		s := lb.String()
		if !(l.Wrapped && i < line2) || i == line2 {
			s = strings.TrimRight(s, " ")
		}
		b.WriteString(s)
		if i < line2 && !l.Wrapped {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ScreenText returns the visible screen as text, one line per row,
// trailing blanks and trailing empty rows removed.
func (t *Terminal) ScreenText() string {
	h := t.History()
	lines := make([]string, t.rows)
	for r := range lines {
		lines[r] = t.LineText(h + r)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
