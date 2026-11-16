package molecules_test

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestChangeBands(t *testing.T) {
	th := tokens.Dark(nil)
	fill := tokens.Hex(0x3060C0)
	cb := molecules.ChangeBands{Bands: []molecules.Band{
		{LeftTop: 10, LeftBottom: 40, RightTop: 60, RightBottom: 120, Fill: fill, Edge: th.Git.Modified, Emphasis: true},
		{LeftTop: 150, LeftBottom: 150, RightTop: 140, RightBottom: 170, Fill: th.Editor.DiffAddedBg, Edge: th.Git.Added},
		{LeftTop: -500, LeftBottom: -400, RightTop: -300, RightBottom: -200, Fill: fill},
		{LeftTop: 900, LeftBottom: 950, RightTop: 900, RightBottom: 990, Fill: fill},
	}}
	exact := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(40, 200))
		return cb.Layout(gtx, th)
	}
	d := uitest.Smoke(t, "bands", image.Pt(40, 200), exact)
	if d.Size != image.Pt(40, 200) {
		t.Fatalf("size %v", d.Size)
	}
	img, err := uitest.Render(th, image.Pt(40, 200), exact)
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	// Inside the first band (middle of the strip, between both ranges).
	if c := img.RGBAAt(20, 55); c.B < c.R+40 {
		t.Errorf("band fill not drawn: %v", c)
	}
	// Outside every band: the gutter background.
	want := th.Editor.Gutter
	if c := img.RGBAAt(20, 190); c.R != want.R || c.G != want.G || c.B != want.B {
		t.Errorf("background %v, want %v", c, want)
	}
}

func TestAnnotationCell(t *testing.T) {
	th := tokens.Light(nil)
	cell := func(a molecules.AnnotationCell) uitest.Widget {
		return func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(200, 20))
			return a.Layout(gtx, th)
		}
	}
	for _, a := range []molecules.AnnotationCell{
		{Text: "2026-01-01  Someone", Bar: th.Color.Accent, Tint: tokens.WithAlpha(th.Color.Accent, 0x30)},
		{Text: "old", Muted: true},
		{},
	} {
		if d := uitest.Smoke(t, "cell", image.Pt(200, 20), cell(a)); d.Size != image.Pt(200, 20) {
			t.Fatalf("size %v", d.Size)
		}
	}
	img, err := uitest.Render(th, image.Pt(200, 20), cell(molecules.AnnotationCell{Bar: th.Color.Accent}))
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	if c := img.RGBAAt(1, 10); c.R != th.Color.Accent.R || c.G != th.Color.Accent.G {
		t.Errorf("bar %v", c)
	}
}
