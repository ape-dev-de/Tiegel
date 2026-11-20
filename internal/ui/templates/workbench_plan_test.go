package templates

import (
	"image"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// U9: the corner mode does not change the side panel widths or the
// bottom panel height, and no resize is reported for the toggle.
func TestWorkbenchCornerModeKeepsSizes(t *testing.T) {
	modes := [][numCorners]bool{{}, {true, true}, {true, false}, {false, true}}
	var want geometry
	for i, full := range modes {
		wb, slots := fullBench()
		wb.Sizes[SideLeft], wb.Sizes[SideRight], wb.Sizes[SideBottom] = 0.22, 0.27, 0.3
		wb.BottomFull = full
		var evs []Event
		w := collect(wb, &slots, &evs)
		h := uitest.NewHarness(image.Pt(winW, winH))
		h.Frame(w)
		h.Frame(w)
		for _, e := range evs {
			if _, ok := e.(ResizeEvent); ok {
				t.Fatalf("mode %v: resize reported without a drag: %v", full, e)
			}
		}
		g := wb.geom
		if i == 0 {
			want = g
			continue
		}
		for _, c := range []struct {
			name      string
			got, want int
		}{
			{"left width", g.panels[SideLeft][GroupPrimary].Dx(), want.panels[SideLeft][GroupPrimary].Dx()},
			{"right width", g.panels[SideRight][GroupPrimary].Dx(), want.panels[SideRight][GroupPrimary].Dx()},
			{"bottom height", g.panels[SideBottom][GroupPrimary].Dy(), want.panels[SideBottom][GroupPrimary].Dy()},
		} {
			if d := c.got - c.want; d < -1 || d > 1 {
				t.Errorf("mode %v: %s %d, want %d", full, c.name, c.got, c.want)
			}
		}
		if wb.Sizes[SideLeft] != 0.22 || wb.Sizes[SideRight] != 0.27 || wb.Sizes[SideBottom] != 0.3 {
			t.Errorf("mode %v: sizes changed to %v", full, wb.Sizes)
		}
	}
}

// The share conversion round-trips.
func TestNodeRatioRoundTrip(t *testing.T) {
	for _, n := range []node{{ratio: 0.3, full: true}, {ratio: 0.7, full: true, invert: true}, {ratio: 0.4}} {
		r := n.ratioFor(800, 1200, 1)
		share := n.shareOf(r, 800, 1200, 1)
		want := n.ratio
		if n.invert {
			want = 1 - want
		}
		if d := share - want; d < -1e-4 || d > 1e-4 {
			t.Errorf("%+v: share %v, want %v", n, share, want)
		}
	}
}
