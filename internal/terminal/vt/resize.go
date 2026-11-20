package vt

// Resize changes the screen size. When the width changes, the main
// screen and the scrollback reflow: lines joined by autowrap are
// rewrapped to the new width and the cursor stays on its character.
// The alternate screen (full-screen programs redraw on SIGWINCH) is cut
// or padded. When only the height changes, rows below the cursor are
// dropped first and then rows above it move to the scrollback; more
// rows pull lines back from the scrollback (like xterm).
func (t *Terminal) Resize(cols, rows int) {
	cols, rows = max(1, cols), max(1, rows)
	if cols == t.cols && rows == t.rows {
		return
	}
	t.seq++
	if cols != t.cols {
		t.reflowMain(cols, rows)
		t.resizeAlt(cols, rows)
	} else {
		t.resizeRows(rows)
	}
	t.cols, t.rows = cols, rows
	t.row = max(0, min(rows-1, t.row))
	t.col = max(0, min(cols-1, t.col))
	t.wrapNext = false
	t.top, t.bottom = 0, rows-1
	t.resetTabs()
	for _, s := range []*screen{&t.main, &t.alt} {
		s.saved.row = max(0, min(s.saved.row, rows-1))
		s.saved.col = max(0, min(s.saved.col, cols-1))
	}
}

// resizeRows changes the number of rows at the same width.
func (t *Terminal) resizeRows(rows int) {
	cols := t.cols
	if rows < t.rows {
		extra := t.rows - rows
		for _, s := range []*screen{&t.main, &t.alt} {
			// Each screen is cut at its own cursor: the active one at
			// the cursor, the main screen behind the alternate one at
			// the cursor saved on entry (1049), where it continues.
			r := t.row
			if s != t.cur && s.saved.valid {
				r = s.saved.row
			}
			below := max(0, len(s.lines)-1-r)
			cut := min(extra, below)
			s.lines = s.lines[:len(s.lines)-cut]
			over := max(0, len(s.lines)-rows)
			if over > 0 {
				if s == &t.main {
					for _, l := range s.lines[:over] {
						t.pushScrollback(l)
					}
				}
				s.lines = s.lines[over:]
			}
			// Positions move up with the content.
			if s == t.cur {
				t.row = max(0, t.row-over)
			}
			if s.saved.valid {
				s.saved.row = max(0, s.saved.row-over)
			}
		}
		return
	}
	// Pull lines back from the scrollback (main screen) so the content
	// stays at the bottom, like xterm.
	add := rows - t.rows
	pull := 0
	if t.cur == &t.main {
		pull = min(add, len(t.scrollback))
	}
	if pull > 0 {
		back := make([]Line, 0, rows)
		h := len(t.scrollback)
		for i := h - pull; i < h; i++ {
			back = append(back, fitLine(t.Line(i), cols))
		}
		t.dropScrollbackTail(pull)
		t.main.lines = append(back, t.main.lines...)
		t.row += pull
		if t.main.saved.valid {
			t.main.saved.row += pull
		}
	}
	for len(t.main.lines) < rows {
		t.main.lines = append(t.main.lines, newLine(cols, Style{}))
	}
	for len(t.alt.lines) < rows {
		t.alt.lines = append(t.alt.lines, newLine(cols, Style{}))
	}
}

// resizeAlt fits the alternate screen to a new size (no reflow).
func (t *Terminal) resizeAlt(cols, rows int) {
	s := &t.alt
	for i := range s.lines {
		s.lines[i] = fitLine(s.lines[i], cols)
	}
	active := t.cur == s
	if len(s.lines) > rows {
		extra := len(s.lines) - rows
		r := len(s.lines) - 1
		if active {
			r = t.row
		}
		cut := min(extra, max(0, len(s.lines)-1-r))
		s.lines = s.lines[:len(s.lines)-cut]
		over := max(0, len(s.lines)-rows)
		s.lines = s.lines[over:]
		if active {
			t.row = max(0, t.row-over)
		}
	}
	for len(s.lines) < rows {
		s.lines = append(s.lines, newLine(cols, Style{}))
	}
}

// point is a tracked position during reflow: absolute line (scrollback
// plus screen) and column.
type point struct {
	line, col int
	valid     bool
}

// reflowMain rewraps scrollback and main screen to cols and lays them
// out on rows rows. The cursor (when the main screen is active) and the
// saved cursor of the main screen keep their characters.
func (t *Terminal) reflowMain(cols, rows int) {
	h := len(t.scrollback)
	s := &t.main
	active := t.cur == s
	// Tracked points: 0 the cursor, 1 the saved cursor.
	var pts [2]point
	if active {
		pts[0] = point{line: h + t.row, col: t.col, valid: true}
	}
	if s.saved.valid {
		pts[1] = point{line: h + s.saved.row, col: s.saved.col, valid: true}
	}
	// The lowest line to keep: below the cursors only content counts.
	last := h + len(s.lines) - 1
	for last >= h && s.lines[last-h].contentLen() == 0 && !s.lines[last-h].Wrapped && last > pointsMax(pts) {
		last--
	}
	all := make([]Line, 0, last+1)
	for i := 0; i <= last; i++ {
		if i < h {
			all = append(all, t.scrollback[(t.sbStart+i)%h])
		} else {
			all = append(all, s.lines[i-h])
		}
	}
	out := make([]Line, 0, len(all)+rows)
	var newPts [2]point
	for i := 0; i < len(all); {
		// One logical line: lines joined by autowrap.
		j := i
		for j < len(all)-1 && all[j].Wrapped {
			j++
		}
		var cells []Cell
		mark := Mark(0)
		// Offsets of the tracked points in the logical line.
		offs := [2]int{-1, -1}
		for k := i; k <= j; k++ {
			l := all[k]
			mark |= l.Mark
			for p := range pts {
				if pts[p].valid && pts[p].line == k {
					offs[p] = len(cells) + pts[p].col
				}
			}
			n := len(l.Cells)
			if k == j {
				n = l.contentLen()
			} else if n > 0 && l.Cells[n-1].R == 0 && len(all[k+1].Cells) > 0 && all[k+1].Cells[0].Width == 2 {
				// The blank a wide character left when it moved to the
				// next line is no content.
				n--
			}
			cells = append(cells, l.Cells[:n]...)
		}
		start := len(out)
		out = rewrap(out, cells, cols)
		if len(out) > start {
			out[start].Mark = mark
		}
		for p := range pts {
			if offs[p] < 0 {
				continue
			}
			// Find the new line of the offset (lines hold cols cells
			// except where a wide character moved on).
			line, off := start, offs[p]
			for line < len(out)-1 && out[line].Wrapped && off >= len(out[line].Cells) {
				off -= len(out[line].Cells)
				line++
			}
			for off >= cols {
				// Past the content: further rows of the same line.
				off -= cols
				line++
				for line >= len(out) {
					out = append(out, Line{})
				}
			}
			newPts[p] = point{line: line, col: off, valid: true}
		}
		i = j + 1
	}
	for i := range out {
		out[i] = fitLine(out[i], cols)
	}
	// The screen shows the last rows lines (the cursor must be on it).
	startScreen := max(0, len(out)-rows)
	if newPts[0].valid && newPts[0].line < startScreen {
		startScreen = newPts[0].line
	}
	screenLines := out[startScreen:min(len(out), startScreen+rows)]
	s.lines = make([]Line, 0, rows)
	s.lines = append(s.lines, screenLines...)
	for len(s.lines) < rows {
		s.lines = append(s.lines, newLine(cols, Style{}))
	}
	// Scrollback: the lines above the screen, at most maxScroll.
	sb := out[:startScreen]
	if t.maxScroll <= 0 {
		t.dropped += len(sb)
		sb = nil
	} else if len(sb) > t.maxScroll {
		t.dropped += len(sb) - t.maxScroll
		sb = sb[len(sb)-t.maxScroll:]
	}
	t.scrollback, t.sbStart = make([]Line, 0, len(sb)), 0
	for _, l := range sb {
		n := l.contentLen()
		t.scrollback = append(t.scrollback, Line{Cells: l.Cells[:n:n], Wrapped: l.Wrapped, Mark: l.Mark})
	}
	if newPts[0].valid {
		t.row = newPts[0].line - startScreen
		t.col = newPts[0].col
	}
	if newPts[1].valid {
		s.saved.row = max(0, newPts[1].line-startScreen)
		s.saved.col = newPts[1].col
		s.saved.pendingEOL = false
	}
}

func pointsMax(pts [2]point) int {
	m := -1
	for _, p := range pts {
		if p.valid {
			m = max(m, p.line)
		}
	}
	return m
}

// rewrap appends the cells of one logical line to out as lines of at
// most cols cells; a wide character never splits (it moves to the next
// line). An empty logical line gives one empty line.
func rewrap(out []Line, cells []Cell, cols int) []Line {
	if len(cells) == 0 {
		return append(out, Line{})
	}
	for len(cells) > 0 {
		n := min(cols, len(cells))
		if n < len(cells) && n > 1 && cells[n].Width == 0 {
			// cells[n-1] is the left half of a wide character.
			n--
		}
		l := Line{Cells: append([]Cell(nil), cells[:n]...)}
		cells = cells[n:]
		l.Wrapped = len(cells) > 0
		out = append(out, l)
	}
	return out
}

// dropScrollbackTail removes the newest n scrollback lines.
func (t *Terminal) dropScrollbackTail(n int) {
	h := len(t.scrollback)
	if n <= 0 || h == 0 {
		return
	}
	// Linearise the ring, then cut.
	lin := make([]Line, 0, h)
	for i := 0; i < h; i++ {
		lin = append(lin, t.scrollback[(t.sbStart+i)%h])
	}
	t.scrollback, t.sbStart = lin[:h-n], 0
}

// fitLine returns l with exactly cols cells.
func fitLine(l Line, cols int) Line {
	switch {
	case len(l.Cells) == cols:
		return l
	case len(l.Cells) > cols:
		c := append([]Cell(nil), l.Cells[:cols]...)
		if c[cols-1].Width == 2 {
			c[cols-1] = Cell{Style: c[cols-1].Style, Width: 1}
		}
		return Line{Cells: c, Wrapped: false, Mark: l.Mark}
	}
	c := make([]Cell, cols)
	copy(c, l.Cells)
	for i := len(l.Cells); i < cols; i++ {
		c[i] = Cell{Width: 1}
	}
	return Line{Cells: c, Wrapped: l.Wrapped, Mark: l.Mark}
}
