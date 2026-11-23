package git

import (
	"fmt"
	"strconv"
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

// rowKind identifies the rows of the change tree.
type rowKind int

const (
	rowGroup rowKind = iota
	rowDir
	rowFile
	rowHunk
	rowLine
)

type panelRow struct {
	kind  rowKind
	group int
	depth int
	node  *gitcli.ChangeNode // group and directory rows
	key   string             // expansion key of group and directory rows
	file  int
	hunk  int
	line  int
}

// groupOf returns the group node of g.
func (p *CommitPanel) groupOf(g int) *gitcli.ChangeNode {
	if g == groupUnversioned {
		return &p.tree.Unversioned
	}
	return &p.tree.Changes
}

// nodeKey is the expansion key of a group or directory node.
func nodeKey(g int, n *gitcli.ChangeNode) string {
	if n.Kind == gitcli.NodeGroup {
		return "g" + strconv.Itoa(g)
	}
	id := n.Path
	if id == "" {
		id = n.Name
	}
	return strconv.Itoa(g) + ":" + id
}

func (p *CommitPanel) dirOf(k string) *dirState {
	d := p.dirs[k]
	if d == nil {
		d = &dirState{}
		p.dirs[k] = d
	}
	return d
}

// collapseAll collapses every directory (the groups stay open).
func (p *CommitPanel) collapseAll() {
	for g := range groupCount {
		var walk func(n *gitcli.ChangeNode)
		walk = func(n *gitcli.ChangeNode) {
			for _, c := range n.Children {
				if c.Kind == gitcli.NodeDir {
					p.closed[nodeKey(g, c)] = true
					walk(c)
				}
			}
		}
		walk(p.groupOf(g))
	}
	p.dirty = true
}

// buildRows flattens groups, directories, files, hunks and lines.
func (p *CommitPanel) buildRows() {
	p.rows = p.rows[:0]
	var walk func(g int, n *gitcli.ChangeNode, depth int)
	walk = func(g int, n *gitcli.ChangeNode, depth int) {
		if n.Kind == gitcli.NodeFile {
			fi, ok := p.nodeFile[n]
			if !ok {
				return
			}
			p.rows = append(p.rows, panelRow{kind: rowFile, group: g, depth: depth, node: n, file: fi})
			f := p.files[fi]
			if !f.expanded || f.change.Diff == nil {
				return
			}
			for h := range f.lines {
				p.rows = append(p.rows, panelRow{kind: rowHunk, group: g, depth: depth + 1, file: fi, hunk: h})
				if !f.hunkExpanded[h] {
					continue
				}
				if f.lineCB[h] == nil {
					f.lineCB[h] = make([]atoms.Checkbox, len(f.lines[h]))
				}
				for l := range f.lines[h] {
					p.rows = append(p.rows, panelRow{kind: rowLine, group: g, depth: depth + 2, file: fi, hunk: h, line: l})
				}
			}
			return
		}
		kind := rowDir
		if n.Kind == gitcli.NodeGroup {
			kind = rowGroup
		}
		k := nodeKey(g, n)
		p.rows = append(p.rows, panelRow{kind: kind, group: g, depth: depth, node: n, key: k})
		if p.closed[k] {
			return
		}
		for _, c := range n.Children {
			walk(g, c, depth+1)
		}
	}
	for g := range groupCount {
		if n := p.groupOf(g); n.Count > 0 {
			walk(g, n, 0)
		}
	}
	p.dirty = false
}

func (p *CommitPanel) selectFile(gtx layout.Context, i int) {
	p.selected = i
	f := p.files[i]
	if f.change.Diff == nil && !f.requested {
		f.requested = true
		st := f.change.Status
		p.emit(gtx, DiffRequested{Path: st.Path, Untracked: st.Kind == gitcli.KindUntracked})
	}
}

// fileRows returns the row indexes of files in tree order.
func (p *CommitPanel) fileRows() []int {
	var out []int
	for i, r := range p.rows {
		if r.kind == rowFile {
			out = append(out, i)
		}
	}
	return out
}

// treeKeys handles the keyboard while the change tree is focused.
func (p *CommitPanel) treeKeys(gtx layout.Context) {
	filters := []event.Filter{
		key.FocusFilter{Target: &p.focus},
		key.Filter{Focus: &p.focus, Name: key.NameUpArrow},
		key.Filter{Focus: &p.focus, Name: key.NameDownArrow},
		key.Filter{Focus: &p.focus, Name: key.NameReturn},
		key.Filter{Focus: &p.focus, Name: key.NameEnter},
		key.Filter{Focus: &p.focus, Name: key.NameSpace},
		key.Filter{Focus: &p.focus, Name: key.NameF4},
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
		p.key(gtx, e.Name)
	}
}

// key handles one key of the focused tree.
func (p *CommitPanel) key(gtx layout.Context, name key.Name) {
	if p.dirty {
		p.buildRows()
	}
	files := p.fileRows()
	cur := -1
	for i, ri := range files {
		if p.rows[ri].file == p.selected {
			cur = i
		}
	}
	move := func(i int) {
		if i < 0 || i >= len(files) {
			return
		}
		p.selectFile(gtx, p.rows[files[i]].file)
		p.list.ScrollTo(files[i] - 2)
		gtx.Execute(op.InvalidateCmd{})
	}
	switch name {
	case key.NameUpArrow:
		if cur < 0 {
			move(len(files) - 1)
		} else {
			move(cur - 1)
		}
	case key.NameDownArrow:
		move(cur + 1)
	case key.NameReturn, key.NameEnter:
		p.openDiff(gtx)
	case key.NameSpace:
		if p.selected >= 0 {
			f := p.files[p.selected]
			f.setAll(f.state() != atoms.Checked)
			gtx.Execute(op.InvalidateCmd{})
		}
	case key.NameF4:
		if s := p.SelectedPath(); s != "" {
			p.emit(gtx, FileOpenRequested{Path: s})
		}
	}
}

// layoutTree draws the change tree (or the empty state).
func (p *CommitPanel) layoutTree(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	size := gtx.Constraints.Max
	gtx.Constraints.Min = size
	if len(p.files) == 0 {
		return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted("No local changes").Fn(th))
	}
	area := clip.Rect{Max: size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &p.focus)
	area.Pop()
	return layout.Inset{Top: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		p.list.Horizontal = true
		return p.list.Layout(gtx, th, len(p.rows), func(gtx layout.Context, i int) layout.Dimensions {
			return p.layoutRow(gtx, th, p.rows[i])
		})
	})
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

// fill is a Flexed(1) child, or a Rigid one when gtx lays out a row at its
// natural width (horizontal scrolling, molecules.Wide): there the content
// is not truncated and the row grows instead.
func fill(gtx layout.Context, w layout.Widget) layout.FlexChild {
	if molecules.Wide(gtx) {
		return layout.Rigid(w)
	}
	return layout.Flexed(1, w)
}

func arrowGlyph(open bool) *atoms.Glyph {
	if open {
		return atoms.GlyphChevronDown
	}
	return atoms.GlyphChevronRight
}

func (p *CommitPanel) indent(th *tokens.Theme, depth int) layout.Inset {
	return layout.Inset{Left: th.Size.IndentStep * unit.Dp(depth)}
}

func filesText(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func (p *CommitPanel) layoutRow(gtx layout.Context, th *tokens.Theme, r panelRow) layout.Dimensions {
	small := th.Size.ControlH - th.Space.S
	focus := func() { gtx.Execute(key.FocusCmd{Tag: &p.focus}) }
	switch r.kind {
	case rowGroup, rowDir:
		n := r.node
		idx := p.under[n]
		files := make([]*fileSel, 0, len(idx))
		for _, i := range idx {
			files = append(files, p.files[i])
		}
		d := p.dirOf(r.key)
		st, inner := syncBox(gtx, &d.cb, groupState(files), false)
		if inner {
			for _, f := range files {
				f.setAll(st == atoms.Checked)
			}
			gtx.Execute(op.InvalidateCmd{})
		}
		d.cb.State = groupState(files)
		d.arr.Size, d.arr.IconSize, d.arr.Description = small, th.Size.IconSmall, "Expand or collapse"
		toggle := d.arr.Clicked(gtx)
		open := !p.closed[r.key]
		d.arr.Glyph = arrowGlyph(open)
		// Clicks on inner controls bubble up to the row; ignore those.
		if c, dbl := d.row.Update(gtx); (c && !inner && (dbl || r.kind == rowGroup)) || toggle {
			p.closed[r.key] = open
			p.dirty = true
			gtx.Execute(op.InvalidateCmd{})
		} else if c {
			focus()
		}
		return d.row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return p.indent(th, r.depth).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.layoutNode(gtx, th, r, d)
			})
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
		f.arrow.Size, f.arrow.IconSize, f.arrow.Description = small, th.Size.IconSmall, "Select hunks"
		if f.arrow.Clicked(gtx) && expandable {
			f.expanded = !f.expanded
			p.dirty = true
			inner = true
			gtx.Execute(op.InvalidateCmd{})
		}
		f.arrow.Glyph = arrowGlyph(f.expanded)
		if c, dbl := f.row.Update(gtx); c && !inner {
			focus()
			p.selectFile(gtx, r.file)
			if dbl {
				st := f.change.Status
				p.emit(gtx, DiffOpenRequested{Path: st.Path, Untracked: st.Kind == gitcli.KindUntracked})
			}
		}
		f.row.Selected = r.file == p.selected
		return f.row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return p.indent(th, r.depth).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.layoutFileContent(gtx, th, f, r.node, expandable)
			})
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
		arr.Size, arr.IconSize, arr.Description = small, th.Size.IconSmall, "Select lines"
		if arr.Clicked(gtx) {
			f.hunkExpanded[h] = !f.hunkExpanded[h]
			p.dirty = true
			inner = true
			gtx.Execute(op.InvalidateCmd{})
		}
		arr.Glyph = arrowGlyph(f.hunkExpanded[h])
		row := &f.hunkRow[h]
		if c, dbl := row.Update(gtx); c && !inner {
			focus()
			p.selectFile(gtx, r.file)
			if dbl {
				st := f.change.Status
				p.emit(gtx, DiffOpenRequested{Path: st.Path, Untracked: st.Kind == gitcli.KindUntracked})
			}
		}
		hk := &f.change.Diff.Hunks[h]
		return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return p.indent(th, r.depth).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return arr.Layout(gtx, th) }),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return cb.Layout(gtx, th) }),
					layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
					fill(gtx, func(gtx layout.Context) layout.Dimensions {
						return atoms.Label{Text: hk.Header(), Role: atoms.RoleCode, Color: th.Color.TextMuted, MaxLines: 1}.Layout(gtx, th)
					}),
				)
			})
		})

	default: // rowLine
		f := p.files[r.file]
		h, l := r.hunk, r.line
		dl := f.change.Diff.Hunks[h].Lines[l]
		in := layout.Inset{Left: th.Space.S + th.Size.IndentStep*unit.Dp(r.depth) + small}
		if molecules.Wide(gtx) {
			// Diff lines keep the visible width (no horizontal growth).
			gtx.Constraints.Max.X = gtx.Constraints.Min.X
		}
		return in.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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

// layoutNode draws a group or directory row: arrow, checkbox, icon, name,
// file count and (groups) the badge.
func (p *CommitPanel) layoutNode(gtx layout.Context, th *tokens.Theme, r panelRow, d *dirState) layout.Dimensions {
	n := r.node
	open := !p.closed[r.key]
	name := atoms.Label{Text: n.Name, MaxLines: 1}
	icon := atoms.GlyphFolder
	if open {
		icon = atoms.GlyphFolderOpen
	}
	if r.kind == rowGroup {
		name = atoms.Strong(n.Name)
		icon = nil
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.arr.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return d.cb.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if icon == nil {
				return layout.Spacer{Width: th.Space.S}.Layout(gtx)
			}
			return layout.Inset{Left: th.Space.S, Right: th.Space.XS + th.Space.XXS}.Layout(gtx,
				atoms.Icon{Glyph: icon, Color: th.Color.TextMuted}.Fn(th))
		}),
		layout.Rigid(name.Fn(th)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(filesText(n.Count)).SingleLine().Fn(th))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if n.Badge == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Badge{Text: n.Badge, Kind: atoms.BadgeInfo}.Fn(th))
		}),
	)
}

// fileDetail is the muted path after a file name: the directory in flat
// and module grouping (not repeated under directory nodes) and the source
// of a rename.
func (p *CommitPanel) fileDetail(n *gitcli.ChangeNode) string {
	dir := ""
	if n != nil {
		switch p.grouping {
		case gitcli.GroupFlat:
			dir = n.Dir
		case gitcli.GroupModule:
			if _, rest, ok := strings.Cut(n.Dir, "/"); ok {
				dir = rest
			}
		}
		if n.OrigPath != "" {
			dir = strings.TrimSpace(dir + "  ← " + n.OrigPath)
		}
	}
	return dir
}

func (p *CommitPanel) layoutFileContent(gtx layout.Context, th *tokens.Theme, f *fileSel, n *gitcli.ChangeNode, expandable bool) layout.Dimensions {
	st := f.change.Status
	name := st.Path
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	detail := p.fileDetail(n)
	glyph := (*atoms.Glyph)(nil)
	if p.FileGlyph != nil {
		glyph = p.FileGlyph(st.Path)
	}
	if glyph == nil {
		glyph = DefaultFileGlyph(st.Path)
	}
	added, deleted := 0, 0
	if f.change.Diff != nil {
		added, deleted = f.change.Diff.Stats()
	}
	status := statusOf(st)
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
				atoms.Icon{Glyph: glyph, Color: th.Color.TextMuted}.Fn(th))
		}),
		fill(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(atoms.Label{Text: name, Color: status.Color(th), MaxLines: 1}.Fn(th)),
				fill(gtx, func(gtx layout.Context) layout.Dimensions {
					if detail == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(detail).SingleLine().Fn(th))
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
