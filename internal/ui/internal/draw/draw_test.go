package draw

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
)

func TestHelpersDoNotPanic(t *testing.T) {
	var ops op.Ops
	c := color.NRGBA{R: 1, A: 255}
	r := image.Rect(0, 0, 20, 10)
	RRect(&ops, r, 100, c)
	RRect(&ops, image.Rectangle{}, 2, c)
	RRect(&ops, r, 2, color.NRGBA{})
	Border(&ops, r, 3, 1, c)
	Border(&ops, image.Rect(0, 0, 1, 1), 3, 4, c)
	Border(&ops, r, 3, 0, c)
	Rect(&ops, r, c)
	Rect(&ops, image.Rectangle{}, c)
	Circle(&ops, 5, 5, 3, c)
	Circle(&ops, 5, 5, 0, c)
	Ring(&ops, 5, 5, 3, 1, c)
	Ring(&ops, 5, 5, 0, 1, c)
	m := op.Record(&ops)
	Centered(&ops, image.Pt(10, 10), image.Pt(4, 4), m.Stop())
}

func TestClampRadius(t *testing.T) {
	if got := clampRadius(image.Rect(0, 0, 20, 10), 100); got != 5 {
		t.Errorf("clamp = %d", got)
	}
	if got := clampRadius(image.Rect(0, 0, 20, 10), -1); got != 0 {
		t.Errorf("negative = %d", got)
	}
}

func TestWidth(t *testing.T) {
	gtx := layout.Context{Constraints: layout.Constraints{Max: image.Pt(300, 10)}}
	if Width(gtx, 50) != 300 {
		t.Error("bounded")
	}
	gtx.Constraints.Max.X = Unbounded
	if Width(gtx, 50) != 50 {
		t.Error("unbounded fallback")
	}
	gtx.Constraints.Min.X = 80
	if Width(gtx, 50) != 80 {
		t.Error("unbounded respects min")
	}
}

func TestMeasure(t *testing.T) {
	var ops op.Ops
	gtx := layout.Context{Ops: &ops}
	d := Measure(gtx, func(gtx layout.Context) layout.Dimensions {
		Rect(gtx.Ops, image.Rect(0, 0, 5, 5), color.NRGBA{A: 255})
		return layout.Dimensions{Size: image.Pt(5, 5)}
	})
	if d.Size != image.Pt(5, 5) {
		t.Fatal(d.Size)
	}
}
