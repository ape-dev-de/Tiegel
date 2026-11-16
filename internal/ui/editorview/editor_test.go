package editorview

import (
	"image"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/editor/search"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func caret(e *Editor) int { return e.Cursor().Primary().Head }

func TestTypingUndoRedo(t *testing.T) {
	r := newRig(t, "")
	r.focus()
	r.typ("echo 1;")
	r.expectText("echo 1;")
	r.press("Enter")
	r.typ("x(")
	r.expectText("echo 1;\nx()") // auto-closed bracket
	r.press("Cmd+Z")
	r.expectText("echo 1;\n")
	r.press("Cmd+Z")
	r.expectText("echo 1;")
	r.press("Cmd+Shift+Z")
	r.expectText("echo 1;\n")
	if caret(r.e) != 8 {
		t.Fatalf("caret after redo = %d", caret(r.e))
	}
}

func TestReadOnly(t *testing.T) {
	r := newRig(t, "abc")
	r.e.ReadOnly = true
	r.focus()
	r.typ("x")
	r.press("Backspace")
	r.press("Cmd+D")
	r.expectText("abc")
	r.press("Cmd+A")
	r.press("Cmd+C")
	if got := r.clipboard(); got != "abc" {
		t.Fatalf("copy in read-only editor: %q", got)
	}
}

func TestKeyboardNavigationAndEditing(t *testing.T) {
	r := newRig(t, "$foo->bar();\n$x = 1;")
	r.focus()
	r.press("Alt+Right")
	if caret(r.e) != 4 {
		t.Fatalf("word right: %d", caret(r.e))
	}
	r.press("Alt+Shift+Right")
	r.press("Alt+Shift+Right")
	if got := r.e.Cursor().SelectedText()[0]; got != "->bar" {
		t.Fatalf("word selection %q", got)
	}
	r.press("Cmd+Right")
	if caret(r.e) != 12 {
		t.Fatalf("line end: %d", caret(r.e))
	}
	r.press("Down")
	r.press("Cmd+Left")
	if caret(r.e) != 13 {
		t.Fatalf("line start: %d", caret(r.e))
	}
	r.press("Cmd+D")
	r.expectText("$foo->bar();\n$x = 1;\n$x = 1;")
	r.press("Cmd+Shift+Up")
	r.press("Cmd+Shift+Up")
	r.expectText("$x = 1;\n$foo->bar();\n$x = 1;")
	r.press("Cmd+/")
	r.expectText("// $x = 1;\n$foo->bar();\n$x = 1;")
	r.press("Cmd+/")
	r.press("Tab")
	r.expectText("    $x = 1;\n$foo->bar();\n$x = 1;")
	r.press("Shift+Tab")
	r.expectText("$x = 1;\n$foo->bar();\n$x = 1;")
	r.press("Cmd+Backspace")
	r.expectText("$foo->bar();\n$x = 1;")
	r.press("Cmd+Down")
	if caret(r.e) != r.e.Buffer().Len() {
		t.Fatal("doc end")
	}
	r.press("Alt+Backspace")
	r.expectText("$foo->bar();\n$x = 1")
	r.press("Escape")
}

func TestNextOccurrenceAndSelectionExpansion(t *testing.T) {
	r := newRig(t, "$a + $a + $a")
	r.focus()
	r.press("Ctrl+G")
	r.press("Ctrl+G")
	r.press("Ctrl+G")
	if n := len(r.e.Cursor().Selections()); n != 3 {
		t.Fatalf("%d selections", n)
	}
	r.typ("b")
	r.expectText("b + b + b")
	r.press("Escape")
	r.press("Escape")
	r.e.SetCaret(1)
	r.press("Alt+Up")
	if got := r.e.Cursor().SelectedText()[0]; got != "b" {
		t.Fatalf("expand 1: %q", got)
	}
	r.press("Alt+Up")
	r.press("Alt+Up") // whole line == whole document: no further step
	if got := r.e.Cursor().SelectedText()[0]; got != "b + b + b" {
		t.Fatalf("expand 2: %q", got)
	}
	r.press("Alt+Down")
	if got := r.e.Cursor().SelectedText()[0]; got != "b" {
		t.Fatalf("shrink: %q", got)
	}
	r.press("Cmd+Alt+Down") // no line below: no extra cursor
	if n := len(r.e.Cursor().Selections()); n != 1 {
		t.Fatalf("add cursor below on last line: %d", n)
	}
}

func TestClipboard(t *testing.T) {
	r := newRig(t, "one\ntwo")
	r.focus()
	r.press("Shift+Right")
	r.press("Shift+Right")
	r.press("Cmd+C")
	if got := r.clipboard(); got != "on" {
		t.Fatalf("copy %q", got)
	}
	r.press("Cmd+X")
	r.expectText("e\ntwo")
	r.press("Cmd+Down")
	r.paste("ON")
	r.expectText("e\ntwoON")
	// Copy without selection copies the line; pasting inserts it above.
	r.press("Cmd+Up")
	r.press("Cmd+C")
	if got := r.clipboard(); got != "e\n" {
		t.Fatalf("line copy %q", got)
	}
	r.press("Down")
	r.paste("e\n")
	r.expectText("e\ne\ntwoON")
	r.press("Cmd+X") // the caret stayed on its line, which is now "twoON"
	r.expectText("e\ne")
	r.press("Cmd+Z")
	r.expectText("e\ne\ntwoON")
}

func TestClickDragAndMultiClick(t *testing.T) {
	src := "first line\nsecond word here\nthird"
	r := newRig(t, src)
	off := strings.Index(src, "word")
	r.click(r.pointAt(off), 0)
	if !r.e.Focused() || caret(r.e) != off {
		t.Fatalf("click: focused=%v caret=%d", r.e.Focused(), caret(r.e))
	}
	// Double click selects the word, triple click the line.
	r.clock += 1000
	r.clicks(r.pointAt(off+1), 0, 2)
	if got := r.e.Cursor().SelectedText()[0]; got != "word" {
		t.Fatalf("double click: %q", got)
	}
	r.clock += 1000
	r.clicks(r.pointAt(off+1), 0, 3)
	if got := r.e.Cursor().SelectedText()[0]; got != "second word here\n" {
		t.Fatalf("triple click: %q", got)
	}
	// Drag selects.
	r.drag(r.pointAt(1), r.pointAt(strings.Index(src, "ond")), 0)
	if got := r.e.Cursor().SelectedText()[0]; got != "irst line\nsec" {
		t.Fatalf("drag: %q", got)
	}
	// Shift+click extends.
	r.click(r.pointAt(0), 0)
	r.click(r.pointAt(5), key.ModShift)
	if got := r.e.Cursor().SelectedText()[0]; got != "first" {
		t.Fatalf("shift click: %q", got)
	}
	// Alt+click adds cursors.
	r.click(r.pointAt(0), 0)
	r.click(r.pointAt(strings.Index(src, "third")), key.ModAlt)
	if n := len(r.e.Cursor().Selections()); n != 2 {
		t.Fatalf("alt click: %d cursors", n)
	}
	r.typ("#")
	r.expectText("#first line\nsecond word here\n#third")
}

func TestBlockSelection(t *testing.T) {
	r := newRig(t, "abcdef\nabcdef\nab\nabcdef")
	r.drag(r.pointAt(1), r.pointAt(17+3), key.ModAlt)
	sels := r.e.Cursor().Selections()
	if len(sels) != 4 {
		t.Fatalf("block: %v", sels)
	}
	got := r.e.Cursor().SelectedText()
	want := []string{"bc", "bc", "b", "bc"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("block text %q, want %q", got, want)
		}
	}
}

func TestUnicodeCursorPositions(t *testing.T) {
	// Tab, emoji (wide), combining sequence, CJK.
	src := "\tx😀é漢z"
	r := newRig(t, src)
	cols := map[int]int{0: 0, 1: 4, 2: 5, 6: 7, 9: 8, 12: 10}
	base, _ := r.e.AnchorRect(0)
	for off, col := range cols {
		rect, ok := r.e.AnchorRect(off)
		if !ok {
			t.Fatalf("offset %d not visible", off)
		}
		if got := rect.Min.X - base.Min.X; got != r.e.met.colX(col) {
			t.Errorf("offset %d at x %d, want col %d", off, got, col)
		}
		// Clicking the cell lands on the same grapheme boundary.
		r.click(r.pointAt(off), 0)
		if caret(r.e) != off {
			t.Errorf("click at offset %d → %d", off, caret(r.e))
		}
	}
	r.focus()
	r.e.SetCaret(6)
	r.press("Right")
	if caret(r.e) != 9 {
		t.Fatalf("right over combining sequence: %d", caret(r.e))
	}
	r.press("Backspace")
	r.expectText("\tx😀漢z")
}

func TestEventsOut(t *testing.T) {
	r := newRig(t, "a\nb\nc\nd")
	r.e.SetFoldRanges([]fold.Range{{StartLine: 0, EndLine: 2}})
	r.frame()
	r.takeEvents()
	r.focus()
	r.typ("x")
	r.press("Cmd+F")
	r.press("Cmd+R")
	var changes, actions, focusIn int
	for _, ev := range r.takeEvents() {
		switch ev := ev.(type) {
		case ChangeEvent:
			changes++
			if string(ev.Edit.Text) != "x" {
				t.Errorf("change text %q", ev.Edit.Text)
			}
		case ActionEvent:
			actions++
		case FocusEvent:
			if ev.Focused {
				focusIn++
			}
		}
	}
	if changes != 1 || actions != 2 || focusIn != 1 {
		t.Fatalf("changes=%d actions=%d focus=%d", changes, actions, focusIn)
	}
	// Focus loss is reported (autosave trigger).
	r.h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Execute(key.FocusCmd{Tag: nil})
		return r.widget(gtx, th)
	})
	r.frame()
	lost := false
	for _, ev := range r.takeEvents() {
		if fe, ok := ev.(FocusEvent); ok && !fe.Focused {
			lost = true
		}
	}
	if !lost || r.e.Focused() {
		t.Fatal("focus loss not reported")
	}
	// Gutter: clicking the fold arrow collapses, clicking a number reports.
	g := r.e.geo
	y := float32(g.lineH / 2)
	r.click(f32.Pt(float32(g.foldX+g.foldW/2), y), 0)
	if !r.e.Folds().IsCollapsed(0) {
		t.Fatal("fold arrow did not collapse")
	}
	r.click(f32.Pt(float32(g.numRight-2), y+float32(g.lineH)), 0)
	var clicks []GutterClickEvent
	for _, ev := range r.takeEvents() {
		if gc, ok := ev.(GutterClickEvent); ok {
			clicks = append(clicks, gc)
		}
	}
	if len(clicks) != 2 || clicks[0].Area != GutterFold || clicks[1].Area != GutterNumbers || clicks[1].Line != 3 {
		t.Fatalf("gutter clicks %+v", clicks)
	}
	if _, ok := r.e.AnchorRect(r.e.Buffer().LineStart(1)); ok {
		t.Fatal("folded line reported visible")
	}
	// A right click into the icon column is reported as secondary; right
	// clicks into the text are not.
	p := f32.Pt(float32(g.iconX+1), y+float32(g.lineH))
	r.h.Router.Queue(r.pointer(pointer.Move, p, 0, 0), r.pointer(pointer.Press, p, key.ModAlt, pointer.ButtonSecondary), r.pointer(pointer.Release, p, 0, 0))
	tp := f32.Pt(float32(g.size.X-20), y)
	r.h.Router.Queue(r.pointer(pointer.Move, tp, 0, 0), r.pointer(pointer.Press, tp, 0, pointer.ButtonSecondary), r.pointer(pointer.Release, tp, 0, 0))
	r.frame()
	clicks = clicks[:0]
	for _, ev := range r.takeEvents() {
		if gc, ok := ev.(GutterClickEvent); ok {
			clicks = append(clicks, gc)
		}
	}
	if len(clicks) != 1 || !clicks[0].Secondary || clicks[0].Area != GutterIcons || clicks[0].Line != 3 || clicks[0].Mods&keymap.Alt == 0 {
		t.Fatalf("secondary clicks %+v", clicks)
	}
	// Moving the caret into the fold reveals it.
	r.e.SetCaret(r.e.Buffer().LineStart(2))
	if r.e.Folds().IsCollapsed(0) {
		t.Fatal("fold not revealed")
	}
}

func TestEventsDroppedWhenNotCollected(t *testing.T) {
	h := uitest.NewHarness(image.Pt(400, 300))
	e := New(buffer.NewString(""))
	defer e.Close()
	e.Buffer().Insert(0, []byte("x"))
	h.Frame(e.Layout)
	h.Frame(e.Layout)
	if len(e.events) != 0 {
		t.Fatalf("%d stale events kept", len(e.events))
	}
}

func TestScrolling(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("line with some text that is quite long ")
		b.WriteString(strings.Repeat("x", i%300))
		b.WriteByte('\n')
	}
	r := newRig(t, b.String())
	r.h.Router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(300, 300), Scroll: f32.Pt(0, 400)})
	r.frame()
	if y := r.e.ScrollOffset().Y; y != 400 {
		t.Fatalf("scroll y = %d", y)
	}
	if l := r.e.FirstVisibleLine(); l != 400/r.e.LineHeight() {
		t.Fatalf("first visible line %d", l)
	}
	r.focus()
	r.press("Cmd+Down")
	r.frame()
	if _, ok := r.e.AnchorRect(caret(r.e)); !ok {
		t.Fatal("caret not scrolled into view")
	}
	r.press("Cmd+Up")
	r.frame()
	if r.e.ScrollOffset().Y != 0 {
		t.Fatalf("not scrolled to top: %v", r.e.ScrollOffset())
	}
	// Horizontal scrolling over long lines.
	r.e.SetCaret(r.e.Buffer().LineEnd(299))
	r.frame()
	if r.e.ScrollOffset().X <= 0 {
		t.Fatal("no horizontal scroll to caret")
	}
	// Dragging the vertical scrollbar thumb.
	g := r.e.geo
	if g.vthumb.Empty() {
		t.Fatal("no scrollbar thumb")
	}
	from := f32.Pt(float32(g.vthumb.Min.X+2), float32(g.vthumb.Min.Y+2))
	r.drag(from, f32.Pt(from.X, from.Y+200), 0)
	if r.e.ScrollOffset().Y <= 0 {
		t.Fatal("scrollbar drag did not scroll")
	}
}

func TestIMEComposition(t *testing.T) {
	r := newRig(t, "ab")
	r.focus()
	r.e.SetCaret(1)
	r.frame()
	st := r.h.Router.EditorState()
	if st.Snippet.Text != "ab" || st.Selection.Range != (key.Range{Start: 1, End: 1}) {
		t.Fatalf("IME state %+v", st)
	}
	// Compose "に" then commit "日".
	r.h.Router.Queue(key.EditEvent{Range: key.Range{Start: 1, End: 1}, Text: "に"},
		key.CompositionEvent{Start: 1, End: 2})
	r.frame()
	r.expectText("aにb")
	if a, b, ok := r.e.compositionRange(); !ok || a != 1 || b != 4 {
		t.Fatalf("composition %d..%d %v", a, b, ok)
	}
	r.h.Router.Queue(key.EditEvent{Range: key.Range{Start: 1, End: 2}, Text: "日"},
		key.CompositionEvent{Start: -1, End: -1})
	r.frame()
	r.expectText("a日b")
	if caret(r.e) != 4 {
		t.Fatalf("caret after commit %d", caret(r.e))
	}
	// Input method moves the selection.
	r.h.Router.Queue(key.SelectionEvent{Start: 0, End: 2})
	r.frame()
	if p := r.e.Cursor().Primary(); p.Head != 0 || p.Anchor != 4 {
		t.Fatalf("IME selection %+v", p)
	}
	r.h.Router.Queue(key.SnippetEvent{Start: 0, End: 3})
	r.frame()
}

func TestIMEWindowOnLongDocument(t *testing.T) {
	src := strings.Repeat("ä line\n", 1000)
	r := newRig(t, src)
	r.focus()
	off := r.e.Buffer().LineStart(500) + 2
	r.e.SetCaret(off)
	r.frame()
	st := r.h.Router.EditorState()
	if st.Snippet.Text != "ä line" || st.Selection.Start != 1 {
		t.Fatalf("snippet %q sel %+v", st.Snippet.Text, st.Selection.Range)
	}
	r.typ("ö")
	if got := string(r.e.Buffer().Line(500)); got != "äö line" {
		t.Fatalf("line 500 = %q", got)
	}
}

func TestFindNextAndSearchHighlight(t *testing.T) {
	r := newRig(t, "foo bar foo baz foo")
	q, err := search.Compile("foo", search.Options{})
	if err != nil {
		t.Fatal(err)
	}
	r.e.SetSearch(q)
	r.focus()
	r.press("Cmd+G")
	r.press("Cmd+G")
	if p := r.e.Cursor().Primary(); p.Start() != 8 {
		t.Fatalf("find next at %d", p.Start())
	}
	r.press("Cmd+Shift+G")
	if p := r.e.Cursor().Primary(); p.Start() != 0 {
		t.Fatalf("find previous at %d", p.Start())
	}
	if r.e.Search() != q {
		t.Fatal("Search()")
	}
	r.e.SetSearch(nil)
	r.press("Cmd+G")
	if evs := r.takeEvents(); len(evs) == 0 {
		t.Fatal("find next without query should ask the host")
	}
}

func TestFoldActions(t *testing.T) {
	src := "function f() {\n  a();\n  b();\n}\n"
	r := newRig(t, src)
	r.e.SetFoldRanges(highlight.BraceFolds(r.e.Buffer().Snapshot()))
	r.focus()
	r.e.SetCaret(r.e.Buffer().LineStart(1))
	r.press("Cmd+-")
	if !r.e.Folds().IsCollapsed(0) || caret(r.e) != r.e.Buffer().LineEnd(0) {
		t.Fatalf("collapse at caret: caret %d", caret(r.e))
	}
	r.press("Cmd+=")
	if r.e.Folds().IsCollapsed(0) {
		t.Fatal("expand")
	}
	r.e.SetCaret(r.e.Buffer().LineStart(2))
	r.press("Cmd+Shift+-")
	if !r.e.Folds().IsCollapsed(0) || caret(r.e) != r.e.Buffer().LineEnd(0) {
		t.Fatalf("collapse all: caret %d", caret(r.e))
	}
	r.press("Cmd+Shift+=")
	if r.e.Folds().IsCollapsed(0) {
		t.Fatal("expand all")
	}
}

func TestDiagnosticsFollowEdits(t *testing.T) {
	r := newRig(t, "$a = undefined();")
	r.e.SetDiagnostics([]Diagnostic{{Start: 5, End: 14, Severity: SeverityError, Message: "unknown"}})
	r.e.Buffer().Insert(0, []byte("  "))
	d := r.e.Diagnostics()
	if d[0].Start != 7 || d[0].End != 16 {
		t.Fatalf("diagnostic not shifted: %+v", d[0])
	}
	if len(r.e.DiagnosticsAt(10)) != 1 || len(r.e.DiagnosticsAt(1)) != 0 {
		t.Fatal("DiagnosticsAt")
	}
	r.frame()
}

func TestLineCacheFollowsEdits(t *testing.T) {
	r := newRig(t, "a\nb\nc\nd")
	if r.e.cache.Len() != 4 {
		t.Fatalf("cache %d", r.e.cache.Len())
	}
	entD := r.e.cache.m[3]
	r.e.Buffer().Insert(0, []byte("new\n"))
	if r.e.cache.m[4] != entD {
		t.Fatal("cache entry of line 3 did not move to line 4")
	}
	call := entD.call
	r.frame()
	if r.e.cache.m[4].call != call {
		t.Fatal("unchanged line was re-shaped")
	}
	r.e.Buffer().Insert(r.e.Buffer().LineStart(4), []byte("x"))
	if _, ok := r.e.cache.m[4]; ok {
		t.Fatal("edited line not dropped")
	}
}

func TestChordOf(t *testing.T) {
	cases := []struct {
		ev   key.Event
		want string
	}{
		{key.Event{Name: "Z", Modifiers: key.ModCommand | key.ModShift}, "Cmd+Shift+Z"},
		{key.Event{Name: "?", Modifiers: key.ModCommand | key.ModShift}, "Cmd+Shift+/"},
		{key.Event{Name: key.NameLeftArrow, Modifiers: key.ModAlt}, "Alt+Left"},
		{key.Event{Name: key.NameDeleteBackward, Modifiers: key.ModSuper}, "Cmd+Backspace"},
		{key.Event{Name: "G", Modifiers: key.ModCtrl}, "Ctrl+G"},
	}
	for _, c := range cases {
		if got := ChordOf(c.ev).String(); got != c.want {
			t.Errorf("ChordOf(%v) = %s, want %s", c.ev, got, c.want)
		}
	}
}

func TestRoleOrderMatchesTokens(t *testing.T) {
	for _, r := range highlight.Roles() {
		if got := tokens.SyntaxRole(r).String(); got != r.String() {
			t.Errorf("role %d: highlight %q vs tokens %q", r, r, got)
		}
	}
}

func TestMarkersAndPopupAnchor(t *testing.T) {
	r := newRig(t, "<?php\necho 1;\n")
	r.e.SetGutterMarkers(LineMarkers{
		0: {{Kind: MarkerBreakpoint}, {Kind: MarkerGitAdded}},
		1: {{Kind: MarkerBreakpointDisabled}, {Kind: MarkerGitModified}, {Kind: MarkerExecutionPoint}},
		2: {{Kind: MarkerBreakpointConditional}, {Kind: MarkerGitDeleted}, {Kind: MarkerIcon, Glyph: atoms.GlyphWarning, Tone: ToneWarning}},
	})
	r.e.SetGutterMarkers(MarkerFunc(func(l int) []Marker {
		return LineMarkers{0: {{Kind: MarkerBreakpoint}}}.Markers(l)
	}))
	r.frame()
	a, ok := r.e.AnchorRect(7)
	if !ok || a.Min.Y != r.e.LineHeight() || a.Dx() <= 0 {
		t.Fatalf("anchor %v %v", a, ok)
	}
	if off := r.e.OffsetAt(a.Min.Add(image.Pt(1, 1))); off != 7 {
		t.Fatalf("OffsetAt = %d", off)
	}
}

func TestLineComments(t *testing.T) {
	if LineComments["php"] != "//" || LineComments["yaml"] != "#" {
		t.Fatal("line comment table")
	}
	r := newRig(t, "a: 1")
	r.e.LineComment = LineComments["yaml"]
	r.focus()
	r.press("Cmd+/")
	r.expectText("# a: 1")
	r.e.LineComment = ""
	if r.e.Do(layout.Context{}, keymap.ToggleComment) {
		t.Fatal("toggle comment without prefix")
	}
	if r.e.Do(layout.Context{}, "unknown.action") {
		t.Fatal("unknown action")
	}
}

func TestSelectionsAcrossFoldsRender(t *testing.T) {
	r := newRig(t, "{\n a\n}\nz")
	r.e.SetFoldRanges([]fold.Range{{StartLine: 0, EndLine: 1}})
	r.e.Folds().Collapse(0)
	r.e.Cursor().SetSelections([]cursor.Selection{cursor.Range(0, r.e.Buffer().Len())}, 0)
	r.frame()
}
