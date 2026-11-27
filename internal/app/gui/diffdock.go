package gui

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight/tshighlight"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Diff, file history and Local History tabs (virtual tab types "diff",
// "fileHistory", "localHistory") and the annotation column (vcs.annotate).
// The organisms come from internal/ui/organisms/diff; the controller
// delivers the git.fileDiff/git.blame results as raw JSON which is
// decoded into the organisms' API mirror types.
//
// F7/Shift+F7 move between differences while a diff tab is active. The
// debugger binds F7 to Step Into globally; its action hands the key to
// the diff instead when a diff tab is active and no debug session is
// paused (diffKeyWins).

// diffView is the view of one diff tab.
type diffView struct {
	win  *diff.DiffWindow
	seen uint64
	// positioned: the view was scrolled to the first change once (when
	// the comparison first had changes).
	positioned bool
	// Buttons of the disk ↔ editor comparison.
	keep, take atoms.Button
}

// histView is the view of one file history tab.
type histView struct {
	view            *diff.FileHistoryView
	seen, diffSeen  uint64
	loaded          int
	pageRequestedAt int
}

// localView is the view of one Local History tab.
type localView struct {
	win      *diff.DiffWindow
	rows     []molecules.ListRow
	list     molecules.ScrollList
	split    molecules.Split
	revert   atoms.Button
	seen     uint64
	diffSeen uint64
}

// blameView is the annotation column of one file.
type blameView struct {
	gutter *diff.BlameGutter
	seen   uint64
	ready  bool
}

func (d *workDock) highlighter() diff.HighlighterFunc {
	return func(b *buffer.Buffer, p string) highlight.Highlighter {
		return tshighlight.New(b, tshighlight.Options{Path: p, Invalidate: func() { d.v.Invalidate() }})
	}
}

func (d *workDock) newDiffWindow() *diff.DiffWindow {
	w := diff.NewDiffWindow()
	w.NewHighlighter = d.highlighter()
	w.TabWidth = d.v.ctl.Prefs.TabWidth
	return w
}

func (d *workDock) installDiffs() {
	r := d.v.Registry
	r.AddTabType(TabType{Kind: controller.TabDiff, Glyph: atoms.GlyphSwap, Layout: d.layoutDiffTab, OnClose: d.closeDiffTab})
	r.AddTabType(TabType{Kind: controller.TabFileHistory, Glyph: atoms.GlyphHistory, Layout: d.layoutHistoryTab, OnClose: d.closeHistoryTab})
	r.AddTabType(TabType{Kind: controller.TabLocalHistory, Glyph: atoms.GlyphHistory, Layout: d.layoutLocalTab, OnClose: d.closeLocalTab})
	r.AddGutterColumn(d.blameColumn)
	r.AddGutterClickHandler(d.blameClick)
	r.AddAction(Action{ID: ActDiffNext, Title: "Next Difference", KeyHint: "F7", Menu: "Navigate", Section: "diff", Run: func(v *View) { d.diffStep(true) }})
	r.AddAction(Action{ID: ActDiffPrev, Title: "Previous Difference", KeyHint: "Shift+F7", Menu: "Navigate", Section: "diff", Run: func(v *View) { d.diffStep(false) }})
}

// gitActions are the VCS actions installed while git is available.
func (d *workDock) gitActions() []Action {
	file := func(f func(rel string)) func(*View) {
		return func(*View) {
			if p, ok := d.fileTarget(); ok {
				f(p)
			}
		}
	}
	as := []Action{
		{ID: ActVcsAnnotate, Title: "Annotate with Git Blame", Menu: "Git", Section: "file", Run: file(d.toggleAnnotate)},
		{ID: ActVcsHistory, Title: "Show History for File", Menu: "Git", Section: "file", Run: file(func(p string) { d.work().OpenFileHistory(p) })},
		{ID: ActVcsCompareHead, Title: "Compare with HEAD", Menu: "Git", Section: "file", Run: file(d.compareWithHead)},
		{ID: ActVcsCompareRev, Title: "Compare with Branch or Revision…", Menu: "Git", Section: "file", Run: file(d.compareWithRevision)},
		{ID: ActVcsRollback, Title: "Rollback File…", Menu: "Git", Section: "file", Run: func(*View) {
			if ts := withoutRoot(d.targets()); len(ts) > 0 {
				d.rollback(ts)
			}
		}},
		{ID: ActShowDiff, Title: "Show Diff", Mac: "Cmd+D", PC: "Ctrl+D", Menu: "Git", Section: "file", Run: func(v *View) { d.showDiff() }},
		{ID: ActGitAdd, Title: "Add to Git", Menu: "Git", Section: "file", Run: func(*View) {
			if ts := d.targets(); len(ts) > 0 {
				d.work().Stage(ts)
			}
		}},
	}
	return append(as, d.remoteActions()...)
}

// ---- actions ---------------------------------------------------------------------

func (d *workDock) compareWithHead(rel string) {
	d.work().OpenDiff(controller.DiffSpec{Path: rel, Base: controller.VersionHead, Target: controller.VersionWorking})
}

// compareWithRevision offers branches and recent commits as the base.
func (d *workDock) compareWithRevision(rel string) {
	g := d.v.ctl.Vcs
	g.LoadBranches()
	if len(g.Log) == 0 {
		g.LoadLog(true)
	}
	type choice struct{ rev, label, detail string }
	build := func(q string) ([]palette.Item, []choice) {
		q = strings.ToLower(q)
		var cs []choice
		for _, b := range g.Branches {
			if !b.Current {
				cs = append(cs, choice{b.Name, b.Name, "branch · " + b.Subject})
			}
		}
		for i, c := range g.Log {
			if i == 50 {
				break
			}
			cs = append(cs, choice{c.Hash, c.Hash[:min(8, len(c.Hash))] + "  " + c.Subject, c.AuthorName + " · " + c.AuthorDate.Local().Format("2006-01-02")})
		}
		var items []palette.Item
		var out []choice
		for _, c := range cs {
			if q != "" && !strings.Contains(strings.ToLower(c.label+" "+c.rev), q) {
				continue
			}
			g := atoms.GlyphCommit
			if strings.HasPrefix(c.detail, "branch") {
				g = atoms.GlyphBranch
			}
			items = append(items, palette.Item{Label: c.label, Detail: c.detail, Glyph: g})
			out = append(out, c)
		}
		return items, out
	}
	items, shown := build("")
	d.v.OpenChooser(ChooserSpec{Title: "Compare " + path.Base(rel) + " with…", Hint: "Branch, revision or commit message", Items: items,
		OnQuery: func(q string) []palette.Item {
			var its []palette.Item
			its, shown = build(q)
			return its
		},
		OnChoose: func(i int, q string) {
			rev := strings.TrimSpace(q)
			if i >= 0 && i < len(shown) {
				rev = shown[i].rev
			}
			if rev != "" {
				d.work().OpenDiff(controller.DiffSpec{Path: rel, Base: rev, Target: controller.VersionWorking})
			}
		}})
}

func (d *workDock) rollback(paths []string) {
	what := paths[0]
	if len(paths) > 1 {
		what = fmt.Sprintf("%d items", len(paths))
	}
	d.v.Confirm("Rollback "+what+"?", "Local changes are discarded and the HEAD version is restored (open files: undoable in the editor).", "Rollback", func() {
		d.work().Rollback(paths)
	})
}

// showDiff is Cmd+D: Duplicate Line while the editor has the focus, else
// the diff of the commit window's selection or of the target file.
func (d *workDock) showDiff() {
	v := d.v
	if s := v.activeSession(); s != nil && s.ed.Focused() {
		v.pendingEdit = keymap.DuplicateLines
		return
	}
	if v.ToolShown(ToolCommit) && v.gitd != nil {
		if p := v.gitd.panel.SelectedPath(); p != "" && (v.activeTool == ToolCommit || v.ctl.ActiveTab() == nil) {
			v.gitd.openChangeDiff(p)
			return
		}
	}
	if p, ok := d.fileTarget(); ok {
		d.compareWithHead(p)
	}
}

// openCommitFileDiff opens the diff of a file of the commit selected in
// the git log.
func (d *workDock) openCommitFileDiff(i int) {
	g := d.v.ctl.Vcs
	if i < 0 || i >= len(g.CommitFiles) {
		return
	}
	f := &g.CommitFiles[i]
	var parents []string
	for _, c := range g.Log {
		if c.Hash == g.SelectedCommit {
			parents = c.Parents
		}
	}
	old := ""
	if f.IsRename || f.IsCopy {
		old = f.OldPath
	}
	d.work().CommitFileDiff(g.SelectedCommit, parents, f.Path(), old, f.IsNew)
}

func (d *workDock) toggleAnnotate(rel string) {
	c := d.v.ctl
	abs := c.Abs(rel)
	if c.Tab(abs) == nil {
		c.OpenFile(abs, nil)
	}
	c.Work.ToggleAnnotate(abs)
}

// ---- F7 ------------------------------------------------------------------------

// diffKeyWins decides who gets F7/Shift+F7: the active diff tab, unless a
// debug session is paused (then F7 is Step Into).
func diffKeyWins(diffActive, debugPaused bool) bool { return diffActive && !debugPaused }

// activeDiffWindow returns the diff window of the active tab (diff, file
// history or Local History), or nil.
func (d *workDock) activeDiffWindow() *diff.DiffWindow {
	t := d.v.ctl.ActiveTab()
	if t == nil {
		return nil
	}
	switch data := t.Data.(type) {
	case *controller.DiffTab:
		if dv := d.diffs[data]; dv != nil {
			return dv.win
		}
	case *controller.HistoryTab:
		if hv := d.hists[data]; hv != nil {
			return hv.view.Diff
		}
	case *controller.LocalHistoryTab:
		if lv := d.locals[data]; lv != nil {
			return lv.win
		}
	}
	return nil
}

// routeDiffKey moves to the next (or previous) difference if F7 belongs
// to the diff (true: handled).
func (v *View) routeDiffKey(next bool) bool {
	if v.work == nil {
		return false
	}
	w := v.work.activeDiffWindow()
	if !diffKeyWins(w != nil, v.ctl.Debug.Paused()) {
		return false
	}
	v.work.diffStep(next)
	return true
}

func (d *workDock) diffStep(next bool) {
	w := d.activeDiffWindow()
	if w == nil {
		return
	}
	if next {
		w.Next()
	} else {
		w.Prev()
	}
	d.v.Invalidate()
}

// ---- diff tab -------------------------------------------------------------------

func (d *workDock) layoutDiffTab(gtx layout.Context, th *tokens.Theme, tab *controller.Tab) layout.Dimensions {
	dt, ok := tab.Data.(*controller.DiffTab)
	if !ok {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	dv := d.diffs[dt]
	if dv == nil {
		dv = &diffView{win: d.newDiffWindow(),
			keep: atoms.Button{Text: "Keep mine", Kind: atoms.Primary}, take: atoms.Button{Text: "Take disk", Kind: atoms.Secondary}}
		d.diffs[dt] = dv
	}
	if dt.Doc != nil {
		dt.Doc.Lock()
		defer dt.Doc.Unlock()
	}
	if dv.seen != dt.Version {
		dv.seen = dt.Version
		d.applyDiff(dt, dv)
	}
	// A fresh comparison opens at its first change.
	if !dv.positioned && !dt.Loading && dv.win.Count() > 0 {
		dv.positioned = true
		if dv.win.Current() < 0 {
			dv.win.Next()
		}
	}
	w := d.work()
	if nav := dt.Nav; nav != nil {
		dv.win.SetFileNav(diff.FileNav{Index: nav.Index, Total: nav.Total(),
			Prev: path.Base(nav.Neighbor(dt.Spec.Path, -1)), Next: path.Base(nav.Neighbor(dt.Spec.Path, 1))})
		if g := d.v.gitd; g != nil && g.focusDiff && dv.seen == dt.Version && !dt.Loading {
			g.focusDiff = false
			if l, r := dv.win.Editors(); r != nil {
				r.Focus(gtx)
			} else if l != nil {
				l.Focus(gtx)
			}
		}
	}
	for _, e := range dv.win.Update(gtx) {
		switch e := e.(type) {
		case diff.FileNavRequested:
			delta := e.Delta
			d.v.Post(func() {
				if w.StepChangeDiff(dt, delta) {
					if g := d.v.gitd; g != nil {
						g.panel.Select(dt.Spec.Path)
					}
				}
			})
		case diff.RevertRequested:
			// One undoable edit of the shared document (the lock is held).
			if err := dv.win.ApplyRevert(e); err != nil {
				d.v.ctl.Status.Notice = err.Error()
			}
		case diff.IgnoreWhitespaceChanged:
			if !dt.Spec.External {
				on := e.Ignore
				d.v.Post(func() { w.SetIgnoreWhitespace(dt, on) })
			}
		case diff.OpenFileRequested:
			file, line := dt.File, e.Line
			d.v.Post(func() { d.v.ctl.OpenFile(file, ptr(controller.AtLine(file, line+1))) })
		}
	}
	if dt.Loading && dv.seen == 0 {
		return layout.Center.Layout(gtx, atoms.Muted("Loading diff…").Fn(th))
	}
	if dt.Err != nil && dt.Result == nil && dt.Disk == "" {
		return layout.UniformInset(th.Space.L).Layout(gtx, molecules.Banner{Kind: molecules.BannerError, Title: "Cannot compare " + dt.Spec.Path, Detail: dt.Err.Error()}.Fn(th))
	}
	ext := dt.Spec.External && w.ExternalOf(dt.File) != nil && !w.ExternalOf(dt.File).Deleted()
	if !ext {
		return dv.win.Layout(gtx, th)
	}
	if dv.keep.Clicked(gtx) {
		file := dt.File
		d.v.Post(func() { w.ResolveExternal(file, controller.ResolveKeepMine) })
	}
	if dv.take.Clicked(gtx) {
		file := dt.File
		d.v.Post(func() { w.ResolveExternal(file, controller.ResolveTakeDisk) })
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, molecules.Banner{Kind: molecules.BannerWarning, Title: "File changed on disk",
						Detail: "Left: the file on disk. Right: the editor content (editable). Revert blocks with », then choose."}.Fn(th)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return dv.keep.Layout(gtx, th) })
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return dv.take.Layout(gtx, th) })
					}),
				)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return dv.win.Layout(gtx, th) }),
	)
}

func ptr[T any](v T) *T { return &v }

// applyDiff shows a loaded diff (document locked if editable).
func (d *workDock) applyDiff(dt *controller.DiffTab, dv *diffView) {
	var right *buffer.Buffer
	if dt.Doc != nil {
		right = dt.Doc.Buffer()
	}
	if dt.Spec.External {
		cur := ""
		if right != nil {
			cur = right.String()
		}
		dv.win.SetContent(diff.Content{
			Left:   diff.Side{Label: "On disk", Path: dt.Spec.Path, Text: dt.Disk},
			Right:  diff.Side{Label: "Editor (unsaved changes)", Path: dt.Spec.Path, Text: cur},
			Blocks: diff.Compute(dt.Disk, cur, dt.Spec.IgnoreWhitespace),
		}, right)
		return
	}
	if dt.Result == nil {
		return
	}
	var r diff.APIFileDiff
	if err := json.Unmarshal(dt.Result, &r); err != nil {
		d.v.ctl.Status.Notice = "diff: " + err.Error()
		return
	}
	content := r.Content()
	if r.Right.Version != controller.VersionWorking || !r.Right.Exists {
		right = nil
	}
	dv.win.SetIgnoreWhitespace(dt.Spec.IgnoreWhitespace)
	dv.win.SetContent(content, right)
}

func (d *workDock) closeDiffTab(tab *controller.Tab) {
	dt, ok := tab.Data.(*controller.DiffTab)
	if !ok {
		return
	}
	if dv := d.diffs[dt]; dv != nil {
		if dt.Doc != nil {
			dt.Doc.Lock()
		}
		dv.win.Close()
		if dt.Doc != nil {
			dt.Doc.Unlock()
		}
		delete(d.diffs, dt)
	}
	d.work().CloseDiff(dt)
}

// ---- file history tab ------------------------------------------------------------

func (d *workDock) layoutHistoryTab(gtx layout.Context, th *tokens.Theme, tab *controller.Tab) layout.Dimensions {
	ht, ok := tab.Data.(*controller.HistoryTab)
	if !ok {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	hv := d.hists[ht]
	if hv == nil {
		hv = &histView{view: diff.NewFileHistoryView()}
		hv.view.Diff.NewHighlighter = d.highlighter()
		d.hists[ht] = hv
	}
	switch {
	case hv.seen != ht.Version:
		hv.seen, hv.loaded = ht.Version, len(ht.Entries)
		hv.view.SetHistory(ht.Path, ht.Entries, ht.More)
	case hv.loaded != len(ht.Entries):
		hv.view.AppendHistory(ht.Entries[hv.loaded:], ht.More)
		hv.loaded = len(ht.Entries)
	}
	if hv.diffSeen != ht.DiffVersion {
		hv.diffSeen = ht.DiffVersion
		var r diff.APIFileDiff
		if json.Unmarshal(ht.Diff, &r) == nil {
			hv.view.Diff.SetContent(r.Content(), nil)
		}
	}
	w := d.work()
	for _, e := range hv.view.Update(gtx) {
		switch e := e.(type) {
		case diff.HistoryDiffRequested:
			d.v.Post(func() { w.HistoryDiff(ht, e.Commit, e.Base, e.Path, e.ParentPath) })
		case diff.HistoryPageRequested:
			d.v.Post(func() { w.MoreHistory(ht) })
		case diff.OpenFileRequested:
			file := ht.File
			d.v.Post(func() { d.v.ctl.OpenFile(file, nil) })
		}
	}
	if len(ht.Entries) == 0 && ht.Err == nil && hv.seen <= 1 {
		if ht.Version == 0 {
			return layout.Center.Layout(gtx, atoms.Muted("Loading history…").Fn(th))
		}
	}
	return hv.view.Layout(gtx, th)
}

func (d *workDock) closeHistoryTab(tab *controller.Tab) {
	if ht, ok := tab.Data.(*controller.HistoryTab); ok {
		if hv := d.hists[ht]; hv != nil {
			hv.view.Close()
			delete(d.hists, ht)
		}
		d.work().CloseFileHistory(ht)
	}
}

// ---- Local History tab -------------------------------------------------------------

func (d *workDock) layoutLocalTab(gtx layout.Context, th *tokens.Theme, tab *controller.Tab) layout.Dimensions {
	lt, ok := tab.Data.(*controller.LocalHistoryTab)
	if !ok {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	lv := d.locals[lt]
	if lv == nil {
		lv = &localView{win: d.newDiffWindow(), split: molecules.Split{Ratio: 0.28, MinPane: 160},
			revert: atoms.Button{Text: "Revert to This Revision", Kind: atoms.Primary, Glyph: atoms.GlyphHistory}}
		d.locals[lt] = lv
	}
	if lv.seen != lt.Version {
		lv.seen = lt.Version
		lv.rows = make([]molecules.ListRow, len(lt.Revisions))
	}
	if lv.diffSeen != lt.DiffVersion {
		lv.diffSeen = lt.DiffVersion
		lv.win.SetContent(diff.Content{
			Left:   diff.Side{Label: fmt.Sprintf("Revision #%d", lt.Selected), Path: lt.Path, Text: lt.RevText},
			Right:  diff.Side{Label: "Current", Path: lt.Path, Text: lt.Current},
			Blocks: diff.Compute(lt.RevText, lt.Current, lv.win.IgnoreWhitespace()),
		}, nil)
	}
	w := d.work()
	for i := range lv.rows {
		if cl, _ := lv.rows[i].Update(gtx); cl && i < len(lt.Revisions) {
			id := lt.Revisions[i].ID
			d.v.Post(func() { w.SelectLocalRevision(lt, id) })
		}
	}
	if lv.revert.Clicked(gtx) && lt.Selected != 0 {
		id := lt.Selected
		d.v.Confirm(fmt.Sprintf("Revert %s to revision #%d?", lt.Path, id), "The current content stays in the Local History (open documents reload as one undo step).", "Revert", func() {
			w.RevertToLocalRevision(lt, id)
		})
	}
	lv.win.Update(gtx)
	list := func(gtx layout.Context) layout.Dimensions {
		if len(lt.Revisions) == 0 {
			return layout.UniformInset(th.Space.M).Layout(gtx, atoms.Muted("No Local History for this file yet.").Fn(th))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if lt.Selected == 0 {
						gtx = gtx.Disabled()
					}
					return lv.revert.Layout(gtx, th)
				})
			}),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return lv.list.Layout(gtx, th, len(lt.Revisions), func(gtx layout.Context, i int) layout.Dimensions {
					r := lt.Revisions[i]
					row := &lv.rows[i]
					row.Selected = r.ID == lt.Selected
					return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(atoms.Label{Text: controller.RevisionLabel(r), MaxLines: 1}.Fn(th)),
							layout.Rigid(atoms.Small(r.Time.Local().Format("2006-01-02 15:04:05")+fmt.Sprintf(" · %d bytes", r.Size)).Fn(th)),
						)
					})
				})
			}),
		)
	}
	gtx.Constraints.Min = gtx.Constraints.Max
	return lv.split.Layout(gtx, th, list, func(gtx layout.Context) layout.Dimensions {
		if lt.Selected == 0 {
			return layout.Center.Layout(gtx, atoms.Muted("Select a revision").Fn(th))
		}
		return lv.win.Layout(gtx, th)
	})
}

func (d *workDock) closeLocalTab(tab *controller.Tab) {
	if lt, ok := tab.Data.(*controller.LocalHistoryTab); ok {
		if lv := d.locals[lt]; lv != nil {
			lv.win.Close()
			delete(d.locals, lt)
		}
		d.work().CloseLocalHistory(lt)
	}
}

// ---- annotations -----------------------------------------------------------------

// syncBlame follows the controller's annotations.
func (d *workDock) syncBlame() {
	w := d.work()
	if d.blameSeen == w.BlameVersion {
		return
	}
	d.blameSeen = w.BlameVersion
	for f := range d.blames {
		if w.Blame[f] == nil {
			delete(d.blames, f)
		}
	}
	for f, st := range w.Blame {
		bv := d.blames[f]
		if bv == nil {
			bv = &blameView{gutter: diff.NewBlameGutter()}
			d.blames[f] = bv
		}
		if bv.seen == st.Version || st.Result == nil {
			continue
		}
		bv.seen = st.Version
		var r diff.APIBlame
		if json.Unmarshal(st.Result, &r) == nil {
			r.Apply(bv.gutter)
			bv.ready = true
		}
	}
}

// blameColumn is the annotation column of an annotated file.
func (d *workDock) blameColumn(file string) editorview.GutterColumn {
	if bv := d.blames[file]; bv != nil && bv.ready {
		return bv.gutter
	}
	return nil
}

// blameClick opens the diff of the commit of a clicked annotation.
func (d *workDock) blameClick(file string, ev editorview.GutterClickEvent) bool {
	bv := d.blames[file]
	if bv == nil || !bv.ready {
		return false
	}
	e, ok := bv.gutter.HandleEditorEvent(ev)
	if !ok {
		return ev.Area == editorview.GutterAnnotation
	}
	if cc, ok := e.(diff.CommitClicked); ok {
		hash := cc.Hash
		d.v.Post(func() { d.work().ShowBlameCommit(file, hash) })
	}
	return true
}
