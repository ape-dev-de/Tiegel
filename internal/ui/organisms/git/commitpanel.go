package git

import (
	"fmt"
	"path"
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

// messageMinH is the minimum height of the commit message field.
const messageMinH = unit.Dp(96)

// rowKind identifies the rows of the commit panel's file list.
type rowKind int

const (
	rowGroup rowKind = iota
	rowFile
	rowHunk
	rowLine
)

type panelRow struct {
	kind              rowKind
	group, file, hunk int
	line              int
}

// CommitPanel is the commit tool window: changed files grouped into
// "Changes" and "Unversioned Files" with tri-state checkboxes per group,
// file, hunk and line; a diff preview of the selected file; a multi-line
// message with history; Amend; "Commit" and "Commit and Push". Cmd/Ctrl+
// Enter commits.
//
// Data in: SetChanges, SetDiff, SetHistory, SetAmendMessage, SetBusy,
// SetProblem, Reset. Events out: CommitRequested, AmendMessageRequested,
// DiffRequested, RefreshRequested, FileOpenRequested.
//
// The commit buttons are disabled while busy, when the message is empty
// or when nothing is selected (with Amend an empty selection is allowed:
// it rewords the last commit). Conflicted files are listed but cannot be
// selected.
type CommitPanel struct {
	files    []*fileSel
	groups   [groupCount][]int // file indexes per group
	groupCB  [groupCount]atoms.Checkbox
	groupRow [groupCount]molecules.ListRow
	groupArr [groupCount]atoms.IconButton
	groupExp [groupCount]bool
	rows     []panelRow
	dirty    bool
	selected int // file index, -1 = none

	message   atoms.TextField
	history   molecules.Dropdown
	amend     atoms.Checkbox
	amendMsg  string // message loaded for amend
	commit    atoms.Button
	push      atoms.Button
	refresh   atoms.IconButton
	list      molecules.ScrollList
	split     molecules.Split
	diff      *DiffView
	busy      bool
	problem   *Problem
	q         queue
	keys      int // tag for the Cmd+Enter shortcut
	historyIt []string
}

// NewCommitPanel returns an empty panel.
func NewCommitPanel() *CommitPanel {
	p := &CommitPanel{selected: -1, diff: NewDiffView()}
	p.groupExp = [groupCount]bool{true, true}
	p.message.Hint = "Commit message"
	p.message.Editor.SingleLine = false
	p.amend.Label = "Amend"
	p.commit = atoms.Button{Kind: atoms.Primary, Text: "Commit", Glyph: atoms.GlyphCommit}
	p.push = atoms.Button{Kind: atoms.Secondary, Text: "Commit and Push", Glyph: atoms.GlyphUpload}
	p.refresh = atoms.IconButton{Glyph: atoms.GlyphRefresh, Description: "Refresh"}
	p.history = molecules.Dropdown{Glyph: atoms.GlyphHistory, Description: "Recent messages", Up: true, Width: 360}
	p.split.Ratio = 0.42
	p.diff.Placeholder = "Select a file to preview its changes"
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
	selPath := ""
	if p.selected >= 0 && p.selected < len(p.files) {
		selPath = p.files[p.selected].change.Status.Path
	}
	p.files = p.files[:0]
	p.groups = [groupCount][]int{}
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
		p.groups[g] = append(p.groups[g], len(p.files))
		p.files = append(p.files, f)
	}
	// Preview the first tracked change when nothing was selected before.
	if p.selected < 0 && len(p.groups[groupChanges]) > 0 {
		p.selected = p.groups[groupChanges][0]
	}
	if p.selected >= 0 {
		p.diff.SetDiff(p.files[p.selected].change.Diff)
	} else {
		p.diff.SetDiff(nil)
	}
	p.dirty = true
}

// SetDiff supplies the diff of path (answer to DiffRequested). The file is
// selected as a whole afterwards if it was selected before.
func (p *CommitPanel) SetDiff(filePath string, d *gitcli.FileDiff) {
	for i, f := range p.files {
		if f.change.Status.Path != filePath {
			continue
		}
		f.setDiff(d, f.state() != atoms.Unchecked)
		if i == p.selected {
			p.diff.SetDiff(d)
		}
		p.dirty = true
		return
	}
}

// SetHistory sets the recent commit messages offered in the history
// dropdown, newest first.
func (p *CommitPanel) SetHistory(msgs []string) {
	p.historyIt = append(p.historyIt[:0], msgs...)
}

// SetAmendMessage supplies the last commit's message (answer to
// AmendMessageRequested). It fills the message field if it is empty.
func (p *CommitPanel) SetAmendMessage(msg string) {
	p.amendMsg = msg
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

// Selection returns the current commit selection in list order.
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

// update handles the controls outside the virtualized list.
func (p *CommitPanel) update(gtx layout.Context) {
	if p.refresh.Clicked(gtx) {
		p.emit(gtx, RefreshRequested{})
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
	can := p.CanCommit()
	p.commit.Disabled, p.push.Disabled = !can, !can
	if p.commit.Clicked(gtx) || (shortcut && can) {
		p.requestCommit(gtx, false)
	}
	if p.push.Clicked(gtx) {
		p.requestCommit(gtx, true)
	}
}

func (p *CommitPanel) requestCommit(gtx layout.Context, push bool) {
	if !p.CanCommit() {
		return
	}
	p.problem = nil
	p.emit(gtx, CommitRequested{
		Message: strings.TrimRight(p.message.Text(), " \t\r\n"),
		Amend:   p.amend.Checked(),
		Push:    push,
		Files:   p.Selection(),
	})
}

// buildRows flattens groups, files, hunks and lines into list rows.
func (p *CommitPanel) buildRows() {
	p.rows = p.rows[:0]
	for g := range p.groups {
		if len(p.groups[g]) == 0 {
			continue
		}
		p.rows = append(p.rows, panelRow{kind: rowGroup, group: g})
		if !p.groupExp[g] {
			continue
		}
		for _, fi := range p.groups[g] {
			f := p.files[fi]
			p.rows = append(p.rows, panelRow{kind: rowFile, group: g, file: fi})
			if !f.expanded || f.change.Diff == nil {
				continue
			}
			for h := range f.lines {
				p.rows = append(p.rows, panelRow{kind: rowHunk, group: g, file: fi, hunk: h})
				if !f.hunkExpanded[h] {
					continue
				}
				if f.lineCB[h] == nil {
					f.lineCB[h] = make([]atoms.Checkbox, len(f.lines[h]))
				}
				for l := range f.lines[h] {
					p.rows = append(p.rows, panelRow{kind: rowLine, group: g, file: fi, hunk: h, line: l})
				}
			}
		}
	}
	p.dirty = false
}

func (p *CommitPanel) selectFile(gtx layout.Context, i int) {
	p.selected = i
	f := p.files[i]
	p.diff.SetDiff(f.change.Diff)
	if f.change.Diff == nil && !f.requested {
		f.requested = true
		st := f.change.Status
		p.emit(gtx, DiffRequested{Path: st.Path, Untracked: st.Kind == gitcli.KindUntracked})
	}
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
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutToolbar(gtx, th) }),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return p.split.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
					return p.layoutLeft(gtx, th)
				}, func(gtx layout.Context) layout.Dimensions {
					return p.diff.Layout(gtx, th)
				})
			}),
		)
	})
}

func (p *CommitPanel) layoutToolbar(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	total, chosen := 0, 0
	for _, f := range p.files {
		total++
		if f.state() != atoms.Unchecked {
			chosen++
		}
	}
	return atoms.Surface{Kind: atoms.SurfaceAlt}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Left: th.Space.M, Right: th.Space.XS, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(atoms.Strong("Commit").Fn(th)),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return atoms.Small(fmt.Sprintf("%d of %d files selected", chosen, total)).SingleLine().Layout(gtx, th)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.refresh.Layout(gtx, th) }),
			)
		})
	})
}

func (p *CommitPanel) layoutLeft(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(p.files) == 0 {
				gtx.Constraints.Min = gtx.Constraints.Max
				return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted("No changes").Fn(th))
			}
			return p.list.Layout(gtx, th, len(p.rows), func(gtx layout.Context, i int) layout.Dimensions {
				return p.layoutRow(gtx, th, p.rows[i])
			})
		}),
		layout.Rigid(atoms.Divider{}.Fn(th)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.layoutMessage(gtx, th)
			})
		}),
	)
}

func (p *CommitPanel) layoutMessage(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	gap := layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout)
	can := p.CanCommit()
	p.commit.Disabled, p.push.Disabled = !can, !can
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.amend.Layout(gtx, th) }),
				layout.Flexed(1, layout.Spacer{}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					p.history.Items = p.historyIt
					return p.history.Layout(gtx, th)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: th.Space.XS}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Dp(messageMinH)
			gtx.Constraints.Max.Y = max(gtx.Constraints.Min.Y, gtx.Constraints.Max.Y)
			return p.message.Layout(gtx, th)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if p.problem == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: th.Space.S}.Layout(gtx, p.problem.banner().Fn(th))
		}),
		gap,
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
			)
		}),
	)
}

// syncBox sets cb to state, processes a click and returns the new state
// and whether it changed.
func syncBox(gtx layout.Context, cb *atoms.Checkbox, state atoms.CheckState, disabled bool) (atoms.CheckState, bool) {
	cb.State, cb.Disabled = state, disabled
	if cb.Update(gtx) {
		return cb.State, true
	}
	return state, false
}

func arrowGlyph(open bool) *atoms.Glyph {
	if open {
		return atoms.GlyphChevronDown
	}
	return atoms.GlyphChevronRight
}

func (p *CommitPanel) layoutRow(gtx layout.Context, th *tokens.Theme, r panelRow) layout.Dimensions {
	small := th.Size.ControlH - th.Space.S
	switch r.kind {
	case rowGroup:
		files := make([]*fileSel, 0, len(p.groups[r.group]))
		for _, i := range p.groups[r.group] {
			files = append(files, p.files[i])
		}
		cb := &p.groupCB[r.group]
		st, inner := syncBox(gtx, cb, groupState(files), false)
		if inner {
			for _, f := range files {
				f.setAll(st == atoms.Checked)
			}
			gtx.Execute(op.InvalidateCmd{})
		}
		cb.State = groupState(files)
		arr := &p.groupArr[r.group]
		arr.Size, arr.IconSize, arr.Description = small, th.Size.IconSmall, "Expand or collapse"
		toggle := arr.Clicked(gtx)
		arr.Glyph = arrowGlyph(p.groupExp[r.group])
		row := &p.groupRow[r.group]
		// Clicks on inner controls bubble up to the row; ignore those.
		if c, _ := row.Update(gtx); (c && !inner) || toggle {
			p.groupExp[r.group] = !p.groupExp[r.group]
			p.dirty = true
			gtx.Execute(op.InvalidateCmd{})
		}
		return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return arr.Layout(gtx, th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return cb.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(atoms.Strong(groupNames[r.group]).Fn(th)),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(atoms.Badge{Text: fmt.Sprint(len(files))}.Fn(th)),
			)
		})

	case rowFile:
		f := p.files[r.file]
		st, inner := syncBox(gtx, &f.cb, f.state(), f.disabled)
		if inner {
			f.setAll(st == atoms.Checked)
			gtx.Execute(op.InvalidateCmd{})
		}
		f.cb.State = f.state()
		expandable := f.change.Diff != nil && len(f.lines) > 0
		f.arrow.Size, f.arrow.IconSize, f.arrow.Description = small, th.Size.IconSmall, "Show hunks"
		if f.arrow.Clicked(gtx) && expandable {
			f.expanded = !f.expanded
			p.dirty = true
			inner = true
			gtx.Execute(op.InvalidateCmd{})
		}
		f.arrow.Glyph = arrowGlyph(f.expanded)
		if c, dbl := f.row.Update(gtx); c && !inner {
			p.selectFile(gtx, r.file)
			if dbl {
				p.emit(gtx, FileOpenRequested{Path: f.change.Status.Path})
			}
		}
		f.row.Selected = r.file == p.selected
		f.row.Indent = th.Space.L
		return f.row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return p.layoutFileContent(gtx, th, f, expandable)
		})

	case rowHunk:
		f := p.files[r.file]
		h := r.hunk
		cb := &f.hunkCB[h]
		st, inner := syncBox(gtx, cb, f.hunkState(h), f.disabled)
		if inner {
			f.setHunk(h, st == atoms.Checked)
			gtx.Execute(op.InvalidateCmd{})
		}
		cb.State = f.hunkState(h)
		arr := &f.hunkArrow[h]
		arr.Size, arr.IconSize, arr.Description = small, th.Size.IconSmall, "Show lines"
		if arr.Clicked(gtx) {
			f.hunkExpanded[h] = !f.hunkExpanded[h]
			p.dirty = true
			inner = true
			gtx.Execute(op.InvalidateCmd{})
		}
		arr.Glyph = arrowGlyph(f.hunkExpanded[h])
		row := &f.hunkRow[h]
		if c, _ := row.Update(gtx); c && !inner {
			if p.selected != r.file {
				p.selectFile(gtx, r.file)
			}
			p.diff.ScrollToHunk(h)
		}
		row.Indent = th.Space.L + th.Size.IndentStep
		hk := &f.change.Diff.Hunks[h]
		return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return arr.Layout(gtx, th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return cb.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return atoms.Label{Text: hk.Header(), Role: atoms.RoleCode, Color: th.Color.TextMuted, MaxLines: 1}.Layout(gtx, th)
				}),
			)
		})

	default: // rowLine
		f := p.files[r.file]
		h, l := r.hunk, r.line
		dl := f.change.Diff.Hunks[h].Lines[l]
		return layout.Inset{Left: th.Space.L + 2*th.Size.IndentStep + th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if dl.Kind == gitcli.LineContext {
						return layout.Spacer{Width: th.Size.Checkbox}.Layout(gtx)
					}
					cb := &f.lineCB[h][l]
					state := atoms.Unchecked
					if f.lines[h][l] {
						state = atoms.Checked
					}
					if st, ch := syncBox(gtx, cb, state, f.disabled); ch {
						f.setLine(h, l, st == atoms.Checked)
						gtx.Execute(op.InvalidateCmd{})
					}
					return cb.Layout(gtx, th)
				}),
				layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					tone := molecules.ToneContext
					switch dl.Kind {
					case gitcli.LineAdded:
						tone = molecules.ToneAdded
					case gitcli.LineDeleted:
						tone = molecules.ToneRemoved
					}
					return molecules.CodeLine{Marker: string(rune(dl.Kind)), Text: expandTabs(dl.Text), Tone: tone}.Layout(gtx, th)
				}),
			)
		})
	}
}

func (p *CommitPanel) layoutFileContent(gtx layout.Context, th *tokens.Theme, f *fileSel, expandable bool) layout.Dimensions {
	st := f.change.Status
	name, dir := path.Base(st.Path), path.Dir(st.Path)
	if dir == "." {
		dir = ""
	}
	if st.OrigPath != "" {
		dir = strings.TrimSpace(dir + "  ← " + st.OrigPath)
	}
	added, deleted := 0, 0
	if f.change.Diff != nil {
		added, deleted = f.change.Diff.Stats()
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !expandable {
				return layout.Spacer{Width: th.Size.ControlH - th.Space.S}.Layout(gtx)
			}
			return f.arrow.Layout(gtx, th)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return f.cb.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S, Right: th.Space.XS + th.Space.XXS}.Layout(gtx,
				atoms.Icon{Glyph: atoms.GlyphFile, Color: th.Color.TextMuted}.Fn(th))
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(atoms.Label{Text: name, Color: statusOf(st).Color(th), MaxLines: 1}.Fn(th)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if dir == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(dir).SingleLine().Fn(th))
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if added+deleted == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(atoms.Label{Text: fmt.Sprintf("+%d", added), Role: atoms.RoleSmall, Color: th.Editor.DiffAddedFg}.Fn(th)),
					layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
					layout.Rigid(atoms.Label{Text: fmt.Sprintf("−%d", deleted), Role: atoms.RoleSmall, Color: th.Editor.DiffRemovedFg}.Fn(th)),
				)
			})
		}),
	)
}

// diffView exposes the embedded preview (tests).
func (p *CommitPanel) diffView() *DiffView { return p.diff }

// shortcutHint describes the commit shortcut for the platform.
func shortcutHint() string {
	if runtime.GOOS == "darwin" {
		return "Cmd+Enter to commit"
	}
	return "Ctrl+Enter to commit"
}
