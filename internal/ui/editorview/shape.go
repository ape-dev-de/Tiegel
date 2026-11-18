package editorview

import (
	"bytes"
	"image/color"
	"math"
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"github.com/rivo/uniseg"
	"golang.org/x/image/math/fixed"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// metricsKey identifies the font setup the metrics were measured for.
type metricsKey struct {
	shaper     *text.Shaper
	font       font.Font
	ppem       fixed.Int26_6
	lineHeight float32
}

// metrics are the monospace cell measurements of the editor font.
type metrics struct {
	key     metricsKey
	valid   bool
	cellW   fixed.Int26_6 // advance of one column
	ascent  fixed.Int26_6
	descent fixed.Int26_6
	lineH   int // row height in pixels
	base    int // baseline offset from the row top
	digits  [10]text.Glyph
}

// defaultLineHeight is the row height relative to the font's
// ascent+descent when the theme leaves Text.CodeLineHeight at zero.
const defaultLineHeight = 1.4

// lineHeightOf returns the editor line height multiplier of th.
func lineHeightOf(th *tokens.Theme) float32 {
	if lh := th.Text.CodeLineHeight; lh > 0 {
		return lh
	}
	return defaultLineHeight
}

// updateMetrics measures the editor font. The cell width is the advance
// of '0' in the first family of the editor font chain; glyphs of fallback
// fonts (CJK, emoji, …) are placed on this grid by paintGlyphs.
func (e *Editor) updateMetrics(gtx layout.Context, th *tokens.Theme) {
	key := metricsKey{shaper: th.Shaper, font: th.Font.Mono, ppem: fixed.I(gtx.Sp(th.Text.Code)), lineHeight: lineHeightOf(th)}
	if e.met.valid && e.met.key == key {
		return
	}
	m := metrics{key: key, valid: true}
	th.Shaper.LayoutString(text.Parameters{Font: key.font, PxPerEm: key.ppem, MaxWidth: 1 << 20, Locale: gtx.Locale}, "0123456789")
	i := 0
	for g, ok := th.Shaper.NextGlyph(); ok; g, ok = th.Shaper.NextGlyph() {
		if i < len(m.digits) {
			m.digits[i] = g
		}
		i++
	}
	m.cellW, m.ascent, m.descent = m.digits[0].Advance, m.digits[0].Ascent, m.digits[0].Descent
	if m.cellW <= 0 {
		m.cellW = key.ppem * 6 / 10
	}
	if m.ascent+m.descent <= 0 {
		m.ascent, m.descent = key.ppem*8/10, key.ppem*2/10
	}
	h := (m.ascent + m.descent).Ceil()
	m.lineH = max(1, int(math.Ceil(float64(h)*float64(key.lineHeight))))
	m.base = (m.lineH-h)/2 + m.ascent.Ceil()
	e.met = m
}

// colX returns the x offset of visual column col.
func (m *metrics) colX(col int) int { return (fixed.Int26_6(col) * m.cellW).Round() }

// lineEntry caches the drawing of one line (or of a horizontal segment of
// a very long line). It is valid while text, spans, theme and metrics are
// unchanged.
type lineEntry struct {
	text  []byte
	from  int // byte offset of text within the line
	col0  int // visual column of from
	spans []highlight.Span
	th    *tokens.Theme
	key   metricsKey
	tab   int
	ws    int // whitespace marks from this byte offset (-1: none)
	ins   []lineInlay
	cols  int // visual width of the whole line
	used  uint64
	ops   op.Ops
	call  op.CallOp
}

// lineKey identifies a cached row: a document line and, with soft wrap,
// the row index within it.
type lineKey struct{ line, sub int }

// lineCache maps document lines to cached drawings. Entries move with
// line insertions and deletions so scrolling and typing only re-shape
// lines whose content changed.
type lineCache struct {
	m     map[lineKey]*lineEntry
	free  []*lineEntry
	frame uint64
}

func (c *lineCache) init() { c.m = make(map[lineKey]*lineEntry) }

// applyEdit drops entries of edited lines and shifts the ones below.
func (c *lineCache) applyEdit(ed buffer.Edit) {
	start, oldEnd, newEnd := ed.StartPoint.Row, ed.OldEndPoint.Row, ed.NewEndPoint.Row
	delta := newEnd - oldEnd
	if delta == 0 {
		for k, ent := range c.m {
			if k.line >= start && k.line <= oldEnd && k.sub > 0 {
				delete(c.m, k) // wrapped rows of an edited line
				c.recycle(ent)
			}
		}
		for l := start; l <= oldEnd; l++ {
			c.drop(lineKey{line: l})
		}
		return
	}
	next := make(map[lineKey]*lineEntry, len(c.m))
	for k, ent := range c.m {
		switch {
		case k.line < start:
			next[k] = ent
		case k.line > oldEnd:
			next[lineKey{k.line + delta, k.sub}] = ent
		default:
			c.recycle(ent)
		}
	}
	c.m = next
}

func (c *lineCache) drop(k lineKey) {
	if ent, ok := c.m[k]; ok {
		delete(c.m, k)
		c.recycle(ent)
	}
}

func (c *lineCache) recycle(ent *lineEntry) {
	if len(c.free) < 64 {
		ent.text = ent.text[:0]
		ent.spans = ent.spans[:0]
		ent.th = nil
		c.free = append(c.free, ent)
	}
}

// get returns the entry for line, creating an empty one if needed.
func (c *lineCache) get(l lineKey) *lineEntry {
	ent, ok := c.m[l]
	if !ok {
		if n := len(c.free); n > 0 {
			ent = c.free[n-1]
			c.free = c.free[:n-1]
		} else {
			ent = new(lineEntry)
		}
		c.m[l] = ent
	}
	ent.used = c.frame
	return ent
}

// sweep evicts entries not used during the last few frames.
func (c *lineCache) sweep() {
	const keep = 3
	if len(c.m) < 256 {
		return
	}
	for l, ent := range c.m {
		if c.frame-ent.used > keep {
			delete(c.m, l)
			c.recycle(ent)
		}
	}
}

// Len returns the number of cached lines.
func (c *lineCache) Len() int { return len(c.m) }

// maxShapeBytes is the line length above which only the visible segment
// of a line is shaped.
const maxShapeBytes = 2048

// lineDrawing returns the cached drawing of the visible part of a line
// (or of one soft-wrapped row). line is the full row content, spans are
// row-relative; whitespace from byte ws on is marked (-1: none). The
// drawing has column 0 at x = 0 and the row top at y = 0.
func (e *Editor) lineDrawing(th *tokens.Theme, l lineKey, line []byte, spans []highlight.Span, firstCol, lastCol, ws int, ins []lineInlay) (op.CallOp, int) {
	from, to := 0, len(line)
	if len(line) > maxShapeBytes {
		from = buffer.ByteColFromVisual(line, max(0, firstCol-8), e.tabWidth())
		to = buffer.ByteColFromVisual(line, lastCol+8, e.tabWidth())
		to = min(len(line), to+nextClusterLen(line[to:]))
	}
	ent := e.cache.get(l)
	if ent.th == th && ent.key == e.met.key && ent.tab == e.tabWidth() && ent.from == from && ent.ws == ws &&
		bytes.Equal(ent.text, line[from:to]) && spansEqual(ent.spans, spans) && inlayEqual(ent.ins, ins) {
		return ent.call, ent.cols
	}
	ent.text = append(ent.text[:0], line[from:to]...)
	ent.spans = append(ent.spans[:0], spans...)
	ent.ins = append(ent.ins[:0], ins...)
	ent.th, ent.key, ent.tab, ent.from, ent.ws = th, e.met.key, e.tabWidth(), from, ws
	ent.col0 = 0
	if from > 0 {
		ent.col0 = buffer.VisualCol(line, from, e.tabWidth())
	}
	ent.cols = buffer.VisualCol(line, len(line), e.tabWidth()) + inlayCols(ins)
	ent.ops.Reset()
	m := op.Record(&ent.ops)
	if ws >= 0 && ws < to {
		// Marks of the shaped part; columns count from the line start.
		e.paintWhitespace(&ent.ops, th.Editor.Whitespace, line[:to], max(ws, from), 0, 0, ins)
	}
	e.paintInlays(&ent.ops, th, line, ins)
	e.paintGlyphs(&ent.ops, th, ent.text, from, ent.col0, spans, ins, color.NRGBA{})
	ent.call = m.Stop()
	return ent.call, ent.cols
}

func nextClusterLen(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	c, _, _, _ := uniseg.FirstGraphemeCluster(b, -1)
	return len(c)
}

func spansEqual(a, b []highlight.Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// cluster is a grapheme cluster of a shaped segment.
type cluster struct {
	b   int // byte offset within the segment
	col int // visual column (absolute within the line)
}

// paintGlyphs shapes seg (which starts at byte from and visual column
// col0 of its line) and paints it on a monospace grid: every grapheme
// cluster starts at its column, so tabs, wide characters and combining
// marks line up exactly with the cursor model's visual columns.
//
// Clusters move right by the columns of the inlay hints ins before them;
// a non-zero fixed color replaces the span colors (hint labels).
func (e *Editor) paintGlyphs(ops *op.Ops, th *tokens.Theme, seg []byte, from, col0 int, spans []highlight.Span, ins []lineInlay, over color.NRGBA) {
	if len(seg) == 0 {
		return
	}
	tab := e.tabWidth()
	clusters := e.shp.clusters[:0]
	runeToCluster := e.shp.runes[:0]
	shapeText := e.shp.text[:0]
	col, state := col0, -1
	rest := seg
	b := 0
	for len(rest) > 0 {
		var c []byte
		var w int
		c, rest, w, state = uniseg.FirstGraphemeCluster(rest, state)
		ci := int32(len(clusters))
		clusters = append(clusters, cluster{b: b, col: col + inlayShift(ins, from+b)})
		for i := 0; i < len(c); {
			_, n := utf8.DecodeRune(c[i:])
			runeToCluster = append(runeToCluster, ci)
			i += n
		}
		if len(c) == 1 && c[0] == '\t' {
			col = (col/tab + 1) * tab
		} else {
			col += w
		}
		for _, x := range c {
			if x < 0x20 || x == 0x7f {
				x = ' '
			}
			shapeText = append(shapeText, x)
		}
		b += len(c)
	}
	e.shp.clusters, e.shp.runes, e.shp.text = clusters, runeToCluster, shapeText

	sh := th.Shaper
	sh.LayoutString(text.Parameters{
		Font: e.met.key.font, PxPerEm: e.met.key.ppem, MaxWidth: math.MaxInt32 / 4,
		DisableSpaceTrim: true,
	}, string(shapeText))

	run := e.shp.glyphs[:0]
	runColor := color.NRGBA{}
	flush := func() {
		if len(run) > 0 {
			paintRun(ops, sh, run, runColor, e.met.base)
			run = run[:0]
		}
	}
	runeIdx, lastCI := 0, int32(-1)
	var firstX fixed.Int26_6
	si := 0
	for g, ok := sh.NextGlyph(); ok; g, ok = sh.NextGlyph() {
		ci := runeToCluster[min(runeIdx, len(runeToCluster)-1)]
		if ci != lastCI {
			firstX, lastCI = g.X, ci
		}
		cl := clusters[ci]
		// Every cluster starts at its grid column, whatever font the
		// shaper picked for it; only offsets within a cluster (combining
		// marks) come from the shaped advances. A glyph from a fallback
		// font with a different advance therefore never shifts the
		// columns after it.
		g.X = fixed.Int26_6(cl.col)*e.met.cellW + (g.X - firstX)
		g.Y = 0
		if e.shp.trace != nil {
			e.shp.trace(g, from+cl.b)
		}
		if g.Flags&text.FlagClusterBreak != 0 {
			runeIdx += int(g.Runes)
		}
		// Spans are sorted; advance to the one covering this cluster.
		off := from + cl.b
		for si < len(spans) && spans[si].End <= off {
			si++
		}
		role := highlight.Plain
		if si < len(spans) && spans[si].Start <= off {
			role = spans[si].Role
		}
		c := th.SyntaxColor(tokens.SyntaxRole(role))
		if over.A != 0 {
			c = over
		}
		if c != runColor || len(run) >= 256 {
			flush()
			runColor = c
		}
		run = append(run, g)
	}
	flush()
	e.shp.glyphs = run
}

// paintRun paints glyphs (one color) with the baseline at y = base.
func paintRun(ops *op.Ops, sh *text.Shaper, run []text.Glyph, c color.NRGBA, base int) {
	x0 := float32(run[0].X) / 64
	t := op.Affine(f32.AffineId().Offset(f32.Pt(x0, float32(base)))).Push(ops)
	path := sh.Shape(run)
	o := clip.Outline{Path: path}.Op().Push(ops)
	paint.ColorOp{Color: c}.Add(ops)
	paint.PaintOp{}.Add(ops)
	o.Pop()
	if call := sh.Bitmaps(run); call != (op.CallOp{}) {
		call.Add(ops)
	}
	t.Pop()
}

// shapeScratch holds reusable buffers for paintGlyphs.
type shapeScratch struct {
	clusters []cluster
	runes    []int32
	text     []byte
	glyphs   []text.Glyph
	// trace, if set (tests only), receives every placed glyph with the
	// line byte offset of its cluster.
	trace func(g text.Glyph, off int)
}

// paintNumber paints the decimal number n right-aligned at x = right with
// the shaped digit glyphs of the metrics.
func (e *Editor) paintNumber(ops *op.Ops, sh *text.Shaper, n, right, y int, c color.NRGBA) {
	var digits [20]byte
	i := len(digits)
	for {
		i--
		digits[i] = byte(n % 10)
		n /= 10
		if n == 0 || i == 0 {
			break
		}
	}
	ds := digits[i:]
	run := e.shp.glyphs[:0]
	for k, d := range ds {
		g := e.met.digits[d]
		g.X = fixed.Int26_6(k) * e.met.cellW
		g.Y = 0
		run = append(run, g)
	}
	t := op.Offset(imagePt(right-e.met.colX(len(ds)), y)).Push(ops)
	paintRun(ops, sh, run, c, e.met.base)
	t.Pop()
	e.shp.glyphs = run
}
