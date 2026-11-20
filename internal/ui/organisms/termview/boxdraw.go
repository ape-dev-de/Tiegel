package termview

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Box drawing, block elements and powerline separators are drawn as
// geometry filling the whole cell, so that lines and prompt segments
// join without gaps whatever the font and the line height.

// boxArms are the arms (up, right, down, left) of U+2500–U+257F: 0 none,
// 1 light, 2 heavy, 3 double; '-' marks characters drawn otherwise
// (dashes, arcs, diagonals).
var boxArms = [128]string{
	"0101", "0202", "1010", "2020", "-", "-", "-", "-", "-", "-", "-", "-", // 2500–250B
	"0110", "0210", "0120", "0220", "0011", "0012", "0021", "0022", // 250C–2513
	"1100", "1200", "2100", "2200", "1001", "1002", "2001", "2002", // 2514–251B
	"1110", "1210", "2110", "1120", "2120", "2210", "1220", "2220", // 251C–2523
	"1011", "1012", "2011", "1021", "2021", "2012", "1022", "2022", // 2524–252B
	"0111", "0112", "0211", "0212", "0121", "0122", "0221", "0222", // 252C–2533
	"1101", "1102", "1201", "1202", "2101", "2102", "2201", "2202", // 2534–253B
	"1111", "1112", "1211", "1212", "2111", "1121", "2121", "2112", // 253C–2543
	"2211", "1122", "1221", "2212", "1222", "2122", "2221", "2222", // 2544–254B
	"-", "-", "-", "-", // 254C–254F dashes
	"0303", "3030", "0310", "0130", "0330", "0013", "0031", "0033", // 2550–2557
	"1300", "3100", "3300", "1003", "3001", "3003", // 2558–255D
	"1310", "3130", "3330", "1013", "3031", "3033", // 255E–2563
	"0313", "0131", "0333", "1303", "3101", "3303", // 2564–2569
	"1313", "3131", "3333", // 256A–256C
	"-", "-", "-", "-", "-", "-", "-", // 256D–2573 arcs, diagonals
	"0001", "1000", "0100", "0010", "0002", "2000", "0200", "0020", // 2574–257B
	"0201", "1020", "0102", "2010", // 257C–257F
}

// isGeometric reports whether r is drawn by drawGlyph.
func isGeometric(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x259f:
		return true
	case r >= 0xe0b0 && r <= 0xe0b7, r == 0xe0b8, r == 0xe0ba, r == 0xe0bc, r == 0xe0be:
		return true
	}
	return false
}

// cellBox is the pixel rectangle of a cell and the line widths.
type cellBox struct {
	r          image.Rectangle
	light, gap int
}

func (b cellBox) midX() int { return b.r.Min.X + (b.r.Dx()-b.light)/2 }
func (b cellBox) midY() int { return b.r.Min.Y + (b.r.Dy()-b.light)/2 }

// drawGlyph draws a box drawing, block or powerline character into the
// cell r with color c.
func drawGlyph(ops *op.Ops, r image.Rectangle, ru rune, c color.NRGBA) {
	lw := max(1, int(math.Round(float64(r.Dx())/8)))
	b := cellBox{r: r, light: lw, gap: max(1, lw)}
	switch {
	case ru >= 0x2500 && ru <= 0x257f:
		arms := boxArms[ru-0x2500]
		if arms != "-" {
			drawArms(ops, b, arms, c)
			return
		}
		drawBoxSpecial(ops, b, ru, c)
	case ru >= 0x2580 && ru <= 0x259f:
		drawBlock(ops, r, ru, c)
	default:
		drawPowerline(ops, r, ru, c, lw)
	}
}

// drawArms draws the arms of a line character.
func drawArms(ops *op.Ops, b cellBox, arms string, c color.NRGBA) {
	r := b.r
	mx, my := b.midX(), b.midY()
	for i := 0; i < 4; i++ {
		w := arms[i] - '0'
		if w == 0 {
			continue
		}
		thick := b.light
		if w == 2 {
			thick = b.light * 2
		}
		// Offsets of the strokes: one centered, or two for double.
		offs := []int{-(thick - b.light) / 2}
		if w == 3 {
			thick = b.light
			offs = []int{-b.gap - b.light/2 - b.light/2, b.gap + b.light/2 + b.light/2}
		}
		for _, o := range offs {
			switch i {
			case 0: // up: from the top to the center (and over it)
				x := mx + o
				draw.Rect(ops, image.Rect(x, r.Min.Y, x+thick, my+b.light+centerExt(w, b)), c)
			case 2: // down
				x := mx + o
				draw.Rect(ops, image.Rect(x, my-centerExt(w, b), x+thick, r.Max.Y), c)
			case 1: // right
				y := my + o
				draw.Rect(ops, image.Rect(mx-centerExt(w, b), y, r.Max.X, y+thick), c)
			case 3: // left
				y := my + o
				draw.Rect(ops, image.Rect(r.Min.X, y, mx+b.light+centerExt(w, b), y+thick), c)
			}
		}
	}
}

// centerExt is how far an arm reaches over the center so that corners
// close (heavy and double arms are wider).
func centerExt(w byte, b cellBox) int {
	switch w {
	case 2:
		return b.light / 2
	case 3:
		return b.gap + b.light
	}
	return 0
}

// drawBoxSpecial draws dashed lines, rounded corners and diagonals.
func drawBoxSpecial(ops *op.Ops, b cellBox, ru rune, c color.NRGBA) {
	r := b.r
	mx, my := b.midX(), b.midY()
	dashed := func(horizontal bool, n int, heavy bool) {
		thick := b.light
		if heavy {
			thick *= 2
		}
		length := r.Dx()
		if !horizontal {
			length = r.Dy()
		}
		seg := float64(length) / float64(n)
		for i := 0; i < n; i++ {
			a := int(math.Round(float64(i) * seg))
			e := int(math.Round(float64(i)*seg + seg*0.6))
			if horizontal {
				draw.Rect(ops, image.Rect(r.Min.X+a, my, r.Min.X+e, my+thick), c)
			} else {
				draw.Rect(ops, image.Rect(mx, r.Min.Y+a, mx+thick, r.Min.Y+e), c)
			}
		}
	}
	switch ru {
	case 0x2504, 0x2505:
		dashed(true, 3, ru == 0x2505)
	case 0x2506, 0x2507:
		dashed(false, 3, ru == 0x2507)
	case 0x2508, 0x2509:
		dashed(true, 4, ru == 0x2509)
	case 0x250a, 0x250b:
		dashed(false, 4, ru == 0x250b)
	case 0x254c, 0x254d:
		dashed(true, 2, ru == 0x254d)
	case 0x254e, 0x254f:
		dashed(false, 2, ru == 0x254f)
	case 0x256d, 0x256e, 0x256f, 0x2570:
		drawArc(ops, b, ru, c)
	case 0x2571, 0x2572, 0x2573:
		var p clip.Path
		p.Begin(ops)
		if ru != 0x2572 {
			p.MoveTo(f32.Pt(float32(r.Max.X), float32(r.Min.Y)))
			p.LineTo(f32.Pt(float32(r.Min.X), float32(r.Max.Y)))
		}
		if ru != 0x2571 {
			p.MoveTo(f32.Pt(float32(r.Min.X), float32(r.Min.Y)))
			p.LineTo(f32.Pt(float32(r.Max.X), float32(r.Max.Y)))
		}
		paint.FillShape(ops, c, clip.Stroke{Path: p.End(), Width: float32(b.light)}.Op())
	}
}

// drawArc draws a rounded corner (╭ ╮ ╯ ╰): a line from one cell edge
// to the center, a quarter circle, and on to the other edge.
func drawArc(ops *op.Ops, b cellBox, ru rune, c color.NRGBA) {
	r := b.r
	lw := float32(b.light)
	cx := float32(b.midX()) + lw/2
	cy := float32(b.midY()) + lw/2
	rad := float32(min(r.Dx(), r.Dy())) / 2
	var hx, vy float32 // the horizontal arm's edge x, the vertical arm's edge y
	var sx, sy float32 // directions of the arms
	switch ru {
	case 0x256d: // down and right
		hx, vy, sx, sy = float32(r.Max.X), float32(r.Max.Y), 1, 1
	case 0x256e: // down and left
		hx, vy, sx, sy = float32(r.Min.X), float32(r.Max.Y), -1, 1
	case 0x256f: // up and left
		hx, vy, sx, sy = float32(r.Min.X), float32(r.Min.Y), -1, -1
	default: // up and right
		hx, vy, sx, sy = float32(r.Max.X), float32(r.Min.Y), 1, -1
	}
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(f32.Pt(hx, cy))
	p.LineTo(f32.Pt(cx+sx*rad, cy))
	// Quarter circle as a cubic Bézier (k = 0.5523).
	const k = 0.5523
	p.CubeTo(f32.Pt(cx+sx*rad*(1-k), cy), f32.Pt(cx, cy+sy*rad*(1-k)), f32.Pt(cx, cy+sy*rad))
	p.LineTo(f32.Pt(cx, vy))
	paint.FillShape(ops, c, clip.Stroke{Path: p.End(), Width: lw}.Op())
}

// drawBlock draws a block element (U+2580–U+259F).
func drawBlock(ops *op.Ops, r image.Rectangle, ru rune, c color.NRGBA) {
	w, h := r.Dx(), r.Dy()
	frac := func(n, total, eighths int) int {
		return int(math.Round(float64(total) * float64(eighths) / float64(n)))
	}
	rect := func(x0, y0, x1, y1 int) {
		draw.Rect(ops, image.Rect(r.Min.X+x0, r.Min.Y+y0, r.Min.X+x1, r.Min.Y+y1), c)
	}
	hw, hh := w/2, h/2
	switch {
	case ru == 0x2580: // upper half
		rect(0, 0, w, hh)
	case ru >= 0x2581 && ru <= 0x2588: // lower 1/8 … full
		n := int(ru - 0x2580)
		rect(0, h-frac(8, h, n), w, h)
	case ru >= 0x2589 && ru <= 0x258f: // left 7/8 … 1/8
		n := int(0x2590 - ru)
		rect(0, 0, frac(8, w, n), h)
	case ru == 0x2590: // right half
		rect(hw, 0, w, h)
	case ru >= 0x2591 && ru <= 0x2593: // shades
		alpha := []uint8{0x40, 0x80, 0xc0}[ru-0x2591]
		draw.Rect(ops, r, tokens.WithAlpha(c, uint8(uint16(c.A)*uint16(alpha)/0xff)))
	case ru == 0x2594: // upper 1/8
		rect(0, 0, w, frac(8, h, 1))
	case ru == 0x2595: // right 1/8
		rect(w-frac(8, w, 1), 0, w, h)
	default: // quadrants 2596–259F: bits upper-left, upper-right, lower-left, lower-right
		q := map[rune]int{0x2596: 4, 0x2597: 8, 0x2598: 1, 0x2599: 1 | 4 | 8, 0x259a: 1 | 8, 0x259b: 1 | 2 | 4,
			0x259c: 1 | 2 | 8, 0x259d: 2, 0x259e: 2 | 4, 0x259f: 2 | 4 | 8}[ru]
		if q&1 != 0 {
			rect(0, 0, hw, hh)
		}
		if q&2 != 0 {
			rect(hw, 0, w, hh)
		}
		if q&4 != 0 {
			rect(0, hh, hw, h)
		}
		if q&8 != 0 {
			rect(hw, hh, w, h)
		}
	}
}

// drawPowerline draws the powerline separators (U+E0B0–E0B7) and the
// slanted ones (E0B8, E0BA, E0BC, E0BE) over the full cell height.
func drawPowerline(ops *op.Ops, r image.Rectangle, ru rune, c color.NRGBA, lw int) {
	x0, y0, x1, y1 := float32(r.Min.X), float32(r.Min.Y), float32(r.Max.X), float32(r.Max.Y)
	ym := (y0 + y1) / 2
	var p clip.Path
	p.Begin(ops)
	fill := true
	const k = 0.5523
	switch ru {
	case 0xe0b0: // solid right-pointing triangle
		p.MoveTo(f32.Pt(x0, y0))
		p.LineTo(f32.Pt(x1, ym))
		p.LineTo(f32.Pt(x0, y1))
	case 0xe0b2: // solid left-pointing triangle
		p.MoveTo(f32.Pt(x1, y0))
		p.LineTo(f32.Pt(x0, ym))
		p.LineTo(f32.Pt(x1, y1))
	case 0xe0b1: // thin right arrow
		fill = false
		p.MoveTo(f32.Pt(x0, y0))
		p.LineTo(f32.Pt(x1, ym))
		p.LineTo(f32.Pt(x0, y1))
	case 0xe0b3: // thin left arrow
		fill = false
		p.MoveTo(f32.Pt(x1, y0))
		p.LineTo(f32.Pt(x0, ym))
		p.LineTo(f32.Pt(x1, y1))
	case 0xe0b4, 0xe0b5: // right half circle
		fill = ru == 0xe0b4
		rx, ry := x1-x0, (y1-y0)/2
		p.MoveTo(f32.Pt(x0, y0))
		p.CubeTo(f32.Pt(x0+rx*k, y0), f32.Pt(x1, ym-ry*k), f32.Pt(x1, ym))
		p.CubeTo(f32.Pt(x1, ym+ry*k), f32.Pt(x0+rx*k, y1), f32.Pt(x0, y1))
	case 0xe0b6, 0xe0b7: // left half circle
		fill = ru == 0xe0b6
		rx, ry := x1-x0, (y1-y0)/2
		p.MoveTo(f32.Pt(x1, y0))
		p.CubeTo(f32.Pt(x1-rx*k, y0), f32.Pt(x0, ym-ry*k), f32.Pt(x0, ym))
		p.CubeTo(f32.Pt(x0, ym+ry*k), f32.Pt(x1-rx*k, y1), f32.Pt(x1, y1))
	case 0xe0b8: // lower left triangle
		p.MoveTo(f32.Pt(x0, y0))
		p.LineTo(f32.Pt(x1, y1))
		p.LineTo(f32.Pt(x0, y1))
	case 0xe0ba: // lower right triangle
		p.MoveTo(f32.Pt(x1, y0))
		p.LineTo(f32.Pt(x1, y1))
		p.LineTo(f32.Pt(x0, y1))
	case 0xe0bc: // upper left triangle
		p.MoveTo(f32.Pt(x0, y0))
		p.LineTo(f32.Pt(x1, y0))
		p.LineTo(f32.Pt(x0, y1))
	default: // 0xe0be upper right triangle
		p.MoveTo(f32.Pt(x0, y0))
		p.LineTo(f32.Pt(x1, y0))
		p.LineTo(f32.Pt(x1, y1))
	}
	if fill {
		p.Close()
		paint.FillShape(ops, c, clip.Outline{Path: p.End()}.Op())
		return
	}
	paint.FillShape(ops, c, clip.Stroke{Path: p.End(), Width: float32(lw)}.Op())
}
