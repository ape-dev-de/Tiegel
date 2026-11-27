package keymap

import "testing"

func TestMacLabel(t *testing.T) {
	for in, want := range map[string]string{
		"Alt+F7":                           "⌥F7",
		"Cmd+Alt+B":                        "⌥⌘B",
		"Cmd+Shift+F":                      "⇧⌘F",
		"Ctrl+Alt+Shift+Cmd+K":             "⌃⌥⇧⌘K",
		"Alt+Enter":                        "⌥↩",
		"Alt+Alt+Up":                       "⌥⌥↑",
		"Find Usages (Alt+F7) on a symbol": "Find Usages (⌥F7) on a symbol",
		"Cmd+[, Cmd+Alt+Left":              "⌘[, ⌥⌘←",
		"Shift+F6":                         "⇧F6",
		"F7":                               "F7",
		"⌥F7":                              "⌥F7",
		"Cmd+a":                            "⌘A",
		"no chord here":                    "no chord here",
	} {
		if got := MacLabel(in); got != want {
			t.Errorf("MacLabel(%q) = %q, want %q", in, got, want)
		}
		if again := MacLabel(MacLabel(in)); again != want {
			t.Errorf("not idempotent: %q", again)
		}
	}
}
