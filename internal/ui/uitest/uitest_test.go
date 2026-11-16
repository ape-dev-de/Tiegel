package uitest

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

func box(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	sz := image.Pt(40, 20)
	paint.FillShape(gtx.Ops, th.Color.Accent, clip.Rect{Max: sz}.Op())
	return layout.Dimensions{Size: sz}
}

func TestSmoke(t *testing.T) {
	d := Smoke(t, "box", image.Pt(100, 100), box)
	if d.Size != image.Pt(40, 20) {
		t.Fatalf("size = %v", d.Size)
	}
}

func TestLayoutRecoversPanic(t *testing.T) {
	_, err := Layout(tokens.Dark(nil), image.Pt(10, 10), func(layout.Context, *tokens.Theme) layout.Dimensions {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected error from panicking widget")
	}
}

func TestContextIsEnabled(t *testing.T) {
	_, err := Layout(tokens.Dark(nil), image.Pt(10, 10), func(gtx layout.Context, _ *tokens.Theme) layout.Dimensions {
		if !gtx.Enabled() {
			t.Error("context source must be enabled")
		}
		return layout.Dimensions{}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRenderHeadless(t *testing.T) {
	th := tokens.Dark(nil)
	img, err := Render(th, image.Pt(64, 32), box)
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	if n := CountDiffering(img, th.Color.Background); n < 40*20 {
		t.Fatalf("only %d pixels painted, want ≥ %d", n, 40*20)
	}
	if got := img.RGBAAt(5, 5); got.R != th.Color.Accent.R || got.G != th.Color.Accent.G {
		t.Fatalf("pixel (5,5) = %v, want accent %v", got, th.Color.Accent)
	}
}
