package editorview

import (
	"image"
	"image/color"
	"sort"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/search"
	"github.com/ape-dev-de/tiegel/internal/editor/wrap"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Fallback size when a constraint is unbounded.
const (
	fallbackWidth  = unit.Dp(640)
	fallbackHeight = unit.Dp(400)
	blinkInterval  = 530 * time.Millisecond
	blinkTimeout   = 30 * time.Second
)

// geometry is the layout of the last frame, used to map pointer
// positions and for AnchorRect.
type geometry struct {
	valid     bool
	size      image.Point
	gutterW   int
	annotW    int
	iconX     int
	iconW     int
	numRight  int
	chgX      int
	chgW      int
	foldX     int
	foldW     int
	textX     int // x of column 0 before scrolling
	textW     int
	rows      int // visible (unfolded) rows
	lineH     int
	maxScroll image.Point
	vbar      image.Rectangle // vertical scrollbar track
	vthumb    image.Rectangle
	hbar      image.Rectangle
	hthumb    image.Rectangle
}

// visLine is one row drawn in the current frame: a whole line, or one
// visual row (segment) of a soft-wrapped line.
type visLine struct {
	row, line  int
	sub        int  // row index within the line (0: first)
	last       bool // last row of the line
	y          int
	start, end int // document offsets of the row content
	ls         int // document offset of the line start
	trail      int // line-relative start of trailing whitespace
	tStart     int // offset of the row text in Editor.lineText
	tEnd       int
}

func (e *Editor) tabWidth() int {
	if e.TabWidth <= 0 {
		return 4
	}
	return e.TabWidth
}

func imagePt(x, y int) image.Point { return image.Point{X: x, Y: y} }

func numDigits(n int) int {
	d := 1
	for n >= 10 {
		n /= 10
		d++
	}
	return d
}

// Layout draws the editor filling the maximum constraints and handles
// input. It implements ui.Component.
func (e *Editor) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	// Events that already survived one Layout are dropped.
	if e.stale > 0 {
		e.events = append(e.events[:0], e.events[e.stale:]...)
		e.stale = 0
	}
	e.processInput(gtx)

	size := gtx.Constraints.Max
	if size.X >= draw.Unbounded {
		size.X = max(gtx.Constraints.Min.X, gtx.Dp(fallbackWidth))
	}
	if size.Y >= draw.Unbounded {
		size.Y = max(gtx.Constraints.Min.Y, gtx.Dp(fallbackHeight))
	}
	e.updateMetrics(gtx, th)
	e.cur.TabWidth = e.tabWidth()
	e.cur.AutoClose = e.AutoClose
	e.computeGeometry(gtx, th, size)
	e.cur.PageLines = max(1, size.Y/e.met.lineH-1)
	if e.scrollCare {
		e.scrollToCaret()
		e.scrollCare = false
	}
	e.clampScroll()
	e.cache.frame++

	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	e.paint(gtx, th)
	e.registerInput(gtx)
	// Re-register the pointer filter with the scroll ranges of this frame.
	e.processPointer(gtx)
	e.updateIME(gtx)
	if e.focused && !e.ReadOnly {
		e.scheduleBlink(gtx)
	}
	e.cache.sweep()
	e.stale = len(e.events)
	return layout.Dimensions{Size: size}
}

func (e *Editor) computeGeometry(gtx layout.Context, th *tokens.Theme, size image.Point) {
	g := &e.geo
	g.valid = true
	g.size = size
	g.lineH = e.met.lineH
	digits := max(3, numDigits(e.buf.LineCount()))
	g.annotW = 0
	if e.annot != nil {
		g.annotW = max(0, e.annot.GutterWidth(gtx, th))
	}
	x := g.annotW + gtx.Dp(6)
	g.iconX, g.iconW = x, gtx.Dp(16)
	x += g.iconW + gtx.Dp(4)
	if !e.HideLineNumbers {
		x += e.met.colX(digits)
	}
	g.numRight = x
	x += gtx.Dp(6)
	g.chgX, g.chgW = x, gtx.Dp(3)
	x += g.chgW + gtx.Dp(3)
	g.foldX, g.foldW = x, gtx.Dp(14)
	x += g.foldW
	g.gutterW = x
	g.textX = x + gtx.Dp(8)
	g.textW = max(0, size.X-g.textX)
	e.updateWrap(g.textW - gtx.Dp(10))
	g.rows = e.rowCount()

	content := g.rows * g.lineH
	g.maxScroll.Y = 0
	if content > size.Y {
		g.maxScroll.Y = content - size.Y + size.Y/2
	}
	g.maxScroll.X = max(0, e.met.colX(e.maxCols+4)-g.textW)
	if e.wrapped {
		g.maxScroll.X = 0
	}

	sb := gtx.Dp(10)
	g.vbar = image.Rect(size.X-sb, 0, size.X, size.Y)
	g.vthumb = image.Rectangle{}
	if g.maxScroll.Y > 0 {
		total := content + size.Y/2
		th := max(gtx.Dp(24), size.Y*size.Y/max(1, total))
		th = min(th, size.Y)
		y := (size.Y - th) * e.scroll.Y / max(1, g.maxScroll.Y)
		g.vthumb = image.Rect(g.vbar.Min.X+gtx.Dp(2), y, g.vbar.Max.X-gtx.Dp(2), y+th)
	}
	g.hbar = image.Rect(g.textX, size.Y-sb, size.X-sb, size.Y)
	g.hthumb = image.Rectangle{}
	if g.maxScroll.X > 0 {
		trackW := g.hbar.Dx()
		total := g.textW + g.maxScroll.X
		tw := max(gtx.Dp(24), trackW*g.textW/max(1, total))
		tw = min(tw, trackW)
		x := g.hbar.Min.X + (trackW-tw)*e.scroll.X/max(1, g.maxScroll.X)
		g.hthumb = image.Rect(x, g.hbar.Min.Y+gtx.Dp(2), x+tw, g.hbar.Max.Y-gtx.Dp(2))
	}
}

func (e *Editor) clampScroll() {
	e.scroll.X = min(max(e.scroll.X, 0), e.geo.maxScroll.X)
	e.scroll.Y = min(max(e.scroll.Y, 0), e.geo.maxScroll.Y)
}

// scrollToCaret scrolls minimally so the primary caret is visible.
func (e *Editor) scrollToCaret() {
	s := e.buf.Snapshot()
	row, col := e.caretRowCol(s, e.cur.Primary())
	lineH := e.geo.lineH
	y := row * lineH
	viewH := e.geo.size.Y
	margin := lineH
	if viewH < 4*lineH {
		margin = 0
	}
	switch {
	case y-margin < e.scroll.Y:
		e.scroll.Y = y - margin
	case y+lineH+margin > e.scroll.Y+viewH:
		e.scroll.Y = y + lineH + margin - viewH
	}
	if e.geo.maxScroll.Y < e.scroll.Y {
		e.geo.maxScroll.Y = e.scroll.Y
	}
	if e.wrapped {
		return
	}
	e.maxCols = max(e.maxCols, col)
	e.geo.maxScroll.X = max(e.geo.maxScroll.X, e.met.colX(e.maxCols+4)-e.geo.textW)
	x := e.met.colX(col)
	mx := e.met.colX(4)
	switch {
	case x-mx < e.scroll.X:
		e.scroll.X = x - mx
	case x+mx > e.scroll.X+e.geo.textW:
		e.scroll.X = x + mx - e.geo.textW
	}
}

// visibleLines collects the rows of the current frame and their text.
func (e *Editor) visibleLines(s buffer.Snapshot) []visLine {
	lineH := e.geo.lineH
	first := e.scroll.Y / lineH
	last := min(e.geo.rows-1, (e.scroll.Y+e.geo.size.Y-1)/lineH)
	vis := e.vis[:0]
	text := e.lineText[:0]
	if e.wrapped {
		vis, text = e.visibleRows(s, first, last, vis, text)
	} else {
		for r := first; r <= last; r++ {
			l := e.folds.VisibleToDoc(r)
			start := s.LineStart(l)
			t0 := len(text)
			text = s.AppendLine(text, l)
			vis = append(vis, visLine{row: r, line: l, last: true, y: r*lineH - e.scroll.Y, start: start,
				end: start + len(text) - t0, ls: start, trail: trailingStart(text[t0:]), tStart: t0, tEnd: len(text)})
		}
	}
	e.vis, e.lineText = vis, text
	return vis
}

// visibleRows collects the soft-wrapped rows first..last: the line of
// each row is wrapped once, its rows become separate visLines.
func (e *Editor) visibleRows(s buffer.Snapshot, first, last int, vis []visLine, text []byte) ([]visLine, []byte) {
	lineH := e.geo.lineH
	l, sub := e.view.LineAt(first)
	for r := first; r <= last; {
		ls := s.LineStart(l)
		t0 := len(text)
		text = s.AppendLine(text, l)
		lt := text[t0:]
		starts := wrap.Breaks(append(e.wrapBrks[:0], 0), lt, e.wrap.Width(), e.wrap.TabWidth())
		e.wrapBrks = starts
		trail := trailingStart(lt)
		for k := sub; k < len(starts) && r <= last; k++ {
			a, b := starts[k], len(lt)
			if k+1 < len(starts) {
				b = starts[k+1]
			}
			vis = append(vis, visLine{row: r, line: l, sub: k, last: k+1 == len(starts), y: r*lineH - e.scroll.Y,
				start: ls + a, end: ls + b, ls: ls, trail: trail, tStart: t0 + a, tEnd: t0 + b})
			r++
		}
		sub = 0
		next := e.folds.VisibleToDoc(e.folds.DocToVisible(l) + 1)
		if next <= l {
			break
		}
		l = next
	}
	return vis, text
}

func (e *Editor) lineBytes(v visLine) []byte { return e.lineText[v.tStart:v.tEnd] }

// colOf returns the visual column of document offset off in line v.
func (e *Editor) colOf(v visLine, off int) int {
	return buffer.VisualCol(e.lineBytes(v), off-v.start, e.tabWidth()) + inlayShift(e.visInlays(v), off-v.start)
}

func (e *Editor) paint(gtx layout.Context, th *tokens.Theme) {
	g := &e.geo
	ops := gtx.Ops
	s := e.buf.Snapshot()
	draw.Rect(ops, image.Rectangle{Max: g.size}, th.Editor.Background)
	vis := e.visibleLines(s)
	sels := e.cur.Selections()
	primary := e.cur.Primary()

	caretLines := map[int]bool{}
	for _, sel := range sels {
		if sel.Empty() {
			caretLines[s.OffsetToPos(sel.Head).Line] = true
		}
	}
	execLines := map[int]bool{}
	markers := make([][]Marker, len(vis))
	if e.markers != nil {
		for i, v := range vis {
			if i > 0 && vis[i-1].line == v.line {
				markers[i] = markers[i-1]
				continue
			}
			markers[i] = e.markers.Markers(v.line)
			for _, m := range markers[i] {
				if m.Kind == MarkerExecutionPoint {
					execLines[v.line] = true
				}
			}
		}
	}

	// Text area.
	textArea := image.Rect(g.gutterW, 0, g.size.X, g.size.Y)
	area := clip.Rect(textArea).Push(ops)
	ox := g.textX - e.scroll.X
	decorated := e.paintDecorations(ops, th, vis, ox)
	for _, v := range vis {
		row := image.Rect(g.gutterW, v.y, g.size.X, v.y+g.lineH)
		switch {
		case execLines[v.line]:
			draw.Rect(ops, row, tokens.WithAlpha(th.Color.Warning, 0x24))
		case caretLines[v.line] && e.focused && !decorated[v.line]:
			draw.Rect(ops, row, th.Editor.CurrentLine)
		}
	}
	e.paintRuler(gtx, th, ox)
	e.paintHighlights(ops, th, s, vis, ox)
	e.paintSearch(ops, th, s, vis, primary, ox)
	e.paintSelections(ops, th, vis, sels, ox)
	e.paintBrackets(ops, th, s, vis, primary, ox)
	e.paintSelectionWhitespace(ops, th, vis, sels, ox)

	firstCol := e.scroll.X / max(1, e.met.cellW.Round())
	lastCol := firstCol + g.textW/max(1, e.met.cellW.Round()) + 1
	for _, v := range vis {
		line := e.lineBytes(v)
		spans := e.lineSpans(s, v)
		call, cols := e.lineDrawing(th, lineKey{v.line, v.sub}, line, spans, firstCol, lastCol, e.wsFrom(v), e.visInlays(v))
		if !e.wrapped {
			e.maxCols = max(e.maxCols, cols)
		}
		t := op.Offset(imagePt(ox, v.y)).Push(ops)
		call.Add(ops)
		t.Pop()
		if v.last && e.folds.IsCollapsed(v.line) {
			if r, ok := e.folds.RangeAt(v.line); ok && r.EndLine > v.line {
				x := ox + e.met.colX(cols+1)
				e.paintFoldPlaceholder(gtx, th, x, v.y)
			}
		}
	}
	e.paintDiagnostics(ops, th, vis, ox)
	e.paintComposition(ops, th, vis, ox)
	e.paintCarets(gtx, th, vis, sels, ox)
	area.Pop()

	e.paintGutter(gtx, th, vis, markers, caretLines, decorated)
	e.paintSeparators(gtx, th)
	e.paintDecorationMarks(gtx, th)
	e.paintScrollbars(gtx, th, s, sels)
}

// lineSpans returns the highlighter spans of v relative to the line start.
func (e *Editor) lineSpans(s buffer.Snapshot, v visLine) []highlight.Span {
	if e.Highlighter == nil || v.end == v.start {
		return nil
	}
	spans := e.Highlighter.Spans(s, v.start, v.end)
	out := e.spans[:0]
	for _, sp := range spans {
		a, b := max(sp.Start, v.start), min(sp.End, v.end)
		if b > a {
			out = append(out, highlight.Span{Start: a - v.start, End: b - v.start, Role: sp.Role})
		}
	}
	e.spans = out
	return out
}

// rangeRects calls fn with the column span of [start, end) on every
// visible row it touches. eolCols is added when the range continues past
// the end of a line.
func (e *Editor) rangeCols(vis []visLine, start, end int, fn func(v visLine, c0, c1 int)) {
	i := sort.Search(len(vis), func(i int) bool { return vis[i].end >= start })
	for ; i < len(vis); i++ {
		v := vis[i]
		if v.start > end || (v.start == end && start != end && v.start > start) {
			break
		}
		if !v.last && start >= v.end && v.end > v.start {
			continue // starts at the soft break: drawn on the next row
		}
		c0 := 0
		if start > v.start {
			c0 = e.colOf(v, min(start, v.end))
		}
		var c1 int
		if end > v.end {
			c1 = e.colOf(v, v.end)
			if v.last {
				c1++ // the line ending
			}
		} else {
			c1 = e.colOf(v, end)
		}
		fn(v, c0, c1)
	}
}

func (e *Editor) paintSelections(ops *op.Ops, th *tokens.Theme, vis []visLine, sels []cursor.Selection, ox int) {
	if len(vis) == 0 {
		return
	}
	c := th.Editor.Selection
	if !e.focused {
		c = tokens.Mix(th.Editor.Selection, th.Editor.Background, 0.35)
	}
	lo, hi := vis[0].start, vis[len(vis)-1].end
	i := sort.Search(len(sels), func(i int) bool { return sels[i].End() >= lo })
	for ; i < len(sels) && sels[i].Start() <= hi; i++ {
		sel := sels[i]
		if sel.Empty() {
			continue
		}
		e.rangeCols(vis, sel.Start(), sel.End(), func(v visLine, c0, c1 int) {
			if c1 > c0 {
				draw.Rect(ops, image.Rect(ox+e.met.colX(c0), v.y, ox+e.met.colX(c1), v.y+e.geo.lineH), c)
			}
		})
	}
}

// searchCache caches the matches of the visible range.
type searchCache struct {
	q        *search.Query
	version  uint64
	lo, hi   int
	matches  []search.Match
	all      []search.Match // whole document, for the scrollbar
	allValid bool
}

// maxSearchDoc is the document size up to which all matches are
// computed for the scrollbar markers.
const maxSearchDoc = 1 << 20

func (e *Editor) visibleMatches(s buffer.Snapshot, lo, hi int) []search.Match {
	c := &e.srch
	if c.q != e.query || c.version != s.Version() {
		*c = searchCache{q: e.query, version: s.Version(), lo: -1}
	}
	if c.lo != lo || c.hi != hi {
		c.lo, c.hi = lo, hi
		c.matches = e.query.FindAllIn(s, lo, hi, 2000)
	}
	return c.matches
}

func (e *Editor) paintSearch(ops *op.Ops, th *tokens.Theme, s buffer.Snapshot, vis []visLine, primary cursor.Selection, ox int) {
	if e.query == nil || len(vis) == 0 {
		return
	}
	for _, m := range e.visibleMatches(s, vis[0].start, vis[len(vis)-1].end) {
		if m.Empty() {
			continue
		}
		c := tokens.WithAlpha(th.Color.Warning, 0x40)
		if m.Start == primary.Start() && m.End == primary.End() {
			c = tokens.WithAlpha(th.Color.Warning, 0x90)
		}
		e.rangeCols(vis, m.Start, m.End, func(v visLine, c0, c1 int) {
			draw.RRect(ops, image.Rect(ox+e.met.colX(c0), v.y+1, ox+e.met.colX(c1), v.y+e.geo.lineH-1), 2, c)
		})
	}
}

// bracketCache caches the bracket pair of the primary caret.
type bracketCache struct {
	version     uint64
	head        int
	valid       bool
	open, close int
	ok          bool
}

func (e *Editor) paintBrackets(ops *op.Ops, th *tokens.Theme, s buffer.Snapshot, vis []visLine, primary cursor.Selection, ox int) {
	if !primary.Empty() || len(vis) == 0 {
		return
	}
	b := &e.brk
	if !b.valid || b.version != s.Version() || b.head != primary.Head {
		b.open, b.close, b.ok = cursor.MatchBracket(s, primary.Head)
		b.version, b.head, b.valid = s.Version(), primary.Head, true
	}
	if !b.ok {
		return
	}
	c := tokens.WithAlpha(th.Color.Accent, 0x48)
	for _, off := range []int{b.open, b.close} {
		e.rangeCols(vis, off, off+1, func(v visLine, c0, c1 int) {
			r := image.Rect(ox+e.met.colX(c0), v.y+1, ox+e.met.colX(c1), v.y+e.geo.lineH-1)
			draw.RRect(ops, r, 2, c)
			draw.Border(ops, r, 2, 1, tokens.WithAlpha(th.Color.Accent, 0xA0))
		})
	}
}

func severityColor(th *tokens.Theme, s Severity) color.NRGBA {
	switch s {
	case SeverityError:
		return th.Color.Danger
	case SeverityWarning:
		return th.Color.Warning
	case SeverityInfo:
		return th.Color.Info
	}
	return th.Color.TextMuted
}

func (e *Editor) paintDiagnostics(ops *op.Ops, th *tokens.Theme, vis []visLine, ox int) {
	if len(e.diags) == 0 || len(vis) == 0 {
		return
	}
	lo, hi := vis[0].start, vis[len(vis)-1].end
	// Paint less severe first so errors end up on top.
	for sev := SeverityHint; ; sev-- {
		for _, d := range e.diags {
			if d.Severity != sev || d.End < lo || d.Start > hi {
				continue
			}
			end := d.End
			if end == d.Start {
				end++ // zero-width diagnostics mark one cell
			}
			c := severityColor(th, d.Severity)
			e.rangeCols(vis, d.Start, end, func(v visLine, c0, c1 int) {
				c1 = max(c1, c0+1)
				x0, x1 := ox+e.met.colX(c0), ox+e.met.colX(c1)
				y := v.y + e.met.base + max(2, e.geo.lineH/8)
				if d.Severity == SeverityHint {
					draw.Rect(ops, image.Rect(x0, y, x1, y+1), tokens.WithAlpha(c, 0xA0))
					return
				}
				squiggle(ops, x0, x1, y, max(2, e.geo.lineH/10), c)
			})
		}
		if sev == SeverityError {
			break
		}
	}
}

// squiggle strokes a wavy line from x0 to x1 around y.
func squiggle(ops *op.Ops, x0, x1, y, amp int, c color.NRGBA) {
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(f32.Pt(float32(x0), float32(y+amp)))
	step := float32(amp) * 1.5
	up := true
	for x := float32(x0) + step; x <= float32(x1)+step/2; x += step {
		dy := float32(amp)
		if up {
			dy = -dy
		}
		p.LineTo(f32.Pt(min(x, float32(x1)), float32(y)+dy*0.5+float32(amp)*0.5))
		up = !up
	}
	paint.FillShape(ops, c, clip.Stroke{Path: p.End(), Width: 1}.Op())
}

func (e *Editor) paintComposition(ops *op.Ops, th *tokens.Theme, vis []visLine, ox int) {
	start, end, ok := e.compositionRange()
	if !ok {
		return
	}
	e.rangeCols(vis, start, end, func(v visLine, c0, c1 int) {
		y := v.y + e.met.base + 2
		draw.Rect(ops, image.Rect(ox+e.met.colX(c0), y, ox+e.met.colX(c1), y+1), th.Editor.Text)
	})
}

func (e *Editor) caretVisible(gtx layout.Context) bool {
	if !e.focused || e.ReadOnly {
		return false
	}
	since := gtx.Now.Sub(e.blinkStart)
	if since < 0 || since > blinkTimeout {
		return true
	}
	return (since/blinkInterval)%2 == 0
}

func (e *Editor) scheduleBlink(gtx layout.Context) {
	since := gtx.Now.Sub(e.blinkStart)
	if since < 0 || since > blinkTimeout {
		return
	}
	next := e.blinkStart.Add((since/blinkInterval + 1) * blinkInterval)
	gtx.Execute(op.InvalidateCmd{At: next})
}

func (e *Editor) paintCarets(gtx layout.Context, th *tokens.Theme, vis []visLine, sels []cursor.Selection, ox int) {
	if len(vis) == 0 || !e.caretVisible(gtx) {
		return
	}
	w := max(1, gtx.Dp(2))
	lo, hi := vis[0].start, vis[len(vis)-1].end
	for _, sel := range sels {
		if sel.Head < lo || sel.Head > hi {
			continue
		}
		i := visIndex(vis, sel.Head, sel.RowEnd)
		if i < 0 {
			continue
		}
		v := vis[i]
		x := ox + e.met.colX(e.colOf(v, sel.Head))
		draw.Rect(gtx.Ops, image.Rect(x-w/2, v.y+1, x-w/2+w, v.y+e.geo.lineH-1), th.Editor.Cursor)
	}
}

func (e *Editor) paintFoldPlaceholder(gtx layout.Context, th *tokens.Theme, x, y int) {
	g := &e.geo
	r := image.Rect(x, y+2, x+e.met.colX(3), y+g.lineH-2)
	draw.RRect(gtx.Ops, r, gtx.Dp(th.Radius.S), th.Color.SurfaceAlt)
	draw.Border(gtx.Ops, r, gtx.Dp(th.Radius.S), 1, th.Color.Border)
	cy := r.Min.Y + r.Dy()/2
	dot := max(1, gtx.Dp(1.2))
	for i := 0; i < 3; i++ {
		cx := r.Min.X + r.Dx()*(i+1)/4
		draw.Circle(gtx.Ops, cx, cy, dot, th.Color.TextMuted)
	}
}

func toneColor(th *tokens.Theme, t Tone) color.NRGBA {
	switch t {
	case ToneAccent:
		return th.Color.Accent
	case ToneInfo:
		return th.Color.Info
	case ToneSuccess:
		return th.Color.Success
	case ToneWarning:
		return th.Color.Warning
	case ToneDanger:
		return th.Color.Danger
	}
	return th.Color.TextMuted
}

func (e *Editor) paintGutter(gtx layout.Context, th *tokens.Theme, vis []visLine, markers [][]Marker, caretLines, decorated map[int]bool) {
	g := &e.geo
	ops := gtx.Ops
	gr := image.Rect(0, 0, g.gutterW, g.size.Y)
	area := clip.Rect(gr).Push(ops)
	defer area.Pop()
	draw.Rect(ops, gr, th.Editor.Gutter)
	lineH := g.lineH
	for i, v := range vis {
		active := caretLines[v.line] && e.focused
		if decorated[v.line] {
			if d, ok := e.decorationAt(v.line); ok {
				lc, _, _ := decorationColors(th, d.Kind)
				draw.Rect(ops, image.Rect(g.annotW, v.y, g.gutterW, v.y+lineH), tokens.Mix(lc, th.Editor.Gutter, 0.35))
			}
		} else if active {
			draw.Rect(ops, image.Rect(0, v.y, g.gutterW, v.y+lineH), th.Editor.CurrentLine)
		}
		nc := th.Editor.LineNumber
		if caretLines[v.line] {
			nc = th.Editor.LineNumberActive
		}
		if !e.HideLineNumbers && v.sub == 0 {
			e.paintNumber(ops, th.Shaper, v.line+1, g.numRight, v.y, nc)
		}

		cy := v.y + lineH/2
		for _, m := range markers[i] {
			// Continuation rows of a wrapped line only continue the change
			// bars.
			if v.sub == 0 || m.Kind == MarkerGitAdded || m.Kind == MarkerGitModified {
				e.paintMarker(gtx, th, m, v, cy)
			}
		}
		if r, ok := e.folds.RangeAt(v.line); ok && r.EndLine > r.StartLine && v.sub == 0 {
			glyph := atoms.GlyphChevronDown
			if e.folds.IsCollapsed(v.line) {
				glyph = atoms.GlyphChevronRight
			}
			sz := min(g.foldW, lineH)
			t := op.Offset(imagePt(g.foldX+(g.foldW-sz)/2, v.y+(lineH-sz)/2)).Push(ops)
			atoms.Icon{Glyph: glyph, Size: unit.Dp(float32(sz) / gtx.Metric.PxPerDp), Color: th.Editor.LineNumber}.Layout(gtx, th)
			t.Pop()
		}
	}
	e.paintGutterColumn(gtx, th, vis, caretLines)
}

func (e *Editor) paintMarker(gtx layout.Context, th *tokens.Theme, m Marker, v visLine, cy int) {
	g := &e.geo
	ops := gtx.Ops
	cx := g.iconX + g.iconW/2
	rad := min(g.iconW, g.lineH) * 3 / 8
	switch m.Kind {
	case MarkerBreakpoint:
		draw.Circle(ops, cx, cy, rad, th.Editor.Breakpoint)
	case MarkerBreakpointDisabled:
		draw.Ring(ops, cx, cy, rad, max(1, gtx.Dp(1.5)), th.Editor.Breakpoint)
	case MarkerBreakpointConditional:
		draw.Circle(ops, cx, cy, rad, th.Editor.Breakpoint)
		draw.Ring(ops, cx, cy, max(1, rad/2), max(1, gtx.Dp(1)), th.Editor.Background)
	case MarkerExecutionPoint:
		e.paintIcon(gtx, th, atoms.GlyphRun, th.Color.Warning, v.y)
	case MarkerGitAdded:
		draw.Rect(ops, image.Rect(g.chgX, v.y, g.chgX+g.chgW, v.y+g.lineH), th.Git.Added)
	case MarkerGitModified:
		draw.Rect(ops, image.Rect(g.chgX, v.y, g.chgX+g.chgW, v.y+g.lineH), th.Git.Modified)
	case MarkerGitDeleted:
		h := max(2, g.lineH/4)
		var p clip.Path
		p.Begin(ops)
		p.MoveTo(f32.Pt(float32(g.chgX), float32(v.y-h)))
		p.LineTo(f32.Pt(float32(g.chgX+g.chgW+h), float32(v.y)))
		p.LineTo(f32.Pt(float32(g.chgX), float32(v.y+h)))
		p.Close()
		paint.FillShape(ops, th.Git.Deleted, clip.Outline{Path: p.End()}.Op())
	case MarkerIcon:
		if m.Glyph != nil {
			e.paintIcon(gtx, th, m.Glyph, toneColor(th, m.Tone), v.y)
		}
	}
}

func (e *Editor) paintIcon(gtx layout.Context, th *tokens.Theme, glyph *atoms.Glyph, c color.NRGBA, y int) {
	g := &e.geo
	sz := min(g.iconW, g.lineH)
	t := op.Offset(imagePt(g.iconX+(g.iconW-sz)/2, y+(g.lineH-sz)/2)).Push(gtx.Ops)
	atoms.Icon{Glyph: glyph, Size: unit.Dp(float32(sz) / gtx.Metric.PxPerDp), Color: c}.Layout(gtx, th)
	t.Pop()
}

func (e *Editor) paintScrollbars(gtx layout.Context, th *tokens.Theme, s buffer.Snapshot, sels []cursor.Selection) {
	g := &e.geo
	ops := gtx.Ops
	thumb := tokens.WithAlpha(th.Color.TextMuted, 0x50)
	if e.ptr.mode == dragVBar || e.ptr.mode == dragHBar {
		thumb = tokens.WithAlpha(th.Color.TextMuted, 0x90)
	}
	if !g.vthumb.Empty() {
		draw.RRect(ops, g.vthumb, g.vthumb.Dx()/2, thumb)
	}
	if !g.hthumb.Empty() {
		draw.RRect(ops, g.hthumb, g.hthumb.Dy()/2, thumb)
	}
	// Markers on the vertical track: search matches, diagnostics, carets.
	if g.rows <= 0 || g.size.Y <= 0 {
		return
	}
	mh := max(2, gtx.Dp(2))
	mx0, mx1 := g.vbar.Min.X+gtx.Dp(3), g.vbar.Max.X-gtx.Dp(1)
	ypos := func(off int) int {
		row := e.lineRow(s.OffsetToPos(off).Line)
		return row * (g.size.Y - mh) / max(1, g.rows)
	}
	if e.query != nil && s.Len() <= maxSearchDoc {
		c := &e.srch
		if !c.allValid || c.q != e.query || c.version != s.Version() {
			if c.q != e.query || c.version != s.Version() {
				*c = searchCache{q: e.query, version: s.Version(), lo: -1}
			}
			c.all, c.allValid = e.query.FindAll(s, 5000), true
		}
		col := tokens.WithAlpha(th.Color.Warning, 0xC0)
		last := -1
		for _, m := range c.all {
			if y := ypos(m.Start); y != last {
				draw.Rect(ops, image.Rect(mx0, y, mx1, y+mh), col)
				last = y
			}
		}
	}
	for _, d := range e.diags {
		if d.Severity > SeverityWarning {
			continue
		}
		y := ypos(d.Start)
		draw.Rect(ops, image.Rect(mx0, y, mx1, y+mh), severityColor(th, d.Severity))
	}
	if len(sels) <= 1000 {
		for _, sel := range sels {
			y := ypos(sel.Head)
			draw.Rect(ops, image.Rect(mx0, y, mx1, y+max(1, mh/2)), th.Editor.Cursor)
		}
	}
}

// registerInput adds the input handler areas.
func (e *Editor) registerInput(gtx layout.Context) {
	g := &e.geo
	ops := gtx.Ops
	a := clip.Rect{Max: g.size}.Push(ops)
	event.Op(ops, e)
	text := clip.Rect(image.Rect(g.gutterW, 0, g.vbar.Min.X, g.size.Y)).Push(ops)
	pointer.CursorText.Add(ops)
	text.Pop()
	a.Pop()
}

// AnchorRect returns the rectangle of the character cell at document
// offset off in the editor's coordinate space (relative to the origin
// passed to the last Layout), e.g. to place completion and hover popups
// below it. ok is false if the offset is folded away or not visible.
func (e *Editor) AnchorRect(off int) (r image.Rectangle, ok bool) {
	return e.caretRect(cursor.Caret(off))
}

// caretRect is AnchorRect for the head of sel, honouring its row-end
// affinity (soft wrap).
func (e *Editor) caretRect(sel cursor.Selection) (r image.Rectangle, ok bool) {
	off := sel.Head
	g := &e.geo
	if !g.valid {
		return image.Rectangle{}, false
	}
	s := e.buf.Snapshot()
	pos := s.OffsetToPos(off)
	if e.folds.IsHidden(pos.Line) {
		return image.Rectangle{}, false
	}
	row, col := e.caretRowCol(s, sel)
	y := row*g.lineH - e.scroll.Y
	x := g.textX - e.scroll.X + e.met.colX(col)
	r = image.Rect(x, y, x+e.met.colX(1), y+g.lineH)
	visible := r.Max.Y > 0 && r.Min.Y < g.size.Y && r.Max.X > g.gutterW && r.Min.X < g.size.X
	return r, visible
}

// GutterIconRect returns the rectangle of the icon column (gutter
// markers such as run arrows and breakpoints) of 0-based document line
// in the editor's coordinate space of the last Layout; ok is false when
// the line is folded away or not visible.
func (e *Editor) GutterIconRect(line int) (r image.Rectangle, ok bool) {
	g := &e.geo
	if !g.valid || line < 0 || line >= e.buf.LineCount() || e.folds.IsHidden(line) {
		return image.Rectangle{}, false
	}
	y := e.lineRow(line)*g.lineH - e.scroll.Y
	r = image.Rect(g.iconX, y, g.iconX+g.iconW, y+g.lineH)
	return r, r.Max.Y > 0 && r.Min.Y < g.size.Y
}

// LineHeight returns the row height of the last Layout in pixels.
func (e *Editor) LineHeight() int { return e.geo.lineH }

// FirstVisibleLine returns the document line at the top of the view.
func (e *Editor) FirstVisibleLine() int {
	if e.geo.lineH == 0 {
		return 0
	}
	return e.rowLine(e.scroll.Y / e.geo.lineH)
}
