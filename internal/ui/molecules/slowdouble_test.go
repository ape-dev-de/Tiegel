package molecules_test

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// TestSlowDoubleClick: two clicks 350 ms apart (an ordinary double click
// on macOS) are a double click; 900 ms apart they are two single clicks.
func TestSlowDoubleClick(t *testing.T) {
	for _, tc := range []struct {
		gap  time.Duration
		want bool
	}{{350 * time.Millisecond, true}, {900 * time.Millisecond, false}} {
		h := uitest.NewHarness(image.Pt(300, 40))
		var r molecules.ListRow
		double := false
		w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			if _, d := r.Update(gtx); d {
				double = true
			}
			return r.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: image.Pt(100, 20)} })
		}
		h.Frame(w)
		pos := f32.Pt(50, 10)
		click := func(at time.Duration) {
			h.Router.Queue(
				pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Time: at},
				pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos, Time: at + 40*time.Millisecond},
			)
			h.Frame(w)
		}
		click(10 * time.Second)
		click(10*time.Second + tc.gap)
		if double != tc.want {
			t.Errorf("gap %v: double=%v", tc.gap, double)
		}
	}
}
