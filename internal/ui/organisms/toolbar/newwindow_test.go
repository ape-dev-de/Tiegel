package toolbar

import (
	"reflect"
	"testing"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
)

// D547: "Open in New Window" through the context menu of a project row,
// Shift+click and Shift+Enter.
func TestProjectPopupNewWindow(t *testing.T) {
	b := sample(false)
	r := newRig(t, b, 1200)
	open := func() {
		r.click(center(r.seg(SegProject)))
		r.take()
	}
	// Without NewWindows there is no alternative and no menu.
	open()
	r.h.ClickWith(r.rowPoint(&b.projectPop, 2), pointer.ButtonPrimary, key.ModShift)
	r.frame()
	r.frame()
	r.expect(SwitchProject{ID: "p2"})

	b.NewWindows = true
	// Shift+click on an open project, a recent one and "Open…".
	for _, c := range []struct {
		row  int
		want Event
	}{{2, SwitchProject{ID: "p2", NewWindow: true}}, {5, OpenRecentProject{Path: "/work/tools", NewWindow: true}}, {7, OpenProject{NewWindow: true}}} {
		open()
		r.h.ClickWith(r.rowPoint(&b.projectPop, c.row), pointer.ButtonPrimary, key.ModShift)
		r.frame()
		r.frame()
		r.expect(c.want)
	}
	// The current project has no alternative.
	open()
	if b.projectPop.items[1].alt != nil || b.projectPop.items[1].menu != nil {
		t.Fatal("current project offers a new window")
	}
	// Shift+Enter on the preselected row.
	r.key(key.NameDownArrow)
	r.h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press, Modifiers: key.ModShift})
	r.frame()
	r.expect(SwitchProject{ID: "p2", NewWindow: true})

	// Right click opens the row's menu as submenu; its entries choose.
	open()
	r.h.ClickWith(r.rowPoint(&b.projectPop, 5), pointer.ButtonSecondary, 0)
	r.frame()
	r.frame()
	r.expect()
	if b.projectPop.subOf != 5 || !reflect.DeepEqual(labels(b.projectPop.items[5].sub), []string{"Open in This Window", "Open in New Window"}) {
		t.Fatalf("context menu %d %q", b.projectPop.subOf, labels(b.projectPop.items[5].sub))
	}
	r.click(r.subPoint(&b.projectPop, 1))
	r.expect(OpenRecentProject{Path: "/work/tools", NewWindow: true})
	// Escape closes the menu; the row then chooses as before.
	open()
	r.h.ClickWith(r.rowPoint(&b.projectPop, 2), pointer.ButtonSecondary, 0)
	r.frame()
	r.frame()
	if b.projectPop.subOf != 2 {
		t.Fatal("no menu")
	}
	r.key(key.NameEscape)
	if b.projectPop.subOf != -1 || b.projectPop.items[2].sub != nil || b.Opened() != PopupProject {
		t.Fatal("menu not closed")
	}
	r.click(r.rowPoint(&b.projectPop, 2))
	r.expect(SwitchProject{ID: "p2"})
	// Ctrl+click on macOS is a context click, not a switch.
	b.GOOS = "darwin"
	open()
	r.h.ClickWith(r.rowPoint(&b.projectPop, 2), pointer.ButtonPrimary, key.ModCtrl)
	r.frame()
	r.frame()
	r.expect()
	if b.projectPop.subOf != 2 {
		t.Fatal("Ctrl+click did not open the menu")
	}
	r.click(r.subPoint(&b.projectPop, 0))
	r.expect(SwitchProject{ID: "p2"})
}
