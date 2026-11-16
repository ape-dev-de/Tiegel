// Package keymap maps key chords to editor actions. It is pure data
// without any UI dependency: actions are named strings, chords are
// modifier sets plus a key name, and a Keymap is a lookup table. The Gio
// editor widget translates its key events into Chords and executes the
// looked-up Action; menus and the command palette can list the same table.
//
// Key names are layout independent and spelled like on a US keyboard:
// "A"…"Z", "0"…"9", punctuation such as "/", "[", "-", "=", and the named
// keys Left, Right, Up, Down, Home, End, PageUp, PageDown, Backspace,
// Delete, Enter, Tab, Escape, Space, F1…F12.
package keymap

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Action names an editor command, e.g. "editor.duplicateLines".
type Action string

// Mods is a set of modifier keys.
type Mods uint8

// Modifier keys. Cmd is the Command key on macOS and the Super/Windows
// key elsewhere; Alt is Option on macOS.
const (
	Shift Mods = 1 << iota
	Ctrl
	Alt
	Cmd
)

// String returns the modifiers in canonical order (Cmd, Ctrl, Alt,
// Shift), e.g. "Cmd+Shift".
func (m Mods) String() string {
	var parts []string
	for _, n := range modNames {
		if m&n.mod != 0 {
			parts = append(parts, n.name)
		}
	}
	return strings.Join(parts, "+")
}

var modNames = []struct {
	name string
	mod  Mods
}{{"Cmd", Cmd}, {"Ctrl", Ctrl}, {"Alt", Alt}, {"Shift", Shift}}

var modAliases = map[string]Mods{
	"ctrl": Ctrl, "control": Ctrl,
	"alt": Alt, "opt": Alt, "option": Alt,
	"shift": Shift,
	"cmd":   Cmd, "command": Cmd, "meta": Cmd, "super": Cmd,
}

// Chord is a key together with the modifiers held down.
type Chord struct {
	Mods Mods
	Key  string
}

// String formats the chord as "Mods+Key", e.g. "Cmd+Shift+Z".
func (c Chord) String() string {
	if c.Mods == 0 {
		return c.Key
	}
	return c.Mods.String() + "+" + c.Key
}

// Named keys.
const (
	KeyLeft      = "Left"
	KeyRight     = "Right"
	KeyUp        = "Up"
	KeyDown      = "Down"
	KeyHome      = "Home"
	KeyEnd       = "End"
	KeyPageUp    = "PageUp"
	KeyPageDown  = "PageDown"
	KeyBackspace = "Backspace"
	KeyDelete    = "Delete"
	KeyEnter     = "Enter"
	KeyTab       = "Tab"
	KeyEscape    = "Escape"
	KeySpace     = "Space"
)

var namedKeys = func() map[string]string {
	m := map[string]string{}
	for _, k := range []string{KeyLeft, KeyRight, KeyUp, KeyDown, KeyHome, KeyEnd, KeyPageUp,
		KeyPageDown, KeyBackspace, KeyDelete, KeyEnter, KeyTab, KeyEscape, KeySpace} {
		m[strings.ToLower(k)] = k
	}
	for i := 1; i <= 12; i++ {
		k := fmt.Sprintf("F%d", i)
		m[strings.ToLower(k)] = k
	}
	m["return"], m["esc"], m["del"], m["pgup"], m["pgdn"] = KeyEnter, KeyEscape, KeyDelete, KeyPageUp, KeyPageDown
	m["plus"], m["minus"] = "=", "-"
	return m
}()

// ErrBadChord is returned by ParseChord for malformed input.
var ErrBadChord = errors.New("bad key chord")

// ParseChord parses "Cmd+Shift+Z", "alt+left", "Ctrl+/" and the like.
// Modifier names are case-insensitive; single characters are upper-cased.
func ParseChord(s string) (Chord, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Chord{}, fmt.Errorf("%w: empty", ErrBadChord)
	}
	var c Chord
	rest := s
	for {
		i := strings.IndexByte(rest, '+')
		// A trailing "+" (as in "Cmd++") is the key itself.
		if i <= 0 || i == len(rest)-1 {
			break
		}
		m, ok := modAliases[strings.ToLower(rest[:i])]
		if !ok {
			break
		}
		c.Mods |= m
		rest = rest[i+1:]
	}
	k, err := normalizeKey(rest)
	if err != nil {
		return Chord{}, fmt.Errorf("%w: %q: %v", ErrBadChord, s, err)
	}
	c.Key = k
	return c, nil
}

// MustParse is ParseChord for static tables; it returns the zero Chord
// for invalid input, which never matches a key event.
func MustParse(s string) Chord {
	c, _ := ParseChord(s)
	return c
}

func normalizeKey(k string) (string, error) {
	if n, ok := namedKeys[strings.ToLower(k)]; ok {
		return n, nil
	}
	if r := []rune(k); len(r) == 1 {
		return strings.ToUpper(k), nil
	}
	return "", fmt.Errorf("unknown key %q", k)
}

// Binding is one chord → action entry.
type Binding struct {
	Chord  Chord
	Action Action
}

// Keymap is a table of bindings. A chord maps to at most one action; an
// action may have several chords. The zero value is an empty keymap.
type Keymap struct {
	// Name describes the keymap, e.g. "macOS" or "Windows/Linux".
	Name    string
	m       map[Chord]Action
	version uint64
}

// New returns a keymap with the given bindings; later bindings override
// earlier ones for the same chord.
func New(name string, bindings ...Binding) *Keymap {
	k := &Keymap{Name: name, m: make(map[Chord]Action, len(bindings))}
	for _, b := range bindings {
		k.m[b.Chord] = b.Action
	}
	return k
}

// Lookup returns the action bound to c.
func (k *Keymap) Lookup(c Chord) (Action, bool) {
	if k == nil {
		return "", false
	}
	a, ok := k.m[c]
	return a, ok
}

// Bind binds c to a, replacing any previous binding of c.
func (k *Keymap) Bind(c Chord, a Action) {
	if k.m == nil {
		k.m = map[Chord]Action{}
	}
	k.m[c] = a
	k.version++
}

// Unbind removes the binding of c.
func (k *Keymap) Unbind(c Chord) {
	delete(k.m, c)
	k.version++
}

// Version returns a counter that changes with every Bind and Unbind, so
// users can cache data derived from the bindings.
func (k *Keymap) Version() uint64 {
	if k == nil {
		return 0
	}
	return k.version
}

// Len returns the number of bindings.
func (k *Keymap) Len() int {
	if k == nil {
		return 0
	}
	return len(k.m)
}

// Bindings returns all bindings sorted by action, then chord.
func (k *Keymap) Bindings() []Binding {
	if k == nil {
		return nil
	}
	out := make([]Binding, 0, len(k.m))
	for c, a := range k.m {
		out = append(out, Binding{c, a})
	}
	slices.SortFunc(out, func(a, b Binding) int {
		if c := strings.Compare(string(a.Action), string(b.Action)); c != 0 {
			return c
		}
		return strings.Compare(a.Chord.String(), b.Chord.String())
	})
	return out
}

// ChordsFor returns the chords bound to a, sorted.
func (k *Keymap) ChordsFor(a Action) []Chord {
	var out []Chord
	for _, b := range k.Bindings() {
		if b.Action == a {
			out = append(out, b.Chord)
		}
	}
	return out
}

// Keys returns the distinct key names used by the bindings, sorted. The
// widget uses it to subscribe to exactly these keys.
func (k *Keymap) Keys() []string {
	if k == nil {
		return nil
	}
	var out []string
	for c := range k.m {
		out = append(out, c.Key)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Clone returns an independent copy of k.
func (k *Keymap) Clone() *Keymap {
	c := &Keymap{Name: k.Name, m: make(map[Chord]Action, len(k.m))}
	for ch, a := range k.m {
		c.m[ch] = a
	}
	return c
}
