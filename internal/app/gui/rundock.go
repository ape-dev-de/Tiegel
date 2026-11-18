package gui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/run"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Run tool window (AP15.1): the header holds the run configuration
// choice and the Run button, below one tab per run with Stop/Rerun and
// its output; file locations in the output open the file at the line.
// Ctrl+R runs the selected configuration, Ctrl+Shift+R the current file.

// Run tool window and action IDs.
const (
	ToolRun = "run"

	ActRun        keymap.Action = "run.run"
	ActRunFile    keymap.Action = "run.runFile"
	ActRerun      keymap.Action = "run.rerun"
	ActRunView    keymap.Action = "view.run"
	ActRunStop    keymap.Action = "run.stop"
	ActRunConfigs keymap.Action = "run.editConfigurations"
)

type runDock struct {
	v       *View
	project string

	cfgSel  *molecules.Dropdown
	cfgSeen uint64
	cfgIDs  []string

	runBtn, fileBtn, stopBtn, rerunBtn atoms.Button
	more                               atoms.IconButton

	tabs     map[string]*molecules.Tab
	rows     []molecules.ListRow
	list     molecules.ScrollList
	shown    string
	lineSeen uint64
	// editor is the configuration editor of the open popup.
	editor *runConfigEditor
}

func installRun(v *View) *runDock {
	d := &runDock{v: v, tabs: map[string]*molecules.Tab{},
		cfgSel:   molecules.NewSelect("No run configuration"),
		runBtn:   atoms.Button{Text: "Run", Kind: atoms.Primary, Glyph: atoms.GlyphRun},
		fileBtn:  atoms.Button{Text: "Run File", Kind: atoms.Ghost, Glyph: atoms.GlyphFileCode},
		stopBtn:  atoms.Button{Text: "Stop", Kind: atoms.Secondary, Glyph: atoms.GlyphStop},
		rerunBtn: atoms.Button{Text: "Rerun", Kind: atoms.Secondary, Glyph: atoms.GlyphRefresh},
		more:     atoms.IconButton{Glyph: atoms.GlyphMore, Description: "Run configurations"},
	}
	r := v.Registry
	r.AddToolWindow(ToolWindow{
		ID: ToolRun, Title: "Run", Glyph: atoms.GlyphRun, Side: Bottom, Order: 15, Share: 0.35,
		Mac: "Cmd+4", PC: "Alt+4", ActionID: ActRunView,
		Badge: func() (string, atoms.BadgeKind) {
			if n := v.ctl.Run.ActiveCount(); n > 0 {
				return fmt.Sprint(n), atoms.BadgeSuccess
			}
			return "", 0
		},
		Layout: d.layout,
		OnOpen: func() { v.ctl.Run.LoadConfigs() },
	})
	r.AddFrameHandler(d.frame)
	for _, a := range d.actions() {
		r.AddAction(a)
	}
	return d
}

func (d *runDock) runs() *controller.Runs { return d.v.ctl.Run }

// actions are the run commands (catalog chords: Ctrl+R, Ctrl+Shift+R,
// Cmd+R outside the editor; PC Shift+F10, Ctrl+Shift+F10, Ctrl+F5).
func (d *runDock) actions() []Action {
	return []Action{
		{ID: ActRun, Title: "Run", Mac: "Ctrl+R", PC: "Shift+F10", Menu: "Run", Section: "run", Run: func(v *View) { d.runSelected() }},
		{ID: ActRunFile, Title: "Run Current File", Mac: "Ctrl+Shift+R", PC: "Ctrl+Shift+F10", Menu: "Run", Section: "run", Run: func(v *View) { d.runFile() }},
		// Rerun has no own Mac chord: the catalog binds Cmd+R in the tool
		// window context; the focused editor keeps it for Replace.
		{ID: ActRerun, Title: "Rerun", PC: "Ctrl+F5", Menu: "Run", Section: "run", Run: func(v *View) {
			if s := v.activeSession(); s != nil && s.ed.Focused() && v.Registry.Scheme().Preset() == keymap.PresetMac {
				v.pendingEdit = keymap.Replace
				return
			}
			d.rerun()
		}},
		{ID: ActRunStop, Title: "Stop Run", Menu: "Run", Section: "run", Run: func(*View) {
			if t := d.runs().Current(); t != nil && t.Active() {
				d.runs().Stop(t.Info.ID)
			}
		}},
		{ID: ActRunConfigs, Title: "Edit Run Configurations…", Menu: "Run", Section: "config", Run: func(*View) { d.openMenu() }},
	}
}

// frame follows the project and shows the window when a run started.
func (d *runDock) frame(v *View, _ layout.Context) {
	if root := v.ctl.Project.Root; root != d.project {
		d.project = root
		v.ctl.Run.ProjectChanged()
	}
	if v.ctl.Run.Started {
		v.ctl.Run.Started = false
		v.ShowTool(ToolRun)
	}
}

// currentPHPFile returns the active editor's file if it is a PHP file.
func (d *runDock) currentFile() string {
	if t := d.v.ctl.ActiveTab(); t != nil {
		return t.File
	}
	return ""
}

func isPHPFile(f string) bool {
	return strings.HasSuffix(strings.ToLower(f), ".php")
}

func (d *runDock) runSelected() {
	file := d.currentFile()
	if !isPHPFile(file) {
		file = ""
	}
	d.runs().RunSelected(file)
}

func (d *runDock) runFile() {
	file := d.currentFile()
	if !isPHPFile(file) {
		d.v.ctl.Status.Notice = "Run Current File: the active file is not a PHP file"
		return
	}
	d.runs().RunFile(file)
}

func (d *runDock) rerun() {
	if t := d.runs().Current(); t != nil {
		d.runs().Rerun(t.Info.ID)
	}
}

// openMenu lists the configuration commands.
func (d *runDock) openMenu() {
	r := d.runs()
	if len(r.Types) == 0 {
		r.LoadConfigs()
	}
	var newItems []MenuItem
	for _, t := range r.Types {
		newItems = append(newItems, MenuItem{Title: t.Label, Run: func() { d.openEditor(run.Config{Type: t.ID}, t) }})
	}
	if len(newItems) == 0 {
		newItems = append(newItems, MenuItem{Title: "Loading types…", Disabled: true})
	}
	items := []MenuItem{{Title: "New Configuration", Children: newItems}}
	if cfg := r.SelectedConfig(); cfg != nil {
		c := cfg.Config
		items = append(items,
			MenuItem{Title: "Edit “" + c.Name + "”…", Run: func() {
				if t, ok := d.typeOf(c.Type); ok {
					d.openEditor(c, t)
				}
			}},
			MenuItem{Title: "Delete “" + c.Name + "”", Run: func() {
				d.v.Confirm("Delete run configuration “"+c.Name+"”?", "The configuration is removed from the project settings.", "Delete", func() { r.DeleteConfig(c.ID) })
			}})
	}
	items = append(items, MenuItem{Separator: true},
		MenuItem{Title: "Run Current File", Run: d.runFile})
	d.v.ShowMenu("Run Configurations", items)
}

func (d *runDock) typeOf(id string) (api.RunConfigTypeInfo, bool) {
	for _, t := range d.runs().Types {
		if t.ID == id {
			return t, true
		}
	}
	return api.RunConfigTypeInfo{}, false
}

// syncConfigs rebuilds the configuration choice.
func (d *runDock) syncConfigs() {
	r := d.runs()
	if d.cfgSeen == r.Version && d.cfgSel.Options != nil {
		return
	}
	d.cfgSeen = r.Version
	opts := make([]molecules.DropdownOption, 0, len(r.Configs))
	d.cfgIDs = d.cfgIDs[:0]
	d.cfgSel.Selected = -1
	for i, c := range r.Configs {
		o := molecules.DropdownOption{Label: c.Name, Detail: c.TypeLabel, Glyph: atoms.GlyphRun}
		if c.Error != "" {
			o.Badge, o.BadgeKind = "!", atoms.BadgeWarning
			o.Detail = c.Error
		}
		opts = append(opts, o)
		d.cfgIDs = append(d.cfgIDs, c.ID)
		if c.Selected {
			d.cfgSel.Selected = i
		}
	}
	d.cfgSel.Options = opts
}

// stateGlyph is the tab icon of a run.
func stateGlyph(i run.Info) *atoms.Glyph {
	switch {
	case i.Active():
		return atoms.GlyphRun
	case i.State == run.StateStopped:
		return atoms.GlyphStop
	case i.ExitCode == 0:
		return atoms.GlyphDone
	}
	return atoms.GlyphError
}

// runSummary describes a run for the header line.
func runSummary(i run.Info) string {
	dur := time.Duration(i.DurationMs) * time.Millisecond
	s := controller.RunState(i)
	if !i.Active() {
		s += " after " + dur.Round(time.Millisecond).String()
	}
	if i.Where != "" {
		s += " · " + i.Where
	}
	return s + " · " + i.Display
}

func (d *runDock) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	r := d.runs()
	d.syncConfigs()
	if i, ok := d.cfgSel.Update(gtx); ok && i >= 0 && i < len(d.cfgIDs) {
		r.SelectConfig(d.cfgIDs[i])
	}
	if d.runBtn.Clicked(gtx) {
		d.runSelected()
	}
	if d.fileBtn.Clicked(gtx) {
		d.runFile()
	}
	if d.more.Clicked(gtx) {
		d.openMenu()
	}
	cur := r.Current()
	if cur != nil {
		if d.stopBtn.Clicked(gtx) && cur.Active() {
			r.Stop(cur.Info.ID)
		}
		if d.rerunBtn.Clicked(gtx) {
			r.Rerun(cur.Info.ID)
		}
	}
	for id, t := range d.tabs {
		sel, closed := t.Update(gtx)
		switch {
		case closed:
			r.CloseTab(id)
		case sel:
			r.Select(id)
		}
	}

	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.layoutHeader(gtx, th) }),
	}
	if len(r.Tabs) > 0 {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.layoutTabs(gtx, th) }))
	}
	if cur != nil {
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.layoutRunBar(gtx, th, cur) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return d.layoutOutput(gtx, th, cur) }))
	} else {
		children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			hint := "No runs yet. Run (Ctrl+R) starts the selected configuration, Run File (Ctrl+Shift+R) the current PHP file."
			return layout.Inset{Left: th.Space.S, Top: th.Space.S}.Layout(gtx, atoms.Muted(hint).Fn(th))
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// layoutHeader draws the configuration choice and the run buttons.
func (d *runDock) layoutHeader(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	gap := func(w layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.XS}.Layout(gtx, w)
		})
	}
	return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(320))
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return d.cfgSel.Layout(gtx, th)
			}),
			gap(func(gtx layout.Context) layout.Dimensions { return d.runBtn.Layout(gtx, th) }),
			gap(func(gtx layout.Context) layout.Dimensions { return d.fileBtn.Layout(gtx, th) }),
			gap(func(gtx layout.Context) layout.Dimensions { return d.more.Layout(gtx, th) }),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
		)
	})
}

// layoutTabs draws one tab per run.
func (d *runDock) layoutTabs(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	r := d.runs()
	cur := r.Current()
	seen := map[string]bool{}
	var children []layout.FlexChild
	for _, rt := range r.Tabs {
		id := rt.Info.ID
		seen[id] = true
		t := d.tabs[id]
		if t == nil {
			t = molecules.NewTab("")
			d.tabs[id] = t
		}
		t.Title, t.Tooltip, t.Glyph = rt.Info.Name, rt.Info.Display, stateGlyph(rt.Info)
		t.Active, t.Modified = rt == cur, rt.Active()
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return t.Layout(gtx, th) }))
	}
	for id := range d.tabs {
		if !seen[id] {
			delete(d.tabs, id)
		}
	}
	return layout.Flex{}.Layout(gtx, children...)
}

// layoutRunBar shows the state of the current run with Stop and Rerun.
func (d *runDock) layoutRunBar(gtx layout.Context, th *tokens.Theme, cur *controller.RunTab) layout.Dimensions {
	label := atoms.Muted(runSummary(cur.Info)).SingleLine()
	if cur.Info.Error != "" {
		label = atoms.Muted(runSummary(cur.Info) + " – " + cur.Info.Error).SingleLine()
	}
	btns := []*atoms.Button{&d.rerunBtn}
	if cur.Active() {
		btns = []*atoms.Button{&d.stopBtn, &d.rerunBtn}
	}
	children := []layout.FlexChild{}
	for _, b := range btns {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
		}))
	}
	children = append(children, layout.Flexed(1, label.Fn(th)))
	return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	})
}

// layoutOutput draws the output lines; lines with a file location are
// clickable and open the first location.
func (d *runDock) layoutOutput(gtx layout.Context, th *tokens.Theme, cur *controller.RunTab) layout.Dimensions {
	lines := cur.Lines
	if len(d.rows) < len(lines) {
		d.rows = append(d.rows, make([]molecules.ListRow, len(lines)-len(d.rows))...)
	}
	// Follow the output while the end is visible (or a new run is shown).
	if d.shown != cur.Info.ID || d.lineSeen != cur.Version {
		follow := d.shown != cur.Info.ID || d.list.LastVisible() >= len(lines)-8
		d.shown, d.lineSeen = cur.Info.ID, cur.Version
		if follow {
			d.list.ScrollTo(len(lines))
		}
	}
	for i := range lines {
		if len(lines[i].Links) == 0 {
			continue
		}
		if clicked, _ := d.rows[i].Update(gtx); clicked {
			d.openLink(lines[i].Links[0])
		}
	}
	return d.list.Layout(gtx, th, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
		l := lines[i]
		w := outputLine(th, l)
		if len(l.Links) == 0 {
			return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx, w)
		}
		return d.rows[i].Layout(gtx, th, w)
	})
}

// openLink opens the file of an output location at its line.
func (d *runDock) openLink(lk run.Link) {
	d.v.ctl.OpenFile(lk.File, ptr(controller.AtLine(lk.File, lk.Line)))
}

// outputLine renders a line: stderr in the danger color, Tiegel's own
// lines muted, file locations in the accent color.
func outputLine(th *tokens.Theme, l run.Line) layout.Widget {
	col := th.Color.Text
	switch l.Stream {
	case run.StreamStderr:
		col = th.Color.Danger
	case run.StreamSystem:
		col = th.Color.TextMuted
	}
	if len(l.Links) == 0 {
		return atoms.Code(l.Text).WithColor(col).SingleLine().Fn(th)
	}
	var parts []layout.FlexChild
	pos := 0
	add := func(s string, c color.NRGBA) {
		if s == "" {
			return
		}
		parts = append(parts, layout.Rigid(atoms.Code(s).WithColor(c).SingleLine().Fn(th)))
	}
	for _, lk := range l.Links {
		if lk.Start < pos || lk.End > len(l.Text) {
			continue
		}
		add(l.Text[pos:lk.Start], col)
		add(l.Text[lk.Start:lk.End], th.Color.Accent)
		pos = lk.End
	}
	add(l.Text[pos:], col)
	return func(gtx layout.Context) layout.Dimensions { return layout.Flex{}.Layout(gtx, parts...) }
}

// confirmClose asks before closing the project or quitting while runs
// are active (setting run.confirmClose); proceed runs when closing may
// go on. The decision is sent for the project the check was made for and
// acknowledged by the backend before proceed runs (which may switch the
// project or quit).
func (d *runDock) confirmClose(proceed func()) {
	r := d.runs()
	r.CloseCheck(func(res api.RunCloseCheck) {
		n := len(res.Active)
		if n == 0 || res.Policy == run.CloseKeep {
			proceed()
			return
		}
		if res.Policy == run.CloseTerminate {
			r.StopAll(res.Project, proceed)
			return
		}
		what := "1 process is"
		if n > 1 {
			what = fmt.Sprintf("%d processes are", n)
		}
		d.v.OpenPopup(&Popup{Title: what + " still running", Centered: true,
			Detail: "Terminate them, or keep them running in the background? (Settings › Run decides without asking.)",
			Buttons: []*PopupButton{
				{Text: "Terminate", Kind: atoms.Primary, OnClick: func() { r.StopAll(res.Project, proceed) }},
				{Text: "Keep Running", Kind: atoms.Secondary, OnClick: func() { r.KeepAll(res.Project, proceed) }},
				{Text: "Cancel", Kind: atoms.Ghost},
			}})
	})
}
