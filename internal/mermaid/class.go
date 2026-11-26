package mermaid

import (
	"regexp"
	"strings"
)

// Class diagrams: classes are boxes with a header (annotation, name), an
// attribute and a method compartment; relations carry UML markers and
// optional cardinalities; namespaces become clusters.

type clClass struct {
	id         string
	label      string
	annotation string
	attrs      []string
	methods    []string
	cluster    int
	node       int
	style      style
}

type clRel struct {
	from, to   string
	start, end marker
	dash       Dash
	label      string
	card1      string
	card2      string
}

type classParser struct {
	dir      string
	classes  []*clClass
	byID     map[string]*clClass
	rels     []clRel
	clusters []*gdCluster
	stack    []int
	notes    []clNote
	classDef map[string]style
}

type clNote struct {
	target, text string
}

var reRelation = regexp.MustCompile(`^(\S+?)\s*(?:"([^"]*)"\s*)?(<\||\*|o|<)?(--|\.\.)(\|>|\*|o|>)?\s*(?:"([^"]*)"\s*)?(\S+?)\s*(?::\s*(.*))?$`)

func (cp *classParser) class(id string) *clClass {
	id = strings.TrimSpace(id)
	label := ""
	if k := strings.Index(id, "["); k > 0 && strings.HasSuffix(id, "]") {
		id, label = id[:k], strings.Trim(id[k+1:len(id)-1], `"`)
	}
	if k := strings.Index(id, ":::"); k >= 0 {
		id = id[:k]
	}
	// Generics: Name~T~ → Name<T>.
	shown := id
	if k := strings.IndexByte(id, '~'); k > 0 {
		shown = id[:k] + "<" + strings.ReplaceAll(strings.Trim(id[k:], "~"), "~", ",") + ">"
		id = id[:k]
	}
	if c, ok := cp.byID[id]; ok {
		if label != "" {
			c.label = label
		}
		return c
	}
	if label == "" {
		label = shown
	}
	c := &clClass{id: id, label: label, cluster: -1, style: style{}}
	if len(cp.stack) > 0 {
		c.cluster = cp.stack[len(cp.stack)-1]
	}
	cp.byID[id] = c
	cp.classes = append(cp.classes, c)
	return c
}

func (c *clClass) member(m string) {
	m = strings.TrimSpace(m)
	if m == "" {
		return
	}
	if strings.HasPrefix(m, "<<") {
		c.annotation = strings.Trim(m, "<> ")
		return
	}
	m = strings.ReplaceAll(m, "~", "")
	if strings.Contains(m, "(") {
		c.methods = append(c.methods, m)
	} else {
		c.attrs = append(c.attrs, m)
	}
}

func renderClass(e *env, lines []srcLine) (*Diagram, error) {
	cp := &classParser{dir: "TB", byID: map[string]*clClass{}, classDef: map[string]style{}}
	for i := 1; i < len(lines); i++ {
		l := lines[i]
		t := l.text
		kw, rest := firstWord(t)
		switch strings.ToLower(kw) {
		case "direction":
			if d := normDir(rest); d != "" {
				cp.dir = d
			}
			continue
		case "namespace":
			name := strings.TrimSpace(strings.TrimSuffix(rest, "{"))
			parent := -1
			if len(cp.stack) > 0 {
				parent = cp.stack[len(cp.stack)-1]
			}
			cp.clusters = append(cp.clusters, &gdCluster{id: name, title: []string{name}, parent: parent})
			cp.stack = append(cp.stack, len(cp.clusters)-1)
			continue
		case "}":
			if len(cp.stack) == 0 {
				return nil, errorf(l.n, "unexpected }")
			}
			cp.stack = cp.stack[:len(cp.stack)-1]
			continue
		case "class":
			body := strings.HasSuffix(rest, "{")
			name := strings.TrimSpace(strings.TrimSuffix(rest, "{"))
			if name == "" {
				return nil, errorf(l.n, "class needs a name")
			}
			c := cp.class(name)
			if body {
				for i++; i < len(lines) && lines[i].text != "}"; i++ {
					c.member(lines[i].text)
				}
				if i >= len(lines) {
					return nil, errorf(l.n, "class %s is not closed with }", c.id)
				}
			}
			continue
		case "note":
			text := rest
			target := ""
			if f, r := firstWord(rest); f == "for" {
				target, text = firstWord(r)
			}
			cp.notes = append(cp.notes, clNote{target: target, text: strings.Trim(strings.TrimSpace(text), `"`)})
			continue
		case "classdef":
			names, props := firstWord(rest)
			for _, n := range strings.Split(names, ",") {
				cp.classDef[n] = parseStyle(props)
			}
			continue
		case "style":
			id, props := firstWord(rest)
			cp.class(id).style.merge(parseStyle(props))
			continue
		case "cssclass", "click", "callback", "link", "acctitle", "accdescr", "acctitle:", "accdescr:":
			continue
		}
		if strings.HasPrefix(t, "<<") {
			// <<interface>> Name
			k := strings.Index(t, ">>")
			if k > 0 {
				cp.class(t[k+2:]).annotation = strings.TrimSpace(t[2:k])
				continue
			}
		}
		if m := reRelation.FindStringSubmatch(t); m != nil {
			r := clRel{from: m[1], to: m[7], card1: m[2], card2: m[6], label: m[8]}
			r.start = classMarker(m[3])
			r.end = classMarker(m[5])
			if m[4] == ".." {
				r.dash = Dashed
			}
			cp.class(r.from)
			cp.class(r.to)
			r.from, r.to = cp.class(r.from).id, cp.class(r.to).id
			cp.rels = append(cp.rels, r)
			continue
		}
		if id, member, ok := strings.Cut(t, ":"); ok && !strings.ContainsAny(strings.TrimSpace(id), " \t") {
			cp.class(id).member(member)
			continue
		}
		if !strings.ContainsAny(t, " \t") {
			cp.class(t)
			continue
		}
		return nil, errorf(l.n, "cannot parse %q", clip(t))
	}
	if len(cp.stack) > 0 {
		return nil, errorf(lines[len(lines)-1].n, "namespace is not closed with }")
	}
	return cp.render(e), nil
}

func classMarker(s string) marker {
	switch s {
	case "<|", "|>":
		return markTriangle
	case "*":
		return markDiamond
	case "o":
		return markODiamond
	case "<", ">":
		return markOpen
	}
	return markNone
}

func (cp *classParser) render(e *env) *Diagram {
	gd := &graphDiagram{kind: "class", dir: cp.dir, clusters: cp.clusters, nodeSep: 40, rankSep: 56, portStep: 30}
	small := e.size * 0.86
	for _, c := range cp.classes {
		c.node = gd.add(classNode(e, c, small), c.cluster)
	}
	for i, n := range cp.notes {
		ls := e.wrap(labelLines(n.text), e.size, false, 220)
		tw, th := e.textSize(ls, e.size, false)
		cl := -1
		if t, ok := cp.byID[n.target]; ok {
			cl = t.cluster
		}
		id := gd.add(&gdNode{id: "note" + itoa(i+1), label: n.text, shape: "note", w: tw + 2*seqNotePadX, h: th + 2*seqNotePadY,
			draw: func(c Point, w, h float64) []Item {
				r := Rect{c.X - w/2, c.Y - h/2, w, h}
				return []Item{
					{Path: rectPath(r, 2), Fill: Paint{Role: RoleNoteFill}, Stroke: Paint{Role: RoleNoteStroke}, Width: 1},
					{Text: e.text(ls, c.X, c.Y, e.size, false, Paint{Role: RoleText})},
				}
			}}, cl)
		if t, ok := cp.byID[n.target]; ok {
			gd.edges = append(gd.edges, &gdEdge{from: id, to: t.node, dash: Dotted, width: 1})
		}
	}
	for _, r := range cp.rels {
		ed := &gdEdge{from: cp.byID[r.from].node, to: cp.byID[r.to].node, width: 1.3, dash: r.dash,
			start: r.start, end: r.end, startNote: r.card1, endNote: r.card2}
		if r.label != "" {
			ed.label = labelLines(r.label)
		}
		gd.edges = append(gd.edges, ed)
	}
	return gd.render(e)
}

// classNode builds the three-compartment box of c.
func classNode(e *env, c *clClass, small float64) *gdNode {
	name := []string{c.label}
	nw, nh := e.textSize(name, e.size, true)
	aw, ah := 0.0, 0.0
	if c.annotation != "" {
		aw, ah = e.textSize([]string{"«" + c.annotation + "»"}, small, false)
	}
	clean := func(ms []string) []string {
		out := make([]string, len(ms))
		for i, m := range ms {
			out[i] = strings.Replace(strings.Replace(strings.TrimRight(m, "$*"), ")*", ")", 1), ")$", ")", 1)
		}
		return out
	}
	attrs, methods := clean(c.attrs), clean(c.methods)
	tw, th := e.textSize(attrs, e.size, false)
	mw, mh := e.textSize(methods, e.size, false)
	const pad = 10.0
	w := max(nw, aw, tw, mw) + 2*pad + 8
	headH := nh + ah + 2*pad - 4
	attrH := max(th, 4) + pad
	methH := max(mh, 4) + pad
	fill, stroke, text, sw, dash := nodePaints(c.style)
	return &gdNode{id: c.id, label: c.label, shape: "class", w: max(w, 90), h: headH + attrH + methH,
		draw: func(ctr Point, w, h float64) []Item {
			r := Rect{ctr.X - w/2, ctr.Y - h/2, w, h}
			items := []Item{{Path: rectPath(r, 3), Fill: fill, Stroke: stroke, Width: sw, Dash: dash}}
			y := r.Y + pad - 2
			if c.annotation != "" {
				items = append(items, Item{Text: e.textAt([]string{"«" + c.annotation + "»"}, ctr.X, y, small, false, AnchorMiddle, Paint{Role: RoleTextMuted, Color: text.Color})})
				y += ah
			}
			items = append(items, Item{Text: e.textAt(name, ctr.X, y, e.size, true, AnchorMiddle, text)})
			y1 := r.Y + headH
			y2 := y1 + attrH
			items = append(items,
				Item{Path: linePath(Point{r.X, y1}, Point{r.X + r.W, y1}), Stroke: stroke, Width: 1},
				Item{Path: linePath(Point{r.X, y2}, Point{r.X + r.W, y2}), Stroke: stroke, Width: 1})
			for i, a := range attrs {
				t := e.textAt([]string{a}, r.X+pad, y1+pad/2+float64(i)*e.lh, e.size, false, AnchorStart, text)
				t.Italic = strings.HasSuffix(c.attrs[i], "*")
				items = append(items, Item{Text: t})
			}
			for i, m := range methods {
				t := e.textAt([]string{m}, r.X+pad, y2+pad/2+float64(i)*e.lh, e.size, false, AnchorStart, text)
				t.Italic = strings.HasSuffix(c.methods[i], "*") || strings.Contains(c.methods[i], ")*")
				items = append(items, Item{Text: t})
			}
			return items
		}}
}
