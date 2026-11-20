package termview

import (
	"image"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"github.com/rivo/uniseg"
	"golang.org/x/image/math/fixed"

	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// blinkPeriod is the time the cursor is on (and off) while blinking;
// bellFlash is how long the visual bell shows.
const (
	blinkPeriod = 530 * time.Millisecond
	bellFlash   = 150 * time.Millisecond
)

// metrics are the cell measurements of the font.
type metrics struct {
	key   metricsKey
	cellW fixed.Int26_6
	lineH int
	textH int
	// base is the baseline of the text inside a row.
	base int
	// lw is the width of decoration lines.
	lw int
}

type metricsKey struct {
	shaper *text.Shaper
	font   font.Font
	ppem   fixed.Int26_6
	lh     float32
}

// rowCache holds the recorded operations of one line: the backgrounds
// and the text with its decorations, so that selection and find
// highlights can go between them. Lines are keyed by their stable index
// (absolute index plus dropped scrollback lines), so output scrolling
// through the view reuses the rows that moved.
type rowCache struct {
	ops    op.Ops
	bg, fg op.CallOp
	valid  bool
	gen    uint64
	spans  []terminal.Span
	links  []terminal.Link
	hover  int
	// screen and index short-cut the comparison while the snapshot is
	// the same.
	screen *terminal.Screen
	index  int
	used   bool
}

// renderCache invalidates the rows when the look changes.
type renderCache struct {
	lines   map[int]*rowCache
	free    []*rowCache
	gen     uint64
	th      *tokens.Theme
	palette tokens.TerminalPalette
	met     metricsKey
	reverse bool
	glyphs  []text.Glyph
	// buffers of drawText
	run             []byte
	runeCols, runeW []int
	// Recorded rows and shaped runs of the last frame (tests).
	recorded int
}

// prepare bumps the generation when theme, palette, font or reverse
// video changed.
func (rc *renderCache) prepare(th *tokens.Theme, m metricsKey, reverse bool) {
	if rc.th != th || rc.palette != th.Terminal || rc.met != m || rc.reverse != reverse {
		rc.th, rc.palette, rc.met, rc.reverse = th, th.Terminal, m, reverse
		rc.gen++
	}
	rc.recorded = 0
	for _, c := range rc.lines {
		c.used = false
	}
}

// get returns the cache entry of a stable line index.
func (rc *renderCache) get(key int) *rowCache {
	if rc.lines == nil {
		rc.lines = map[int]*rowCache{}
	}
	c := rc.lines[key]
	if c == nil {
		if n := len(rc.free); n > 0 {
			c, rc.free = rc.free[n-1], rc.free[:n-1]
		} else {
			c = new(rowCache)
		}
		c.valid = false
		rc.lines[key] = c
	}
	c.used = true
	return c
}

// sweep drops the entries of lines no longer visible (their buffers
// are reused).
func (rc *renderCache) sweep() {
	for k, c := range rc.lines {
		if !c.used {
			delete(rc.lines, k)
			c.spans, c.links, c.screen = nil, nil, nil
			rc.free = append(rc.free, c)
		}
	}
}

func (v *View) updateMetrics(gtx layout.Context, th *tokens.Theme) {
	size := v.FontSize
	if size <= 0 {
		size = th.Text.Code
	}
	lh := v.LineHeight
	if lh <= 0 {
		lh = th.Text.CodeLineHeight
	}
	lh = max(1, min(3, lh))
	k := metricsKey{shaper: th.Shaper, font: th.Font.Mono, ppem: fixed.I(gtx.Sp(size)), lh: lh}
	if v.met.key == k && v.met.lineH > 0 {
		return
	}
	m := metrics{key: k}
	th.Shaper.LayoutString(text.Parameters{Font: k.font, PxPerEm: k.ppem, MaxWidth: 1 << 20, Locale: gtx.Locale}, "0")
	var asc, desc fixed.Int26_6
	for g, ok := th.Shaper.NextGlyph(); ok; g, ok = th.Shaper.NextGlyph() {
		if m.cellW == 0 {
			m.cellW, asc, desc = g.Advance, g.Ascent, g.Descent
		}
	}
	if m.cellW <= 0 {
		m.cellW = k.ppem * 6 / 10
	}
	if asc+desc <= 0 {
		asc, desc = k.ppem*8/10, k.ppem*2/10
	}
	m.textH = (asc + desc).Ceil()
	m.lineH = max(1, int(math.Ceil(float64(m.textH)*float64(lh))))
	m.base = (m.lineH-m.textH)/2 + asc.Round()
	m.lw = max(1, int(math.Round(float64(k.ppem.Round())/14)))
	v.met = m
}

func (m metrics) colX(col int) int { return (fixed.Int26_6(col) * m.cellW).Round() }

// Layout draws the terminal filling the constraints.
func (v *View) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.updateMetrics(gtx, th)
	size := gtx.Constraints.Max
	pad := gtx.Dp(th.Space.XS)
	v.origin = image.Pt(pad, pad)
	cols := max(1, (size.X-2*pad)*64/max(1, int(v.met.cellW)))
	rows := max(1, (size.Y-2*pad)/v.met.lineH)
	if cols != v.cols || rows != v.rows {
		v.cols, v.rows = cols, rows
		v.emit(ResizeEvent{Cols: cols, Rows: rows})
	}
	sc := v.Screen
	reverse := sc != nil && sc.Modes.ReverseVideo
	tp := th.Terminal
	bg := tp.Background
	if reverse {
		bg = tp.Foreground
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.FillShape(gtx.Ops, bg, clip.Rect{Max: size}.Op())
	event.Op(gtx.Ops, v)
	switch {
	case v.hover != nil && (v.hover.URL == "" || v.shortcut):
		pointer.CursorPointer.Add(gtx.Ops)
	case sc != nil && v.reporting(0):
		pointer.CursorDefault.Add(gtx.Ops)
	default:
		pointer.CursorText.Add(gtx.Ops)
	}
	if sc == nil {
		return layout.Dimensions{Size: size}
	}
	if !v.follow && v.top >= sc.History {
		v.follow = true
	}
	v.rc.prepare(th, v.met.key, reverse)
	defer v.rc.sweep()
	top := v.Top()
	lineH := v.met.lineH
	// Backgrounds, then highlights, then text.
	for row := 0; row < rows; row++ {
		c := v.row(th, row, top+row)
		if c == nil {
			continue
		}
		t := op.Offset(image.Pt(pad, pad+row*lineH)).Push(gtx.Ops)
		c.bg.Add(gtx.Ops)
		t.Pop()
	}
	v.drawHighlights(gtx, th, top, rows, pad)
	for row := 0; row < rows; row++ {
		c := v.row(th, row, top+row)
		if c == nil {
			continue
		}
		t := op.Offset(image.Pt(pad, pad+row*lineH)).Push(gtx.Ops)
		c.fg.Add(gtx.Ops)
		t.Pop()
	}
	v.drawCursor(gtx, th, top, rows, pad)
	v.drawBell(gtx, th, size)
	if v.Notice != "" && sc.Total-top <= rows {
		y := pad + (sc.Total-top)*lineH
		if y+lineH <= size.Y {
			t := op.Offset(image.Pt(pad, y+(lineH-v.met.textH)/2)).Push(gtx.Ops)
			draw.Text(gtx, th.Shaper, draw.TextOpts{Font: th.Font.UI, Size: th.Text.Small, Color: th.Color.TextMuted, MaxLines: 1}, v.Notice)
			t.Pop()
		}
	}
	return layout.Dimensions{Size: size}
}

// row returns the cache of visible row i showing absolute line idx,
// recording it if its content changed; nil if the snapshot has no such
// line.
func (v *View) row(th *tokens.Theme, _, idx int) *rowCache {
	sc := v.Screen
	j := idx - sc.Top
	if j < 0 || j >= len(sc.Lines) {
		return nil
	}
	c := v.rc.get(idx + sc.Dropped)
	l := &sc.Lines[j]
	hover := -1
	if v.hover != nil && v.hoverAt.Line == idx {
		for _, lk := range l.Links {
			if v.hoverAt.Col >= lk.Col && v.hoverAt.Col < lk.EndCol && (lk.URL == "" || v.shortcut) {
				hover = lk.Col
			}
		}
	}
	if c.valid && c.gen == v.rc.gen && c.hover == hover {
		if c.screen == sc && c.index == j {
			return c
		}
		if slices.Equal(c.spans, l.Spans) && slices.Equal(c.links, l.Links) {
			c.screen, c.index = sc, j
			return c
		}
	}
	v.record(th, c, l, hover)
	c.valid, c.gen, c.hover, c.spans, c.links, c.screen, c.index = true, v.rc.gen, hover, l.Spans, l.Links, sc, j
	v.rc.recorded++
	return c
}

// colors resolves the colors of a span.
func colors(th *tokens.Theme, s terminal.Span, reverse bool) (fg, bg color.NRGBA, bgSet bool) {
	tp := th.Terminal
	defFG, defBG := tp.Foreground, tp.Background
	if reverse {
		defFG, defBG = defBG, defFG
	}
	resolve := func(c string, def color.NRGBA, bold bool) (color.NRGBA, bool) {
		i, r, g, b, ok := terminal.ParseColor(c)
		switch {
		case !ok:
			return def, false
		case i >= 0:
			if bold && i < 8 {
				i += 8 // bold brightens the normal colors
			}
			return th.TerminalColor(uint8(i)), true
		}
		return color.NRGBA{R: r, G: g, B: b, A: 0xff}, true
	}
	fg, _ = resolve(s.FG, defFG, s.Bold)
	bg, bgSet = resolve(s.BG, defBG, false)
	if s.Inverse {
		fg, bg, bgSet = bg, fg, true
	}
	if s.Faint {
		fg = tokens.Mix(fg, bg, 0.4)
	}
	return fg, bg, bgSet
}

// record draws line l into the cache c (row origin at 0,0).
func (v *View) record(th *tokens.Theme, c *rowCache, l *terminal.ScreenLine, hover int) {
	c.ops.Reset()
	ops := &c.ops
	m := v.met
	reverse := v.rc.reverse
	mac := op.Record(ops)
	for _, s := range l.Spans {
		if _, bg, bgSet := colors(th, s, reverse); bgSet {
			draw.Rect(ops, image.Rect(m.colX(s.Col), 0, m.colX(s.Col+s.Width), m.lineH), bg)
		}
	}
	c.bg = mac.Stop()
	mac = op.Record(ops)
	for _, s := range l.Spans {
		fg, _, _ := colors(th, s, reverse)
		if !s.Hidden {
			v.drawText(ops, th, s, fg)
		}
		v.drawDecorations(ops, th, s, fg, reverse)
	}
	for i := range l.Links {
		lk := &l.Links[i]
		x0, x1 := m.colX(lk.Col), m.colX(lk.EndCol)
		y := min(m.lineH-m.lw, m.base+max(m.lw, (m.lineH-m.base)/3))
		col := tokens.WithAlpha(th.Terminal.Link, 0x90)
		if lk.Col == hover {
			col = th.Terminal.Link
			draw.Rect(ops, image.Rect(x0, y, x1, y+m.lw), col)
			continue
		}
		if lk.URL != "" {
			dotted(ops, x0, x1, y, m.lw, col)
			continue
		}
		draw.Rect(ops, image.Rect(x0, y, x1, y+m.lw), col)
	}
	c.fg = mac.Stop()
}

// dotted draws a dotted line.
func dotted(ops *op.Ops, x0, x1, y, lw int, c color.NRGBA) {
	for x := x0; x < x1; x += 2 * lw {
		draw.Rect(ops, image.Rect(x, y, min(x1, x+lw), y+lw), c)
	}
}

// drawDecorations draws underline (all styles), strikethrough and
// overline of a span.
func (v *View) drawDecorations(ops *op.Ops, th *tokens.Theme, s terminal.Span, fg color.NRGBA, reverse bool) {
	m := v.met
	x0, x1 := m.colX(s.Col), m.colX(s.Col+s.Width)
	lw := m.lw
	if s.Underline {
		uc := fg
		if s.UL != "" {
			uc, _ = resolveColor(th, s.UL, fg)
		}
		y := min(m.lineH-lw, m.base+max(lw, (m.lineH-m.base)/3))
		switch s.UnderlineStyle {
		case 2: // double
			y2 := max(m.base+1, y-2*lw)
			draw.Rect(ops, image.Rect(x0, y2, x1, y2+lw), uc)
			draw.Rect(ops, image.Rect(x0, y, x1, y+lw), uc)
		case 3: // curly
			amp := float32(max(lw, 2))
			var p clip.Path
			p.Begin(ops)
			yc := float32(y) - amp/2
			p.MoveTo(f32.Pt(float32(x0), yc))
			step := float32(max(2, m.colX(1)/2))
			for x, up := float32(x0), true; x < float32(x1); x, up = x+step, !up {
				dy := amp
				if up {
					dy = -amp
				}
				p.QuadTo(f32.Pt(x+step/2, yc+dy), f32.Pt(min(x+step, float32(x1)), yc))
			}
			paint.FillShape(ops, uc, clip.Stroke{Path: p.End(), Width: float32(lw)}.Op())
		case 4: // dotted
			dotted(ops, x0, x1, y, lw, uc)
		case 5: // dashed
			dash := max(2, m.colX(1)/2)
			for x := x0; x < x1; x += 2 * dash {
				draw.Rect(ops, image.Rect(x, y, min(x1, x+dash), y+lw), uc)
			}
		default:
			draw.Rect(ops, image.Rect(x0, y, x1, y+lw), uc)
		}
	}
	if s.Strike {
		y := m.base - m.textH*3/10
		draw.Rect(ops, image.Rect(x0, y, x1, y+lw), fg)
	}
	if s.Overline {
		y := (m.lineH - m.textH) / 2
		draw.Rect(ops, image.Rect(x0, y, x1, y+lw), fg)
	}
}

// resolveColor resolves a span color string.
func resolveColor(th *tokens.Theme, c string, def color.NRGBA) (color.NRGBA, bool) {
	i, r, g, b, ok := terminal.ParseColor(c)
	switch {
	case !ok:
		return def, false
	case i >= 0:
		return th.TerminalColor(uint8(i)), true
	}
	return color.NRGBA{R: r, G: g, B: b, A: 0xff}, true
}

// drawText draws the text of a span on the grid: geometric characters
// as shapes, the rest as shaped runs (split at spaces) whose glyph
// clusters are moved to their cells.
func (v *View) drawText(ops *op.Ops, th *tokens.Theme, s terminal.Span, fg color.NRGBA) {
	if strings.TrimSpace(s.Text) == "" {
		return
	}
	m := v.met
	f := th.Font.Mono
	if s.Bold {
		f.Weight = font.Bold
	}
	if s.Italic {
		f.Style = font.Italic
	}
	rc := &v.rc
	if isPlainASCII(s.Text) {
		// One cell per byte: shape the span at once.
		rc.runeCols, rc.runeW = rc.runeCols[:0], rc.runeW[:0]
		for i := range len(s.Text) {
			rc.runeCols = append(rc.runeCols, s.Col+i)
			rc.runeW = append(rc.runeW, 1)
		}
		v.shapeRun(ops, th, f, s.Text, rc.runeCols, rc.runeW, fg)
		return
	}
	wide := s.Width == 2 && uniseg.GraphemeClusterCount(s.Text) == 1
	run := rc.run[:0]
	runeCols, runeW := rc.runeCols[:0], rc.runeW[:0]
	flush := func() {
		if len(run) > 0 {
			v.shapeRun(ops, th, f, string(run), runeCols, runeW, fg)
		}
		run = run[:0]
		runeCols, runeW = runeCols[:0], runeW[:0]
	}
	defer func() { rc.run, rc.runeCols, rc.runeW = run[:0], runeCols[:0], runeW[:0] }()
	col := s.Col
	rest, state := s.Text, -1
	for rest != "" {
		var g string
		g, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		w := 1
		if wide {
			w = 2
		}
		r0, size := utf8.DecodeRuneInString(g)
		switch {
		case size == len(g) && isGeometric(r0):
			flush()
			drawGlyph(ops, image.Rect(m.colX(col), 0, m.colX(col+w), m.lineH), r0, fg)
		case g == " ":
			flush()
		default:
			for range utf8.RuneCountInString(g) {
				runeCols = append(runeCols, col)
				runeW = append(runeW, w)
			}
			run = append(run, g...)
		}
		col += w
	}
	flush()
}

// isPlainASCII reports whether s has only printable ASCII without
// characters drawn as geometry.
func isPlainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] >= 0x7f {
			return false
		}
	}
	return true
}

// shapeRun shapes text and paints its glyphs at the columns of their
// clusters (runeCols holds the column of every rune, runeW its width).
func (v *View) shapeRun(ops *op.Ops, th *tokens.Theme, f font.Font, s string, runeCols, runeW []int, fg color.NRGBA) {
	m := v.met
	sh := th.Shaper
	sh.LayoutString(text.Parameters{Font: f, PxPerEm: m.key.ppem, MaxWidth: 1 << 24, MaxLines: 1}, s)
	gs := v.rc.glyphs[:0]
	runeIdx, start := 0, 0
	for g, ok := sh.NextGlyph(); ok; g, ok = sh.NextGlyph() {
		gs = append(gs, g)
		if g.Flags&text.FlagClusterBreak == 0 {
			continue
		}
		cl := gs[start:]
		if runeIdx < len(runeCols) && len(cl) > 0 {
			col, w := runeCols[runeIdx], runeW[runeIdx]
			var adv fixed.Int26_6
			for _, x := range cl {
				adv += x.Advance
			}
			target := fixed.I(m.colX(col))
			if cells := fixed.Int26_6(w) * m.cellW; w > 1 && adv < cells {
				target += (cells - adv) / 2
			}
			x0 := cl[0].X
			for i := range cl {
				cl[i].X = target + cl[i].X - x0
				cl[i].Y = int32(m.base)
			}
		}
		runeIdx += int(g.Runes)
		start = len(gs)
	}
	v.rc.glyphs = gs[:0]
	if len(gs) == 0 {
		return
	}
	first := gs[0]
	t := op.Affine(f32.AffineId().Offset(f32.Pt(float32(first.X)/64, float32(first.Y)))).Push(ops)
	outline := clip.Outline{Path: sh.Shape(gs)}.Op().Push(ops)
	paint.ColorOp{Color: fg}.Add(ops)
	paint.PaintOp{}.Add(ops)
	outline.Pop()
	if call := sh.Bitmaps(gs); call != (op.CallOp{}) {
		call.Add(ops)
	}
	t.Pop()
}

// drawHighlights draws the selection and the find matches behind the
// text.
func (v *View) drawHighlights(gtx layout.Context, th *tokens.Theme, top, rows, pad int) {
	m := v.met
	cols := v.Screen.Cols
	span := func(from, to Pos, c color.NRGBA) {
		for idx := max(from.Line, top); idx <= min(to.Line, top+rows-1); idx++ {
			c0, c1 := 0, cols
			if idx == from.Line {
				c0 = from.Col
			}
			if idx == to.Line {
				c1 = to.Col
			}
			if c1 > c0 {
				y := pad + (idx-top)*m.lineH
				draw.Rect(gtx.Ops, image.Rect(pad+m.colX(c0), y, pad+m.colX(c1), y+m.lineH), c)
			}
		}
	}
	match := tokens.WithAlpha(th.Color.Warning, 0x60)
	cur := tokens.WithAlpha(th.Color.Warning, 0xc0)
	for i, mt := range v.Matches {
		if mt.EndLine < top || mt.Line >= top+rows {
			continue
		}
		c := match
		if i == v.Current {
			c = cur
		}
		span(Pos{mt.Line, mt.Col}, Pos{mt.EndLine, mt.EndCol}, c)
	}
	if v.HasSelection() {
		from, to := v.Selection()
		span(from, to, th.Terminal.Selection)
	}
}

// drawCursor draws the cursor (only when its row is visible and the
// program runs): a block with the character in the background color, an
// underline or a bar (DECSCUSR), an outline without focus; blinking when
// the program asks for it.
func (v *View) drawCursor(gtx layout.Context, th *tokens.Theme, top, rows, pad int) {
	sc := v.Screen
	cur := sc.Cursor
	if !sc.Running || !cur.Visible || sc.History+cur.Row < top || sc.History+cur.Row >= top+rows {
		return
	}
	m := v.met
	if v.focused && cur.Blink {
		if v.blinkStart.IsZero() {
			v.blinkStart = gtx.Now
		}
		phase := gtx.Now.Sub(v.blinkStart)
		n := phase / blinkPeriod
		gtx.Execute(op.InvalidateCmd{At: v.blinkStart.Add((n + 1) * blinkPeriod)})
		if n%2 == 1 {
			return
		}
	}
	tp := th.Terminal
	y := pad + (sc.History+cur.Row-top)*m.lineH
	x := pad + m.colX(cur.Col)
	line, _ := v.line(sc.History + cur.Row)
	cell := cursorCell(line, cur.Col)
	w := max(1, cell.Width)
	r := image.Rect(x, y, pad+m.colX(cur.Col+w), y+m.lineH)
	bw := max(1, gtx.Dp(1))
	switch {
	case !v.focused:
		draw.Border(gtx.Ops, r, 0, bw, tp.Cursor)
	case cur.Shape >= 5:
		draw.Rect(gtx.Ops, image.Rect(x, y, x+max(1, gtx.Dp(2)), y+m.lineH), tp.Cursor)
	case cur.Shape == 3 || cur.Shape == 4:
		draw.Rect(gtx.Ops, image.Rect(x, y+m.lineH-max(1, gtx.Dp(2)), r.Max.X, y+m.lineH), tp.Cursor)
	default:
		draw.Rect(gtx.Ops, r, tp.Cursor)
		if strings.TrimSpace(cell.Text) != "" && !cell.Hidden {
			_, bg, _ := colors(th, cell, v.rc.reverse)
			t := op.Offset(image.Pt(pad, y)).Push(gtx.Ops)
			v.drawText(gtx.Ops, th, cell, bg)
			t.Pop()
		}
	}
}

// cursorCell returns the character under the cursor as a span of one
// cell (Width 0 when the cell is empty).
func cursorCell(l terminal.ScreenLine, col int) terminal.Span {
	for _, s := range l.Spans {
		if col < s.Col || col >= s.Col+s.Width {
			continue
		}
		c := s
		if s.Width == 2 && uniseg.GraphemeClusterCount(s.Text) == 1 {
			return c
		}
		rest, state, i := s.Text, -1, s.Col
		for rest != "" {
			var g string
			g, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
			if i == col {
				c.Col, c.Width, c.Text = col, 1, g
				return c
			}
			i++
		}
	}
	return terminal.Span{Col: col}
}

// drawBell flashes the view when the program rang the bell.
func (v *View) drawBell(gtx layout.Context, th *tokens.Theme, size image.Point) {
	sc := v.Screen
	switch {
	case !v.bellInit:
		v.bellInit, v.bellSeen = true, sc.Bells
	case sc.Bells > v.bellSeen:
		v.bellSeen = sc.Bells
		v.bellUntil = gtx.Now.Add(bellFlash)
	case sc.Bells < v.bellSeen:
		v.bellSeen = sc.Bells
	}
	if gtx.Now.Before(v.bellUntil) {
		draw.Rect(gtx.Ops, image.Rectangle{Max: size}, tokens.WithAlpha(th.Terminal.Foreground, 0x18))
		gtx.Execute(op.InvalidateCmd{At: v.bellUntil})
	}
}
