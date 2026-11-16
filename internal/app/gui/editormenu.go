package gui

import (
	"image"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// The editor and gutter context menus (D160): a right click (on macOS
// also Ctrl+click) into the text opens the model menu actions.CtxEditor,
// into the line numbers or the breakpoint column actions.CtxEditorGutter;
// entries registered with Registry.AddContextMenu follow. A right click
// outside the selection moves the caret first, so the actions work on the
// clicked symbol.

// editorContextClick records a context click into the text of s (editor
// coordinates; from hoverInput, document locked).
func (s *editorSession) editorContextClick(pt image.Point) {
	p := pt
	s.ctxClick = &p
}

// gutterContextClick records a context click into the gutter at the
// 1-based line.
func (s *editorSession) gutterContextClick(line int) { s.gutterCtx = line }

// afterEditor opens pending context menus of the active editor s and
// applies per-editor settings (document locked).
func (d *navDock) afterEditor(gtx layout.Context, s *editorSession) {
	s.ed.HideLineNumbers = d.hideNums
	switch {
	case s.gutterCtx > 0:
		line := s.gutterCtx
		s.gutterCtx, s.ctxClick = 0, nil
		d.openGutterMenu(s, line)
	case s.ctxClick != nil:
		pt := *s.ctxClick
		s.ctxClick = nil
		d.openEditorMenu(s, pt)
	}
}

// openEditorMenu shows the editor context menu at the click pt (document
// locked).
func (d *navDock) openEditorMenu(s *editorSession, pt image.Point) {
	v := d.v
	off, ok := s.textOffsetAt(pt)
	if !ok {
		off = s.ed.OffsetAt(pt)
	}
	if sel := s.ed.Cursor().Primary(); sel.Empty() || off < sel.Start() || off > sel.End() {
		s.ed.SetCaret(off)
	}
	ac := v.sessionContext(s, actions.CtxEditor)
	line, _ := s.caretLineSel()
	v.ShowActionMenu(ContextEditor, ContextTarget{File: s.file, Line: line, Offset: s.ed.Cursor().Primary().Head, Group: s.group}, ac)
}

// openGutterMenu shows the gutter context menu for the 1-based line
// (document locked).
func (d *navDock) openGutterMenu(s *editorSession, line int) {
	v := d.v
	s.menuLine = line
	ac := v.sessionContext(s, actions.CtxEditorGutter)
	ac.Line = line
	if !d.hideNums {
		ac.Values["lineNumbers"] = "true"
	}
	if v.dbgd != nil && v.dbgd.installed {
		if b, ok := v.ctl.Debug.BreakpointAt(s.file, line); ok {
			ac.Values["breakpoint"] = b.ID
			if b.Enabled {
				ac.Values["breakpointEnabled"] = "true"
			}
		}
	}
	items := v.Registry.ContextEntries(actions.CtxEditorGutter, ac, v.goos)
	// Toggle Breakpoint acts on the clicked line, not on the caret.
	for i := range items {
		if items[i].Action == ActToggleBreakpoint {
			file := s.file
			items[i].Action, items[i].Run = "", func() {
				if v.dbgd != nil && v.dbgd.installed {
					v.ctl.Debug.ToggleBreakpoint(file, line)
				}
			}
		}
	}
	// Index marks of the line.
	if m, ok := d.n.GutterMarks(s.file)[line]; ok {
		cur := controller.At(s.file, m.Range.Start.Offset)
		var nav []MenuItem
		if len(m.Up) > 0 {
			nav = append(nav, MenuItem{Title: "Go to Super Declaration", Icon: "step-out", Run: func() { d.n.GutterJump(cur, line, true) }})
		}
		if len(m.Down) > 0 {
			nav = append(nav, MenuItem{Title: "Go to Implementations", Icon: "step-into", Run: func() { d.n.GutterJump(cur, line, false) }})
		}
		items = append(nav, append([]MenuItem{{Separator: true}}, items...)...)
	}
	v.ShowContextMenu(ContextEditorGutter, ContextTarget{File: s.file, Line: line, Group: s.group}, items)
}

// isGutterContext reports a right click into the line numbers or the
// icon column.
func isGutterContext(ev editorview.GutterClickEvent) bool {
	return ev.Secondary && (ev.Area == editorview.GutterIcons || ev.Area == editorview.GutterNumbers)
}
