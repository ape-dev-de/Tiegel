package git

import (
	"fmt"

	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// DiffMode selects the rendering of a DiffView.
type DiffMode int

// Diff modes.
const (
	// Unified shows old and new lines interleaved with two number columns.
	Unified DiffMode = iota
	// SideBySide shows the old version left and the new version right.
	SideBySide
)

// String returns the mode name.
func (m DiffMode) String() string {
	if m == SideBySide {
		return "side-by-side"
	}
	return "unified"
}

// DiffView shows the diff of one file, unified or side by side, with line
// numbers, navigation between change blocks and optional word
// highlighting. Rows are virtualized, so complete files (see
// WholeFileDiff) and very large diffs are cheap to show.
//
// Data: SetDiff. The view reports no events; navigation and mode switches
// are internal. Use NewDiffView or the zero value.
type DiffView struct {
	// Mode is the current rendering; the toolbar toggles it.
	Mode DiffMode
	// WordDiff highlights the changed words of modified lines.
	WordDiff bool
	// HideToolbar removes the title/navigation bar (embedding).
	HideToolbar bool
	// Placeholder is shown when no diff is set.
	Placeholder string

	diff    *gitcli.FileDiff
	model   *diffModel
	current int // index of the current change block, -1 = none

	list    molecules.ScrollList
	prev    atoms.IconButton
	next    atoms.IconButton
	mode    atoms.Button
	words   atoms.Checkbox
	drawn   int // rows laid out in the last frame (tests)
	pending int // row to scroll to on the next layout, -1 = none
}

// NewDiffView returns a unified diff view with word highlighting.
func NewDiffView() *DiffView {
	v := &DiffView{WordDiff: true}
	v.init()
	return v
}

func (v *DiffView) init() {
	if v.model == nil {
		v.model = buildDiffModel(nil)
		v.current = -1
		v.pending = -1
	}
}

// SetDiff shows f (nil clears the view). The scroll position is reset
// unless f is the diff already shown.
func (v *DiffView) SetDiff(f *gitcli.FileDiff) {
	v.init()
	if f == v.diff && f != nil {
		return
	}
	v.diff = f
	v.model = buildDiffModel(f)
	v.current = -1
	v.list.ScrollTo(0)
	v.pending = -1
}

// Diff returns the diff shown.
func (v *DiffView) Diff() *gitcli.FileDiff { return v.diff }

// Changes returns the number of change blocks.
func (v *DiffView) Changes() int {
	v.init()
	return len(v.changes())
}

// Current returns the index of the current change block (-1 before the
// first navigation).
func (v *DiffView) Current() int {
	v.init()
	return v.current
}

func (v *DiffView) rows() []diffRow {
	if v.Mode == SideBySide {
		return v.model.split
	}
	return v.model.unified
}

func (v *DiffView) changes() []int {
	if v.Mode == SideBySide {
		return v.model.changesS
	}
	return v.model.changesU
}

// NextChange scrolls to the next change block and reports whether there
// was one.
func (v *DiffView) NextChange() bool {
	v.init()
	ch := v.changes()
	if v.current+1 >= len(ch) {
		return false
	}
	v.current++
	v.scrollToRow(ch[v.current])
	return true
}

// PrevChange scrolls to the previous change block.
func (v *DiffView) PrevChange() bool {
	v.init()
	if v.current <= 0 {
		return false
	}
	v.current--
	v.scrollToRow(v.changes()[v.current])
	return true
}

// ScrollToHunk scrolls to the header of hunk i.
func (v *DiffView) ScrollToHunk(i int) {
	v.init()
	hs := v.model.hunksU
	if v.Mode == SideBySide {
		hs = v.model.hunksS
	}
	if i >= 0 && i < len(hs) {
		v.scrollToRow(hs[i])
		// Make the next NextChange go to the first change of the hunk.
		ch := v.changes()
		v.current = -1
		for k, r := range ch {
			if r < hs[i] {
				v.current = k
			}
		}
	}
}

func (v *DiffView) scrollToRow(r int) {
	// Keep a little context above the target.
	v.pending = max(0, r-2)
}

// SetMode switches the rendering and keeps the current change block.
func (v *DiffView) SetMode(m DiffMode) {
	v.init()
	if m == v.Mode {
		return
	}
	v.Mode = m
	if v.current >= 0 && v.current < len(v.changes()) {
		v.scrollToRow(v.changes()[v.current])
	} else {
		v.pending = 0
	}
}

func (v *DiffView) update(gtx layout.Context) {
	if v.prev.Clicked(gtx) {
		v.PrevChange()
	}
	if v.next.Clicked(gtx) {
		v.NextChange()
	}
	if v.mode.Clicked(gtx) {
		if v.Mode == Unified {
			v.SetMode(SideBySide)
		} else {
			v.SetMode(Unified)
		}
	}
	if v.words.Update(gtx) {
		v.WordDiff = v.words.Checked()
	}
}

// Layout draws the view filling the maximum constraints.
func (v *DiffView) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.init()
	v.update(gtx)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if v.HideToolbar {
				return layout.Dimensions{}
			}
			return v.layoutToolbar(gtx, th)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return atoms.Surface{Kind: atoms.SurfaceEditor}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
				return v.layoutRows(gtx, th)
			})
		}),
	)
}

func (v *DiffView) layoutToolbar(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.prev.Glyph, v.prev.Description = atoms.GlyphChevronUp, "Previous change"
	v.next.Glyph, v.next.Description = atoms.GlyphChevronDown, "Next change"
	n := len(v.changes())
	v.prev.Disabled = v.current <= 0
	v.next.Disabled = v.current+1 >= n
	v.mode.Kind = atoms.Ghost
	if v.Mode == Unified {
		v.mode.Text = "Side by side"
	} else {
		v.mode.Text = "Unified"
	}
	v.words.Label = "Words"
	v.words.State = atoms.Unchecked
	if v.WordDiff {
		v.words.State = atoms.Checked
	}
	title := v.Placeholder
	if v.diff != nil {
		title = v.diff.Path()
		if v.diff.IsRename && v.diff.OldPath != "" {
			title = v.diff.OldPath + " → " + v.diff.NewPath
		}
	}
	pos := fmt.Sprintf("%d changes", n)
	if n == 1 {
		pos = "1 change"
	}
	if v.current >= 0 {
		pos = fmt.Sprintf("%d of %d", v.current+1, n)
	}
	return atoms.Surface{Kind: atoms.SurfaceAlt}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Left: th.Space.S, Right: th.Space.XS, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return atoms.Strong(title).SingleLine().Layout(gtx, th)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if v.diff == nil {
								return layout.Dimensions{}
							}
							return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return statBadges(gtx, th, v.model.added, v.model.deleted)
							})
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if v.diff == nil {
						return layout.Dimensions{}
					}
					return v.layoutControls(gtx, th, pos)
				}),
			)
		})
	})
}

// layoutControls draws position, navigation, word toggle and mode switch.
func (v *DiffView) layoutControls(gtx layout.Context, th *tokens.Theme, pos string) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S, Right: th.Space.XS}.Layout(gtx, atoms.Small(pos).Fn(th))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.prev.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.next.Layout(gtx, th) }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return v.words.Layout(gtx, th) })
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.XS}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return v.mode.Layout(gtx, th) })
		}),
	)
}

// statBadges draws "+a −d" as success/danger badges.
func statBadges(gtx layout.Context, th *tokens.Theme, added, deleted int) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(atoms.Badge{Text: fmt.Sprintf("+%d", added), Kind: atoms.BadgeSuccess}.Fn(th)),
		layout.Rigid(layout.Spacer{Width: th.Space.XS}.Layout),
		layout.Rigid(atoms.Badge{Text: fmt.Sprintf("−%d", deleted), Kind: atoms.BadgeDanger}.Fn(th)),
	)
}

func (v *DiffView) placeholderText() string {
	switch {
	case v.diff == nil:
		if v.Placeholder != "" {
			return v.Placeholder
		}
		return "No file selected"
	case v.diff.IsBinary:
		return "Binary file – no text preview"
	case v.diff.IsCombined:
		return "Unmerged file – resolve the conflict in the merge view"
	case v.diff.IsRename && len(v.diff.Hunks) == 0:
		return "Renamed without content changes"
	case v.diff.ModeChanged() && len(v.diff.Hunks) == 0:
		return fmt.Sprintf("File mode changed: %s → %s", v.diff.OldMode, v.diff.NewMode)
	}
	return "No content changes"
}

func (v *DiffView) layoutRows(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	rows := v.rows()
	v.drawn = 0
	if len(rows) == 0 {
		return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted(v.placeholderText()).Fn(th))
	}
	if v.pending >= 0 {
		v.list.ScrollTo(v.pending)
		v.pending = -1
	}
	digits := v.model.digits
	return v.list.Layout(gtx, th, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		v.drawn++
		r := &rows[i]
		if r.header {
			return molecules.CodeLine{Tone: molecules.ToneHeader, Text: r.text, Muted: true, Marker: " "}.Layout(gtx, th)
		}
		if v.WordDiff {
			v.ensureSpans(rows, i)
		}
		if v.Mode == Unified {
			l := molecules.CodeLine{No: r.left.no, No2: r.right.no, Columns: 2, Digits: digits,
				Marker: r.marker, Text: r.left.text, Tone: r.left.tone}
			if v.WordDiff {
				l.Spans = r.left.spans
			}
			return l.Layout(gtx, th)
		}
		return layoutSplitRow(gtx, th, r, digits, v.WordDiff)
	})
}

// layoutSplitRow draws the two halves of a side-by-side row.
func layoutSplitRow(gtx layout.Context, th *tokens.Theme, r *diffRow, digits int, words bool) layout.Dimensions {
	half := func(s side, marker string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			l := molecules.CodeLine{No: s.no, Columns: 1, Digits: digits, Text: s.text, Tone: s.tone}
			if s.present && s.tone != molecules.ToneContext {
				l.Marker = marker
			}
			if words {
				l.Spans = s.spans
			}
			return l.Layout(gtx, th)
		}
	}
	return layout.Flex{}.Layout(gtx,
		layout.Flexed(0.5, half(r.left, "-")),
		layout.Rigid(layout.Spacer{Width: unit.Dp(1)}.Layout),
		layout.Flexed(0.5, half(r.right, "+")),
	)
}

// ensureSpans computes the word highlighting of row i (and its partner)
// once, lazily for visible rows only.
func (v *DiffView) ensureSpans(rows []diffRow, i int) {
	r := &rows[i]
	if r.spansDone {
		return
	}
	r.spansDone = true
	switch {
	case r.pair == -2:
		r.left.spans, r.right.spans = wordSpans(r.left.text, r.right.text)
	case r.pair >= 0 && r.pair < len(rows):
		p := &rows[r.pair]
		p.spansDone = true
		if r.marker == "-" {
			r.left.spans, p.left.spans = wordSpans(r.left.text, p.left.text)
		} else {
			p.left.spans, r.left.spans = wordSpans(p.left.text, r.left.text)
		}
	}
}
