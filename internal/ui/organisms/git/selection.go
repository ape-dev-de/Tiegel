package git

import (
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
)

// Change is one entry of the commit panel: a status entry and, if
// available, its diff against HEAD (git diff HEAD for tracked files,
// DiffUntracked for new ones). Without a diff the file can only be
// committed as a whole; selecting it emits DiffRequested.
type Change struct {
	Status gitcli.FileStatus
	Diff   *gitcli.FileDiff
}

// Group indexes of the commit panel.
const (
	groupChanges = iota
	groupUnversioned
	groupCount
)

var groupNames = [groupCount]string{"Changes", "Unversioned Files"}

// fileSel is the selection state of one file. Only added and deleted lines
// are selectable; context lines are ignored.
type fileSel struct {
	change   Change
	group    int
	disabled bool // conflicts cannot be committed from the panel
	// whole is the state of files without selectable lines.
	whole bool
	// lines[h][l] is the inclusion of line l of hunk h.
	lines [][]bool
	// counts per hunk: selectable and selected lines.
	total, chosen []int
	requested     bool // DiffRequested was emitted

	// UI state.
	expanded     bool
	hunkExpanded []bool
	cb           atoms.Checkbox
	row          molecules.ListRow
	arrow        atoms.IconButton
	hunkCB       []atoms.Checkbox
	hunkRow      []molecules.ListRow
	hunkArrow    []atoms.IconButton
	lineCB       [][]atoms.Checkbox // allocated when a hunk is expanded
}

func newFileSel(c Change, group int, on bool) *fileSel {
	f := &fileSel{change: c, group: group, disabled: c.Status.IsConflict()}
	f.setDiff(c.Diff, on)
	return f
}

// setDiff installs a diff and selects all or nothing.
func (f *fileSel) setDiff(d *gitcli.FileDiff, on bool) {
	f.change.Diff = d
	f.whole = on && !f.disabled
	f.lines, f.total, f.chosen = nil, nil, nil
	f.hunkExpanded, f.hunkCB, f.hunkRow, f.hunkArrow, f.lineCB = nil, nil, nil, nil, nil
	if d == nil || d.IsBinary || d.IsCombined {
		return
	}
	n := len(d.Hunks)
	f.lines = make([][]bool, n)
	f.total = make([]int, n)
	f.chosen = make([]int, n)
	f.hunkExpanded = make([]bool, n)
	f.hunkCB = make([]atoms.Checkbox, n)
	f.hunkRow = make([]molecules.ListRow, n)
	f.hunkArrow = make([]atoms.IconButton, n)
	f.lineCB = make([][]atoms.Checkbox, n)
	for h := range d.Hunks {
		f.lines[h] = make([]bool, len(d.Hunks[h].Lines))
		for _, dl := range d.Hunks[h].Lines {
			if dl.Kind != gitcli.LineContext {
				f.total[h]++
			}
		}
	}
	f.setAll(on)
}

// selectable reports whether line l of hunk h can be toggled.
func (f *fileSel) selectable(h, l int) bool {
	return f.change.Diff.Hunks[h].Lines[l].Kind != gitcli.LineContext
}

// lineBased reports whether the file has selectable lines.
func (f *fileSel) lineBased() bool {
	for _, n := range f.total {
		if n > 0 {
			return true
		}
	}
	return false
}

func (f *fileSel) setAll(on bool) {
	if f.disabled {
		on = false
	}
	f.whole = on
	for h := range f.lines {
		f.setHunk(h, on)
	}
}

func (f *fileSel) setHunk(h int, on bool) {
	if f.disabled {
		return
	}
	for l := range f.lines[h] {
		if f.selectable(h, l) {
			f.lines[h][l] = on
		}
	}
	if on {
		f.chosen[h] = f.total[h]
	} else {
		f.chosen[h] = 0
	}
	f.syncWhole()
}

func (f *fileSel) setLine(h, l int, on bool) {
	if f.disabled || !f.selectable(h, l) || f.lines[h][l] == on {
		return
	}
	f.lines[h][l] = on
	if on {
		f.chosen[h]++
	} else {
		f.chosen[h]--
	}
	f.syncWhole()
}

// syncWhole keeps whole consistent for line based files (so files whose
// diff is replaced keep a sensible state).
func (f *fileSel) syncWhole() {
	if f.lineBased() {
		f.whole = f.state() == atoms.Checked
	}
}

func (f *fileSel) hunkState(h int) atoms.CheckState {
	switch {
	case f.total[h] == 0 || f.chosen[h] == 0:
		return atoms.Unchecked
	case f.chosen[h] == f.total[h]:
		return atoms.Checked
	}
	return atoms.Indeterminate
}

func (f *fileSel) state() atoms.CheckState {
	if !f.lineBased() {
		if f.whole {
			return atoms.Checked
		}
		return atoms.Unchecked
	}
	all, any := true, false
	for h := range f.total {
		if f.total[h] == 0 {
			continue
		}
		if f.chosen[h] > 0 {
			any = true
		}
		if f.chosen[h] < f.total[h] {
			all = false
		}
	}
	switch {
	case all:
		return atoms.Checked
	case any:
		return atoms.Indeterminate
	}
	return atoms.Unchecked
}

// selection returns the commit selection of the file; ok is false when
// nothing of it is selected.
func (f *fileSel) selection() (FileSelection, bool) {
	st := f.change.Status
	fs := FileSelection{Path: st.Path, OrigPath: st.OrigPath, Untracked: st.Kind == gitcli.KindUntracked}
	if fs.Path == "" && f.change.Diff != nil {
		fs.Path = f.change.Diff.Path()
	}
	switch f.state() {
	case atoms.Unchecked:
		return fs, false
	case atoms.Checked:
		fs.Whole = true
		return fs, true
	}
	for h := range f.lines {
		switch f.hunkState(h) {
		case atoms.Checked:
			fs.Hunks = append(fs.Hunks, gitcli.HunkSelection{Hunk: h})
		case atoms.Indeterminate:
			var ls []int
			for l, on := range f.lines[h] {
				if on {
					ls = append(ls, l)
				}
			}
			fs.Hunks = append(fs.Hunks, gitcli.HunkSelection{Hunk: h, Lines: ls})
		}
	}
	return fs, true
}

// groupState aggregates the states of the enabled files of a group.
func groupState(files []*fileSel) atoms.CheckState {
	all, any, n := true, false, 0
	for _, f := range files {
		if f.disabled {
			continue
		}
		n++
		switch f.state() {
		case atoms.Checked:
			any = true
		case atoms.Indeterminate:
			any, all = true, false
		default:
			all = false
		}
	}
	switch {
	case n == 0 || !any:
		return atoms.Unchecked
	case all:
		return atoms.Checked
	}
	return atoms.Indeterminate
}

// statusOf maps a porcelain entry to the file name color status.
func statusOf(s gitcli.FileStatus) molecules.FileStatus {
	switch {
	case s.Kind == gitcli.KindUntracked:
		return molecules.StatusUntracked
	case s.Kind == gitcli.KindIgnored:
		return molecules.StatusIgnored
	case s.Kind == gitcli.KindUnmerged:
		return molecules.StatusConflict
	case s.Index == 'A' || s.Worktree == 'A':
		return molecules.StatusAdded
	case s.Index == 'D' || s.Worktree == 'D':
		return molecules.StatusDeleted
	}
	return molecules.StatusModified
}

// sameHunks reports whether two diffs have identical hunk structure (so a
// line selection can be carried over on refresh).
func sameHunks(a, b *gitcli.FileDiff) bool {
	if a == nil || b == nil || len(a.Hunks) != len(b.Hunks) {
		return false
	}
	for i := range a.Hunks {
		ha, hb := &a.Hunks[i], &b.Hunks[i]
		if ha.Header() != hb.Header() || len(ha.Lines) != len(hb.Lines) {
			return false
		}
		for l := range ha.Lines {
			if ha.Lines[l] != hb.Lines[l] {
				return false
			}
		}
	}
	return true
}
