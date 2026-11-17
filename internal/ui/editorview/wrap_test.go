package editorview

import (
	"image"
	"strings"
	"testing"

	"gioui.org/f32"

	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
)

// longLine is about 4.5 wrap widths of words at the default rig width.
var longLine = strings.Repeat("alpha beta gamma delta ", 20)

// wrapRig opens text with soft wrap on, focused.
func wrapRig(t *testing.T, text string) *rig {
	t.Helper()
	r := newRig(t, text)
	r.e.SoftWrap = true
	r.frame()
	r.focus()
	if !r.e.Wrapped() {
		t.Fatal("soft wrap not active")
	}
	return r
}

func TestSoftWrapRows(t *testing.T) {
	r := wrapRig(t, "short\n"+longLine+"\nend")
	s := r.e.Buffer().Snapshot()
	rows := r.e.wrap.Rows(1)
	if rows < 3 {
		t.Fatalf("long line has %d rows", rows)
	}
	if r.e.geo.rows != rows+2 || r.e.geo.maxScroll.X != 0 {
		t.Fatalf("geometry rows %d maxScrollX %d", r.e.geo.rows, r.e.geo.maxScroll.X)
	}
	// The visible rows cover the long line without gaps; only its first
	// row has sub 0, only its last is marked last.
	var segs []visLine
	for _, v := range r.e.vis {
		if v.line == 1 {
			segs = append(segs, v)
		}
	}
	if len(segs) != rows {
		t.Fatalf("visible segments %d, want %d", len(segs), rows)
	}
	pos := s.LineStart(1)
	for i, v := range segs {
		if v.start != pos || v.sub != i || v.last != (i == rows-1) {
			t.Fatalf("segment %d: %+v (want start %d)", i, v, pos)
		}
		if w := e2col(r, v); w > r.e.wrap.Width() {
			t.Fatalf("segment %d is %d cells, wrap width %d", i, w, r.e.wrap.Width())
		}
		pos = v.end
	}
	if pos != s.LineEnd(1) {
		t.Fatalf("segments end at %d, line ends at %d", pos, s.LineEnd(1))
	}
	if r.e.FirstVisibleLine() != 0 || r.e.LineTop(2) != (rows+1)*r.e.LineHeight() {
		t.Fatalf("LineTop(2) = %d", r.e.LineTop(2))
	}
}

func e2col(r *rig, v visLine) int { return r.e.colOf(v, v.end) }

func TestSoftWrapCaretMovement(t *testing.T) {
	r := wrapRig(t, longLine+"\nnext")
	s := r.e.Buffer().Snapshot()
	r.e.SetCaret(3)
	r.frame()
	r.press("Down")
	head := r.e.Cursor().Primary().Head
	row, col := r.e.view.RowOf(s, head)
	if row != 1 || col != 3 || head >= s.LineEnd(0) {
		t.Fatalf("Down moved to %d (row %d col %d)", head, row, col)
	}
	r.press("Up")
	if got := r.e.Cursor().Primary().Head; got != 3 {
		t.Fatalf("Up came back to %d", got)
	}
	// End on the second row goes to its break (after its last cluster,
	// drawn at the row end), the second End goes to the line end; Home
	// goes back to the row start.
	r.press("Down")
	start, end, last := r.e.view.RowBounds(s, r.e.Cursor().Primary().Head)
	if last {
		t.Fatal("second row is the last")
	}
	r.press("Cmd+Right") // End on mac
	if got := r.e.Cursor().Primary(); got.Head != end || !got.RowEnd {
		t.Fatalf("End = %+v, want %d at the row end", got, end)
	}
	r.frame()
	if row, _ := r.e.caretRowCol(s, r.e.Cursor().Primary()); row != 1 {
		t.Fatalf("caret at the row end drawn on row %d", row)
	}
	if i := visIndex(r.e.vis, end, true); i < 0 || r.e.vis[i].row != 1 {
		t.Fatalf("caret at the row end painted in visible row %d", i)
	}
	r.press("Cmd+Right")
	if got := r.e.Cursor().Primary().Head; got != s.LineEnd(0) {
		t.Fatalf("second End = %d, want line end %d", got, s.LineEnd(0))
	}
	r.press("Cmd+Left") // Home: start of the last row
	h := r.e.Cursor().Primary().Head
	if st, _, _ := r.e.view.RowBounds(s, s.LineEnd(0)); h != st {
		t.Fatalf("Home = %d, want row start %d", h, st)
	}
	r.e.SetCaret(start + 2)
	r.frame()
	r.press("Cmd+Left")
	if got := r.e.Cursor().Primary().Head; got != start {
		t.Fatalf("Home = %d, want %d", got, start)
	}
	r.press("Cmd+Left")
	if got := r.e.Cursor().Primary().Head; got != 0 {
		t.Fatalf("second Home = %d, want 0", got)
	}
	// Clone caret below adds a caret on the next visual row.
	r.e.SetCaret(1)
	r.frame()
	r.e.Cursor().AddCursorVertical(1)
	sels := r.e.Cursor().Selections()
	if len(sels) != 2 {
		t.Fatalf("carets %v", sels)
	}
	if row, _ := r.e.view.RowOf(s, sels[1].Head); row != 1 {
		t.Fatalf("cloned caret on row %d", row)
	}
	r.frame() // paints carets on wrapped rows
}

func TestSoftWrapClick(t *testing.T) {
	r := wrapRig(t, longLine+"\nnext")
	s := r.e.Buffer().Snapshot()
	start, _, _ := r.e.view.RowBounds(s, r.e.wrap.Width()+5)
	target := start + 4
	r.click(r.pointAt(target), 0)
	if got := r.e.Cursor().Primary().Head; got != target {
		t.Fatalf("click on row 2 put the caret at %d, want %d", got, target)
	}
	// A click right of a continued row puts the caret at its end.
	p := r.pointAt(start)
	r.click(f32.Pt(float32(r.e.geo.size.X-20), p.Y-float32(r.e.LineHeight())), 0)
	if got := r.e.Cursor().Primary(); got.Head != start || !got.RowEnd {
		t.Fatalf("click past row 1 = %+v, want %d at the row end", got, start)
	}
	// Gutter clicks report the document line of any of its rows.
	r.takeEvents()
	r.click(f32.Pt(float32(r.e.geo.iconX+2), p.Y), 0)
	found := false
	for _, ev := range r.takeEvents() {
		if g, ok := ev.(GutterClickEvent); ok && g.Line == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("no gutter click on line 0")
	}
}

func TestSoftWrapSelectionAndDiagnostics(t *testing.T) {
	r := wrapRig(t, longLine+"\nnext")
	r.e.SetDiagnostics([]Diagnostic{{Start: 10, End: 200, Severity: SeverityError}})
	r.e.Cursor().SetSelections([]cursor.Selection{cursor.Range(5, 150)}, 0)
	var rowsDrawn []int
	r.frame()
	r.e.rangeCols(r.e.vis, 5, 150, func(v visLine, c0, c1 int) { rowsDrawn = append(rowsDrawn, v.row) })
	if len(rowsDrawn) < 2 {
		t.Fatalf("selection drawn on rows %v", rowsDrawn)
	}
	// A range starting exactly at a soft break is not drawn on the row
	// before it.
	s := r.e.Buffer().Snapshot()
	_, brk, _ := r.e.view.RowBounds(s, 0)
	rowsDrawn = rowsDrawn[:0]
	r.e.rangeCols(r.e.vis, brk, brk+3, func(v visLine, c0, c1 int) { rowsDrawn = append(rowsDrawn, v.row) })
	if len(rowsDrawn) != 1 || rowsDrawn[0] != 1 {
		t.Fatalf("range at break drawn on rows %v", rowsDrawn)
	}
}

func TestSoftWrapFolds(t *testing.T) {
	text := "{\n" + longLine + "\n}\n" + longLine
	r := wrapRig(t, text)
	r.e.SetFoldRanges([]fold.Range{{StartLine: 0, EndLine: 2}})
	r.frame()
	before := r.e.geo.rows
	r.e.Folds().Collapse(0)
	r.frame()
	hidden := r.e.wrap.Rows(1) + 1
	if r.e.geo.rows != before-hidden {
		t.Fatalf("rows %d after fold, want %d", r.e.geo.rows, before-hidden)
	}
	if l := r.e.rowLine(1); l != 3 {
		t.Fatalf("row 1 shows line %d", l)
	}
	// The fold arrow of the header toggles it back.
	r.click(f32.Pt(float32(r.e.geo.foldX+2), float32(r.e.LineHeight()/2)), 0)
	if r.e.Folds().IsCollapsed(0) || r.e.geo.rows != before {
		t.Fatalf("fold click: collapsed %v rows %d", r.e.Folds().IsCollapsed(0), r.e.geo.rows)
	}
}

func TestSoftWrapResizeKeepsTopLine(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(longLine)
		b.WriteString("\n")
	}
	r := wrapRig(t, b.String())
	top := r.e.lineRow(100) * r.e.LineHeight()
	r.e.SetScrollOffset(image.Pt(0, top))
	r.frame()
	if r.e.FirstVisibleLine() != 100 {
		t.Fatalf("first line %d", r.e.FirstVisibleLine())
	}
	width := r.e.wrap.Width()
	r.h.Max = image.Pt(500, 600)
	r.frame()
	if r.e.wrap.Width() >= width {
		t.Fatalf("wrap width %d did not shrink from %d", r.e.wrap.Width(), width)
	}
	if r.e.FirstVisibleLine() != 100 || r.e.ScrollOffset().Y%r.e.LineHeight() != 0 {
		t.Fatalf("after resize first line %d, scroll %d", r.e.FirstVisibleLine(), r.e.ScrollOffset().Y)
	}
	// Turning wrap off keeps the line at the top as well.
	r.e.SoftWrap = false
	r.frame()
	if r.e.Wrapped() || r.e.FirstVisibleLine() != 100 || r.e.Cursor().Rows != nil {
		t.Fatalf("wrap off: first line %d", r.e.FirstVisibleLine())
	}
	r.e.SoftWrap = true
	r.frame()
	if r.e.FirstVisibleLine() != 100 {
		t.Fatalf("wrap on again: first line %d", r.e.FirstVisibleLine())
	}
}

func TestSoftWrapTypingUpdatesRows(t *testing.T) {
	r := wrapRig(t, "x")
	if r.e.geo.rows != 1 {
		t.Fatalf("rows %d", r.e.geo.rows)
	}
	r.e.SetCaret(1)
	r.frame()
	r.typ(strings.Repeat("word ", 40))
	if r.e.geo.rows < 2 {
		t.Fatalf("rows %d after typing a long line", r.e.geo.rows)
	}
	// The caret stays visible at the end of the wrapped line.
	if _, ok := r.e.AnchorRect(r.e.Cursor().Primary().Head); !ok {
		t.Fatal("caret not visible")
	}
	r.press("Enter")
	r.typ("next")
	if r.e.wrap.LineCount() != 2 || !r.e.wrap.Valid(r.e.wrap.Width(), 4) {
		t.Fatal("wrap model out of sync")
	}
}

func TestWhitespaceModes(t *testing.T) {
	if ParseWhitespaceMode("all") != WhitespaceAll || ParseWhitespaceMode("trailing") != WhitespaceTrailing ||
		ParseWhitespaceMode("selection") != WhitespaceSelection || ParseWhitespaceMode("x") != WhitespaceNone {
		t.Fatal("ParseWhitespaceMode")
	}
	r := newRig(t, "\tfoo bar  \nbaz")
	ent := func() *lineEntry { return r.e.cache.m[lineKey{}] }
	if ent().ws != -1 {
		t.Fatalf("none: ws %d", ent().ws)
	}
	r.e.Whitespace = WhitespaceAll
	r.frame()
	if ent().ws != 0 {
		t.Fatalf("all: ws %d", ent().ws)
	}
	r.e.Whitespace = WhitespaceTrailing
	r.frame()
	if ent().ws != 8 || r.e.cache.m[lineKey{line: 1}].ws != -1 {
		t.Fatalf("trailing: ws %d", ent().ws)
	}
	r.e.Whitespace = WhitespaceSelection
	r.e.Cursor().SetSelections([]cursor.Selection{cursor.Range(0, 9)}, 0)
	r.frame()
	if ent().ws != -1 {
		t.Fatalf("selection: cached ws %d", ent().ws)
	}
	// Wrapped trailing whitespace: only the rows holding it are marked.
	r2 := wrapRig(t, longLine+"   ")
	r2.e.Whitespace = WhitespaceTrailing
	r2.frame()
	marked := 0
	for k, e := range r2.e.cache.m {
		if k.line == 0 && e.ws >= 0 {
			marked++
		}
	}
	if marked != 1 {
		t.Fatalf("trailing marks on %d rows", marked)
	}
}

func TestRightMargin(t *testing.T) {
	r := newRig(t, "abc")
	r.e.RightMargin = 10
	r.frame()
	r.e.RightMargin = 100000 // outside the view: not drawn, no panic
	r.frame()
}
