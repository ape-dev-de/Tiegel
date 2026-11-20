package git

import (
	"image"
	"runtime"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Heights of the commit message editor: it starts at messageMinH and
// grows with its text up to messageMaxShare of the panel height.
const (
	messageMinH     = unit.Dp(72)
	messageMaxShare = 0.4
)

// Toolbar item IDs of the commit panel.
const (
	toolRefresh  = "refresh"
	toolRollback = "rollback"
	toolUpdate   = "update"
	toolStash    = "stash"
	toolDiff     = "diff"
	toolExpand   = "expand"
	toolCollapse = "collapse"
)

// groupingLabels are the view options in gitcli.ChangeGroupings order.
var groupingLabels = map[gitcli.ChangeGrouping]string{
	gitcli.GroupDirectory: "Group by Directory",
	gitcli.GroupModule:    "Group by Module",
	gitcli.GroupFlat:      "No Grouping",
}

// CommitPanel is the commit tool window: tabs "Commit" and "Stash"; a
// toolbar (refresh, rollback, update, stash, show diff, view options,
// expand/collapse all); the change tree with the root groups "Changes"
// (with the branch badge) and "Unversioned Files", grouped by directory,
// module or flat, tri-state checkboxes per group, directory, file, hunk
// and line, file icons and status colours; below it Amend with the last
// commit, the message history, a message editor that grows with its text,
// "Commit", "Commit and Push…" and the commit options (gear).
//
// The panel shows no diff itself: double click or Enter on a file (and
// "Show Diff") reports DiffOpenRequested and the host opens the diff as
// an editor tab. Up/Down move between files, Space toggles the file, F4
// opens the file in the editor. Cmd/Ctrl+Enter commits.
//
// Data in: SetChanges, SetDiff, SetBranch, SetLastCommit, SetHistory,
// SetAmendMessage, SetBusy, SetProblem, SetGrouping, Reset; the host's
// Stash widget and FileGlyph provider. Events out: CommitRequested,
// AmendMessageRequested, DiffRequested, DiffOpenRequested,
// FileOpenRequested, RefreshRequested, RollbackRequested,
// UpdateRequested, StashRequested, GroupingChanged, StashTabShown.
//
// The commit buttons are disabled while busy, when the message is empty
// or when nothing is selected (with Amend an empty selection is allowed:
// it rewords the last commit). Conflicted files are listed but cannot be
// selected.
type CommitPanel struct {
	// FileGlyph returns the icon of a file (nil: DefaultFileGlyph).
	FileGlyph FileGlyphFunc
	// Stash draws the content of the "Stash" tab (nil: a placeholder).
	Stash func(gtx layout.Context, th *tokens.Theme) layout.Dimensions

	files    []*fileSel
	tree     gitcli.ChangeTree
	nodeFile map[*gitcli.ChangeNode]int   // file nodes → file index
	under    map[*gitcli.ChangeNode][]int // group/dir nodes → file indexes
	groups   [groupCount][]int            // file indexes per group, tree order
	dirs     map[string]*dirState
	closed   map[string]bool // collapsed group/dir keys
	grouping gitcli.ChangeGrouping
	branch   string
	rows     []panelRow
	dirty    bool
	selected int // file index, -1 = none

	message   atoms.TextField
	history   molecules.Dropdown
	amend     atoms.Checkbox
	amendMsg  string // message loaded for amend
	lastMsg   string // subject of the last commit
	commit    atoms.Button
	push      atoms.Button
	gear      atoms.IconButton
	gearTip   atoms.Tooltip
	histTip   atoms.Tooltip
	showOpts  bool
	signOff   atoms.Checkbox
	noVerify  atoms.Checkbox
	tabs      [2]atoms.Button
	stashTab  bool
	tools     molecules.Toolbar
	view      molecules.Dropdown
	viewTip   atoms.Tooltip
	list      molecules.ScrollList
	busy      bool
	problem   *Problem
	q         queue
	keys      int // tag for the Cmd+Enter shortcut
	focus     int // tag of the change tree's keyboard focus
	historyIt []string
	// headH is the height above the change tree (tests locate rows).
	headH int
}

// dirState is the UI state of a group or directory row.
type dirState struct {
	cb  atoms.Checkbox
	row molecules.ListRow
	arr atoms.IconButton
}

// NewCommitPanel returns an empty panel.
func NewCommitPanel() *CommitPanel {
	p := &CommitPanel{selected: -1, grouping: gitcli.GroupDirectory, dirs: map[string]*dirState{}, closed: map[string]bool{}}
	p.message.Hint = "Commit message"
	p.message.Editor.SingleLine = false
	p.amend.Label = "Amend"
	p.commit = atoms.Button{Kind: atoms.Primary, Text: "Commit", Glyph: atoms.GlyphCommit}
	p.push = atoms.Button{Kind: atoms.Secondary, Text: "Commit and Push…", Glyph: atoms.GlyphUpload}
	p.gear = atoms.IconButton{Glyph: atoms.GlyphSettings, Description: "Commit options"}
	p.gearTip.Text, p.gearTip.Above = "Commit options", true
	p.histTip.Text, p.histTip.Above = "Recent commit messages", true
	p.signOff.Label = "Sign-off commit"
	p.noVerify.Label = "Skip git hooks"
	p.tabs[0] = atoms.Button{Text: "Commit", Kind: atoms.Secondary}
	p.tabs[1] = atoms.Button{Text: "Stash", Kind: atoms.Ghost}
	p.history = molecules.Dropdown{Glyph: atoms.GlyphHistory, Description: "Recent messages", Up: true, Width: 360}
	p.view = molecules.Dropdown{Glyph: glyphViewOptions, Description: "View options", Width: 220}
	p.viewTip.Text = "View options"
	for _, g := range gitcli.ChangeGroupings {
		p.view.Items = append(p.view.Items, groupingLabels[g])
	}
	p.tools.Items = []molecules.ToolbarItem{
		{ID: toolRefresh, Glyph: atoms.GlyphRefresh, Label: "Refresh"},
		{ID: toolRollback, Glyph: glyphRollback, Label: "Rollback…"},
		{ID: toolUpdate, Glyph: glyphUpdate, Label: "Update Project…"},
		{ID: toolStash, Glyph: glyphStash, Label: "Stash Changes…"},
		{ID: toolDiff, Glyph: glyphShowDiff, Label: "Show Diff", Shortcut: "Enter"},
		{Separator: true},
		{ID: toolExpand, Glyph: glyphExpandAll, Label: "Expand All"},
		{ID: toolCollapse, Glyph: glyphCollapseAll, Label: "Collapse All"},
	}
	p.rebuildTree()
	return p
}

// SetChanges replaces the file list. Selections, expansion and the
// selected file are kept for paths that are still present; partial
// selections are kept only when the diff did not change. New tracked
// changes start selected, new unversioned files unselected.
func (p *CommitPanel) SetChanges(changes []Change) {
	old := make(map[string]*fileSel, len(p.files))
	for _, f := range p.files {
		old[f.change.Status.Path] = f
	}
	selPath := p.SelectedPath()
	p.files = p.files[:0]
	p.selected = -1
	for _, c := range changes {
		g := groupChanges
		switch c.Status.Kind {
		case gitcli.KindIgnored:
			continue
		case gitcli.KindUntracked:
			g = groupUnversioned
		}
		var f *fileSel
		if o := old[c.Status.Path]; o != nil {
			f = o
			if o.change.Diff != c.Diff {
				st := o.state()
				if sameHunks(o.change.Diff, c.Diff) {
					lines, chosen, exp := o.lines, o.chosen, o.hunkExpanded
					f.setDiff(c.Diff, false)
					copy(f.chosen, chosen)
					for h := range lines {
						copy(f.lines[h], lines[h])
					}
					copy(f.hunkExpanded, exp)
					f.syncWhole()
					if !f.lineBased() {
						f.whole = st == atoms.Checked
					}
				} else {
					f.setDiff(c.Diff, st != atoms.Unchecked)
				}
			}
			f.change.Status = c.Status
			f.group = g
			f.disabled = c.Status.IsConflict()
			if f.disabled {
				f.setAll(false)
			}
		} else {
			f = newFileSel(c, g, g == groupChanges)
		}
		if c.Status.Path == selPath {
			p.selected = len(p.files)
		}
		p.files = append(p.files, f)
	}
	p.rebuildTree()
}

// rebuildTree groups the files (gitcli.BuildChangeTree) and maps nodes to
// files.
func (p *CommitPanel) rebuildTree() {
	st := make([]gitcli.FileStatus, len(p.files))
	idx := make(map[string]int, len(p.files))
	for i, f := range p.files {
		st[i] = f.change.Status
		idx[f.change.Status.Path] = i
	}
	p.tree = gitcli.BuildChangeTree(st, p.grouping, p.branch)
	p.nodeFile = map[*gitcli.ChangeNode]int{}
	p.under = map[*gitcli.ChangeNode][]int{}
	var walk func(n *gitcli.ChangeNode) []int
	walk = func(n *gitcli.ChangeNode) []int {
		if n.Kind == gitcli.NodeFile {
			i, ok := idx[n.Path]
			if !ok {
				return nil
			}
			p.nodeFile[n] = i
			return []int{i}
		}
		var out []int
		for _, c := range n.Children {
			out = append(out, walk(c)...)
		}
		p.under[n] = out
		return out
	}
	p.groups[groupChanges] = walk(&p.tree.Changes)
	p.groups[groupUnversioned] = walk(&p.tree.Unversioned)
	p.dirty = true
}

// SetDiff supplies the diff of path (answer to DiffRequested). The file is
// selected as a whole afterwards if it was selected before.
func (p *CommitPanel) SetDiff(filePath string, d *gitcli.FileDiff) {
	for _, f := range p.files {
		if f.change.Status.Path != filePath {
			continue
		}
		f.setDiff(d, f.state() != atoms.Unchecked)
		p.dirty = true
		return
	}
}

// SetBranch sets the badge of the "Changes" group (the current branch).
func (p *CommitPanel) SetBranch(b string) {
	if b != p.branch {
		p.branch = b
		p.tree.Changes.Badge = b
	}
}

// SetGrouping groups the change tree (no event).
func (p *CommitPanel) SetGrouping(g gitcli.ChangeGrouping) {
	if g == "" {
		g = gitcli.GroupDirectory
	}
	if g != p.grouping {
		p.grouping = g
		p.rebuildTree()
	}
}

// Grouping returns the grouping of the change tree.
func (p *CommitPanel) Grouping() gitcli.ChangeGrouping { return p.grouping }

// Order returns the file paths in tree order (the diff tab's sequence).
func (p *CommitPanel) Order() []string { return p.tree.Order() }

// SetHistory sets the recent commit messages offered in the history
// dropdown, newest first. The first one is the last commit (shown next to
// Amend) unless SetLastCommit says otherwise.
func (p *CommitPanel) SetHistory(msgs []string) {
	p.historyIt = append(p.historyIt[:0], msgs...)
	if len(msgs) > 0 {
		p.lastMsg = firstLine(msgs[0])
	}
}

// SetLastCommit sets the subject of the last commit shown next to Amend.
func (p *CommitPanel) SetLastCommit(subject string) { p.lastMsg = firstLine(subject) }

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(s)
}

// SetAmendMessage supplies the last commit's message (answer to
// AmendMessageRequested). It fills the message field if it is empty.
func (p *CommitPanel) SetAmendMessage(msg string) {
	p.amendMsg = msg
	if p.lastMsg == "" {
		p.lastMsg = firstLine(msg)
	}
	if p.amend.Checked() && strings.TrimSpace(p.message.Text()) == "" {
		p.message.SetText(msg)
	}
}

// SelectedPath returns the path of the selected file ("" if none), e.g.
// for "Show Diff".
func (p *CommitPanel) SelectedPath() string {
	if p.selected < 0 || p.selected >= len(p.files) {
		return ""
	}
	return p.files[p.selected].change.Status.Path
}

// Select selects the file at path (e.g. following the diff tab); it
// reports whether the path is listed.
func (p *CommitPanel) Select(path string) bool {
	for i, f := range p.files {
		if f.change.Status.Path == path {
			p.selected = i
			return true
		}
	}
	return false
}

// SetBusy disables the commit buttons while the host commits or pushes.
func (p *CommitPanel) SetBusy(b bool) { p.busy = b }

// Busy reports the busy state.
func (p *CommitPanel) Busy() bool { return p.busy }

// SetProblem shows (or with nil hides) an error above the buttons, e.g.
// ProblemFromError(err) of a failed commit or push.
func (p *CommitPanel) SetProblem(pr *Problem) { p.problem = pr }

// Problem returns the problem shown, or nil.
func (p *CommitPanel) Problem() *Problem { return p.problem }

// Reset clears message, Amend and problem after a successful commit.
func (p *CommitPanel) Reset() {
	p.message.SetText("")
	p.amend.State = atoms.Unchecked
	p.amendMsg = ""
	p.problem = nil
	p.busy = false
}

// Message returns the current commit message.
func (p *CommitPanel) Message() string { return p.message.Text() }

// SetMessage replaces the commit message.
func (p *CommitPanel) SetMessage(s string) { p.message.SetText(s) }

// Amend reports whether Amend is checked.
func (p *CommitPanel) Amend() bool { return p.amend.Checked() }

// StashShown reports whether the "Stash" tab is shown.
func (p *CommitPanel) StashShown() bool { return p.stashTab }

// Selection returns the current commit selection in tree order.
func (p *CommitPanel) Selection() []FileSelection {
	var out []FileSelection
	for g := range p.groups {
		for _, i := range p.groups[g] {
			if fs, ok := p.files[i].selection(); ok {
				out = append(out, fs)
			}
		}
	}
	return out
}

// CanCommit reports whether the commit buttons are enabled.
func (p *CommitPanel) CanCommit() bool {
	if p.busy || strings.TrimSpace(p.message.Text()) == "" {
		return false
	}
	return p.amend.Checked() || p.hasSelection()
}

func (p *CommitPanel) hasSelection() bool {
	for _, f := range p.files {
		if f.state() != atoms.Unchecked {
			return true
		}
	}
	return false
}

// Update returns the events since the last call.
func (p *CommitPanel) Update(gtx layout.Context) []Event {
	p.update(gtx)
	return p.q.drain()
}

func (p *CommitPanel) emit(gtx layout.Context, e Event) {
	p.q.emit(e)
	gtx.Execute(op.InvalidateCmd{})
}

// update handles the controls outside the virtualized tree.
func (p *CommitPanel) update(gtx layout.Context) {
	for i := range p.tabs {
		if p.tabs[i].Clicked(gtx) && (i == 1) != p.stashTab {
			p.stashTab = i == 1
			p.emit(gtx, StashTabShown{Shown: p.stashTab})
		}
	}
	for _, id := range p.tools.Update(gtx) {
		p.toolbar(gtx, id)
	}
	if i, ok := p.view.Update(gtx); ok && i < len(gitcli.ChangeGroupings) {
		if g := gitcli.ChangeGroupings[i]; g != p.grouping {
			p.SetGrouping(g)
			p.emit(gtx, GroupingChanged{Grouping: g})
		}
	}
	if p.amend.Update(gtx) {
		if p.amend.Checked() {
			if p.amendMsg == "" {
				p.emit(gtx, AmendMessageRequested{})
			} else if strings.TrimSpace(p.message.Text()) == "" {
				p.message.SetText(p.amendMsg)
			}
		} else if p.message.Text() == p.amendMsg {
			p.message.SetText("")
		}
	}
	p.history.Items = p.historyIt
	if i, ok := p.history.Update(gtx); ok && i < len(p.historyIt) {
		p.message.SetText(p.historyIt[i])
	}
	if p.gear.Clicked(gtx) {
		p.showOpts = !p.showOpts
	}
	p.signOff.Update(gtx)
	p.noVerify.Update(gtx)
	shortcut := false
	for {
		e, ok := gtx.Event(
			key.Filter{Name: key.NameReturn, Required: key.ModShortcut},
			key.Filter{Name: key.NameEnter, Required: key.ModShortcut},
		)
		if !ok {
			break
		}
		if ke, ok := e.(key.Event); ok && ke.State == key.Press {
			shortcut = true
		}
	}
	p.treeKeys(gtx)
	can := p.CanCommit()
	p.commit.Disabled, p.push.Disabled = !can, !can
	if p.commit.Clicked(gtx) || (shortcut && can) {
		p.requestCommit(gtx, false)
	}
	if p.push.Clicked(gtx) {
		p.requestCommit(gtx, true)
	}
}

// toolbar runs a toolbar item.
func (p *CommitPanel) toolbar(gtx layout.Context, id string) {
	switch id {
	case toolRefresh:
		p.emit(gtx, RefreshRequested{})
	case toolRollback:
		if paths := p.rollbackPaths(); len(paths) > 0 {
			p.emit(gtx, RollbackRequested{Paths: paths})
		}
	case toolUpdate:
		p.emit(gtx, UpdateRequested{})
	case toolStash:
		p.emit(gtx, StashRequested{})
	case toolDiff:
		p.openDiff(gtx)
	case toolExpand:
		clear(p.closed)
		p.dirty = true
	case toolCollapse:
		p.collapseAll()
	}
}

// rollbackPaths are the checked tracked files, or the selected file.
func (p *CommitPanel) rollbackPaths() []string {
	var out []string
	for _, i := range p.groups[groupChanges] {
		if f := p.files[i]; f.state() != atoms.Unchecked {
			out = append(out, f.change.Status.Path)
		}
	}
	if len(out) == 0 {
		if s := p.SelectedPath(); s != "" {
			out = []string{s}
		}
	}
	return out
}

// openDiff asks for the diff tab of the selected file.
func (p *CommitPanel) openDiff(gtx layout.Context) {
	if p.selected < 0 || p.selected >= len(p.files) {
		return
	}
	st := p.files[p.selected].change.Status
	p.emit(gtx, DiffOpenRequested{Path: st.Path, Untracked: st.Kind == gitcli.KindUntracked})
}

func (p *CommitPanel) requestCommit(gtx layout.Context, push bool) {
	if !p.CanCommit() {
		return
	}
	p.problem = nil
	p.emit(gtx, CommitRequested{
		Message:  strings.TrimRight(p.message.Text(), " \t\r\n"),
		Amend:    p.amend.Checked(),
		Push:     push,
		SignOff:  p.signOff.Checked(),
		NoVerify: p.noVerify.Checked(),
		Files:    p.Selection(),
	})
}

// Layout draws the panel filling the maximum constraints.
func (p *CommitPanel) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	p.update(gtx)
	if p.dirty || p.rows == nil {
		p.buildRows()
	}
	size := gtx.Constraints.Max
	gtx.Constraints = layout.Exact(size)
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &p.keys)
	area.Pop()
	return atoms.Surface{Kind: atoms.SurfacePanel}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		head := 0
		measure := func(w layout.Widget) layout.Widget {
			return func(gtx layout.Context) layout.Dimensions {
				d := w(gtx)
				head += d.Size.Y
				return d
			}
		}
		children := []layout.FlexChild{
			layout.Rigid(measure(func(gtx layout.Context) layout.Dimensions { return p.layoutTabs(gtx, th) })),
			layout.Rigid(measure(atoms.Divider{}.Fn(th))),
		}
		if p.stashTab {
			children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if p.Stash == nil {
					return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted("No stash view").Fn(th))
				}
				gtx.Constraints.Min = gtx.Constraints.Max
				return p.Stash(gtx, th)
			}))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		}
		children = append(children,
			layout.Rigid(measure(func(gtx layout.Context) layout.Dimensions { return p.layoutToolbar(gtx, th) })),
			layout.Rigid(measure(atoms.Divider{}.Fn(th))),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				p.headH = head
				return p.layoutTree(gtx, th)
			}),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: th.Space.M, Right: th.Space.M, Top: th.Space.S, Bottom: th.Space.M}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.layoutMessage(gtx, th, size.Y)
				})
			}),
		)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// layoutTabs draws "Commit" | "Stash".
func (p *CommitPanel) layoutTabs(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	p.tabs[0].Kind, p.tabs[1].Kind = atoms.Secondary, atoms.Ghost
	if p.stashTab {
		p.tabs[0].Kind, p.tabs[1].Kind = atoms.Ghost, atoms.Secondary
	}
	return atoms.Surface{Kind: atoms.SurfaceAlt}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.tabs[0].Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.tabs[1].Layout(gtx, th) }),
			)
		})
	})
}

func (p *CommitPanel) layoutToolbar(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	empty := len(p.files) == 0
	for i := range p.tools.Items {
		switch p.tools.Items[i].ID {
		case toolRollback:
			p.tools.Items[i].Disabled = len(p.rollbackPaths()) == 0 || p.busy
		case toolStash:
			p.tools.Items[i].Disabled = empty || p.busy
		case toolDiff:
			p.tools.Items[i].Disabled = p.selected < 0
		case toolExpand, toolCollapse:
			p.tools.Items[i].Disabled = empty
		}
	}
	return layout.Inset{Left: th.Space.XS, Right: th.Space.XS, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.tools.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return p.viewTip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return p.view.Layout(gtx, th) })
			}),
			layout.Flexed(1, layout.Spacer{}.Layout),
		)
	})
}

func (p *CommitPanel) layoutMessage(gtx layout.Context, th *tokens.Theme, panelH int) layout.Dimensions {
	can := p.CanCommit()
	p.commit.Disabled, p.push.Disabled = !can, !can
	gap := func(h unit.Dp) layout.FlexChild { return layout.Rigid(layout.Spacer{Height: h}.Layout) }
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Amend [last commit] … history.
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.amend.Layout(gtx, th) }),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if p.lastMsg == "" {
						return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
					}
					return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx, atoms.Small(p.lastMsg).SingleLine().Fn(th))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					p.history.Items = p.historyIt
					return p.histTip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return p.history.Layout(gtx, th) })
				}),
			)
		}),
		gap(th.Space.XS),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutEditor(gtx, th, panelH) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.problem == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: th.Space.S}.Layout(gtx, p.problem.banner().Fn(th))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !p.showOpts {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.signOff.Layout(gtx, th) }),
					layout.Rigid(layout.Spacer{Width: th.Space.M}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.noVerify.Layout(gtx, th) }),
				)
			})
		}),
		gap(th.Space.S),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.commit.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.push.Layout(gtx, th) }),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					hint := shortcutHint()
					if p.busy {
						hint = "Working…"
					}
					return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(hint).SingleLine().Fn(th))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					p.gear.Active = p.showOpts
					return p.gearTip.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions { return p.gear.Layout(gtx, th) })
				}),
			)
		}),
	)
}

// layoutEditor draws the message field: at least messageMinH, growing
// with its text up to messageMaxShare of the panel; longer text is
// clipped (the editor scrolls its caret into view).
func (p *CommitPanel) layoutEditor(gtx layout.Context, th *tokens.Theme, panelH int) layout.Dimensions {
	minH := gtx.Dp(messageMinH)
	maxH := max(minH, int(float32(panelH)*messageMaxShare))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	gtx.Constraints.Min.Y = minH
	gtx.Constraints.Max.Y = maxH
	m := op.Record(gtx.Ops)
	d := p.message.Layout(gtx, th)
	call := m.Stop()
	defer clip.Rect{Max: d.Size}.Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return d
}

// shortcutHint describes the commit shortcut for the platform.
func shortcutHint() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+Enter to commit"
	}
	return "Ctrl+Enter to commit"
}
