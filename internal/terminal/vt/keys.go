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
	var out []byte
	switch k.Name {
	case "Enter":
		out = []byte{'\r'}
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
	case r == '_' || r == '7' || r == '-':
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
