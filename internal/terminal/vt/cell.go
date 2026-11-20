// Package vt is a terminal emulator: it parses the output of programs
// running in a pseudo terminal (VT100/xterm control sequences) into a
// screen of styled cells with scrollback, cursor, alternate screen and
// the modes the input encoding needs (cursor keys, bracketed paste,
// mouse and focus reporting, modifyOtherKeys).
//
// The supported set is the one shells, editors and full-screen TUI
// programs use in practice (xterm-256color): cursor movement and
// addressing, erase, insert/delete of characters and lines, scroll
// regions, SGR with 16, 256 and true colors (colon and semicolon forms),
// underline styles and colors, overline, DEC special graphics, tab stops,
// alternate screen (47/1047/1049), autowrap with pending wrap, origin
// mode, synchronized output (2026), device status, attribute, mode and
// version reports, window title with title stack (OSC 0/2, CSI 22/23 t),
// working directory (OSC 7), hyperlinks (OSC 8), clipboard writes
// (OSC 52, only through a callback the owner enables), shell integration
// marks (OSC 133) and color queries (OSC 4/10/11/12). The main screen
// reflows when the width changes. DCS, SOS, PM and APC strings are
// consumed and ignored.
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

// Attr is a set of text attributes. The underline style lives in three
// bits (see UnderlineStyle); Underline is set for every style.
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
	Overline
)

const (
	ulShift = 9
	ulMask  = Attr(7) << ulShift
)

// UnderlineStyle is the style of an underline (SGR 4:n, 21).
type UnderlineStyle uint8

// Underline styles.
const (
	ULNone UnderlineStyle = iota
	ULSingle
	ULDouble
	ULCurly
	ULDotted
	ULDashed
)

// UnderlineStyle returns the underline style of a.
func (a Attr) UnderlineStyle() UnderlineStyle {
	if a&Underline == 0 {
		return ULNone
	}
	if s := UnderlineStyle(a & ulMask >> ulShift); s > ULSingle {
		return s
	}
	return ULSingle
}

// withUnderline returns a with underline style s (ULNone removes it).
func (a Attr) withUnderline(s UnderlineStyle) Attr {
	a &^= Underline | ulMask
	if s == ULNone {
		return a
	}
	if s > ULDashed || s == ULSingle {
		return a | Underline
	}
	return a | Underline | Attr(s)<<ulShift
}

// Style is the look of a cell. UL is the underline color (Default: the
// foreground); Link is a hyperlink (OSC 8, see Terminal.LinkURL), 0 for
// none.
type Style struct {
	FG, BG, UL Color
	Attr       Attr
	Link       uint16
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

// Mark is a shell integration mark of a line (OSC 133).
type Mark uint8

// Marks; a line can have several.
const (
	// MarkPrompt: a prompt starts on the line (OSC 133;A).
	MarkPrompt Mark = 1 << iota
	// MarkInput: the command input starts (OSC 133;B).
	MarkInput
	// MarkOutput: the command output starts (OSC 133;C).
	MarkOutput
	// MarkDone: a command finished (OSC 133;D).
	MarkDone
)

// Line is one row of the screen or the scrollback.
type Line struct {
	Cells []Cell
	// Wrapped tells that the text continues on the next line (autowrap),
	// so copying joins both without a newline.
	Wrapped bool
	// Mark holds the shell integration marks of the line.
	Mark Mark
}

func newLine(cols int, st Style) Line {
	l := Line{Cells: make([]Cell, cols)}
	fillBlank(l.Cells, st)
	return l
}

// fillBlank erases cells with the background of st.
func fillBlank(cs []Cell, st Style) {
	if len(cs) == 0 {
		return
	}
	cs[0] = blank(st)
	// Doubling copies are memmoves (much faster than a cell loop).
	for n := 1; n < len(cs); n *= 2 {
		copy(cs[n:], cs[:n])
	}
}

// contentLen returns the number of cells without trailing blank default
// cells.
func (l Line) contentLen() int {
	n := len(l.Cells)
	for n > 0 {
		c := l.Cells[n-1]
		if c.R != 0 || c.Width != 1 || c.Style != (Style{}) {
			break
		}
		n--
	}
	return n
}

// Cursor is the cursor position (0-based) and visibility.
type Cursor struct {
	Row     int  `json:"row"`
	Col     int  `json:"col"`
	Visible bool `json:"visible"`
	// Shape is the DECSCUSR shape (0 default, 1/2 block, 3/4 underline,
	// 5/6 bar).
	Shape int `json:"shape,omitempty"`
	// Blink tells that the cursor blinks (odd DECSCUSR shapes, or mode
	// 12 with the default shape).
	Blink bool `json:"blink,omitempty"`
}

// Mouse reporting modes (Modes.Mouse).
const (
	MouseModeX10    = 9    // presses only
	MouseModeNormal = 1000 // presses and releases
	MouseModeButton = 1002 // and motion while a button is held
	MouseModeAny    = 1003 // and all motion
)

// Modes are the terminal modes a client needs (keyboard encoding,
// paste, mouse, rendering).
type Modes struct {
	AppCursor      bool `json:"appCursor,omitempty"`      // DECCKM: cursor keys send ESC O x
	AppKeypad      bool `json:"appKeypad,omitempty"`      // DECKPAM
	BracketedPaste bool `json:"bracketedPaste,omitempty"` // 2004
	AltScreen      bool `json:"altScreen,omitempty"`      // 47/1047/1049 active
	ReverseVideo   bool `json:"reverseVideo,omitempty"`   // DECSCNM
	// Mouse is the requested mouse reporting (0 off, MouseModeX10,
	// MouseModeNormal, MouseModeButton, MouseModeAny); MouseSGR the SGR encoding
	// (1006), MouseURXVT the urxvt one (1015).
	Mouse      int  `json:"mouse,omitempty"`
	MouseSGR   bool `json:"mouseSgr,omitempty"`
	MouseURXVT bool `json:"mouseUrxvt,omitempty"`
	// FocusEvents (1004) asks for focus in/out reports.
	FocusEvents bool `json:"focusEvents,omitempty"`
	// AltScroll (1007) turns the wheel into cursor keys on the
	// alternate screen; on by default.
	AltScroll bool `json:"altScroll,omitempty"`
	// SyncOutput (2026): the program is drawing a frame; clients hold
	// the last complete frame until it ends.
	SyncOutput bool `json:"syncOutput,omitempty"`
	// ModifyOtherKeys is the xterm modifyOtherKeys level (CSI > 4 ; n m).
	ModifyOtherKeys int `json:"modifyOtherKeys,omitempty"`
}
