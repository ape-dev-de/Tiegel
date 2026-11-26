package keymap

import (
	"errors"
	"testing"
)

func TestParseChord(t *testing.T) {
	cases := map[string]Chord{
		"Cmd+Shift+Z":        {Mods: Cmd | Shift, Key: "Z"},
		"alt+left":           {Mods: Alt, Key: KeyLeft},
		"Ctrl+/":             {Mods: Ctrl, Key: "/"},
		"cmd++":              {Mods: Cmd, Key: "+"},
		"Option+Esc":         {Mods: Alt, Key: KeyEscape},
		"Meta+return":        {Mods: Cmd, Key: KeyEnter},
		"d":                  {Mods: 0, Key: "D"},
		"Shift+F3":           {Mods: Shift, Key: "F3"},
		"Ctrl+Cmd+g":         {Mods: Ctrl | Cmd, Key: "G"},
		" Cmd+Minus ":        {Mods: Cmd, Key: "-"},
		"command+plus":       {Mods: Cmd, Key: "="},
		"Alt+Alt+Up":         {Mods: Alt, Key: KeyUp, Double: Alt},
		"ctrl+Ctrl+Down":     {Mods: Ctrl, Key: KeyDown, Double: Ctrl},
		"Shift+Alt+alt+Left": {Mods: Alt | Shift, Key: KeyLeft, Double: Alt},
	}
	for in, want := range cases {
		got, err := ParseChord(in)
		if err != nil || got != want {
			t.Errorf("ParseChord(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "Cmd+", "Hyper+X", "Cmd+Foo", "Shift+Shift+Up", "Alt+Alt+Ctrl+Ctrl+Up", "Alt+Alt+Alt+Up", "Alt+Alt", "Alt+Alt+MouseBack"} {
		if _, err := ParseChord(bad); !errors.Is(err, ErrBadChord) {
			t.Errorf("ParseChord(%q) err = %v", bad, err)
		}
	}
	if MustParse("nope+nope") != (Chord{}) {
		t.Error("MustParse of invalid chord")
	}
}

func TestChordString(t *testing.T) {
	for _, s := range []string{"Cmd+Ctrl+Alt+Shift+K", "Cmd+/", "Left", "Alt+Shift+Up", "Alt+Alt+Up", "Cmd+Ctrl+Ctrl+Shift+Down"} {
		if got := MustParse(s).String(); got != s {
			t.Errorf("round trip %q → %q", s, got)
		}
	}
}

func TestBuiltinTablesParse(t *testing.T) {
	for _, list := range [][][2]string{common, macPairs, pcPairs} {
		for _, p := range list {
			if _, err := ParseChord(p[0]); err != nil {
				t.Errorf("chord %q: %v", p[0], err)
			}
			if p[1] == "" {
				t.Errorf("chord %q without action", p[0])
			}
		}
	}
}

func TestMacBindings(t *testing.T) {
	k := ForOS("darwin")
	want := map[string]Action{
		"Cmd+D":          DuplicateLines,
		"Cmd+Backspace":  DeleteLine,
		"Cmd+/":          ToggleComment,
		"Cmd+Shift+Up":   MoveLinesUp,
		"Cmd+Shift+Down": MoveLinesDown,
		"Ctrl+G":         AddNextOccurrence,
		"Cmd+F":          Find,
		"Cmd+R":          Replace,
		"Cmd+Z":          Undo,
		"Cmd+Shift+Z":    Redo,
		"Alt+Left":       MoveWordLeft,
		"Alt+Right":      MoveWordRight,
		"Alt+Up":         ExpandSelection,
		"Tab":            Indent,
		"Shift+Tab":      Outdent,
	}
	for c, a := range want {
		if got, ok := k.Lookup(MustParse(c)); !ok || got != a {
			t.Errorf("%s → %q, want %q", c, got, a)
		}
	}
	if k.Name != "macOS" || k.Len() < 60 {
		t.Errorf("name %q len %d", k.Name, k.Len())
	}
}

func TestPCBindings(t *testing.T) {
	k := ForOS("linux")
	if a, _ := k.Lookup(MustParse("Ctrl+D")); a != DuplicateLines {
		t.Errorf("Ctrl+D → %q", a)
	}
	if a, _ := k.Lookup(MustParse("Ctrl+Left")); a != MoveWordLeft {
		t.Errorf("Ctrl+Left → %q", a)
	}
	if _, ok := k.Lookup(MustParse("Cmd+D")); ok {
		t.Error("PC keymap uses Cmd")
	}
}

func TestEditing(t *testing.T) {
	var k Keymap
	if k.Len() != 0 || k.Bindings() != nil && len(k.Bindings()) != 0 {
		t.Fatal("zero keymap not empty")
	}
	k.Bind(MustParse("Cmd+K"), "x.a")
	k.Bind(MustParse("Cmd+J"), "x.a")
	k.Bind(MustParse("Cmd+L"), "x.b")
	if got := k.ChordsFor("x.a"); len(got) != 2 || got[0].String() != "Cmd+J" {
		t.Fatalf("ChordsFor = %v", got)
	}
	if keys := k.Keys(); len(keys) != 3 || keys[0] != "J" {
		t.Fatalf("Keys = %v", keys)
	}
	c := k.Clone()
	v := k.Version()
	k.Unbind(MustParse("Cmd+K"))
	if _, ok := k.Lookup(MustParse("Cmd+K")); ok || k.Version() == v {
		t.Fatal("unbind")
	}
	if _, ok := c.Lookup(MustParse("Cmd+K")); !ok {
		t.Fatal("clone shares state")
	}
	var nilMap *Keymap
	if _, ok := nilMap.Lookup(Chord{}); ok || nilMap.Len() != 0 || nilMap.Keys() != nil || nilMap.Version() != 0 || nilMap.Bindings() != nil {
		t.Fatal("nil keymap")
	}
	b := New("t", Binding{MustParse("A"), "a"}, Binding{MustParse("A"), "b"})
	if a, _ := b.Lookup(MustParse("A")); a != "b" {
		t.Fatal("later binding must win")
	}
}

func TestExternal(t *testing.T) {
	if !IsExternal(Find) || !IsExternal(Replace) || IsExternal(Undo) {
		t.Fatal("IsExternal")
	}
	if Mods(0).String() != "" {
		t.Fatal("empty mods")
	}
}
