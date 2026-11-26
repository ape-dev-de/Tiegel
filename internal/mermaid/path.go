package mermaid

import "math"

// path is a path builder.
type path []Seg

func (p *path) move(x, y float64) { *p = append(*p, Seg{Op: Move, Pts: []Point{{x, y}}}) }
func (p *path) line(x, y float64) { *p = append(*p, Seg{Op: Line, Pts: []Point{{x, y}}}) }
func (p *path) quad(cx, cy, x, y float64) {
	*p = append(*p, Seg{Op: Quad, Pts: []Point{{cx, cy}, {x, y}}})
}
func (p *path) cube(c1, c2, to Point) { *p = append(*p, Seg{Op: Cube, Pts: []Point{c1, c2, to}}) }
func (p *path) close()                { *p = append(*p, Seg{Op: Close}) }

// kappa is the cubic Bézier control distance of a quarter circle.
const kappa = 0.5522847498

// rectPath is a rectangle with corner radius r.
func rectPath(b Rect, r float64) path {
	r = min(r, b.W/2, b.H/2)
	var p path
	x0, y0, x1, y1 := b.X, b.Y, b.X+b.W, b.Y+b.H
	if r <= 0 {
		p.move(x0, y0)
		p.line(x1, y0)
		p.line(x1, y1)
		p.line(x0, y1)
		p.close()
		return p
	}
	k := r * (1 - kappa)
	p.move(x0+r, y0)
	p.line(x1-r, y0)
	p.cube(Point{x1 - k, y0}, Point{x1, y0 + k}, Point{x1, y0 + r})
	p.line(x1, y1-r)
	p.cube(Point{x1, y1 - k}, Point{x1 - k, y1}, Point{x1 - r, y1})
	p.line(x0+r, y1)
	p.cube(Point{x0 + k, y1}, Point{x0, y1 - k}, Point{x0, y1 - r})
	p.line(x0, y0+r)
	p.cube(Point{x0, y0 + k}, Point{x0 + k, y0}, Point{x0 + r, y0})
	p.close()
	return p
}

// ellipsePath is an ellipse centered at c with radii rx, ry.
func ellipsePath(c Point, rx, ry float64) path {
	var p path
	kx, ky := rx*kappa, ry*kappa
	p.move(c.X+rx, c.Y)
	p.cube(Point{c.X + rx, c.Y + ky}, Point{c.X + kx, c.Y + ry}, Point{c.X, c.Y + ry})
	p.cube(Point{c.X - kx, c.Y + ry}, Point{c.X - rx, c.Y + ky}, Point{c.X - rx, c.Y})
	p.cube(Point{c.X - rx, c.Y - ky}, Point{c.X - kx, c.Y - ry}, Point{c.X, c.Y - ry})
	p.cube(Point{c.X + kx, c.Y - ry}, Point{c.X + rx, c.Y - ky}, Point{c.X + rx, c.Y})
	p.close()
	return p
}

// polyPath is a closed polygon.
func polyPath(pts ...Point) path {
	var p path
	for i, q := range pts {
		if i == 0 {
			p.move(q.X, q.Y)
		} else {
			p.line(q.X, q.Y)
		}
	}
	p.close()
	return p
}

// linePath is an open polyline.
func linePath(pts ...Point) path {
	var p path
	for i, q := range pts {
		if i == 0 {
			p.move(q.X, q.Y)
		} else {
			p.line(q.X, q.Y)
		}
	}
	return p
}

// roundedPolyline is an open polyline with rounded corners of radius r.
func roundedPolyline(pts []Point, r float64) path {
	var p path
	if len(pts) == 0 {
		return p
	}
	p.move(pts[0].X, pts[0].Y)
	for i := 1; i < len(pts)-1; i++ {
		a, b, c := pts[i-1], pts[i], pts[i+1]
		d1, d2 := dist(a, b), dist(b, c)
		rr := min(r, d1/2, d2/2)
		if rr < 0.5 {
			p.line(b.X, b.Y)
			continue
		}
		s := lerp(b, a, rr/d1)
		e := lerp(b, c, rr/d2)
		p.line(s.X, s.Y)
		p.quad(b.X, b.Y, e.X, e.Y)
	}
	l := pts[len(pts)-1]
	p.line(l.X, l.Y)
	return p
}

func dist(a, b Point) float64 { return math.Hypot(b.X-a.X, b.Y-a.Y) }

func lerp(a, b Point, t float64) Point { return Point{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t} }

// unit returns the unit vector from a to b (zero for a == b).
func unit(a, b Point) Point {
	d := dist(a, b)
	if d == 0 {
		return Point{}
	}
	return Point{(b.X - a.X) / d, (b.Y - a.Y) / d}
}

// Marker is the decoration at an end of an edge.
type marker uint8

const (
	markNone       marker = iota
	markArrow             // filled triangle
	markOpen              // open "V" (async messages)
	markCross             // ×
	markCircle            // filled circle
	markTriangle          // hollow triangle (inheritance, realization)
	markDiamond           // filled diamond (composition)
	markODiamond          // hollow diamond (aggregation)
	markOne               // ER: exactly one  ||
	markZeroOne           // ER: zero or one  o|
	markMany              // ER: one or many  }|
	markZeroMany          // ER: zero or many }o
	markArrowSmall        // small open arrow (class association)
)

// markerLen is how far a marker reaches back from the tip along the edge.
func markerLen(m marker, w float64) float64 {
	switch m {
	case markNone:
		return 0
	case markArrow:
		return 9 + 1.5*w
	case markOpen, markArrowSmall:
		return 1.5 * w
	case markCross:
		return 0
	case markCircle:
		return 8
	case markTriangle:
		return 14
	case markDiamond, markODiamond:
		return 18
	case markOne, markZeroOne, markMany, markZeroMany:
		return 0
	}
	return 0
}

// markerItems draws marker m with its tip at tip, pointing in direction
// dir (unit vector from the edge towards the tip).
func markerItems(m marker, tip, dir Point, w float64, stroke Paint) []Item {
	nx, ny := -dir.Y, dir.X // normal
	at := func(back, side float64) Point {
		return Point{tip.X - dir.X*back + nx*side, tip.Y - dir.Y*back + ny*side}
	}
	fill := stroke
	bg := Paint{Role: RoleBackground}
	switch m {
	case markArrow:
		l := 9 + 1.5*w
		return []Item{{Path: polyPath(tip, at(l, l*0.45), at(l*0.8, 0), at(l, -l*0.45)), Fill: fill}}
	case markOpen, markArrowSmall:
		l := 10.0
		return []Item{{Path: linePath(at(l, l*0.55), tip, at(l, -l*0.55)), Stroke: stroke, Width: w}}
	case markCross:
		s := 5.0
		c := at(s+2, 0)
		return []Item{{Path: append(linePath(Point{c.X - s, c.Y - s}, Point{c.X + s, c.Y + s}),
			linePath(Point{c.X - s, c.Y + s}, Point{c.X + s, c.Y - s})...), Stroke: stroke, Width: w * 1.5}}
	case markCircle:
		return []Item{{Path: ellipsePath(at(4, 0), 4, 4), Fill: fill}}
	case markTriangle:
		l := 14.0
		return []Item{{Path: polyPath(tip, at(l, l*0.5), at(l, -l*0.5)), Fill: bg, Stroke: stroke, Width: w}}
	case markDiamond, markODiamond:
		l := 18.0
		f := fill
		if m == markODiamond {
			f = bg
		}
		return []Item{{Path: polyPath(tip, at(l/2, l*0.3), at(l, 0), at(l/2, -l*0.3)), Fill: f, Stroke: stroke, Width: w}}
	case markOne, markZeroOne, markMany, markZeroMany:
		return erMarker(m, at, w, stroke)
	}
	return nil
}

// erMarker draws crow's foot cardinality markers.
func erMarker(m marker, at func(back, side float64) Point, w float64, stroke Paint) []Item {
	var p path
	bar := func(back float64) {
		a, b := at(back, 7), at(back, -7)
		p.move(a.X, a.Y)
		p.line(b.X, b.Y)
	}
	var items []Item
	circle := func(back float64) {
		items = append(items, Item{Path: ellipsePath(at(back, 0), 5, 5), Fill: Paint{Role: RoleBackground}, Stroke: stroke, Width: w})
	}
	switch m {
	case markOne:
		bar(6)
		bar(11)
	case markZeroOne:
		bar(6)
		circle(17)
	case markMany, markZeroMany:
		// Crow's foot: three prongs from a point 14 back to the tip line.
		o := at(14, 0)
		for _, s := range []float64{8, 0, -8} {
			t := at(0, s)
			p.move(o.X, o.Y)
			p.line(t.X, t.Y)
		}
		if m == markMany {
			bar(17)
		} else {
			circle(21)
		}
	}
	return append([]Item{{Path: p, Stroke: stroke, Width: w}}, items...)
}
