// Package completion provides the editor popups: the completion list
// (List) and the information bubble used for hover text, diagnostics and
// signature help (Info). The host positions them (editorview.AnchorRect)
// and drives the keyboard (Up/Down/Enter/Escape); the list reports clicks.
package completion

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Item is a completion proposal.
type Item struct {
	Label  string
	Detail string
	// Kind is the LSP kind name ("method", "property", "class", …).
	Kind       string
	Deprecated bool
}

// Event is reported by List.Update.
type Event interface{ isCompletionEvent() }

// ChooseEvent: an item was clicked (Index into Items).
type ChooseEvent struct{ Index int }

func (ChooseEvent) isCompletionEvent() {}

// MaxRows is the number of rows visible without scrolling.
const MaxRows = 10

// Width is the popup width.
const Width = unit.Dp(460)

// List is the completion popup.
type List struct {
	Items []Item
	// Selected is the highlighted item.
	Selected int
	// Loading shows a placeholder while the server answers.
	Loading bool

	rows   []molecules.ListRow
	list   molecules.ScrollList
	lastSl int
}

// Update returns clicks on items.
func (l *List) Update(gtx layout.Context) []Event {
	for len(l.rows) < len(l.Items) {
		l.rows = append(l.rows, molecules.ListRow{})
	}
	var out []Event
	for i := range l.Items {
		if c, _ := l.rows[i].Update(gtx); c {
			l.Selected = i
			out = append(out, ChooseEvent{Index: i})
		}
	}
	return out
}

// Size returns the popup size for the current items.
func (l *List) Size(gtx layout.Context, th *tokens.Theme) image.Point {
	n := min(max(len(l.Items), 1), MaxRows)
	return image.Pt(gtx.Dp(Width), n*gtx.Dp(th.Size.RowH)+2*gtx.Dp(th.Size.BorderWidth))
}

// Layout draws the popup at its Size.
func (l *List) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	l.Update(gtx)
	size := l.Size(gtx, th)
	gtx.Constraints = layout.Exact(size)
	if l.Selected != l.lastSl {
		l.lastSl = l.Selected
		first, last := l.list.FirstVisible(), l.list.LastVisible()
		if l.Selected < first {
			l.list.ScrollTo(l.Selected)
		} else if l.Selected > last {
			l.list.ScrollTo(l.Selected - MaxRows + 1)
		}
	}
	return atoms.Surface{Kind: atoms.SurfacePanel, Border: true, Rounded: true}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		if len(l.Items) == 0 {
			text := "No suggestions"
			if l.Loading {
				text = "Loading…"
			}
			layout.UniformInset(th.Space.XS).Layout(gtx, atoms.Muted(text).Fn(th))
			return layout.Dimensions{Size: size}
		}
		return l.list.Layout(gtx, th, len(l.Items), func(gtx layout.Context, i int) layout.Dimensions {
			it := l.Items[i]
			r := &l.rows[i]
			r.Selected = i == l.Selected
			return r.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Right: th.Space.S}.Layout(gtx, atoms.Icon{Glyph: KindGlyph(it.Kind), Size: th.Size.IconSmall, Color: th.Color.Accent}.Fn(th))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						lb := atoms.Code(it.Label).SingleLine()
						if it.Deprecated {
							lb = lb.WithColor(th.Color.TextMuted)
						}
						return lb.Layout(gtx, th)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = 0
						if it.Detail == "" {
							return layout.Dimensions{}
						}
						return layout.Inset{Left: th.Space.M}.Layout(gtx, atoms.Small(it.Detail).SingleLine().Fn(th))
					}),
				)
			})
		})
	})
}

// KindGlyph returns the icon of an LSP completion kind.
func KindGlyph(kind string) *atoms.Glyph {
	switch kind {
	case "method", "function", "constructor":
		return atoms.GlyphRun
	case "class", "interface", "struct", "enum", "module":
		return atoms.GlyphFileCode
	case "variable", "field", "property":
		return atoms.GlyphTag
	case "constant", "enumMember", "value":
		return atoms.GlyphInfo
	case "keyword", "snippet":
		return atoms.GlyphEdit
	case "file", "folder":
		return atoms.GlyphFile
	}
	return atoms.GlyphMore
}

// Info is a popup with text (hover, diagnostics, signature help). An
// optional highlighted range [HighlightStart, HighlightEnd) of Text (byte
// offsets, e.g. the active parameter) is drawn emphasized.
type Info struct {
	Text                         string
	HighlightStart, HighlightEnd int
	// Mono draws code text.
	Mono bool
}

// MaxInfoWidth bounds the info popup width.
const MaxInfoWidth = unit.Dp(520)

// Layout draws the popup at its intrinsic size (bounded by MaxInfoWidth).
func (in Info) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	pad := gtx.Dp(th.Space.S)
	maxW := min(gtx.Dp(MaxInfoWidth), draw.Width(gtx, gtx.Dp(MaxInfoWidth)))
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, maxW-2*pad), gtx.Dp(300))}
	m := op.Record(gtx.Ops)
	var d layout.Dimensions
	if in.HighlightEnd > in.HighlightStart && in.HighlightEnd <= len(in.Text) {
		d = layout.Flex{Alignment: layout.Baseline}.Layout(cg,
			layout.Rigid(atoms.Code(in.Text[:in.HighlightStart]).Fn(th)),
			layout.Rigid(atoms.Label{Text: in.Text[in.HighlightStart:in.HighlightEnd], Role: atoms.RoleCode, Color: th.Color.Accent}.Fn(th)),
			layout.Rigid(atoms.Code(in.Text[in.HighlightEnd:]).Fn(th)),
		)
	} else {
		lb := atoms.Label{Text: in.Text, Role: atoms.RoleSmall, MaxLines: 16}
		if in.Mono {
			lb.Role = atoms.RoleCode
		}
		d = lb.Layout(cg, th)
	}
	call := m.Stop()
	size := image.Pt(d.Size.X+2*pad, d.Size.Y+2*pad)
	r := image.Rectangle{Max: size}
	draw.RRect(gtx.Ops, r, gtx.Dp(th.Radius.M), th.Color.Surface)
	draw.Border(gtx.Ops, r, gtx.Dp(th.Radius.M), gtx.Dp(th.Size.BorderWidth), th.Color.BorderStrong)
	t := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return layout.Dimensions{Size: size}
}

// Place returns the top-left position for a popup of size next to the
// anchor rectangle within bounds: below the anchor if it fits, otherwise
// above; shifted left to stay inside.
func Place(anchor image.Rectangle, size, bounds image.Point, above bool) image.Point {
	p := image.Pt(anchor.Min.X, anchor.Max.Y)
	if above || p.Y+size.Y > bounds.Y {
		if y := anchor.Min.Y - size.Y; y >= 0 || above {
			p.Y = max(0, y)
		}
	}
	if p.X+size.X > bounds.X {
		p.X = max(0, bounds.X-size.X)
	}
	return p
}
