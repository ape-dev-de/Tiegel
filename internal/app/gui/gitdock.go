package gui

import (
	"fmt"
	"image"
	"path"
	"sort"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	gitui "github.com/ape-dev-de/tiegel/internal/ui/organisms/git"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// The git feature docks onto the shell only through the Registry: the
// "Commit" and "Git" tool windows, per-file merge windows, gutter change
// markers with a change popup, the branch status item and the git
// shortcuts. Everything is installed while the git module runs (and the
// project is a repository) and removed again when it stops.

// Git tool window and action IDs.
const (
	ToolCommit = "commit"
	ToolGit    = "git"
	toolMerge  = "merge:"

	ActCommit   keymap.Action = "git.commit"
	ActPush     keymap.Action = "git.push"
	ActRollback keymap.Action = "git.rollback"
	ActGitView  keymap.Action = "view.git"
	// ActCommitView toggles the commit tool window.
	ActCommitView keymap.Action = "view.commit"
)

// gitDock is the view side of the git feature.
type gitDock struct {
	v         *View
	installed bool

	panel *gitui.CommitPanel
	seen  struct {
		changes, diffs, amend, problem, committed uint64
		log, branches, commit, merge, block       uint64
		gitVersion                                uint64
		logLen                                    int
	}

	tab          int // 0 log, 1 branches, 2 stashes
	tabBtns      [3]atoms.Button
	log          *gitui.LogView
	branches     *gitui.BranchList
	logSplit     molecules.Split
	detailSplit  molecules.Split
	files        molecules.ScrollList
	fileRows     []molecules.FileRow
	fileSel      int
	commitDiff   *gitui.DiffView
	conflictBtns map[string]*atoms.Button

	merges     map[string]*gitui.MergeView
	blockDiff  *gitui.DiffView
	blockShown uint64

	// focusDiff asks the changes diff tab to take the keyboard focus once
	// (opened from the commit window).
	focusDiff bool
}

func installGit(v *View) *gitDock {
	d := &gitDock{
		v: v, panel: gitui.NewCommitPanel(), log: gitui.NewLogView(), branches: gitui.NewBranchList(),
		commitDiff: gitui.NewDiffView(), blockDiff: gitui.NewDiffView(),
		merges: map[string]*gitui.MergeView{}, conflictBtns: map[string]*atoms.Button{},
	}
	d.logSplit.Ratio = 0.5
	d.detailSplit = molecules.Split{Vertical: true, Ratio: 0.3, MinPane: 40}
	d.commitDiff.Placeholder = "Select a commit to see its changes"
	d.blockDiff.HideToolbar = true
	// The change tree uses the project tree's file icons.
	d.panel.FileGlyph = func(p string) *atoms.Glyph { return fileGlyph(p) }
	d.tabBtns[0].Text, d.tabBtns[1].Text, d.tabBtns[2].Text = "Log", "Branches", "Stashes"
	r := v.Registry
	r.AddFrameHandler(d.frame)
	r.AddGutterProvider(d.markers)
	r.AddGutterClickHandler(d.gutterClick)
	return d
}

func (d *gitDock) git() *controller.Git { return d.v.ctl.Vcs }

// frame installs or removes the tool windows and actions and shows the
// change popup.
func (d *gitDock) frame(v *View, gtx layout.Context) {
	g := d.git()
	on := g.Available && v.ctl.Project.Open
	if on != d.installed {
		d.installed = on
		if on {
			d.install()
		} else {
			d.uninstall()
		}
	}
	// The open commit window follows repository changes (other clients,
	// saves).
	if d.installed && v.ToolShown(ToolCommit) && d.seen.gitVersion != v.ctl.GitVersion && !g.Busy {
		d.seen.gitVersion = v.ctl.GitVersion
		g.LoadChanges()
	}
	if d.seen.merge != g.MergeVersion {
		d.seen.merge = g.MergeVersion
		d.syncMerges()
	}
	if d.seen.block != g.BlockVersion {
		d.seen.block = g.BlockVersion
		if b := g.Block; b != nil {
			d.showBlock(b)
		}
	}
}

func (d *gitDock) install() {
	r := d.v.Registry
	g := d.git()
	r.AddToolWindow(ToolWindow{
		ID: ToolCommit, Title: "Commit", Glyph: atoms.GlyphCommit, Order: 20, Share: 0.45,
		Mac: "Cmd+0", PC: "Alt+0", ActionID: ActCommitView,
		Badge: func() (string, atoms.BadgeKind) {
			if n := len(d.v.ctl.Git); n > 0 {
				return fmt.Sprint(n), atoms.BadgeInfo
			}
			return "", 0
		},
		Layout: d.layoutCommit,
		OnOpen: func() {
			d.seen.gitVersion = d.v.ctl.GitVersion
			g.LoadChanges()
		},
	})
	r.AddToolWindow(ToolWindow{
		ID: ToolGit, Title: "Git", Glyph: atoms.GlyphBranch, Order: 30, Share: 0.45,
		Mac: "Cmd+9", PC: "Alt+9", ActionID: ActGitView,
		Badge: func() (string, atoms.BadgeKind) {
			if n := len(g.Conflicts); n > 0 {
				return fmt.Sprint(n), atoms.BadgeDanger
			}
			return "", 0
		},
		Layout: d.layoutGit,
		OnOpen: func() {
			g.LoadLog(true)
			g.LoadBranches()
		},
	})
	for _, a := range d.actions() {
		r.AddAction(a)
	}
}

func (d *gitDock) uninstall() {
	r := d.v.Registry
	d.v.HideTool(ToolCommit)
	d.v.HideTool(ToolGit)
	r.RemoveToolWindow(ToolCommit)
	r.RemoveToolWindow(ToolGit)
	for p := range d.merges {
		r.RemoveToolWindow(toolMerge + p)
		delete(d.merges, p)
	}
	for _, a := range d.actions() {
		r.RemoveAction(a.ID)
	}
}

func (d *gitDock) actions() []Action {
	return []Action{
		{ID: ActCommit, Title: "Commit…", Mac: "Cmd+K", PC: "Ctrl+K", Menu: "Git", Section: "commit", Run: func(v *View) {
			v.ShowTool(ToolCommit)
		}},
		{ID: ActPush, Title: "Push", Mac: "Cmd+Shift+K", PC: "Ctrl+Shift+K", Menu: "Git", Section: "commit", Run: func(v *View) {
			v.ctl.Status.Notice = "Pushing…"
			d.git().Push()
		}},
		{ID: ActRollback, Title: "Rollback Changes", Mac: "Cmd+Alt+Z", PC: "Ctrl+Alt+Z", Menu: "Git", Section: "changes", Run: func(v *View) { d.rollbackAtCaret() }},
	}
}

// ---- commit window --------------------------------------------------------

func (d *gitDock) syncPanel() {
	g, p := d.git(), d.panel
	if d.seen.changes != g.ChangesVersion {
		d.seen.changes = g.ChangesVersion
		changes := make([]gitui.Change, 0, len(g.Changes))
		for _, c := range g.Changes {
			changes = append(changes, gitui.Change{Status: c.Status, Diff: c.Diff})
		}
		p.SetChanges(changes)
		p.SetHistory(g.History)
		g.Grouping = p.Grouping()
		d.applyUntrackedDiffs()
	}
	if d.seen.diffs != g.DiffsVersion {
		d.seen.diffs = g.DiffsVersion
		d.applyUntrackedDiffs()
	}
	if d.seen.amend != g.AmendVersion {
		d.seen.amend = g.AmendVersion
		p.SetAmendMessage(g.AmendMessage)
	}
	if d.seen.committed != g.Committed {
		d.seen.committed = g.Committed
		p.Reset()
	}
	if d.seen.problem != g.ProblemVersion {
		d.seen.problem = g.ProblemVersion
		p.SetProblem(problemOf(g.Problem))
	}
	p.SetBusy(g.Busy)
	p.SetBranch(g.Info.Branch)
}

// applyUntrackedDiffs supplies the loaded diffs of new files that are
// still untracked.
func (d *gitDock) applyUntrackedDiffs() {
	g := d.git()
	for _, c := range g.Changes {
		if c.Status.Kind != gitcli.KindUntracked || c.Diff != nil {
			continue
		}
		if diff := g.Diffs[c.Status.Path]; diff != nil {
			d.panel.SetDiff(c.Status.Path, diff)
		}
	}
}

// problemOf turns a controller problem into the panel's banner.
func problemOf(pr *controller.GitProblem) *gitui.Problem {
	switch {
	case pr == nil:
		return nil
	case pr.Stale:
		return &gitui.Problem{Warning: true, Title: "The changes were modified after the diff was loaded (e.g. by a formatter); nothing was committed.",
			Detail: "The diff has been reloaded. Check the selected changes and commit again."}
	case pr.Push:
		prefix := "Push failed. "
		if pr.Committed {
			prefix = "Committed, but the push failed. "
		}
		detail := pr.Message
		if s := strings.TrimSpace(pr.Stderr); s != "" && !strings.Contains(detail, s) {
			detail = strings.TrimSpace(detail + "\n" + s)
		}
		return &gitui.Problem{Title: prefix + gitui.PushErrorTitle(pr.PushKind), Detail: detail}
	}
	return gitui.ProblemFromError(pr.Err)
}

// commitRequest converts the panel's request.
func commitRequest(e gitui.CommitRequested) controller.GitCommitRequest {
	req := controller.GitCommitRequest{Message: e.Message, Amend: e.Amend, Push: e.Push, SignOff: e.SignOff, NoVerify: e.NoVerify}
	for _, f := range e.Files {
		req.Files = append(req.Files, controller.GitFileSelection{Path: f.Path, OrigPath: f.OrigPath, Untracked: f.Untracked, Whole: f.Whole, Hunks: f.Hunks})
	}
	return req
}

func (d *gitDock) handlePanel(gtx layout.Context) {
	g := d.git()
	for _, e := range d.panel.Update(gtx) {
		switch e := e.(type) {
		case gitui.CommitRequested:
			d.handleCommitRequest(e)
		case gitui.AmendMessageRequested:
			g.LoadAmendMessage()
		case gitui.DiffRequested:
			if e.Untracked {
				g.LoadUntrackedDiff(e.Path)
			}
		case gitui.RefreshRequested:
			g.LoadChanges()
		case gitui.FileOpenRequested:
			d.v.ctl.OpenFile(d.v.ctl.Abs(e.Path), nil)
			d.v.focusEditor = true
		case gitui.DiffOpenRequested:
			d.openChangeDiff(e.Path)
		case gitui.RollbackRequested:
			if d.v.work != nil {
				paths := e.Paths
				d.v.Post(func() { d.v.work.rollback(paths) })
			}
		case gitui.UpdateRequested:
			if d.v.work != nil {
				d.v.Post(func() { d.v.work.openPullDialog(true) })
			}
		case gitui.StashRequested:
			if d.v.work != nil {
				d.v.Post(d.v.work.openStashDialog)
			}
		case gitui.StashTabShown:
			if e.Shown {
				d.v.ctl.Work.LoadStashes()
			}
		case gitui.GroupingChanged:
			g.Grouping = e.Grouping
		}
	}
}

// handleCommitRequest commits the panel's selection.
func (d *gitDock) handleCommitRequest(e gitui.CommitRequested) {
	d.panel.SetBusy(true)
	d.git().Commit(commitRequest(e))
}

// openChangeDiff opens the diff of a change as the changes editor tab
// (HEAD ↔ work tree) and focuses it.
func (d *gitDock) openChangeDiff(p string) {
	d.git().Grouping = d.panel.Grouping()
	d.v.ctl.Work.OpenChangeDiff(p, gitcli.DiffLocal)
	d.focusDiff = true
}

func (d *gitDock) layoutCommit(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if d.panel.Stash == nil && d.v.work != nil {
		d.panel.Stash = d.v.work.layoutStashes
	}
	d.syncPanel()
	d.handlePanel(gtx)
	return d.panel.Layout(gtx, th)
}

// ---- git window: log, branches, conflicts ----------------------------------

func (d *gitDock) syncGit() {
	g := d.git()
	if d.seen.log != g.LogVersion {
		d.seen.log = g.LogVersion
		d.log.SetCommits(g.Log, g.LogMore)
		d.seen.logLen = len(g.Log)
	} else if len(g.Log) != d.seen.logLen {
		d.log.AppendCommits(g.Log[min(d.seen.logLen, len(g.Log)):], g.LogMore)
		d.seen.logLen = len(g.Log)
	}
	if d.seen.branches != g.BranchesVersion {
		d.seen.branches = g.BranchesVersion
		d.branches.SetBranches(g.Branches)
	}
	if d.seen.commit != g.CommitVersion {
		d.seen.commit = g.CommitVersion
		d.fileRows = make([]molecules.FileRow, len(g.CommitFiles))
		d.fileSel = -1
		d.commitDiff.SetDiff(nil)
		if len(g.CommitFiles) > 0 {
			d.selectCommitFile(0)
		}
	}
}

func (d *gitDock) selectCommitFile(i int) {
	g := d.git()
	if i < 0 || i >= len(g.CommitFiles) {
		return
	}
	d.fileSel = i
	d.commitDiff.SetDiff(&g.CommitFiles[i])
}

func (d *gitDock) handleGit(gtx layout.Context) {
	g := d.git()
	for i := range d.tabBtns {
		if d.tabBtns[i].Clicked(gtx) {
			d.tab = i
			if i == gitTabStashes {
				d.v.ctl.Work.LoadStashes()
			}
		}
	}
	for _, e := range d.log.Update(gtx) {
		switch e := e.(type) {
		case gitui.CommitSelected:
			g.SelectCommit(e.Hash)
		case gitui.LogPageRequested:
			g.LoadLog(false)
		}
	}
	for _, e := range d.branches.Update(gtx) {
		switch e := e.(type) {
		case gitui.CheckoutRequested:
			g.Checkout(e.Branch, e.Remote)
		case gitui.CreateBranchRequested:
			g.CreateBranch(e.Name, e.Start, e.Checkout)
		case gitui.DeleteBranchRequested:
			name := e.Name
			d.v.Confirm("Delete branch "+name+"?", "The branch is deleted locally; unmerged commits are refused by git.", "Delete", func() {
				g.DeleteBranch(name, false)
			})
		}
	}
	for p, b := range d.conflictBtns {
		if b.Clicked(gtx) {
			g.OpenMerge(p)
		}
	}
}

func (d *gitDock) layoutGit(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	d.syncGit()
	d.handleGit(gtx)
	g := d.git()
	size := gtx.Constraints.Max
	gtx.Constraints = layout.Exact(size)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.layoutGitHeader(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(g.Conflicts) == 0 {
				return layout.Dimensions{}
			}
			return d.layoutConflicts(gtx, th)
		}),
		layout.Rigid(atoms.Divider{}.Fn(th)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if d.tab == 1 {
				return d.branches.Layout(gtx, th)
			}
			if d.tab == gitTabStashes {
				return d.v.work.layoutStashes(gtx, th)
			}
			return d.logSplit.Layout(gtx, th,
				func(gtx layout.Context) layout.Dimensions { return d.log.Layout(gtx, th) },
				func(gtx layout.Context) layout.Dimensions { return d.layoutCommitDetail(gtx, th) })
		}),
	)
}

func (d *gitDock) layoutGitHeader(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	info := d.git().Info
	branch := info.Branch
	if info.Detached && len(info.Head) >= 7 {
		branch = "detached at " + info.Head[:7]
	}
	return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{}
		for i := range d.tabBtns {
			b := &d.tabBtns[i]
			b.Kind = atoms.Ghost
			if d.tab == i {
				b.Kind = atoms.Secondary
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
			}))
		}
		children = append(children,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.v.work.layoutGitHeaderButtons(gtx, th) }),
			layout.Rigid(atoms.Small(branchText(branch, info.Ahead, info.Behind)).Fn(th)),
		)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
	})
}

func (d *gitDock) layoutConflicts(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	g := d.git()
	return layout.Inset{Left: th.Space.S, Right: th.Space.S, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		rows := []layout.FlexChild{layout.Rigid(molecules.Banner{Kind: molecules.BannerWarning,
			Title:  fmt.Sprintf("Merge conflicts in %d file(s)", len(g.Conflicts)),
			Detail: "Resolve each file in the merge window; applying marks it resolved."}.Fn(th))}
		btns := []layout.FlexChild{}
		for _, p := range g.Conflicts {
			b := d.conflictBtns[p]
			if b == nil {
				b = &atoms.Button{Kind: atoms.Secondary, Glyph: atoms.GlyphSwap}
				d.conflictBtns[p] = b
			}
			b.Text = "Resolve " + path.Base(p)
			btns = append(btns, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Right: th.Space.XS, Top: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return b.Layout(gtx, th) })
			}))
		}
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx, btns...)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}

func (d *gitDock) layoutCommitDetail(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	g := d.git()
	if len(g.CommitFiles) == 0 {
		return d.commitDiff.Layout(gtx, th)
	}
	for i := range d.fileRows {
		if cl, _ := d.fileRows[i].Update(gtx); cl {
			d.selectCommitFile(i)
			if d.fileRows[i].DoubleClicked() {
				d.v.work.openCommitFileDiff(i)
			}
		}
	}
	files := func(gtx layout.Context) layout.Dimensions {
		return d.files.Layout(gtx, th, len(g.CommitFiles), func(gtx layout.Context, i int) layout.Dimensions {
			f := &g.CommitFiles[i]
			r := &d.fileRows[i]
			p := f.Path()
			r.Name, r.Detail, r.Selected, r.Status = path.Base(p), path.Dir(p), i == d.fileSel, diffStatus(f)
			if r.Detail == "." {
				r.Detail = ""
			}
			return r.Layout(gtx, th)
		})
	}
	return d.detailSplit.Layout(gtx, th, files, func(gtx layout.Context) layout.Dimensions { return d.commitDiff.Layout(gtx, th) })
}

func diffStatus(f *gitcli.FileDiff) molecules.FileStatus {
	switch {
	case f.IsNew:
		return molecules.StatusAdded
	case f.IsDeleted:
		return molecules.StatusDeleted
	}
	return molecules.StatusModified
}

// ---- merge windows ----------------------------------------------------------

// syncMerges adds a merge window per loaded merge and removes closed ones.
func (d *gitDock) syncMerges() {
	g := d.git()
	r := d.v.Registry
	for p := range d.merges {
		if _, ok := g.Merges[p]; !ok {
			d.v.HideTool(toolMerge + p)
			r.RemoveToolWindow(toolMerge + p)
			delete(d.merges, p)
		}
	}
	paths := make([]string, 0, len(g.Merges))
	for p := range g.Merges {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for i, p := range paths {
		if d.merges[p] != nil {
			continue
		}
		mv := gitui.NewMergeView()
		mv.SetMerge(p, g.Merges[p].Result, gitcli.MarkerLabels{Ours: "ours", Base: "base", Theirs: "theirs"})
		d.merges[p] = mv
		id := toolMerge + p
		r.AddToolWindow(ToolWindow{ID: id, Title: "Merge " + path.Base(p), Glyph: atoms.GlyphSwap, Order: 40 + i, Share: 0.6,
			Layout: func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return d.layoutMerge(gtx, th, p) }})
		d.v.ShowTool(id)
	}
}

func (d *gitDock) layoutMerge(gtx layout.Context, th *tokens.Theme, p string) layout.Dimensions {
	mv := d.merges[p]
	if mv == nil {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	g := d.git()
	for _, e := range mv.Update(gtx) {
		switch e := e.(type) {
		case gitui.MergeApplied:
			g.ApplyMerge(p, e.Content)
		case gitui.MergeCancelled:
			g.CloseMerge(p)
		}
	}
	return mv.Layout(gtx, th)
}

// ---- gutter -------------------------------------------------------------------

// markers returns the change markers of file (0-based editor lines).
func (d *gitDock) markers(file string) editorview.MarkerProvider {
	if !d.installed {
		return nil
	}
	ranges := d.git().Gutter(file)
	if len(ranges) == 0 {
		return nil
	}
	m := editorview.LineMarkers{}
	for _, r := range ranges {
		switch r.Kind {
		case "deleted":
			l := max(r.Start-1, 0)
			m[l] = append(m[l], editorview.Marker{Kind: editorview.MarkerGitDeleted})
		default:
			k := editorview.MarkerGitModified
			if r.Kind == "added" {
				k = editorview.MarkerGitAdded
			}
			for l := r.Start - 1; l < r.End-1; l++ {
				m[l] = append(m[l], editorview.Marker{Kind: k})
			}
		}
	}
	return m
}

// gutterClick opens the change popup for a click on a change marker.
func (d *gitDock) gutterClick(file string, ev editorview.GutterClickEvent) bool {
	if !d.installed || ev.Area != editorview.GutterChanges {
		return false
	}
	g := d.git()
	if _, ok := g.RangeAt(file, ev.Line+1); !ok {
		return false
	}
	g.ShowBlock(file, ev.Line+1)
	return true
}

// showBlock opens the popup with the diff of a change and "Rollback".
func (d *gitDock) showBlock(b *controller.GitBlock) {
	g := d.git()
	d.blockDiff.SetDiff(b.Diff)
	file, line := b.File, b.Range.Start
	title := fmt.Sprintf("Lines %d–%d changed", b.Range.Start, max(b.Range.End-1, b.Range.Start))
	switch b.Range.Kind {
	case "added":
		title = fmt.Sprintf("Lines %d–%d added", b.Range.Start, max(b.Range.End-1, b.Range.Start))
	case "deleted":
		title = fmt.Sprintf("%d line(s) deleted", b.Range.BaseEnd-b.Range.BaseStart)
	}
	rows := 0
	if len(b.Diff.Hunks) > 0 {
		rows = len(b.Diff.Hunks[0].Lines)
	}
	d.v.OpenPopup(&Popup{
		Title: title, Detail: d.v.ctl.Rel(file) + " (against HEAD)", Width: 640, BodyHeight: bodyHeight(rows),
		Body: func(gtx layout.Context, th *tokens.Theme) layout.Dimensions { return d.blockDiff.Layout(gtx, th) },
		Buttons: []*PopupButton{
			{Text: "Rollback", Kind: atoms.Primary, OnClick: func() { g.Rollback(file, line) }},
			{Text: "Close", Kind: atoms.Secondary, OnClick: g.CloseBlock},
		},
		OnClose: g.CloseBlock,
	})
}

func bodyHeight(rows int) unit.Dp {
	return unit.Dp(min(max(rows, 2), 14)*20 + 40)
}

// rollbackAtCaret rolls back the change at the caret, or (after asking)
// all changes of the file.
func (d *gitDock) rollbackAtCaret() {
	file, line := d.v.CaretLine()
	if file == "" {
		return
	}
	g := d.git()
	if _, ok := g.RangeAt(file, line); ok {
		g.Rollback(file, line)
		return
	}
	if len(g.Gutter(file)) == 0 {
		d.v.ctl.Status.Notice = "No changes to roll back in " + path.Base(file)
		return
	}
	d.v.Confirm("Roll back all changes in "+path.Base(file)+"?", "The file returns to its HEAD version (undoable in the editor).", "Rollback", func() {
		g.RollbackFile(file)
	})
}

func branchText(branch string, ahead, behind int) string {
	t := branch
	if ahead > 0 {
		t += fmt.Sprintf(" ↑%d", ahead)
	}
	if behind > 0 {
		t += fmt.Sprintf(" ↓%d", behind)
	}
	return t
}
