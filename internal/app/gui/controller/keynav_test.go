package controller

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestPlacesAndLastEdit(t *testing.T) {
	var n Navigation
	n.Push(At("/a.php", 5))
	n.Record(Place{Location: At("/b.php", 10), Line: 2, Snippet: "x", Edit: true})
	n.Record(Place{Location: At("/c.php", 20), Line: 4, Snippet: "y", Edit: true})
	// Same line again: moves to the front, no duplicate.
	n.Record(Place{Location: At("/b.php", 12), Line: 2, Snippet: "x2"})
	ps := n.Places()
	if len(ps) != 3 || ps[0].File != "/b.php" || !ps[0].Edit || ps[0].Snippet != "x2" || ps[2].File != "/a.php" {
		t.Fatalf("places %+v", ps)
	}
	// Last edit: b (newest), then c; the current line is skipped.
	p, ok := n.LastEdit(At("/b.php", 12), 2)
	if !ok || p.File != "/c.php" {
		t.Fatalf("last edit from b: %+v %v", p, ok)
	}
	if _, ok := n.LastEdit(Location{}, 0); ok {
		t.Fatal("no older edits")
	}
	// A new edit restarts at the newest.
	n.Record(Place{Location: At("/d.php", 1), Line: 1, Edit: true})
	if p, _ := n.LastEdit(Location{}, 0); p.File != "/d.php" {
		t.Fatalf("after new edit: %+v", p)
	}
	for i := 0; i < maxPlaces+5; i++ {
		n.Record(Place{Location: At(fmt.Sprintf("/f%d.php", i), 0), Line: 1})
	}
	if len(n.Places()) != maxPlaces {
		t.Fatalf("bounded: %d", len(n.Places()))
	}
}

func TestNextDiagnostic(t *testing.T) {
	ds := []Diagnostic{{Start: 30, Message: "c"}, {Start: 10, Message: "a"}, {Start: 20, Message: "b"}}
	cases := []struct {
		caret   int
		forward bool
		want    string
	}{
		{0, true, "a"}, {10, true, "b"}, {30, true, "a"}, // wraps
		{25, false, "b"}, {10, false, "c"}, {31, false, "c"},
	}
	for _, c := range cases {
		if d, ok := NextDiagnostic(ds, c.caret, c.forward); !ok || d.Message != c.want {
			t.Errorf("caret %d forward %v: %q", c.caret, c.forward, d.Message)
		}
	}
	if _, ok := NextDiagnostic(nil, 0, true); ok {
		t.Fatal("no diagnostics")
	}
}

func TestKeymapSettingChanged(t *testing.T) {
	for payload, want := range map[string]bool{
		`{"Key":"keymap.overrides"}`: true, `{"Key":"ui.theme"}`: false, `nope`: false,
	} {
		if got := keymapSettingChanged(json.RawMessage(payload)); got != want {
			t.Errorf("%s: %v", payload, got)
		}
	}
}
