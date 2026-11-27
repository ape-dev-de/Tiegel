package molecules

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// TreeRow is one row of a generic tree (variables, structures, settings):
// indentation, expand arrow, optional glyph, a name, a monospace value, a
// muted detail at the right end (e.g. a type) and optional trailing
// actions.
type TreeRow struct {
	// count counts clicks for double clicks (atoms.DoubleClickInterval).
	count atoms.ClickCounter
	Name  string
	// Value is drawn in the code font after the name.
	Value string
	// Detail is drawn small and muted at the right end.
	Detail     string
	Depth      int
	Expandable bool
	Expanded   bool
	Selected   bool
	Glyph      *atoms.Glyph
	// GlyphColor, NameColor and ValueColor override the defaults when
	// non-zero.
	GlyphColor color.NRGBA
	NameColor  color.NRGBA
	ValueColor color.NRGBA
	// Trailing is drawn at the right end (e.g. icon buttons).
	Trailing layout.Widget

	row    widget.Clickable
	toggle widget.Clickable
}

// Update processes input. clicked reports a click on the row, toggled that
// the arrow was clicked or an expandable row was double-clicked (Expanded
// has already been flipped), double a double click on any row.
func (r *TreeRow) Update(gtx layout.Context) (clicked, toggled, double bool) {
	r.count.Update(gtx)
	for {
		if _, ok := r.toggle.Update(gtx); !ok {
			break
		}
		r.count.Next() // the arrow's press is no row click
		if r.Expandable {
			r.Expanded = !r.Expanded
			toggled = true
		}
	}
	for {
		_, ok := r.row.Update(gtx)
		if !ok {
			break
		}
		clicked = true
		if r.count.Next() >= 2 {
			double = true
			if r.Expandable {
				r.Expanded = !r.Expanded
				toggled = true
			}
		}
	}
	return clicked, toggled, double
}

// Hovered reports whether the pointer is over the row.
func (r *TreeRow) Hovered() bool { return r.row.Hovered() }

// Layout draws the row across the available width.
func (r *TreeRow) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return r.count.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return r.layoutRow(gtx, th) })
}

func (r *TreeRow) layoutRow(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	r.Update(gtx)
	h := gtx.Dp(th.Size.RowH)
	w := draw.Width(gtx, gtx.Dp(320))
	size := image.Pt(w, h)
	d := r.row.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		switch {
		case r.Selected:
			draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.Selection)
		case r.row.Hovered():
			draw.Rect(gtx.Ops, image.Rectangle{Max: size}, th.Color.SurfaceHover)
		}
		return layout.Dimensions{Size: size}
	})
	indent := gtx.Dp(th.Space.XS) + r.Depth*gtx.Dp(th.Size.IndentStep)
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, w-indent-gtx.Dp(th.Space.XS)), h)}
	m := op.Record(gtx.Ops)
	cd := layout.Flex{Alignment: layout.Middle}.Layout(cgtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return r.layoutArrow(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if r.Glyph == nil {
				return layout.Spacer{Width: th.Space.XXS}.Layout(gtx)
			}
			col := r.GlyphColor
			if col.A == 0 {
				col = th.Color.TextMuted
			}
			return layout.Inset{Left: th.Space.XXS, Right: th.Space.XS + th.Space.XXS}.Layout(gtx,
				atoms.Icon{Glyph: r.Glyph, Color: col, Size: th.Size.IconSmall + th.Space.XXS}.Fn(th))
		}),
		layout.Rigid(atoms.Label{Text: r.Name, Color: r.NameColor, MaxLines: 1}.Fn(th)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			w := gtx.Constraints.Max.X
			if r.Value == "" {
				return layout.Dimensions{Size: image.Pt(w, 0)}
			}
			d := layout.Inset{Left: th.Space.S}.Layout(gtx,
				atoms.Label{Text: r.Value, Role: atoms.RoleCode, Color: r.ValueColor, MaxLines: 1}.Fn(th))
			d.Size.X = w // fill, so detail and trailing sit at the right end
			return d
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if r.Detail == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(r.Detail).SingleLine().Fn(th))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if r.Trailing == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.XS}.Layout(gtx, r.Trailing)
		}),
	)
	call := m.Stop()
	t := op.Offset(image.Pt(indent, (h-cd.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return d
}

func (r *TreeRow) layoutArrow(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if !r.Expandable {
		return layout.Spacer{Width: th.Size.Icon}.Layout(gtx)
	}
	return r.toggle.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		g := atoms.GlyphChevronRight
		if r.Expanded {
			g = atoms.GlyphChevronDown
		}
		d := atoms.Icon{Glyph: g, Color: th.Color.TextMuted}.Layout(gtx, th)
		pointer.CursorPointer.Add(gtx.Ops)
		return d
	})
}
