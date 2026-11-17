package keymap

import (
	"slices"
	"testing"
)

// TestShadowedAndConflictsWith covers the queries of the keymap editor:
// chords another action wins, and the conflicts a new binding would cause.
func TestShadowedAndConflictsWith(t *testing.T) {
	cmdB := MustParse("Cmd+B")
	// Cmd+[ is Back by default; the user binds it to Settings as well.
	back := MustParse("Cmd+[")
	s := NewScheme(PresetMac, Overrides{"app.settings": {back}})
	if sh := s.Shadowed(NavBack); !slices.Equal(sh, []Chord{back}) {
		t.Fatalf("shadowed back %v", sh)
	}
	if sh := s.Shadowed("app.settings"); len(sh) != 0 {
		t.Fatalf("user binding shadowed %v", sh)
	}
	if s.Shadowed("nope") != nil {
		t.Fatal("unknown action")
	}
	// Cmd+B is Go to Definition in the editor: a global binding conflicts.
	cs := s.ConflictsWith("app.settings", cmdB)
	if len(cs) != 1 || cs[0].A != "app.settings" || cs[0].B != "navigate.definition" || cs[0].CtxB != CtxEditor {
		t.Fatalf("conflicts %+v", cs)
	}
	if cs := s.ConflictsWith("navigate.definition", cmdB); len(cs) != 0 {
		t.Fatalf("own chord %+v", cs)
	}
	if cs := s.ConflictsWith("app.settings", MustParse("Cmd+Ctrl+Alt+Shift+F9")); len(cs) != 0 {
		t.Fatalf("free chord %+v", cs)
	}
	if s.ConflictsWith("nope", cmdB) != nil {
		t.Fatal("unknown action")
	}
}

func TestStateDropInvalid(t *testing.T) {
	s := State{Invalid: []string{"a Hyper+X", "b Nope+Y"}}
	orig := s.Invalid
	if s.DropInvalid("zzz") {
		t.Fatal("unknown line dropped")
	}
	if !s.DropInvalid("a Hyper+X") || !slices.Equal(s.Invalid, []string{"b Nope+Y"}) {
		t.Fatalf("invalid %q", s.Invalid)
	}
	if orig[0] != "a Hyper+X" {
		t.Fatal("DropInvalid modified the original slice")
	}
}
