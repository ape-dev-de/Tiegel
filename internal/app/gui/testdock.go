package gui

import (
	"fmt"
	"path/filepath"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/teamcity"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Tests (AP15.3/15.4): a green play arrow in the gutter at every test
// class and method (Run / Debug / Modify Run Configuration…), the last
// result as gutter icon, the "Tests" tool window with the result tree,
// durations, failure details, expected/actual in the diff window and
// "Rerun Failed", Debug (Ctrl+D) and Go to Test / Create Test
// (Cmd+Shift+T). The backend is the test plugin's test.* namespace.

// Tests tool window and action IDs.
const (
	ToolTests = "tests"

	ActTestsView    keymap.Action = "view.tests"
	ActDebug        keymap.Action = "run.debug"
	ActDebugFile    keymap.Action = "run.debugFile"
	ActGotoTest     keymap.Action = "navigate.gotoTest"
	ActRerunFailed  keymap.Action = "test.rerunFailed"
	testsToolOrder                = 16
	testDetailWidth               = 0.55
)

type testDock struct {
	v *View

	tree    resultTree
	nodes   map[string]*resultNode
	data    map[*resultNode]*teamcity.Node
	seen    uint64
	project string
	split   molecules.Split
	detail  molecules.ScrollList
	// root is the tree's top node: the run (its console output);
	// rootAuto is set while it is selected because nothing else was.
	root     *resultNode
	rootAuto bool
	progress atoms.ProgressBar

	runAllBtn, rerunBtn, stopBtn, installBtn, diffBtn, failBtn atoms.Button
}

func installTests(v *View) *testDock {
	d := &testDock{v: v, nodes: map[string]*resultNode{}, data: map[*resultNode]*teamcity.Node{},
		split:      molecules.Split{Ratio: 1 - testDetailWidth},
		runAllBtn:  atoms.Button{Text: "Run All", Kind: atoms.Secondary, Glyph: atoms.GlyphRun},
		rerunBtn:   atoms.Button{Text: "Rerun Failed", Kind: atoms.Secondary, Glyph: atoms.GlyphRefresh},
		stopBtn:    atoms.Button{Text: "Stop", Kind: atoms.Secondary, Glyph: atoms.GlyphStop},
		installBtn: atoms.Button{Text: "Install…", Kind: atoms.Primary},
		diffBtn:    atoms.Button{Text: "Show Diff", Kind: atoms.Secondary, Glyph: atoms.GlyphSwap},
		failBtn:    atoms.Button{Text: "Go to Failure", Kind: atoms.Ghost, Glyph: atoms.GlyphArrowRight},
	}
	d.tree.Empty = "No test results yet. Click the play arrow next to a test in the editor, or Run All."
	r := v.Registry
	r.AddToolWindow(ToolWindow{
		ID: ToolTests, Title: "Tests", Glyph: atoms.GlyphDone, Side: Bottom, Order: testsToolOrder, Share: 0.35,
		ActionID: ActTestsView,
		Badge: func() (string, atoms.BadgeKind) {
			if t := v.ctl.Tests.Run; t != nil && t.Counts.Failed > 0 {
				return fmt.Sprint(t.Counts.Failed), atoms.BadgeDanger
			}
			return "", 0
		},
		Layout: d.layout,
		OnOpen: func() { v.ctl.Tests.LoadStatus() },
	})
	r.AddFrameHandler(d.frame)
	r.AddGutterProvider(d.markers)
	// Before the navigation marks: a test class line has both.
	r.gutterClicks = append([]GutterClickHandler{d.gutterClick}, r.gutterClicks...)
	for _, a := range d.actions() {
		r.AddAction(a)
	}
	d.installTestMenus()
	return d
}

func (d *testDock) tests() *controller.Tests { return d.v.ctl.Tests }

func (d *testDock) actions() []Action {
	return []Action{
		{ID: ActDebug, Title: "Debug", Mac: "Ctrl+D", PC: "Shift+F9", Menu: "Run", Section: "run", Run: func(v *View) {
			file := d.v.rund.currentFile()
			if !isPHPFile(file) {
				file = ""
			}
			d.v.ctl.Run.DebugSelected(file)
		}},
		{ID: ActDebugFile, Title: "Debug Context Configuration", Mac: "Ctrl+Shift+D", PC: "Ctrl+Shift+F9", Menu: "Run", Section: "run",
			Run: func(v *View) { d.runContext(true) }},
		{ID: ActRerunFailed, Title: "Rerun Failed Tests", Menu: "Run", Section: "tests", Run: func(*View) { d.tests().RerunFailed() }},
		{ID: ActGotoTest, Title: "Go to Test", Mac: "Cmd+Shift+T", PC: "Ctrl+Shift+T", Scope: ScopeEditor, Menu: "Navigate", Section: "goto",
			Run: func(v *View) {
				if s := v.activeSession(); s != nil {
					file := s.file
					v.Post(func() { d.gotoTest(file) })
				}
			}},
	}
}

// frame follows the project and shows the window when a test run started.
func (d *testDock) frame(v *View, _ layout.Context) {
	t := d.tests()
	if root := v.ctl.Project.Root; root != d.project {
		d.project = root
		t.ProjectChanged()
		d.nodes, d.data = map[string]*resultNode{}, map[*resultNode]*teamcity.Node{}
		d.tree.set(nil)
		d.root, d.rootAuto = nil, false
		if root != "" {
			t.LoadStatus()
		}
	}
	if t.Started {
		t.Started = false
		v.ShowTool(ToolTests)
	}
}

// caret returns the file and 1-based caret line of the active editor.
func (d *testDock) caret() (string, int) {
	s := d.v.activeSession()
	if s == nil {
		return "", 0
	}
	s.doc.Lock()
	defer s.doc.Unlock()
	off := s.ed.Cursor().Primary().Head
	return s.file, s.doc.Buffer().OffsetToPos(off).Line + 1
}

// runContext runs (or debugs) the test at the caret; outside tests the
// current PHP file (Run/Debug Context Configuration).
func (d *testDock) runContext(debug bool) bool {
	file, line := d.caret()
	if item, ok := d.tests().TestAt(file, line); ok {
		d.tests().RunTests(controller.ScopeOf(item), debug)
		return true
	}
	if !isPHPFile(file) {
		d.v.ctl.Status.Notice = "Run: the active file is not a PHP file"
		return false
	}
	if debug {
		d.v.ctl.Run.DebugFile(file)
	} else {
		d.v.ctl.Run.RunFile(file)
	}
	return true
}

// ---- gutter -------------------------------------------------------------------

// statusMarker is the gutter icon of a test: the last result, else the
// green play arrow.
func statusMarker(st teamcity.Status) editorview.Marker {
	m := editorview.Marker{Kind: editorview.MarkerIcon, Glyph: atoms.GlyphRun, Tone: editorview.ToneSuccess}
	switch st {
	case teamcity.StatusPassed:
		m.Glyph = atoms.GlyphDone
	case teamcity.StatusFailed, teamcity.StatusTerminated:
		m.Glyph, m.Tone = atoms.GlyphError, editorview.ToneDanger
	case teamcity.StatusIgnored:
		m.Glyph, m.Tone = atoms.GlyphWarning, editorview.ToneWarning
	case teamcity.StatusRunning:
		m.Tone = editorview.ToneInfo
	}
	return m
}

func (d *testDock) markers(file string) editorview.MarkerProvider {
	ft := d.tests().ForFile(file)
	if ft == nil || len(ft.ByLine) == 0 {
		return nil
	}
	t := d.tests()
	return editorview.MarkerFunc(func(line int) []editorview.Marker {
		it, ok := ft.ByLine[line+1]
		if !ok {
			return nil
		}
		return []editorview.Marker{statusMarker(t.StatusOf(it.ID))}
	})
}

// gutterClick opens the menu of the test on the clicked line.
func (d *testDock) gutterClick(file string, ev editorview.GutterClickEvent) bool {
	if ev.Area != editorview.GutterIcons || ev.Secondary || ev.Mods != 0 {
		return false
	}
	ft := d.tests().ForFile(file)
	if ft == nil {
		return false
	}
	it, ok := ft.ByLine[ev.Line+1]
	if !ok {
		return false
	}
	items := d.testMenu(it)
	if _, nav := d.v.navd.n.GutterMarks(file)[ev.Line+1]; nav {
		items = append(items, MenuItem{Separator: true},
			MenuItem{Title: "Go to Super / Implementations…", Icon: "step-out", Run: func() { d.v.navd.gutterClick(file, ev) }})
	}
	d.v.ShowMenu(it.Name, items)
	return true
}

// testMenu are the run entries of a discovered test.
func (d *testDock) testMenu(it api.TestItem) []MenuItem {
	scope := controller.ScopeOf(it)
	name := it.Name
	if it.Kind == "method" {
		name = it.Method
	}
	return []MenuItem{
		{Title: "Run '" + name + "'", Icon: "run", Shortcut: d.shortcut(ActRunFile), Run: func() { d.tests().RunTests(scope, false) }},
		{Title: "Debug '" + name + "'", Icon: "debug", Shortcut: d.shortcut(ActDebugFile), Run: func() { d.tests().RunTests(scope, true) }},
		{Title: "Modify Run Configuration…", Icon: "edit", Run: func() { d.modifyConfig(scope) }},
	}
}

// shortcut renders the chord of action id in the current preset.
func (d *testDock) shortcut(id keymap.Action) string {
	if a, ok := d.v.Registry.Action(id); ok {
		if d.v.Registry.Scheme().Preset() == keymap.PresetMac {
			return a.Mac
		}
		return a.PC
	}
	return ""
}

// modifyConfig opens the run configuration editor with a configuration
// for scope (saved only with Save).
func (d *testDock) modifyConfig(scope api.TestScope) {
	d.tests().ConfigFor(scope, func(cfg run.Config) {
		r := d.v.ctl.Run
		open := func() bool {
			if t, ok := d.v.rund.typeOf(cfg.Type); ok {
				d.v.rund.openEditor(cfg, t)
				return true
			}
			return false
		}
		if !open() {
			r.LoadConfigs()
			d.v.ctl.Status.Notice = "Loading run configuration types – try again"
		}
	})
}

// ---- go to test ---------------------------------------------------------------

func (d *testDock) gotoTest(file string) {
	if !isPHPFile(file) {
		d.v.ctl.Status.Notice = "Go to Test: not a PHP file"
		return
	}
	d.tests().GotoTest(file, func(g api.TestGotoResult) {
		var items []MenuItem
		for _, tg := range g.Targets {
			if !tg.Exists {
				continue
			}
			f := tg.File
			items = append(items, MenuItem{Title: tg.Class, Icon: "file-code", Run: func() { d.v.ctl.OpenFile(f, nil) }})
		}
		if g.Create != nil && !g.Create.Exists {
			if len(items) > 0 {
				items = append(items, MenuItem{Separator: true})
			}
			plan := *g.Create
			items = append(items, MenuItem{Title: "Create New Test…", Icon: "add", Run: func() { d.confirmCreate(file, plan) }})
		}
		if len(items) == 0 {
			d.v.ctl.Status.Notice = "Go to Test: nothing found for " + g.Class
			return
		}
		title := "Choose Test for " + g.Class
		if g.Direction == "subject" {
			title = "Choose Class under Test"
		}
		d.v.ShowMenu(title, items)
	})
}

// confirmCreate shows the proposed test file and creates it on OK.
func (d *testDock) confirmCreate(file string, plan api.TestCreatePlan) {
	rel := d.v.ctl.Rel(plan.File)
	d.v.OpenPopup(&Popup{Title: "Create Test", Width: 560, Centered: true,
		Detail: "Creates " + rel + " (" + plan.Class + ") with a test skeleton.",
		Body: func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			lines := strings.Split(strings.TrimRight(plan.Content, "\n"), "\n")
			children := make([]layout.FlexChild, 0, len(lines))
			for _, l := range lines {
				children = append(children, layout.Rigid(atoms.Code(l).SingleLine().Fn(th)))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		},
		Buttons: []*PopupButton{
			{Text: "Create", Kind: atoms.Primary, OnClick: func() { d.tests().CreateTest(file, plan.File) }},
			{Text: "Cancel", Kind: atoms.Secondary},
		}})
}

// ---- tool window -------------------------------------------------------------

// statusTone colors a result: passed green, skipped orange, failed red.
func statusTone(st teamcity.Status) nodeTone {
	switch st {
	case teamcity.StatusPassed:
		return toneSuccess
	case teamcity.StatusFailed, teamcity.StatusTerminated:
		return toneDanger
	case teamcity.StatusIgnored:
		return toneWarning
	case teamcity.StatusRunning:
		return toneInfo
	}
	return toneNone
}

func statusGlyph(st teamcity.Status) *atoms.Glyph {
	switch st {
	case teamcity.StatusPassed:
		return atoms.GlyphDone
	case teamcity.StatusFailed, teamcity.StatusTerminated:
		return atoms.GlyphError
	case teamcity.StatusIgnored:
		return atoms.GlyphWarning
	case teamcity.StatusRunning:
		return atoms.GlyphRun
	}
	return atoms.GlyphRun
}

// nodeName is the tree label of a result node.
func nodeName(n *teamcity.Node) string {
	switch n.Kind {
	case teamcity.KindDataSet:
		return "data set " + n.DataSet
	case teamcity.KindClass:
		if i := strings.LastIndexByte(n.Name, '\\'); i >= 0 {
			return n.Name[i+1:]
		}
	case teamcity.KindSuite:
		if strings.ContainsAny(n.Name, `/\`) {
			return filepath.Base(n.Name)
		}
	}
	return n.Name
}

// syncTree rebuilds the tree from the shown run, keeping the nodes (and
// so expansion and selection) of tests that were there before.
func (d *testDock) syncTree() {
	t := d.tests()
	if d.seen == t.Version {
		return
	}
	d.seen = t.Version
	if t.Run == nil {
		return
	}
	data := map[*resultNode]*teamcity.Node{}
	var build func(ns []*teamcity.Node) []*resultNode
	build = func(ns []*teamcity.Node) []*resultNode {
		out := make([]*resultNode, 0, len(ns))
		for _, n := range ns {
			rn := d.nodes[n.ID]
			if rn == nil {
				rn = &resultNode{Expanded: n.Kind != teamcity.KindMethod || n.Status == teamcity.StatusFailed}
				d.nodes[n.ID] = rn
			}
			rn.Name, rn.Glyph, rn.Tone = nodeName(n), statusGlyph(n.Status), statusTone(n.Status)
			rn.Detail = fmt.Sprintf("%d ms", n.DurationMs)
			if n.Status == teamcity.StatusRunning {
				rn.Detail = "running"
			}
			rn.Children = build(n.Children)
			node := n
			rn.Open = func() { d.openNode(node) }
			data[rn] = n
			out = append(out, rn)
		}
		return out
	}
	roots := make([]*teamcity.Node, len(t.Run.Tree))
	for i := range t.Run.Tree {
		roots[i] = &t.Run.Tree[i]
	}
	// The run is the top node; selecting it shows the console output.
	runNode := &teamcity.Node{Name: t.Run.Name, Kind: teamcity.KindSuite, Children: roots}
	runNode.Status = teamcity.Aggregate(roots)
	for _, r := range roots {
		runNode.DurationMs += r.DurationMs
	}
	if t.Run.State == run.StateRunning {
		runNode.Status = teamcity.StatusRunning
	}
	if d.root == nil {
		d.root = &resultNode{Expanded: true}
	}
	rr := d.root
	rr.Name, rr.Glyph, rr.Tone = t.Run.Name, statusGlyph(runNode.Status), statusTone(runNode.Status)
	rr.Detail = fmt.Sprintf("%d ms", t.Run.DurationMs)
	if t.Run.State == run.StateRunning {
		rr.Detail = "running"
	}
	rr.Children, rr.Open = build(roots), nil
	data[rr] = runNode
	d.tree.Roots, d.tree.dirty = []*resultNode{rr}, true
	if d.tree.rows == nil {
		d.tree.rows = map[*resultNode]*molecules.TreeRow{}
	}
	d.data = data
	if d.tree.sel != nil && data[d.tree.sel] == nil {
		d.tree.sel = nil // a node of an earlier run
	}
	if d.tree.sel != rr {
		d.rootAuto = false
	}
	// While nothing is selected the console streams; a finished run
	// selects its first failure.
	if d.tree.sel == nil {
		d.tree.sel, d.rootAuto = rr, true
	}
	if d.rootAuto && t.Run.State != run.StateRunning {
		var first *resultNode
		for rn, n := range data {
			if n.Kind.IsTest() && n.Status == teamcity.StatusFailed && (first == nil || n.ID < d.data[first].ID) {
				first = rn
			}
		}
		if first != nil {
			d.tree.sel, d.rootAuto = first, false
		}
	}
}

// openNode jumps to the failure of a test, else to its declaration.
func (d *testDock) openNode(n *teamcity.Node) {
	if n.FailFile != "" && n.FailLine > 0 {
		d.v.ctl.OpenFile(n.FailFile, ptr(controller.AtLine(n.FailFile, n.FailLine)))
		return
	}
	if n.File == "" {
		return
	}
	if ft := d.tests().ForFile(n.File); ft != nil {
		want := n.Class
		if n.Method != "" {
			want += "::" + n.Method
		}
		for line, it := range ft.ByLine {
			if it.ID == want {
				d.v.ctl.OpenFile(n.File, ptr(controller.AtLine(n.File, line)))
				return
			}
		}
	}
	d.v.ctl.OpenFile(n.File, nil)
}

// showDiff opens expected/actual of a comparison failure in the diff
// window.
func (d *testDock) showDiff(n *teamcity.Node) {
	path := n.File
	if path == "" {
		path = n.FailFile
	}
	d.v.ctl.Work.OpenDiff(controller.DiffSpec{
		Path:  d.v.ctl.Rel(path),
		Title: nodeName(n) + " (expected ↔ actual)",
		Texts: &controller.DiffTexts{Left: n.Expected, Right: n.Actual, LeftLabel: "Expected", RightLabel: "Actual", Key: n.ID},
	})
}

// runProgress is the progress bar of a test run: finished tests of the
// announced count (indeterminate before the count is known), red with
// failures, green when everything passed.
func runProgress(r *api.TestRun) atoms.ProgressBar {
	c := r.Counts
	p := atoms.ProgressBar{Width: 140, Height: 6, Value: -1, Danger: c.Failed+c.Terminated > 0}
	done := c.Passed + c.Failed + c.Ignored + c.Terminated
	switch {
	case r.State != run.StateRunning:
		p.Value, p.Success = 1, !p.Danger
	case c.Expected > 0:
		p.Value = float32(done) / float32(c.Expected)
	}
	return p
}

func (d *testDock) summary() string {
	t := d.tests()
	r := t.Run
	if r == nil {
		return ""
	}
	c := r.Counts
	s := fmt.Sprintf("%d/%d passed", c.Passed, max(c.Total, c.Expected))
	if c.Failed > 0 {
		s += fmt.Sprintf(", %d failed", c.Failed)
	}
	if c.Ignored > 0 {
		s += fmt.Sprintf(", %d skipped", c.Ignored)
	}
	if c.Terminated > 0 {
		s += fmt.Sprintf(", %d terminated", c.Terminated)
	}
	if r.State == run.StateRunning {
		return s + " · running"
	}
	return s + fmt.Sprintf(" · %d ms", r.DurationMs)
}

func (d *testDock) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	t := d.tests()
	d.syncTree()
	if d.runAllBtn.Clicked(gtx) {
		t.RunTests(api.TestScope{}, false)
	}
	if d.rerunBtn.Clicked(gtx) {
		t.RerunFailed()
	}
	if d.stopBtn.Clicked(gtx) {
		t.Stop()
	}
	if d.installBtn.Clicked(gtx) && t.Status.Install != "" {
		d.v.lspd.openInstall(t.Status.Install)
	}
	sel := d.data[d.tree.sel]
	if sel != nil {
		// The tree is light (no output, details, expected/actual, D380):
		// the selected node comes in full from test.node. Until then the
		// comparison (empty here) is hidden.
		if full := t.Detail(sel.ID); full != nil {
			sel = full
		} else if sel.Comparison {
			light := *sel
			light.Comparison = false
			sel = &light
		}
	}
	if sel != nil && d.diffBtn.Clicked(gtx) && sel.Comparison {
		d.showDiff(sel)
	}
	if sel != nil && d.failBtn.Clicked(gtx) {
		d.openNode(sel)
	}
	title := "Tests"
	if t.Run != nil {
		title = t.Run.Name
	}
	var btns []func(gtx layout.Context, th *tokens.Theme) layout.Dimensions
	if t.Run != nil {
		d.progress = runProgress(t.Run)
		btns = append(btns, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return d.progress.Layout(gtx, th) })
	}
	if t.Run != nil && t.Run.State == run.StateRunning {
		btns = append(btns, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return d.stopBtn.Layout(gtx, th) })
	}
	if len(t.Failed()) > 0 {
		btns = append(btns, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return d.rerunBtn.Layout(gtx, th) })
	}
	btns = append(btns, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return d.runAllBtn.Layout(gtx, th) })
	d.tree.Header = headerText(title, d.summary(), btns...)

	children := []layout.FlexChild{}
	if t.Disabled {
		children = append(children, layout.Rigid(d.banner(th, molecules.BannerInfo, "No test plugin", "Enable a test plugin (Settings › Plugins) to discover and run tests.", false)))
	} else if t.Loaded && !t.Status.Available && t.Status.Hint != "" {
		children = append(children, layout.Rigid(d.banner(th, molecules.BannerWarning, t.Status.Framework+" not found", t.Status.Hint, t.Status.Install != "")))
	}
	if t.Run != nil && t.Run.Error != "" {
		children = append(children, layout.Rigid(d.banner(th, molecules.BannerError, "No results", t.Run.Error, false)))
	}
	children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
		return d.split.Layout(gtx, th,
			func(gtx layout.Context) layout.Dimensions { return d.tree.layout(gtx, th) },
			func(gtx layout.Context) layout.Dimensions { return d.layoutDetail(gtx, th, sel) })
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

func (d *testDock) banner(th *tokens.Theme, kind molecules.BannerKind, title, detail string, install bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			children := []layout.FlexChild{layout.Flexed(1, molecules.Banner{Kind: kind, Title: title, Detail: detail}.Fn(th))}
			if install {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.installBtn.Layout(gtx, th) })
				}))
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
		})
	}
}

// detailLines are the text lines of the detail pane of n.
func detailLines(n *teamcity.Node, rel func(string) string) []string {
	if n == nil {
		return []string{"Select a test to see its result."}
	}
	head := string(n.Status)
	if n.Status == teamcity.StatusIgnored {
		head = "skipped"
	}
	if n.Kind.IsTest() || n.DurationMs > 0 {
		head += fmt.Sprintf(" · %d ms", n.DurationMs)
	}
	title := n.ID
	if title == "" {
		title = n.Name
	}
	lines := []string{title, head}
	if n.Message != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(n.Message, "\n")...)
	}
	if n.Comparison {
		lines = append(lines, "", "Expected:")
		lines = append(lines, strings.Split(n.Expected, "\n")...)
		lines = append(lines, "Actual:")
		lines = append(lines, strings.Split(n.Actual, "\n")...)
	}
	if n.FailFile != "" {
		lines = append(lines, "", fmt.Sprintf("at %s:%d", rel(n.FailFile), n.FailLine))
	}
	if n.Details != "" {
		lines = append(lines, "")
		lines = append(lines, strings.Split(n.Details, "\n")...)
	}
	switch {
	case n.Output != "" && n.Kind.IsTest():
		lines = append(lines, "", "Output:")
		lines = append(lines, strings.Split(n.Output, "\n")...)
	case n.Output != "":
		lines = append(lines, "")
		lines = append(lines, strings.Split(n.Output, "\n")...)
	case !n.Kind.IsTest() && n.Status != teamcity.StatusRunning:
		lines = append(lines, "", "No output.")
	}
	return lines
}

func (d *testDock) layoutDetail(gtx layout.Context, th *tokens.Theme, n *teamcity.Node) layout.Dimensions {
	lines := detailLines(n, d.v.ctl.Rel)
	var btns []layout.FlexChild
	if n != nil && n.Comparison {
		btns = append(btns, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.diffBtn.Layout(gtx, th) })
		}))
	}
	if n != nil && n.FailFile != "" {
		btns = append(btns, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.failBtn.Layout(gtx, th) }))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(btns) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, btns...)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return d.detail.Layout(gtx, th, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
				w := atoms.Code(lines[i]).SingleLine().Fn(th)
				if i == 0 {
					w = atoms.Strong(lines[i]).SingleLine().Fn(th)
				}
				return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx, w)
			})
		}),
	)
}
