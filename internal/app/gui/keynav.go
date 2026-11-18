package gui

import (
	"fmt"
	"path/filepath"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/clipring"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
)

// Keyboard parity with the reference IDE's macOS keymap (M13, AP13.2):
// live keymap settings, pointer and gesture navigation, Recent Locations,
// Last Edit Location, the File Structure popup, Parameter Info, Quick
// Documentation, Next/Previous Highlighted Error and Paste from History.

// Window actions added by M13 (IDs of the keymap catalog).
const (
	ActLastEdit        = keymap.NavLastEdit
	ActRecentLocations = keymap.NavRecentLocations
	ActFileStructure   = keymap.NavFileStructure
	ActNextError       = keymap.NavNextError
	ActPrevError       = keymap.NavPreviousError
	ActParameterInfo   = keymap.ParameterInfo
	ActQuickDoc        = keymap.QuickDocumentation
	ActFindView        = keymap.ViewFind
)

// clipHistory is the clipboard history shared by all windows.
var clipHistory = clipring.New(0)

// keyNav is the view state of the M13 navigation features.
type keyNav struct {
	keymapSeen uint64
	// mouseTag receives the pointer presses of the whole window (side
	// buttons, delivered by Gio on Windows and Wayland).
	mouseTag int
	// structFile is the file whose File Structure popup waits for the
	// outline; structSeen the outline version when it was requested.
	structFile string
	structSeen uint64
}

func (v *View) installKeyNav() {
	r := v.Registry
	ed := func(id keymap.Action, title, menu, section string, run func(s *editorSession)) Action {
		return Action{ID: id, Title: title, Scope: ScopeEditor, Menu: menu, Section: section, Run: func(v *View) {
			if s := v.activeSession(); s != nil {
				run(s)
			}
		}}
	}
	for _, a := range []Action{
		{ID: ActRecentLocations, Title: "Recent Locations…", Menu: "Navigate", Section: "goto", Run: (*View).openRecentLocations},
		{ID: ActLastEdit, Title: "Last Edit Location", Menu: "Navigate", Section: "history", Run: (*View).lastEditLocation},
		{ID: ActFindView, Title: "Find", Menu: "View/Tool Windows", Run: func(v *View) { v.navd.showTool(ToolFind) }},
		ed(ActFileStructure, "File Structure", "Navigate", "structure", v.openFileStructure),
		ed(ActNextError, "Next Highlighted Error", "Navigate", "errors", func(s *editorSession) { v.stepError(s, true) }),
		ed(ActPrevError, "Previous Highlighted Error", "Navigate", "errors", func(s *editorSession) { v.stepError(s, false) }),
		ed(ActParameterInfo, "Parameter Info", "View", "info", func(s *editorSession) {
			v.ctl.RequestSignature(s.file, s.ed.Cursor().Primary().Head)
		}),
		ed(ActQuickDoc, "Quick Documentation", "View", "info", v.quickDoc),
		editorMenuAction(keymap.JoinLines, "Join Lines", "code"),
		editorMenuAction(keymap.ToggleCase, "Toggle Case", "code"),
		editorMenuAction(keymap.PasteFromHistory, "Paste from History…", "clipboard"),
	} {
		r.AddAction(a)
	}
	r.AddFrameHandler(func(v *View, gtx layout.Context) { v.syncFileStructure() })
	v.ctl.LoadKeymap()
}

// syncKeymap rebuilds the bindings when the keymap settings changed.
func (v *View) syncKeymap() {
	k := v.ctl.Keymap
	if k.Version == v.keys.keymapSeen {
		return
	}
	v.keys.keymapSeen = k.Version
	preset := keymap.Preset(k.Preset)
	if !preset.Valid() {
		preset = keymap.PresetFor(v.goos)
	}
	// Bad lines are skipped and reported; the valid ones apply (D248).
	ov, err := keymap.ParseOverrides(k.Overrides)
	if err != nil {
		v.ctl.Status.Notice = strings.ReplaceAll(err.Error(), "\n", "; ")
	}
	// Dynamic actions (external tools) can be bound like catalog ones.
	var extra []keymap.Def
	if v.extd != nil {
		extra = v.extd.defs()
	}
	v.Registry.SetScheme(keymap.NewScheme(preset, ov, extra...))
}

// ---- pointer and gesture navigation ------------------------------------------------

// pointerNav runs the mouse side buttons (Gio reports them as
// ButtonQuaternary/Quinary on Windows and Wayland; macOS delivers them
// through package native) and registers the window-wide, pass-through
// area for the next frame.
func (v *View) pointerNav(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &v.keys.mouseTag, Kinds: pointer.Press})
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch {
		case pe.Buttons.Contain(pointer.ButtonQuaternary):
			v.Gesture(keymap.KeyMouseBack)
		case pe.Buttons.Contain(pointer.ButtonQuinary):
			v.Gesture(keymap.KeyMouseForward)
		}
	}
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, &v.keys.mouseTag)
	area.Pop()
	pass.Pop()
}

// gesturePrefix marks native gesture IDs routed like menu items.
const gesturePrefix = "gesture:"

// ---- recent locations, last edit ---------------------------------------------------

// placeAt describes the caret of s as a place (document locked).
func (s *editorSession) placeAt(off int, edit bool) controller.Place {
	buf := s.doc.Buffer()
	off = min(max(off, 0), buf.Len())
	line := buf.OffsetToPos(off).Line
	return controller.Place{Location: controller.At(s.file, off), Line: line + 1, Snippet: lineSnippet(buf.Line(line)), Edit: edit}
}

func lineSnippet(line []byte) string {
	s := strings.TrimSpace(string(line))
	if r := []rune(s); len(r) > 80 {
		s = string(r[:79]) + "…"
	}
	return s
}

// recordEdit notes the caret of an edited editor as edit location
// (document locked).
func (v *View) recordEdit(s *editorSession) {
	v.ctl.Nav.Record(s.placeAt(s.ed.Cursor().Primary().Head, true))
}

// fillPlace completes line and snippet of a place recorded without them
// from an open editor of its file.
func (v *View) fillPlace(p controller.Place) controller.Place {
	if p.Snippet != "" && p.Line > 0 {
		return p
	}
	for k, s := range v.editors {
		if k.file != p.File {
			continue
		}
		s.doc.Lock()
		off := p.Range.Start.Offset
		if off < 0 && p.Line > 0 {
			off = s.doc.Buffer().LineStart(min(p.Line-1, s.doc.Buffer().LineCount()-1))
		}
		q := s.placeAt(off, p.Edit)
		s.doc.Unlock()
		return q
	}
	return p
}

// openRecentLocations shows the recent caret and edit places with their
// code line; choosing one jumps there.
func (v *View) openRecentLocations() {
	if !v.ctl.Project.Open {
		return
	}
	cur := v.currentLocation()
	var places []controller.Place
	for _, p := range v.ctl.Nav.Places() {
		places = append(places, v.fillPlace(p))
	}
	var shown []controller.Place
	items := func(q string) []palette.Item {
		shown = shown[:0]
		var out []palette.Item
		q = strings.ToLower(q)
		for _, p := range places {
			label := fmt.Sprintf("%s:%d", filepath.Base(p.File), p.Line)
			if p.Line == 0 {
				label = filepath.Base(p.File)
			}
			if q != "" && !strings.Contains(strings.ToLower(label+" "+p.Snippet), q) {
				continue
			}
			g := atoms.GlyphFileCode
			if p.Edit {
				g = atoms.GlyphEdit
			}
			shown = append(shown, p)
			out = append(out, palette.Item{Label: label, Detail: p.Snippet, Glyph: g})
		}
		return out
	}
	v.OpenChooser(ChooserSpec{
		Title: "Recent Locations", Hint: "Filter (file or code)", Items: items(""), OnQuery: items,
		OnChoose: func(i int, _ string) {
			if i >= 0 && i < len(shown) {
				v.ctl.GoTo(cur, shown[i].Location)
			}
			v.focusEditor = true
		},
	})
}

// lastEditLocation jumps to the most recent edit place; pressing it again
// goes further back.
func (v *View) lastEditLocation() {
	cur, line := controller.Location{}, 0
	if s := v.activeSession(); s != nil {
		s.doc.Lock()
		p := s.placeAt(s.ed.Cursor().Primary().Head, false)
		s.doc.Unlock()
		cur, line = p.Location, p.Line
	}
	if p, ok := v.ctl.Nav.LastEdit(cur, line); ok {
		v.ctl.OpenFile(p.File, &p.Location)
		v.focusEditor = true
	}
}

// ---- file structure popup ------------------------------------------------------------

// openFileStructure loads the outline of the editor's file; the popup
// opens when it arrives (syncFileStructure). Editor scope: locked.
func (v *View) openFileStructure(s *editorSession) {
	v.keys.structFile, v.keys.structSeen = s.file, v.navd.n.StructureVersion
	v.navd.n.LoadStructure(s.file)
}

type structItem struct {
	node  controller.OutlineNode
	depth int
}

func flattenOutline(ns []controller.OutlineNode, depth int, out []structItem) []structItem {
	for _, n := range ns {
		out = append(out, structItem{n, depth})
		out = flattenOutline(n.Children, depth+1, out)
	}
	return out
}

// syncFileStructure opens the pending File Structure popup.
func (v *View) syncFileStructure() {
	n := v.navd.n
	file := v.keys.structFile
	if file == "" || n.StructureVersion == v.keys.structSeen {
		return
	}
	v.keys.structFile = ""
	st := n.Structure
	if st == nil || st.File != file {
		return
	}
	if st.Err != "" && len(st.Nodes) == 0 {
		v.ctl.Status.Notice = "File Structure: " + st.Err
		return
	}
	all := flattenOutline(st.Nodes, 0, nil)
	var shown []structItem
	items := func(q string) []palette.Item {
		shown = shown[:0]
		var out []palette.Item
		for _, it := range all {
			if q != "" {
				if _, _, ok := controller.FuzzyMatch(q, it.node.Name); !ok {
					continue
				}
			}
			label := it.node.Name
			if q == "" {
				label = strings.Repeat("   ", it.depth) + label
			}
			detail := strings.TrimSpace(strings.Join(it.node.Modifiers, " ") + " " + it.node.Detail)
			shown = append(shown, it)
			out = append(out, palette.Item{Label: label, Detail: fmt.Sprintf("%s  :%d", detail, it.node.Line), Glyph: kindGlyph(it.node.Kind)})
		}
		return out
	}
	cur := v.currentLocation()
	v.OpenChooser(ChooserSpec{
		Title: "File Structure – " + filepath.Base(file), Hint: "Filter members", Items: items(""), OnQuery: items,
		OnChoose: func(i int, _ string) {
			if i >= 0 && i < len(shown) {
				v.ctl.GoTo(cur, controller.At(file, shown[i].node.Offset))
			}
			v.focusEditor = true
		},
	})
}

// ---- code info and errors ---------------------------------------------------------

// quickDoc shows the hover information (and diagnostics) of the symbol
// at the caret (editor scope: locked).
func (v *View) quickDoc(s *editorSession) {
	off := s.ed.Cursor().Primary().Head
	var msgs []string
	for _, d := range s.ed.DiagnosticsAt(off) {
		msgs = append(msgs, d.Message)
	}
	v.ctl.RequestHover(s.file, off, msgs)
	s.hoverDone = true
}

// stepError moves the caret to the next (previous) diagnostic of the
// file and shows its message (editor scope: locked).
func (v *View) stepError(s *editorSession, forward bool) {
	caret := s.ed.Cursor().Primary().Head
	d, ok := controller.NextDiagnostic(v.ctl.Diagnostics(s.file), caret, forward)
	if !ok {
		v.ctl.Status.Notice = "No problems in this file"
		return
	}
	off := min(max(d.Start, 0), s.doc.Buffer().Len())
	s.ed.SetCaret(off)
	v.ctl.RequestHover(s.file, off, []string{d.Message})
	s.hoverDone = true
}

// ---- paste from history --------------------------------------------------------------

// openPasteHistory offers the clipboard history; the chosen text is
// pasted into s and becomes the clipboard content.
func (v *View) openPasteHistory(s *editorSession) {
	texts := clipHistory.Items()
	if len(texts) == 0 {
		v.ctl.Status.Notice = "Clipboard history is empty"
		return
	}
	items := make([]palette.Item, len(texts))
	for i, t := range texts {
		first, _, _ := strings.Cut(strings.TrimSpace(t), "\n")
		detail := ""
		if n := strings.Count(t, "\n"); n > 0 {
			detail = fmt.Sprintf("%d lines", n+1)
		}
		items[i] = palette.Item{Label: lineSnippet([]byte(first)), Detail: detail, Glyph: atoms.GlyphCopy}
	}
	v.OpenChooser(ChooserSpec{Title: "Paste from History", Hint: "Filter", Items: items,
		OnQuery: func(q string) []palette.Item {
			if q == "" {
				return items
			}
			var out []palette.Item
			for i, t := range texts {
				if strings.Contains(strings.ToLower(t), strings.ToLower(q)) {
					out = append(out, items[i])
				}
			}
			return out
		},
		OnChoose: func(i int, q string) {
			// Map the filtered index back to the history.
			idx := -1
			for j, t := range texts {
				if q == "" || strings.Contains(strings.ToLower(t), strings.ToLower(q)) {
					idx++
					if idx == i {
						v.pasteText(s, j)
						break
					}
				}
			}
			v.focusEditor = true
		},
	})
}

// pasteText pastes history entry i into s.
func (v *View) pasteText(s *editorSession, i int) {
	text, ok := clipHistory.Take(i)
	if !ok || v.editors[sessionKey{s.group, s.file}] != s {
		return
	}
	s.doc.Lock()
	if !s.ed.ReadOnly {
		s.ed.Cursor().Paste(text)
		s.ed.RevealCaret()
	}
	s.doc.Unlock()
	v.clipboard = &text
}
