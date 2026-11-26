package mermaid

import "math"

// DashPattern returns the on/off lengths of d for stroke width w (0, 0
// for Solid). SVG export and Dashes use the same pattern.
func DashPattern(d Dash, w float64) (on, off float64) {
	switch d {
	case Dotted:
		return 2 * w, 3 * w
	case Dashed:
		return 5 * w, 4 * w
	}
	return 0, 0
}

// Flatten approximates segs by polylines (one per subpath; closed
// subpaths repeat their first point at the end).
func Flatten(segs []Seg) [][]Point {
	var out [][]Point
	var cur []Point
	flush := func() {
		if len(cur) > 1 {
			out = append(out, cur)
		}
		cur = nil
	}
	last := func() Point {
		if len(cur) == 0 {
			return Point{}
		}
		return cur[len(cur)-1]
	}
	for _, s := range segs {
		switch s.Op {
		case Move:
			flush()
			cur = []Point{s.Pts[0]}
		case Line:
			cur = append(cur, s.Pts[0])
		case Quad:
			p0, c, p1 := last(), s.Pts[0], s.Pts[1]
			for i := 1; i <= 8; i++ {
				t := float64(i) / 8
				u := 1 - t
				cur = append(cur, Point{u*u*p0.X + 2*u*t*c.X + t*t*p1.X, u*u*p0.Y + 2*u*t*c.Y + t*t*p1.Y})
			}
		case Cube:
			p0, c1, c2, p1 := last(), s.Pts[0], s.Pts[1], s.Pts[2]
			n := 16
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				cur = append(cur, Point{
					u*u*u*p0.X + 3*u*u*t*c1.X + 3*u*t*t*c2.X + t*t*t*p1.X,
					u*u*u*p0.Y + 3*u*u*t*c1.Y + 3*u*t*t*c2.Y + t*t*t*p1.Y,
				})
			}
		case Close:
			if len(cur) > 0 {
				cur = append(cur, cur[0])
			}
			flush()
		}
	}
	flush()
	return out
}

// Dashes converts the outline of segs into dash pieces of length on,
// separated by gaps of length off, for renderers without dashed strokes.
func Dashes(segs []Seg, on, off float64) []Seg {
	if on <= 0 || off <= 0 {
		return segs
	}
	var out []Seg
	for _, pl := range Flatten(segs) {
		pos, drawing := 0.0, true // position within the current dash/gap
		var p path
		p.move(pl[0].X, pl[0].Y)
		for i := 1; i < len(pl); i++ {
			a, b := pl[i-1], pl[i]
			segLen := dist(a, b)
			t := 0.0
			for segLen-t > 1e-9 {
				limit := on
				if !drawing {
					limit = off
				}
				step := math.Min(limit-pos, segLen-t)
				t += step
				pos += step
				q := lerp(a, b, t/segLen)
				if drawing {
					p.line(q.X, q.Y)
				}
				if pos >= limit-1e-9 {
					pos = 0
					drawing = !drawing
					if drawing {
						p.move(q.X, q.Y)
					}
				}
			}
		}
		// Drop moves that are not followed by a line.
		for i, s := range p {
			if s.Op == Line || (s.Op == Move && i+1 < len(p) && p[i+1].Op == Line) {
				out = append(out, s)
			}
		}
	}
	return out
}
