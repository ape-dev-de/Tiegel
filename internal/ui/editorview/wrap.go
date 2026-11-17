package editorview

import (
	"image"
	"image/color"
	"sort"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"golang.org/x/image/math/fixed"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// This file holds the soft wrap glue between the widget and wrap.View,
// the whitespace marks and the right margin ruler (M12.3).

// WhitespaceMode selects which whitespace is drawn as marks: dots for
// spaces, arrows for tabs.
type WhitespaceMode uint8

// Whitespace modes (values of the editor.showWhitespace setting).
const (
	WhitespaceNone WhitespaceMode = iota
	// WhitespaceSelection marks whitespace inside selections.
	WhitespaceSelection
	// WhitespaceTrailing marks whitespace at the end of lines.
	WhitespaceTrailing
	// WhitespaceAll marks every space and tab.
	WhitespaceAll
)

// ParseWhitespaceMode maps a setting value (none, selection, trailing,
// all) to a mode; unknown values mean none.
func ParseWhitespaceMode(s string) WhitespaceMode {
	switch s {
	case "selection":
		return WhitespaceSelection
	case "trailing":
		return WhitespaceTrailing
	case "all":
		return WhitespaceAll
	}
	return WhitespaceNone
}

// Wrapped reports whether the last Layout soft-wrapped the lines.
func (e *Editor) Wrapped() bool { return e.wrapped }

// updateWrap switches soft wrap on or off for this frame and re-wraps
// the document when the width or tab width changed, keeping the text at
// the top of the view in place. textW is the width of the text area.
func (e *Editor) updateWrap(textW int) {
	lineH := max(1, e.met.lineH)
	if !e.SoftWrap {
		if e.wrapped {
			// Keep the first visible line at the top.
			l, _ := e.view.LineAt(e.scroll.Y / lineH)
			e.scroll.Y = e.folds.DocToVisible(l)*lineH + e.scroll.Y%lineH
		}
		e.wrapped = false
		e.wrap.Invalidate()
		e.cur.Rows = nil
		return
	}
	cols := wrapColumns(textW, e.met.cellW)
	s := e.buf.Snapshot()
	if !e.wrap.Valid(cols, e.tabWidth()) || e.wrap.LineCount() != s.LineCount() {
		// Anchor: the document offset at the top row.
		anchor, rem := -1, e.scroll.Y%lineH
		switch {
		case e.wrapped && e.wrap.LineCount() == s.LineCount():
			anchor = e.view.OffsetAt(s, e.scroll.Y/lineH, 0)
		case !e.wrapped:
			anchor = s.LineStart(e.folds.VisibleToDoc(e.scroll.Y / lineH))
		}
		e.wrap.Reset(s, cols, e.tabWidth())
		if anchor >= 0 {
			row, _ := e.view.RowOf(s, anchor)
			e.scroll.Y = row*lineH + rem
		}
	}
	e.wrapped = true
	e.scroll.X = 0
	e.cur.Rows = &e.view
}

// wrapColumns returns the wrap width in cells for a text area of textW
// pixels: the visible columns minus one, so the caret after the last
// cluster stays visible.
func wrapColumns(textW int, cellW fixed.Int26_6) int {
	if cellW <= 0 {
		return 80
	}
	return int(fixed.I(textW)/cellW) - 1
}

// rowCount returns the number of visual rows.
func (e *Editor) rowCount() int {
	if e.wrapped {
		return e.view.RowCount()
	}
	return e.folds.VisibleCount()
}

// lineRow returns the (first) visual row of a document line; hidden
// lines map to their fold header.
func (e *Editor) lineRow(line int) int {
	if e.wrapped {
		return e.view.LineRow(line)
	}
	return e.folds.DocToVisible(line)
}

// rowLine returns the document line drawn in visual row row.
func (e *Editor) rowLine(row int) int {
	if e.wrapped {
		l, _ := e.view.LineAt(row)
		return l
	}
	return e.folds.VisibleToDoc(row)
}

// rowCol returns the visual row and the visual column within it of a
// document offset.
func (e *Editor) rowCol(s buffer.Snapshot, off int) (row, col int) {
	if e.wrapped {
		return e.view.RowOf(s, off)
	}
	pos := s.OffsetToPos(off)
	return e.folds.DocToVisible(pos.Line), buffer.VisualCol(s.Line(pos.Line), pos.Col, e.tabWidth())
}

// visIndex returns the index of the visible row showing the caret at
// off, or -1. An offset at a soft break belongs to the following row.
func visIndex(vis []visLine, off int) int {
	i := sort.Search(len(vis), func(i int) bool { return vis[i].end >= off })
	if i < len(vis) && !vis[i].last && vis[i].end == off && vis[i].end > vis[i].start {
		i++
	}
	if i >= len(vis) || vis[i].start > off {
		return -1
	}
	return i
}

// trailingStart returns the byte offset in line where trailing
// whitespace begins (len(line) if there is none).
func trailingStart(line []byte) int {
	i := len(line)
	for i > 0 && (line[i-1] == ' ' || line[i-1] == '\t') {
		i--
	}
	return i
}

// wsFrom returns the segment-relative byte offset from which the cached
// drawing of v marks whitespace, or -1 for none.
func (e *Editor) wsFrom(v visLine) int {
	switch e.Whitespace {
	case WhitespaceAll:
		return 0
	case WhitespaceTrailing:
		if t := v.ls + v.trail; t < v.end {
			return max(0, t-v.start)
		}
	}
	return -1
}

// paintWhitespace marks the spaces and tabs of seg[b0:] (seg starts at
// visual column 0) in a row whose top left corner is at (x0, y).
func (e *Editor) paintWhitespace(ops *op.Ops, c color.NRGBA, seg []byte, b0, x0, y int) {
	tab := e.tabWidth()
	lineH := e.met.lineH
	cy := y + lineH/2
	dot := max(1, lineH/12)
	col := 0
	for i := 0; i < len(seg); {
		n, w := 1, 1
		switch ch := seg[i]; {
		case ch == '\t':
			w = (col/tab+1)*tab - col
			if i >= b0 {
				e.paintTabArrow(ops, c, x0+e.met.colX(col), x0+e.met.colX(col+w), cy)
			}
		case ch == ' ':
			if i >= b0 {
				cx := x0 + (e.met.colX(col)+e.met.colX(col+1))/2
				draw.Rect(ops, image.Rect(cx-dot/2, cy-dot/2, cx-dot/2+dot, cy-dot/2+dot), c)
			}
		case ch >= 0x80:
			n = nextClusterLen(seg[i:])
			w = buffer.VisualCol(seg[i:i+n], n, tab)
		case ch < 0x20 || ch == 0x7f:
			w = 0
		}
		col += w
		i += n
	}
}

// paintTabArrow draws a thin arrow from x0 to x1 at height cy.
func (e *Editor) paintTabArrow(ops *op.Ops, c color.NRGBA, x0, x1, cy int) {
	cw := e.met.colX(1)
	pad := max(1, cw/5)
	a, b := x0+pad, x1-pad
	if b-a < 2 {
		return
	}
	h := max(2, e.met.lineH/7)
	draw.Rect(ops, image.Rect(a, cy, b, cy+1), c)
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(f32.Pt(float32(b), float32(cy)+0.5))
	p.LineTo(f32.Pt(float32(b-h), float32(cy-h)+0.5))
	p.LineTo(f32.Pt(float32(b-h), float32(cy+h)+0.5))
	p.Close()
	paint.FillShape(ops, c, clip.Outline{Path: p.End()}.Op())
}

// paintSelectionWhitespace marks whitespace inside the selections
// (WhitespaceSelection; drawn live, not cached).
func (e *Editor) paintSelectionWhitespace(ops *op.Ops, th *tokens.Theme, vis []visLine, sels []cursor.Selection, ox int) {
	if e.Whitespace != WhitespaceSelection || len(vis) == 0 {
		return
	}
	lo, hi := vis[0].start, vis[len(vis)-1].end
	i := sort.Search(len(sels), func(i int) bool { return sels[i].End() >= lo })
	for ; i < len(sels) && sels[i].Start() <= hi; i++ {
		sel := sels[i]
		if sel.Empty() {
			continue
		}
		for _, v := range vis {
			if v.end < sel.Start() || v.start >= sel.End() {
				continue
			}
			seg := e.lineBytes(v)
			b0, b1 := max(0, sel.Start()-v.start), min(len(seg), sel.End()-v.start)
			if b1 > b0 {
				e.paintWhitespace(ops, th.Editor.Whitespace, seg[:b1], b0, ox, v.y)
			}
		}
	}
}

// paintRuler draws the right margin ruler in the text area.
func (e *Editor) paintRuler(gtx layout.Context, th *tokens.Theme, ox int) {
	if e.RightMargin <= 0 {
		return
	}
	g := &e.geo
	x := ox + e.met.colX(e.RightMargin)
	if x < g.gutterW || x >= g.size.X {
		return
	}
	draw.Rect(gtx.Ops, image.Rect(x, 0, x+max(1, gtx.Dp(1)), g.size.Y), th.Editor.Ruler)
}
