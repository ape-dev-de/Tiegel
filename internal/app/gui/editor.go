package gui

import (
	"fmt"
	"image"
	"strings"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight/tshighlight"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/editor/search"
	"github.com/ape-dev-de/tiegel/internal/editor/snippet"
	"github.com/ape-dev-de/tiegel/internal/editor/wrap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/completion"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/findbar"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// HoverDelay is how long the pointer rests before hover information is
// requested.
const HoverDelay = 600 * time.Millisecond

// editorSession is the editor widget of one open tab. The widget edits the
// shared buffer directly; every access happens with the document locked
// (see api.Doc), and the controller is never asked to lock.
type editorSession struct {
	tab   *controller.Tab
	group *controller.EditorGroup
	file  string
	doc   controller.DocHandle
	ed    *editorview.Editor
	hl    *tshighlight.Highlighter

	diagSeen  uint64
	caretText string

	hoverTag   int
	hoverIn    bool
	hoverPos   image.Point
	hoverSince time.Time
	hoverDone  bool
	cmdClick   *image.Point
	// ctxClick and gutterCtx are pending context clicks (editormenu.go);
	// menuLine is the line of the last gutter menu.
	ctxClick  *image.Point
	gutterCtx int
	menuLine  int

	query     *search.Query
	findKey   [4]int
	ignoreEdt bool

	// wrapToggle overrides the soft wrap setting for this editor (View ›
	// Soft-Wrap): 0 follows the setting, 1 on, -1 off.
	wrapToggle int8

	// snip is the tab-stop session of an expanded live template
	// (templatedock.go); snipOff removes its buffer listener.
	snip    *snippet.Session
	snipOff func()

	inlay inlayState // inlay hints (inlays.go)
}

// softWrap reports whether the editor wraps lines: the per-editor toggle,
// else editor.softWrap and its file patterns (D285).
func (s *editorSession) softWrap(p controller.Prefs) bool {
	if s.wrapToggle != 0 {
		return s.wrapToggle > 0
	}
	return wrap.Enabled(p.SoftWrap, p.SoftWrapFiles, s.file)
}

// applyDisplayPrefs sets soft wrap, whitespace marks and the right margin
// of the editor from the preferences and the file's .editorconfig.
func (s *editorSession) applyDisplayPrefs(p controller.Prefs) {
	s.ed.SoftWrap = s.softWrap(p)
	s.ed.Whitespace = editorview.ParseWhitespaceMode(p.ShowWhitespace)
	margin := p.RightMargin
	if s.tab != nil {
		margin = s.tab.RightMarginFor(margin)
	}
	if !p.ShowRightMargin {
		margin = 0
	}
	s.ed.RightMargin = margin
}

// sessionKey identifies an editor widget: one per group and file, so a
// file open in two groups has two carets and scroll positions on the
// same shared buffer.
type sessionKey struct {
	group *controller.EditorGroup
	file  string
}

// session returns the editor of tab t in group g, creating it on first
// use.
func (v *View) session(g *controller.EditorGroup, t *controller.Tab) *editorSession {
	if t == nil || t.Doc == nil || g == nil {
		return nil
	}
	k := sessionKey{g, t.File}
	if s := v.editors[k]; s != nil && s.doc == t.Doc {
		s.tab = t
		return s
	} else if s != nil {
		v.closeSession(k, s)
	}
	t.Doc.Lock()
	buf := t.Doc.Buffer()
	ed := editorview.New(buf)
	// The editor follows the view's platform (its window chords and menus
	// do), not the build's: one keymap per window.
	ed.Keymap = v.Registry.EditorKeymap()
	ed.OnCopy = clipHistory.Push
	if lc, ok := editorview.LineComments[t.Language]; ok {
		ed.LineComment = lc
	}
	hl := tshighlight.New(buf, tshighlight.Options{Path: t.File, Invalidate: func() { v.Invalidate() }})
	ed.Highlighter = hl
	ed.SetFoldRanges(highlight.BraceFolds(buf.Snapshot()))
	if off, ok := v.restoreCarets[t.File]; ok {
		delete(v.restoreCarets, t.File)
		ed.SetCaret(min(max(off, 0), buf.Len()))
	}
	t.Doc.Unlock()
	s := &editorSession{tab: t, file: t.File, doc: t.Doc, ed: ed, hl: hl, group: g}
	v.editors[k] = s
	return s
}

// activeSession returns the editor of the active tab of the active
// group.
func (v *View) activeSession() *editorSession {
	return v.session(v.ctl.ActiveGroup(), v.ctl.ActiveTab())
}

// closeStaleEditors releases editors whose tabs were closed (in their
// group) or whose group went away.
func (v *View) closeStaleEditors() {
	live := map[sessionKey]controller.DocHandle{}
	for _, g := range v.ctl.Groups() {
		tabs, _ := v.ctl.GroupTabs(g)
		for _, t := range tabs {
			if t.Doc != nil {
				live[sessionKey{g, t.File}] = t.Doc
			}
		}
	}
	for k, s := range v.editors {
		if d, ok := live[k]; ok && d == s.doc {
			continue
		}
		v.closeSession(k, s)
	}
	for g := range v.tabBars {
		if v.ctl.GroupIndex(g) < 0 {
			delete(v.tabBars, g)
		}
	}
}

func (v *View) closeSession(k sessionKey, s *editorSession) {
	s.doc.Lock()
	s.endSnippet()
	s.ed.Close()
	s.hl.Close()
	s.doc.Unlock()
	delete(v.editors, k)
}

// Close releases all editors.
func (v *View) Close() {
	for k, s := range v.editors {
		v.closeSession(k, s)
	}
	if v.navd != nil {
		v.navd.Close()
	}
}

// currentLocation returns the caret location of the active editor.
func (v *View) currentLocation() controller.Location {
	s := v.activeSession()
	if s == nil {
		return controller.Location{}
	}
	s.doc.Lock()
	defer s.doc.Unlock()
	return s.location()
}

// location is currentLocation with the document locked.
func (s *editorSession) location() controller.Location {
	return controller.At(s.file, s.ed.Cursor().Primary().Head)
}

// ---- editor layout ------------------------------------------------------------

// layoutEditor draws the editor s of a group; only the active group's
// editor gets reveals, editor shortcuts, popups and the keyboard focus.
func (v *View) layoutEditor(gtx layout.Context, s *editorSession, active bool) layout.Dimensions {
	c := v.ctl
	if active {
		v.editorKeys.build(v.Registry, ScopeEditor, s.ed)
	}
	s.doc.Lock()
	defer s.doc.Unlock()
	buf := s.doc.Buffer()

	if r := c.Reveal; active && r != nil && r.File == s.file {
		c.Reveal = nil
		off := r.Range.Start.Offset
		if off < 0 { // by line (controller.AtLine)
			off = buf.LineStart(min(max(r.Range.Start.Line-1, 0), buf.LineCount()-1))
		}
		s.ed.SetCaret(min(max(off, 0), buf.Len()))
		v.focusEditor = true
	}
	if rs, ok := s.hl.TakeFolds(); ok {
		s.ed.SetFoldRanges(rs)
	}
	if s.diagSeen != c.DiagVersion {
		s.diagSeen = c.DiagVersion
		s.ed.SetDiagnostics(toEditorDiags(c.Diagnostics(s.file), buf.Len(), v.analysisHint()))
	}
	s.ed.TabWidth, s.ed.InsertSpaces = c.Prefs.TabWidth, c.Prefs.InsertSpaces
	if s.tab != nil { // .editorconfig of the file wins over the preferences
		s.ed.TabWidth, s.ed.InsertSpaces = s.tab.TabWidthFor(c.Prefs.TabWidth), s.tab.InsertSpacesFor(c.Prefs.InsertSpaces)
	}
	s.applyDisplayPrefs(c.Prefs)
	v.inlayHints(gtx, s, buf.Version())
	s.ed.Keymap = v.Registry.EditorKeymap() // follows keymap changes
	s.ed.SetGutterMarkers(v.Registry.markers(s.file))
	s.ed.SetGutterColumn(v.Registry.gutterColumn(s.file))

	// The window handles popup navigation and editor shortcuts before the
	// widget sees the keys.
	if active {
		v.startSnippet(s)
		v.templateKeys(gtx, s)
		v.popupKeys(gtx, s)
		v.editorKeys.process(gtx, v)
		v.hoverInput(gtx, s)
		if v.focusEditor && v.chooseMode == chooseNone && !v.settingsOpen && v.popup == nil {
			v.focusEditor = false
			s.ed.Focus(gtx)
		}
		if a := v.pendingEdit; a != "" {
			v.pendingEdit = ""
			s.ed.Do(gtx, a)
		}
	}

	dims := s.ed.Layout(gtx, v.th)
	v.editorEvents(gtx, s, active)
	s.tab.Modified = buf.Modified()
	s.caretText = caretText(buf, s.ed.Cursor().Primary())
	if !active {
		return dims
	}
	if v.findOpen {
		v.updateFindCount(s)
	}

	// Hover tracking above the widget; events pass through to it.
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &s.hoverTag)
	area.Pop()
	pass.Pop()
	v.hoverAfterLayout(gtx, s)
	v.navd.afterEditor(gtx, s)
	v.layoutPopups(gtx, s, dims.Size)
	return dims
}

func (v *View) layoutNoEditor(gtx layout.Context) layout.Dimensions {
	th := v.th
	lines := []string{
		"No file open",
		"Go to File    " + v.Registry.ChordText(ActQuickOpen, v.goos),
		"Recent Files    " + v.Registry.ChordText(ActRecentFiles, v.goos),
		"Settings    " + v.Registry.ChordText(ActSettings, v.goos),
	}
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, len(lines))
		for i, l := range lines {
			lb := atoms.Muted(l)
			if i == 0 {
				lb = atoms.Strong(l).WithColor(th.Color.TextMuted)
			}
			children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Bottom: th.Space.S}.Layout(gtx, lb.Fn(th))
			})
		}
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx, children...)
	})
}

// toEditorDiags converts controller diagnostics; empty ranges cover one
// byte so they stay visible.
// toEditorDiags converts diagnostics for the editor. hint is the line
// added to the hover of subtle (PHPStan) findings; nil hides them (the
// inline setting is off).
func toEditorDiags(ds []controller.Diagnostic, n int, hint *string) []editorview.Diagnostic {
	out := make([]editorview.Diagnostic, 0, len(ds))
	for _, d := range ds {
		if d.Subtle && hint == nil {
			continue
		}
		start, end := min(max(d.Start, 0), n), min(max(d.End, 0), n)
		if end <= start {
			end = min(start+1, n)
			if end == start && start > 0 {
				start--
			}
		}
		ed := editorview.Diagnostic{Start: start, End: end, Severity: editorview.Severity(d.Severity), Message: d.Message, Source: d.Source, Subtle: d.Subtle}
		if d.Subtle {
			ed.Detail = analysisDetail(d, *hint)
		}
		out = append(out, ed)
	}
	return out
}

// analysisDetail are the hover lines of a PHPStan finding: tip, stale
// note and the Fix with AI hint.
func analysisDetail(d controller.Diagnostic, hint string) string {
	var lines []string
	if d.Tip != "" {
		lines = append(lines, "Tip: "+d.Tip)
	}
	if d.Stale {
		lines = append(lines, "The file changed after this was reported – it may be outdated.")
	}
	if hint != "" {
		lines = append(lines, hint)
	}
	return strings.Join(lines, "\n")
}

func caretText(b *buffer.Buffer, sel cursor.Selection) string {
	p := b.OffsetToPos(sel.Head)
	t := fmt.Sprintf("%d:%d", p.Line+1, buffer.RuneCol(b.Line(p.Line), p.Col)+1)
	if !sel.Empty() {
		t += fmt.Sprintf(" (%d chars)", len([]rune(string(b.Slice(sel.Start(), sel.End())))))
	}
	return t
}

// editorEvents handles the widget's events (document locked).
func (v *View) editorEvents(gtx layout.Context, s *editorSession, active bool) {
	c := v.ctl
	typed := ""
	changed := false
	for {
		ev, ok := s.ed.Update(gtx)
		if !ok {
			break
		}
		switch ev := ev.(type) {
		case editorview.ChangeEvent:
			changed = true
			typed = string(ev.Edit.Text)
		case editorview.FocusEvent:
			if !ev.Focused {
				c.Autosave("focusLost")
			} else if !active {
				// Clicked into another group's editor: it becomes active
				// (next frame; groups are being laid out).
				g := s.group
				v.Post(func() { v.focusGroup(g) })
			}
		case editorview.ActionEvent:
			if !active {
				continue
			}
			v.editorAction(s, ev.Action)
		case editorview.GutterClickEvent:
			if isGutterContext(ev) {
				s.gutterContextClick(ev.Line + 1)
				continue
			}
			for _, h := range v.Registry.gutterClicks {
				if h(s.file, ev) {
					break
				}
			}
		}
	}
	if !active {
		return
	}
	ignore := s.ignoreEdt
	s.ignoreEdt = false
	buf := s.doc.Buffer()
	caret := s.ed.Cursor().Primary().Head
	cp := &c.Completion
	if cp.Open && cp.File == s.file && (caret < cp.Start || buf.OffsetToPos(caret).Line != buf.OffsetToPos(cp.Start).Line) {
		c.CloseCompletion()
	}
	if changed && !ignore {
		v.recordEdit(s)
	}
	if !changed || ignore || !s.ed.Focused() {
		return
	}
	if cp.Open && cp.File == s.file {
		c.UpdateCompletion(string(buf.Slice(cp.Start, caret)), caret)
	} else if len(typed) > 0 && len(typed) <= 2 && c.Status.LSPRunning {
		ls := buf.LineStart(buf.OffsetToPos(caret).Line)
		before := buf.Slice(ls, caret)
		if controller.AutoTrigger(string(before)) {
			start := ls + controller.WordStart(before, len(before))
			c.RequestCompletion(s.file, caret, start, string(buf.Slice(start, caret)))
		}
	}
	if req, cl := controller.SignatureTrigger(typed); req {
		c.RequestSignature(s.file, caret)
	} else if cl {
		c.CloseSignature()
	}
}

// editorAction handles host actions reported by the widget.
func (v *View) editorAction(s *editorSession, a keymap.Action) {
	switch a {
	case keymap.Find, keymap.Replace:
		sel := s.ed.Cursor().Primary()
		q := ""
		if !sel.Empty() && sel.Len() < 200 {
			if t := string(s.doc.Buffer().Slice(sel.Start(), sel.End())); !strings.ContainsRune(t, '\n') {
				q = t
			}
		}
		v.findOpen = true
		v.find.Open(q, a == keymap.Replace)
		v.findQueryChanged(s, v.find.Query(), v.find.Options())
	case keymap.GotoLine:
		v.openGotoLine()
	case keymap.Save:
		v.ctl.Save(s.file)
	case keymap.PasteFromHistory:
		v.openPasteHistory(s)
	}
}

// popupKeys handles navigation of the completion popup and Escape for
// the info popups while the editor has the focus (document locked).
func (v *View) popupKeys(gtx layout.Context, s *editorSession) {
	c := v.ctl
	complOpen := c.Completion.Open && c.Completion.File == s.file
	infoOpen := (c.Hover.Open && c.Hover.File == s.file) || (c.Signature.Open && c.Signature.File == s.file)
	if !complOpen && !infoOpen {
		return
	}
	names := []key.Name{key.NameEscape}
	if complOpen {
		names = append(names, key.NameUpArrow, key.NameDownArrow, key.NamePageUp, key.NamePageDown, key.NameReturn, key.NameEnter, key.NameTab)
	}
	filters := make([]event.Filter, len(names))
	for i, n := range names {
		filters[i] = key.Filter{Focus: s.ed, Name: n}
	}
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case key.NameEscape:
			c.CloseCompletion()
			c.CloseHover()
			c.CloseSignature()
		case key.NameUpArrow:
			c.MoveSelection(-1)
		case key.NameDownArrow:
			c.MoveSelection(1)
		case key.NamePageUp:
			c.MoveSelection(-completion.MaxRows)
		case key.NamePageDown:
			c.MoveSelection(completion.MaxRows)
		case key.NameReturn, key.NameEnter, key.NameTab:
			v.acceptCompletion(s)
		}
	}
}

// acceptCompletion inserts the selected proposal (document locked).
func (v *View) acceptCompletion(s *editorSession) {
	caret := s.ed.Cursor().Primary().Head
	edits, nc, ok := v.ctl.AcceptCompletion(caret)
	if !ok {
		return
	}
	buf := s.doc.Buffer()
	buf.BreakUndo()
	for _, e := range edits {
		start, end := min(max(e.Start, 0), buf.Len()), min(max(e.End, 0), buf.Len())
		if end < start {
			end = start
		}
		_, _ = buf.Replace(start, end-start, []byte(e.Text))
	}
	buf.BreakUndo()
	s.ed.SetCaret(min(max(nc, 0), buf.Len()))
	s.ignoreEdt = true
}

// completeAtCaret opens completion explicitly (Ctrl+Space; document
// locked: editor scope).
func (v *View) completeAtCaret() {
	s := v.activeSession()
	if s == nil {
		return
	}
	buf := s.doc.Buffer()
	caret := s.ed.Cursor().Primary().Head
	ls := buf.LineStart(buf.OffsetToPos(caret).Line)
	before := buf.Slice(ls, caret)
	start := ls + controller.WordStart(before, len(before))
	v.ctl.RequestCompletion(s.file, caret, start, string(buf.Slice(start, caret)))
}

// goToDefinition asks for the definition/implementations at the caret
// (document locked: editor scope).
func (v *View) goToDefinition(method string) {
	if s := v.activeSession(); s != nil {
		v.ctl.GoToDefinition(s.location(), method)
	}
}

// gotoLine moves the caret to a 1-based line and column.
func (v *View) gotoLine(line, col int) {
	s := v.activeSession()
	if s == nil {
		return
	}
	s.doc.Lock()
	defer s.doc.Unlock()
	buf := s.doc.Buffer()
	l := min(max(line-1, 0), buf.LineCount()-1)
	v.ctl.Nav.Push(s.location())
	s.ed.SetCaret(buf.LineStart(l) + buffer.ByteColFromRunes(buf.Line(l), col-1))
	v.focusEditor = true
}

// ---- hover ----------------------------------------------------------------------

func (v *View) hoverInput(gtx layout.Context, s *editorSession) {
	c := v.ctl
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &s.hoverTag, Kinds: pointer.Move | pointer.Enter | pointer.Leave | pointer.Press})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		pt := image.Pt(int(pe.Position.X), int(pe.Position.Y))
		switch pe.Kind {
		case pointer.Move, pointer.Enter:
			s.hoverIn = true
			if pt != s.hoverPos {
				s.hoverPos, s.hoverSince, s.hoverDone = pt, gtx.Now, false
				if c.Hover.Open && c.Hover.File == s.file {
					if off, ok := s.textOffsetAt(pt); !ok || off != c.Hover.Offset {
						c.CloseHover()
					}
				}
			}
		case pointer.Leave:
			s.hoverIn = false
			c.CloseHover()
		case pointer.Press:
			c.CloseHover()
			s.hoverDone = true
			if pe.Modifiers.Contain(key.ModShortcut) && pe.Buttons.Contain(pointer.ButtonPrimary) {
				p := pt
				s.cmdClick = &p
			}
			if molecules.IsContextClick(pe, v.goos) {
				s.editorContextClick(pt)
			}
		}
	}
}

// textOffsetAt returns the document offset of the character under pt, or
// false if pt is not over text.
func (s *editorSession) textOffsetAt(pt image.Point) (int, bool) {
	off := s.ed.OffsetAt(pt)
	r, ok := s.ed.AnchorRect(off)
	if !ok || pt.Y < r.Min.Y || pt.Y >= r.Max.Y {
		return 0, false
	}
	w := r.Dx()
	if pt.X < r.Min.X-w || pt.X > r.Max.X+w/2 {
		return 0, false
	}
	return off, true
}

func (v *View) hoverAfterLayout(gtx layout.Context, s *editorSession) {
	c := v.ctl
	if p := s.cmdClick; p != nil {
		s.cmdClick = nil
		if off, ok := s.textOffsetAt(*p); ok {
			v.navd.n.Navigate(controller.At(s.file, off), controller.JumpDefinition)
		}
	}
	if !s.hoverIn || s.hoverDone {
		return
	}
	if due := s.hoverSince.Add(HoverDelay); gtx.Now.Before(due) {
		gtx.Execute(op.InvalidateCmd{At: due})
		return
	}
	s.hoverDone = true
	off, ok := s.textOffsetAt(s.hoverPos)
	if !ok {
		return
	}
	var msgs []string
	for _, d := range s.ed.DiagnosticsAt(off) {
		m := d.Message
		if d.Source != "" {
			m += "  [" + d.Source + "]"
		}
		if d.Detail != "" {
			m += "\n" + d.Detail
		}
		msgs = append(msgs, m)
	}
	c.RequestHover(s.file, off, msgs)
}

// layoutPopups draws completion, signature and hover popups above the
// editor (deferred, on top of everything).
func (v *View) layoutPopups(gtx layout.Context, s *editorSession, bounds image.Point) {
	c := v.ctl
	th := v.th
	if cp := c.Completion; cp.Open && cp.File == s.file {
		if r, ok := s.ed.AnchorRect(cp.Start); ok {
			v.compl.Items = v.compl.Items[:0]
			for _, i := range cp.Visible {
				it := cp.Items[i]
				v.compl.Items = append(v.compl.Items, completion.Item{Label: it.Label, Detail: firstLine(it.Detail), Kind: it.Kind, Deprecated: it.Deprecated})
			}
			v.compl.Selected, v.compl.Loading = cp.Selected, cp.Loading
			for _, e := range v.compl.Update(gtx) {
				if ch, ok := e.(completion.ChooseEvent); ok {
					c.Completion.Selected = ch.Index
					v.acceptCompletion(s)
					return
				}
			}
			size := v.compl.Size(gtx, th)
			deferAt(gtx, completion.Place(r, size, bounds, false), func(gtx layout.Context) { v.compl.Layout(gtx, th) })
		}
	}
	if sg := c.Signature; sg.Open && sg.File == s.file {
		if r, ok := s.ed.AnchorRect(s.ed.Cursor().Primary().Head); ok {
			info := completion.Info{Text: sg.Label, HighlightStart: sg.ParamStart, HighlightEnd: sg.ParamEnd}
			size := measure(gtx, th, info)
			deferAt(gtx, completion.Place(r, size, bounds, true), func(gtx layout.Context) { info.Layout(gtx, th) })
		}
	}
	if h := c.Hover; h.Open && h.File == s.file && h.Text != "" {
		if r, ok := s.ed.AnchorRect(h.Offset); ok {
			info := completion.Info{Text: h.Text}
			size := measure(gtx, th, info)
			deferAt(gtx, completion.Place(r, size, bounds, false), func(gtx layout.Context) { info.Layout(gtx, th) })
		}
	}
}

func measure(gtx layout.Context, th *tokens.Theme, info completion.Info) image.Point {
	m := op.Record(gtx.Ops)
	gtx.Constraints.Min = image.Point{}
	d := info.Layout(gtx, th)
	m.Stop()
	return d.Size
}

// deferAt draws w at p on top of everything else.
func deferAt(gtx layout.Context, p image.Point, w func(gtx layout.Context)) {
	m := op.Record(gtx.Ops)
	t := op.Offset(p).Push(gtx.Ops)
	cg := gtx
	cg.Constraints = layout.Constraints{Max: gtx.Constraints.Max}
	w(cg)
	t.Pop()
	op.Defer(gtx.Ops, m.Stop())
}

// ---- find / replace -------------------------------------------------------------

func (v *View) layoutFind(gtx layout.Context) layout.Dimensions {
	s := v.activeSession()
	if evs := v.find.Update(gtx); len(evs) > 0 && s != nil {
		s.doc.Lock()
		for _, e := range evs {
			v.findEvent(gtx, s, e)
		}
		s.doc.Unlock()
	}
	return v.find.Layout(gtx, v.th)
}

func (v *View) findEvent(gtx layout.Context, s *editorSession, e findbar.Event) {
	buf := s.doc.Buffer()
	switch e := e.(type) {
	case findbar.QueryEvent:
		v.findQueryChanged(s, e.Query, e.Options)
	case findbar.NextEvent:
		s.ed.Do(gtx, keymap.FindNext)
	case findbar.PrevEvent:
		s.ed.Do(gtx, keymap.FindPrevious)
	case findbar.ReplaceEvent:
		if q := s.query; q != nil {
			sel := s.ed.Cursor().Primary()
			for _, m := range q.FindAllIn(buf.Snapshot(), sel.Start(), sel.End(), 1) {
				if m.Start == sel.Start() && m.End == sel.End() {
					if _, err := search.Replace(buf, q, m, e.With); err != nil {
						v.ctl.Status.Notice = err.Error()
					}
				}
			}
			s.ed.Do(gtx, keymap.FindNext)
		}
	case findbar.ReplaceAllEvent:
		if q := s.query; q != nil {
			n, err := search.ReplaceAll(buf, q, e.With)
			if err != nil {
				v.ctl.Status.Notice = err.Error()
			} else {
				v.ctl.Status.Notice = fmt.Sprintf("Replaced %d occurrences", n)
			}
		}
	case findbar.CloseEvent:
		v.findOpen = false
		s.query = nil
		s.ed.SetSearch(nil)
		v.focusEditor = true
	}
	s.findKey = [4]int{}
}

// findQueryChanged compiles the query and selects the next match from the
// current selection (document locked).
func (v *View) findQueryChanged(s *editorSession, text string, o findbar.Options) {
	s.findKey = [4]int{}
	if text == "" {
		s.query = nil
		s.ed.SetSearch(nil)
		v.find.Invalid = false
		return
	}
	q, err := search.Compile(text, search.Options{CaseSensitive: o.CaseSensitive, WholeWord: o.WholeWord, Regex: o.Regex})
	v.find.Invalid = err != nil
	if err != nil {
		s.query = nil
		s.ed.SetSearch(nil)
		return
	}
	s.query = q
	s.ed.SetSearch(q)
	buf := s.doc.Buffer()
	if m, ok := q.Next(buf.Snapshot(), s.ed.Cursor().Primary().Start(), true); ok {
		s.ed.Cursor().SetSelections([]cursor.Selection{cursor.Range(m.Start, m.End)}, 0)
		s.ed.RevealCaret()
	}
}

// updateFindCount refreshes the match counter when the text or the
// selection changed (document locked).
func (v *View) updateFindCount(s *editorSession) {
	buf := s.doc.Buffer()
	sel := s.ed.Cursor().Primary()
	k := [4]int{int(buf.Version()) + 1, sel.Start(), sel.End(), 1}
	if s.query == nil {
		v.find.Matches, v.find.Current = 0, 0
		return
	}
	if k == s.findKey {
		return
	}
	s.findKey = k
	ms := s.query.FindAll(buf.Snapshot(), 10000)
	v.find.Matches, v.find.Current = len(ms), 0
	for i, m := range ms {
		if m.Start == sel.Start() && m.End == sel.End() {
			v.find.Current = i + 1
		}
	}
}
