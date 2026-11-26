package mermaid

import (
	"math"
	"strconv"
	"strings"
)

// Pie charts: slices start at 12 o'clock and run clockwise in source
// order; the legend on the right lists label, percentage and (showData)
// the value.

func renderPie(e *env, lines []srcLine) (*Diagram, error) {
	showData := strings.Contains(strings.ToLower(lines[0].text), "showdata")
	title := ""
	if f := strings.Fields(lines[0].text); len(f) > 1 {
		rest := strings.TrimSpace(strings.TrimPrefix(lines[0].text, f[0]))
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "showData"))
		if t, ok := strings.CutPrefix(rest, "title"); ok {
			title = strings.TrimSpace(t)
		}
	}
	type slice struct {
		label string
		value float64
	}
	var slices []slice
	total := 0.0
	for _, l := range lines[1:] {
		t := l.text
		if kw, rest := firstWord(t); strings.EqualFold(kw, "title") {
			title = rest
			continue
		} else if strings.EqualFold(kw, "showData") {
			showData = true
			continue
		} else if strings.HasPrefix(strings.ToLower(kw), "acc") {
			continue
		}
		k := strings.LastIndexByte(t, ':')
		if k < 0 {
			return nil, errorf(l.n, `expected "label" : value`)
		}
		label := strings.Trim(strings.TrimSpace(t[:k]), `"`)
		v, err := strconv.ParseFloat(strings.TrimSpace(t[k+1:]), 64)
		if err != nil || v < 0 {
			return nil, errorf(l.n, "invalid value %q", strings.TrimSpace(t[k+1:]))
		}
		slices = append(slices, slice{label, v})
		total += v
	}
	if len(slices) == 0 || total <= 0 {
		return nil, errorf(lines[0].n, "pie chart without values")
	}
	const r = 110.0
	c := Point{r + 8, r + 8}
	d := &Diagram{Kind: "pie"}
	a := -math.Pi / 2
	for i, s := range slices {
		sweep := 2 * math.Pi * s.value / total
		if sweep <= 0 {
			continue
		}
		var p path
		if sweep >= 2*math.Pi-1e-9 {
			p = ellipsePath(c, r, r)
		} else {
			p.move(c.X, c.Y)
			p.line(c.X+r*math.Cos(a), c.Y+r*math.Sin(a))
			arc(&p, c, r, a, a+sweep)
			p.close()
		}
		d.Items = append(d.Items, Item{Path: p, Fill: Paint{Role: RolePie, Index: i}, Stroke: Paint{Role: RoleBackground}, Width: 2})
		d.Nodes = append(d.Nodes, NodeInfo{ID: s.label, Label: s.label, Shape: "slice", Box: Rect{c.X - r, c.Y - r, 2 * r, 2 * r}})
		a += sweep
	}
	// Legend: label column, then percentages aligned.
	lx := c.X + r + 30
	labelW := 0.0
	for _, s := range slices {
		txt := s.label
		if showData {
			txt += " [" + strconv.FormatFloat(s.value, 'f', -1, 64) + "]"
		}
		labelW = max(labelW, e.measure(txt, e.size, false))
	}
	ly := c.Y - float64(len(slices))*(e.lh+6)/2
	for i, s := range slices {
		y := ly + float64(i)*(e.lh+6)
		d.Items = append(d.Items, Item{Path: rectPath(Rect{lx, y + (e.lh-12)/2, 12, 12}, 2), Fill: Paint{Role: RolePie, Index: i}})
		txt := s.label
		pct := strconv.FormatFloat(100*s.value/total, 'f', 1, 64)
		pct = strings.TrimSuffix(pct, ".0") + "%"
		if showData {
			txt += " [" + strconv.FormatFloat(s.value, 'f', -1, 64) + "]"
		}
		d.Items = append(d.Items, Item{Text: e.textAt([]string{txt}, lx+20, y, e.size, false, AnchorStart, Paint{Role: RoleText})})
		d.Items = append(d.Items, Item{Text: e.textAt([]string{pct}, lx+20+labelW+14, y, e.size, false, AnchorStart, Paint{Role: RoleTextMuted})})
	}
	if title != "" {
		d.fit(e, 8)
		d.addTitle(e, title)
		return d, nil
	}
	d.fit(e, 8)
	return d, nil
}

// arc appends a circular arc from angle a0 to a1 (radians, clockwise in
// screen space) as cubic segments of at most 90°.
func arc(p *path, c Point, r, a0, a1 float64) {
	n := int(math.Ceil((a1 - a0) / (math.Pi / 2)))
	step := (a1 - a0) / float64(n)
	k := 4.0 / 3 * math.Tan(step/4)
	for i := 0; i < n; i++ {
		s, e := a0+float64(i)*step, a0+float64(i+1)*step
		p0 := Point{c.X + r*math.Cos(s), c.Y + r*math.Sin(s)}
		p3 := Point{c.X + r*math.Cos(e), c.Y + r*math.Sin(e)}
		c1 := Point{p0.X - k*r*math.Sin(s), p0.Y + k*r*math.Cos(s)}
		c2 := Point{p3.X + k*r*math.Sin(e), p3.Y - k*r*math.Cos(e)}
		p.cube(c1, c2, p3)
	}
}
