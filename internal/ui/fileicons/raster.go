package fileicons

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/vector"
)

// grid is the size of the design grid. All icons are drawn in 16×16
// units and scaled to the requested pixel size; at 16 px (and 32 px)
// shapes on integer units land exactly on pixel edges.
const grid = 16

// pt is a point in design units.
type pt struct{ x, y float32 }

// drawer rasterizes icon shapes into a premultiplied RGBA image. Shapes
// are added to the current path, then painted (or erased) as one layer;
// overlapping shapes of one layer union, holes are added with the
// opposite orientation.
type drawer struct {
	img  *image.RGBA
	mask *image.Alpha
	z    *vector.Rasterizer
	px   int
	s    float32 // pixels per unit
}

func newDrawer(px int) *drawer {
	d := &drawer{
		img:  image.NewRGBA(image.Rect(0, 0, px, px)),
		mask: image.NewAlpha(image.Rect(0, 0, px, px)),
		z:    vector.NewRasterizer(px, px),
		px:   px,
		s:    float32(px) / grid,
	}
	d.z.DrawOp = draw.Src
	return d
}

// segs returns the number of segments for a curve of radius r units.
func (d *drawer) segs(r float32) int {
	n := int(r * d.s * 2)
	if n < 8 {
		n = 8
	}
	if n > 64 {
		n = 64
	}
	return n
}

// poly adds a closed polygon. Fills wind one way, holes the other.
func (d *drawer) poly(ps []pt, hole bool) {
	if len(ps) < 3 {
		return
	}
	var a float32
	for i := range ps {
		j := (i + 1) % len(ps)
		a += ps[i].x*ps[j].y - ps[j].x*ps[i].y
	}
	rev := (a < 0) != hole
	at := func(i int) pt {
		if rev {
			return ps[len(ps)-1-i]
		}
		return ps[i]
	}
	p := at(0)
	d.z.MoveTo(p.x*d.s, p.y*d.s)
	for i := 1; i < len(ps); i++ {
		p = at(i)
		d.z.LineTo(p.x*d.s, p.y*d.s)
	}
	d.z.ClosePath()
}

// rect adds an axis-aligned rectangle.
func (d *drawer) rect(x0, y0, x1, y1 float32) {
	d.poly([]pt{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}, false)
}

// rrectPts returns the outline of a rounded rectangle with the corner
// radii top-left, top-right, bottom-right, bottom-left.
func (d *drawer) rrectPts(x0, y0, x1, y1 float32, r [4]float32) []pt {
	var ps []pt
	corner := func(cx, cy, rad, a0 float32) {
		if rad <= 0 {
			ps = append(ps, pt{cx, cy})
			return
		}
		n := d.segs(rad) / 4
		if n < 2 {
			n = 2
		}
		for i := 0; i <= n; i++ {
			a := float64(a0) + float64(i)/float64(n)*math.Pi/2
			ps = append(ps, pt{cx + rad*float32(math.Cos(a)), cy + rad*float32(math.Sin(a))})
		}
	}
	corner(x0+r[0], y0+r[0], r[0], math.Pi)
	if r[0] <= 0 {
		ps[len(ps)-1] = pt{x0, y0}
	}
	corner(x1-r[1], y0+r[1], r[1], 1.5*math.Pi)
	if r[1] <= 0 {
		ps[len(ps)-1] = pt{x1, y0}
	}
	corner(x1-r[2], y1-r[2], r[2], 0)
	if r[2] <= 0 {
		ps[len(ps)-1] = pt{x1, y1}
	}
	corner(x0+r[3], y1-r[3], r[3], 0.5*math.Pi)
	if r[3] <= 0 {
		ps[len(ps)-1] = pt{x0, y1}
	}
	return ps
}

// rrect adds a rounded rectangle with one radius.
func (d *drawer) rrect(x0, y0, x1, y1, r float32) {
	d.poly(d.rrectPts(x0, y0, x1, y1, [4]float32{r, r, r, r}), false)
}

// rrectRing adds the outline of a rounded rectangle, w units thick,
// inside the given bounds.
func (d *drawer) rrectRing(x0, y0, x1, y1, r, w float32) {
	d.poly(d.rrectPts(x0, y0, x1, y1, [4]float32{r, r, r, r}), false)
	ir := r - w
	if ir < 0 {
		ir = 0
	}
	d.poly(d.rrectPts(x0+w, y0+w, x1-w, y1-w, [4]float32{ir, ir, ir, ir}), true)
}

// arcPts samples an elliptical arc from angle a0 to a1 (radians, y down).
func (d *drawer) arcPts(cx, cy, rx, ry, a0, a1 float32) []pt {
	r := rx
	if ry > r {
		r = ry
	}
	n := int(float32(d.segs(r)) * float32(math.Abs(float64(a1-a0))) / (2 * math.Pi))
	if n < 3 {
		n = 3
	}
	ps := make([]pt, 0, n+1)
	for i := 0; i <= n; i++ {
		a := float64(a0) + float64(a1-a0)*float64(i)/float64(n)
		ps = append(ps, pt{cx + rx*float32(math.Cos(a)), cy + ry*float32(math.Sin(a))})
	}
	return ps
}

// circlePts returns a closed circle outline.
func (d *drawer) circlePts(cx, cy, r float32) []pt {
	ps := d.arcPts(cx, cy, r, r, 0, 2*math.Pi)
	return ps[:len(ps)-1]
}

// circle adds a filled circle.
func (d *drawer) circle(cx, cy, r float32) {
	d.poly(d.circlePts(cx, cy, r), false)
}

// ring adds a circle outline of width w centered on radius r.
func (d *drawer) ring(cx, cy, r, w float32) {
	d.poly(d.circlePts(cx, cy, r+w/2), false)
	d.poly(d.circlePts(cx, cy, r-w/2), true)
}

// ellipseRing adds an elliptical outline of width w.
func (d *drawer) ellipseRing(cx, cy, rx, ry, w float32) {
	o := d.arcPts(cx, cy, rx+w/2, ry+w/2, 0, 2*math.Pi)
	i := d.arcPts(cx, cy, rx-w/2, ry-w/2, 0, 2*math.Pi)
	d.poly(o[:len(o)-1], false)
	d.poly(i[:len(i)-1], true)
}

// stroke adds a polyline of width w with round joins and caps. With
// closed, the last point connects to the first.
func (d *drawer) stroke(w float32, closed bool, ps ...pt) {
	h := w / 2
	seg := func(a, b pt) {
		dx, dy := b.x-a.x, b.y-a.y
		l := float32(math.Hypot(float64(dx), float64(dy)))
		if l == 0 {
			return
		}
		nx, ny := -dy/l*h, dx/l*h
		d.poly([]pt{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}}, false)
	}
	for i := 0; i+1 < len(ps); i++ {
		seg(ps[i], ps[i+1])
	}
	if closed && len(ps) > 2 {
		seg(ps[len(ps)-1], ps[0])
	}
	for _, p := range ps {
		d.poly(d.circlePts(p.x, p.y, h), false)
	}
}

// line adds a straight stroke with square caps along an axis or round
// caps otherwise; it is a shorthand for stroke with two points.
func (d *drawer) line(w float32, x0, y0, x1, y1 float32) {
	d.stroke(w, false, pt{x0, y0}, pt{x1, y1})
}

// quadPts samples a quadratic Bézier curve.
func quadPts(a, c, b pt, n int) []pt {
	ps := make([]pt, 0, n+1)
	for i := 0; i <= n; i++ {
		t := float32(i) / float32(n)
		u := 1 - t
		ps = append(ps, pt{u*u*a.x + 2*u*t*c.x + t*t*b.x, u*u*a.y + 2*u*t*c.y + t*t*b.y})
	}
	return ps
}

// rasterize turns the current path into the mask and starts a new path.
func (d *drawer) rasterize() {
	d.z.Draw(d.mask, d.mask.Bounds(), image.Opaque, image.Point{})
	d.z.Reset(d.px, d.px)
	d.z.DrawOp = draw.Src
}

// fill paints the current path in c over the image.
func (d *drawer) fill(c color.NRGBA) {
	d.rasterize()
	ca := uint32(c.A)
	r, g, b := uint32(c.R)*ca/255, uint32(c.G)*ca/255, uint32(c.B)*ca/255
	for i, m := range d.mask.Pix {
		if m == 0 {
			continue
		}
		mm := uint32(m)
		sa := ca * mm / 255
		inv := 255 - sa
		p := d.img.Pix[i*4 : i*4+4 : i*4+4]
		p[0] = uint8(r*mm/255 + uint32(p[0])*inv/255)
		p[1] = uint8(g*mm/255 + uint32(p[1])*inv/255)
		p[2] = uint8(b*mm/255 + uint32(p[2])*inv/255)
		p[3] = uint8(sa + uint32(p[3])*inv/255)
	}
}

// erase clears the current path from the image (makes it transparent).
func (d *drawer) erase() {
	d.rasterize()
	for i, m := range d.mask.Pix {
		if m == 0 {
			continue
		}
		inv := 255 - uint32(m)
		p := d.img.Pix[i*4 : i*4+4 : i*4+4]
		for k := range p {
			p[k] = uint8(uint32(p[k]) * inv / 255)
		}
	}
}

// fade multiplies the whole image by a/255 (for disabled contexts).
func fade(img *image.RGBA, a uint8) {
	for i, v := range img.Pix {
		img.Pix[i] = uint8(uint32(v) * uint32(a) / 255)
	}
}
