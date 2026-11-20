package vt

import (
	"strconv"
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
	// blinkMode is mode 12 (blinking cursor for the default shape).
	blinkMode bool

	top, bottom int // scroll region (inclusive)
	tabs        []bool

	autowrap, origin, insert, newline bool
	modes                             Modes
	// charsets: G0/G1 use DEC special graphics; activeG1 after SO.
	charsets [2]bool
	activeG1 bool

	title, cwd string
	titles     []string // title stack (CSI 22/23 t)
	bells      int
	// lastExit is the exit code of the last command (OSC 133;D), -1
	// when unknown.
	lastExit int

	// graphemes interns base+combining sequences; Cell.R values from
	// graphemeBase on index it.
	graphemes []string
	gindex    map[string]rune
	// links interns hyperlink targets (OSC 8); Style.Link is index+1.
	links     []string
	linkIndex map[string]uint16

	p   parser
	seq uint64

	// Reply receives answers to device queries (DSR, DA); may be nil.
	Reply func([]byte)
	// OSC receives operating system commands the emulator does not
	// handle itself (number and text); may be nil.
	OSC func(num, text string)
	// Clipboard receives clipboard writes of programs (OSC 52: target
	// such as "c" or "p", decoded text). Nil ignores them; clipboard
	// reads are never answered.
	Clipboard func(target, text string)
	// Colors answer color queries (OSC 4/10/11/12).
	Colors Colors
}

// Colors are the colors the terminal reports to programs that ask (vim
// and others pick a light or dark scheme from the background).
type Colors struct {
	FG, BG, Cursor [3]uint8
	// ANSI are palette entries 0–15; 16–255 follow the xterm scheme.
	ANSI [16][3]uint8
}

// DefaultColors are a dark scheme with the xterm palette.
func DefaultColors() Colors {
	c := Colors{FG: [3]uint8{0xd0, 0xd0, 0xd0}, BG: [3]uint8{0x1e, 0x1f, 0x22}, Cursor: [3]uint8{0xd0, 0xd0, 0xd0}}
	for i := range c.ANSI {
		c.ANSI[i] = PaletteRGB(uint8(i))
	}
	return c
}

// xtermANSI is the xterm default palette 0–15.
var xtermANSI = [16][3]uint8{
	{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0}, {0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
	{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

var cubeLevels = [6]uint8{0, 0x5f, 0x87, 0xaf, 0xd7, 0xff}

// PaletteRGB returns the xterm default RGB of palette entry i.
func PaletteRGB(i uint8) [3]uint8 {
	switch {
	case i < 16:
		return xtermANSI[i]
	case i < 232:
		n := i - 16
		return [3]uint8{cubeLevels[n/36], cubeLevels[n/6%6], cubeLevels[n%6]}
	}
	g := 8 + 10*(i-232)
	return [3]uint8{g, g, g}
}

const graphemeBase = 0x110000

// New returns a terminal of the given size. scrollback is the maximum
// number of lines kept above the screen (0: none, negative: default).
func New(cols, rows, scrollback int) *Terminal {
	if scrollback < 0 {
		scrollback = DefaultScrollback
	}
	t := &Terminal{maxScroll: scrollback, gindex: map[string]rune{}, Colors: DefaultColors()}
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
	t.modes = Modes{AltScroll: true}
	t.cursor = Cursor{Visible: true}
	t.blinkMode = false
	t.charsets, t.activeG1 = [2]bool{}, false
	t.titles = nil
	t.lastExit = -1
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
	switch {
	case c.Shape == 0:
		c.Blink = t.blinkMode
	default:
		c.Blink = c.Shape%2 == 1
	}
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

// LastExit returns the exit code of the last command the shell reported
// through OSC 133;D (-1 when unknown).
func (t *Terminal) LastExit() int { return t.lastExit }

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
	case c.R < utf8.RuneSelf:
		return asciiStrings[c.R]
	}
	return string(c.R)
}

// asciiStrings avoids an allocation per ASCII cell in Grapheme.
var asciiStrings = func() (s [utf8.RuneSelf]string) {
	for i := range s {
		s[i] = string(rune(i))
	}
	return s
}()

// LinkURL returns the target of hyperlink id (Style.Link), "" for none.
func (t *Terminal) LinkURL(id uint16) string {
	if id == 0 || int(id) > len(t.links) {
		return ""
	}
	return t.links[id-1]
}

// ---- printing --------------------------------------------------------------

// decGraphics maps the DEC special graphics set (line drawing).
var decGraphics = map[rune]rune{
	'`': '◆', 'a': '▒', 'b': '␉', 'c': '␌', 'd': '␍', 'e': '␊', 'f': '°', 'g': '±',
	'h': '␤', 'i': '␋', 'j': '┘', 'k': '┐', 'l': '┌', 'm': '└', 'n': '┼', 'o': '⎺',
	'p': '⎻', 'q': '─', 'r': '⎼', 's': '⎽', 't': '├', 'u': '┤', 'v': '┴', 'w': '┬',
	'x': '│', 'y': '≤', 'z': '≥', '{': 'π', '|': '≠', '}': '£', '~': '·',
}

// RuneWidth returns the column width of r: 0 for combining marks and
// format characters, 2 for East Asian wide and fullwidth characters and
// emoji presentation, else 1.
func RuneWidth(r rune) int {
	if r < 0x300 {
		return 1
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	return max(1, min(2, uniseg.StringWidth(string(r))))
}

// printASCII prints a run of printable ASCII (the common case): cells
// are written directly as long as no special state (charsets, insert
// mode, pending wrap) or wide character is involved.
func (t *Terminal) printASCII(b []byte) {
	for len(b) > 0 {
		g := 0
		if t.activeG1 {
			g = 1
		}
		if t.charsets[g] || t.insert || t.wrapNext {
			t.printRune(rune(b[0]))
			b = b[1:]
			continue
		}
		cells := t.cur.lines[t.row].Cells
		n := min(len(b), t.cols-t.col)
		pen := t.pen
		i := 0
		for ; i < n; i++ {
			c := &cells[t.col+i]
			if c.Width != 1 {
				break // half of a wide character
			}
			*c = Cell{R: rune(b[i]), Style: pen, Width: 1}
		}
		t.col += i
		b = b[i:]
		switch {
		case t.col >= t.cols:
			t.col = t.cols - 1
			t.wrapNext = true
		case i < n:
			t.printRune(rune(b[0]))
			b = b[1:]
		}
	}
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
	w := RuneWidth(r)
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
		if t.autowrap && t.cols > 1 {
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

// rotate moves the first n lines of ls to its end (no allocation).
func rotate(ls []Line, n int) {
	if n <= 0 || n >= len(ls) {
		return
	}
	if n <= 4 {
		var tmp [4]Line
		copy(tmp[:n], ls[:n])
		copy(ls, ls[n:])
		copy(ls[len(ls)-n:], tmp[:n])
		return
	}
	if m := len(ls) - n; m <= 4 {
		var tmp [4]Line
		copy(tmp[:m], ls[n:])
		copy(ls[m:], ls[:n])
		copy(ls, tmp[:m])
		return
	}
	rev := func(s []Line) {
		for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
			s[i], s[j] = s[j], s[i]
		}
	}
	rev(ls[:n])
	rev(ls[n:])
	rev(ls)
}

// clearLine blanks line l in place (keeping its cells array).
func (t *Terminal) clearLine(l *Line, st Style) {
	if len(l.Cells) != t.cols {
		*l = newLine(t.cols, st)
		return
	}
	fillBlank(l.Cells, st)
	l.Wrapped, l.Mark = false, 0
}

// scrollUp scrolls rows top..bottom up by n; lines leaving the top of
// the main screen go to the scrollback. The cells of the leaving lines
// are reused for the new blank lines.
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
	rotate(ls[top:bottom+1], n)
	for i := bottom - n + 1; i <= bottom; i++ {
		t.clearLine(&ls[i], t.pen)
	}
}

func (t *Terminal) scrollDown(top, bottom, n int) {
	n = min(n, bottom-top+1)
	if n <= 0 {
		return
	}
	ls := t.cur.lines
	rotate(ls[top:bottom+1], bottom+1-top-n)
	for i := top; i < top+n; i++ {
		t.clearLine(&ls[i], t.pen)
	}
}

// pushScrollback appends a copy of l (without trailing blanks) to the
// scrollback; a full ring reuses the cells of the line it drops.
func (t *Terminal) pushScrollback(l Line) {
	if t.maxScroll <= 0 {
		t.dropped++
		return
	}
	n := l.contentLen()
	if len(t.scrollback) < t.maxScroll {
		t.scrollback = append(t.scrollback, Line{Cells: append([]Cell(nil), l.Cells[:n]...), Wrapped: l.Wrapped, Mark: l.Mark})
		return
	}
	old := &t.scrollback[t.sbStart]
	*old = Line{Cells: append(old.Cells[:0], l.Cells[:n]...), Wrapped: l.Wrapped, Mark: l.Mark}
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
				t.clearLine(&t.alt.lines[i], Style{})
			}
		}
		return
	}
	for i := range t.alt.lines {
		t.clearLine(&t.alt.lines[i], Style{})
	}
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
			t.clearLine(&t.cur.lines[r], t.pen)
		}
	case 1:
		t.eraseLine(1)
		for r := 0; r < t.row; r++ {
			t.clearLine(&t.cur.lines[r], t.pen)
		}
	case 2:
		for r := range t.cur.lines {
			t.clearLine(&t.cur.lines[r], t.pen)
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
	rotate(ls[t.row:t.bottom+1], n)
	for i := t.bottom - n + 1; i <= t.bottom; i++ {
		t.clearLine(&ls[i], t.pen)
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

// markLine sets a shell integration mark on the cursor line.
func (t *Terminal) markLine(m Mark) {
	if t.cur == &t.main {
		t.cur.lines[t.row].Mark |= m
	}
}

// itoa is strconv.Itoa (short name for the reply builders).
func itoa(n int) string { return strconv.Itoa(n) }
