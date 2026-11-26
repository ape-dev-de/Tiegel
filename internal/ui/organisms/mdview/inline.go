package mdview

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// LayoutInline draws the whole document for embedding in another list
// (e.g. a chat message): no scrolling, background or outer padding, the
// width of the constraints, the UI text size instead of the larger reader
// size, tighter block spacing. The height is the content height. Links
// and code block copy buttons work as in Layout (Update reports them).
func (v *View) LayoutInline(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.Update(gtx)
	v.hotLinks, v.hotCopy = v.hotLinks[:0], v.hotCopy[:0]
	cth := *th
	cth.Text.Large = th.Text.Body
	w := draw.Width(gtx, gtx.Dp(320))
	v.key = shapeKey{
		sh: th.Shaper, ui: th.Font.UI, mono: th.Font.Mono, body: cth.Text.Large, code: th.Text.Code,
		pxPerSp: gtx.Metric.PxPerSp, pxDp: gtx.Metric.PxPerDp, imgGen: v.imgGen, hasImageLoader: v.LoadImage != nil,
	}
	y := 0
	for i, n := range v.nodes {
		if i > 0 {
			y += gtx.Dp(th.Space.S)
		}
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(w, draw.Unbounded)}
		t := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
		d := v.layoutNode(cg, &cth, n, ctx{})
		t.Pop()
		y += d.Size.Y
	}
	for _, l := range v.hotLinks {
		for _, h := range l.hits[:len(l.rects)] {
			for {
				if _, ok := h.Update(gtx.Source); !ok {
					break
				}
			}
		}
	}
	return layout.Dimensions{Size: image.Pt(w, y)}
}
