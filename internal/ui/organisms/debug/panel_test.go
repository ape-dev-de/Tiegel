package debug

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// Narrow panels (docked at a side) stack the columns instead of
// squeezing them below the minimum column width.
func TestDebugPanelStacksWhenNarrow(t *testing.T) {
	th := tokens.Dark(nil)
	minCol := int(th.Shell().PanelColumnMin)
	for _, c := range []struct {
		w       int
		stacked bool
	}{{2*minCol - 20, true}, {2*minCol + 40, false}, {900, false}, {260, true}} {
		p := NewDebugPanel()
		p.Breakpoints.Items = []Breakpoint{{ID: "1", File: "/app/src/Mailer.php", Line: 42, Enabled: true}}
		h := uitest.NewHarness(image.Pt(c.w, 600))
		h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return p.Layout(gtx, th) })
		if p.Stacked() != c.stacked {
			t.Errorf("width %d: stacked %v, want %v", c.w, p.Stacked(), c.stacked)
		}
		uitest.Smoke(t, "debug panel", image.Pt(c.w, 600), p.Layout)
	}
}
