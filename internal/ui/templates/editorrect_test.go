package templates

import (
	"image"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// EditorRect is the editor area between the panels.
func TestWorkbenchEditorRect(t *testing.T) {
	wb, slots := fullBench()
	var evs []Event
	h := uitest.NewHarness(image.Pt(winW, winH))
	h.Frame(collect(wb, &slots, &evs))
	r := wb.EditorRect()
	left, right := wb.PanelRect("project"), wb.PanelRect("debug")
	if r.Empty() || r != wb.geom.editor || r.Min.X < left.Max.X || r.Max.X > right.Min.X {
		t.Fatalf("editor %v between %v and %v", r, left, right)
	}
}
