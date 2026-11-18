package gui

import (
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/editor/snippet"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
)

// Live templates in the editor (AP17.3): Tab expands the abbreviation
// before the caret, Insert Live Template (Cmd+J) and Surround With
// (Cmd+Alt+T, Cmd+Alt+J) offer the templates of the caret's context. The
// backend applies the expansion (templates.expand); the editor then walks
// the tab stops: all occurrences of a variable are selected at once
// (one caret each, so typing edits them together), Tab and Shift+Tab move
// between the stops, Escape or the last Tab ends at $END$.

// templateDock follows the live templates state of the controller.
type templateDock struct {
	v       *View
	project string
	loaded  bool
	seen    uint64
}

func installTemplates(v *View) *templateDock {
	d := &templateDock{v: v}
	r := v.Registry
	ed := func(id keymap.Action, title, menu, section string, run func(s *editorSession)) Action {
		return Action{ID: id, Title: title, Scope: ScopeEditor, Menu: menu, Section: section, Run: func(v *View) {
			if s := v.activeSession(); s != nil {
				run(s)
			}
		}}
	}
	r.AddAction(ed(keymap.InsertLiveTemplate, "Insert Live Template…", "Code", "templates", func(s *editorSession) { d.choose(s, false) }))
	r.AddAction(ed(keymap.SurroundWith, "Surround With…", "Code", "templates", func(s *editorSession) { d.choose(s, true) }))
	r.AddAction(ed(keymap.SurroundWithLiveTemplate, "Surround with Live Template…", "Code", "templates", func(s *editorSession) { d.choose(s, true) }))
	extract := func(target string) func(s *editorSession) {
		return func(s *editorSession) {
			sel := s.ed.Cursor().Primary()
			v.navd.n.LoadExtract(s.file, sel.Start(), sel.End(), target)
		}
	}
	r.AddAction(ed(keymap.ExtractVariable, "Extract Variable…", "Refactor", "extract", extract(controller.ExtractVariable)))
	r.AddAction(ed(keymap.ExtractMethod, "Extract Method…", "Refactor", "extract", extract(controller.ExtractMethod)))
	r.AddAction(ed(keymap.CodeGenerate, "Generate…", "Code", "generate", func(s *editorSession) {
		v.ctl.LoadGenerate(s.file, s.ed.Cursor().Primary().Head, d.showGenerate)
	}))
	r.AddFrameHandler(d.frame)
	return d
}

// frame loads the abbreviations once per project (and after the settings
// dialog closed: user templates may have changed).
func (d *templateDock) frame(v *View, _ layout.Context) {
	if root := v.ctl.Project.Root; root != d.project {
		d.project, d.loaded = root, false
	}
	if v.settingsOpen {
		d.loaded = false
		return
	}
	if !d.loaded && v.ctl.Project.Open {
		d.loaded = true
		v.ctl.LoadTemplates()
	}
}

// choose offers the templates at the caret (surround: those wrapping the
// selection, or the caret line without one) and expands the chosen one.
func (d *templateDock) choose(s *editorSession, surround bool) {
	buf := s.doc.Buffer()
	sel := s.ed.Cursor().Primary()
	start, end := sel.Start(), sel.End()
	if surround && start == end {
		line := buf.OffsetToPos(start).Line
		ls := buf.LineStart(line)
		text := buf.Line(line)
		i := 0
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		start, end = ls+i, buf.LineEnd(line)
	}
	if !surround {
		end = start
		if !sel.Empty() {
			// A plain template replaces the selection.
			start, end = sel.Start(), sel.End()
		}
	}
	file := s.file
	title := "Insert Live Template"
	if surround {
		title = "Surround With"
	}
	d.v.ctl.TemplatesAt(file, start, surround, func(ts []controller.TemplateItem) {
		items := make([]palette.Item, len(ts))
		for i, t := range ts {
			items[i] = palette.Item{Label: t.Abbreviation, Detail: t.Description, Glyph: atoms.GlyphEdit}
		}
		idx := make([]int, len(ts))
		for i := range idx {
			idx[i] = i
		}
		d.v.OpenChooser(ChooserSpec{Title: title, Hint: "Filter by abbreviation or description", Items: items,
			OnQuery: func(q string) []palette.Item {
				var out []palette.Item
				idx = idx[:0]
				for i, it := range items {
					if matchTemplate(it, q) {
						out = append(out, it)
						idx = append(idx, i)
					}
				}
				return out
			},
			OnChoose: func(i int, _ string) {
				if i < 0 || i >= len(idx) {
					return
				}
				d.v.focusEditor = true
				d.v.ctl.ExpandTemplate(file, start, end, ts[idx[i]].Abbreviation, lastClip(), nil)
			}})
	})
}

func matchTemplate(it palette.Item, q string) bool {
	q = strings.ToLower(q)
	return q == "" || strings.Contains(strings.ToLower(it.Label), q) || strings.Contains(strings.ToLower(it.Detail), q)
}

// lastClip is the newest entry of the clipboard history (clipboard()).
func lastClip() string {
	if items := clipHistory.Items(); len(items) > 0 {
		return items[0]
	}
	return ""
}

// ---- tab stops (document locked) ----------------------------------------

// startSnippet begins the tab-stop session of an applied expansion if it
// belongs to s and the buffer is still at the expansion's version.
func (v *View) startSnippet(s *editorSession) {
	lt := &v.ctl.Templates
	if v.tmpl == nil || v.tmpl.seen == lt.ExpandedVersion {
		return
	}
	ex := lt.Expanded
	if ex == nil || ex.File != s.file {
		return
	}
	v.tmpl.seen = lt.ExpandedVersion
	buf := s.doc.Buffer()
	s.endSnippet()
	if ex.Version != 0 && ex.Version != buf.Version() {
		return // edited in the meantime: keep the text, skip the stops
	}
	sn := snippet.NewSession(ex.Fields, ex.Caret)
	if sn.Done() {
		s.ed.SetCaret(min(max(ex.Caret, 0), buf.Len()))
		return
	}
	s.snip = sn
	s.snipOff = buf.OnEdit(func(e buffer.Edit) { sn.Edit(e.StartByte, e.OldEndByte, e.NewEndByte) })
	s.selectField()
	v.focusEditor = true
}

// endSnippet stops the tab-stop session.
func (s *editorSession) endSnippet() {
	if s.snipOff != nil {
		s.snipOff()
		s.snipOff = nil
	}
	s.snip = nil
}

// selectField selects all occurrences of the current field (one caret
// each); after the last field the caret goes to the end position.
func (s *editorSession) selectField() {
	buf := s.doc.Buffer()
	f, ok := s.snip.Current()
	if !ok {
		end := min(max(s.snip.End(), 0), buf.Len())
		s.endSnippet()
		s.ed.SetCaret(end)
		return
	}
	sels := make([]cursor.Selection, 0, len(f.Ranges))
	for _, r := range f.Ranges {
		sels = append(sels, cursor.Range(min(max(r.Start, 0), buf.Len()), min(max(r.End, 0), buf.Len())))
	}
	s.ed.Cursor().SetSelections(sels, 0)
	s.ed.RevealCaret()
}

// templateKeys handles Tab, Shift+Tab and Escape of a tab-stop session
// and Tab expansion of an abbreviation before the caret; it runs before
// the completion popup and the widget see the keys.
func (v *View) templateKeys(gtx layout.Context, s *editorSession) {
	c := v.ctl
	if c.Completion.Open && c.Completion.File == s.file {
		return
	}
	active := s.snip != nil && !s.snip.Done()
	if s.snip != nil && !active {
		s.endSnippet()
	}
	abbr := ""
	if !active {
		abbr = s.abbreviationBeforeCaret()
		if abbr == "" || !c.Templates.IsAbbreviation(abbr) {
			return
		}
	}
	filters := []event.Filter{key.Filter{Focus: s.ed, Name: key.NameTab, Optional: key.ModShift}}
	if active {
		filters = append(filters, key.Filter{Focus: s.ed, Name: key.NameEscape})
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
		switch {
		case !active && e.Name == key.NameTab && !e.Modifiers.Contain(key.ModShift):
			caret := s.ed.Cursor().Primary().Head
			c.ExpandTemplate(s.file, caret, caret, "", lastClip(), func() {
				// No template applies here: Tab indents as usual.
				v.pendingEdit = keymap.Indent
				v.focusEditor = true
			})
		case !active:
			v.pendingEdit = keymap.Outdent
		case e.Name == key.NameEscape:
			s.endSnippet()
			return
		case e.Modifiers.Contain(key.ModShift):
			s.snip.Prev()
			s.selectField()
		default:
			s.snip.Next()
			s.selectField()
		}
	}
}

// abbreviationBeforeCaret returns the word before a single, empty caret
// ("" otherwise).
func (s *editorSession) abbreviationBeforeCaret() string {
	cur := s.ed.Cursor()
	if len(cur.Selections()) != 1 || !cur.Primary().Empty() {
		return ""
	}
	buf := s.doc.Buffer()
	off := cur.Primary().Head
	ls := buf.LineStart(buf.OffsetToPos(off).Line)
	text := buf.Slice(ls, off)
	i := len(text)
	for i > 0 {
		b := text[i-1]
		if !(b == '_' || b == '-' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z') {
			break
		}
		i--
	}
	return string(text[i:])
}
