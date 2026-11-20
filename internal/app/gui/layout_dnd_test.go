package gui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/templates"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// liveShot is shot with the harness router as event source, so pointer
// state (a drag in progress) survives the rendering; a fresh router
// would cancel it.
func (e *env) liveShot(name string) {
	dir := os.Getenv("TIEGEL_SHOTS")
	if dir == "" {
		return
	}
	for _, mode := range []string{"dark", "light"} {
		e.ctl.Prefs.Theme = mode
		e.frame()
		img, err := uitest.Render(e.v.th, windowSize, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			gtx.Source = e.h.Router.Source()
			return e.v.Frame(gtx)
		})
		if err != nil {
			e.t.Logf("headless rendering unavailable: %v", err)
			return
		}
		f, err := os.Create(filepath.Join(dir, name+"-"+mode+".png"))
		if err != nil {
			e.t.Fatal(err)
		}
		_ = png.Encode(f, img)
		f.Close()
	}
	e.ctl.Prefs.Theme = "dark"
	e.frame()
}

// AP18.8: drag a panel header to the bottom edge (terminal full width
// under project and structure), join a panel as tab, corner and layout
// actions. TIEGEL_SHOTS=<dir> writes the screenshots.
func TestDragAndDropLayout(t *testing.T) {
	e := newGitEnvWith(t, map[string]string{"src/Model/Customer.php": "<?php\nclass Customer {}\n"})
	e.waitFor("tool layout", func() bool { return e.ctl.ToolsReady() })
	e.ctl.OpenFile("src/Shop.php", nil)
	e.waitFor("tab", func() bool { return e.ctl.ActiveTab() != nil })
	l := e.ctl.Tools
	e.v.ShowTool(ToolProject)
	e.v.ShowTool(ToolStructure)
	e.v.ShowTool(ToolTerminal)
	e.settle()
	if l.IsBottomFull(controller.SideLeft) || l.IsBottomFull(controller.SideRight) {
		t.Fatal("default: the side panels own the bottom corners")
	}
	e.shot("dnd-before")

	// Drag the terminal header to the bottom edge; the preview shows the
	// full-width target while dragging.
	term := e.v.bench.PanelRect(ToolTerminal)
	if term.Empty() {
		t.Fatal("terminal panel not laid out")
	}
	from := term.Min.Add(image.Pt(60, 12))
	to := image.Pt(windowSize.X/3, term.Max.Y-6)
	pt := func(p image.Point) f32.Point { return f32.Pt(float32(p.X), float32(p.Y)) }
	e.h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pt(from)},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pt(from)},
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pt(to)},
	)
	e.frame()
	if !e.v.bench.Dragging() {
		t.Fatal("header drag not active")
	}
	e.liveShot("dnd-drag-preview")
	e.h.Router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pt(to)})
	e.frame()
	e.settle()
	if !l.IsBottomFull(controller.SideLeft) || !l.IsBottomFull(controller.SideRight) || !e.v.ToolShown(ToolTerminal) ||
		!e.v.ToolShown(ToolProject) || !e.v.ToolShown(ToolStructure) {
		t.Fatalf("after drop: corners %v", l.BottomFull)
	}
	if r := e.v.bench.PanelRect(ToolTerminal); r.Min.X > e.v.bench.PanelRect(ToolProject).Min.X {
		t.Fatalf("terminal %v not under the project panel %v", r, e.v.bench.PanelRect(ToolProject))
	}
	e.shot("dnd-terminal-full-width")

	// Problems joins the terminal panel as a tab.
	e.v.handleBenchEvent(templates.MoveEvent{ID: toolProblems, Side: templates.SideBottom, Tab: true})
	e.settle()
	if tabs := l.TabsOf(controller.SideBottom, controller.GroupPrimary); len(tabs) != 2 || !e.v.ToolShown(toolProblems) {
		t.Fatalf("tabs %v", tabs)
	}
	e.shot("dnd-tabs")
	e.v.handleBenchEvent(templates.SelectEvent{ID: ToolTerminal})
	if !e.v.ToolShown(ToolTerminal) || len(l.TabsOf(controller.SideBottom, controller.GroupPrimary)) != 2 {
		t.Fatal("selecting a tab")
	}

	// Corner actions and Reset Layout.
	e.v.RunAction(ActBottomUnderRight)
	if !l.IsBottomFull(controller.SideLeft) || l.IsBottomFull(controller.SideRight) {
		t.Fatalf("toggle right corner: %v", l.BottomFull)
	}
	e.v.RunAction(ActBottomFullWidth)
	if !l.IsBottomFull(controller.SideRight) {
		t.Fatal("full width")
	}
	e.v.RunAction(ActBottomFullWidth)
	if l.IsBottomFull(controller.SideLeft) || l.IsBottomFull(controller.SideRight) {
		t.Fatal("full width off")
	}
	e.v.RunAction(ActBottomUnderLeft)
	if !l.IsBottomFull(controller.SideLeft) {
		t.Fatal("left corner")
	}
	e.v.openToolMenu(ToolTerminal)
	e.settle()
	e.v.closeOverlay()

	// Named layouts through the kernel.
	e.v.RunAction(ActSaveLayout)
	e.v.custom.OnChoose(-1, "Wide terminal")
	e.settle()
	e.v.RunAction(ActResetLayout)
	e.settle()
	if l.IsBottomFull(controller.SideLeft) || e.v.ToolShown(ToolTerminal) || !e.v.ToolShown(ToolProject) {
		t.Fatal("reset layout")
	}
	e.v.RunAction(ActApplyLayout)
	e.waitFor("layout chooser", func() bool { return e.v.chooseMode == chooseCustom && len(e.v.chooser.Items) == 1 })
	e.v.custom.OnChoose(0, "")
	e.waitFor("applied layout", func() bool { return l.IsBottomFull(controller.SideLeft) })
	if !e.v.ToolShown(ToolTerminal) || len(l.TabsOf(controller.SideBottom, controller.GroupPrimary)) != 2 {
		t.Fatal("applied layout incomplete")
	}
	e.v.closeOverlay()
	e.v.RunAction(ActDeleteLayout)
	e.waitFor("delete chooser", func() bool { return e.v.chooseMode == chooseCustom && len(e.v.chooser.Items) == 1 })
	e.v.custom.OnQuery("nothing")
	e.v.custom.OnChoose(0, "nothing") // filtered away: nothing deleted
	e.v.custom.OnQuery("wide")
	e.v.custom.OnChoose(0, "wide")
	e.settle()
	e.v.closeOverlay()
	e.ctl.Status.Notice = ""
	e.v.RunAction(ActApplyLayout)
	e.waitFor("no layouts notice", func() bool { return e.ctl.Status.Notice != "" })
}
