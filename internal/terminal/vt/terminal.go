package vt

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// DefaultScrollback is the scrollback size used when New gets a negative
// value.
const DefaultScrollback = 10000

// screen is the main or the alternate screen.
type screen struct {
	lines []Line
	// saved cursor (DECSC) per screen.
	saved savedCursor
}

type savedCursor struct {
	valid      bool
	row, col   int
	style      Style
	origin     bool
	charsets   [2]bool
	activeG1   bool
	pendingEOL bool
}

// Terminal is the emulated terminal: screen, scrollback, cursor and
// modes. See the package documentation.
type Terminal struct {
	cols, rows int
	main, alt  screen
	cur        *screen
	// scrollback holds the lines scrolled off the top of the main screen,
	// oldest first; it is a ring of at most maxScroll lines starting at
	// sbStart.
	scrollback []Line
	sbStart    int
	maxScroll  int
	// dropped counts lines discarded from the scrollback.
	dropped int

	row, col int
	// wrapNext is the pending wrap: the cursor sits on the last column
	// after printing there; the next character wraps first.
	wrapNext bool
	pen      Style
	cursor   Cursor

	top, bottom int // scroll region (inclusive)
	tabs        []bool

	autowrap, origin, insert, newline bool
	modes                             Modes
	// charsets: G0/G1 use DEC special graphics; activeG1 after SO.
	charsets [2]bool
	activeG1 bool

	title, cwd string
	bells      int

	// graphemes interns base+combining sequences; Cell.R values from
	// graphemeBase on index it.
	graphemes []string
	gindex    map[string]rune

	p   parser
	seq uint64

	// Reply receives answers to device queries (DSR, DA); may be nil.
	Reply func([]byte)
}

const graphemeBase = 0x110000

// New returns a terminal of the given size. scrollback is the maximum
// number of lines kept above the screen (0: none, negative: default).
func New(cols, rows, scrollback int) *Terminal {
	if scrollback < 0 {
		scrollback = DefaultScrollback
	}
	t := &Terminal{maxScroll: scrollback, gindex: map[string]rune{}}
	t.cols, t.rows = max(1, cols), max(1, rows)
	t.reset()
	return t
}

// reset is RIS (and the initial state).
func (t *Terminal) reset() {
	t.pen = Style{}
	t.main = screen{lines: t.blankLines(t.rows)}
	t.alt = screen{lines: t.blankLines(t.rows)}
	t.cur = &t.main
	t.row, t.col, t.wrapNext = 0, 0, false
	t.top, t.bottom = 0, t.rows-1
	t.autowrap, t.origin, t.insert, t.newline = true, false, false, false
	t.modes = Modes{}
	t.cursor = Cursor{Visible: true}
	t.charsets, t.activeG1 = [2]bool{}, false
	t.resetTabs()
}

func (t *Terminal) blankLines(n int) []Line {
	ls := make([]Line, n)
	for i := range ls {
		ls[i] = newLine(t.cols, Style{})
	}
	return ls
}

func (t *Terminal) resetTabs() {
	t.tabs = make([]bool, t.cols)
	for i := 8; i < t.cols; i += 8 {
		t.tabs[i] = true
	}
}

// ---- accessors -------------------------------------------------------------

// Size returns the screen size.
func (t *Terminal) Size() (cols, rows int) { return t.cols, t.rows }

// Cursor returns the cursor.
func (t *Terminal) Cursor() Cursor {
	c := t.cursor
	c.Row, c.Col = t.row, min(t.col, t.cols-1)
	return c
}

// Modes returns the active modes.
func (t *Terminal) Modes() Modes {
	m := t.modes
	m.AltScreen = t.cur == &t.alt
	return m
}

// Title returns the window title set by OSC 0/2.
func (t *Terminal) Title() string { return t.title }

// Dir returns the working directory reported by OSC 7 (a local path,
// "" when unknown).
func (t *Terminal) Dir() string { return t.cwd }

// Bells returns how often BEL was received.
func (t *Terminal) Bells() int { return t.bells }

// Seq increases with every Write and Resize.
func (t *Terminal) Seq() uint64 { return t.seq }

// History returns the number of scrollback lines.
func (t *Terminal) History() int { return len(t.scrollback) }

// Dropped returns the number of lines discarded from the scrollback.
func (t *Terminal) Dropped() int { return t.dropped }

// Total returns the number of addressable lines: scrollback plus screen.
func (t *Terminal) Total() int { return len(t.scrollback) + t.rows }

// Line returns line i of the scrollback followed by the screen (0 is the
// oldest scrollback line; History() is the first screen row). The cells
// must not be modified.
func (t *Terminal) Line(i int) Line {
	h := len(t.scrollback)
	switch {
	case i < 0:
		return Line{}
	case i < h:
		return t.scrollback[(t.sbStart+i)%h]
	case i < h+t.rows:
		return t.cur.lines[i-h]
	}
	return Line{}
}

// Grapheme returns the text of a cell.
func (t *Terminal) Grapheme(c Cell) string {
	switch {
	case c.R == 0:
		return " "
	case c.R >= graphemeBase:
		if i := int(c.R - graphemeBase); i < len(t.graphemes) {
			return t.graphemes[i]
		}
		return " "
	}
	return string(c.R)
}

// ---- printing --------------------------------------------------------------

// decGraphics maps the DEC special graphics set (line drawing).
var decGraphics = map[rune]rune{
	'`': '◆', 'a': '▒', 'b': '␉', 'c': '␌', 'd': '␍', 'e': '␊', 'f': '°', 'g': '±',
	'h': '␤', 'i': '␋', 'j': '┘', 'k': '┐', 'l': '┌', 'm': '└', 'n': '┼', 'o': '⎺',
	'p': '⎻', 'q': '─', 'r': '⎼', 's': '⎽', 't': '├', 'u': '┤', 'v': '┴', 'w': '┬',
	'x': '│', 'y': '≤', 'z': '≥', '{': 'π', '|': '≠', '}': '£', '~': '·',
}

// runeWidth returns the column width of r (0 for combining marks).
func runeWidth(r rune) int {
	if r < 0x300 {
		return 1
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	return max(1, min(2, uniseg.StringWidth(string(r))))
}

func (t *Terminal) printRune(r rune) {
	g := 0
	if t.activeG1 {
		g = 1
	}
	if t.charsets[g] && r < 0x80 {
		if m, ok := decGraphics[r]; ok {
			r = m
		}
	}
	w := runeWidth(r)
	if w == 0 {
		t.combine(r)
		return
	}
	if t.wrapNext {
		if t.autowrap {
			t.cur.lines[t.row].Wrapped = true
			t.col = 0
			t.lineFeed()
		}
		t.wrapNext = false
	}
	if w == 2 && t.col == t.cols-1 {
		// A wide character does not fit: wrap (or stay) first.
		if t.autowrap {
			t.put(t.col, Cell{Style: t.pen, Width: 1})
			t.cur.lines[t.row].Wrapped = true
			t.col = 0
			t.lineFeed()
		} else {
			w = 1
		}
	}
	if t.insert {
		t.insertBlanks(w)
	}
	t.put(t.col, Cell{R: r, Style: t.pen, Width: uint8(w)})
	if w == 2 && t.col+1 < t.cols {
		t.put(t.col+1, Cell{Style: t.pen, Width: 0})
	}
	t.col += w
	if t.col >= t.cols {
		t.col = t.cols - 1
		t.wrapNext = true
	}
}

// put writes c at column col of the cursor row, clearing a wide
// character it partly overwrites.
func (t *Terminal) put(col int, c Cell) {
	l := t.cur.lines[t.row].Cells
	if col < 0 || col >= len(l) {
		return
	}
	old := l[col]
	if old.Width == 0 && c.Width != 0 && col > 0 && l[col-1].Width == 2 {
		l[col-1] = Cell{Style: l[col-1].Style, Width: 1}
	}
	if old.Width == 2 && col+1 < len(l) && c.Width != 2 {
		l[col+1] = Cell{Style: l[col+1].Style, Width: 1}
	}
	l[col] = c
}

// combine attaches a combining mark to the previous character.
func (t *Terminal) combine(r rune) {
	row, col := t.row, t.col-1
	if t.wrapNext {
		col = t.col
	}
	l := t.cur.lines[row].Cells
	if col < 0 || col >= len(l) {
		return
	}
	if l[col].Width == 0 && col > 0 {
		col--
	}
	c := &l[col]
	if c.R == 0 {
		return
	}
	s := t.Grapheme(*c) + string(r)
	if len(s) > 32 {
		return
	}
	id, ok := t.gindex[s]
	if !ok {
		if len(t.graphemes) >= 1<<16 {
			return
		}
		id = graphemeBase + rune(len(t.graphemes))
		t.graphemes = append(t.graphemes, s)
		t.gindex[s] = id
	}
	c.R = id
}

// ---- controls --------------------------------------------------------------

func (t *Terminal) control(c byte) {
	switch c {
	case 0x07:
		t.bells++
	case 0x08: // BS
		if t.wrapNext {
			t.wrapNext = false
			return
		}
		if t.col > 0 {
			t.col--
		}
	case 0x09: // HT
		t.tabForward(1)
	case 0x0a, 0x0b, 0x0c: // LF, VT, FF
		t.lineFeed()
		if t.newline {
			t.col = 0
		}
	case 0x0d: // CR
		t.col, t.wrapNext = 0, false
	case 0x0e: // SO
		t.activeG1 = true
	case 0x0f: // SI
		t.activeG1 = false
	}
}

func (t *Terminal) tabForward(n int) {
	t.wrapNext = false
	for ; n > 0 && t.col < t.cols-1; n-- {
		t.col++
		for t.col < t.cols-1 && !t.tabs[t.col] {
			t.col++
		}
	}
}

func (t *Terminal) tabBackward(n int) {
	t.wrapNext = false
	for ; n > 0 && t.col > 0; n-- {
		t.col--
		for t.col > 0 && !t.tabs[t.col] {
			t.col--
		}
	}
}

// lineFeed moves down one row, scrolling at the bottom of the region.
func (t *Terminal) lineFeed() {
	t.wrapNext = false
	switch {
	case t.row == t.bottom:
		t.scrollUp(t.top, t.bottom, 1)
	case t.row < t.rows-1:
		t.row++
	}
}

// reverseIndex moves up one row, scrolling down at the top of the region.
func (t *Terminal) reverseIndex() {
	t.wrapNext = false
	switch {
	case t.row == t.top:
		t.scrollDown(t.top, t.bottom, 1)
	case t.row > 0:
		t.row--
	}
}

// scrollUp scrolls rows top..bottom up by n; lines leaving the full main
// screen go to the scrollback.
func (t *Terminal) scrollUp(top, bottom, n int) {
	n = min(n, bottom-top+1)
	if n <= 0 {
		return
	}
	ls := t.cur.lines
	if t.cur == &t.main && top == 0 {
		for i := 0; i < n; i++ {
			t.pushScrollback(ls[i])
		}
	}
	copy(ls[top:], ls[top+n:bottom+1])
	for i := bottom - n + 1; i <= bottom; i++ {
		ls[i] = newLine(t.cols, t.pen)
	}
}

func (t *Terminal) scrollDown(top, bottom, n int) {
	n = min(n, bottom-top+1)
	if n <= 0 {
		return
	}
	ls := t.cur.lines
	copy(ls[top+n:bottom+1], ls[top:bottom+1-n])
	for i := top; i < top+n; i++ {
		ls[i] = newLine(t.cols, t.pen)
	}
}

func (t *Terminal) pushScrollback(l Line) {
	if t.maxScroll <= 0 {
		t.dropped++
		return
	}
	l = l.trimmed()
	if len(t.scrollback) < t.maxScroll {
		t.scrollback = append(t.scrollback, l)
		return
	}
	t.scrollback[t.sbStart] = l
	t.sbStart = (t.sbStart + 1) % len(t.scrollback)
	t.dropped++
}

// ClearScrollback discards the scrollback (ED 3).
func (t *Terminal) ClearScrollback() {
	t.dropped += len(t.scrollback)
	t.scrollback, t.sbStart = nil, 0
	t.seq++
}

// ---- escape sequences ----------------------------------------------------------

func (t *Terminal) escDispatch(c byte) {
	inter := t.p.inter
	if len(inter) == 1 && inter[0] == '#' {
		if c == '8' { // DECALN: fill with E
			for _, l := range t.cur.lines {
				for i := range l.Cells {
					l.Cells[i] = Cell{R: 'E', Width: 1}
				}
			}
		}
		return
	}
	if len(inter) > 0 {
		return
	}
	switch c {
	case '7':
		t.saveCursor()
	case '8':
		t.restoreCursor()
	case 'D':
		t.lineFeed()
	case 'E':
		t.lineFeed()
		t.col = 0
	case 'H':
		if t.col < t.cols {
			t.tabs[t.col] = true
		}
	case 'M':
		t.reverseIndex()
	case 'c':
		t.reset()
	case '=':
		t.modes.AppKeypad = true
	case '>':
		t.modes.AppKeypad = false
	case '\\': // ST without a string
	}
}

func (t *Terminal) designate(g int, c byte) {
	if g > 1 {
		return
	}
	t.charsets[g] = c == '0'
}

func (t *Terminal) saveCursor() {
	t.cur.saved = savedCursor{valid: true, row: t.row, col: t.col, style: t.pen, origin: t.origin,
		charsets: t.charsets, activeG1: t.activeG1, pendingEOL: t.wrapNext}
}

func (t *Terminal) restoreCursor() {
	s := t.cur.saved
	if !s.valid {
		t.row, t.col, t.pen, t.origin, t.wrapNext = 0, 0, Style{}, false, false
		return
	}
	t.row, t.col = min(s.row, t.rows-1), min(s.col, t.cols-1)
	t.pen, t.origin, t.charsets, t.activeG1, t.wrapNext = s.style, s.origin, s.charsets, s.activeG1, s.pendingEOL
}

// ---- CSI -------------------------------------------------------------------

func (t *Terminal) csiDispatch(final byte) {
	p := &t.p
	if len(p.inter) > 0 {
		t.csiIntermediate(final)
		return
	}
	switch p.priv {
	case '?':
		switch final {
		case 'h', 'l':
			for i := range p.params {
				t.setPrivateMode(p.rawParam(i, 0), final == 'h')
			}
		case 'J':
			t.eraseDisplay(p.rawParam(0, 0))
		case 'K':
			t.eraseLine(p.rawParam(0, 0))
		}
		return
	case '>':
		if final == 'c' { // secondary DA: VT220-ish, version 10
			t.reply("\x1b[>1;10;0c")
		}
		return
	case 0:
	default:
		return
	}
	n := p.param(0, 1)
	switch final {
	case '@':
		t.insertBlanks(n)
	case 'A':
		t.moveTo(t.row-n, t.col, true)
	case 'B', 'e':
		t.moveTo(t.row+n, t.col, true)
	case 'C', 'a':
		t.moveTo(t.row, t.col+n, true)
	case 'D':
		t.moveTo(t.row, t.col-n, true)
	case 'E':
		t.moveTo(t.row+n, 0, true)
	case 'F':
		t.moveTo(t.row-n, 0, true)
	case 'G', '`':
		t.moveTo(t.row, n-1, true)
	case 'H', 'f':
		row := p.param(0, 1) - 1
		col := p.param(1, 1) - 1
		if t.origin {
			row += t.top
		}
		t.moveTo(row, col, false)
	case 'I':
		t.tabForward(n)
	case 'J':
		t.eraseDisplay(p.rawParam(0, 0))
	case 'K':
		t.eraseLine(p.rawParam(0, 0))
	case 'L':
		t.insertLines(n)
	case 'M':
		t.deleteLines(n)
	case 'P':
		t.deleteChars(n)
	case 'S':
		t.scrollUp(t.top, t.bottom, n)
	case 'T':
		t.scrollDown(t.top, t.bottom, n)
	case 'X':
		t.eraseChars(n)
	case 'Z':
		t.tabBackward(n)
	case 'b':
		t.repeat(n)
	case 'c':
		if p.rawParam(0, 0) == 0 {
			t.reply("\x1b[?62;22c") // VT220 with ANSI color
		}
	case 'd':
		row := n - 1
		if t.origin {
			row += t.top
		}
		t.moveTo(row, t.col, false)
	case 'g':
		switch p.rawParam(0, 0) {
		case 0:
			if t.col < t.cols {
				t.tabs[t.col] = false
			}
		case 3:
			clear(t.tabs)
		}
	case 'h', 'l':
		for i := range p.params {
			switch p.rawParam(i, 0) {
			case 4:
				t.insert = final == 'h'
			case 20:
				t.newline = final == 'h'
			}
		}
	case 'm':
		t.sgr()
	case 'n':
		switch p.rawParam(0, 0) {
		case 5:
			t.reply("\x1b[0n")
		case 6:
			row := t.row + 1
			if t.origin {
				row -= t.top
			}
			t.reply("\x1b[" + strconv.Itoa(row) + ";" + strconv.Itoa(min(t.col, t.cols-1)+1) + "R")
		}
	case 'r':
		top := p.param(0, 1) - 1
		bottom := p.param(1, t.rows) - 1
		bottom = min(bottom, t.rows-1)
		if top < bottom {
			t.top, t.bottom = top, bottom
			t.moveTo(0, 0, false)
			if t.origin {
				t.row = t.top
			}
		}
	case 's':
		t.saveCursor()
	case 'u':
		t.restoreCursor()
	}
}

// csiIntermediate handles CSI sequences with intermediate bytes.
func (t *Terminal) csiIntermediate(final byte) {
	p := &t.p
	switch string(p.inter) {
	case " ":
		if final == 'q' { // DECSCUSR
			t.cursor.Shape = p.rawParam(0, 0)
		}
	case "!":
		if final == 'p' { // DECSTR soft reset
			t.pen = Style{}
			t.insert, t.origin, t.autowrap = false, false, true
			t.top, t.bottom = 0, t.rows-1
			t.cursor.Visible = true
			t.modes.AppCursor, t.modes.AppKeypad = false, false
			t.charsets, t.activeG1 = [2]bool{}, false
		}
	}
}

func (t *Terminal) reply(s string) {
	if t.Reply != nil {
		t.Reply([]byte(s))
	}
}

// moveTo places the cursor; relative moves stay inside the scroll region
// when they start inside it.
func (t *Terminal) moveTo(row, col int, relative bool) {
	t.wrapNext = false
	minRow, maxRow := 0, t.rows-1
	if relative && t.row >= t.top && t.row <= t.bottom {
		minRow, maxRow = t.top, t.bottom
	} else if !relative && t.origin {
		minRow, maxRow = t.top, t.bottom
	}
	t.row = max(minRow, min(maxRow, row))
	t.col = max(0, min(t.cols-1, col))
}

func (t *Terminal) setPrivateMode(mode int, on bool) {
	switch mode {
	case 1:
		t.modes.AppCursor = on
	case 5:
		t.modes.ReverseVideo = on
	case 6:
		t.origin = on
		t.moveTo(0, 0, false)
		if on {
			t.row = t.top
		}
	case 7:
		t.autowrap = on
		if !on {
			t.wrapNext = false
		}
	case 25:
		t.cursor.Visible = on
	case 47, 1047:
		t.switchScreen(on, false)
	case 1048:
		if on {
			t.saveCursor()
		} else {
			t.restoreCursor()
		}
	case 1049:
		t.switchScreen(on, true)
	case 1000, 1002, 1003:
		if on {
			t.modes.Mouse = mode
		} else if t.modes.Mouse == mode {
			t.modes.Mouse = 0
		}
	case 1004:
		t.modes.FocusEvents = on
	case 1006:
		t.modes.MouseSGR = on
	case 2004:
		t.modes.BracketedPaste = on
	}
}

// switchScreen enters or leaves the alternate screen; with save (1049)
// the cursor is saved before entering and restored after leaving, and
// the alternate screen is cleared on entry.
func (t *Terminal) switchScreen(alt, save bool) {
	if alt == (t.cur == &t.alt) {
		return
	}
	if alt {
		if save {
			t.saveCursor()
		}
		t.cur = &t.alt
		t.alt.saved = t.main.saved
		if save {
			for i := range t.alt.lines {
				t.alt.lines[i] = newLine(t.cols, Style{})
			}
		}
		return
	}
	t.alt.lines = t.blankLines(t.rows)
	t.cur = &t.main
	if save {
		t.restoreCursor()
	}
	t.wrapNext = false
}

// ---- erase, insert, delete --------------------------------------------------

func (t *Terminal) eraseDisplay(mode int) {
	t.wrapNext = false
	switch mode {
	case 0:
		t.eraseLine(0)
		for r := t.row + 1; r < t.rows; r++ {
			t.cur.lines[r] = newLine(t.cols, t.pen)
		}
	case 1:
		t.eraseLine(1)
		for r := 0; r < t.row; r++ {
			t.cur.lines[r] = newLine(t.cols, t.pen)
		}
	case 2:
		for r := range t.cur.lines {
			t.cur.lines[r] = newLine(t.cols, t.pen)
		}
	case 3:
		t.ClearScrollback()
	}
}

func (t *Terminal) eraseLine(mode int) {
	t.wrapNext = false
	l := &t.cur.lines[t.row]
	from, to := 0, t.cols
	switch mode {
	case 0:
		from = t.col
		l.Wrapped = false
	case 1:
		to = t.col + 1
	case 2:
		l.Wrapped = false
	default:
		return
	}
	for i := from; i < to && i < len(l.Cells); i++ {
		t.put(i, blank(t.pen))
	}
}

func (t *Terminal) eraseChars(n int) {
	t.wrapNext = false
	for i := t.col; i < t.col+n && i < t.cols; i++ {
		t.put(i, blank(t.pen))
	}
}

func (t *Terminal) insertBlanks(n int) {
	l := t.cur.lines[t.row].Cells
	n = min(n, t.cols-t.col)
	copy(l[t.col+n:], l[t.col:t.cols-n])
	for i := t.col; i < t.col+n; i++ {
		l[i] = blank(t.pen)
	}
	t.fixWide(l)
}

func (t *Terminal) deleteChars(n int) {
	t.wrapNext = false
	l := t.cur.lines[t.row].Cells
	n = min(n, t.cols-t.col)
	copy(l[t.col:], l[t.col+n:])
	for i := t.cols - n; i < t.cols; i++ {
		l[i] = blank(t.pen)
	}
	t.fixWide(l)
}

// fixWide blanks halves of wide characters split by a shift.
func (t *Terminal) fixWide(l []Cell) {
	for i := range l {
		switch {
		case l[i].Width == 2 && (i+1 >= len(l) || l[i+1].Width != 0):
			l[i] = Cell{Style: l[i].Style, Width: 1}
		case l[i].Width == 0 && (i == 0 || l[i-1].Width != 2):
			l[i] = Cell{Style: l[i].Style, Width: 1}
		}
	}
}

func (t *Terminal) insertLines(n int) {
	if t.row < t.top || t.row > t.bottom {
		return
	}
	t.scrollDown(t.row, t.bottom, n)
	t.col, t.wrapNext = 0, false
}

func (t *Terminal) deleteLines(n int) {
	if t.row < t.top || t.row > t.bottom {
		return
	}
	// Lines deleted inside the screen never go to the scrollback.
	ls := t.cur.lines
	n = min(n, t.bottom-t.row+1)
	copy(ls[t.row:], ls[t.row+n:t.bottom+1])
	for i := t.bottom - n + 1; i <= t.bottom; i++ {
		ls[i] = newLine(t.cols, t.pen)
	}
	t.col, t.wrapNext = 0, false
}

// repeat is REP: print the preceding character n more times.
func (t *Terminal) repeat(n int) {
	col := t.col - 1
	if t.wrapNext {
		col = t.col
	}
	l := t.cur.lines[t.row].Cells
	if col < 0 || col >= len(l) || l[col].R == 0 {
		return
	}
	if l[col].Width == 0 && col > 0 {
		col--
	}
	r := l[col].R
	if r >= graphemeBase {
		r, _ = utf8.DecodeRuneInString(t.Grapheme(l[col]))
	}
	// More than a screenful only scrolls the same character again.
	n = min(n, t.cols*t.rows)
	for ; n > 0; n-- {
		t.printRune(r)
	}
}

// ---- OSC -------------------------------------------------------------------

func (t *Terminal) oscDispatch() {
	s := string(t.p.osc)
	num, rest, _ := strings.Cut(s, ";")
	switch num {
	case "0", "2":
		t.title = sanitize(rest)
	case "7":
		t.cwd = fileURLPath(rest)
	}
}

// sanitize drops control characters from a title.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
}

// fileURLPath extracts the path of a file:// URL (OSC 7); the host part
// is ignored, percent escapes are decoded.
func fileURLPath(u string) string {
	if !strings.HasPrefix(u, "file://") {
		return ""
	}
	u = u[len("file://"):]
	i := strings.IndexByte(u, '/')
	if i < 0 {
		return ""
	}
	p := u[i:]
	var b strings.Builder
	for j := 0; j < len(p); j++ {
		if p[j] == '%' && j+2 < len(p) {
			if v, err := strconv.ParseUint(p[j+1:j+3], 16, 8); err == nil {
				b.WriteByte(byte(v))
				j += 2
				continue
			}
		}
		b.WriteByte(p[j])
	}
	return sanitize(b.String())
}

// ---- resize ----------------------------------------------------------------

// Resize changes the screen size. Lines are not reflowed: shrinking the
// width cuts them, growing pads them. When the screen loses rows below
// the cursor they are dropped; rows above it move to the scrollback.
func (t *Terminal) Resize(cols, rows int) {
	cols, rows = max(1, cols), max(1, rows)
	if cols == t.cols && rows == t.rows {
		return
	}
	t.seq++
	for _, s := range []*screen{&t.main, &t.alt} {
		for i := range s.lines {
			s.lines[i] = fitLine(s.lines[i], cols)
		}
	}
	t.cols = cols
	// Rows: shrink from the bottom while the rows there are below the
	// cursor, then push the top into the scrollback.
	if rows < t.rows {
		extra := t.rows - rows
		for _, s := range []*screen{&t.main, &t.alt} {
			below := max(0, len(s.lines)-1-t.row)
			cut := min(extra, below)
			s.lines = s.lines[:len(s.lines)-cut]
			if over := len(s.lines) - rows; over > 0 {
				if s == &t.main {
					for _, l := range s.lines[:over] {
						t.pushScrollback(l)
					}
				}
				s.lines = s.lines[over:]
			}
		}
		t.row = max(0, t.row-max(0, extra-max(0, t.rows-1-t.row)))
	} else if rows > t.rows {
		// Pull lines back from the scrollback (main screen) so the
		// content stays at the bottom, like xterm.
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
		}
		for len(t.main.lines) < rows {
			t.main.lines = append(t.main.lines, newLine(cols, Style{}))
		}
		for len(t.alt.lines) < rows {
			t.alt.lines = append(t.alt.lines, newLine(cols, Style{}))
		}
	}
	t.rows = rows
	t.row = max(0, min(rows-1, t.row))
	t.col = max(0, min(cols-1, t.col))
	t.wrapNext = false
	t.top, t.bottom = 0, rows-1
	t.resetTabs()
	for _, s := range []*screen{&t.main, &t.alt} {
		s.saved.row = min(s.saved.row, rows-1)
		s.saved.col = min(s.saved.col, cols-1)
	}
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
		return Line{Cells: c, Wrapped: false}
	}
	c := make([]Cell, cols)
	copy(c, l.Cells)
	for i := len(l.Cells); i < cols; i++ {
		c[i] = Cell{Width: 1}
	}
	return Line{Cells: c, Wrapped: l.Wrapped}
}
