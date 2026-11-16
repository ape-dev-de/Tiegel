package molecules

import (
	"image"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// ScrollList is a vertical, virtualized list (layout.List) with a slim
// scroll indicator drawn in token colors. It fills the available height
// when that is bounded.
type ScrollList struct {
	List layout.List
}

// ScrollTo scrolls so that item i is the first visible one.
func (s *ScrollList) ScrollTo(i int) { s.List.ScrollTo(i) }

// Layout draws n items produced by el.
func (s *ScrollList) Layout(gtx layout.Context, th *tokens.Theme, n int, el layout.ListElement) layout.Dimensions {
	s.List.Axis = layout.Vertical
	bounded := gtx.Constraints.Max.Y < draw.Unbounded
	d := s.List.Layout(gtx, n, el)
	if bounded {
		d.Size.Y = gtx.Constraints.Max.Y
	}
	d.Size.X = max(d.Size.X, gtx.Constraints.Min.X)
	if gtx.Constraints.Max.X < draw.Unbounded {
		d.Size.X = gtx.Constraints.Max.X
	}
	s.paintIndicator(gtx, th, d.Size)
	return d
}

// paintIndicator draws the thumb when the content is longer than the view.
func (s *ScrollList) paintIndicator(gtx layout.Context, th *tokens.Theme, view image.Point) {
	p := s.List.Position
	if p.Count <= 0 || p.Length <= view.Y || view.Y <= 0 {
		return
	}
	avg := float32(p.Length) / float32(p.Count)
	top := (float32(p.First)*avg + float32(p.Offset)) / float32(p.Length)
	frac := float32(view.Y) / float32(p.Length)
	h := max(int(frac*float32(view.Y)), gtx.Dp(th.Space.XL))
	y := min(int(top*float32(view.Y)), view.Y-h)
	w := gtx.Dp(th.Space.XS)
	x := view.X - w - gtx.Dp(th.Space.XXS)
	draw.RRect(gtx.Ops, image.Rect(x, y, x+w, y+h), w/2, tokens.WithAlpha(th.Color.TextMuted, 0x60))
}
