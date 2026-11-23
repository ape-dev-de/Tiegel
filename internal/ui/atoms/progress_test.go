package atoms_test

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestProgressBar(t *testing.T) {
	d := uitest.Smoke(t, "progress", area, atoms.ProgressBar{Value: 0.5, Width: 100}.Layout)
	if d.Size != image.Pt(100, 4) {
		t.Errorf("size %v", d.Size)
	}
	d = uitest.Smoke(t, "default width", area, atoms.ProgressBar{Value: 2, Height: 2, Danger: true}.Layout)
	if d.Size != image.Pt(80, 2) {
		t.Errorf("default size %v", d.Size)
	}
	d = uitest.Smoke(t, "min width", area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Constraints.Min.X = 50
		return atoms.ProgressBar{}.Layout(gtx, th)
	})
	if d.Size.X != 50 {
		t.Errorf("min width %v", d.Size)
	}
	ind := atoms.ProgressBar{Value: -1, Width: 60}
	if !ind.Indeterminate() || (atoms.ProgressBar{}).Indeterminate() {
		t.Error("Indeterminate")
	}
	uitest.Smoke(t, "indeterminate", area, ind.Layout)
	uitest.Smoke(t, "fn", area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return ind.Fn(th)(gtx)
	})
}

// The filled part is drawn in the accent color, proportional to Value.
func TestProgressBarFill(t *testing.T) {
	th := tokens.Dark(nil)
	size := image.Pt(100, 8)
	count := func(v float32) int {
		img, err := uitest.Render(th, size, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return atoms.ProgressBar{Value: v, Width: 100, Height: 8}.Layout(gtx, th)
		})
		if err != nil {
			t.Skip(err)
		}
		n := 0
		for x := 0; x < size.X; x++ {
			if c := img.RGBAAt(x, 4); c.R == th.Color.Accent.R && c.G == th.Color.Accent.G && c.B == th.Color.Accent.B {
				n++
			}
		}
		return n
	}
	half, full := count(0.5), count(1)
	if half < 40 || half > 60 || full < 95 {
		t.Fatalf("accent pixels: half %d, full %d", half, full)
	}
}
