package gui

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	debugui "github.com/ape-dev-de/tiegel/internal/ui/organisms/debug"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// The debug feature docks onto the shell through the Registry: the
// "Debug" tool window (DebugPanel), breakpoint and execution point gutter
// markers with click handling, the listener status item and the debugger
// shortcuts. It is installed while the debug-xdebug module runs.

// Debug tool window, status and action IDs.
const (
	ToolDebug     = "debug"
	statusDebugID = "debug.listener"

	ActToggleBreakpoint keymap.Action = "debug.toggleBreakpoint"
	ActDebugView        keymap.Action = "view.debug"
)

type debugDock struct {
	v         *View
	installed bool
	panel     *debugui.DebugPanel
	seen      struct{ vars, watches, bps, brk, hints uint64 }
	muted     bool
	dismiss   atoms.Button
}

func installDebug(v *View) *debugDock {
	d := &debugDock{v: v, panel: debugui.NewDebugPanel()}
	d.panel.Toolbar.Shortcuts = debugui.Shortcuts(v.goos)
	d.panel.Stack.Empty = "Not paused"
	d.panel.Variables.Empty = "No variables (not paused)"
	d.dismiss = atoms.Button{Kind: atoms.Ghost, Text: "Dismiss"}
	r := v.Registry
	r.AddFrameHandler(d.frame)
	r.AddGutterProvider(d.markers)
	r.AddGutterClickHandler(d.gutterClick)
	r.AddStatusProvider(d.status)
	r.AddStatusClickHandler(func(v *View, id string) bool {
		if id != statusDebugID {
			return false
		}
		d.dbg().ToggleListen()
		return true
	})
	return d
}

func (d *debugDock) dbg() *controller.Debug { return d.v.ctl.Debug }

func (d *debugDock) frame(v *View, gtx layout.Context) {
	db := d.dbg()
	on := db.Available && v.ctl.Project.Open
	if on != d.installed {
		d.installed = on
		if on {
			d.install()
		} else {
			d.uninstall()
		}
	}
	if d.seen.brk != db.BreakVersion {
		d.seen.brk = db.BreakVersion
		if on {
			v.ShowTool(ToolDebug)
			v.Raise()
		}
	}
	if d.seen.hints != db.HintsVersion {
		d.seen.hints = db.HintsVersion
		if on && len(db.Hints) > 0 {
			v.ShowTool(ToolDebug)
		}
	}
}

func (d *debugDock) install() {
	r := d.v.Registry
	r.AddToolWindow(ToolWindow{
		ID: ToolDebug, Title: "Debug", Glyph: atoms.GlyphDebug, Order: 25, Share: 0.45,
		Mac: "Cmd+5", PC: "Alt+5", ActionID: ActDebugView,
		Badge: func() (string, atoms.BadgeKind) {
			switch {
			case d.dbg().Paused():
				return "paused", atoms.BadgeWarning
			case len(d.dbg().Sessions) > 0:
				return fmt.Sprint(len(d.dbg().Sessions)), atoms.BadgeSuccess
			}
			return "", 0
		},
		Layout: d.layout,
	})
	for _, a := range d.actions() {
		r.AddAction(a)
	}
}

func (d *debugDock) uninstall() {
	d.v.HideTool(ToolDebug)
	d.v.Registry.RemoveToolWindow(ToolDebug)
	for _, a := range d.actions() {
		d.v.Registry.RemoveAction(a.ID)
	}
}

// actions are the debugger commands with the chords of
// organisms/debug.Shortcuts (F9 resume, F8 over, F7 into, Shift+F8 out,
// Cmd/Ctrl+F2 stop) plus Cmd/Ctrl+F8 toggle breakpoint.
func (d *debugDock) actions() []Action {
	chord := func(goos string, a keymap.Action) string {
		for _, b := range debugui.Shortcuts(goos) {
			if b.Action == a {
				return b.Chord.String()
			}
		}
		return ""
	}
	step := func(id keymap.Action, title, cmd string) Action {
		return Action{ID: id, Title: title, Mac: chord("darwin", id), PC: chord("linux", id), Menu: "Run", Section: "step", Run: func(v *View) {
			// F7 moves through an active diff tab unless a session is
			// paused (diffKeyWins).
			if id == debugui.ActionStepInto && v.routeDiffKey(true) {
				return
			}
			d.dbg().Step(cmd)
		}}
	}
	return []Action{
		step(debugui.ActionResume, "Resume", "run"),
		step(debugui.ActionStepOver, "Step Over", "stepOver"),
		step(debugui.ActionStepInto, "Step Into", "stepInto"),
		step(debugui.ActionStepOut, "Step Out", "stepOut"),
		step(debugui.ActionStop, "Stop", "stopSession"),
		{ID: debugui.ActionToggleListen, Title: "Start/Stop Listening for Xdebug", Menu: "Run", Section: "listen", Run: func(*View) { d.dbg().ToggleListen() }},
		{ID: ActToggleBreakpoint, Title: "Toggle Breakpoint", Mac: "Cmd+F8", PC: "Ctrl+F8", Menu: "Run", Section: "breakpoints", Run: func(v *View) {
			if file, line := v.CaretLine(); file != "" {
				d.dbg().ToggleBreakpoint(file, line)
			}
		}},
	}
}

// ---- panel ----------------------------------------------------------------------

func stateOf(s string) debugui.State {
	switch s {
	case "starting":
		return debugui.StateStarting
	case "running":
		return debugui.StateRunning
	case "break":
		return debugui.StateBreak
	case "stopping":
		return debugui.StateStopping
	case "stopped":
		return debugui.StateStopped
	}
	return debugui.StateNone
}

func toVariables(vs []*controller.DebugVariable) []*debugui.Variable {
	out := make([]*debugui.Variable, 0, len(vs))
	for _, v := range vs {
		out = append(out, toVariable(v))
	}
	return out
}

func toVariable(v *controller.DebugVariable) *debugui.Variable {
	if v == nil {
		return nil
	}
	name := v.Name
	if name == "" {
		name = v.FullName
	}
	out := &debugui.Variable{Name: name, FullName: v.FullName, Type: v.Type, ClassName: v.ClassName, Value: v.Value,
		HasChildren: v.HasChildren, NumChildren: v.NumChildren, Page: v.Page, PageSize: v.PageSize}
	for _, c := range v.Children {
		out.Children = append(out.Children, toVariable(c))
	}
	return out
}

func (d *debugDock) sync() {
	db, p, c := d.dbg(), d.panel, d.v.ctl
	p.Toolbar.Listening = db.Listening
	p.Toolbar.Port = 0
	if i := strings.LastIndexByte(db.Addr, ':'); i >= 0 {
		p.Toolbar.Port, _ = strconv.Atoi(db.Addr[i+1:])
	}
	p.Toolbar.State = stateOf(db.State())
	p.Toolbar.Muted = d.muted
	p.Sessions.Items = p.Sessions.Items[:0]
	for _, s := range db.Sessions {
		title := path.Base(s.File)
		if title == "." || title == "/" {
			title = s.ID
		}
		p.Sessions.Items = append(p.Sessions.Items, debugui.Session{ID: s.ID, Title: title, Detail: s.IDEKey, State: stateOf(s.Status)})
	}
	p.Sessions.Selected = db.Current
	p.Stack.Frames = p.Stack.Frames[:0]
	for _, f := range db.Frames {
		file := f.File
		if file == "" {
			file = f.URI
		}
		p.Stack.Frames = append(p.Stack.Frames, debugui.Frame{Level: f.Level, Where: f.Where, File: file, Line: f.Line})
	}
	p.Stack.Selected = db.SelectedFrame
	if d.seen.vars != db.VarsVersion {
		d.seen.vars = db.VarsVersion
		p.Variables.Roots = toVariables(db.Variables)
	}
	if d.seen.watches != db.WatchVersion {
		d.seen.watches = db.WatchVersion
		p.Watches.Items = p.Watches.Items[:0]
		for _, w := range db.Watches {
			it := debugui.Watch{Expression: w.Expr, Error: w.Error}
			if w.Value != nil {
				it.Value, it.Type = toVariable(w.Value).Display(), w.Value.Type
			}
			p.Watches.Items = append(p.Watches.Items, it)
		}
	}
	if d.seen.bps != db.BPVersion {
		d.seen.bps = db.BPVersion
		p.Breakpoints.Items = p.Breakpoints.Items[:0]
		for _, b := range db.Breakpoints {
			p.Breakpoints.Items = append(p.Breakpoints.Items, debugui.Breakpoint{ID: b.ID, File: c.Abs(b.Path), Line: b.Line, Condition: b.Condition, Enabled: b.Enabled})
		}
	}
}

func (d *debugDock) handle(gtx layout.Context) {
	db := d.dbg()
	if d.dismiss.Clicked(gtx) {
		db.DismissHints()
	}
	for _, e := range d.panel.Update(gtx) {
		switch e := e.(type) {
		case debugui.ActionEvent:
			switch e.Action {
			case debugui.ActionToggleListen:
				db.ToggleListen()
			case debugui.ActionResume:
				db.Step("run")
			case debugui.ActionStepOver:
				db.Step("stepOver")
			case debugui.ActionStepInto:
				db.Step("stepInto")
			case debugui.ActionStepOut:
				db.Step("stepOut")
			case debugui.ActionStop:
				db.Step("stopSession")
			case debugui.ActionMuteBreakpoints:
				d.toggleMute()
			}
		case debugui.FrameSelectEvent:
			db.SelectFrame(e.Level)
		case debugui.ExpandEvent:
			db.Expand(e.FullName, e.Page)
		case debugui.CopyValueEvent:
			d.v.CopyText(e.Value)
		case debugui.WatchAddEvent:
			db.SetWatches(append(db.WatchExpressions(), e.Expression))
		case debugui.WatchEditEvent:
			ws := db.WatchExpressions()
			if e.Index < len(ws) {
				ws[e.Index] = e.Expression
				db.SetWatches(ws)
			}
		case debugui.WatchRemoveEvent:
			ws := db.WatchExpressions()
			if e.Index < len(ws) {
				db.SetWatches(append(ws[:e.Index], ws[e.Index+1:]...))
			}
		case debugui.BreakpointToggleEvent:
			db.EnableBreakpoint(e.ID, e.Enabled)
		case debugui.BreakpointConditionEvent:
			db.SetConditionByID(e.ID, e.Condition)
		case debugui.BreakpointRemoveEvent:
			db.RemoveBreakpoint(e.ID)
		case debugui.BreakpointJumpEvent:
			loc := controller.AtLine(e.File, e.Line)
			d.v.ctl.OpenFile(e.File, &loc)
		case debugui.SessionSelectEvent:
			db.SelectSession(e.ID)
		case debugui.SessionStopEvent:
			db.StopSession(e.ID)
		}
	}
}

// toggleMute disables all enabled breakpoints (and enables them again);
// the muted set lives only in this window.
func (d *debugDock) toggleMute() {
	db := d.dbg()
	d.muted = !d.muted
	for _, b := range db.Breakpoints {
		if b.Enabled == d.muted {
			db.EnableBreakpoint(b.ID, !d.muted)
		}
	}
}

func (d *debugDock) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	d.sync()
	d.handle(gtx)
	hints := d.dbg().Hints
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(hints) == 0 {
				return layout.Dimensions{}
			}
			return d.layoutHints(gtx, th, hints)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return d.panel.Layout(gtx, th) }),
	)
}

// layoutHints shows the Xdebug setup hints of the first listener start
// without path mappings (one banner, dismissable).
func (d *debugDock) layoutHints(gtx layout.Context, th *tokens.Theme, hints []controller.Hint) layout.Dimensions {
	kind := molecules.BannerInfo
	var lines []string
	for _, h := range hints {
		if h.Severity == "warning" || h.Severity == "error" {
			kind = molecules.BannerWarning
		}
		lines = append(lines, "• "+h.Message)
	}
	return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, molecules.Banner{Kind: kind, Title: "Xdebug setup: no path mappings configured", Detail: strings.Join(lines, "\n")}.Fn(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return d.dismiss.Layout(gtx, th) })
			}),
		)
	})
}

// ---- gutter and status ---------------------------------------------------------------

func (d *debugDock) markers(file string) editorview.MarkerProvider {
	if !d.installed {
		return nil
	}
	db := d.dbg()
	m := editorview.LineMarkers{}
	for _, b := range db.Breakpoints {
		if b.File != file || b.Line < 1 {
			continue
		}
		k := editorview.MarkerBreakpoint
		switch {
		case !b.Enabled:
			k = editorview.MarkerBreakpointDisabled
		case b.Condition != "":
			k = editorview.MarkerBreakpointConditional
		}
		m[b.Line-1] = append(m[b.Line-1], editorview.Marker{Kind: k})
	}
	if x := db.Exec; x != nil && x.File == file && x.Line > 0 {
		m[x.Line-1] = append(m[x.Line-1], editorview.Marker{Kind: editorview.MarkerExecutionPoint})
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// gutterClick toggles a breakpoint (icon column or line numbers); with
// Alt or a right click the condition is edited.
func (d *debugDock) gutterClick(file string, ev editorview.GutterClickEvent) bool {
	if !d.installed || (ev.Area != editorview.GutterIcons && ev.Area != editorview.GutterNumbers) {
		return false
	}
	line := ev.Line + 1
	if ev.Secondary || ev.Mods&keymap.Alt != 0 {
		d.editCondition(file, line)
		return true
	}
	if ev.Area == editorview.GutterNumbers && ev.Clicks > 1 {
		return false
	}
	d.dbg().ToggleBreakpoint(file, line)
	return true
}

func (d *debugDock) editCondition(file string, line int) {
	db := d.dbg()
	cond := ""
	if b, ok := db.BreakpointAt(file, line); ok {
		cond = b.Condition
	}
	d.v.OpenChooser(ChooserSpec{
		Title: fmt.Sprintf("Breakpoint condition (%s:%d)", path.Base(file), line), Hint: "PHP expression, empty: always stop",
		Query: cond, NoList: true,
		OnChoose: func(_ int, q string) { db.SetCondition(file, line, q) },
	})
}

func (d *debugDock) status() []statusbar.Item {
	if !d.installed {
		return nil
	}
	db := d.dbg()
	// The header toolbar has the listen toggle; the footer shows the
	// listener only while it runs.
	if !db.Listening {
		return nil
	}
	it := statusbar.Item{ID: statusDebugID, Glyph: atoms.GlyphListen, Clickable: true, Text: "Xdebug", Tone: statusbar.ToneAccent}
	it.Tooltip = "Xdebug listening on " + db.Addr + " – click to stop"
	if db.Paused() {
		it.Text, it.Tone = "Xdebug: paused", statusbar.ToneWarning
	}
	return []statusbar.Item{it}
}
