package editorview

import (
	"image"
	"image/color"
	"slices"
	"sort"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// DecorationKind selects the colors of a LineDecoration.
type DecorationKind uint8

// Decoration kinds (the change kinds of a diff).
const (
	DecorationAdded DecorationKind = iota
	DecorationModified
	DecorationDeleted
)

// InlineRange is a byte range [Start, End) within document line Line
// (columns without the line terminator), e.g. a changed word.
type InlineRange struct {
	Line, Start, End int
}

// LineDecoration tints the document lines [StartLine, EndLine) across the
// text area and the gutter, e.g. a change block of a diff. An empty range
// (StartLine == EndLine) draws a separator line at the top of StartLine
// instead: the place where the other side of a diff has lines. Inline
// ranges inside the block get a stronger tint.
type LineDecoration struct {
	StartLine, EndLine int
	Kind               DecorationKind
	Inline             []InlineRange
}

// SetLineDecorations replaces the line decorations. They follow later
// line insertions and deletions until the next call (whole lines shift;
// the host recomputes them, e.g. after a new diff).
func (e *Editor) SetLineDecorations(d []LineDecoration) {
	e.decos = slices.Clone(d)
	for i := range e.decos {
		e.decos[i].Inline = slices.Clone(e.decos[i].Inline)
	}
	slices.SortStableFunc(e.decos, func(a, b LineDecoration) int { return a.StartLine - b.StartLine })
}

// LineDecorations returns the current decorations.
func (e *Editor) LineDecorations() []LineDecoration { return slices.Clone(e.decos) }

// shiftDecorations moves decorations below an edit that added or removed
// lines.
func (e *Editor) shiftDecorations(ed buffer.Edit) {
	delta := ed.NewEndPoint.Row - ed.OldEndPoint.Row
	if delta == 0 || len(e.decos) == 0 {
		return
	}
	pivot := ed.StartPoint.Row
	move := func(l int) int {
		switch {
		case l <= pivot:
			return l
		case l > ed.OldEndPoint.Row:
			return l + delta
		}
		// Inside the replaced lines.
		return min(l, ed.NewEndPoint.Row)
	}
	for i := range e.decos {
		d := &e.decos[i]
		d.StartLine, d.EndLine = move(d.StartLine), move(d.EndLine)
		for j := range d.Inline {
			d.Inline[j].Line = move(d.Inline[j].Line)
		}
	}
}

// decorationAt returns the decoration covering line (non-empty ranges
// only).
func (e *Editor) decorationAt(line int) (*LineDecoration, bool) {
	i := sort.Search(len(e.decos), func(i int) bool { return e.decos[i].StartLine > line })
	// Decorations do not overlap: the last non-empty one starting at or
	// before line is the only candidate.
	for i--; i >= 0; i-- {
		if d := &e.decos[i]; d.StartLine < d.EndLine {
			return d, line < d.EndLine
		}
	}
	return nil, false
}

// decorationColors returns the line and inline background of a kind.
func decorationColors(th *tokens.Theme, k DecorationKind) (line, inline, sep color.NRGBA) {
	bg := th.Editor.Background
	switch k {
	case DecorationAdded:
		return th.Editor.DiffAddedBg, tokens.Over(tokens.WithAlpha(th.Git.Added, 0x60), th.Editor.DiffAddedBg), th.Git.Added
	case DecorationDeleted:
		return th.Editor.DiffRemovedBg, tokens.Over(tokens.WithAlpha(th.Git.Deleted, 0x60), th.Editor.DiffRemovedBg), th.Git.Deleted
	}
	lc := tokens.Over(tokens.WithAlpha(th.Git.Modified, 0x2C), bg)
	return lc, tokens.Over(tokens.WithAlpha(th.Git.Modified, 0x60), lc), th.Git.Modified
}

// DecorationColors returns the line background, the inline background and
// the separator color the editor uses for a decoration kind, so
// surrounding widgets (e.g. the bands of a diff window) can match them.
func DecorationColors(th *tokens.Theme, k DecorationKind) (line, inline, separator color.NRGBA) {
	return decorationColors(th, k)
}

// paintDecorations paints line tints and inline ranges of the visible
// rows (text area, called inside its clip) and reports the decorated
// lines.
func (e *Editor) paintDecorations(ops *op.Ops, th *tokens.Theme, vis []visLine, ox int) map[int]bool {
	if len(e.decos) == 0 || len(vis) == 0 {
		return nil
	}
	g := &e.geo
	out := map[int]bool{}
	for _, v := range vis {
		d, ok := e.decorationAt(v.line)
		if !ok {
			continue
		}
		out[v.line] = true
		lc, ic, _ := decorationColors(th, d.Kind)
		draw.Rect(ops, image.Rect(g.gutterW, v.y, g.size.X, v.y+g.lineH), lc)
		for _, r := range d.Inline {
			if r.Line != v.line {
				continue
			}
			n := v.end - v.start
			s, en := min(max(r.Start, 0), n), min(max(r.End, 0), n)
			if en <= s {
				continue
			}
			c0, c1 := e.colOf(v, v.start+s), e.colOf(v, v.start+en)
			draw.RRect(ops, image.Rect(ox+e.met.colX(c0), v.y+1, ox+e.met.colX(c1), v.y+g.lineH-1), 2, ic)
		}
	}
	return out
}

// paintSeparators draws the separators of empty decorations on top of the
// text area.
func (e *Editor) paintSeparators(gtx layout.Context, th *tokens.Theme) {
	if len(e.decos) == 0 {
		return
	}
	g := &e.geo
	h := max(1, gtx.Dp(2))
	for _, d := range e.decos {
		if d.StartLine != d.EndLine {
			continue
		}
		y := e.LineTop(d.StartLine)
		if y < -h || y > g.size.Y+h {
			continue
		}
		_, _, sc := decorationColors(th, d.Kind)
		draw.Rect(gtx.Ops, image.Rect(0, y-h/2, g.size.X, y-h/2+h), sc)
	}
}

// LineTop returns the y coordinate of the top of document line line in
// the editor's coordinate space (as of the last Layout; may be outside
// the view). Lines past the end map below the last line; folded lines map
// to the top of their fold.
func (e *Editor) LineTop(line int) int {
	g := &e.geo
	n := e.buf.LineCount()
	var row int
	switch {
	case line <= 0:
		row = 0
	case line >= n:
		row = e.folds.DocToVisible(n-1) + 1 + (line - n)
	default:
		row = e.folds.DocToVisible(line)
	}
	return row*g.lineH - e.scroll.Y
}

// ViewportHeight returns the height of the editor in pixels as of the
// last Layout.
func (e *Editor) ViewportHeight() int { return e.geo.size.Y }

// MaxScrollY returns the largest vertical scroll offset as of the last
// Layout.
func (e *Editor) MaxScrollY() int { return e.geo.maxScroll.Y }

// GutterColumn is an extra column at the left edge of the gutter, e.g.
// annotations (author and date per line) or the old and new line numbers
// of a unified diff. Clicks into it are reported as GutterClickEvent with
// Area GutterAnnotation.
type GutterColumn interface {
	// GutterWidth returns the column width in pixels for this frame.
	GutterWidth(gtx layout.Context, th *tokens.Theme) int
	// LayoutGutterLine draws the column cell of document line line. The
	// constraints are exact (column width × line height), the origin is
	// the cell's top left corner. active marks lines with a caret.
	LayoutGutterLine(gtx layout.Context, th *tokens.Theme, line int, active bool)
}

// SetGutterColumn installs an extra gutter column; nil removes it.
func (e *Editor) SetGutterColumn(c GutterColumn) { e.annot = c }

func (e *Editor) paintGutterColumn(gtx layout.Context, th *tokens.Theme, vis []visLine, caretLines map[int]bool) {
	g := &e.geo
	if e.annot == nil || g.annotW <= 0 {
		return
	}
	for _, v := range vis {
		t := op.Offset(image.Pt(0, v.y)).Push(gtx.Ops)
		cg := gtx
		cg.Constraints = layout.Exact(image.Pt(g.annotW, g.lineH))
		e.annot.LayoutGutterLine(cg, th, v.line, caretLines[v.line] && e.focused)
		t.Pop()
	}
}

// paintDecorationMarks marks the decorations on the vertical scrollbar
// track (an overview of all changes).
func (e *Editor) paintDecorationMarks(gtx layout.Context, th *tokens.Theme) {
	g := &e.geo
	if len(e.decos) == 0 || g.rows <= 0 || g.size.Y <= 0 {
		return
	}
	mh := max(2, gtx.Dp(2))
	x0, x1 := g.vbar.Min.X+gtx.Dp(1), g.vbar.Min.X+gtx.Dp(4)
	n := e.buf.LineCount()
	ypos := func(line int) int {
		row := g.rows
		if line < n {
			row = e.folds.DocToVisible(max(0, line))
		}
		return row * (g.size.Y - mh) / max(1, g.rows)
	}
	for _, d := range e.decos {
		_, _, c := decorationColors(th, d.Kind)
		y0 := ypos(d.StartLine)
		y1 := max(y0+mh, ypos(d.EndLine))
		draw.Rect(gtx.Ops, image.Rect(x0, y0, x1, y1), c)
	}
}
