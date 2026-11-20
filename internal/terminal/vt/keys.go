package vt

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Key is a key press to encode for the program in the terminal.
type Key struct {
	// Name is a named key (Up, Down, Left, Right, Home, End, PageUp,
	// PageDown, Insert, Delete, Backspace, Enter, Tab, Escape, Space,
	// F1…F12) or a single character ("a", "A", "1", "[").
	Name  string `json:"name"`
	Ctrl  bool   `json:"ctrl,omitempty"`
	Alt   bool   `json:"alt,omitempty"`
	Shift bool   `json:"shift,omitempty"`
}

// modParam is the xterm modifier parameter (1 + shift + 2·alt + 4·ctrl).
func (k Key) modParam() int {
	m := 1
	if k.Shift {
		m++
	}
	if k.Alt {
		m += 2
	}
	if k.Ctrl {
		m += 4
	}
	return m
}

// cursorKeys map to the final byte of their sequence.
var cursorKeys = map[string]byte{"Up": 'A', "Down": 'B', "Right": 'C', "Left": 'D', "Home": 'H', "End": 'F'}

// tildeKeys are sent as CSI n ~.
var tildeKeys = map[string]int{"Insert": 2, "Delete": 3, "PageUp": 5, "PageDown": 6,
	"F5": 15, "F6": 17, "F7": 18, "F8": 19, "F9": 20, "F10": 21, "F11": 23, "F12": 24}

// ssKeys are F1–F4 (SS3 P–S).
var ssKeys = map[string]byte{"F1": 'P', "F2": 'Q', "F3": 'R', "F4": 'S'}

// EncodeKey returns the bytes xterm sends for k in mode m; nil for keys
// it cannot encode. Alt prefixes ESC (meta), Ctrl with a letter or one
// of @[\]^_ sends the control character; Alt+Left/Right send ESC b/f
// (word movement).
func EncodeKey(k Key, m Modes) []byte {
	mod := k.modParam()
	// Alt+Left/Right move by word like in terminal applications
	// (readline and zsh bind ESC b / ESC f).
	if k.Alt && !k.Ctrl && !k.Shift {
		switch k.Name {
		case "Left":
			return []byte("\x1bb")
		case "Right":
			return []byte("\x1bf")
		}
	}
	if b, ok := cursorKeys[k.Name]; ok {
		switch {
		case mod > 1:
			return []byte("\x1b[1;" + strconv.Itoa(mod) + string(b))
		case m.AppCursor:
			return []byte{0x1b, 'O', b}
		}
		return []byte{0x1b, '[', b}
	}
	if n, ok := tildeKeys[k.Name]; ok {
		s := "\x1b[" + strconv.Itoa(n)
		if mod > 1 {
			s += ";" + strconv.Itoa(mod)
		}
		return []byte(s + "~")
	}
	if b, ok := ssKeys[k.Name]; ok {
		if mod > 1 {
			return []byte("\x1b[1;" + strconv.Itoa(mod) + string(b))
		}
		return []byte{0x1b, 'O', b}
	}
	if b := modifyOther(k, m); b != nil {
		return b
	}
	var out []byte
	switch k.Name {
	case "Enter":
		out = []byte{'\r'}
		if k.Shift && !k.Ctrl && !k.Alt {
			// Shift+Enter inserts a line break where programs bind
			// Meta+Enter (zsh, fish, CLI agents); plain CR would run
			// the command.
			return []byte("\x1b\r")
		}
	case "Tab":
		if k.Shift {
			return []byte("\x1b[Z")
		}
		out = []byte{'\t'}
	case "Backspace":
		out = []byte{0x7f}
		if k.Ctrl {
			out = []byte{0x08}
		}
	case "Escape":
		out = []byte{0x1b}
	case "Space":
		out = []byte{' '}
		if k.Ctrl {
			out = []byte{0}
		}
	default:
		r, size := utf8.DecodeRuneInString(k.Name)
		if size == 0 || size != len(k.Name) || r == utf8.RuneError {
			return nil
		}
		if k.Ctrl {
			c, ok := ctrlChar(r)
			if !ok {
				return nil
			}
			out = []byte{c}
		} else {
			// Letters follow Shift (key names may come upper case).
			s := strings.ToLower(k.Name)
			if k.Shift {
				s = strings.ToUpper(s)
			}
			out = []byte(s)
		}
	}
	if k.Alt {
		out = append([]byte{0x1b}, out...)
	}
	return out
}

// modifyOther encodes keys with modifiers the legacy encoding cannot
// tell apart (Shift/Ctrl+Enter, Ctrl+Tab, Ctrl+digits, Ctrl+Shift+letter)
// as CSI 27 ; mod ; code ~ when the program enabled modifyOtherKeys
// (vim, neovim); nil otherwise.
func modifyOther(k Key, m Modes) []byte {
	if m.ModifyOtherKeys == 0 || !k.Ctrl && !k.Shift && !k.Alt {
		return nil
	}
	code := 0
	switch k.Name {
	case "Enter":
		code = '\r'
	case "Tab":
		if !k.Ctrl {
			return nil // Shift+Tab stays CSI Z
		}
		code = '\t'
	case "Backspace":
		if !k.Ctrl && !k.Shift {
			return nil
		}
		code = 0x7f
	case "Escape":
		code = 0x1b
	case "Space":
		if !k.Ctrl || !k.Shift && m.ModifyOtherKeys < 2 {
			return nil
		}
		code = ' '
	default:
		r, size := utf8.DecodeRuneInString(k.Name)
		if size == 0 || size != len(k.Name) || r == utf8.RuneError || !k.Ctrl {
			return nil
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		_, ok := ctrlChar(r)
		// Level 1 only for keys without a control character; level 2
		// for all with Ctrl (Shift distinguishes Ctrl+Shift+letter).
		if ok && m.ModifyOtherKeys < 2 && !k.Shift {
			return nil
		}
		if k.Shift && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		code = int(r)
	}
	return []byte("\x1b[27;" + strconv.Itoa(k.modParam()) + ";" + strconv.Itoa(code) + "~")
}

// ctrlChar maps Ctrl+r to its control character.
func ctrlChar(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r-'a') + 1, true
	case r >= 'A' && r <= 'Z':
		return byte(r-'A') + 1, true
	case r == '@' || r == '2' || r == ' ':
		return 0, true
	case r == '[' || r == '3':
		return 0x1b, true
	case r == '\\' || r == '4':
		return 0x1c, true
	case r == ']' || r == '5':
		return 0x1d, true
	case r == '^' || r == '6':
		return 0x1e, true
	case r == '_' || r == '7' || r == '-' || r == '/':
		return 0x1f, true
	case r == '8' || r == '?':
		return 0x7f, true
	}
	return 0, false
}

// pasteEnd ends a bracketed paste.
const pasteEnd = "\x1b[201~"

// EncodePaste wraps pasted text for the program: with bracketed paste
// mode in ESC [200~ … ESC [201~ (embedded end markers removed until
// none is left, so that nested ones cannot rebuild a marker), line
// breaks become CR like typed Enter.
func EncodePaste(text string, m Modes) []byte {
	text = strings.ReplaceAll(text, "\r\n", "\r")
	text = strings.ReplaceAll(text, "\n", "\r")
	if !m.BracketedPaste {
		return []byte(text)
	}
	for strings.Contains(text, pasteEnd) {
		text = strings.ReplaceAll(text, pasteEnd, "")
	}
	return []byte("\x1b[200~" + text + "\x1b[201~")
}

// MouseAction is what happened in a mouse event.
type MouseAction string

// Mouse actions.
const (
	MousePress   MouseAction = "press"
	MouseRelease MouseAction = "release"
	MouseMove    MouseAction = "move"
)

// MouseButton names a mouse button or wheel direction.
type MouseButton string

// Mouse buttons; MouseNone is motion without a button.
const (
	MouseNone       MouseButton = ""
	MouseLeft       MouseButton = "left"
	MouseMiddle     MouseButton = "middle"
	MouseRight      MouseButton = "right"
	MouseWheelUp    MouseButton = "wheelUp"
	MouseWheelDown  MouseButton = "wheelDown"
	MouseWheelLeft  MouseButton = "wheelLeft"
	MouseWheelRight MouseButton = "wheelRight"
)

// Mouse is a mouse event at a cell (0-based row and column of the
// screen). Wheel events are presses of the wheel buttons.
type Mouse struct {
	Action MouseAction `json:"action"`
	Button MouseButton `json:"button,omitempty"`
	Row    int         `json:"row"`
	Col    int         `json:"col"`
	Shift  bool        `json:"shift,omitempty"`
	Alt    bool        `json:"alt,omitempty"`
	Ctrl   bool        `json:"ctrl,omitempty"`
}

// buttonCode returns the xterm button number; ok is false for unknown
// buttons.
func (e Mouse) buttonCode() (int, bool) {
	switch e.Button {
	case MouseLeft:
		return 0, true
	case MouseMiddle:
		return 1, true
	case MouseRight:
		return 2, true
	case MouseNone:
		return 3, true
	case MouseWheelUp:
		return 64, true
	case MouseWheelDown:
		return 65, true
	case MouseWheelLeft:
		return 66, true
	case MouseWheelRight:
		return 67, true
	}
	return 0, false
}

// EncodeMouse returns the report of e for the program's mouse mode and
// encoding (SGR 1006, urxvt 1015 or the X10 bytes), nil when the mode
// does not report it.
func EncodeMouse(e Mouse, m Modes) []byte {
	code, ok := e.buttonCode()
	if !ok || m.Mouse == 0 || e.Row < 0 || e.Col < 0 {
		return nil
	}
	wheel := code >= 64
	switch e.Action {
	case MousePress:
		if e.Button == MouseNone {
			return nil
		}
	case MouseRelease:
		if m.Mouse == MouseModeX10 || wheel || e.Button == MouseNone {
			return nil
		}
	case MouseMove:
		switch {
		case m.Mouse == MouseModeAny:
		case m.Mouse == MouseModeButton && e.Button != MouseNone:
		default:
			return nil
		}
		if wheel {
			return nil
		}
		code += 32
	default:
		return nil
	}
	if m.Mouse != MouseModeX10 {
		if e.Shift {
			code += 4
		}
		if e.Alt {
			code += 8
		}
		if e.Ctrl {
			code += 16
		}
	}
	x, y := e.Col+1, e.Row+1
	switch {
	case m.MouseSGR:
		final := "M"
		if e.Action == MouseRelease {
			final = "m"
		}
		return []byte("\x1b[<" + strconv.Itoa(code) + ";" + strconv.Itoa(x) + ";" + strconv.Itoa(y) + final)
	case e.Action == MouseRelease:
		// The legacy encodings do not tell which button was released.
		code = code&^3 | 3
	}
	if m.MouseURXVT {
		return []byte("\x1b[" + strconv.Itoa(code+32) + ";" + strconv.Itoa(x) + ";" + strconv.Itoa(y) + "M")
	}
	if x > 223 || y > 223 {
		return nil // not representable in one byte
	}
	return []byte{0x1b, '[', 'M', byte(32 + code), byte(32 + x), byte(32 + y)}
}

// EncodeFocus returns the focus report (CSI I / CSI O) when the program
// asked for focus events (1004), else nil.
func EncodeFocus(focused bool, m Modes) []byte {
	if !m.FocusEvents {
		return nil
	}
	if focused {
		return []byte("\x1b[I")
	}
	return []byte("\x1b[O")
}
