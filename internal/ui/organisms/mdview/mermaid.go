package mdview

import (
	"image"
	"image/color"
	"math"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"golang.org/x/image/math/fixed"

	"github.com/ape-dev-de/tiegel/internal/mermaid"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Mermaid diagrams (D551): a code block with the language "mermaid" is
// drawn as a diagram. internal/mermaid parses and lays it out once per
// source and font (text measured with the Gio shaper, so boxes fit); the
// view scales it down to the column width and paints its paths and text
// with theme colors. A toggle switches to the source; Copy copies it.
// Sources that do not parse show the code with a muted error line.

// isMermaid reports whether a fence language selects a diagram.
func isMermaid(lang string) bool {
	switch strings.ToLower(lang) {
	case "mermaid", "mmd":
		return true
	}
	return false
}

// mermaidState is the render state of a diagram block.
type mermaidState struct {
	showSource bool
	toggle     atoms.Button

	key  mmKey
	diag *mermaid.Diagram
	err  error

	// Shaped text of the last scale.
	scale  float32
	glyphs [][][]text.Glyph // per item, per line (nil for path items)
	widths [][]fixed.Int26_6
}

type mmKey struct {
	code string
	sh   *text.Shaper
	ui   font.Font
	size float32
}

func newMermaidState() *mermaidState {
	return &mermaidState{toggle: atoms.Button{Kind: atoms.Ghost, Text: "Source"}}
}

// ensure lays the diagram out for the current fonts.
func (m *mermaidState) ensure(th *tokens.Theme, code string) {
	k := mmKey{code: code, sh: th.Shaper, ui: th.Font.UI, size: float32(th.Text.Body)}
	if m.diag != nil || m.err != nil {
		if m.key == k {
			return
		}
	}
	m.key, m.scale, m.glyphs = k, 0, nil
	sh := th.Shaper
	measure := func(s string, size float64, bold bool) float64 {
		if sh == nil {
			return mermaid.EstimateWidth(s, size, bold)
		}
		f := th.Font.UI
		if bold {
			f.Weight = font.Bold
		}
		var w fixed.Int26_6
		for _, g := range shapeFixed(sh, f, fixed.Int26_6(size*64), s) {
			w += g.Advance
		}
		return float64(w) / 64
	}
	m.diag, m.err = mermaid.Render(code, mermaid.Options{Measure: measure, FontSize: float64(th.Text.Body)})
}

func shapeFixed(sh *text.Shaper, f font.Font, px fixed.Int26_6, s string) []text.Glyph {
	if s == "" || sh == nil || px <= 0 {
		return nil
	}
	sh.LayoutString(text.Parameters{Font: f, PxPerEm: px, MaxWidth: 1 << 24, DisableSpaceTrim: true}, s)
	var gs []text.Glyph
	for {
		g, ok := sh.NextGlyph()
		if !ok {
			break
		}
		gs = append(gs, g)
	}
	return gs
}

// shapeText shapes every text line of the diagram at scale s (pixels per
// unit), cached until the scale changes.
func (m *mermaidState) shapeText(th *tokens.Theme, s float32) {
	if m.scale == s && m.glyphs != nil {
		return
	}
	m.scale = s
	m.glyphs = make([][][]text.Glyph, len(m.diag.Items))
	m.widths = make([][]fixed.Int26_6, len(m.diag.Items))
	for i, it := range m.diag.Items {
		t := it.Text
		if t == nil {
			continue
		}
		f := th.Font.UI
		if t.Bold {
			f.Weight = font.Bold
		}
		if t.Italic {
			f.Style = font.Italic
		}
		px := fixed.Int26_6(t.Size * float64(s) * 64)
		for _, l := range t.Lines {
			gs := shapeFixed(th.Shaper, f, px, l)
			var w fixed.Int26_6
			for _, g := range gs {
				w += g.Advance
			}
			m.glyphs[i] = append(m.glyphs[i], gs)
			m.widths[i] = append(m.widths[i], w)
		}
	}
}

// layoutMermaid draws a diagram block: a header with the language, the
// Source/Diagram toggle and Copy, then the diagram fitted to the width
// (or the source, or the source with the parse error).
func (v *View) layoutMermaid(gtx layout.Context, th *tokens.Theme, n *node, c ctx) layout.Dimensions {
	m := n.mm
	m.ensure(th, n.b.Code)
	w := gtx.Constraints.Max.X
	pad := gtx.Dp(th.Space.M)
	rad := gtx.Dp(th.Radius.M)
	bw := max(1, gtx.Dp(th.Size.BorderWidth))
	in := gtx.Dp(th.Space.XS)
	showDiagram := m.err == nil && !m.showSource
	if showDiagram {
		m.toggle.Text = "Source"
	} else {
		m.toggle.Text = "Diagram"
	}

	// Header: language left, toggle and copy right.
	bg := gtx
	bg.Constraints = layout.Constraints{Max: image.Pt(w, draw.Unbounded)}
	hm := op.Record(gtx.Ops)
	rec := op.Record(gtx.Ops)
	cd := n.copy.Layout(bg, th)
	copyCall := rec.Stop()
	hdrH := cd.Size.Y + 2*in
	x := w - in - cd.Size.X
	t := op.Offset(image.Pt(x, in)).Push(gtx.Ops)
	copyCall.Add(gtx.Ops)
	t.Pop()
	v.hotCopy = append(v.hotCopy, n)
	if m.err == nil {
		rec := op.Record(gtx.Ops)
		td := m.toggle.Layout(bg, th)
		tc := rec.Stop()
		x -= td.Size.X + in
		t := op.Offset(image.Pt(x, in)).Push(gtx.Ops)
		tc.Add(gtx.Ops)
		t.Pop()
		v.hotMermaid = append(v.hotMermaid, n)
	}
	{
		rec := op.Record(gtx.Ops)
		ld := atoms.Small(n.b.Lang).SingleLine().Layout(bg, th)
		lbl := rec.Stop()
		t := op.Offset(image.Pt(pad, (hdrH-ld.Size.Y)/2)).Push(gtx.Ops)
		lbl.Add(gtx.Ops)
		t.Pop()
	}
	hdr := hm.Stop()

	var body op.CallOp
	bodyH := 0
	bm := op.Record(gtx.Ops)
	if showDiagram {
		d := m.diag
		avail := float32(max(1, w-2*pad))
		s := gtx.Metric.PxPerDp
		if dw := float32(d.Width) * s; dw > avail {
			s = avail / float32(d.Width)
		}
		s = float32(math.Round(float64(s)*100) / 100)
		dw := int(math.Ceil(float64(d.Width) * float64(s)))
		bodyH = int(math.Ceil(float64(d.Height) * float64(s)))
		ox := pad + max(0, (w-2*pad-dw)/2)
		t := op.Offset(image.Pt(ox, 0)).Push(gtx.Ops)
		m.paint(gtx, th, s)
		t.Pop()
		bodyH += pad
	} else {
		tw := max(1, w-2*pad)
		col := th.Editor.Text
		if c.muted {
			col = th.Color.TextMuted
		}
		d := n.text.layout(gtx, th, v, tw, col, text.Start)
		bodyH = d.Size.Y
		if m.err != nil {
			y := bodyH + gtx.Dp(th.Space.S)
			t := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
			lg := gtx
			lg.Constraints = layout.Constraints{Max: image.Pt(tw, draw.Unbounded)}
			ed := draw.Text(lg, th.Shaper, draw.TextOpts{Font: th.Font.UI, Size: th.Text.Small, Color: th.Color.TextMuted},
				"Diagram not shown – "+m.err.Error())
			t.Pop()
			bodyH = y + ed.Size.Y
		}
		bodyH += pad
	}
	body = bm.Stop()

	h := hdrH + bodyH
	box := image.Rect(0, 0, w, h)
	draw.RRect(gtx.Ops, box, rad, th.Color.Surface)
	draw.Border(gtx.Ops, box, rad, bw, th.Color.Border)
	hdr.Add(gtx.Ops)
	bx := 0
	if !showDiagram {
		bx = pad
	}
	t = op.Offset(image.Pt(bx, hdrH)).Push(gtx.Ops)
	body.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: image.Pt(w, h), Baseline: h - hdrH}
}

// paint draws the diagram at scale s (pixels per unit) at the origin.
func (m *mermaidState) paint(gtx layout.Context, th *tokens.Theme, s float32) {
	d := m.diag
	m.shapeText(th, s)
	pal := mermaidPalette(th)
	for i, it := range d.Items {
		if t := it.Text; t != nil {
			col := pal.color(t.Paint)
			for li := range t.Lines {
				gs := m.glyphs[i][li]
				if len(gs) == 0 {
					continue
				}
				wd := m.widths[i][li]
				x := fixed.Int26_6(t.X * float64(s) * 64)
				switch t.Anchor {
				case mermaid.AnchorMiddle:
					x -= wd / 2
				case mermaid.AnchorEnd:
					x -= wd
				}
				base := int(math.Round((t.Y + float64(li)*t.LineHeight) * float64(s)))
				drawGlyphs(gtx, th.Shaper, gs, x, base, col)
			}
			continue
		}
		if len(it.Path) == 0 {
			continue
		}
		if it.Fill.Role != mermaid.RoleNone || it.Fill.Color != "" {
			if col := pal.color(it.Fill); col.A > 0 {
				ps := buildPath(gtx.Ops, it.Path, s)
				paint.FillShape(gtx.Ops, col, clip.Outline{Path: ps}.Op())
			}
		}
		if it.Stroke.Role != mermaid.RoleNone || it.Stroke.Color != "" {
			col := pal.color(it.Stroke)
			if col.A == 0 {
				continue
			}
			w := it.Width
			if w == 0 {
				w = 1
			}
			segs := it.Path
			if on, off := mermaid.DashPattern(it.Dash, w); on > 0 {
				segs = mermaid.Dashes(segs, on, off)
			}
			ps := buildPath(gtx.Ops, segs, s)
			paint.FillShape(gtx.Ops, col, clip.Stroke{Path: ps, Width: float32(w) * s}.Op())
		}
	}
}

func buildPath(ops *op.Ops, segs []mermaid.Seg, s float32) clip.PathSpec {
	pt := func(p mermaid.Point) f32.Point { return f32.Pt(float32(p.X)*s, float32(p.Y)*s) }
	var p clip.Path
	p.Begin(ops)
	for _, sg := range segs {
		switch sg.Op {
		case mermaid.Move:
			p.MoveTo(pt(sg.Pts[0]))
		case mermaid.Line:
			p.LineTo(pt(sg.Pts[0]))
		case mermaid.Quad:
			p.QuadTo(pt(sg.Pts[0]), pt(sg.Pts[1]))
		case mermaid.Cube:
			p.CubeTo(pt(sg.Pts[0]), pt(sg.Pts[1]), pt(sg.Pts[2]))
		case mermaid.Close:
			p.Close()
		}
	}
	return p.End()
}

// palette maps diagram paint roles to theme colors.
type palette struct {
	roles [mermaid.RolePie + 1]color.NRGBA
	pie   []color.NRGBA
}

func mermaidPalette(th *tokens.Theme) *palette {
	c := th.Color
	bg := c.Surface
	p := &palette{}
	p.roles[mermaid.RoleNodeFill] = tokens.Mix(bg, c.Accent, 0.13)
	p.roles[mermaid.RoleNodeStroke] = tokens.Mix(c.Accent, c.Text, 0.15)
	p.roles[mermaid.RoleClusterFill] = tokens.Mix(bg, c.Text, 0.04)
	p.roles[mermaid.RoleClusterStroke] = c.BorderStrong
	p.roles[mermaid.RoleLine] = tokens.Mix(c.TextMuted, c.Text, 0.3)
	p.roles[mermaid.RoleLabelBg] = bg
	p.roles[mermaid.RoleText] = c.Text
	p.roles[mermaid.RoleTextMuted] = c.TextMuted
	p.roles[mermaid.RoleNoteFill] = tokens.Mix(bg, c.Warning, 0.18)
	p.roles[mermaid.RoleNoteStroke] = tokens.Mix(c.Warning, bg, 0.2)
	p.roles[mermaid.RoleActivation] = tokens.Mix(bg, c.Text, 0.1)
	p.roles[mermaid.RoleBackground] = bg
	p.roles[mermaid.RoleAccent] = c.Accent
	p.roles[mermaid.RoleOnAccent] = c.OnAccent
	p.pie = []color.NRGBA{c.Accent, c.Warning, c.Success, c.Danger, c.Info,
		th.SyntaxColor(tokens.RoleKeyword), th.SyntaxColor(tokens.RoleString), th.SyntaxColor(tokens.RoleType),
		th.SyntaxColor(tokens.RoleFunction), th.SyntaxColor(tokens.RoleConstant)}
	return p
}

func (p *palette) color(pt mermaid.Paint) color.NRGBA {
	if pt.Color != "" {
		if r, g, b, a, ok := mermaid.RGBA(pt.Color); ok {
			return color.NRGBA{R: r, G: g, B: b, A: a}
		}
	}
	if pt.Role == mermaid.RolePie {
		return p.pie[pt.Index%len(p.pie)]
	}
	if int(pt.Role) < len(p.roles) {
		return p.roles[pt.Role]
	}
	return color.NRGBA{}
}
