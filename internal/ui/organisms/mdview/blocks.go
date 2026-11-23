package mdview

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// node is the render state of a block: its rich texts, children and the
// copy button of a code block. Nodes survive SetDoc for unchanged blocks.
type node struct {
	b        markdown.Block
	text     *richText     // paragraph, heading, code, HTML
	cells    [][]*richText // table
	children []*node
	copy     atoms.Button
}

func buildNode(b *markdown.Block) *node {
	n := &node{b: *b}
	switch b.Kind {
	case markdown.Paragraph:
		n.text = newRich(b.Spans, styleBody)
	case markdown.Heading:
		n.text = newRich(b.Spans, headingStyle(b.Level))
	case markdown.CodeBlock:
		n.text = newRich(codeSpans(b.Code), styleCode)
		n.copy = atoms.Button{Kind: atoms.Ghost, Text: "Copy", Glyph: atoms.GlyphCopy}
	case markdown.HTML:
		n.text = newRich(codeSpans(b.Code), styleCode)
	case markdown.Table:
		n.cells = make([][]*richText, len(b.Rows))
		for i, row := range b.Rows {
			st := styleCell
			if i == 0 {
				st = styleHeader
			}
			n.cells[i] = make([]*richText, len(row))
			for j, c := range row {
				n.cells[i][j] = newRich(c.Spans, st)
			}
		}
	}
	n.children = make([]*node, len(b.Children))
	for i := range b.Children {
		n.children[i] = buildNode(&b.Children[i])
	}
	return n
}

// rebind points a reused node at the block of the new document (same
// content, possibly other lines).
func (n *node) rebind(b *markdown.Block) {
	n.b = *b
	for i, c := range n.children {
		c.rebind(&b.Children[i])
	}
}

// ctx is the inherited rendering context of nested blocks.
type ctx struct {
	muted bool // inside a block quote
	depth int  // list nesting depth
}

func (c ctx) color(th *tokens.Theme) color.NRGBA {
	if c.muted {
		return th.Color.TextMuted
	}
	return th.Color.Text
}

// layoutNode draws n at the origin within gtx.Constraints.Max.X. The
// returned Baseline is that of the first text line (from the bottom).
func (v *View) layoutNode(gtx layout.Context, th *tokens.Theme, n *node, c ctx) layout.Dimensions {
	w := gtx.Constraints.Max.X
	switch n.b.Kind {
	case markdown.Paragraph:
		return n.text.layout(gtx, th, v, w, c.color(th), text.Start)
	case markdown.Heading:
		return v.layoutHeading(gtx, th, n, c)
	case markdown.CodeBlock, markdown.HTML:
		return v.layoutCode(gtx, th, n, c)
	case markdown.Quote:
		return v.layoutQuote(gtx, th, n, c)
	case markdown.List:
		return v.layoutList(gtx, th, n, c)
	case markdown.ListItem:
		return v.stack(gtx, th, n.children, c, true)
	case markdown.Table:
		return v.layoutTable(gtx, th, n, c)
	case markdown.Rule:
		m := gtx.Dp(th.Space.XS)
		h := max(1, gtx.Dp(th.Space.XXS))
		draw.Rect(gtx.Ops, image.Rect(0, m, w, m+h), th.Color.Border)
		return layout.Dimensions{Size: image.Pt(w, 2*m+h), Baseline: m + h}
	}
	// Unknown kinds: render their children, if any.
	return v.stack(gtx, th, n.children, c, false)
}

func (v *View) layoutHeading(gtx layout.Context, th *tokens.Theme, n *node, c ctx) layout.Dimensions {
	w := gtx.Constraints.Max.X
	col := c.color(th)
	if n.b.Level >= 6 {
		col = th.Color.TextMuted
	}
	d := n.text.layout(gtx, th, v, w, col, text.Start)
	if n.b.Level > 2 {
		return d
	}
	y := d.Size.Y + gtx.Dp(th.Space.XS)
	h := max(1, gtx.Dp(th.Size.BorderWidth))
	draw.Rect(gtx.Ops, image.Rect(0, y, w, y+h), th.Color.Border)
	size := image.Pt(w, y+h)
	return layout.Dimensions{Size: size, Baseline: size.Y - n.text.firstBaseline()}
}

// layoutCode draws a code block (header with language and copy button,
// wrapped monospace text on a surface) or a raw HTML block (muted source
// in an outlined box).
func (v *View) layoutCode(gtx layout.Context, th *tokens.Theme, n *node, c ctx) layout.Dimensions {
	w := gtx.Constraints.Max.X
	pad := gtx.Dp(th.Space.M)
	rad := gtx.Dp(th.Radius.M)
	bw := max(1, gtx.Dp(th.Size.BorderWidth))
	isCode := n.b.Kind == markdown.CodeBlock
	tw := max(1, w-2*pad)
	ts := n.text.measure(gtx, th, v, tw)

	// Header (code blocks only): language left, copy button right.
	var hdr op.CallOp
	hdrH := 0
	if isCode {
		m := op.Record(gtx.Ops)
		in := gtx.Dp(th.Space.XS)
		bg := gtx
		bg.Constraints = layout.Constraints{Max: image.Pt(w, draw.Unbounded)}
		rec := op.Record(gtx.Ops)
		bd := n.copy.Layout(bg, th)
		btn := rec.Stop()
		hdrH = bd.Size.Y + 2*in
		if n.b.Lang != "" {
			rec := op.Record(gtx.Ops)
			ld := atoms.Small(n.b.Lang).SingleLine().Layout(bg, th)
			lbl := rec.Stop()
			t := op.Offset(image.Pt(pad, (hdrH-ld.Size.Y)/2)).Push(gtx.Ops)
			lbl.Add(gtx.Ops)
			t.Pop()
		}
		t := op.Offset(image.Pt(max(0, w-in-bd.Size.X), in)).Push(gtx.Ops)
		btn.Add(gtx.Ops)
		t.Pop()
		hdr = m.Stop()
		v.hotCopy = append(v.hotCopy, n)
	}
	top := hdrH
	if !isCode {
		top = pad
	}
	h := top + ts.Y + pad
	box := image.Rect(0, 0, w, h)
	col := th.Editor.Text
	if isCode {
		draw.RRect(gtx.Ops, box, rad, th.Color.Surface)
		draw.Border(gtx.Ops, box, rad, bw, th.Color.Border)
		hdr.Add(gtx.Ops)
	} else {
		draw.Border(gtx.Ops, box, rad, bw, th.Color.Border)
		col = th.Color.TextMuted
	}
	if c.muted {
		col = th.Color.TextMuted
	}
	t := op.Offset(image.Pt(pad, top)).Push(gtx.Ops)
	n.text.layout(gtx, th, v, tw, col, text.Start)
	t.Pop()
	return layout.Dimensions{Size: image.Pt(w, h), Baseline: h - top - n.text.firstBaseline()}
}

func (v *View) layoutQuote(gtx layout.Context, th *tokens.Theme, n *node, c ctx) layout.Dimensions {
	w := gtx.Constraints.Max.X
	bar := gtx.Dp(th.Space.XS)
	padL := gtx.Dp(th.Space.L)
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(1, w-padL), draw.Unbounded)}
	c.muted = true
	t := op.Offset(image.Pt(padL, 0)).Push(gtx.Ops)
	d := v.stack(cg, th, n.children, c, false)
	t.Pop()
	h := max(d.Size.Y, gtx.Sp(readerSize(th)))
	draw.Rect(gtx.Ops, image.Rect(0, 0, bar, h), th.Color.BorderStrong)
	return layout.Dimensions{Size: image.Pt(w, h), Baseline: d.Baseline + h - d.Size.Y}
}

// stack lays out blocks vertically with block spacing (tight: list item
// spacing).
func (v *View) stack(gtx layout.Context, th *tokens.Theme, nodes []*node, c ctx, tight bool) layout.Dimensions {
	w := gtx.Constraints.Max.X
	y, fb := 0, -1
	for i, ch := range nodes {
		if i > 0 {
			y += v.gap(gtx, th, ch, tight)
		}
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(w, draw.Unbounded)}
		t := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
		d := v.layoutNode(cg, th, ch, c)
		t.Pop()
		if fb < 0 {
			fb = y + d.Size.Y - d.Baseline
		}
		y += d.Size.Y
	}
	fb = max(fb, 0)
	return layout.Dimensions{Size: image.Pt(w, y), Baseline: y - fb}
}

// layoutList draws list items with a marker column: bullets by depth,
// numbers from Start, or task checkboxes.
func (v *View) layoutList(gtx layout.Context, th *tokens.Theme, n *node, c ctx) layout.Dimensions {
	w := gtx.Constraints.Max.X
	px := gtx.Sp(readerSize(th))
	markW := gtx.Dp(th.Space.XL)
	start := n.b.Start
	if n.b.Ordered {
		widest := fmt.Sprintf("%d.", start+max(0, len(n.children)-1))
		widest = strings.Repeat("0", len(widest)-1) + "."
		d := draw.Measure(gtx, func(gtx layout.Context) layout.Dimensions {
			return v.markerLabel(gtx, th, widest, c)
		})
		markW = max(markW, d.Size.X+gtx.Dp(th.Space.S))
	}
	tight := n.b.Tight
	inner := c
	inner.depth++
	y, fb := 0, -1
	for i, item := range n.children {
		if i > 0 {
			if tight {
				y += gtx.Dp(th.Space.XS)
			} else {
				y += gtx.Dp(th.Space.S)
			}
		}
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(max(1, w-markW), draw.Unbounded)}
		t := op.Offset(image.Pt(markW, y)).Push(gtx.Ops)
		d := v.stack(cg, th, item.children, inner, tight)
		t.Pop()
		ib := d.Size.Y - d.Baseline // first baseline within the item
		ih := d.Size.Y
		if len(item.children) == 0 {
			ih = px * 3 / 2
			ib = px
		}
		t = op.Offset(image.Pt(0, y)).Push(gtx.Ops)
		mh := v.marker(gtx, th, n, item, i, markW, ib, c)
		t.Pop()
		if fb < 0 {
			fb = y + ib
		}
		y += max(ih, mh)
	}
	fb = max(fb, 0)
	return layout.Dimensions{Size: image.Pt(w, y), Baseline: y - fb}
}

// marker draws the marker of item i in a column of width markW aligned to
// the item's first baseline and returns its height.
func (v *View) marker(gtx layout.Context, th *tokens.Theme, list, item *node, i, markW, base int, c ctx) int {
	px := gtx.Sp(readerSize(th))
	right := markW - gtx.Dp(th.Space.S)
	col := c.color(th)
	switch {
	case item.b.Task:
		sz := gtx.Dp(th.Size.Checkbox)
		r := image.Rect(right-sz, base-sz+sz/8, right, base+sz/8)
		rad := gtx.Dp(th.Radius.S)
		if item.b.Checked {
			draw.RRect(gtx.Ops, r, rad, th.Color.Accent)
			t := op.Offset(r.Min).Push(gtx.Ops)
			atoms.Icon{Glyph: atoms.GlyphCheck, Size: th.Size.Checkbox, Color: th.Color.OnAccent}.Layout(gtx, th)
			t.Pop()
		} else {
			draw.RRect(gtx.Ops, r, rad, th.Color.Surface)
			draw.Border(gtx.Ops, r, rad, max(1, gtx.Dp(th.Size.BorderWidth)), th.Color.BorderStrong)
		}
		return r.Max.Y
	case list.b.Ordered:
		s := fmt.Sprintf("%d.", list.b.Start+i)
		m := op.Record(gtx.Ops)
		lg := gtx
		lg.Constraints.Min = image.Point{}
		d := v.markerLabel(lg, th, s, c)
		call := m.Stop()
		fb := d.Size.Y - d.Baseline
		t := op.Offset(image.Pt(right-d.Size.X, base-fb)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		return base - fb + d.Size.Y
	}
	r := max(2, px*9/50)
	cx, cy := right-r, base-px*8/25
	switch c.depth % 3 {
	case 0:
		draw.Circle(gtx.Ops, cx, cy, r, col)
	case 1:
		draw.Ring(gtx.Ops, cx, cy, r, max(1, r/2), col)
	default:
		draw.Rect(gtx.Ops, image.Rect(cx-r+1, cy-r+1, cx+r-1, cy+r-1), col)
	}
	return cy + r
}

func (v *View) markerLabel(gtx layout.Context, th *tokens.Theme, s string, c ctx) layout.Dimensions {
	return draw.Text(gtx, th.Shaper, draw.TextOpts{Font: th.Font.UI, Size: readerSize(th), Color: c.color(th), MaxLines: 1}, s)
}
