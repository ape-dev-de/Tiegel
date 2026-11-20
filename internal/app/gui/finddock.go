package gui

import (
	"fmt"
	"path"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Find in Files (Cmd+Shift+F) and Replace in Files (Cmd+Shift+R, D160):
// a dialog with the options of search.inProject, and the "Find" tool
// window that shows the streamed matches (search.results), can stop the
// search and, for Replace, shows the preview of search.replaceInProject
// before replacing.

// Scope options of the dialog.
var findScopes = []string{"project", "directory", "openFiles"}

// findDock is the dialog and the tool window.
type findDock struct {
	d *navDock

	pattern, replace, mask, dir atoms.TextField
	regex, matchCase, words     atoms.Checkbox
	scope                       *molecules.Dropdown
	replaceMode                 bool
	focus                       bool

	tree      resultTree
	seen      uint64
	shownFor  *controller.FindState
	filesSeen int
	preview   bool

	stop, apply atoms.Button
}

func newFindDock(d *navDock) *findDock {
	f := &findDock{d: d}
	for _, tf := range []*atoms.TextField{&f.pattern, &f.replace, &f.mask, &f.dir} {
		tf.Editor.SingleLine, tf.Editor.Submit = true, true
	}
	f.pattern.Hint, f.replace.Hint = "Text to find", "Replace with"
	f.mask.Hint, f.dir.Hint = "File mask, e.g. *.php,!*Test.php", "Directory (project relative)"
	f.pattern.Mono, f.replace.Mono, f.mask.Mono, f.dir.Mono = true, true, true, true
	f.regex.Label, f.matchCase.Label, f.words.Label = "Regex", "Match case", "Words"
	f.scope = molecules.NewSelect("Scope",
		molecules.DropdownOption{Label: "Project", Glyph: atoms.GlyphFolder},
		molecules.DropdownOption{Label: "Directory", Glyph: atoms.GlyphFolderOpen},
		molecules.DropdownOption{Label: "Open files", Glyph: atoms.GlyphFile})
	f.scope.Selected = 0
	f.stop = atoms.Button{Text: "Stop", Kind: atoms.Secondary}
	f.apply = atoms.Button{Text: "Replace All", Kind: atoms.Primary}
	f.tree.Empty = "Find in Files (Cmd+Shift+F) shows its matches here."
	return f
}

// params reads the dialog.
func (f *findDock) params() controller.FindParams {
	p := controller.FindParams{
		Pattern: f.pattern.Text(), Regex: f.regex.Checked(), CaseSensitive: f.matchCase.Checked(), WholeWord: f.words.Checked(),
		Mask: strings.TrimSpace(f.mask.Text()), Replace: f.replaceMode, Replacement: f.replace.Text(),
	}
	if i := f.scope.Selected; i > 0 && i < len(findScopes) {
		p.Scope = findScopes[i]
	}
	if p.Scope == "directory" {
		p.Dir = strings.TrimSpace(f.dir.Text())
	}
	return p
}

// openDialog shows the Find/Replace in Files dialog, prefilled with the
// selected text of the editor.
func (f *findDock) openDialog(replace bool) {
	v := f.d.v
	if !v.ctl.Project.Open {
		return
	}
	f.replaceMode, f.focus = replace, true
	if s := v.activeSession(); s != nil {
		s.doc.Lock()
		sel := s.ed.Cursor().Primary()
		if !sel.Empty() && sel.Len() < 200 {
			if t := string(s.doc.Buffer().Slice(sel.Start(), sel.End())); !strings.ContainsRune(t, '\n') {
				f.pattern.SetText(t)
			}
		}
		if f.dir.Text() == "" {
			if d := path.Dir(v.ctl.Rel(s.file)); d != "." {
				f.dir.SetText(d)
			}
		}
		s.doc.Unlock()
	}
	f.pattern.Editor.SetCaret(len(f.pattern.Text()), 0)
	title, ok := "Find in Files", "Find"
	if replace {
		title, ok = "Replace in Files", "Find…"
	}
	v.OpenPopup(&Popup{Title: title, Width: 520, Body: f.layoutDialog,
		Buttons: []*PopupButton{
			{Text: ok, Kind: atoms.Primary, OnClick: f.start},
			{Text: "Cancel", Kind: atoms.Secondary},
		}})
}

// start runs the search of the dialog.
func (f *findDock) start() {
	p := f.params()
	if p.Pattern == "" {
		return
	}
	f.d.n.StartFind(p)
	f.d.showTool(ToolFind)
}

func (f *findDock) layoutDialog(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v := f.d.v
	if f.focus {
		f.focus = false
		gtx.Execute(key.FocusCmd{Tag: &f.pattern.Editor})
	}
	for _, tf := range []*atoms.TextField{&f.pattern, &f.replace, &f.mask, &f.dir} {
		for {
			ev, ok := tf.Editor.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok && f.pattern.Text() != "" {
				v.Post(func() {
					v.ClosePopup()
					f.start()
				})
			}
		}
	}
	f.scope.Update(gtx)
	row := func(w layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Bottom: th.Space.S}.Layout(gtx, w)
		})
	}
	field := func(tf *atoms.TextField) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions { return tf.Layout(gtx, th) }
	}
	rows := []layout.FlexChild{row(field(&f.pattern))}
	if f.replaceMode {
		rows = append(rows, row(field(&f.replace)))
	}
	rows = append(rows,
		row(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return f.matchCase.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.M}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return f.words.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.M}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return f.regex.Layout(gtx, th) }),
			)
		}),
		row(field(&f.mask)),
		row(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(0.4, func(gtx layout.Context) layout.Dimensions { return f.scope.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Flexed(0.6, func(gtx layout.Context) layout.Dimensions {
					if f.scope.Selected != 1 {
						return layout.Dimensions{Size: gtx.Constraints.Min}
					}
					return f.dir.Layout(gtx, th)
				}),
			)
		}),
	)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// frame follows the search state.
func (f *findDock) frame(gtx layout.Context) {
	n := f.d.n
	if f.stop.Clicked(gtx) {
		n.CancelFind()
	}
	if f.apply.Clicked(gtx) {
		n.ApplyReplace(nil)
	}
	if f.seen == n.FindVersion {
		return
	}
	f.seen = n.FindVersion
	st := n.Find
	if st == nil {
		f.shownFor = nil
		f.tree.set(nil)
		f.tree.Header = nil
		return
	}
	if st != f.shownFor || f.preview != (st.Replace != nil) {
		f.shownFor, f.filesSeen, f.preview = st, 0, st.Replace != nil
		f.tree.set(nil)
	}
	if st.Replace != nil {
		if f.tree.rowCount() == 0 {
			f.tree.set(f.replaceNodes(st.Replace))
		}
	} else {
		var add []*resultNode
		for _, fm := range st.Files[f.filesSeen:] {
			add = append(add, f.fileNode(fm.Path, fm.File, fm.Matches, nil))
		}
		f.filesSeen = len(st.Files)
		if len(add) > 0 {
			f.tree.Roots = append(f.tree.Roots, add...)
			f.tree.dirty = true
		}
	}
	title := fmt.Sprintf("Find %q", st.Params.Pattern)
	if st.Params.Replace {
		title = fmt.Sprintf("Replace %q with %q", st.Params.Pattern, st.Params.Replacement)
	}
	summary := fmt.Sprintf("%s in %s", plural(st.Matches, "match"), plural(len(st.Files), "file"))
	switch {
	case st.Err != "":
		summary += " – " + firstLine(st.Err)
	case st.Running:
		summary += " – searching…"
	case st.Summary != nil && st.Summary.Cancelled:
		summary += " – stopped"
	case st.Summary != nil && st.Summary.Limited:
		summary += " – limit reached"
	case st.Replaced != nil:
		summary = fmt.Sprintf("replaced %s in %s", plural(st.Replaced.Replaced, "occurrence"), plural(st.Replaced.FilesChanged, "file"))
	case st.Params.Replace && st.Replace == nil && st.Matches > 0:
		summary += " – preparing preview…"
	case st.Replace != nil:
		summary = fmt.Sprintf("preview: %s in %s", plural(st.Replace.Replaced, "replacement"), plural(len(st.Replace.Files), "file"))
	}
	var extra []func(gtx layout.Context, th *tokens.Theme) layout.Dimensions
	if st.Running {
		extra = append(extra, f.stop.Layout)
	}
	if st.Replace != nil && st.Replace.Replaced > 0 {
		extra = append(extra, f.apply.Layout)
	}
	f.tree.Header = headerText(title, summary, extra...)
}

// fileNode is a file with its matches (or planned replacements).
func (f *findDock) fileNode(p, file string, ms []api.SearchMatch, repl []api.ReplaceMatch) *resultNode {
	v := f.d.v
	if file == "" {
		file = v.ctl.Abs(p)
	}
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	}
	n := len(ms) + len(repl)
	fn := &resultNode{Name: path.Base(p), Value: dir, Detail: plural(n, "match"), Glyph: fileGlyph(p), Expanded: true}
	add := func(m api.SearchMatch, text string) {
		loc := controller.At(file, m.Start)
		fn.Children = append(fn.Children, &resultNode{Name: fmt.Sprintf("%d:%d", m.Line, m.Col), Value: text,
			Open: func() { v.ctl.GoTo(v.currentLocation(), loc); v.focusEditor = true }})
	}
	for _, m := range ms {
		add(m, strings.TrimSpace(m.LineText))
	}
	for _, m := range repl {
		add(m.SearchMatch, strings.TrimSpace(m.LineText)+"   →   "+m.Replacement)
	}
	return fn
}

func (f *findDock) replaceNodes(r *api.ProjectReplaceResult) []*resultNode {
	out := make([]*resultNode, 0, len(r.Files))
	for _, fr := range r.Files {
		fn := f.fileNode(fr.Path, fr.File, nil, fr.Matches)
		if fr.Error != "" {
			fn.Detail = fr.Error
		}
		out = append(out, fn)
	}
	return out
}

func (f *findDock) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return f.tree.layout(gtx, th)
}
