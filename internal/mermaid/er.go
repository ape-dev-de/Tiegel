package mermaid

import (
	"regexp"
	"strings"
)

// Entity relationship diagrams: entities are tables (name header, one row
// per attribute: type, name, keys, comment), relationships carry crow's
// foot markers and a label.

type erEntity struct {
	id, label string
	attrs     [][]string // type, name, keys, comment
	node      int
}

type erRel struct {
	from, to   string
	start, end marker
	dash       Dash
	label      string
}

var reERRel = regexp.MustCompile(`^(\S+)\s*(\|\||\|o|\}\||\}o)(--|\.\.)(\|\||o\||\|\{|o\{)\s*(\S+)\s*(?::\s*(.*))?$`)

func renderER(e *env, lines []srcLine) (*Diagram, error) {
	var ents []*erEntity
	byID := map[string]*erEntity{}
	ent := func(id string) *erEntity {
		label := ""
		if k := strings.IndexByte(id, '['); k > 0 && strings.HasSuffix(id, "]") {
			id, label = id[:k], strings.Trim(id[k+1:len(id)-1], `"`)
		}
		id = strings.Trim(id, `"`)
		if x, ok := byID[id]; ok {
			if label != "" {
				x.label = label
			}
			return x
		}
		if label == "" {
			label = id
		}
		x := &erEntity{id: id, label: label}
		byID[id] = x
		ents = append(ents, x)
		return x
	}
	var rels []erRel
	dir := "TB"
	for i := 1; i < len(lines); i++ {
		l := lines[i]
		t := l.text
		if kw, rest := firstWord(t); strings.EqualFold(kw, "direction") {
			if d := normDir(rest); d != "" {
				dir = d
			}
			continue
		}
		if strings.HasSuffix(t, "{") {
			x := ent(strings.TrimSpace(strings.TrimSuffix(t, "{")))
			for i++; i < len(lines) && lines[i].text != "}"; i++ {
				x.attrs = append(x.attrs, erAttr(lines[i].text))
			}
			if i >= len(lines) {
				return nil, errorf(l.n, "entity %s is not closed with }", x.id)
			}
			continue
		}
		if m := reERRel.FindStringSubmatch(t); m != nil {
			r := erRel{from: ent(m[1]).id, to: ent(m[5]).id, label: strings.Trim(strings.TrimSpace(m[6]), `"`)}
			r.start = erCard(m[2])
			r.end = erCard(m[4])
			if m[3] == ".." {
				r.dash = Dashed
			}
			rels = append(rels, r)
			continue
		}
		if !strings.ContainsAny(t, " \t") {
			ent(t)
			continue
		}
		return nil, errorf(l.n, "cannot parse %q", clip(t))
	}
	gd := &graphDiagram{kind: "er", dir: dir, nodeSep: 50, rankSep: 70, portStep: 44}
	for _, x := range ents {
		x.node = gd.add(erNode(e, x), -1)
	}
	for _, r := range rels {
		ed := &gdEdge{from: byID[r.from].node, to: byID[r.to].node, width: 1.3, dash: r.dash, start: r.start, end: r.end}
		if r.label != "" {
			ed.label = labelLines(r.label)
		}
		gd.edges = append(gd.edges, ed)
	}
	return gd.render(e), nil
}

func erCard(s string) marker {
	switch s {
	case "||":
		return markOne
	case "|o", "o|":
		return markZeroOne
	case "}|", "|{":
		return markMany
	case "}o", "o{":
		return markZeroMany
	}
	return markNone
}

// erAttr splits "type name PK,FK \"comment\"".
func erAttr(s string) []string {
	comment := ""
	if k := strings.IndexByte(s, '"'); k >= 0 {
		comment = strings.Trim(s[k:], `"`)
		s = s[:k]
	}
	f := strings.Fields(s)
	out := []string{"", "", "", comment}
	for i := 0; i < len(f) && i < 2; i++ {
		out[i] = f[i]
	}
	if len(f) > 2 {
		out[2] = strings.Join(f[2:], " ")
	}
	return out
}

func erNode(e *env, x *erEntity) *gdNode {
	const pad = 10.0
	nw, nh := e.textSize([]string{x.label}, e.size, true)
	cols := [4]float64{}
	for _, a := range x.attrs {
		for c := range a {
			if a[c] != "" {
				cols[c] = max(cols[c], e.measure(a[c], e.size*0.93, c == 1 && strings.Contains(a[2], "PK")))
			}
		}
	}
	rowsW := 0.0
	for _, c := range cols {
		if c > 0 {
			rowsW += c + 2*pad
		}
	}
	w := max(nw+2*pad+10, rowsW, 100)
	head := nh + 2*pad - 4
	rowH := e.lineHeight(e.size*0.93) + 8
	h := head + float64(len(x.attrs))*rowH
	attrs := x.attrs
	return &gdNode{id: x.id, label: x.label, shape: "entity", w: w, h: h,
		draw: func(c Point, w, h float64) []Item {
			r := Rect{c.X - w/2, c.Y - h/2, w, h}
			items := []Item{
				{Path: rectPath(r, 3), Fill: Paint{Role: RoleNodeFill}, Stroke: Paint{Role: RoleNodeStroke}, Width: 1.3},
				{Text: e.text([]string{x.label}, c.X, r.Y+head/2, e.size, true, Paint{Role: RoleText})},
			}
			for i, a := range attrs {
				y := r.Y + head + float64(i)*rowH
				if i%2 == 0 {
					items = append(items, Item{Path: rectPath(Rect{r.X + 1, y, r.W - 2, rowH}, 0), Fill: Paint{Role: RoleClusterFill}})
				}
				items = append(items, Item{Path: linePath(Point{r.X, y}, Point{r.X + r.W, y}), Stroke: Paint{Role: RoleNodeStroke}, Width: 0.8})
				cx := r.X + pad
				for col, v := range a {
					if cols[col] == 0 {
						continue
					}
					p := Paint{Role: RoleText}
					if col != 1 {
						p.Role = RoleTextMuted
					}
					t := e.textAt([]string{v}, cx, y+4, e.size*0.93, col == 1 && strings.Contains(a[2], "PK"), AnchorStart, p)
					items = append(items, Item{Text: t})
					cx += cols[col] + 2*pad
				}
			}
			return items
		}}
}
