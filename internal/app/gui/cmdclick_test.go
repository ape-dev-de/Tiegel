package gui

import (
	"image"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
)

// TestCmdClickNavigates: Cmd+click on a call jumps to its definition
// (the window routes the press of the editor's hover layer).
func TestCmdClickNavigates(t *testing.T) {
	e := newEnvWith(t, true, navFixture, nil)
	e.waitIndex()
	s := e.openEditor("src/Room.php")
	// Editor origin in window coordinates: move the pointer into the
	// editor and compare with the local hover position.
	probe := image.Pt(800, 300)
	e.h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(float32(probe.X), float32(probe.Y))})
	e.frame()
	if !s.hoverIn {
		t.Fatal("probe point is not over the editor")
	}
	origin := probe.Sub(s.hoverPos)
	s.doc.Lock()
	off := strings.Index(string(s.doc.Buffer().Slice(0, s.doc.Buffer().Len())), "Shape $floor") + 1
	s.doc.Unlock()
	r, ok := s.ed.AnchorRect(off)
	if !ok {
		t.Fatal("call not visible")
	}
	at := origin.Add(image.Pt(r.Min.X+1, (r.Min.Y+r.Max.Y)/2))
	pos := f32.Pt(float32(at.X), float32(at.Y))
	e.h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos, Modifiers: key.ModShortcut},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Modifiers: key.ModShortcut},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos, Modifiers: key.ModShortcut},
	)
	e.frame()
	e.waitFor("definition", func() bool { e.frame(); return strings.HasSuffix(e.ctl.ActiveTab().File, "Shape.php") })
}

func TestCmdBNavigatesSameSpot(t *testing.T) {
	e := newEnvWith(t, true, navFixture, nil)
	e.waitIndex()
	s := e.openEditor("src/Room.php")
	e.caretAt(s, "Shape $floor", 1)
	e.key("B", key.ModCommand)
	e.waitFor("definition", func() bool { e.frame(); return strings.HasSuffix(e.ctl.ActiveTab().File, "Shape.php") })
}

// TestCmdBOnDeclarationFindsUsages: on the declaration itself the jump
// becomes Find Usages (without a language server: its notice).
func TestCmdBOnDeclarationFindsUsages(t *testing.T) {
	e := newEnvWith(t, true, navFixture, nil)
	e.waitIndex()
	s := e.openEditor("src/Shape.php")
	e.caretAt(s, "interface Shape", len("interface S"))
	e.key("B", key.ModCommand)
	e.waitFor("usages notice", func() bool { e.frame(); return strings.HasPrefix(e.ctl.Status.Notice, "Find Usages") })
}

// TestCopyReference: Copy Reference copies the FQN of the symbol at the
// caret (path:line elsewhere); Copy Path:Line copies path:line.
func TestCopyReference(t *testing.T) {
	e := newEnvWith(t, true, navFixture, nil)
	e.waitIndex()
	s := e.openEditor("src/Room.php")
	clip := func() string {
		e.frame()
		_, c, _ := e.h.Router.WriteClipboard()
		return string(c)
	}
	e.caretAt(s, "function size", len("function s"))
	e.v.RunAction(ActCopyReference)
	e.waitFor("reference", func() bool { return clip() == `App\Room::size` })
	e.caretAt(s, "Shape $floor", 1)
	e.v.RunAction(ActCopyReference)
	e.waitFor("class reference", func() bool { return clip() == `App\Shape` })
	e.caretAt(s, "return", 0)
	e.v.RunAction(ActCopyReference)
	e.waitFor("fallback", func() bool { return clip() == "src/Room.php:13" })
	e.caretAt(s, "final class", 0)
	e.v.RunAction(ActCopyPathLine)
	if got := clip(); got != "src/Room.php:5" {
		t.Fatalf("path:line %q", got)
	}
	// The editor context menu offers both.
	items := e.v.Registry.ContextEntries("editor", e.v.sessionContext(s, "editor"), e.v.goos)
	var found bool
	for _, it := range items {
		for _, ch := range it.Children {
			found = found || ch.Action == ActCopyReference
		}
	}
	if !found {
		t.Fatal("Copy Reference not in the editor menu")
	}
}
