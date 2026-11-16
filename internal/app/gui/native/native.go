// Package native is a tiny bridge to platform UI that Gio does not
// provide: the native folder chooser and the native menu bar. Only macOS
// (cgo, Cocoa) has an implementation; on every other platform, and when
// cgo is disabled, the functions are stubs and the *Supported functions
// report false so callers can fall back to Gio UI.
//
// Threading: Gio owns the macOS main thread (app.Main runs the Cocoa run
// loop). All Cocoa work of this package is scheduled onto the main thread
// and only runs once that run loop runs. ChooseFolder blocks its caller and
// must therefore be called from a background goroutine; SetMenus never
// blocks and may be called from any goroutine.
package native

import (
	"encoding/binary"
	"errors"
	"strings"
)

// ErrUnsupported is returned by functions that have no native
// implementation on the current platform.
var ErrUnsupported = errors.New("native: not supported on this platform")

// MenuItem is an entry of a native menu.
type MenuItem struct {
	// ID is passed to the action callback.
	ID    string
	Title string
	// Shortcut is shown next to the item but not performed by the menu
	// (the window handles keys itself). Keymap chord syntax such as
	// "Cmd+Shift+O", "Cmd+,", "F9" or "Shift+Escape"; "" for none.
	Shortcut string
	// Separator makes the entry a separator line; other fields are ignored.
	Separator bool
	Disabled  bool
	// Checked shows a check mark.
	Checked bool
	// Children turns the entry into a submenu.
	Children []MenuItem
}

// Menu is a top-level menu of the menu bar ("File", "Edit", …).
type Menu struct {
	Title string
	Items []MenuItem
}

// Cocoa modifier flags (NSEventModifierFlag*).
const (
	modShift uint32 = 1 << 17
	modCtrl  uint32 = 1 << 18
	modAlt   uint32 = 1 << 19
	modCmd   uint32 = 1 << 20
)

var chordMods = map[string]uint32{
	"cmd": modCmd, "command": modCmd, "meta": modCmd, "super": modCmd,
	"shift": modShift,
	"alt":   modAlt, "opt": modAlt, "option": modAlt,
	"ctrl": modCtrl, "control": modCtrl,
}

// Cocoa key equivalents for named keys (NS*FunctionKey and control chars).
var chordKeys = map[string]string{
	"escape": "\x1b", "esc": "\x1b",
	"tab": "\t", "space": " ",
	"return": "\r", "enter": "\r",
	"backspace": "\b", "delete": "", "del": "",
	"up": "", "down": "", "left": "", "right": "",
	"home": "", "end": "", "pageup": "", "pagedown": "",
	"pgup": "", "pgdn": "",
	"plus": "=", "minus": "-",
}

// parseChord translates a keymap chord like "Cmd+Shift+O" into a Cocoa
// key equivalent and modifier mask. ok is false for empty or malformed
// chords and for multi-chord sequences.
func parseChord(s string) (key string, mods uint32, ok bool) {
	rest := strings.TrimSpace(s)
	if rest == "" || strings.ContainsAny(rest, " \t") {
		return "", 0, false
	}
	for {
		i := strings.IndexByte(rest, '+')
		// A trailing "+" (as in "Cmd++") is the key itself.
		if i <= 0 || i == len(rest)-1 {
			break
		}
		m, known := chordMods[strings.ToLower(rest[:i])]
		if !known {
			return "", 0, false
		}
		mods |= m
		rest = rest[i+1:]
	}
	lower := strings.ToLower(rest)
	if k, named := chordKeys[lower]; named {
		return k, mods, true
	}
	if len(lower) >= 2 && lower[0] == 'f' {
		n := 0
		for _, c := range lower[1:] {
			if c < '0' || c > '9' {
				return "", 0, false
			}
			n = n*10 + int(c-'0')
		}
		if n < 1 || n > 20 {
			return "", 0, false
		}
		return string(rune(0xf704 + n - 1)), mods, true
	}
	if r := []rune(rest); len(r) == 1 && r[0] > ' ' && r[0] < 0x7f {
		return lower, mods, true
	}
	return "", 0, false
}

// Record opcodes and flags of the menu buffer parsed by the Objective-C
// side. Integers are little-endian, strings are a uint32 length followed by
// UTF-8 bytes.
//
//	'M' flags:u8 title            begin a top-level menu (ended by 'E')
//	'I' kind:u8 flags:u8 tag:i64 mods:u32 title key   an item; with
//	    itemSubmenu its children follow, ended by 'E'
//	'S'                           separator
//	'E'                           end of menu or submenu
const (
	menuApp     = 1 // top-level: application menu (functional shortcuts)
	menuWindows = 2 // top-level: register as NSApp.windowsMenu

	kindAction     = 0 // calls the Go action callback
	kindHide       = 1 // hide:
	kindHideOthers = 2 // hideOtherApplications:
	kindShowAll    = 3 // unhideAllApplications:

	itemDisabled   = 1
	itemChecked    = 2
	itemSubmenu    = 4
	itemFunctional = 8 // key equivalent is performed by the menu
)

// menuEncoder serializes menus into the buffer format above. Item tags are
// gen<<32 | index into ids, so actions of stale menus can be recognised.
type menuEncoder struct {
	buf []byte
	ids []string
	gen uint32
}

func (e *menuEncoder) str(s string) {
	e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(len(s)))
	e.buf = append(e.buf, s...)
}

func (e *menuEncoder) begin(title string, flags byte) {
	e.buf = append(e.buf, 'M', flags)
	e.str(title)
}

func (e *menuEncoder) end() { e.buf = append(e.buf, 'E') }

func (e *menuEncoder) sep() { e.buf = append(e.buf, 'S') }

func (e *menuEncoder) item(kind, flags byte, id, title, shortcut string) {
	tag := int64(e.gen)<<32 | int64(len(e.ids))
	e.ids = append(e.ids, id)
	key, mods, _ := parseChord(shortcut)
	e.buf = append(e.buf, 'I', kind, flags)
	e.buf = binary.LittleEndian.AppendUint64(e.buf, uint64(tag))
	e.buf = binary.LittleEndian.AppendUint32(e.buf, mods)
	e.str(title)
	e.str(key)
}

func (e *menuEncoder) items(items []MenuItem) {
	for _, it := range items {
		if it.Separator {
			e.sep()
			continue
		}
		var flags byte
		if it.Disabled {
			flags |= itemDisabled
		}
		if it.Checked {
			flags |= itemChecked
		}
		if len(it.Children) > 0 {
			flags |= itemSubmenu
		}
		e.item(kindAction, flags, it.ID, it.Title, it.Shortcut)
		if len(it.Children) > 0 {
			e.items(it.Children)
			e.end()
		}
	}
}

// encodeMenus builds the buffer for the application menu followed by
// menus, and returns it with the item IDs indexed by tag.
func encodeMenus(menus []Menu, gen uint32) ([]byte, []string) {
	e := &menuEncoder{gen: gen}
	e.begin("", menuApp)
	e.item(kindAction, 0, "app.about", "About Tiegel", "")
	e.sep()
	e.item(kindAction, 0, "app.settings", "Settings…", "Cmd+,")
	e.sep()
	e.item(kindHide, itemFunctional, "", "Hide Tiegel", "Cmd+H")
	e.item(kindHideOthers, itemFunctional, "", "Hide Others", "Cmd+Alt+H")
	e.item(kindShowAll, 0, "", "Show All", "")
	e.sep()
	e.item(kindAction, itemFunctional, "app.quit", "Quit Tiegel", "Cmd+Q")
	e.end()
	for _, m := range menus {
		var flags byte
		if m.Title == "Window" {
			flags |= menuWindows
		}
		e.begin(m.Title, flags)
		e.items(m.Items)
		e.end()
	}
	return e.buf, e.ids
}
