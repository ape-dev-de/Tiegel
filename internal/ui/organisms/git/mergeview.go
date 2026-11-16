package git

import (
	"fmt"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// mergeBlock is a gitcli.MergeBlock plus its widgets.
type mergeBlock struct {
	gitcli.MergeBlock
	conflict    int // ordinal of the conflict (-1 for other blocks)
	resultStart int // 0-based first line in the merged result

	takeOurs, takeTheirs, both, edit, reset atoms.Button
	editing                                 bool
	editor                                  atoms.TextField
}

// ours returns the lines shown in the ours column. Unchanged blocks only
// carry the base lines, which equal both sides.
func (b *mergeBlock) ours() []string {
	if b.Kind == gitcli.MergeUnchanged {
		return b.Base
	}
	return b.Ours
}

// theirs returns the lines shown in the theirs column.
func (b *mergeBlock) theirs() []string {
	if b.Kind == gitcli.MergeUnchanged {
		return b.Base
	}
	return b.Theirs
}

// mergeRow is one row of the three column list.
type mergeRow struct {
	block  int
	header bool // conflict action bar
	edit   bool // inline editor of the result
	i      int  // line within the block
}

// MergeView is the three-way merge: ours | result | theirs. Every conflict
// block has actions to take ours, theirs or both (ours first), to edit the
// result by hand or to reset it. A counter shows the unresolved conflicts;
// "Apply" is enabled when all are resolved, "Apply with markers" applies
// deliberately with conflict markers left in.
//
// Data in: SetMerge. Events out: MergeApplied (Content is the rendered
// result), MergeCancelled.
type MergeView struct {
	path   string
	labels gitcli.MarkerLabels
	blocks []*mergeBlock
	rows   []mergeRow
	heads  []int // row index of each conflict header
	digits int
	cur    int

	list    molecules.ScrollList
	prev    atoms.IconButton
	next    atoms.IconButton
	apply   atoms.Button
	markers atoms.Button
	cancel  atoms.Button
	q       queue
	pending int
	dirty   bool
}

// NewMergeView returns an empty merge view.
func NewMergeView() *MergeView {
	v := &MergeView{cur: -1, pending: -1}
	v.prev = atoms.IconButton{Glyph: atoms.GlyphChevronUp, Description: "Previous conflict"}
	v.next = atoms.IconButton{Glyph: atoms.GlyphChevronDown, Description: "Next conflict"}
	v.apply = atoms.Button{Kind: atoms.Primary, Text: "Apply", Glyph: atoms.GlyphDone}
	v.markers = atoms.Button{Kind: atoms.Ghost, Text: "Apply with markers"}
	v.cancel = atoms.Button{Kind: atoms.Ghost, Text: "Cancel"}
	return v
}

// SetMerge shows the merge of path. The view works on a copy of the
// blocks; res is not modified. labels name the sides (e.g. branch names)
// in the column headers and conflict markers.
func (v *MergeView) SetMerge(path string, res *gitcli.MergeResult, labels gitcli.MarkerLabels) {
	v.path, v.labels = path, labels
	v.blocks = v.blocks[:0]
	v.cur, v.pending = -1, 0
	if res == nil {
		v.rebuild()
		return
	}
	n := 0
	for _, b := range res.Blocks {
		mb := &mergeBlock{MergeBlock: b, conflict: -1}
		mb.Custom = append([]byte(nil), b.Custom...)
		if b.Kind == gitcli.MergeConflict {
			mb.conflict = n
			n++
		}
		mb.takeOurs = atoms.Button{Kind: atoms.Ghost, Text: "Take ours", Glyph: atoms.GlyphArrowRight}
		mb.takeTheirs = atoms.Button{Kind: atoms.Ghost, Text: "Take theirs", Glyph: atoms.GlyphArrowLeft}
		mb.both = atoms.Button{Kind: atoms.Ghost, Text: "Both"}
		mb.edit = atoms.Button{Kind: atoms.Ghost, Text: "Edit", Glyph: atoms.GlyphEdit}
		mb.reset = atoms.Button{Kind: atoms.Ghost, Text: "Reset"}
		mb.editor.Editor.SingleLine = false
		mb.editor.Mono = true
		v.blocks = append(v.blocks, mb)
	}
	v.rebuild()
}

// Result returns the merge with the current resolutions.
func (v *MergeView) Result() *gitcli.MergeResult {
	r := &gitcli.MergeResult{Blocks: make([]gitcli.MergeBlock, len(v.blocks))}
	for i, b := range v.blocks {
		r.Blocks[i] = b.MergeBlock
	}
	return r
}

// Conflicts returns the number of conflict blocks.
func (v *MergeView) Conflicts() int { return len(v.heads) }

// Unresolved returns the number of unresolved conflicts.
func (v *MergeView) Unresolved() int {
	n := 0
	for _, b := range v.blocks {
		if _, ok := b.Merged(); !ok {
			n++
		}
	}
	return n
}

// Resolve sets the resolution of conflict i (0-based among conflicts).
// TakeCustom uses custom as the block's text.
func (v *MergeView) Resolve(i int, r gitcli.Resolution, custom []byte) bool {
	for _, b := range v.blocks {
		if b.conflict == i {
			b.Resolution = r
			b.Custom = append([]byte(nil), custom...)
			b.editing = false
			v.rebuild()
			return true
		}
	}
	return false
}

// rebuild recomputes result line numbers and rows.
func (v *MergeView) rebuild() {
	v.dirty = false
	v.rows = v.rows[:0]
	v.heads = v.heads[:0]
	res, maxNo := 0, 0
	for bi, b := range v.blocks {
		b.resultStart = res
		merged, _ := b.Merged()
		res += len(merged)
		maxNo = max(maxNo, b.OursStart+len(b.ours()), b.TheirsStart+len(b.theirs()), res)
		if b.Kind == gitcli.MergeConflict {
			v.heads = append(v.heads, len(v.rows))
			v.rows = append(v.rows, mergeRow{block: bi, header: true})
			if b.editing {
				v.rows = append(v.rows, mergeRow{block: bi, edit: true})
				continue
			}
		}
		n := max(len(b.ours()), len(b.theirs()), len(merged))
		if b.Kind == gitcli.MergeConflict && n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			v.rows = append(v.rows, mergeRow{block: bi, i: i})
		}
	}
	v.digits = max(3, len(strconv.Itoa(maxNo)))
}

// Update returns the events since the last call.
func (v *MergeView) Update(gtx layout.Context) []Event {
	v.update(gtx)
	return v.q.drain()
}

func (v *MergeView) emit(gtx layout.Context, e Event) {
	v.q.emit(e)
	gtx.Execute(op.InvalidateCmd{})
}

func (v *MergeView) update(gtx layout.Context) {
	if v.prev.Clicked(gtx) && v.cur > 0 {
		v.cur--
		v.pending = max(0, v.heads[v.cur]-1)
	}
	if v.next.Clicked(gtx) && v.cur+1 < len(v.heads) {
		v.cur++
		v.pending = max(0, v.heads[v.cur]-1)
	}
	un := v.Unresolved()
	v.apply.Disabled = un > 0 || len(v.blocks) == 0
	v.markers.Disabled = un == 0
	if v.apply.Clicked(gtx) {
		v.emit(gtx, MergeApplied{Path: v.path, Content: v.Result().Render(v.labels)})
	}
	if v.markers.Clicked(gtx) {
		v.emit(gtx, MergeApplied{Path: v.path, Content: v.Result().Render(v.labels), Unresolved: un})
	}
	if v.cancel.Clicked(gtx) {
		v.emit(gtx, MergeCancelled{Path: v.path})
	}
}

// Layout draws the view filling the maximum constraints.
func (v *MergeView) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if v.dirty {
		v.rebuild()
	}
	v.update(gtx)
	gtx.Constraints.Min = gtx.Constraints.Max
	return atoms.Surface{Kind: atoms.SurfacePanel}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.layoutToolbar(gtx, th) }),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.layoutColumnTitles(gtx, th) }),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return atoms.Surface{Kind: atoms.SurfaceEditor}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
					if len(v.rows) == 0 {
						return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted("Nothing to merge").Fn(th))
					}
					if v.pending >= 0 {
						v.list.ScrollTo(v.pending)
						v.pending = -1
					}
					return v.list.Layout(gtx, th, len(v.rows), func(gtx layout.Context, i int) layout.Dimensions {
						return v.layoutRow(gtx, th, v.rows[i])
					})
				})
			}),
		)
	})
}

func (v *MergeView) layoutToolbar(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	un, total := v.Unresolved(), len(v.heads)
	badge := atoms.Badge{Text: "No conflicts", Kind: atoms.BadgeSuccess}
	switch {
	case un > 0:
		badge = atoms.Badge{Text: fmt.Sprintf("%d of %d conflicts unresolved", un, total), Kind: atoms.BadgeDanger}
	case total > 0:
		badge = atoms.Badge{Text: "All conflicts resolved", Kind: atoms.BadgeSuccess}
	}
	v.prev.Disabled = v.cur <= 0
	v.next.Disabled = v.cur+1 >= len(v.heads)
	return atoms.Surface{Kind: atoms.SurfaceAlt}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Left: th.Space.M, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return atoms.Strong(v.path).SingleLine().Layout(gtx, th)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx, badge.Fn(th))
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.prev.Layout(gtx, th) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.next.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.cancel.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.markers.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.apply.Layout(gtx, th) }),
			)
		})
	})
}

func label(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (v *MergeView) layoutColumnTitles(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	title := func(s string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx, atoms.Small(s).SingleLine().Fn(th))
		}
	}
	return threeColumns(gtx,
		title("Ours · "+label(v.labels.Ours, "ours")),
		title("Result"),
		title("Theirs · "+label(v.labels.Theirs, "theirs")))
}

// threeColumns lays out three equally wide cells with 1dp gaps.
func threeColumns(gtx layout.Context, a, b, c layout.Widget) layout.Dimensions {
	gap := layout.Rigid(layout.Spacer{Width: unit.Dp(1)}.Layout)
	cell := func(w layout.Widget) layout.FlexChild {
		return layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			d := w(gtx)
			d.Size.X = gtx.Constraints.Max.X
			return d
		})
	}
	return layout.Flex{}.Layout(gtx, cell(a), gap, cell(b), gap, cell(c))
}

func trimEOL(s string) string { return strings.TrimRight(s, "\r\n") }

// sideTones returns the tones of the ours, result and theirs cells.
func sideTones(b *mergeBlock) (ours, result, theirs molecules.LineTone) {
	switch b.Kind {
	case gitcli.MergeOursOnly:
		return molecules.ToneChanged, molecules.ToneChanged, molecules.ToneContext
	case gitcli.MergeTheirsOnly:
		return molecules.ToneContext, molecules.ToneChanged, molecules.ToneChanged
	case gitcli.MergeBothSame:
		return molecules.ToneChanged, molecules.ToneChanged, molecules.ToneChanged
	case gitcli.MergeConflict:
		switch b.Resolution {
		case gitcli.Unresolved:
			return molecules.ToneConflict, molecules.ToneConflict, molecules.ToneConflict
		case gitcli.TakeOurs:
			return molecules.ToneAdded, molecules.ToneAdded, molecules.ToneRemoved
		case gitcli.TakeTheirs:
			return molecules.ToneRemoved, molecules.ToneAdded, molecules.ToneAdded
		case gitcli.TakeOursFirst, gitcli.TakeTheirsFirst:
			return molecules.ToneAdded, molecules.ToneAdded, molecules.ToneAdded
		case gitcli.TakeBase:
			return molecules.ToneRemoved, molecules.ToneAdded, molecules.ToneRemoved
		}
		return molecules.ToneChanged, molecules.ToneAdded, molecules.ToneChanged
	}
	return molecules.ToneContext, molecules.ToneContext, molecules.ToneContext
}

func resolutionText(r gitcli.Resolution) string {
	switch r {
	case gitcli.TakeOurs:
		return "ours"
	case gitcli.TakeTheirs:
		return "theirs"
	case gitcli.TakeOursFirst:
		return "both (ours first)"
	case gitcli.TakeTheirsFirst:
		return "both (theirs first)"
	case gitcli.TakeBase:
		return "base"
	case gitcli.TakeCustom:
		return "edited"
	}
	return ""
}

func (v *MergeView) layoutRow(gtx layout.Context, th *tokens.Theme, r mergeRow) layout.Dimensions {
	b := v.blocks[r.block]
	if r.header {
		return v.layoutHeader(gtx, th, b)
	}
	to, tr, tt := sideTones(b)
	if r.edit {
		return v.layoutEdit(gtx, th, b, to, tt)
	}
	merged, ok := b.Merged()
	cell := func(lines []string, start, i int, tone molecules.LineTone) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			if i >= len(lines) {
				t := molecules.ToneFiller
				if tone == molecules.ToneConflict {
					t = tone
				}
				return molecules.CodeLine{Columns: 1, Digits: v.digits, Tone: t}.Layout(gtx, th)
			}
			return molecules.CodeLine{No: start + i + 1, Columns: 1, Digits: v.digits, Text: expandTabs(trimEOL(lines[i])), Tone: tone}.Layout(gtx, th)
		}
	}
	result := cell(merged, b.resultStart, r.i, tr)
	if !ok {
		result = func(gtx layout.Context) layout.Dimensions {
			txt := ""
			if r.i == 0 {
				txt = "unresolved – choose a side or edit"
			}
			return molecules.CodeLine{Columns: 1, Digits: v.digits, Text: txt, Muted: true, Tone: molecules.ToneConflict}.Layout(gtx, th)
		}
	}
	return threeColumns(gtx,
		cell(b.ours(), b.OursStart, r.i, to),
		result,
		cell(b.theirs(), b.TheirsStart, r.i, tt))
}

func (v *MergeView) layoutHeader(gtx layout.Context, th *tokens.Theme, b *mergeBlock) layout.Dimensions {
	changed := false
	if b.takeOurs.Clicked(gtx) {
		b.Resolution, b.editing, changed = gitcli.TakeOurs, false, true
	}
	if b.takeTheirs.Clicked(gtx) {
		b.Resolution, b.editing, changed = gitcli.TakeTheirs, false, true
	}
	if b.both.Clicked(gtx) {
		b.Resolution, b.editing, changed = gitcli.TakeOursFirst, false, true
	}
	if b.reset.Clicked(gtx) {
		b.Resolution, b.Custom, b.editing, changed = gitcli.Unresolved, nil, false, true
	}
	if b.edit.Clicked(gtx) {
		if b.editing {
			b.Custom = v.editedText(b)
			b.Resolution = gitcli.TakeCustom
			b.editing = false
		} else {
			text := ""
			if lines, ok := b.Merged(); ok {
				text = strings.Join(lines, "")
			} else {
				text = strings.Join(b.Ours, "") + strings.Join(b.Theirs, "")
			}
			b.editor.SetText(strings.TrimSuffix(text, "\n"))
			b.editing = true
		}
		changed = true
	}
	if changed {
		// Rows are rebuilt before the next layout; the list is iterating
		// over them right now.
		v.dirty = true
		gtx.Execute(op.InvalidateCmd{})
	}
	b.reset.Disabled = b.Resolution == gitcli.Unresolved && !b.editing
	b.edit.Text = "Edit"
	if b.editing {
		b.edit.Text = "Done"
	}
	status := atoms.Badge{Text: fmt.Sprintf("Conflict %d", b.conflict+1), Kind: atoms.BadgeWarning}
	if b.Resolution != gitcli.Unresolved {
		status = atoms.Badge{Text: fmt.Sprintf("Conflict %d: %s", b.conflict+1, resolutionText(b.Resolution)), Kind: atoms.BadgeSuccess}
	}
	btn := func(bt *atoms.Button) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions { return bt.Layout(gtx, th) }
	}
	return atoms.Surface{Kind: atoms.SurfaceAlt}.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return threeColumns(gtx,
				func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, layout.Spacer{}.Layout),
						layout.Rigid(btn(&b.takeOurs)))
				},
				func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: th.Space.S, Right: th.Space.S}.Layout(gtx, status.Fn(th))
						}),
						layout.Flexed(1, layout.Spacer{}.Layout),
						layout.Rigid(btn(&b.both)),
						layout.Rigid(btn(&b.edit)),
						layout.Rigid(btn(&b.reset)))
				},
				func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(btn(&b.takeTheirs)))
				})
		})
	})
}

// editedText returns the editor content as block text with a line
// terminator, so it does not merge with the next block.
func (v *MergeView) editedText(b *mergeBlock) []byte {
	s := b.editor.Text()
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return []byte(s)
}

func (v *MergeView) layoutEdit(gtx layout.Context, th *tokens.Theme, b *mergeBlock, to, tt molecules.LineTone) layout.Dimensions {
	stack := func(lines []string, start int, tone molecules.LineTone) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			ch := make([]layout.FlexChild, 0, len(lines))
			for i := range lines {
				ch = append(ch, layout.Rigid(molecules.CodeLine{No: start + i + 1, Columns: 1, Digits: v.digits,
					Text: expandTabs(trimEOL(lines[i])), Tone: tone}.Fn(th)))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		}
	}
	return threeColumns(gtx,
		stack(b.Ours, b.OursStart, to),
		func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(th.Space.XS).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return b.editor.Layout(gtx, th)
			})
		},
		stack(b.Theirs, b.TheirsStart, tt))
}
