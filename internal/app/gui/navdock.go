package gui

import (
	"fmt"
	"path"
	"strings"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/filetree"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/statusbar"
)

// Code navigation in the GUI (D160): jumps (Cmd+B, Cmd+Alt+B, Cmd+U,
// Cmd+click) through nav.* with the index and the language server, the
// type hierarchy, gutter marks with ↑/↓ icons, Go to Class/Symbol, Search
// Everywhere (Shift Shift), the index progress in the status bar, and the
// editor and gutter context menus (editormenu.go). The LSP features and
// Find in Files are in codedock.go and finddock.go. All state lives in
// controller.CodeNav; this file only shows it.

// Tool window IDs of the navigation feature.
const (
	ToolUsages    = "usages"
	ToolFind      = "find"
	ToolStructure = "structure"
	ToolHierarchy = "hierarchy"

	statusIndexID = "index.progress"
)

// Action IDs (shared with the action model).
const (
	ActSuper            keymap.Action = actions.NavSuper
	ActTypeHierarchy    keymap.Action = actions.NavTypeHierarchy
	ActGotoClass        keymap.Action = actions.NavGotoClass
	ActGotoSymbol       keymap.Action = actions.NavGotoSymbol
	ActSearchEverywhere keymap.Action = actions.NavSearchEverywhere
	ActFindUsages       keymap.Action = actions.EditorFindUses
	ActRenameSymbol     keymap.Action = actions.RefactorRename
	ActReformat         keymap.Action = actions.CodeReformat
	ActContextActions   keymap.Action = actions.CodeContextActions
	ActStructureView    keymap.Action = actions.ViewStructure
	ActFindInPath       keymap.Action = actions.FindInPath
	ActReplaceInPath    keymap.Action = actions.ReplaceInPath
)

// HighlightDelay is how long the caret rests before usages of the symbol
// under it are highlighted.
const HighlightDelay = 500 * time.Millisecond

// doubleShift is the longest pause between the two Shift presses of
// Search Everywhere.
const doubleShift = 400 * time.Millisecond

// navDock is the view side of code navigation.
type navDock struct {
	v     *View
	n     *controller.CodeNav
	menus menuPresenter
	root  string

	seen struct {
		choice, hierarchy, usages, actions, rename, structure, find, symbols uint64
	}

	// Shift Shift detection.
	shiftUp   time.Time
	shiftMark uint64
	shiftSkip bool

	// Symbol choosers (Go to Class/Symbol, Search Everywhere).
	sym *symbolChooser

	// Caret rest for usage highlights.
	hl struct {
		file      string
		off       int
		version   uint64
		since     time.Time
		requested bool
		applied   uint64
		session   *editorSession
	}

	usages    resultTree
	hierarchy resultTree
	structure resultTree
	structFor string
	structVer uint64
	structAt  time.Time

	find   *findDock
	rename *renameDock
	// hideNums hides the line numbers of all editors.
	hideNums bool
}

func installNav(v *View) *navDock {
	d := &navDock{v: v, n: controller.NewCodeNav(v.ctl)}
	d.find = newFindDock(d)
	d.rename = &renameDock{d: d}
	v.MenuPresenter = d.menus.present
	r := v.Registry
	r.AddFrameHandler(d.frame)
	r.AddGutterProvider(d.markers)
	// Index marks take clicks on their icons before the breakpoint toggle.
	r.gutterClicks = append([]GutterClickHandler{d.gutterClick}, r.gutterClicks...)
	r.AddStatusProvider(d.status)
	d.usages.Empty = "Find Usages (Alt+F7) on a symbol lists its usages here."
	d.hierarchy.Empty = "Type Hierarchy (Ctrl+H) on a class shows its supertypes and subtypes."
	d.structure.Empty = "No structure for this file."
	d.structure.OpenOnClick = true
	r.AddToolWindow(ToolWindow{ID: ToolStructure, Title: "Structure", Glyph: atoms.GlyphList, Side: Left, Order: 5,
		Mac: "Cmd+7", PC: "Alt+7", ActionID: ActStructureView, Layout: d.structure.layout,
		OnOpen: func() { d.structFor = "" }})
	for _, a := range d.actions() {
		r.AddAction(a)
	}
	return d
}

// actions are the handlers of the navigation actions (editor scope: run
// with the active document locked).
func (d *navDock) actions() []Action {
	ed := func(id keymap.Action, title, mac, pc, menu, section string, run func(s *editorSession)) Action {
		return Action{ID: id, Title: title, Mac: mac, PC: pc, Scope: ScopeEditor, Menu: menu, Section: section, Run: func(v *View) {
			if s := v.activeSession(); s != nil {
				run(s)
			}
		}}
	}
	glob := func(id keymap.Action, title, mac, pc, menu, section string, run func()) Action {
		return Action{ID: id, Title: title, Mac: mac, PC: pc, Menu: menu, Section: section, Run: func(*View) { run() }}
	}
	return []Action{
		ed(ActDefinition, "Go to Definition", "Cmd+B", "Ctrl+B", "Navigate", "symbol", func(s *editorSession) { d.jump(s, controller.JumpDefinition) }),
		ed(ActImplementation, "Go to Implementation", "Cmd+Alt+B", "Ctrl+Alt+B", "Navigate", "symbol", func(s *editorSession) { d.jump(s, controller.JumpImplementations) }),
		ed(ActSuper, "Go to Super Method", "Cmd+U", "Ctrl+U", "Navigate", "symbol", func(s *editorSession) { d.jump(s, controller.JumpSuper) }),
		ed(ActTypeHierarchy, "Type Hierarchy", "Ctrl+H", "Ctrl+H", "Navigate", "hierarchy", func(s *editorSession) {
			d.n.LoadTypeHierarchy(s.location())
			d.showTool(ToolHierarchy)
		}),
		ed(ActFindUsages, "Find Usages", "Alt+F7", "Alt+F7", "Edit/Find", "usages", func(s *editorSession) {
			d.n.FindUsages(s.location(), s.wordAtCaret())
		}),
		glob(ActGotoClass, "Go to Class…", "Cmd+O", "Ctrl+N", "Navigate", "goto", func() { d.openSymbols(symClasses) }),
		glob(ActGotoSymbol, "Go to Symbol…", "Cmd+Alt+O", "Ctrl+Alt+Shift+N", "Navigate", "goto", func() { d.openSymbols(symSymbols) }),
		glob(ActSearchEverywhere, "Search Everywhere", "", "", "Navigate", "goto", func() { d.openSymbols(symEverywhere) }),
		ed(ActRenameSymbol, "Rename Symbol…", "Shift+F6", "Shift+F6", "Refactor", "rename", func(s *editorSession) {
			d.n.StartRename(s.location(), s.wordAtCaret())
		}),
		ed(ActReformat, "Reformat Code", "Cmd+Alt+L", "Ctrl+Alt+L", "Code", "format", func(s *editorSession) {
			sel := s.ed.Cursor().Primary()
			d.n.Reformat(s.file, sel.Start(), sel.End(), d.v.ctl.Prefs.TabWidth)
		}),
		ed(ActContextActions, "Show Context Actions", "Alt+Enter", "Alt+Enter", "Code", "actions", func(s *editorSession) {
			sel := s.ed.Cursor().Primary()
			d.n.LoadCodeActions(s.file, sel.Start(), sel.End())
		}),
		glob(ActFindInPath, "Find in Files…", "Cmd+Shift+F", "Ctrl+Shift+F", "Edit/Find", "find", func() { d.find.openDialog(false) }),
		glob(ActReplaceInPath, "Replace in Files…", "Cmd+Shift+R", "Ctrl+Shift+R", "Edit/Find", "find", func() { d.find.openDialog(true) }),
		{ID: actions.DebugEditCondition, Title: "Edit Breakpoint Condition…", Run: func(v *View) { d.gutterBreakpoint(func(file string, line int) { v.dbgd.editCondition(file, line) }) }},
		{ID: actions.DebugEnableToggle, Title: "Enabled", Run: func(v *View) {
			d.gutterBreakpoint(func(file string, line int) {
				if b, ok := v.ctl.Debug.BreakpointAt(file, line); ok {
					v.ctl.Debug.EnableBreakpoint(b.ID, !b.Enabled)
				}
			})
		}},
		{ID: actions.GutterShowLineNumbers, Title: "Show Line Numbers", Run: func(v *View) { d.toggleLineNumbers() }},
	}
}

// showTool registers a result tool window on first use (Usages, Find,
// Hierarchy appear in the stripes once they have content) and shows it.
func (d *navDock) showTool(id string) {
	r := d.v.Registry
	if _, ok := r.ToolWindow(id); !ok {
		switch id {
		case ToolUsages:
			r.AddToolWindow(ToolWindow{ID: ToolUsages, Title: "Usages", Glyph: atoms.GlyphSearch, Order: 40, Share: 0.35, Layout: d.usages.layout})
		case ToolFind:
			r.AddToolWindow(ToolWindow{ID: ToolFind, Title: "Find", Glyph: atoms.GlyphSearch, Order: 41, Share: 0.35, Layout: d.find.layout})
		case ToolHierarchy:
			r.AddToolWindow(ToolWindow{ID: ToolHierarchy, Title: "Hierarchy", Glyph: atoms.GlyphBranch, Side: Right, Order: 50, Layout: d.hierarchy.layout})
		}
		d.v.syncTools()
	}
	d.v.ShowTool(id)
}

// ---- frame ----------------------------------------------------------------------

func (d *navDock) frame(v *View, gtx layout.Context) {
	n := d.n
	if root := v.ctl.Project.Root; root != d.root {
		d.root = root
		n.Reset()
		d.usages.set(nil)
		d.hierarchy.set(nil)
		d.structure.set(nil)
		d.structFor = ""
		if root != "" {
			n.LoadIndexStatus()
		}
	}
	d.shiftKeys(gtx)
	if d.seen.choice != n.ChoiceVersion {
		d.seen.choice = n.ChoiceVersion
		if ch := n.Choice; ch != nil {
			d.showChoice(ch)
		}
	}
	if d.seen.hierarchy != n.HierarchyVersion {
		d.seen.hierarchy = n.HierarchyVersion
		d.syncHierarchy()
	}
	if d.seen.usages != n.UsagesVersion {
		d.seen.usages = n.UsagesVersion
		d.syncUsages()
	}
	if d.seen.actions != n.ActionsVersion {
		d.seen.actions = n.ActionsVersion
		if as := n.Actions; as != nil {
			d.showCodeActions(as)
		}
	}
	if d.seen.rename != n.RenameVersion {
		d.seen.rename = n.RenameVersion
		d.rename.sync()
	}
	if sc := d.sym; d.seen.symbols != n.SymbolsVersion || sc != nil && sc.mode == symEverywhere && sc.files != len(v.ctl.Files) {
		d.seen.symbols = n.SymbolsVersion
		if sc != nil {
			sc.refresh()
		}
	}
	d.find.frame(gtx)
	d.syncStructure(gtx)
	d.highlights(gtx)
	d.menus.frame(v, gtx)
}

// jump starts a navigation from the caret of s (document locked).
func (d *navDock) jump(s *editorSession, kind string) {
	d.n.Navigate(s.location(), kind)
}

// showChoice offers several jump targets in a popup list.
func (d *navDock) showChoice(ch *controller.TargetChoice) {
	items := make([]palette.Item, 0, len(ch.Targets))
	for _, t := range ch.Targets {
		items = append(items, palette.Item{Label: t.Label(), Detail: t.Where(), Glyph: kindGlyph(t.Kind)})
	}
	d.v.OpenChooser(ChooserSpec{Title: ch.Title, Hint: "Filter", Items: items,
		OnQuery: func(q string) []palette.Item {
			var out []palette.Item
			for _, it := range items {
				if q == "" || strings.Contains(strings.ToLower(it.Label+" "+it.Detail), strings.ToLower(q)) {
					out = append(out, it)
				}
			}
			return out
		},
		OnChoose: func(i int, q string) {
			// Map the filtered index back.
			k := -1
			for j, it := range items {
				if q == "" || strings.Contains(strings.ToLower(it.Label+" "+it.Detail), strings.ToLower(q)) {
					k++
					if k == i {
						d.n.ChooseTarget(j)
						return
					}
				}
			}
			d.n.CancelChoice()
		}})
}

// kindGlyph is the icon of a symbol kind.
func kindGlyph(kind string) *atoms.Glyph {
	switch kind {
	case "class", "interface", "trait", "enum":
		return atoms.GlyphFileCode
	case "method", "function":
		return atoms.GlyphRun
	case "property", "classConstant", "constant", "enumCase", "enum case":
		return atoms.GlyphTag
	case "namespace":
		return atoms.GlyphFolder
	}
	return atoms.GlyphFileCode
}

// ---- status bar -------------------------------------------------------------------

func (d *navDock) status() []statusbar.Item {
	ix := d.n.Index
	if !ix.Busy() {
		return nil
	}
	text := "Indexing…"
	if ix.Total > 0 {
		text = fmt.Sprintf("Indexing %d/%d", ix.Done, ix.Total)
	}
	if ix.Phase == "scanning" {
		text = "Scanning files…"
	}
	return []statusbar.Item{{ID: statusIndexID, Text: text, Glyph: atoms.GlyphRefresh, Tone: statusbar.ToneMuted,
		Tooltip: "The PHP symbol index is being built; navigation results may be incomplete."}}
}

// ---- gutter ------------------------------------------------------------------------

// markers shows ↑ (implements/overrides) and ↓ (implemented/overridden)
// icons from nav.gutter.
func (d *navDock) markers(file string) editorview.MarkerProvider {
	marks := d.n.GutterMarks(file)
	if len(marks) == 0 {
		return nil
	}
	return editorview.MarkerFunc(func(line int) []editorview.Marker {
		m, ok := marks[line+1]
		if !ok {
			return nil
		}
		var out []editorview.Marker
		if len(m.Up) > 0 {
			out = append(out, editorview.Marker{Kind: editorview.MarkerIcon, Glyph: atoms.GlyphStepOut, Tone: editorview.ToneAccent})
		}
		if len(m.Down) > 0 {
			out = append(out, editorview.Marker{Kind: editorview.MarkerIcon, Glyph: atoms.GlyphStepInto, Tone: editorview.ToneInfo})
		}
		return out
	})
}

// gutterClick opens the targets of a mark clicked in the icon column.
func (d *navDock) gutterClick(file string, ev editorview.GutterClickEvent) bool {
	if ev.Area != editorview.GutterIcons || ev.Secondary || ev.Mods != 0 {
		return false
	}
	marks := d.n.GutterMarks(file)
	m, ok := marks[ev.Line+1]
	if !ok {
		return false
	}
	cur := controller.At(file, m.Range.Start.Offset)
	switch {
	case len(m.Up) > 0 && len(m.Down) > 0:
		d.v.ShowMenu("", []MenuItem{
			{Title: fmt.Sprintf("Go to Super Declaration (%d)", max(m.UpTotal, len(m.Up))), Icon: "step-out", Run: func() { d.n.GutterJump(cur, ev.Line+1, true) }},
			{Title: fmt.Sprintf("Go to Implementations (%d)", max(m.DownTotal, len(m.Down))), Icon: "step-into", Run: func() { d.n.GutterJump(cur, ev.Line+1, false) }},
		})
		return true
	case len(m.Down) > 0:
		return d.n.GutterJump(cur, ev.Line+1, false)
	case len(m.Up) > 0:
		return d.n.GutterJump(cur, ev.Line+1, true)
	}
	return false
}

// ---- Shift Shift --------------------------------------------------------------------

// shiftKeys opens Search Everywhere on two Shift presses in quick
// succession with nothing typed in between.
func (d *navDock) shiftKeys(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameShift, Optional: key.ModShift})
		if !ok {
			return
		}
		e, ok := ev.(key.Event)
		if !ok {
			continue
		}
		mark := d.activity()
		switch e.State {
		case key.Release:
			if d.shiftSkip {
				// The release of the second Shift starts no new pair.
				d.shiftSkip = false
				continue
			}
			d.shiftUp, d.shiftMark = gtx.Now, mark
		case key.Press:
			if !d.shiftUp.IsZero() && gtx.Now.Sub(d.shiftUp) <= doubleShift && mark == d.shiftMark {
				d.shiftUp, d.shiftSkip = time.Time{}, true
				d.openSymbols(symEverywhere)
			}
		}
	}
}

// activity changes with typing (the active buffer's version), so Shift
// held for capital letters does not count as a double Shift.
func (d *navDock) activity() uint64 {
	s := d.v.activeSession()
	if s == nil {
		return 0
	}
	s.doc.Lock()
	defer s.doc.Unlock()
	return s.doc.Buffer().Version()
}

// ---- usage highlights ------------------------------------------------------------

// highlights asks for the usages of the symbol under a resting caret and
// shows them in the editor.
func (d *navDock) highlights(gtx layout.Context) {
	v := d.v
	s := v.activeSession()
	h := &d.hl
	if s == nil || !strings.EqualFold(path.Ext(s.file), ".php") || !v.ctl.Status.LSPRunning {
		h.session = nil
		return
	}
	s.doc.Lock()
	defer s.doc.Unlock()
	buf := s.doc.Buffer()
	sel := s.ed.Cursor().Primary()
	off, ver := sel.Head, buf.Version()
	if h.session != s || h.file != s.file || h.off != off || h.version != ver {
		if h.session != nil && h.session != s {
			h.session.ed.SetHighlights(0, nil)
		}
		h.session, h.file, h.off, h.version, h.since, h.requested = s, s.file, off, ver, gtx.Now, false
		if hs := d.n.Highlights; hs.File == s.file && (hs.Version != ver || !inHighlight(hs, off)) {
			d.n.ClearHighlights()
		}
	}
	if !h.requested && sel.Empty() {
		if due := h.since.Add(HighlightDelay); gtx.Now.Before(due) {
			gtx.Execute(op.InvalidateCmd{At: due})
		} else {
			h.requested = true
			d.n.Highlight(s.file, off, ver)
		}
	}
	if h.applied != d.n.HighlightsVersion {
		h.applied = d.n.HighlightsVersion
		hs := d.n.Highlights
		if hs.File != s.file || hs.Version != ver || len(hs.Ranges) < 2 {
			s.ed.SetHighlights(0, nil)
			return
		}
		out := make([]editorview.Highlight, 0, len(hs.Ranges))
		for _, r := range hs.Ranges {
			out = append(out, editorview.Highlight{Start: r.Start, End: r.End, Write: r.Write})
		}
		s.ed.SetHighlights(ver, out)
	}
}

func inHighlight(h controller.Highlights, off int) bool {
	for _, r := range h.Ranges {
		if off >= r.Start && off <= r.End {
			return true
		}
	}
	return false
}

// ---- results: hierarchy and usages -------------------------------------------------

func (d *navDock) syncHierarchy() {
	h := d.n.Hierarchy
	if h == nil {
		d.hierarchy.set(nil)
		return
	}
	var conv func(ns []controller.HierarchyNode) []*resultNode
	conv = func(ns []controller.HierarchyNode) []*resultNode {
		out := make([]*resultNode, 0, len(ns))
		for _, x := range ns {
			rn := &resultNode{Name: shortFQN(x.FQN, x.Name), Detail: x.Path, Glyph: kindGlyph(x.Kind), Expanded: true, Children: conv(x.Children)}
			if x.File != "" {
				t := x.NavTarget
				rn.Open = func() { d.n.OpenTarget(d.v.currentLocation(), t) }
			} else {
				rn.Detail = "not indexed"
			}
			out = append(out, rn)
		}
		return out
	}
	cls := h.Class
	root := &resultNode{Name: shortFQN(cls.FQN, cls.Name), Value: cls.FQN, Detail: cls.Path, Glyph: kindGlyph(cls.Kind), Expanded: true,
		Open: func() { d.n.OpenTarget(d.v.currentLocation(), cls) }}
	sup := &resultNode{Name: "Supertypes", Detail: fmt.Sprint(countNodes(h.Supertypes)), Expanded: true, Children: conv(h.Supertypes)}
	sub := &resultNode{Name: "Subtypes", Detail: fmt.Sprint(countNodes(h.Subtypes)), Expanded: true, Children: conv(h.Subtypes)}
	if h.Truncated {
		sub.Detail += " (truncated)"
	}
	root.Children = []*resultNode{sup, sub}
	d.hierarchy.set([]*resultNode{root})
	d.hierarchy.Header = headerText("Type Hierarchy", cls.FQN)
}

func countNodes(ns []controller.HierarchyNode) int {
	n := len(ns)
	for _, x := range ns {
		n += countNodes(x.Children)
	}
	return n
}

func shortFQN(fqn, name string) string {
	if name != "" {
		return name
	}
	if i := strings.LastIndexByte(fqn, '\\'); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

func (d *navDock) syncUsages() {
	u := d.n.Usages
	if u == nil {
		d.usages.set(nil)
		return
	}
	roots := make([]*resultNode, 0, len(u.Groups))
	for _, g := range u.Groups {
		gn := &resultNode{Name: path.Base(g.Path), Value: path.Dir(g.Path), Detail: plural(len(g.Items), "usage"), Glyph: filetree.Glyph(&filetree.Node{Name: path.Base(g.Path)}), Expanded: true}
		for _, it := range g.Items {
			loc := it.Location
			gn.Children = append(gn.Children, &resultNode{Name: fmt.Sprintf("%d:%d", loc.Range.Start.Line, loc.Range.Start.Col), Value: strings.TrimSpace(it.LineText),
				Open: func() { d.v.ctl.GoTo(d.v.currentLocation(), loc); d.v.focusEditor = true }})
		}
		roots = append(roots, gn)
	}
	d.usages.set(roots)
	d.usages.Header = headerText("Usages of "+orDefault(u.Symbol, "symbol"), fmt.Sprintf("%s in %s", plural(u.Total, "usage"), plural(len(u.Groups), "file")))
	if u.Total > 0 {
		d.showTool(ToolUsages)
	}
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	if strings.HasSuffix(what, "ch") || strings.HasSuffix(what, "s") {
		return fmt.Sprintf("%d %ses", n, what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// ---- structure ----------------------------------------------------------------

// structureDelay is how long edits rest before the structure reloads.
const structureDelay = 700 * time.Millisecond

// syncStructure reloads the structure of the active file while the tool
// window is open.
func (d *navDock) syncStructure(gtx layout.Context) {
	v := d.v
	if !v.ToolShown(ToolStructure) {
		return
	}
	t := v.ctl.ActiveTab()
	if t == nil || t.Doc == nil {
		if d.structFor != "" {
			d.structFor = ""
			d.structure.set(nil)
		}
		return
	}
	t.Doc.Lock()
	ver := t.Doc.Buffer().Version()
	t.Doc.Unlock()
	switch {
	case d.structFor != t.File:
		d.structFor, d.structVer, d.structAt = t.File, ver, gtx.Now
		d.n.LoadStructure(t.File)
	case d.structVer != ver:
		if d.structAt.IsZero() || gtx.Now.Sub(d.structAt) > structureDelay*4 {
			d.structAt = gtx.Now
		}
		if due := d.structAt.Add(structureDelay); gtx.Now.Before(due) {
			gtx.Execute(op.InvalidateCmd{At: due})
		} else {
			d.structVer, d.structAt = ver, gtx.Now
			d.n.LoadStructure(t.File)
		}
	}
	if d.seen.structure != d.n.StructureVersion {
		d.seen.structure = d.n.StructureVersion
		st := d.n.Structure
		if st == nil || st.File != t.File {
			return
		}
		file := st.File
		var conv func(ns []controller.OutlineNode) []*resultNode
		conv = func(ns []controller.OutlineNode) []*resultNode {
			out := make([]*resultNode, 0, len(ns))
			for _, x := range ns {
				off := x.Offset
				out = append(out, &resultNode{Name: x.Name, Value: x.Detail, Detail: strings.Join(x.Modifiers, " "), Glyph: kindGlyph(x.Kind),
					Expanded: true, Children: conv(x.Children),
					Open: func() { v.ctl.GoTo(v.currentLocation(), controller.At(file, off)); v.focusEditor = true }})
			}
			return out
		}
		d.structure.set(conv(st.Nodes))
		d.structure.Empty = "No structure for this file."
		if st.Err != "" {
			d.structure.Empty = st.Err
		}
	}
}

// ---- gutter and line numbers ------------------------------------------------------

// gutterBreakpoint runs fn for the line of the last gutter menu.
func (d *navDock) gutterBreakpoint(fn func(file string, line int)) {
	if s := d.v.activeSession(); s != nil && s.menuLine > 0 && d.v.dbgd != nil && d.v.dbgd.installed {
		fn(s.file, s.menuLine)
	}
}

// toggleLineNumbers shows or hides the line numbers of all editors.
func (d *navDock) toggleLineNumbers() {
	d.hideNums = !d.hideNums
	for _, s := range d.v.editors {
		s.ed.HideLineNumbers = d.hideNums
	}
}

// wordAtCaret is the identifier at the caret (document locked).
func (s *editorSession) wordAtCaret() string {
	buf := s.doc.Buffer()
	off := s.ed.Cursor().Primary().Head
	line := buf.OffsetToPos(off).Line
	ls := buf.LineStart(line)
	text := buf.Line(line)
	col := min(max(off-ls, 0), len(text))
	isWord := func(b byte) bool {
		return b == '_' || b == '$' || b == '\\' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
	}
	a, b := col, col
	for a > 0 && isWord(text[a-1]) {
		a--
	}
	for b < len(text) && isWord(text[b]) {
		b++
	}
	return strings.TrimLeft(string(text[a:b]), "\\")
}

// Close stops the event subscription.
func (d *navDock) Close() { d.n.Close() }
