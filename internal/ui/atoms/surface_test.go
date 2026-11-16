package atoms_test

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestSurface(t *testing.T) {
	th := tokens.Dark(nil)
	seen := map[string]bool{}
	for _, k := range atoms.SurfaceKinds {
		if k.Color(th).A == 0 || seen[k.String()] {
			t.Errorf("kind %s", k)
		}
		seen[k.String()] = true
		s := atoms.Surface{Kind: k, Border: true, Rounded: true}
		d := uitest.Smoke(t, "surface "+k.String(), area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return s.Layout(gtx, th, atoms.Text("content").Fn(th))
		})
		if d.Size.X >= area.X {
			t.Errorf("Layout must keep the content size, got %v", d.Size)
		}
		f := uitest.Smoke(t, "surface fill "+k.String(), area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			gtx.Constraints.Min = image.Pt(200, 100)
			return s.Fill(gtx, th, atoms.Text("x").Fn(th))
		})
		if f.Size != image.Pt(200, 100) {
			t.Errorf("Fill size %v", f.Size)
		}
	}
}
