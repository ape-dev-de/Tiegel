package mermaid

import "math"

// Node shapes of flowcharts (also used by state diagrams).
const (
	shapeRect         = "rect"
	shapeRound        = "round"
	shapeStadium      = "stadium"
	shapeSubroutine   = "subroutine"
	shapeCylinder     = "cylinder"
	shapeCircle       = "circle"
	shapeDoubleCircle = "doublecircle"
	shapeDiamond      = "diamond"
	shapeHexagon      = "hexagon"
	shapeFlag         = "flag"
	shapeLeanRight    = "parallelogram"
	shapeLeanLeft     = "parallelogram-alt"
	shapeTrapezoid    = "trapezoid"
	shapeTrapezoidAlt = "trapezoid-alt"
	// State diagram specials.
	shapeStart = "start" // filled circle
	shapeEnd   = "end"   // ring with a filled center
	shapeBar   = "bar"   // fork/join bar
)

// shapeSize returns the node size of shape around a text block tw×th.
func shapeSize(shape string, tw, th float64) (w, h float64) {
	const px, py = 30, 18
	h = th + py
	switch shape {
	case shapeStadium:
		return tw + h + 10, h
	case shapeSubroutine:
		return tw + px + 16, h
	case shapeCylinder:
		w = tw + px
		ry := cylRY(w)
		return w, th + py + 2*ry
	case shapeCircle:
		d := max(tw, th) + px
		return d, d
	case shapeDoubleCircle:
		d := max(tw, th) + px + 10
		return d, d
	case shapeDiamond:
		p, q := tw/2+12, th/2+8
		b := 2.6 * q
		a := p / (1 - q/b)
		return math.Ceil(2 * a), math.Ceil(2 * b)
	case shapeHexagon:
		return tw + px + h/2, h
	case shapeFlag:
		return tw + px + h/3, h
	case shapeLeanRight, shapeLeanLeft, shapeTrapezoid, shapeTrapezoidAlt:
		return tw + px + h*0.7, h
	case shapeStart:
		return 16, 16
	case shapeEnd:
		return 18, 18
	case shapeBar:
		return 70, 8
	}
	return tw + px, h
}

func cylRY(w float64) float64 { return math.Round(w / 2 / (2.5 + w/50)) }

// textOffset is the shift of the label center from the node center.
func textOffset(shape string, w, h float64) Point {
	switch shape {
	case shapeFlag:
		return Point{h / 6, 0}
	case shapeCylinder:
		return Point{0, cylRY(w) / 2}
	}
	return Point{}
}

// shapeItems draws shape centered at c.
func shapeItems(shape string, c Point, w, h float64, fill, stroke Paint, sw float64, dash Dash) []Item {
	x0, y0, x1, y1 := c.X-w/2, c.Y-h/2, c.X+w/2, c.Y+h/2
	box := Rect{x0, y0, w, h}
	it := func(p path) Item { return Item{Path: p, Fill: fill, Stroke: stroke, Width: sw, Dash: dash} }
	switch shape {
	case shapeRound:
		return []Item{it(rectPath(box, 6))}
	case shapeStadium:
		return []Item{it(rectPath(box, h/2))}
	case shapeSubroutine:
		inner := Item{Path: append(linePath(Point{x0 + 8, y0}, Point{x0 + 8, y1}), linePath(Point{x1 - 8, y0}, Point{x1 - 8, y1})...),
			Stroke: stroke, Width: sw}
		return []Item{it(rectPath(box, 0)), inner}
	case shapeCylinder:
		ry := cylRY(w)
		rx := w / 2
		k := kappa
		var p path
		p.move(x0, y0+ry)
		p.cube(Point{x0, y0 + ry - ry*k}, Point{c.X - rx*k, y0}, Point{c.X, y0})
		p.cube(Point{c.X + rx*k, y0}, Point{x1, y0 + ry - ry*k}, Point{x1, y0 + ry})
		p.line(x1, y1-ry)
		p.cube(Point{x1, y1 - ry + ry*k}, Point{c.X + rx*k, y1}, Point{c.X, y1})
		p.cube(Point{c.X - rx*k, y1}, Point{x0, y1 - ry + ry*k}, Point{x0, y1 - ry})
		p.close()
		var lid path
		lid.move(x0, y0+ry)
		lid.cube(Point{x0, y0 + ry + ry*k}, Point{c.X - rx*k, y0 + 2*ry}, Point{c.X, y0 + 2*ry})
		lid.cube(Point{c.X + rx*k, y0 + 2*ry}, Point{x1, y0 + ry + ry*k}, Point{x1, y0 + ry})
		return []Item{it(p), {Path: lid, Stroke: stroke, Width: sw}}
	case shapeCircle:
		return []Item{it(ellipsePath(c, w/2, h/2))}
	case shapeDoubleCircle:
		return []Item{it(ellipsePath(c, w/2, h/2)), {Path: ellipsePath(c, w/2-5, h/2-5), Stroke: stroke, Width: sw}}
	case shapeDiamond:
		return []Item{it(polyPath(Point{c.X, y0}, Point{x1, c.Y}, Point{c.X, y1}, Point{x0, c.Y}))}
	case shapeHexagon:
		s := h / 4
		return []Item{it(polyPath(Point{x0 + s, y0}, Point{x1 - s, y0}, Point{x1, c.Y}, Point{x1 - s, y1}, Point{x0 + s, y1}, Point{x0, c.Y}))}
	case shapeFlag:
		return []Item{it(polyPath(Point{x0, y0}, Point{x1, y0}, Point{x1, y1}, Point{x0, y1}, Point{x0 + h/3, c.Y}))}
	case shapeLeanRight:
		s := h * 0.35
		return []Item{it(polyPath(Point{x0 + s, y0}, Point{x1, y0}, Point{x1 - s, y1}, Point{x0, y1}))}
	case shapeLeanLeft:
		s := h * 0.35
		return []Item{it(polyPath(Point{x0, y0}, Point{x1 - s, y0}, Point{x1, y1}, Point{x0 + s, y1}))}
	case shapeTrapezoid:
		s := h * 0.35
		return []Item{it(polyPath(Point{x0 + s, y0}, Point{x1 - s, y0}, Point{x1, y1}, Point{x0, y1}))}
	case shapeTrapezoidAlt:
		s := h * 0.35
		return []Item{it(polyPath(Point{x0, y0}, Point{x1, y0}, Point{x1 - s, y1}, Point{x0 + s, y1}))}
	case shapeStart:
		return []Item{{Path: ellipsePath(c, w/2, h/2), Fill: Paint{Role: RoleText}}}
	case shapeEnd:
		return []Item{
			{Path: ellipsePath(c, w/2, h/2), Fill: Paint{Role: RoleBackground}, Stroke: Paint{Role: RoleText}, Width: 1.5},
			{Path: ellipsePath(c, w/2-4, h/2-4), Fill: Paint{Role: RoleText}},
		}
	case shapeBar:
		return []Item{{Path: rectPath(box, 2), Fill: Paint{Role: RoleText}}}
	}
	return []Item{it(rectPath(box, 0))}
}
