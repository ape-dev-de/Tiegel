package controller

import (
	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

// The changes diff tab (AP18.4): opening a change in the commit window
// shows its side-by-side diff as one editor tab that steps through the
// changed files in place (next/previous file). The versions and the order
// come from gitcli (ChangeDiff, BuildChangeTree), exactly as for the API
// method vcs.diffTab and `tiegel --headless git-diff-tab`.

// changesDiffKey is the virtual tab id and Workspace.diffs key of the
// changes diff tab.
const changesDiffKey = "changes"

// DiffNav is the position of the changes diff tab in the changed files.
type DiffNav struct {
	Mode gitcli.DiffMode
	// Order lists the changed files in commit window order; Index is the
	// shown file's position (-1 if it is no longer a change).
	Order []string
	Index int
}

// Total returns the number of changed files.
func (n *DiffNav) Total() int { return len(n.Order) }

// Neighbor returns the file delta steps away (wrapping), "" if none.
func (n *DiffNav) Neighbor(cur string, delta int) string {
	p, _, ok := gitcli.Neighbor(n.Order, cur, delta)
	if !ok {
		return ""
	}
	return p
}

// statuses returns the status entries of the loaded changes.
func (g *Git) statuses() []gitcli.FileStatus {
	out := make([]gitcli.FileStatus, 0, len(g.Changes))
	for _, c := range g.Changes {
		out = append(out, c.Status)
	}
	return out
}

// ChangeTree groups the loaded changes like the commit window.
func (g *Git) ChangeTree() gitcli.ChangeTree {
	return gitcli.BuildChangeTree(g.statuses(), g.Grouping, g.Info.Branch)
}

// ChangeOrder returns the changed files in commit window order.
func (g *Git) ChangeOrder() []string { return g.ChangeTree().Order() }

// change returns the status of a changed path.
func (g *Git) change(p string) (gitcli.FileStatus, bool) {
	for _, c := range g.Changes {
		if c.Status.Path == p {
			return c.Status, true
		}
	}
	return gitcli.FileStatus{}, false
}

// ChangesDiff returns the changes diff tab, or nil.
func (w *Workspace) ChangesDiff() *DiffTab { return w.diffs[changesDiffKey] }

// OpenChangeDiff shows the diff of a changed project-relative path in the
// changes diff tab (opened, or reused and activated): HEAD ↔ work tree
// for DiffLocal, HEAD ↔ index for DiffStaged, index ↔ work tree for
// DiffUnstaged; new files compare with the empty file, renames with their
// source.
func (w *Workspace) OpenChangeDiff(p string, mode gitcli.DiffMode) *DiffTab {
	c := w.c
	g := c.Vcs
	st, ok := g.change(p)
	if !ok {
		st = gitcli.FileStatus{Path: p, Index: '.', Worktree: 'M'}
	}
	v := gitcli.ChangeDiff(st, mode)
	spec := DiffSpec{Path: p, BasePath: v.BasePath, Base: v.Base, Target: v.Target, Title: v.Title(p)}
	file := c.Abs(p)
	dt := w.diffs[changesDiffKey]
	if dt == nil {
		dt = &DiffTab{Nav: &DiffNav{}}
		w.diffs[changesDiffKey] = dt
	} else if dt.File != file || dt.Spec.key() != spec.key() {
		w.releaseDoc(dt)
		spec.IgnoreWhitespace = dt.Spec.IgnoreWhitespace
	} else {
		spec.IgnoreWhitespace = dt.Spec.IgnoreWhitespace
	}
	dt.Spec, dt.File = spec, file
	order := g.ChangeOrder()
	dt.Nav.Mode, dt.Nav.Order, dt.Nav.Index = mode, order, -1
	for i, o := range order {
		if o == p {
			dt.Nav.Index = i
		}
	}
	c.OpenVirtual(TabDiff, changesDiffKey, spec.title(), dt)
	w.loadDiff(dt)
	return dt
}

// releaseDoc gives up the right-side document before the changes tab
// shows another file: a document the tab opened itself is saved and
// closed unless an editor tab uses it.
func (w *Workspace) releaseDoc(dt *DiffTab) {
	if dt.ownGen != 0 && w.c.documentFor(dt.File) == nil {
		w.closeOwnDoc(dt.File, dt.ownGen)
	}
	dt.ownGen = 0
	dt.Doc = nil
}

// StepChangeDiff moves the changes diff tab delta files on (wrapping) in
// the current commit window order. It reports whether another file is
// shown.
func (w *Workspace) StepChangeDiff(dt *DiffTab, delta int) bool {
	if dt == nil || dt.Nav == nil || delta == 0 {
		return false
	}
	order := w.c.Vcs.ChangeOrder()
	next, _, ok := gitcli.Neighbor(order, dt.Spec.Path, delta)
	if !ok || next == dt.Spec.Path {
		return false
	}
	w.OpenChangeDiff(next, dt.Nav.Mode)
	return true
}
