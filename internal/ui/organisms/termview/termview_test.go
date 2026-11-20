package termview

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func screen(history int) *terminal.Screen {
	sc := &terminal.Screen{ID: "t1", Cols: 40, Rows: 4, History: history, Total: history + 4, Top: history, Running: true,
		Cursor: vt.Cursor{Row: 1, Col: 2, Visible: true}}
	sc.Lines = []terminal.ScreenLine{
		{Index: history, Text: "$ ls", Spans: []terminal.Span{{Col: 0, Width: 4, Text: "$ ls", FG: "p2", Bold: true}}},
		{Index: history + 1, Text: "a.php:3 err", Spans: []terminal.Span{
			{Col: 0, Width: 7, Text: "a.php:3", Underline: true},
			{Col: 7, Width: 4, Text: " err", FG: "#ff0000", BG: "p1", Inverse: true, Faint: true, Strike: true, Italic: true}},
			Links: []terminal.Link{{Col: 0, EndCol: 7, File: "/p/a.php", Line: 3}}},
		{Index: history + 2, Text: "世", Spans: []terminal.Span{{Col: 0, Width: 2, Text: "世", BG: "#102030"}}},
	}
	return sc
}

type rec struct {
	v   *View
	evs []Event
}

func (r *rec) frame(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	r.evs = append(r.evs, r.v.Update(gtx)...)
	return r.v.Layout(gtx, th)
}

func (r *rec) take() []Event {
	e := r.evs
	r.evs = nil
	return e
}

func TestSmoke(t *testing.T) {
	v := New()
	v.Screen = screen(0)
	v.Notice = "process exited with code 0"
	uitest.Smoke(t, "terminal", image.Pt(600, 200), v.Layout)
	uitest.Smoke(t, "empty", image.Pt(600, 200), New().Layout)
	for _, th := range []*tokens.Theme{tokens.Light(nil), tokens.Dark(nil)} {
		img, err := uitest.Render(th, image.Pt(300, 100), v.Layout)
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		if uitest.CountDiffering(img, th.Terminal.Background) == 0 {
			t.Fatal("nothing drawn")
		}
	}
}

func TestResizeAndKeys(t *testing.T) {
	r := &rec{v: New()}
	r.v.Screen = screen(0)
	r.v.Mac = true
	h := uitest.NewHarness(image.Pt(600, 200))
	r.v.Focus()
	h.Frame(r.frame)
	h.Frame(r.frame)
	evs := r.take()
	var rs *ResizeEvent
	focused := false
	for _, e := range evs {
		switch e := e.(type) {
		case ResizeEvent:
			rs = &e
		case FocusEvent:
			focused = e.Focused
		}
	}
	if rs == nil || rs.Cols < 10 || rs.Rows < 2 || r.v.Cols() != rs.Cols || r.v.Rows() != rs.Rows || !focused || !r.v.Focused() {
		t.Fatalf("resize/focus %+v focused=%v", evs, focused)
	}
	press := func(name key.Name, mods key.Modifiers) {
		h.Router.Queue(key.Event{Name: name, Modifiers: mods, State: key.Press})
		h.Frame(r.frame)
	}
	press(key.NameUpArrow, 0)
	press("C", key.ModCtrl)
	press(key.NameSpace, 0)                   // typed text: no key event
	press("B", key.ModAlt)                    // Option without Meta on macOS: text
	press(key.NameLeftArrow, key.ModAlt)      // word left
	press(key.NameDeleteBackward, key.ModAlt) // delete word
	press(key.NameF5, key.ModShift)           // F-keys with modifiers
	press(key.NameTab, key.ModShift)          // back tab
	press(key.NameEscape, 0)                  // to the program
	press(key.NameEscape, key.ModShift)       // hide
	press(key.NameCtrl, key.ModCtrl)          // modifier alone: nothing
	press(key.NameReturn, 0)
	h.Router.Queue(key.EditEvent{Text: "\n"}) // the Return's text is dropped
	h.Frame(r.frame)
	h.Router.Queue(key.EditEvent{Text: "ä"})
	h.Frame(r.frame)
	r.v.OptionAsMeta = true
	// macOS delivers the text of Option+B before the key event: the
	// text is dropped while Option is held.
	h.Router.Queue(key.Event{Name: key.NameAlt, State: key.Press}, key.EditEvent{Text: "∫"})
	h.Frame(r.frame)
	press("B", key.ModAlt)
	h.Router.Queue(key.Event{Name: key.NameAlt, State: key.Release}, key.EditEvent{Text: "x"})
	h.Frame(r.frame)
	r.v.EscapeToEditor = true
	press(key.NameEscape, 0)
	want := []Event{
		KeyEvent{Key: vt.Key{Name: "Up"}},
		KeyEvent{Key: vt.Key{Name: "C", Ctrl: true}},
		KeyEvent{Key: vt.Key{Name: "Left", Alt: true}},
		KeyEvent{Key: vt.Key{Name: "Backspace", Alt: true}},
		KeyEvent{Key: vt.Key{Name: "F5", Shift: true}},
		KeyEvent{Key: vt.Key{Name: "Tab", Shift: true}},
		KeyEvent{Key: vt.Key{Name: "Escape"}},
		HideEvent{},
		KeyEvent{Key: vt.Key{Name: "Enter"}},
		TextEvent{Text: "ä"},
		KeyEvent{Key: vt.Key{Name: "B", Alt: true}},
		TextEvent{Text: "x"},
		EscapeEvent{},
	}
	got := r.take()
	if len(got) != len(want) {
		t.Fatalf("events\n got %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d: %+v want %+v", i, got[i], want[i])
		}
	}
	// PC: Alt is Meta, Ctrl+Shift+C copies (nothing selected: no event).
	r.v.Mac, r.v.OptionAsMeta = false, false
	press("X", key.ModAlt)
	press("C", key.ModCtrl|key.ModShift)
	// AltGr (Ctrl+Alt) types characters: the text counts, not the key.
	press("Q", key.ModCtrl|key.ModAlt)
	h.Router.Queue(key.EditEvent{Text: "@"})
	h.Frame(r.frame)
	if got := r.take(); len(got) != 2 || got[0] != (KeyEvent{Key: vt.Key{Name: "X", Alt: true}}) || got[1] != (TextEvent{Text: "@"}) {
		t.Fatalf("pc events %+v", got)
	}
}

func TestSelectionLinksAndScroll(t *testing.T) {
	r := &rec{v: New()}
	r.v.Screen = screen(10)
	r.v.Mac = true
	h := uitest.NewHarness(image.Pt(600, 200))
	h.Frame(r.frame)
	m := r.v.met
	cell := func(row, col int) image.Point {
		return image.Pt(r.v.origin.X+m.colX(col)+m.colX(1)/2, r.v.origin.Y+row*m.lineH+m.lineH/2)
	}
	// Click on the link opens it.
	h.Click(cell(1, 2))
	h.Frame(r.frame)
	h.Frame(r.frame) // the focus request defers the release
	evs := r.take()
	var link *LinkEvent
	for _, e := range evs {
		if l, ok := e.(LinkEvent); ok {
			link = &l
		}
	}
	if link == nil || link.Link.File != "/p/a.php" || link.Link.Line != 3 {
		t.Fatalf("link events %+v", evs)
	}
	// Drag selects; Cmd+C asks to copy.
	h.Drag(cell(0, 0).Sub(image.Pt(m.colX(1)/2, 0)), cell(1, 4))
	h.Frame(r.frame)
	h.Frame(r.frame)
	if !r.v.HasSelection() {
		t.Fatal("no selection")
	}
	from, to := r.v.Selection()
	if from.Line != 10 || to.Line != 11 || from.Col != 0 {
		t.Fatalf("selection %+v %+v", from, to)
	}
	h.Router.Queue(key.Event{Name: "C", Modifiers: key.ModCommand, State: key.Press})
	h.Frame(r.frame)
	evs = r.take()
	found := false
	for _, e := range evs {
		if c, ok := e.(CopyEvent); ok && c.From == from && c.To == to {
			found = true
		}
	}
	if !found {
		t.Fatalf("copy events %+v", evs)
	}
	r.v.SelectAll()
	if f, to := r.v.Selection(); f != (Pos{}) || to.Line != 13 {
		t.Fatalf("select all %+v %+v", f, to)
	}
	r.v.ClearSelection()
	// Wheel up leaves the bottom; typing returns to it.
	if !r.v.Following() || r.v.Top() != 10 {
		t.Fatal("not following")
	}
	h.Router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(50, 50), Scroll: f32.Pt(0, float32(-3*m.lineH))})
	h.Frame(r.frame)
	if r.v.Following() || r.v.Top() != 7 {
		t.Fatalf("after wheel top %d following %v", r.v.Top(), r.v.Following())
	}
	h.Router.Queue(key.Event{Name: key.NamePageUp, Modifiers: key.ModShift, State: key.Press})
	h.Frame(r.frame)
	if r.v.Top() >= 7 {
		t.Fatalf("shift+pageup top %d", r.v.Top())
	}
	h.Router.Queue(key.EditEvent{Text: "x"})
	h.Frame(r.frame)
	if !r.v.Following() {
		t.Fatal("typing did not return to the bottom")
	}
	// Alternate screen: the wheel sends arrow keys.
	r.v.Screen.Modes.AltScreen, r.v.Screen.Modes.AltScroll = true, true
	r.take()
	h.Router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(50, 50), Scroll: f32.Pt(0, float32(2*m.lineH))})
	h.Frame(r.frame)
	if evs := r.take(); len(evs) != 2 || evs[0] != (KeyEvent{Key: vt.Key{Name: "Down"}}) {
		t.Fatalf("alt screen wheel %+v", evs)
	}
	r.v.ScrollToBottom()
	if !r.v.Following() {
		t.Fatal("ScrollToBottom")
	}
}

func TestColors(t *testing.T) {
	th := tokens.Dark(nil)
	fg, _, set := colors(th, terminal.Span{FG: "p1", Bold: true}, false)
	if fg != th.Terminal.ANSI[9] || set {
		t.Fatalf("bold brightens: %v", fg)
	}
	fg, bg, set := colors(th, terminal.Span{FG: "#010203", Inverse: true}, false)
	if bg.R != 1 || fg != th.Terminal.Background || !set {
		t.Fatalf("inverse %v %v", fg, bg)
	}
}
