package mdview

import (
	"image"
	"image/color"
	"math"
	"strings"
	"unicode"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/image/math/fixed"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// shapeKey holds every input of text shaping. When it changes (other
// fonts, sizes, shaper or metric, reloaded images) all text is reshaped;
// a light/dark switch keeps it, because colors are applied while painting.
type shapeKey struct {
	sh             *text.Shaper
	ui, mono       font.Font
	body, code     unit.Sp
	pxPerSp, pxDp  float32
	imgGen         int
	hasImageLoader bool
}

// textStyle is the base style of a rich text (by block kind).
type textStyle struct {
	mono      bool    // monospace (code blocks, raw HTML)
	bold      bool    // headings, table header cells
	sizeMul   float32 // relative to the base size (reader body or code)
	lineScale float32 // line height as a multiple of ascent+descent
}

var (
	styleBody    = textStyle{sizeMul: 1, lineScale: 1.5}
	styleCell    = textStyle{sizeMul: 1, lineScale: 1.35}
	styleHeader  = textStyle{sizeMul: 1, lineScale: 1.35, bold: true}
	styleCode    = textStyle{mono: true, sizeMul: 1, lineScale: 1.4}
	headingMul   = [...]float32{2, 1.5, 1.25, 1, 0.9, 0.85}
	inlineCodeSz = float32(0.9)  // inline code relative to the text size
	chipSz       = float32(0.85) // image placeholder text relative to the text size
)

func headingStyle(level int) textStyle {
	level = min(max(level, 1), len(headingMul))
	return textStyle{bold: true, sizeMul: headingMul[level-1], lineScale: 1.25}
}

type runKind uint8

const (
	runText  runKind = iota
	runCode          // inline code: monospace on a subtle background
	runChip          // placeholder of an image that is not shown
	runImage         // image delivered by View.LoadImage
)

// link is a clickable link of a rich text (consecutive spans with the same
// destination form one link).
type link struct {
	dest string
	// rects are the hit areas of the last layout in rich-text coordinates
	// (one per line), each with its own click gesture: Gio delivers a
	// target's events to one area only.
	rects []image.Rectangle
	hits  []*gesture.Click
}

// hovered reports whether the pointer is over any part of the link.
func (l *link) hovered() bool {
	for _, h := range l.hits[:len(l.rects)] {
		if h.Hovered() {
			return true
		}
	}
	return false
}

// addRect adds a hit area, merging it with the previous one on the same
// line.
func (l *link) addRect(r image.Rectangle) {
	if n := len(l.rects); n > 0 && l.rects[n-1].Min.Y == r.Min.Y {
		l.rects[n-1] = l.rects[n-1].Union(r)
		return
	}
	l.rects = append(l.rects, r)
	for len(l.hits) < len(l.rects) {
		l.hits = append(l.hits, new(gesture.Click))
	}
}

// run is one shaped span.
type run struct {
	kind      runKind
	style     markdown.Style
	glyphs    []text.Glyph
	asc, desc fixed.Int26_6
	link      *link
	img       *loadedImage
	// natural image size in pixels (runImage).
	imgW, imgH int
}

// tok is an unbreakable piece of a run (a word with its trailing spaces),
// an image or a hard line break.
type tok struct {
	run        int
	g0, g1     int
	w, ink     fixed.Int26_6 // advance with / without trailing spaces, both incl. padding
	padL, padR fixed.Int26_6
	brk        bool
}

// piece is a placed token (or part of a token too long for a line).
type piece struct {
	tok         int
	g0, g1      int
	x, w        fixed.Int26_6
	first, last bool // piece starts / ends its token (padding applies)
	imgW, imgH  int  // runImage: scaled size
}

type line struct {
	top, h, base int // pixels; base is the baseline
	w            fixed.Int26_6
	pieces       []piece
}

// richText lays out a sequence of styled spans with word wrapping across
// span boundaries. Shaping is cached per shapeKey, wrapping per width.
type richText struct {
	spans []markdown.Span
	style textStyle

	key             shapeKey
	shaped          bool
	runs            []run
	toks            []tok
	links           []*link
	px              int // text size in pixels
	defAsc, defDesc fixed.Int26_6

	wrapW int
	lines []line
	size  image.Point
}

func newRich(spans []markdown.Span, st textStyle) *richText {
	return &richText{spans: spans, style: st, wrapW: -1}
}

// codeSpans turns code into one span per line (tabs as four spaces).
func codeSpans(code string) []markdown.Span {
	ls := strings.Split(strings.ReplaceAll(code, "\t", "    "), "\n")
	out := make([]markdown.Span, len(ls))
	for i, l := range ls {
		out[i] = markdown.Span{Text: strings.TrimRight(l, "\r"), Break: i < len(ls)-1}
	}
	return out
}

// ensure shapes the text if the shaping inputs changed.
func (rt *richText) ensure(gtx layout.Context, th *tokens.Theme, v *View) {
	if rt.shaped && rt.key == v.key {
		return
	}
	rt.key, rt.shaped = v.key, true
	rt.shape(gtx, th, v)
	rt.wrapW = -1
}

func (rt *richText) baseFont(gtx layout.Context, th *tokens.Theme) (font.Font, int) {
	f, size := th.Font.UI, readerSize(th)
	if rt.style.mono {
		f, size = th.Font.Mono, th.Text.Code
	}
	if rt.style.bold {
		f.Weight = font.Bold
	}
	return f, max(1, gtx.Sp(unit.Sp(float32(size)*rt.style.sizeMul)))
}

func (rt *richText) shape(gtx layout.Context, th *tokens.Theme, v *View) {
	rt.runs, rt.toks, rt.links = rt.runs[:0], rt.toks[:0], nil
	sh := th.Shaper
	base, px := rt.baseFont(gtx, th)
	rt.px = px
	rt.defAsc, rt.defDesc = fixed.I(px)*4/5, fixed.I(px)/5
	if gs := shapeString(sh, base, px, "x"); len(gs) > 0 {
		rt.defAsc, rt.defDesc = gs[0].Ascent, gs[0].Descent
	}
	var cur *link
	for _, sp := range rt.spans {
		if sp.Link != "" && !sp.Image {
			if cur == nil || cur.dest != sp.Link {
				cur = &link{dest: sp.Link}
				rt.links = append(rt.links, cur)
			}
		} else {
			cur = nil
		}
		switch {
		case sp.Image:
			rt.shapeImage(gtx, th, v, sp, px)
		case sp.Text != "":
			f, size, kind := base, px, runText
			if sp.Style&markdown.Code != 0 {
				f = th.Font.Mono
				f.Weight = base.Weight
				size, kind = max(1, int(math.Round(float64(float32(px)*inlineCodeSz)))), runCode
			}
			if sp.Style&markdown.Bold != 0 {
				f.Weight = font.Bold
			}
			if sp.Style&markdown.Italic != 0 {
				f.Style = font.Italic
			}
			s := strings.ReplaceAll(sp.Text, "\n", " ")
			r := run{kind: kind, style: sp.Style, link: cur, glyphs: shapeString(sh, f, size, s)}
			r.asc, r.desc = metrics(r.glyphs, rt.defAsc, rt.defDesc)
			var padL, padR fixed.Int26_6
			if kind == runCode {
				padL = fixed.I(gtx.Dp(th.Space.XS))
				padR = padL
			}
			rt.runs = append(rt.runs, r)
			rt.tokenize(len(rt.runs)-1, s, padL, padR, true)
		}
		if sp.Break {
			rt.toks = append(rt.toks, tok{run: -1, brk: true})
		}
	}
}

// shapeImage adds an image run (when the loader delivers it) or a
// placeholder chip with icon, alt text and source.
func (rt *richText) shapeImage(gtx layout.Context, th *tokens.Theme, v *View, sp markdown.Span, px int) {
	if img := v.image(sp.Link); img != nil {
		r := run{kind: runImage, img: img,
			imgW: max(1, gtx.Dp(unit.Dp(img.size.X))), imgH: max(1, gtx.Dp(unit.Dp(img.size.Y)))}
		rt.runs = append(rt.runs, r)
		rt.toks = append(rt.toks, tok{run: len(rt.runs) - 1})
		return
	}
	label := strings.TrimSpace(sp.Text)
	if label == "" {
		label = "image"
	}
	if sp.Link != "" {
		label += " · " + sp.Link
	}
	label = strings.ReplaceAll(label, "\n", " ")
	f := th.Font.UI
	size := max(1, int(math.Round(float64(float32(px)*chipSz))))
	r := run{kind: runChip, glyphs: shapeString(th.Shaper, f, size, label)}
	r.asc, r.desc = metrics(r.glyphs, rt.defAsc, rt.defDesc)
	// Room for the frame above and below the text.
	pad := fixed.I(gtx.Dp(th.Space.XXS))
	r.asc += pad
	r.desc += pad
	rt.runs = append(rt.runs, r)
	padL := fixed.I(gtx.Dp(th.Space.S) + gtx.Dp(th.Size.IconSmall) + gtx.Dp(th.Space.XS))
	rt.tokenize(len(rt.runs)-1, label, padL, fixed.I(gtx.Dp(th.Space.S)), false)
}

func shapeString(sh *text.Shaper, f font.Font, px int, s string) []text.Glyph {
	if s == "" || sh == nil {
		return nil
	}
	sh.LayoutString(text.Parameters{
		Font: f, PxPerEm: fixed.I(px), MaxWidth: 1 << 24, DisableSpaceTrim: true,
	}, s)
	var gs []text.Glyph
	for {
		g, ok := sh.NextGlyph()
		if !ok {
			break
		}
		gs = append(gs, g)
	}
	return gs
}

func metrics(gs []text.Glyph, defAsc, defDesc fixed.Int26_6) (asc, desc fixed.Int26_6) {
	if len(gs) == 0 {
		return defAsc, defDesc
	}
	for _, g := range gs {
		asc, desc = max(asc, g.Ascent), max(desc, g.Descent)
	}
	return asc, desc
}

// tokenize splits run ri into words (break opportunities after spaces)
// or, with words false, keeps it as one token.
func (rt *richText) tokenize(ri int, s string, padL, padR fixed.Int26_6, words bool) {
	gs := rt.runs[ri].glyphs
	if len(gs) == 0 {
		return
	}
	runes := []rune(s)
	start := len(rt.toks)
	t := tok{run: ri}
	pos := 0
	var adv, ink fixed.Int26_6
	for gi := range gs {
		adv += gs[gi].Advance
		if gs[gi].Flags&text.FlagClusterBreak == 0 {
			continue
		}
		pos += int(gs[gi].Runes)
		space := pos > 0 && pos <= len(runes) && unicode.IsSpace(runes[pos-1])
		if !space {
			ink = adv
		}
		if words && space && pos < len(runes) && !unicode.IsSpace(runes[pos]) {
			t.g1, t.w, t.ink = gi+1, adv, ink
			rt.toks = append(rt.toks, t)
			t = tok{run: ri, g0: gi + 1}
			adv, ink = 0, 0
		}
	}
	if t.g0 < len(gs) {
		t.g1, t.w, t.ink = len(gs), adv, ink
		rt.toks = append(rt.toks, t)
	}
	if start == len(rt.toks) {
		return
	}
	first, last := &rt.toks[start], &rt.toks[len(rt.toks)-1]
	first.padL = padL
	first.w += padL
	first.ink += padL
	last.padR = padR
	last.w += padR
	last.ink += padR
}

// natural returns the width of the longest unbreakable token and the
// width of the text without wrapping (pixels).
func (rt *richText) natural(gtx layout.Context, th *tokens.Theme, v *View) (minW, natW int) {
	rt.ensure(gtx, th, v)
	var lineW fixed.Int26_6
	flush := func() {
		natW = max(natW, lineW.Ceil())
		lineW = 0
	}
	for _, t := range rt.toks {
		if t.brk {
			flush()
			continue
		}
		if r := &rt.runs[t.run]; r.kind == runImage {
			minW = max(minW, min(r.imgW, gtx.Dp(unit.Dp(160))))
			lineW += fixed.I(r.imgW)
			continue
		}
		minW = max(minW, t.ink.Ceil())
		lineW += t.w
	}
	flush()
	return minW, natW
}

// measure shapes and wraps the text to width w and returns its size.
func (rt *richText) measure(gtx layout.Context, th *tokens.Theme, v *View, w int) image.Point {
	rt.ensure(gtx, th, v)
	rt.wrap(w)
	return rt.size
}

// wrap breaks the tokens into lines of at most w pixels.
func (rt *richText) wrap(w int) {
	w = max(w, 1)
	if rt.wrapW == w {
		return
	}
	rt.wrapW = w
	maxW := fixed.I(w)
	lines := rt.lines[:0]
	var cur []piece
	var x fixed.Int26_6
	flush := func() {
		var lw fixed.Int26_6
		if n := len(cur); n > 0 {
			p := cur[n-1]
			lw = p.x + p.w
			if p.last && p.tok >= 0 {
				t := rt.toks[p.tok]
				lw -= t.w - t.ink
			}
		}
		lines = append(lines, line{pieces: cur, w: lw})
		cur, x = nil, 0
	}
	for ti := range rt.toks {
		t := &rt.toks[ti]
		if t.brk {
			flush()
			continue
		}
		r := &rt.runs[t.run]
		if r.kind == runImage {
			iw := min(r.imgW, w)
			ih := max(1, int(int64(r.imgH)*int64(iw)/int64(r.imgW)))
			if x > 0 && x+fixed.I(iw) > maxW {
				flush()
			}
			cur = append(cur, piece{tok: ti, x: x, w: fixed.I(iw), first: true, last: true, imgW: iw, imgH: ih})
			x += fixed.I(iw)
			continue
		}
		if x > 0 && x+t.ink > maxW {
			flush()
		}
		if x+t.ink <= maxW {
			cur = append(cur, piece{tok: ti, g0: t.g0, g1: t.g1, x: x, w: t.w, first: true, last: true})
			x += t.w
			continue
		}
		// Longer than a whole line: break between clusters.
		for g := t.g0; g < t.g1; {
			var pw fixed.Int26_6
			if g == t.g0 {
				pw = t.padL
			}
			rest := t.padR - (t.w - t.ink) // the rest's ink: glyphs + padding - trailing spaces
			for i := g; i < t.g1; i++ {
				rest += r.glyphs[i].Advance
			}
			end := t.g1
			if pw+rest > maxW-x {
				end = fitClusters(r.glyphs, g, t.g1, maxW-x-pw)
			}
			for i := g; i < end; i++ {
				pw += r.glyphs[i].Advance
			}
			last := end == t.g1
			if last {
				pw += t.padR
			}
			cur = append(cur, piece{tok: ti, g0: g, g1: end, x: x, w: pw, first: g == t.g0, last: last})
			x += pw
			g = end
			if !last {
				flush()
			}
		}
	}
	if len(cur) > 0 || len(lines) == 0 {
		flush()
	}
	// Vertical metrics.
	leading := fixed.Int26_6(float32(rt.defAsc+rt.defDesc) * (rt.style.lineScale - 1))
	y := 0
	sz := image.Point{}
	for i := range lines {
		l := &lines[i]
		asc, desc := rt.defAsc, rt.defDesc
		if len(l.pieces) > 0 {
			asc, desc = 0, 0
		}
		for _, p := range l.pieces {
			r := &rt.runs[rt.toks[p.tok].run]
			if r.kind == runImage {
				asc = max(asc, fixed.I(p.imgH))
				continue
			}
			asc, desc = max(asc, r.asc), max(desc, r.desc)
		}
		l.top = y
		l.h = (asc + desc + leading).Ceil()
		l.base = y + (leading/2 + asc).Round()
		y += l.h
		sz.X = max(sz.X, l.w.Ceil())
	}
	sz.Y = y
	rt.lines, rt.size = lines, sz
}

// fitClusters returns the end of the longest glyph range [g0, end) ending
// at a cluster boundary whose advance fits avail; at least one cluster.
func fitClusters(gs []text.Glyph, g0, g1 int, avail fixed.Int26_6) int {
	var adv fixed.Int26_6
	end := -1
	for i := g0; i < g1; i++ {
		adv += gs[i].Advance
		if gs[i].Flags&text.FlagClusterBreak == 0 && i != g1-1 {
			continue
		}
		if adv > avail && end >= 0 {
			return end
		}
		end = i + 1
		if adv > avail {
			return end
		}
	}
	if end < 0 {
		return g1
	}
	return end
}

// firstBaseline returns the baseline of the first line after wrapping.
func (rt *richText) firstBaseline() int {
	if len(rt.lines) == 0 {
		return 0
	}
	return rt.lines[0].base
}

// layout shapes, wraps to w and paints the text in col with the given
// alignment within w. Links are registered with the view. The returned
// width is w; Baseline is that of the first line.
func (rt *richText) layout(gtx layout.Context, th *tokens.Theme, v *View, w int, col color.NRGBA, align text.Alignment) layout.Dimensions {
	sz := rt.measure(gtx, th, v, w)
	for _, l := range rt.links {
		l.rects = l.rects[:0]
	}
	sh := th.Shaper
	for li := range rt.lines {
		l := &rt.lines[li]
		var dx fixed.Int26_6
		switch align {
		case text.Middle:
			dx = (fixed.I(w) - l.w) / 2
		case text.End:
			dx = fixed.I(w) - l.w
		}
		dx = max(dx, 0)
		for _, p := range l.pieces {
			t := &rt.toks[p.tok]
			r := &rt.runs[t.run]
			x := dx + p.x
			if r.kind == runImage {
				pt := image.Pt(x.Round(), l.base-p.imgH)
				tr := op.Offset(pt).Push(gtx.Ops)
				ig := gtx
				ig.Constraints = layout.Exact(image.Pt(p.imgW, p.imgH))
				widget.Image{Src: r.img.op, Fit: widget.Fill}.Layout(ig)
				tr.Pop()
				continue
			}
			rt.paintPiece(gtx, th, sh, l, p, t, r, x, col)
			if r.link != nil {
				inkEnd := x + p.w
				if p.last {
					inkEnd -= t.w - t.ink
				}
				r.link.addRect(image.Rect(x.Floor(), l.top, inkEnd.Ceil(), l.top+l.h))
			}
		}
	}
	for _, l := range rt.links {
		for i, rc := range l.rects {
			a := clip.Rect(rc).Push(gtx.Ops)
			pointer.CursorPointer.Add(gtx.Ops)
			l.hits[i].Add(gtx.Ops)
			a.Pop()
		}
		if len(l.rects) > 0 {
			v.hotLinks = append(v.hotLinks, l)
		}
	}
	return layout.Dimensions{Size: image.Pt(max(w, sz.X), sz.Y), Baseline: sz.Y - rt.firstBaseline()}
}

func (rt *richText) paintPiece(gtx layout.Context, th *tokens.Theme, sh *text.Shaper, l *line, p piece, t *tok, r *run, x fixed.Int26_6, col color.NRGBA) {
	gx := x
	if p.first {
		gx += t.padL
	}
	inkEnd := x + p.w
	if p.last {
		inkEnd -= t.w - t.ink
	}
	switch r.kind {
	case runCode:
		bg := image.Rect(x.Floor(), l.base-r.asc.Ceil(), inkEnd.Ceil(), l.base+r.desc.Ceil())
		draw.RRect(gtx.Ops, bg, gtx.Dp(th.Radius.S), th.Color.SurfaceAlt)
	case runChip:
		bg := image.Rect(x.Floor(), l.base-r.asc.Ceil(), inkEnd.Ceil(), l.base+r.desc.Ceil())
		rad := gtx.Dp(th.Radius.S)
		draw.RRect(gtx.Ops, bg, rad, th.Color.SurfaceAlt)
		draw.Border(gtx.Ops, bg, rad, max(1, gtx.Dp(th.Size.BorderWidth)), th.Color.Border)
		col = th.Color.TextMuted
		if p.first {
			isz := gtx.Dp(th.Size.IconSmall)
			pt := image.Pt(x.Round()+gtx.Dp(th.Space.S), (bg.Min.Y+bg.Max.Y-isz)/2)
			tr := op.Offset(pt).Push(gtx.Ops)
			atoms.Icon{Glyph: atoms.GlyphImage, Size: th.Size.IconSmall, Color: col}.Layout(gtx, th)
			tr.Pop()
		}
	}
	if r.link != nil {
		col = th.Color.Accent
	}
	drawGlyphs(gtx, sh, r.glyphs[p.g0:p.g1], gx, l.base, col)
	thick := max(1, gtx.Dp(th.Size.BorderWidth))
	textEnd := inkEnd
	if p.last {
		textEnd -= t.padR
	}
	if r.style&markdown.Strike != 0 {
		y := l.base - int(float32(r.asc.Round())*0.3)
		draw.Rect(gtx.Ops, image.Rect(gx.Floor(), y, textEnd.Ceil(), y+thick), col)
	}
	if r.link != nil && r.link.hovered() {
		y := l.base + max(1, r.desc.Round()/2)
		draw.Rect(gtx.Ops, image.Rect(gx.Floor(), y, textEnd.Ceil(), y+thick), col)
	}
}

// drawGlyphs paints a run of glyphs of one line with the dot of the first
// glyph at (x, baseline).
func drawGlyphs(gtx layout.Context, sh *text.Shaper, gs []text.Glyph, x fixed.Int26_6, baseline int, col color.NRGBA) {
	if len(gs) == 0 || sh == nil {
		return
	}
	t := op.Affine(f32.AffineId().Offset(f32.Pt(float32(x)/64, float32(baseline)))).Push(gtx.Ops)
	outline := clip.Outline{Path: sh.Shape(gs)}.Op().Push(gtx.Ops)
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	outline.Pop()
	if call := sh.Bitmaps(gs); call != (op.CallOp{}) {
		call.Add(gtx.Ops)
	}
	t.Pop()
}

// readerSize is the body text size of the reader (a step larger than UI
// text, for long-form reading).
func readerSize(th *tokens.Theme) unit.Sp { return th.Text.Large }
