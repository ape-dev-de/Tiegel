package gui

import (
	"strings"
	"testing"

	"gioui.org/io/key"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Find in Files searches while typing: the dialog lists the matches with
// a preview, Up/Down select and Enter opens the selected match.
func TestFindInFilesLive(t *testing.T) {
	e := newEnvWith(t, true, navFixture, nil)
	e.openEditor("src/Room.php")
	e.key("F", key.ModCommand|key.ModShift)
	if e.v.popup == nil || e.v.popup.Title != "Find in Files" {
		t.Fatal("no find dialog")
	}
	f := e.v.navd.find
	f.pattern.SetText("area")
	f.mask.SetText("*.php")
	e.waitFor("live rows", func() bool {
		st := f.live.st
		return st != nil && !st.Running && len(f.live.rows) == 4
	})
	if e.v.ToolShown(ToolFind) {
		t.Fatal("live search opened the Find window")
	}
	// The preview of the selected match loads its file.
	e.waitFor("preview", func() bool {
		r := f.live.rows[f.live.selected]
		ft := e.v.navd.n.FindText(r.path, r.open)
		return !ft.Loading && strings.Contains(ft.Text, "area")
	})
	if s := f.liveStatus(); !strings.Contains(s, "4 matches") {
		t.Fatalf("status %q", s)
	}
	e.shot("nav-find-live")
	// Typing more searches again.
	f.pattern.SetText("area()")
	e.waitFor("narrowed", func() bool {
		st := f.live.st
		return st != nil && st.Params.Pattern == "area()" && !st.Running
	})
	n := len(f.live.rows)
	if n == 0 {
		t.Fatal("no rows for area()")
	}
	// Down selects the next match; Enter opens it.
	e.key(key.NameDownArrow, 0)
	if n > 1 && f.live.selected != 1 {
		t.Fatalf("selected %d", f.live.selected)
	}
	want := f.live.rows[f.live.selected]
	e.key(key.NameReturn, 0)
	e.settle()
	if e.v.popup != nil {
		t.Fatal("Enter kept the dialog open")
	}
	if tab := e.ctl.ActiveTab(); tab == nil || !strings.HasSuffix(tab.File, want.path) {
		t.Fatalf("Enter opened %v, want %s", tab, want.path)
	}
	// "Open in Find Window" shows the live results without searching again.
	e.key("F", key.ModCommand|key.ModShift)
	f.pattern.SetText("area")
	e.waitFor("rows again", func() bool {
		st := f.live.st
		return st != nil && st.Params.Pattern == "area" && !st.Running && len(f.live.rows) == 4
	})
	st := e.v.navd.n.Find
	e.v.popup.Buttons[0].OnClick()
	e.v.ClosePopup()
	e.frame()
	if e.v.navd.n.Find != st || !e.v.ToolShown(ToolFind) {
		t.Fatal("Open in Find Window searched again or did not show the window")
	}
}

func TestSplitMatch(t *testing.T) {
	b, m, a := splitMatch(api.SearchMatch{LineText: "    return $this->area();", Col: 19, Text: "area"})
	if b != "return $this->" || m != "area" || a != "();" {
		t.Fatalf("%q %q %q", b, m, a)
	}
	b, m, a = splitMatch(api.SearchMatch{LineText: "äö x", Col: 4, Text: "x"})
	if b != "äö " || m != "x" || a != "" {
		t.Fatalf("runes: %q %q %q", b, m, a)
	}
	b, m, _ = splitMatch(api.SearchMatch{LineText: "cut line with needle", Col: 900, PreviewStart: 800, Text: "needle"})
	if b != "cut line with " || m != "needle" {
		t.Fatalf("preview: %q %q", b, m)
	}
	if b, m, _ = splitMatch(api.SearchMatch{LineText: strings.Repeat("x", 60) + "y", Col: 61, Text: "y"}); !strings.HasPrefix(b, "…") || m != "y" {
		t.Fatalf("long: %q", b)
	}
}
