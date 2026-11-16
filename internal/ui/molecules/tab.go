package molecules

import (
	"image"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// tabMaxWidth bounds a tab's width; longer titles are truncated.
const tabMaxWidth = unit.Dp(240)

// Tab is an editor tab: optional icon, title, modified dot and close
// button. The active tab shares the editor background and carries an accent
// underline.
type Tab struct {
	Title    string
	Glyph    *atoms.Glyph
	Active   bool
	Modified bool
	// Tooltip is shown when hovering the title (e.g. the full path).
	Tooltip string

	click widget.Clickable
	close atoms.IconButton
	tip   atoms.Tooltip
}

// NewTab returns a tab with title.
func NewTab(title string) *Tab { return &Tab{Title: title} }

// Update processes input: selected reports a click on the tab, closed a
// click on its close button. Layout calls Update as well; call it first
// to observe the events.
func (t *Tab) Update(gtx layout.Context) (selected, closed bool) {
	if t.close.Clicked(gtx) {
		closed = true
	}
	for {
		if _, ok := t.click.Update(gtx); !ok {
			break
		}
		selected = true
	}
	return selected, closed
}

// Layout draws the tab at its intrinsic width (bounded by tabMaxWidth).
func (t *Tab) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	t.Update(gtx)
	t.close.Glyph = atoms.GlyphClose
	t.close.Description = "Close tab"
	t.close.Size = th.Size.Icon + th.Space.XS
	t.close.IconSize = th.Size.IconSmall
	t.tip.Text = t.Tooltip

	return t.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(th.Size.TabH)
		padL, padR := gtx.Dp(th.Space.M), gtx.Dp(th.Space.XS)
		hovered := t.click.Hovered()

		cgtx := gtx
		cgtx.Constraints = layout.Constraints{Max: image.Pt(min(gtx.Constraints.Max.X, gtx.Dp(tabMaxWidth))-padL-padR, h)}
		titleColor := th.Color.TextMuted
		if t.Active || hovered {
			titleColor = th.Color.Text
		}
		m := op.Record(gtx.Ops)
		d := layout.Flex{Alignment: layout.Middle}.Layout(cgtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if t.Glyph == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Right: th.Space.S - th.Space.XXS}.Layout(gtx,
					atoms.Icon{Glyph: t.Glyph, Color: th.Color.TextMuted}.Fn(th))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = 0
				return t.tip.Layout(gtx, th, atoms.Label{Text: t.Title, Color: titleColor, MaxLines: 1}.Fn(th))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// Modified dot.
				sz := gtx.Dp(th.Space.M)
				if t.Modified {
					r := gtx.Dp(3)
					draw.Circle(gtx.Ops, sz/2+gtx.Dp(th.Space.XXS), sz/2, r, th.Color.Accent)
				}
				return layout.Dimensions{Size: image.Pt(sz, sz)}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// The close button is always laid out so tabs do not change
				// width on hover; it is invisible on inactive, unhovered tabs.
				if !t.Active && !hovered && !t.close.Hovered() {
					m := op.Record(gtx.Ops)
					d := t.close.Layout(gtx, th)
					m.Stop()
					return d
				}
				return t.close.Layout(gtx, th)
			}),
		)
		call := m.Stop()

		size := image.Pt(d.Size.X+padL+padR, h)
		r := image.Rectangle{Max: size}
		switch {
		case t.Active:
			draw.Rect(gtx.Ops, r, th.Editor.Background)
			draw.Rect(gtx.Ops, image.Rect(0, h-gtx.Dp(2), size.X, h), th.Color.Accent)
		case hovered:
			draw.Rect(gtx.Ops, r, th.Color.SurfaceHover)
		default:
			draw.Rect(gtx.Ops, r, th.Color.SurfaceAlt)
		}
		// Right separator.
		draw.Rect(gtx.Ops, image.Rect(size.X-1, gtx.Dp(th.Space.S), size.X, h-gtx.Dp(th.Space.S)), th.Color.Border)

		tr := op.Offset(image.Pt(padL, (h-d.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		tr.Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		return layout.Dimensions{Size: size}
	})
}
