package filetree

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// TestTreeContextClickAndKeys: a secondary click selects the row and
// reports a ContextEvent (the empty area reports the root); while focused
// the arrows move the selection and command keys come out as KeyEvent.
func TestTreeContextClickAndKeys(t *testing.T) {
	tr := &Tree{Root: sample(), GOOS: "darwin"}
	var evs []Event
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		evs = append(evs, tr.Update(gtx)...)
		return tr.Layout(gtx, th)
	}
	h := uitest.NewHarness(image.Pt(300, 400))
	h.Frame(w)
	press := func(p image.Point, b pointer.Buttons, mods key.Modifiers) {
		pos := f32.Pt(float32(p.X), float32(p.Y))
		h.Router.Queue(
			pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos},
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: b, Position: pos, Modifiers: mods},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos, Modifiers: mods},
		)
		h.Frame(w)
		h.Frame(w)
	}
	find := func() *ContextEvent {
		for _, e := range evs {
			if c, ok := e.(ContextEvent); ok {
				return &c
			}
		}
		return nil
	}
	press(image.Pt(150, 2*24+12), pointer.ButtonSecondary, 0)
	if c := find(); c == nil || c.Path != "index.php" || c.Dir || tr.Selected() != "index.php" {
		t.Fatalf("context on row: %+v selected %q (%v)", c, tr.Selected(), evs)
	}
	evs = nil
	press(image.Pt(150, 1*24+12), pointer.ButtonPrimary, key.ModCtrl) // Ctrl+click on macOS
	if c := find(); c == nil || c.Path != "src" || !c.Dir {
		t.Fatalf("ctrl click: %+v", c)
	}
	evs = nil
	press(image.Pt(150, 350), pointer.ButtonSecondary, 0)
	if c := find(); c == nil || !c.Background || c.Path != "" {
		t.Fatalf("background: %+v", c)
	}

	// Keys while focused (the context click focused the tree).
	evs = nil
	press1 := func(name key.Name, mods key.Modifiers) {
		h.Router.Queue(key.Event{Name: name, Modifiers: mods, State: key.Press})
		h.Frame(w)
		h.Frame(w)
	}
	tr.Select("src")
	press1(key.NameDownArrow, 0)
	if tr.Selected() != "index.php" {
		t.Fatalf("down: %q (focused %v)", tr.Selected(), tr.Focused())
	}
	press1(key.NameF6, key.ModShift)
	press1("C", key.ModShortcut)
	var keys []KeyEvent
	for _, e := range evs {
		if k, ok := e.(KeyEvent); ok {
			keys = append(keys, k)
		}
	}
	if len(keys) != 2 || keys[0].Name != key.NameF6 || keys[1].Name != "C" || keys[1].Path != "index.php" {
		t.Fatalf("key events %+v", keys)
	}
	evs = nil
	press1(key.NameReturn, 0)
	if len(evs) == 0 {
		t.Fatal("enter did not open")
	}
	if o, ok := evs[len(evs)-1].(OpenEvent); !ok || o.Path != "index.php" {
		t.Fatalf("enter %v", evs)
	}
}
