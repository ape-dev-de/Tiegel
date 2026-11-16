package ui_test

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// Every leaf component satisfies the common convention.
var _ = []ui.Component{
	atoms.Label{}, atoms.Icon{}, atoms.Badge{}, atoms.Divider{}, atoms.TooltipBubble{},
	&atoms.Button{}, &atoms.IconButton{}, &atoms.TextField{}, &atoms.Checkbox{},
	&molecules.SearchField{}, &molecules.FileRow{}, &molecules.Tab{},
	&molecules.BreakpointRow{}, &molecules.DiffBlock{},
}

func TestComponentFuncAndWidget(t *testing.T) {
	called := false
	c := ui.ComponentFunc(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		called = th != nil
		return layout.Dimensions{Size: image.Pt(3, 4)}
	})
	d := uitest.Smoke(t, "func", image.Pt(10, 10), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return ui.Widget(c, th)(gtx)
	})
	if !called || d.Size != image.Pt(3, 4) {
		t.Fatalf("called=%v size=%v", called, d.Size)
	}
}
