package keymap

import (
	"regexp"
	"strings"
)

// macChordRE finds chords with at least one modifier in a text ("Find
// Usages (Alt+F7)", "Cmd+[ or Cmd+Alt+Left").
var macChordRE = regexp.MustCompile(`(?i)\b(?:(?:cmd|command|ctrl|control|alt|opt|option|shift|meta)\+)+[^\s,/()]+`)

// macKeys are the macOS symbols of named keys.
var macKeys = map[string]string{
	KeyLeft: "←", KeyRight: "→", KeyUp: "↑", KeyDown: "↓",
	KeyHome: "↖", KeyEnd: "↘", KeyPageUp: "⇞", KeyPageDown: "⇟",
	KeyBackspace: "⌫", KeyDelete: "⌦", KeyEnter: "↩", KeyTab: "⇥", KeyEscape: "⎋", KeySpace: "Space",
}

// MacLabel rewrites the chords in text the way macOS shows shortcuts:
// modifier symbols in the order ⌃⌥⇧⌘ without plus signs and symbols for
// named keys ("Cmd+Alt+B" → "⌥⌘B", "Alt+Enter" → "⌥↩"). Text that is no
// chord is kept, so the function can be applied twice.
func MacLabel(text string) string {
	return macChordRE.ReplaceAllStringFunc(text, func(s string) string {
		c, err := ParseChord(s)
		if err != nil || IsGesture(c) {
			return s
		}
		var b strings.Builder
		for _, m := range []struct {
			mod Mods
			sym string
		}{{Ctrl, "⌃"}, {Alt, "⌥"}, {Shift, "⇧"}, {Cmd, "⌘"}} {
			if c.Mods&m.mod == 0 {
				continue
			}
			b.WriteString(m.sym)
			if c.Double&m.mod != 0 {
				b.WriteString(m.sym)
			}
		}
		if k, ok := macKeys[c.Key]; ok {
			b.WriteString(k)
		} else {
			b.WriteString(strings.ToUpper(c.Key))
		}
		return b.String()
	})
}
