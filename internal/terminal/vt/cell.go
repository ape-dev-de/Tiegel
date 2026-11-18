// Package vt is a terminal emulator: it parses the output of programs
// running in a pseudo terminal (VT100/xterm control sequences) into a
// screen of styled cells with scrollback, cursor, alternate screen and
// the modes the keyboard encoding needs (application cursor keys,
// bracketed paste).
//
// The supported set is the one shells, editors and TUI programs use in
// practice (xterm-256color): cursor movement and addressing, erase,
// insert/delete of characters and lines, scroll regions, SGR with 16, 256
// and true colors, DEC special graphics, tab stops, alternate screen
// (47/1047/1049), autowrap with pending wrap, origin mode, device status
// and attribute reports, window title and working directory (OSC 0/2/7).
// Mouse reporting modes are recorded but not generated. DCS, SOS, PM and
// APC strings are consumed and ignored.
//
// A Terminal is not safe for concurrent use; the owner serialises Write,
// Resize and the readers.
package vt

// Color is a cell color: Default, one of the 256 palette entries
// (Indexed) or a true color (RGB).
type Color uint32

const (
	colorIndexed Color = 1 << 24
	colorRGB     Color = 2 << 24
	colorKind    Color = 0xff << 24
)

// Default is the terminal's default foreground or background color.
const Default Color = 0

// Indexed returns palette color i (0–15 ANSI, 16–231 cube, 232–255 gray).
func Indexed(i uint8) Color { return colorIndexed | Color(i) }

// RGB returns a true color.
func RGB(r, g, b uint8) Color { return colorRGB | Color(r)<<16 | Color(g)<<8 | Color(b) }

// IsDefault reports whether c is the default color.
func (c Color) IsDefault() bool { return c == Default }

// Index returns the palette index of an indexed color.
func (c Color) Index() (uint8, bool) {
	if c&colorKind != colorIndexed {
		return 0, false
	}
	return uint8(c), true
}

// RGB returns the components of a true color.
func (c Color) RGB() (r, g, b uint8, ok bool) {
	if c&colorKind != colorRGB {
		return 0, 0, 0, false
	}
	return uint8(c >> 16), uint8(c >> 8), uint8(c), true
}

// Attr is a set of text attributes.
type Attr uint16

// Attributes.
const (
	Bold Attr = 1 << iota
	Faint
	Italic
	Underline
	Blink
	Inverse
	Hidden
	Strike
)

// Style is the look of a cell.
type Style struct {
	FG, BG Color
	Attr   Attr
}

// Cell is one character cell. Width is 1 or 2 for a printed character
// (2: a wide character that also covers the next cell), 0 for the right
// half of a wide character, and 1 for blank cells.
type Cell struct {
	// R is the character; 0 is a blank. Values above the Unicode range
	// refer to a grapheme cluster (base plus combining marks) interned
	// by the Terminal.
	R     rune
	Style Style
	Width uint8
}

// blank returns an empty cell with the background of st (erase uses
// the current background, like xterm's bce).
func blank(st Style) Cell {
	return Cell{Style: Style{BG: st.BG}, Width: 1}
}

// Line is one row of the screen or the scrollback.
type Line struct {
	Cells []Cell
	// Wrapped tells that the text continues on the next line (autowrap),
	// so copying joins both without a newline.
	Wrapped bool
}

func newLine(cols int, st Style) Line {
	l := Line{Cells: make([]Cell, cols)}
	for i := range l.Cells {
		l.Cells[i] = blank(st)
	}
	return l
}

// trimmed returns a copy of l without trailing blank default cells (for
// the scrollback).
func (l Line) trimmed() Line {
	n := len(l.Cells)
	for n > 0 {
		c := l.Cells[n-1]
		if c.R != 0 || c.Width == 0 || c.Style != (Style{}) {
			break
		}
		n--
	}
	return Line{Cells: append([]Cell(nil), l.Cells[:n]...), Wrapped: l.Wrapped}
}

// Cursor is the cursor position (0-based) and visibility.
type Cursor struct {
	Row     int  `json:"row"`
	Col     int  `json:"col"`
	Visible bool `json:"visible"`
	// Shape is the DECSCUSR shape (0 default, 1/2 block, 3/4 underline,
	// 5/6 bar).
	Shape int `json:"shape,omitempty"`
}

// Modes are the terminal modes a client needs (keyboard encoding,
// paste, mouse).
type Modes struct {
	AppCursor      bool `json:"appCursor,omitempty"`      // DECCKM: cursor keys send ESC O x
	AppKeypad      bool `json:"appKeypad,omitempty"`      // DECKPAM
	BracketedPaste bool `json:"bracketedPaste,omitempty"` // 2004
	AltScreen      bool `json:"altScreen,omitempty"`      // 47/1047/1049 active
	ReverseVideo   bool `json:"reverseVideo,omitempty"`   // DECSCNM
	// Mouse is the requested mouse reporting (0 off, 1000, 1002, 1003)
	// and MouseSGR the SGR encoding (1006); not generated yet.
	Mouse    int  `json:"mouse,omitempty"`
	MouseSGR bool `json:"mouseSgr,omitempty"`
	// FocusEvents (1004) asks for focus in/out reports.
	FocusEvents bool `json:"focusEvents,omitempty"`
}
