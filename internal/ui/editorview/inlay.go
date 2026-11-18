package editorview

import (
	"image"
	"slices"
	"sort"

	"gioui.org/op"
	"github.com/rivo/uniseg"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// InlayHint is virtual text (a parameter name, an inferred type) shown
// before the character at document offset Offset. Hints take whole
// columns: the text after them moves right, the document is unchanged.
// They are drawn only while lines are not soft-wrapped.
type InlayHint struct {
	Offset   int
	Label    string
	PadLeft  bool
	PadRight bool
}

// lineInlay is a hint of one line: at is the line-relative byte offset,
// cols the columns it takes (label plus padding).
type lineInlay struct {
	at    int
	cols  int
	label string
	padL  bool
}

// SetInlayHints replaces the inlay hints (nil: none).
func (e *Editor) SetInlayHints(h []InlayHint) {
	e.inlays = slices.Clone(h)
	slices.SortStableFunc(e.inlays, func(a, b InlayHint) int { return a.Offset - b.Offset })
}

// InlayHints returns the current hints (offsets moved with edits).
func (e *Editor) InlayHints() []InlayHint { return slices.Clone(e.inlays) }

// shiftInlays moves the hints across an edit; hints inside the replaced
// text are dropped.
func (e *Editor) shiftInlays(ed buffer.Edit) {
	out := e.inlays[:0]
	for _, h := range e.inlays {
		if h.Offset > ed.StartByte && h.Offset < ed.OldEndByte {
			continue
		}
		h.Offset = mapOffset(h.Offset, ed)
		out = append(out, h)
	}
	e.inlays = out
}

// labelCols is the column width of a hint label.
func labelCols(s string) int {
	return max(1, uniseg.StringWidth(s))
}

// lineInlays returns the hints of the line starting at ls with content
// line (nil while wrapping or for very long lines).
func (e *Editor) lineInlays(ls int, line []byte) []lineInlay {
	if e.wrapped || len(e.inlays) == 0 || len(line) > maxShapeBytes {
		return nil
	}
	i := sort.Search(len(e.inlays), func(i int) bool { return e.inlays[i].Offset >= ls })
	var out []lineInlay
	for ; i < len(e.inlays) && e.inlays[i].Offset <= ls+len(line); i++ {
		h := e.inlays[i]
		c := labelCols(h.Label)
		if h.PadLeft {
			c++
		}
		if h.PadRight {
			c++
		}
		out = append(out, lineInlay{at: h.Offset - ls, cols: c, label: h.Label, padL: h.PadLeft})
	}
	return out
}

// inlayShift returns the columns taken by the hints at or before byte b.
func inlayShift(ins []lineInlay, b int) int {
	n := 0
	for _, in := range ins {
		if in.at > b {
			break
		}
		n += in.cols
	}
	return n
}

// inlayCols is the total width of the hints.
func inlayCols(ins []lineInlay) int { return inlayShift(ins, int(^uint(0)>>1)) }

// inlayEqual reports whether two hint lists draw the same.
func inlayEqual(a, b []lineInlay) bool { return slices.Equal(a, b) }

// unshiftCol maps a display column of a line with hints to the visual
// column of the text (a column inside a hint snaps to its position).
func unshiftCol(ins []lineInlay, line []byte, col, tab int) int {
	shift := 0
	for _, in := range ins {
		start := buffer.VisualCol(line, min(in.at, len(line)), tab) + shift
		if col <= start {
			break
		}
		if col < start+in.cols {
			return start - shift
		}
		shift += in.cols
	}
	return col - shift
}

// paintInlays draws the hint pills of a line into ops (column 0 at x 0).
func (e *Editor) paintInlays(ops *op.Ops, th *tokens.Theme, line []byte, ins []lineInlay) {
	tab := e.tabWidth()
	shift := 0
	for _, in := range ins {
		col := buffer.VisualCol(line, min(in.at, len(line)), tab) + shift
		shift += in.cols
		if in.padL {
			col++
		}
		w := labelCols(in.label)
		x0, x1 := e.met.colX(col), e.met.colX(col+w)
		r := image.Rect(x0, 2, x1, e.met.lineH-2)
		draw.RRect(ops, r, max(2, e.met.lineH/6), tokens.WithAlpha(th.Color.SurfaceAlt, 0xd0))
		e.paintGlyphs(ops, th, []byte(in.label), 0, col, nil, nil, th.Color.TextMuted)
	}
}

// visInlays returns the hints of a drawn row.
func (e *Editor) visInlays(v visLine) []lineInlay {
	if e.wrapped || len(e.inlays) == 0 {
		return nil
	}
	return e.lineInlays(v.start, e.lineBytes(v))
}
