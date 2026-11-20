package gui

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// TestTerminalTabRowHeader: the tab row of the terminal window follows
// the panel header conventions of D464 (height PanelHeaderH, icon
// buttons of ActionButton size) (review wave 9).
func TestTerminalTabRowHeader(t *testing.T) {
	e := newEnv(t, true)
	th := tokens.Dark(nil)
	gtx := layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 2, PxPerSp: 2},
		Constraints: layout.Constraints{Max: image.Pt(800, 400)}}
	d := e.v.termd
	dims := d.layoutTabs(gtx, th)
	if want := gtx.Dp(th.Size.PanelHeaderH); dims.Size.Y != want {
		t.Fatalf("tab row height %d, want %d", dims.Size.Y, want)
	}
	if d.addBtn.Size != th.Size.ActionButton || d.renameBt.Size != th.Size.ActionButton {
		t.Fatalf("button sizes %v %v, want %v", d.addBtn.Size, d.renameBt.Size, th.Size.ActionButton)
	}
}
