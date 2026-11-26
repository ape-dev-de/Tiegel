package gui

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/filetree"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/tabs"
)

// The work dock is the file side of the shell (GUI integration part 2):
// the project tree's context menu and keys (files.*), the additions to
// the tab menu, the external change banners (externaldock.go), the diff,
// file history and Local History tabs with annotations (diffdock.go) and
// the remote git UI (vcsremote.go). It docks onto the shell through the
// Registry (actions, context menus, tab types, editor banners, gutter
// columns, tab decorators, frame handlers); its state lives in
// controller.Workspace.

// Action IDs of the work dock. The file actions reuse the IDs of
// internal/actions (D130); vcs.* are the editor/tree VCS actions.
const (
	ActNewFile        keymap.Action = actions.FileNewFile
	ActNewDir         keymap.Action = actions.FileNewDir
	ActNewPHPClass    keymap.Action = actions.FileNewPHPClass
	ActRenameFile     keymap.Action = actions.FileRename
	ActDeleteFile     keymap.Action = actions.FileDelete
	ActDuplicateFile  keymap.Action = actions.FileCopy
	ActRevealFile     keymap.Action = actions.FileReveal
	ActCopyPath       keymap.Action = actions.FileCopyPath
	ActCopyRelPath    keymap.Action = actions.FileCopyRelPath
	ActLocalHistory   keymap.Action = actions.FileHistory
	ActCloseOthers    keymap.Action = actions.TabCloseOthers
	ActCloseAll       keymap.Action = actions.TabCloseAll
	ActCloseRight     keymap.Action = actions.TabCloseRight
	ActSelectInTree   keymap.Action = "view.selectInProject"
	ActVcsAnnotate    keymap.Action = "vcs.annotate"
	ActVcsHistory     keymap.Action = "vcs.fileHistory"
	ActVcsCompareHead keymap.Action = "vcs.compareWithHead"
	ActVcsCompareRev  keymap.Action = "vcs.compareWithRevision"
	ActVcsRollback    keymap.Action = "vcs.rollback"
	ActShowDiff       keymap.Action = actions.GitShowDiff
	ActGitAdd         keymap.Action = actions.GitStage
	ActDiffNext       keymap.Action = "diff.nextChange"
	ActDiffPrev       keymap.Action = "diff.previousChange"
)

// workDock is the view state of the work dock.
type workDock struct {
	v *View
	// ext are the banner buttons per file (externaldock.go).
	ext map[string]*extButtons
	// diff, history and Local History views per tab state.
	diffs  map[*controller.DiffTab]*diffView
	hists  map[*controller.HistoryTab]*histView
	locals map[*controller.LocalHistoryTab]*localView
	// blames are the annotation columns per file.
	blames    map[string]*blameView
	blameSeen uint64
	// splitFile is opened in a split once its tab is active.
	splitFile string
	remote    remoteUI
	gitOn     bool
}

// installWork installs the work dock.
func installWork(v *View) *workDock {
	d := &workDock{v: v, ext: map[string]*extButtons{}, diffs: map[*controller.DiffTab]*diffView{},
		hists: map[*controller.HistoryTab]*histView{}, locals: map[*controller.LocalHistoryTab]*localView{},
		blames: map[string]*blameView{}}
	v.tree.GOOS = v.goos
	r := v.Registry
	for _, a := range d.fileActions() {
		r.AddAction(a)
	}
	r.AddContextMenu(ContextTab, d.tabMenu)
	r.AddFrameHandler(d.frame)
	d.installExternal()
	d.installDiffs()
	d.installRemote()
	return d
}

func (d *workDock) work() *controller.Workspace { return d.v.ctl.Work }

// frame follows renames, opens pending splits and installs the git
// actions while git is available.
func (d *workDock) frame(v *View, gtx layout.Context) {
	c := v.ctl
	for _, rn := range c.Work.TakeRenames() {
		d.rekey(rn)
	}
	if f := d.splitFile; f != "" {
		if t := c.ActiveTab(); t != nil && t.File == f {
			d.splitFile = ""
			c.Split(false)
			v.focusEditor = true
		}
	}
	on := c.Vcs.Available && c.Project.Open
	if on != d.gitOn {
		d.gitOn = on
		for _, a := range d.gitActions() {
			if on {
				v.Registry.AddAction(a)
			} else {
				v.Registry.RemoveAction(a.ID)
			}
		}
	}
	d.syncBlame()
	d.remoteFrame()
}

// rekey moves the editor widgets of a renamed document to its new path
// (caret and scroll stay); a changed extension rebuilds the highlighter.
func (d *workDock) rekey(rn controller.Rename) {
	v := d.v
	for k, s := range v.editors {
		if k.file != rn.From {
			continue
		}
		delete(v.editors, k)
		if filepath.Ext(rn.From) != filepath.Ext(rn.To) {
			s.doc.Lock()
			s.ed.Close()
			s.hl.Close()
			s.doc.Unlock()
			continue
		}
		s.file = rn.To
		v.editors[sessionKey{k.group, rn.To}] = s
	}
	if b := d.blames[rn.From]; b != nil {
		delete(d.blames, rn.From)
		d.blames[rn.To] = b
	}
	if v.tree.Selected() == v.ctl.Rel(rn.From) {
		v.tree.Select(v.ctl.Rel(rn.To))
	}
}

// ---- targets ----------------------------------------------------------------------

// targets returns the project-relative entries an action applies to: the
// tree selection while the tree has the focus, else the active file.
func (d *workDock) targets() []string {
	v := d.v
	if v.tree.Focused() || v.activeTool == ToolProject && v.ctl.ActiveTab() == nil {
		if s := v.tree.Selected(); s != "" || v.tree.Root != nil {
			return []string{s}
		}
	}
	if t := v.ctl.ActiveTab(); t != nil && t.Virtual == "" {
		return []string{t.Path}
	}
	if s := v.tree.Selected(); s != "" {
		return []string{s}
	}
	return nil
}

// target returns the single target ("" and false if none).
func (d *workDock) target() (string, bool) {
	ts := d.targets()
	if len(ts) != 1 {
		return "", false
	}
	return ts[0], true
}

// fileTarget is target restricted to files.
func (d *workDock) fileTarget() (string, bool) {
	p, ok := d.target()
	if !ok || d.isDir(p) {
		return "", false
	}
	return p, true
}

func (d *workDock) isDir(rel string) bool {
	if rel == "" {
		return true
	}
	if t := d.v.ctl.Tree; t != nil {
		if n := t.Node(rel); n != nil {
			return n.Dir
		}
	}
	return false
}

// ---- actions --------------------------------------------------------------------

func (d *workDock) fileActions() []Action {
	on := func(f func(paths []string)) func(*View) {
		return func(*View) {
			if ts := d.targets(); len(ts) > 0 {
				f(ts)
			}
		}
	}
	one := func(f func(p string)) func(*View) {
		return func(*View) {
			if p, ok := d.target(); ok {
				f(p)
			}
		}
	}
	return []Action{
		{ID: ActNewFile, Title: "New File…", Menu: "File/New", Section: "new", Run: one(func(p string) { d.newFile(d.work().DirOf(p)) })},
		{ID: ActNewDir, Title: "New Directory…", Menu: "File/New", Section: "new", Run: one(func(p string) { d.newDir(d.work().DirOf(p)) })},
		{ID: ActNewPHPClass, Title: "New PHP Class…", Menu: "File/New", Section: "php", Run: one(func(p string) { d.newPHPClass(d.work().DirOf(p)) })},
		{ID: ActRenameFile, Title: "Rename File…", KeyHint: "Shift+F6", Menu: "File", Section: "fileops", Run: one(d.rename)},
		{ID: ActDuplicateFile, Title: "Duplicate File…", Menu: "File", Section: "fileops", Run: one(d.duplicate)},
		{ID: ActDeleteFile, Title: "Delete…", KeyHint: deleteHint(d.v.goos), Menu: "File", Section: "fileops", Run: on(d.delete)},
		{ID: ActCopyPath, Title: "Copy Absolute Path", Mac: "Cmd+Shift+C", PC: "Ctrl+Shift+C", Menu: "Edit/Copy Path", Section: "path", Run: on(func(ps []string) { d.copyPath(ps, controller.PathAbsolute) })},
		{ID: ActCopyRelPath, Title: "Copy Path from Project Root", Mac: "Cmd+Alt+Shift+C", PC: "Ctrl+Alt+Shift+C", Menu: "Edit/Copy Path", Section: "path", Run: on(func(ps []string) { d.copyPath(ps, controller.PathRelative) })},
		{ID: ActRevealFile, Title: actions.RevealLabel(d.v.goos), Menu: "File", Section: "reveal", Run: one(d.work().Reveal)},
		{ID: ActSelectInTree, Title: "Select in Project View", Mac: "Cmd+Shift+1", PC: "Alt+Shift+1", Menu: "Navigate", Section: "select", Run: func(v *View) { d.selectInTree() }},
		{ID: ActLocalHistory, Title: "Local History…", Menu: "File", Section: "history", Run: one(func(p string) { d.work().OpenLocalHistory(p, 0) })},
		{ID: ActCloseOthers, Title: "Close Other Tabs", Menu: "Window", Section: "tabs", Run: func(v *View) { d.closeTabs(v.ctl.ActiveTab(), closeOthers) }},
		{ID: ActCloseRight, Title: "Close Tabs to the Right", Menu: "Window", Section: "tabs", Run: func(v *View) { d.closeTabs(v.ctl.ActiveTab(), closeRight) }},
		{ID: ActCloseAll, Title: "Close All Tabs", Menu: "Window", Section: "tabs", Run: func(v *View) { d.closeTabs(nil, closeAll) }},
	}
}

func deleteHint(goos string) string {
	if goos == "darwin" {
		return "Cmd+Backspace"
	}
	return "Delete"
}

// ---- dialogs ------------------------------------------------------------------------

func dirLabel(dir string) string {
	if dir == "" {
		return "project root"
	}
	return dir
}

func (d *workDock) newFile(dir string) {
	d.v.OpenChooser(ChooserSpec{Title: "New File", Hint: "File name in " + dirLabel(dir) + " (sub/dirs allowed)", NoList: true,
		OnChoose: func(_ int, q string) { d.work().CreateFile(dir, q) }})
}

func (d *workDock) newDir(dir string) {
	d.v.OpenChooser(ChooserSpec{Title: "New Directory", Hint: "Directory name in " + dirLabel(dir), NoList: true,
		OnChoose: func(_ int, q string) { d.work().CreateDir(dir, q) }})
}

// newPHPClass asks for name and kind; the namespace comes from
// composer.json (files.phpNamespace) and is shown with the kinds.
func (d *workDock) newPHPClass(dir string) {
	d.work().LoadNamespace(dir, func(ns controller.PHPNamespace) {
		detail := "no namespace (no PSR-4 mapping in composer.json)"
		if ns.Namespace != "" {
			detail = "namespace " + ns.Namespace
		}
		items := make([]palette.Item, len(controller.PHPClassKinds))
		for i, k := range controller.PHPClassKinds {
			items[i] = palette.Item{Label: controller.PHPClassKindLabel(k), Detail: detail, Glyph: atoms.GlyphFileCode}
		}
		d.v.OpenChooser(ChooserSpec{Title: "New PHP Class in " + dirLabel(dir), Hint: "Class name, then choose the kind", Items: items,
			OnChoose: func(i int, q string) {
				if i < 0 || i >= len(controller.PHPClassKinds) {
					i = 0
				}
				d.work().CreatePHPClass(dir, q, controller.PHPClassKinds[i], nil)
			}})
	})
}

func (d *workDock) rename(p string) {
	if p == "" {
		return
	}
	d.v.OpenChooser(ChooserSpec{Title: "Rename " + p, Hint: "New name (a path with / moves it)", Query: path.Base(p), NoList: true,
		OnChoose: func(_ int, q string) { d.work().Rename(p, q) }})
}

func (d *workDock) duplicate(p string) {
	if p == "" {
		return
	}
	ext := path.Ext(p)
	name := strings.TrimSuffix(path.Base(p), ext) + " copy" + ext
	d.v.OpenChooser(ChooserSpec{Title: "Duplicate " + p, Hint: "Name of the copy", Query: name, NoList: true,
		OnChoose: func(_ int, q string) { d.work().Duplicate(p, q) }})
}

// delete asks, moves to the trash and, without a trash, asks again
// before deleting permanently.
func (d *workDock) delete(paths []string) {
	paths = withoutRoot(paths)
	if len(paths) == 0 {
		return
	}
	what := paths[0]
	if len(paths) > 1 {
		what = fmt.Sprintf("%d items", len(paths))
	}
	w := d.work()
	d.v.Confirm("Delete "+what+"?", "The entries are moved to the trash.", "Delete", func() {
		w.Delete(paths, false, func(r controller.DeleteResult) {
			if !r.NoTrash {
				return
			}
			d.v.Confirm("Delete "+what+" permanently?", "There is no trash for these files ("+r.Err.Error()+"). Deleting cannot be undone.", "Delete Permanently", func() {
				w.Delete(paths, true, nil)
			})
		})
	})
}

func withoutRoot(paths []string) []string {
	out := paths[:0:0]
	for _, p := range paths {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (d *workDock) copyPath(paths []string, form string) {
	text := d.work().PathText(paths, form)
	d.v.CopyText(text)
	d.v.ctl.Status.Notice = "Copied " + firstLine(text)
}

// selectInTree shows the active file in the project tree.
func (d *workDock) selectInTree() {
	v := d.v
	t := v.ctl.ActiveTab()
	if t == nil || t.Virtual != "" {
		return
	}
	v.ShowTool(ToolProject)
	v.tree.Reveal(t.Path)
	v.ctl.LoadAncestors(t.Path)
}

// openInSplit opens a file in a new group to the right.
func (d *workDock) openInSplit(rel string) {
	abs := d.v.ctl.Abs(rel)
	d.splitFile = abs
	d.v.ctl.OpenFile(abs, nil)
}

// ---- tree -----------------------------------------------------------------------------

// treeEvent handles context clicks and command keys of the project tree.
func (v *View) treeEvent(e filetree.Event) {
	d := v.work
	switch e := e.(type) {
	case filetree.ContextEvent:
		v.activeTool = ToolProject
		v.ShowContextMenu(ContextProjectTree, ContextTarget{File: v.ctl.Abs(e.Path), Paths: []string{v.ctl.Abs(e.Path)}}, d.treeMenu(e.Path, e.Dir))
	case filetree.KeyEvent:
		short := e.Modifiers.Contain(key.ModShortcut)
		switch {
		case e.Name == key.NameF6 && e.Modifiers.Contain(key.ModShift):
			d.rename(e.Path)
		case e.Name == key.NameDeleteForward, e.Name == key.NameDeleteBackward && (short || v.goos != "darwin"):
			d.delete([]string{e.Path})
		case e.Name == "C" && short:
			d.work().CopyToClip([]string{e.Path})
			v.ctl.Status.Notice = "Copied " + e.Path + " (paste with " + shortcutText(v.goos, "V") + ")"
		case e.Name == "X" && short:
			d.work().CutToClip([]string{e.Path})
			v.ctl.Status.Notice = "Cut " + e.Path + " (paste with " + shortcutText(v.goos, "V") + ")"
		case e.Name == "V" && short:
			d.work().Paste(d.work().DirOf(e.Path))
		case e.Name == key.NameF4 && !e.Dir:
			v.ctl.OpenFile(e.Path, nil)
		}
	}
}

func shortcutText(goos, k string) string {
	if goos == "darwin" {
		return "Cmd+" + k
	}
	return "Ctrl+" + k
}

// treeMenu is the context menu of a tree entry (rel "" = project root).
func (d *workDock) treeMenu(rel string, dir bool) []MenuItem {
	v, w := d.v, d.work()
	ps := []string{rel}
	in := w.DirOf(rel)
	if rel == "" {
		dir = true
	}
	git := v.ctl.Vcs.Available
	items := []MenuItem{
		{Title: "New", Children: []MenuItem{
			{Title: "File…", Run: func() { d.newFile(in) }},
			{Title: "Directory…", Run: func() { d.newDir(in) }},
			{Separator: true},
			{Title: "PHP Class…", Run: func() { d.newPHPClass(in) }},
		}},
		{Separator: true},
		{Title: "Cut", Shortcut: shortcutText(v.goos, "X"), Disabled: rel == "", Run: func() { w.CutToClip(ps) }},
		{Title: "Copy", Shortcut: shortcutText(v.goos, "C"), Disabled: rel == "", Run: func() { w.CopyToClip(ps) }},
		{Title: d.pasteTitle(), Shortcut: shortcutText(v.goos, "V"), Disabled: len(w.Clip.Paths) == 0, Run: func() { w.Paste(in) }},
		{Title: "Copy Path", Children: []MenuItem{
			{Title: "Absolute Path", Shortcut: v.Registry.ChordText(ActCopyPath, v.goos), Run: func() { d.copyPath(ps, controller.PathAbsolute) }},
			{Title: "Path from Project Root", Shortcut: v.Registry.ChordText(ActCopyRelPath, v.goos), Run: func() { d.copyPath(ps, controller.PathRelative) }},
			{Title: "File Name", Run: func() { d.copyPath(ps, controller.PathName) }},
		}},
		{Separator: true},
		{Title: "Rename…", Shortcut: "Shift+F6", Disabled: rel == "", Run: func() { d.rename(rel) }},
		{Title: "Duplicate…", Disabled: rel == "" || dir, Run: func() { d.duplicate(rel) }},
		{Title: "Delete…", Shortcut: deleteHint(v.goos), Disabled: rel == "", Run: func() { d.delete(ps) }},
		{Separator: true},
	}
	if !dir {
		items = append(items, MenuItem{Title: "Open in Split", Run: func() { d.openInSplit(rel) }})
	}
	items = append(items, MenuItem{Title: actions.RevealLabel(v.goos), Run: func() { w.Reveal(rel) }})
	syncIn := rel
	if !dir {
		syncIn = parentOf(rel)
	}
	items = append(items, MenuItem{Title: "Reload from Disk", Icon: "refresh", Run: func() { w.Sync(syncIn) }})
	if !dir {
		items = append(items, MenuItem{Separator: true}, MenuItem{Title: "Local History", Children: []MenuItem{
			{Title: "Show History", Run: func() { w.OpenLocalHistory(rel, 0) }},
		}})
	}
	if git {
		items = append(items, MenuItem{Title: "Git", Children: d.gitMenu(rel, dir)})
	}
	return items
}

func (d *workDock) pasteTitle() string {
	clip := d.work().Clip
	switch {
	case len(clip.Paths) == 0:
		return "Paste"
	case clip.Cut:
		return "Paste (move " + path.Base(clip.Paths[0]) + ")"
	}
	return "Paste (copy of " + path.Base(clip.Paths[0]) + ")"
}

// gitMenu is the Git submenu of a tree entry or tab.
func (d *workDock) gitMenu(rel string, dir bool) []MenuItem {
	v, w := d.v, d.work()
	ps := []string{rel}
	chord := func(id keymap.Action) string { return v.Registry.ChordText(id, v.goos) }
	var out []MenuItem
	if !dir {
		out = append(out,
			MenuItem{Title: "Compare with HEAD", Shortcut: chord(ActVcsCompareHead), Run: func() { d.compareWithHead(rel) }},
			MenuItem{Title: "Compare with Branch or Revision…", Run: func() { d.compareWithRevision(rel) }},
			MenuItem{Title: "Show History", Run: func() { w.OpenFileHistory(rel) }},
		)
		if v.ctl.Tab(v.ctl.Abs(rel)) != nil {
			out = append(out, MenuItem{Title: "Annotate with Git Blame", Checked: w.Annotated(v.ctl.Abs(rel)), Run: func() { w.ToggleAnnotate(v.ctl.Abs(rel)) }})
		}
		out = append(out, MenuItem{Separator: true})
	}
	out = append(out,
		MenuItem{Title: "Rollback…", Shortcut: chord(ActVcsRollback), Run: func() { d.rollback(ps) }},
		MenuItem{Title: "Add", Run: func() { w.Stage(ps) }},
	)
	return out
}

// ---- tabs ---------------------------------------------------------------------------------

type closeMode int

const (
	closeOthers closeMode = iota
	closeRight
	closeAll
)

// closeTabs closes tabs of the active group relative to t.
func (d *workDock) closeTabs(t *controller.Tab, mode closeMode) {
	v := d.v
	c := v.ctl
	idx := -1
	for i, x := range c.Tabs {
		if x == t {
			idx = i
		}
	}
	if mode != closeAll && idx < 0 {
		return
	}
	for i := len(c.Tabs) - 1; i >= 0; i-- {
		switch {
		case mode == closeAll,
			mode == closeOthers && i != idx,
			mode == closeRight && i > idx:
			v.closeTab(i)
		}
	}
}

// tabMenu adds Close All, Close to the Right, Copy Path, Reveal and the
// VCS entries to the tab context menu.
func (d *workDock) tabMenu(v *View, t ContextTarget) []MenuItem {
	tab := t.Tab
	if tab == nil {
		return nil
	}
	items := []MenuItem{
		{Title: "Close Tabs to the Right", Run: func() { d.closeTabs(tab, closeRight) }},
		{Title: "Close All Tabs", Run: func() { d.closeTabs(nil, closeAll) }},
	}
	if tab.Virtual != "" {
		return items
	}
	rel := tab.Path
	ps := []string{rel}
	items = append(items, MenuItem{Separator: true},
		MenuItem{Title: "Copy Path", Children: []MenuItem{
			{Title: "Absolute Path", Shortcut: v.Registry.ChordText(ActCopyPath, v.goos), Run: func() { d.copyPath(ps, controller.PathAbsolute) }},
			{Title: "Path from Project Root", Shortcut: v.Registry.ChordText(ActCopyRelPath, v.goos), Run: func() { d.copyPath(ps, controller.PathRelative) }},
			{Title: "File Name", Run: func() { d.copyPath(ps, controller.PathName) }},
		}},
		MenuItem{Title: "Select in Project View", Shortcut: v.Registry.ChordText(ActSelectInTree, v.goos), Run: d.selectInTree},
		MenuItem{Title: actions.RevealLabel(v.goos), Run: func() { d.work().Reveal(rel) }},
		MenuItem{Title: "Rename File…", Shortcut: "Shift+F6", Run: func() { d.rename(rel) }},
		MenuItem{Separator: true},
		MenuItem{Title: "Local History", Children: []MenuItem{{Title: "Show History", Run: func() { d.work().OpenLocalHistory(rel, 0) }}}},
	)
	if v.ctl.Vcs.Available {
		items = append(items, MenuItem{Title: "Git", Children: d.gitMenu(rel, false)})
	}
	return items
}

// decorateTab marks tabs whose file changed or vanished on disk.
func (d *workDock) decorateTab(t *controller.Tab, it *tabs.Item) {
	if t.Virtual != "" {
		return
	}
	e := d.v.ctl.Work.ExternalOf(t.File)
	if e == nil {
		return
	}
	it.Glyph = atoms.GlyphWarning
	if e.Deleted() {
		it.Tooltip = t.Path + " – deleted on disk"
		if e.RenamedTo != "" {
			it.Tooltip = t.Path + " – renamed on disk to " + e.RenamedTo
		}
		return
	}
	it.Tooltip = t.Path + " – changed on disk"
}

// parentOf returns the project-relative parent directory of rel ("" for
// top-level entries).
func parentOf(rel string) string {
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}
