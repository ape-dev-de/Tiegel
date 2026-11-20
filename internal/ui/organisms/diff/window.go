package diff

import (
	"fmt"
	"image"
	"math"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Layout constants.
const (
	stripW        = unit.Dp(40)
	revertSize    = unit.Dp(18)
	recomputeWait = 150 * time.Millisecond
)

// Toolbar item IDs.
const (
	itemPrev     = "prev"
	itemNext     = "next"
	itemOpen     = "open"
	itemPrevFile = "prevFile"
	itemNextFile = "nextFile"
)

// HighlighterFunc creates the syntax highlighter of one side; path selects
// the language. Highlighters with a Close method are closed when the
// window replaces or drops them.
type HighlighterFunc func(b *buffer.Buffer, path string) highlight.Highlighter

// DiffWindow compares two versions of a file: two editor columns side by
// side (or one unified column) with tinted change blocks, changed words
// highlighted, curved bands connecting the blocks between the columns and
// synchronized scrolling. The toolbar navigates between differences
// (F7 / Shift+F7), switches side-by-side / unified, ignores white space
// and shows the number of differences.
//
// The right side is editable when SetContent gets the host's document
// buffer (target = work tree): edits go straight into that buffer (the
// host holds the document lock around Update/Layout, as for the main
// editor), are reported as RightEdited, and the window refreshes its
// blocks itself. Each block then has a "»" arrow in the strip that asks to
// revert it (RevertRequested; ApplyRevert performs it).
//
// With SetFileNav the window is part of a change set (the changes diff
// tab of the commit window): the toolbar gains previous/next file buttons
// and the position ("2 / 7"); they report FileNavRequested and the host
// replaces the content.
//
// Data in: SetContent, SetBlocks, SetFileNav. Events out: RevertRequested,
// IgnoreWhitespaceChanged, ViewModeChanged, RightEdited,
// OpenFileRequested, FileNavRequested.
type DiffWindow struct {
	// NewHighlighter creates the highlighters of both sides (nil: plain
	// text), e.g. tshighlight.New.
	NewHighlighter HighlighterFunc
	// TabWidth of both editors (default 4).
	TabWidth int

	content  Content
	blocks   []Block
	editable bool
	unified  bool
	ignoreWS bool
	cur      int

	left, right, uni       *editorview.Editor
	leftHL, rightHL, uniHL highlight.Highlighter
	model                  *unifiedModel

	sync      scrollSync
	dirty     bool
	dirtyAt   time.Time
	pendingTo int // block to reveal on the next Layout (-1 none)

	tools   molecules.Toolbar
	openBar molecules.Toolbar
	fileBar molecules.Toolbar
	nav     FileNav
	sbsBtn  atoms.Button
	uniBtn  atoms.Button
	ws      atoms.Checkbox
	reverts []atoms.IconButton
	tips    []atoms.Tooltip
	q       queue
}

// NewDiffWindow returns an empty diff window.
func NewDiffWindow() *DiffWindow {
	w := &DiffWindow{cur: -1, pendingTo: -1}
	w.tools.Items = []molecules.ToolbarItem{
		{ID: itemPrev, Glyph: atoms.GlyphChevronUp, Label: "Previous difference", Shortcut: "Shift+F7"},
		{ID: itemNext, Glyph: atoms.GlyphChevronDown, Label: "Next difference", Shortcut: "F7"},
	}
	w.openBar.Items = []molecules.ToolbarItem{{ID: itemOpen, Glyph: atoms.GlyphEdit, Label: "Open in editor"}}
	w.fileBar.Items = []molecules.ToolbarItem{
		{ID: itemPrevFile, Glyph: atoms.GlyphArrowLeft, Label: "Previous file"},
		{ID: itemNextFile, Glyph: atoms.GlyphArrowRight, Label: "Next file"},
	}
	w.sbsBtn = atoms.Button{Text: "Side-by-side", Kind: atoms.Secondary}
	w.uniBtn = atoms.Button{Text: "Unified", Kind: atoms.Ghost}
	w.ws = atoms.Checkbox{Label: "Ignore whitespace"}
	return w
}

// SetContent shows a new comparison. right, if non-nil, is the host's
// document buffer of the right side, which then becomes editable (its
// content replaces Content.Right.Text). Editors are kept when the texts
// did not change, so a refresh keeps the scroll position.
func (w *DiffWindow) SetContent(c Content, right *buffer.Buffer) {
	sameLeft := w.left != nil && c.Left.Text == w.content.Left.Text && c.Left.Path == w.content.Left.Path
	sameRight := w.right != nil && c.Right.Path == w.content.Right.Path &&
		((right != nil && w.right.Buffer() == right) || (right == nil && !w.editable && c.Right.Text == w.content.Right.Text))
	w.content = c
	w.editable = right != nil
	if !sameLeft {
		w.closeSide(&w.left, &w.leftHL)
		w.left, w.leftHL = w.newEditor(buffer.NewString(c.Left.Text), c.Left.Path, true)
		w.sync.reset()
	}
	if !sameRight {
		w.closeSide(&w.right, &w.rightHL)
		b := right
		if b == nil {
			b = buffer.NewString(c.Right.Text)
		}
		w.right, w.rightHL = w.newEditor(b, c.Right.Path, right == nil)
		w.sync.reset()
	}
	if !sameLeft || !sameRight {
		w.cur = -1
	}
	w.setBlocks(c.Blocks)
}

func (w *DiffWindow) newEditor(b *buffer.Buffer, path string, readOnly bool) (*editorview.Editor, highlight.Highlighter) {
	ed := editorview.New(b)
	ed.ReadOnly = readOnly
	if w.TabWidth > 0 {
		ed.TabWidth = w.TabWidth
	}
	var hl highlight.Highlighter
	if w.NewHighlighter != nil {
		hl = w.NewHighlighter(b, path)
		ed.Highlighter = hl
	}
	return ed, hl
}

func (w *DiffWindow) closeSide(ed **editorview.Editor, hl *highlight.Highlighter) {
	if c, ok := (*hl).(interface{ Close() }); ok && *hl != nil {
		c.Close()
	}
	*hl = nil
	if *ed != nil {
		(*ed).Close()
		*ed = nil
	}
}

// Close releases the editors and highlighters (the host's buffer stays
// untouched).
func (w *DiffWindow) Close() {
	w.closeSide(&w.left, &w.leftHL)
	w.closeSide(&w.right, &w.rightHL)
	w.closeSide(&w.uni, &w.uniHL)
	w.model = nil
}

// SetBlocks replaces the change blocks (e.g. a fresh git.fileDiff after
// IgnoreWhitespaceChanged) without touching the texts.
func (w *DiffWindow) SetBlocks(bs []Block) { w.setBlocks(bs) }

func (w *DiffWindow) setBlocks(bs []Block) {
	w.blocks = append([]Block(nil), bs...)
	w.content.Blocks = w.blocks
	if w.cur >= len(w.blocks) {
		w.cur = len(w.blocks) - 1
	}
	if len(w.reverts) != len(w.blocks) {
		w.reverts = make([]atoms.IconButton, len(w.blocks))
		w.tips = make([]atoms.Tooltip, len(w.blocks))
		for i := range w.reverts {
			w.reverts[i] = atoms.IconButton{Glyph: atoms.GlyphChevronRight, Description: "Revert block", Size: revertSize}
			w.tips[i].Text = "Revert: replace with the left side"
		}
	}
	if w.left != nil {
		w.left.SetLineDecorations(decorations(w.blocks, true))
		w.right.SetLineDecorations(decorations(w.blocks, false))
	}
	w.closeSide(&w.uni, &w.uniHL)
	w.model = nil
	w.dirty = false
}

// FileNav is the position of the shown file in a change set.
type FileNav struct {
	// Index is 0-based (-1: the file is not part of the set any more);
	// Total is the number of files.
	Index, Total int
	// Prev and Next name the neighbours (tooltips).
	Prev, Next string
}

// SetFileNav enables the previous/next file buttons (Total > 0) or hides
// them (zero FileNav).
func (w *DiffWindow) SetFileNav(n FileNav) {
	w.nav = n
	single := n.Total < 2 && n.Index >= 0
	w.fileBar.Items[0].Disabled, w.fileBar.Items[1].Disabled = single, single
	w.fileBar.Items[0].Label, w.fileBar.Items[1].Label = navLabel("Previous file", n.Prev), navLabel("Next file", n.Next)
}

// FileNav returns the file navigation.
func (w *DiffWindow) FileNav() FileNav { return w.nav }

func (n FileNav) text() string {
	if n.Index < 0 {
		return fmt.Sprintf("– / %d", n.Total)
	}
	return fmt.Sprintf("%d / %d", n.Index+1, n.Total)
}

// Content returns the shown content (the blocks as currently computed).
func (w *DiffWindow) Content() Content { return w.content }

// Blocks returns the current change blocks.
func (w *DiffWindow) Blocks() []Block { return w.blocks }

// Count returns the number of differences.
func (w *DiffWindow) Count() int { return len(w.blocks) }

// Current returns the index of the selected difference (-1 if none).
func (w *DiffWindow) Current() int { return w.cur }

// Editable reports whether the right side is editable.
func (w *DiffWindow) Editable() bool { return w.editable }

// Unified reports the unified view mode.
func (w *DiffWindow) Unified() bool { return w.unified }

// SetUnified switches the view mode (no event).
func (w *DiffWindow) SetUnified(u bool) {
	w.unified = u
	if w.cur >= 0 {
		w.pendingTo = w.cur
	}
}

// IgnoreWhitespace reports the white space toggle.
func (w *DiffWindow) IgnoreWhitespace() bool { return w.ignoreWS }

// SetIgnoreWhitespace sets the toggle (no event, blocks are not
// recomputed).
func (w *DiffWindow) SetIgnoreWhitespace(on bool) {
	w.ignoreWS = on
	w.ws.State = atoms.Unchecked
	if on {
		w.ws.State = atoms.Checked
	}
}

// Title returns a title for the window or tab, e.g.
// "src/Foo.php (HEAD (1a2b3c4d) ↔ Working tree)".
func (w *DiffWindow) Title() string {
	p := w.content.Right.Path
	if p == "" {
		p = w.content.Left.Path
	}
	return fmt.Sprintf("%s (%s ↔ %s)", p, w.content.Left.Label, w.content.Right.Label)
}

// Editors returns the left and right editors (nil before SetContent), e.g.
// to focus the right one.
func (w *DiffWindow) Editors() (left, right *editorview.Editor) { return w.left, w.right }

// Next selects the next difference and scrolls to it.
func (w *DiffWindow) Next() bool { return w.goTo(w.cur + 1) }

// Prev selects the previous difference and scrolls to it.
func (w *DiffWindow) Prev() bool {
	if w.cur < 0 {
		return w.goTo(len(w.blocks) - 1)
	}
	return w.goTo(w.cur - 1)
}

func (w *DiffWindow) goTo(i int) bool {
	if i < 0 || i >= len(w.blocks) {
		return false
	}
	w.cur = i
	w.pendingTo = i
	return true
}

// ApplyRevert performs a RevertRequested on the editable right buffer as
// one undoable edit.
func (w *DiffWindow) ApplyRevert(ev RevertRequested) error {
	if !w.editable || w.right == nil {
		return fmt.Errorf("diff: right side is not editable")
	}
	b := w.right.Buffer()
	s := b.Snapshot()
	off := func(line int) int {
		if line >= s.LineCount() {
			return s.Len()
		}
		return s.LineStart(max(0, line))
	}
	start, end := off(ev.Change.RightStart), off(ev.Change.RightEnd)
	if _, err := b.Replace(start, end-start, []byte(ev.Text)); err != nil {
		return fmt.Errorf("diff: revert: %w", err)
	}
	w.recompute()
	return nil
}

// leftText returns the left lines [from, to) including terminators.
func (w *DiffWindow) leftText(from, to int) string {
	s := w.left.Buffer().Snapshot()
	off := func(line int) int {
		if line >= s.LineCount() {
			return s.Len()
		}
		return s.LineStart(max(0, line))
	}
	return string(s.Slice(off(from), off(to)))
}

func (w *DiffWindow) markDirty(now time.Time) {
	w.dirty = true
	w.dirtyAt = now
}

// Update returns the events since the last call.
func (w *DiffWindow) Update(gtx layout.Context) []Event {
	w.update(gtx)
	return w.q.drain()
}

func (w *DiffWindow) emit(gtx layout.Context, e Event) {
	w.q.emit(e)
	gtx.Execute(op.InvalidateCmd{})
}

func (w *DiffWindow) update(gtx layout.Context) {
	for _, id := range w.tools.Update(gtx) {
		switch id {
		case itemPrev:
			w.Prev()
		case itemNext:
			w.Next()
		}
	}
	for _, id := range w.fileBar.Update(gtx) {
		if w.nav.Total == 0 {
			continue
		}
		switch id {
		case itemPrevFile:
			w.emit(gtx, FileNavRequested{Delta: -1})
		case itemNextFile:
			w.emit(gtx, FileNavRequested{Delta: 1})
		}
	}
	for _, id := range w.openBar.Update(gtx) {
		if id == itemOpen {
			w.emit(gtx, OpenFileRequested{Path: w.content.Right.Path, Line: w.openLine()})
		}
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameF7, Optional: key.ModShift})
		if !ok {
			break
		}
		if ke, ok := ev.(key.Event); ok && ke.State == key.Press {
			if ke.Modifiers.Contain(key.ModShift) {
				w.Prev()
			} else {
				w.Next()
			}
		}
	}
	if w.sbsBtn.Clicked(gtx) && w.unified {
		w.SetUnified(false)
		w.emit(gtx, ViewModeChanged{Unified: false})
	}
	if w.uniBtn.Clicked(gtx) && !w.unified {
		w.SetUnified(true)
		w.emit(gtx, ViewModeChanged{Unified: true})
	}
	if w.ws.Update(gtx) {
		w.ignoreWS = w.ws.Checked()
		w.emit(gtx, IgnoreWhitespaceChanged{Ignore: w.ignoreWS})
		w.markDirty(time.Time{})
	}
	if w.editable {
		for i := range w.reverts {
			if w.reverts[i].Clicked(gtx) && i < len(w.blocks) {
				b := w.blocks[i]
				w.emit(gtx, RevertRequested{Block: i, Change: b, Text: w.leftText(b.LeftStart, b.LeftEnd)})
			}
		}
	}
	for _, ed := range []*editorview.Editor{w.left, w.right, w.uni} {
		if ed == nil {
			continue
		}
		for {
			ev, ok := ed.Update(gtx)
			if !ok {
				break
			}
			if ce, ok := ev.(editorview.ChangeEvent); ok && ed == w.right {
				w.emit(gtx, RightEdited{Edit: ce.Edit})
				w.markDirty(gtx.Now)
			}
		}
	}
	if w.dirty && w.left != nil {
		if due := w.dirtyAt.Add(recomputeWait); gtx.Now.Before(due) {
			gtx.Execute(op.InvalidateCmd{At: due})
		} else {
			w.recompute()
		}
	}
}

// recompute refreshes the blocks from the current texts.
func (w *DiffWindow) recompute() {
	cur := w.cur
	w.setBlocks(Compute(w.left.Buffer().String(), w.right.Buffer().String(), w.ignoreWS))
	w.cur = min(cur, len(w.blocks)-1)
}

// openLine is the right line to open: the selected block or the caret.
func (w *DiffWindow) openLine() int {
	if w.cur >= 0 && w.cur < len(w.blocks) {
		return w.blocks[w.cur].RightStart
	}
	if w.right != nil {
		s := w.right.Buffer().Snapshot()
		return s.OffsetToPos(w.right.Cursor().Primary().Head).Line
	}
	return 0
}

// Layout draws the window filling the maximum constraints.
func (w *DiffWindow) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	w.update(gtx)
	gtx.Constraints.Min = gtx.Constraints.Max
	return atoms.Surface{Kind: atoms.SurfacePanel}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.layoutToolbar(gtx, th) }),
			layout.Rigid(atoms.Divider{}.Fn(th)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return w.layoutBody(gtx, th) }),
		)
	})
}

func (w *DiffWindow) counterText() string {
	switch n := len(w.blocks); {
	case w.content.Left.Binary || w.content.Right.Binary:
		return "Binary files differ"
	case n == 0 && w.left != nil:
		return "No differences"
	case n == 1:
		return "1 difference"
	default:
		if w.cur >= 0 {
			return fmt.Sprintf("%d differences · %d of %d", n, w.cur+1, n)
		}
		return fmt.Sprintf("%d differences", n)
	}
}

func (w *DiffWindow) layoutToolbar(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	w.tools.Items[0].Disabled = len(w.blocks) == 0 || w.cur == 0
	w.tools.Items[1].Disabled = len(w.blocks) == 0 || w.cur >= len(w.blocks)-1
	if w.unified {
		w.sbsBtn.Kind, w.uniBtn.Kind = atoms.Ghost, atoms.Secondary
	} else {
		w.sbsBtn.Kind, w.uniBtn.Kind = atoms.Secondary, atoms.Ghost
	}
	return atoms.Surface{Kind: atoms.SurfaceAlt}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		gap := func() layout.FlexChild { return layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout) }
		var children []layout.FlexChild
		if w.nav.Total > 0 {
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.fileBar.Layout(gtx, th) }),
				layout.Rigid(atoms.Small(w.nav.text()).SingleLine().Fn(th)),
				gap(),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Max.Y = gtx.Dp(th.Size.IconSmall)
					return atoms.Divider{Vertical: true}.Layout(gtx, th)
				}),
				gap(),
			)
		}
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.tools.Layout(gtx, th) }),
			gap(),
			layout.Rigid(atoms.Muted(w.counterText()).SingleLine().Fn(th)),
			layout.Flexed(1, layout.Spacer{}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.sbsBtn.Layout(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.uniBtn.Layout(gtx, th) }),
			gap(),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.ws.Layout(gtx, th) }),
		)
		if w.editable {
			children = append(children, gap(),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.openBar.Layout(gtx, th) }))
		}
		return layout.Inset{Left: th.Space.XS, Right: th.Space.S, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
			})
	})
}

func navLabel(what, name string) string {
	if name == "" {
		return what
	}
	return what + ": " + name
}

// sideHeader is the title above an editor column.
func sideHeader(th *tokens.Theme, s Side) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return atoms.Surface{Kind: atoms.SurfaceAlt}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S, Right: th.Space.S, Top: th.Space.XS, Bottom: th.Space.XS}.Layout(gtx,
				func(gtx layout.Context) layout.Dimensions {
					note := s.Path
					switch {
					case s.Missing:
						note += " — does not exist"
					case s.Binary:
						note += " — binary"
					}
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(atoms.Strong(s.Label).SingleLine().Fn(th)),
						layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
						layout.Flexed(1, atoms.Muted(note).SingleLine().Fn(th)),
					)
				})
		})
	}
}

func (w *DiffWindow) layoutBody(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if w.left == nil {
		return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted("Nothing to compare").Fn(th))
	}
	if w.content.Left.Binary || w.content.Right.Binary {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.layoutHeaders(gtx, th) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(th.Space.L).Layout(gtx, atoms.Muted("Binary content is not compared.").Fn(th))
			}),
		)
	}
	if w.unified {
		return w.layoutUnified(gtx, th)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return w.layoutHeaders(gtx, th) }),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return w.layoutColumns(gtx, th) }),
	)
}

// columns returns the widths of the left column, the strip and the right
// column.
func columns(gtx layout.Context, total int) (l, s, r int) {
	s = min(gtx.Dp(stripW), total/5)
	l = (total - s) / 2
	return l, s, total - s - l
}

func (w *DiffWindow) layoutHeaders(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	lw, sw, rw := columns(gtx, gtx.Constraints.Max.X)
	fixed := func(px int, wd layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = px, px
			return wd(gtx)
		})
	}
	return layout.Flex{}.Layout(gtx,
		fixed(lw, sideHeader(th, w.content.Left)),
		fixed(sw, func(gtx layout.Context) layout.Dimensions {
			return atoms.Surface{Kind: atoms.SurfaceAlt}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, gtx.Dp(th.Size.RowH))}
			})
		}),
		fixed(rw, sideHeader(th, w.content.Right)),
	)
}

// lineMaps returns the vertical offset mappings left→right and
// right→left, aligning the centers of both viewports.
func (w *DiffWindow) lineMaps() (lr, rl func(int) int) {
	gb := toGit(w.blocks)
	m := func(fromLeft bool, ed *editorview.Editor) func(int) int {
		return func(y int) int {
			lh := ed.LineHeight()
			if lh <= 0 {
				return y
			}
			half := float64(ed.ViewportHeight()) / 2
			line := (float64(y) + half) / float64(lh)
			return int(math.Round(gitcli.MapLine(gb, line, fromLeft)*float64(lh) - half))
		}
	}
	return m(true, w.left), m(false, w.right)
}

func (w *DiffWindow) layoutColumns(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	size := gtx.Constraints.Max
	lw, sw, rw := columns(gtx, size.X)
	if w.pendingTo >= 0 && w.pendingTo < len(w.blocks) && w.right.LineHeight() > 0 {
		b := w.blocks[w.pendingTo]
		y := w.right.LineTop(b.RightStart) + w.right.ScrollOffset().Y - size.Y/3
		w.right.SetScrollOffset(image.Pt(w.right.ScrollOffset().X, max(0, y)))
		w.left.Cursor().SetCaret(w.left.Buffer().Snapshot().LineStart(min(b.LeftStart, w.left.Buffer().LineCount()-1)))
		w.right.Cursor().SetCaret(w.right.Buffer().Snapshot().LineStart(min(b.RightStart, w.right.Buffer().LineCount()-1)))
		w.pendingTo = -1
	}
	lr, rl := w.lineMaps()
	w.sync.before(w.left, w.right, lr, rl)

	place := func(x, wd int, f func(gtx layout.Context)) {
		t := op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
		cg := gtx
		cg.Constraints = layout.Exact(image.Pt(wd, size.Y))
		f(cg)
		t.Pop()
	}
	place(0, lw, func(gtx layout.Context) { w.left.Layout(gtx, th) })
	place(lw+sw, rw, func(gtx layout.Context) { w.right.Layout(gtx, th) })
	if w.sync.after(w.left, w.right, lr, rl) {
		gtx.Execute(op.InvalidateCmd{})
	}
	place(lw, sw, func(gtx layout.Context) { w.layoutStrip(gtx, th) })
	return layout.Dimensions{Size: size}
}

// layoutStrip draws the bands between the columns and the revert arrows.
func (w *DiffWindow) layoutStrip(gtx layout.Context, th *tokens.Theme) {
	size := gtx.Constraints.Max
	bands := molecules.ChangeBands{Bands: make([]molecules.Band, 0, 16)}
	type vis struct {
		i, y int
	}
	var arrows []vis
	for i, b := range w.blocks {
		lt, lb := w.left.LineTop(b.LeftStart), w.left.LineTop(b.LeftEnd)
		rt, rb := w.right.LineTop(b.RightStart), w.right.LineTop(b.RightEnd)
		if max(lb, rb) < 0 {
			continue
		}
		if min(lt, rt) > size.Y {
			break
		}
		lineC, _, edge := editorview.DecorationColors(th, b.Kind.decoration())
		bands.Bands = append(bands.Bands, molecules.Band{LeftTop: lt, LeftBottom: lb, RightTop: rt, RightBottom: rb,
			Fill: lineC, Edge: tokens.WithAlpha(edge, 0xB0), Emphasis: i == w.cur})
		if w.editable {
			y := lt
			if lb > lt {
				y = lt + (min(lb-lt, w.left.LineHeight())-gtx.Dp(revertSize))/2
			} else {
				y = lt - gtx.Dp(revertSize)/2
			}
			arrows = append(arrows, vis{i: i, y: min(max(y, 0), size.Y-gtx.Dp(revertSize))})
		}
	}
	bands.Layout(gtx, th)
	for _, a := range arrows {
		t := op.Offset(image.Pt(gtx.Dp(2), a.y)).Push(gtx.Ops)
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(size.X, gtx.Dp(revertSize))}
		w.tips[a.i].Layout(cg, th, func(gtx layout.Context) layout.Dimensions { return w.reverts[a.i].Layout(gtx, th) })
		t.Pop()
	}
}

func (w *DiffWindow) layoutUnified(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if w.model == nil {
		w.model = buildUnified(w.left.Buffer().String(), w.right.Buffer().String(), w.blocks)
		w.closeSide(&w.uni, &w.uniHL)
		w.uni, w.uniHL = w.newEditor(buffer.NewString(w.model.text), w.content.Right.Path, true)
		w.uni.HideLineNumbers = true
		w.uni.SetGutterColumn(w.model)
		w.uni.SetLineDecorations(w.model.decos)
	}
	if w.pendingTo >= 0 && w.pendingTo < len(w.model.starts) && w.uni.LineHeight() > 0 {
		row := w.model.starts[w.pendingTo]
		y := w.uni.LineTop(row) + w.uni.ScrollOffset().Y - gtx.Constraints.Max.Y/3
		w.uni.SetScrollOffset(image.Pt(0, max(0, y)))
		w.uni.Cursor().SetCaret(w.uni.Buffer().Snapshot().LineStart(min(row, w.uni.Buffer().LineCount()-1)))
		w.pendingTo = -1
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(sideHeader(th, Side{Label: w.content.Left.Label + " → " + w.content.Right.Label, Path: w.content.Right.Path})),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if w.uni.LineHeight() == 0 && w.pendingTo >= 0 {
				gtx.Execute(op.InvalidateCmd{})
			}
			return w.uni.Layout(gtx, th)
		}),
	)
}
