package termview

import (
	"image"
	"image/color"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// richScreen emulates output with every kind of decoration, geometry
// and link the view draws.
func richScreen(t *testing.T) *terminal.Screen {
	t.Helper()
	data := "╭─┬─╮ ┏━┓ ╔═╗ ┄┅┆┇┈┉┊┋╌╍╎╏ ╱╲╳ ╴╵╶╷╸╹╺╻╼╽╾╿\r\n" +
		"▀▁▂▃▄▅▆▇█▉▊▋▌▍▎▏▐░▒▓▔▕▖▗▘▙▚▛▜▝▞▟ \r\n" +
		"\x1b[4mu\x1b[4:2md\x1b[4:3mc\x1b[4:4mo\x1b[4:5ma\x1b[58;5;1mr\x1b[0m \x1b[9ms\x1b[0m \x1b[53mo\x1b[0m \x1b[8mh\x1b[0m \x1b[7mi\x1b[0m \x1b[2mf\x1b[0m \x1b[1;3mbi\x1b[0m\r\n" +
		"日本 é 👍 https://example.com/x \x1b]8;;https://osc.test\x1b\\osc\x1b]8;;\x1b\\ a.php:3\r\n" +
		"\x1b[38;2;10;20;30;48;5;200mtrue\x1b[0m\x1b[?12h"
	r := terminal.Emulate([]byte(data), terminal.EmulateOptions{Cols: 70, Rows: 6, Screen: terminal.ScreenOptions{Top: -1, URLs: true}})
	sc := r.Screen
	// A file link (the emulator has no linker).
	sc.Lines[3].Links = append(sc.Lines[3].Links, terminal.Link{Col: 40, EndCol: 47, File: "/p/a.php", Line: 3})
	return &sc
}

func TestSmokeRich(t *testing.T) {
	sc := richScreen(t)
	v := New()
	v.Screen = sc
	v.Matches = []terminal.Match{{Line: 0, Col: 0, EndLine: 0, EndCol: 3}, {Line: 3, Col: 5, EndLine: 3, EndCol: 8}}
	v.Current = 1
	v.Select(Pos{Line: 1, Col: 2}, Pos{Line: 2, Col: 4})
	uitest.Smoke(t, "rich", image.Pt(900, 200), v.Layout)
	// Reverse video and a focused, blinking bar cursor.
	sc2 := *sc
	sc2.Modes.ReverseVideo = true
	sc2.Cursor.Shape = 5
	v2 := New()
	v2.Screen = &sc2
	uitest.Smoke(t, "reverse", image.Pt(900, 200), v2.Layout)
	for _, th := range []*tokens.Theme{tokens.Light(nil), tokens.Dark(nil)} {
		img, err := uitest.Render(th, image.Pt(900, 200), v.Layout)
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		if uitest.CountDiffering(img, th.Terminal.Background) < 1000 {
			t.Fatal("nothing drawn")
		}
	}
}

// TestBoxDrawingJoins: a row of ─ is one unbroken line across the cell
// boundaries, and a full block fills its cell.
func TestBoxDrawingJoins(t *testing.T) {
	r := terminal.Emulate([]byte("────────\r\n████"), terminal.EmulateOptions{Cols: 10, Rows: 2, Screen: terminal.ScreenOptions{Top: -1}})
	v := New()
	v.Screen = &r.Screen
	v.Screen.Running = false
	th := tokens.Dark(nil)
	img, err := uitest.Render(th, image.Pt(200, 80), v.Layout)
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	m := v.met
	fg := th.Terminal.Foreground
	near := func(c color.RGBA) bool {
		d := func(a, b uint8) int { return abs(int(a) - int(b)) }
		return d(c.R, fg.R)+d(c.G, fg.G)+d(c.B, fg.B) < 40
	}
	// Find the line's row of pixels in the first text row.
	ly := -1
	for y := v.origin.Y; y < v.origin.Y+m.lineH; y++ {
		if near(img.RGBAAt(v.origin.X+m.colX(1)+1, y)) {
			ly = y
			break
		}
	}
	if ly < 0 {
		t.Fatal("no horizontal line")
	}
	for x := v.origin.X; x < v.origin.X+m.colX(8); x++ {
		if !near(img.RGBAAt(x, ly)) {
			t.Fatalf("gap at x=%d", x)
		}
	}
	// The full block covers its whole cell including the line gap.
	for y := v.origin.Y + m.lineH; y < v.origin.Y+2*m.lineH; y++ {
		if !near(img.RGBAAt(v.origin.X+m.colX(1)+m.colX(1)/2, y)) {
			t.Fatalf("block gap at y=%d", y)
		}
	}
}

func TestDrawGlyphAll(t *testing.T) {
	var ops op.Ops
	c := color.NRGBA{A: 0xff}
	for r := rune(0x2500); r <= 0x259f; r++ {
		drawGlyph(&ops, image.Rect(0, 0, 9, 18), r, c)
	}
	for _, r := range []rune{0xe0b0, 0xe0b1, 0xe0b2, 0xe0b3, 0xe0b4, 0xe0b5, 0xe0b6, 0xe0b7, 0xe0b8, 0xe0ba, 0xe0bc, 0xe0be} {
		if !isGeometric(r) {
			t.Fatalf("%U not geometric", r)
		}
		drawGlyph(&ops, image.Rect(0, 0, 9, 18), r, c)
	}
	if isGeometric('a') || isGeometric(0xe0a0) {
		t.Fatal("not geometric")
	}
}

// TestRowCache: an unchanged snapshot records nothing; output scrolling
// by one line records only the new line.
func TestRowCache(t *testing.T) {
	mk := func(first int) *terminal.Screen {
		sc := &terminal.Screen{Cols: 20, Rows: 4, History: first, Total: first + 4, Top: first, Running: true}
		for i := 0; i < 4; i++ {
			txt := "line " + string(rune('a'+(first+i)%26))
			sc.Lines = append(sc.Lines, terminal.ScreenLine{Index: first + i, Text: txt, Spans: []terminal.Span{{Col: 0, Width: len(txt), Text: txt, FG: "p2"}}})
		}
		return sc
	}
	v := New()
	h := uitest.NewHarness(image.Pt(300, 4*40))
	v.Screen = mk(0)
	h.Frame(v.Layout)
	rows := min(v.Rows(), 4)
	if v.rc.recorded != rows {
		t.Fatalf("first frame recorded %d want %d", v.rc.recorded, rows)
	}
	h.Frame(v.Layout)
	if v.rc.recorded != 0 {
		t.Fatalf("static frame recorded %d", v.rc.recorded)
	}
	// A new but equal snapshot.
	v.Screen = mk(0)
	h.Frame(v.Layout)
	if v.rc.recorded != 0 {
		t.Fatalf("equal snapshot recorded %d", v.rc.recorded)
	}
	v.Screen = mk(1)
	h.Frame(v.Layout)
	if v.rc.recorded != 1 {
		t.Fatalf("scrolled snapshot recorded %d", v.rc.recorded)
	}
	// Dropped scrollback lines keep the stable keys.
	sc := mk(1)
	sc.Dropped = 1
	for i := range sc.Lines {
		sc.Lines[i].Index--
	}
	sc.History, sc.Top, sc.Total = sc.History-1, sc.Top-1, sc.Total-1
	v.Screen = sc
	h.Frame(v.Layout)
	if v.rc.recorded != 0 {
		t.Fatalf("after drop recorded %d", v.rc.recorded)
	}
	// Another theme records everything again.
	h.Theme = tokens.Light(nil)
	h.Frame(v.Layout)
	if v.rc.recorded != rows {
		t.Fatalf("theme change recorded %d", v.rc.recorded)
	}
}

// line5 is a snapshot for pointer tests: words, a URL and a file link.
func line5() *terminal.Screen {
	sc := &terminal.Screen{ID: "t", Cols: 40, Rows: 3, Total: 3, Running: true, Cursor: vt.Cursor{Visible: true}}
	l0 := "foo bar/baz.php qux"
	l1 := "see https://x.test/a now"
	sc.Lines = []terminal.ScreenLine{
		{Index: 0, Text: l0, Wrapped: true, Spans: []terminal.Span{{Col: 0, Width: len(l0), Text: l0}}},
		{Index: 1, Text: l1, Spans: []terminal.Span{{Col: 0, Width: len(l1), Text: l1}},
			Links: []terminal.Link{{Col: 4, EndCol: 20, URL: "https://x.test/a"}}},
		{Index: 2, Text: "世x", Spans: []terminal.Span{{Col: 0, Width: 2, Text: "世"}, {Col: 2, Width: 1, Text: "x"}}},
	}
	return sc
}

func TestMultiClickSelection(t *testing.T) {
	r := &rec{v: New()}
	r.v.Screen = line5()
	r.v.CopyOnSelect = true
	h := uitest.NewHarness(image.Pt(600, 200))
	h.Frame(r.frame)
	m := r.v.met
	cell := func(row, col int) image.Point {
		return image.Pt(r.v.origin.X+m.colX(col)+m.colX(1)/2, r.v.origin.Y+row*m.lineH+m.lineH/2)
	}
	r.take()
	// Double click selects the path-like word.
	h.DoubleClick(cell(0, 6))
	h.Frame(r.frame)
	h.Frame(r.frame)
	from, to := r.v.Selection()
	if from != (Pos{0, 4}) || to != (Pos{0, 15}) {
		t.Fatalf("word %+v %+v", from, to)
	}
	// Copy on select.
	var copied bool
	for _, e := range r.take() {
		if c, ok := e.(CopyEvent); ok && c.From == from && c.To == to {
			copied = true
		}
	}
	if !copied {
		t.Fatal("copy on select")
	}
	// Triple click selects the logical line (row 0 wraps into row 1).
	p := cell(0, 1)
	pos := f32.Pt(float32(p.X), float32(p.Y))
	var evs []any
	for i := 0; i < 3; i++ {
		evs = append(evs,
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Time: time.Duration(100+i*50) * time.Second / 1000},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos, Time: time.Duration(100+i*50) * time.Second / 1000})
	}
	for _, e := range evs {
		h.Router.Queue(e.(pointer.Event))
	}
	h.Frame(r.frame)
	h.Frame(r.frame)
	from, to = r.v.Selection()
	if from != (Pos{0, 0}) || to != (Pos{1, 40}) {
		t.Fatalf("line %+v %+v", from, to)
	}
	// Word helpers.
	if a, b := r.v.wordAt(2, 1); a != 0 || b != 3 {
		t.Fatalf("wide word %d %d", a, b)
	}
	if a, b := r.v.wordAt(0, 3); a != 3 || b != 4 {
		t.Fatalf("space %d %d", a, b)
	}
	if a, b := r.v.wordAt(9, 3); a != 3 || b != 3 {
		t.Fatal("missing line")
	}
}

func TestURLNeedsModifier(t *testing.T) {
	r := &rec{v: New()}
	r.v.Screen = line5()
	r.v.Mac = true
	h := uitest.NewHarness(image.Pt(600, 200))
	h.Frame(r.frame)
	m := r.v.met
	p := image.Pt(r.v.origin.X+m.colX(8)+2, r.v.origin.Y+m.lineH+m.lineH/2)
	pos := f32.Pt(float32(p.X), float32(p.Y))
	clock := time.Duration(0)
	click := func(mods key.Modifiers) []Event {
		clock += time.Second
		h.Router.Queue(
			pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos, Modifiers: mods},
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Modifiers: mods, Time: clock},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos, Modifiers: mods, Time: clock})
		h.Frame(r.frame)
		h.Frame(r.frame)
		return r.take()
	}
	for _, e := range click(0) {
		if _, ok := e.(OpenURLEvent); ok {
			t.Fatal("plain click opened the URL")
		}
	}
	h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos.Add(f32.Pt(1, 0)), Modifiers: key.ModCommand})
	h.Frame(r.frame)
	var opened string
	for _, e := range click(key.ModCommand) {
		if o, ok := e.(OpenURLEvent); ok {
			opened = o.URL
		}
	}
	if opened != "https://x.test/a" {
		t.Fatalf("cmd click %q", opened)
	}
	if !r.v.linkMod(key.ModSuper) {
		t.Fatal("super is the mac shortcut too")
	}
	r.v.Mac = false
	if !r.v.linkMod(key.ModCtrl) || r.v.linkMod(key.ModCommand) {
		t.Fatal("pc link modifier")
	}
}

func TestMouseReporting(t *testing.T) {
	r := &rec{v: New()}
	sc := line5()
	sc.Modes.Mouse = vt.MouseModeAny
	r.v.Screen = sc
	h := uitest.NewHarness(image.Pt(600, 200))
	h.Frame(r.frame)
	r.take()
	m := r.v.met
	at := func(row, col int) f32.Point {
		return f32.Pt(float32(r.v.origin.X+m.colX(col)+2), float32(r.v.origin.Y+row*m.lineH+2))
	}
	h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: at(0, 1)},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: at(0, 1)}, // same cell: once
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary, Position: at(1, 2), Modifiers: key.ModCtrl},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary, Position: at(1, 3)},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at(1, 3)},
	)
	h.Frame(r.frame)
	h.Router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: at(2, 0), Scroll: f32.Pt(0, float32(-2*m.lineH))})
	h.Frame(r.frame)
	var got []vt.Mouse
	for _, e := range r.take() {
		if me, ok := e.(MouseEvent); ok {
			got = append(got, me.Mouse)
		}
	}
	want := []vt.Mouse{
		{Action: vt.MouseMove, Row: 0, Col: 1},
		{Action: vt.MousePress, Button: vt.MouseRight, Row: 1, Col: 2, Ctrl: true},
		{Action: vt.MouseMove, Button: vt.MouseRight, Row: 1, Col: 3},
		{Action: vt.MouseRelease, Button: vt.MouseRight, Row: 1, Col: 3},
		{Action: vt.MousePress, Button: vt.MouseWheelUp, Row: 2, Col: 0},
		{Action: vt.MousePress, Button: vt.MouseWheelUp, Row: 2, Col: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("mouse %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v want %+v", i, got[i], want[i])
		}
	}
	if r.v.HasSelection() {
		t.Fatal("reported events selected text")
	}
	// Shift bypasses the program: it selects.
	h.Router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at(0, 0), Modifiers: key.ModShift, Time: 5 * time.Second},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at(0, 5), Modifiers: key.ModShift, Time: 5 * time.Second},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at(0, 5), Modifiers: key.ModShift, Time: 5 * time.Second})
	h.Frame(r.frame)
	if !r.v.HasSelection() {
		t.Fatal("shift drag did not select")
	}
	for _, e := range r.take() {
		if _, ok := e.(MouseEvent); ok {
			t.Fatal("shift drag reported")
		}
	}
}

func TestFindKeysAndIME(t *testing.T) {
	r := &rec{v: New()}
	r.v.Screen = line5()
	r.v.Mac = true
	h := uitest.NewHarness(image.Pt(600, 200))
	r.v.Focus()
	h.Frame(r.frame)
	h.Frame(r.frame)
	r.take()
	h.Router.Queue(key.Event{Name: "F", Modifiers: key.ModCommand, State: key.Press})
	h.Frame(r.frame)
	r.v.Mac = false
	h.Router.Queue(key.Event{Name: "F", Modifiers: key.ModCtrl | key.ModShift, State: key.Press})
	h.Frame(r.frame)
	h.Router.Queue(key.Event{Name: "A", Modifiers: key.ModCtrl | key.ModShift, State: key.Press})
	h.Frame(r.frame)
	if !r.v.HasSelection() {
		t.Fatal("ctrl+shift+a selects all")
	}
	// An input method composes ´ + e into é: the composition is
	// replaced.
	h.Router.Queue(key.Event{Name: key.NameCtrl, State: key.Release})
	h.Router.Queue(key.EditEvent{Range: key.Range{Start: 0, End: 0}, Text: "´"})
	h.Frame(r.frame)
	h.Router.Queue(key.EditEvent{Range: key.Range{Start: 0, End: 1}, Text: "é"})
	h.Frame(r.frame)
	got := r.take()
	want := []Event{FindEvent{}, FindEvent{}, TextEvent{Text: "´"}, TextEvent{Text: "é", Erase: 1}}
	if len(got) != len(want) {
		t.Fatalf("events %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestRevealBellBlink(t *testing.T) {
	sc := &terminal.Screen{Cols: 10, Rows: 3, History: 100, Total: 103, Top: 100, Running: true,
		Cursor: vt.Cursor{Visible: true, Blink: true}}
	for i := 0; i < 3; i++ {
		sc.Lines = append(sc.Lines, terminal.ScreenLine{Index: 100 + i, Text: "x", Spans: []terminal.Span{{Width: 1, Text: "x"}}})
	}
	v := New()
	v.Screen = sc
	v.Focus()
	h := uitest.NewHarness(image.Pt(200, 3*30))
	frame := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { v.Update(gtx); return v.Layout(gtx, th) }
	h.Frame(frame)
	h.Frame(frame)
	v.Reveal(101) // visible: nothing moves
	if !v.Following() {
		t.Fatal("reveal of a visible line scrolled")
	}
	v.Reveal(20)
	if v.Following() || v.Top() > 20 || v.Top()+v.Rows() <= 20 {
		t.Fatalf("reveal top %d rows %d", v.Top(), v.Rows())
	}
	// The bell flashes once per new BEL.
	sc.Bells = 1
	h.Frame(frame)
	if !v.bellUntil.After(time.Time{}) {
		t.Fatal("no flash")
	}
	sc.Bells = 0
	h.Frame(frame)
	if v.bellSeen != 0 {
		t.Fatal("bell reset")
	}
	// A blinking cursor has a start time while focused.
	if v.blinkStart.IsZero() {
		t.Fatal("blink not started")
	}
}

func TestCursorCellAndCellTexts(t *testing.T) {
	l := terminal.ScreenLine{Spans: []terminal.Span{{Col: 0, Width: 3, Text: "aéb", Bold: true}, {Col: 3, Width: 2, Text: "世"}}}
	if c := cursorCell(l, 1); c.Text != "é" || c.Col != 1 || c.Width != 1 || !c.Bold {
		t.Fatalf("combining %+v", c)
	}
	if c := cursorCell(l, 4); c.Text != "世" || c.Width != 2 {
		t.Fatalf("wide %+v", c)
	}
	if c := cursorCell(l, 7); c.Text != "" || c.Width != 0 {
		t.Fatalf("empty %+v", c)
	}
	cells := cellTexts(l, 6)
	if cells[1] != "é" || cells[3] != "世" || cells[4] != wideRight || cells[5] != "" {
		t.Fatalf("cells %+q", cells)
	}
	if floorDiv(-1, 10) != -1 || floorDiv(15, 10) != 1 {
		t.Fatal("floorDiv")
	}
}
