package mermaid

import (
	"strings"
)

// State diagrams map onto the layered graph layout: states are rounded
// boxes, composite states clusters, [*] start/end circles, <<fork>> and
// <<join>> bars, <<choice>> small diamonds, notes yellow boxes.

type stState struct {
	id      string
	label   string
	desc    []string
	kind    string // "", "start", "end", "fork", "join", "choice", "note"
	cluster int
	classes []string
	node    int
}

type stEdge struct {
	from, to string
	label    string
	note     bool
}

type stateParser struct {
	dir       string
	states    []*stState
	byID      map[string]*stState
	edges     []stEdge
	clusters  []*gdCluster
	compByID  map[string]int // composite id -> cluster
	stack     []int
	classDefs map[string]style
	notes     int
}

func (sp *stateParser) cluster() int {
	if len(sp.stack) == 0 {
		return -1
	}
	return sp.stack[len(sp.stack)-1]
}

func (sp *stateParser) scope() string {
	if c := sp.cluster(); c >= 0 {
		return sp.clusters[c].id
	}
	return "root"
}

func (sp *stateParser) state(id string) *stState {
	id = strings.TrimSpace(id)
	if s, ok := sp.byID[id]; ok {
		return s
	}
	s := &stState{id: id, label: id, cluster: sp.cluster()}
	sp.byID[id] = s
	sp.states = append(sp.states, s)
	return s
}

// ref resolves "[*]" to the start or end pseudo state of the scope and
// strips ":::class".
func (sp *stateParser) ref(id string, source bool) string {
	id = strings.TrimSpace(id)
	cls := ""
	if k := strings.Index(id, ":::"); k >= 0 {
		id, cls = id[:k], id[k+3:]
	}
	if id == "[*]" {
		kind := "end"
		if source {
			kind = "start"
		}
		key := sp.scope() + "[*]" + kind
		s := sp.state(key)
		s.kind, s.label = kind, ""
		return key
	}
	s := sp.state(id)
	if cls != "" {
		s.classes = append(s.classes, cls)
	}
	return id
}

func renderState(e *env, lines []srcLine) (*Diagram, error) {
	sp := &stateParser{dir: "TB", byID: map[string]*stState{}, compByID: map[string]int{}, classDefs: map[string]style{}}
	for i := 1; i < len(lines); i++ {
		l := lines[i]
		t := l.text
		kw, rest := firstWord(t)
		switch strings.ToLower(kw) {
		case "direction":
			if d := normDir(rest); d != "" && len(sp.stack) == 0 {
				sp.dir = d
			}
			continue
		case "classdef":
			names, props := firstWord(rest)
			for _, n := range strings.Split(names, ",") {
				sp.classDefs[n] = parseStyle(props)
			}
			continue
		case "class":
			ids, cls := firstWord(rest)
			for _, id := range strings.Split(ids, ",") {
				s := sp.state(id)
				s.classes = append(s.classes, strings.TrimSpace(cls))
			}
			continue
		case "acctitle", "accdescr", "acctitle:", "accdescr:", "--":
			continue
		case "note":
			// note right of X : text   |   note left of X \n … \n end note
			head, text, inline := strings.Cut(rest, ":")
			f := strings.Fields(head)
			if len(f) < 3 {
				return nil, errorf(l.n, "note needs a position and a state")
			}
			target := strings.Join(f[2:], " ")
			if !inline {
				var body []string
				for i++; i < len(lines) && !strings.EqualFold(lines[i].text, "end note"); i++ {
					body = append(body, lines[i].text)
				}
				if i >= len(lines) {
					return nil, errorf(l.n, "note is not closed with end note")
				}
				text = strings.Join(body, "\n")
			}
			sp.notes++
			id := "note" + itoa(sp.notes)
			n := sp.state(id)
			n.kind, n.label = "note", strings.TrimSpace(text)
			n.cluster = sp.state(target).cluster
			sp.edges = append(sp.edges, stEdge{from: target, to: id, note: true})
			continue
		case "}":
			if len(sp.stack) == 0 {
				return nil, errorf(l.n, "unexpected }")
			}
			sp.stack = sp.stack[:len(sp.stack)-1]
			continue
		case "state":
			if err := sp.stateDecl(l, rest); err != nil {
				return nil, err
			}
			continue
		}
		if strings.Contains(t, "-->") {
			a, b, _ := strings.Cut(t, "-->")
			label := ""
			if k := strings.IndexByte(b, ':'); k >= 0 {
				b, label = b[:k], strings.TrimSpace(b[k+1:])
			}
			if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
				return nil, errorf(l.n, "transition needs two states")
			}
			from := sp.ref(a, true)
			to := sp.ref(b, false)
			sp.edges = append(sp.edges, stEdge{from: from, to: to, label: label})
			continue
		}
		if id, desc, ok := strings.Cut(t, ":"); ok {
			s := sp.state(sp.ref(id, false))
			s.desc = append(s.desc, labelLines(desc)...)
			continue
		}
		// A bare state name.
		sp.ref(t, false)
	}
	if len(sp.stack) > 0 {
		return nil, errorf(lines[len(lines)-1].n, "composite state is not closed with }")
	}
	return sp.render(e), nil
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}

func (sp *stateParser) stateDecl(l srcLine, rest string) error {
	open := strings.HasSuffix(rest, "{")
	rest = strings.TrimSpace(strings.TrimSuffix(rest, "{"))
	label := ""
	id := rest
	if strings.HasPrefix(rest, `"`) {
		// state "Description" as ID
		k := strings.Index(rest[1:], `"`)
		if k < 0 {
			return errorf(l.n, "unterminated string")
		}
		label = rest[1 : k+1]
		after := strings.TrimSpace(rest[k+2:])
		if !strings.HasPrefix(after, "as ") {
			return errorf(l.n, `expected "as" after the description`)
		}
		id = strings.TrimSpace(after[3:])
	} else if k := strings.Index(rest, " as "); k >= 0 {
		label, id = strings.TrimSpace(rest[:k]), strings.TrimSpace(rest[k+4:])
	}
	kind := ""
	if k := strings.Index(id, "<<"); k >= 0 {
		kind = strings.ToLower(strings.Trim(strings.TrimSpace(id[k:]), "<>"))
		id = strings.TrimSpace(id[:k])
	}
	if id == "" {
		return errorf(l.n, "state needs a name")
	}
	if open {
		parent := sp.cluster()
		title := label
		if title == "" {
			title = id
		}
		sp.clusters = append(sp.clusters, &gdCluster{id: id, title: labelLines(title), parent: parent})
		sp.compByID[id] = len(sp.clusters) - 1
		sp.stack = append(sp.stack, len(sp.clusters)-1)
		return nil
	}
	s := sp.state(sp.ref(id, false))
	if label != "" {
		s.label = label
	}
	switch kind {
	case "fork", "join", "choice":
		s.kind = kind
	}
	return nil
}

func (sp *stateParser) render(e *env) *Diagram {
	gd := &graphDiagram{kind: "state", dir: sp.dir, clusters: sp.clusters, nodeSep: 36, rankSep: 46}
	// Transitions to a composite state go to its start (or first) state.
	redirect := func(id string, source bool) string {
		c, ok := sp.compByID[id]
		if !ok {
			return id
		}
		kind := "start"
		if source {
			kind = "end"
		}
		if s, ok := sp.byID[id+"[*]"+kind]; ok {
			return s.id
		}
		// Leaving: the last state of the composite; entering: the first.
		pick := ""
		for _, s := range sp.states {
			if s.cluster == c && s.id != id && s.kind != "note" && (!source || s.kind != "start") {
				pick = s.id
				if !source {
					break
				}
			}
		}
		if pick == "" {
			return id
		}
		return pick
	}
	for _, s := range sp.states {
		if _, comp := sp.compByID[s.id]; comp {
			s.node = -1
			continue
		}
		st := style{}
		for _, c := range s.classes {
			st.merge(sp.classDefs[c])
		}
		s.node = gd.add(sp.stateNode(e, s, st), s.cluster)
	}
	for _, ed := range sp.edges {
		f, t := sp.byID[redirect(ed.from, true)], sp.byID[redirect(ed.to, false)]
		if f == nil || t == nil || f.node < 0 || t.node < 0 {
			continue
		}
		ge := &gdEdge{from: f.node, to: t.node, width: 1.4, end: markArrow}
		if ed.label != "" {
			ge.label = e.wrap(labelLines(ed.label), e.size, false, 200)
		}
		if ed.note {
			ge.end, ge.dash = markNone, Dotted
			ge.width = 1
		}
		gd.edges = append(gd.edges, ge)
	}
	return gd.render(e)
}

func (sp *stateParser) stateNode(e *env, s *stState, st style) *gdNode {
	n := &gdNode{id: s.id, label: s.label}
	switch s.kind {
	case "start", "end":
		n.shape = s.kind
		n.w, n.h = shapeSize(s.kind, 0, 0)
		n.draw = func(c Point, w, h float64) []Item { return shapeItems(n.shape, c, w, h, Paint{}, Paint{}, 0, Solid) }
		return n
	case "fork", "join":
		n.shape = shapeBar
		n.w, n.h = 70, 8
		if sp.dir == "LR" || sp.dir == "RL" {
			n.w, n.h = 8, 70
		}
		n.draw = func(c Point, w, h float64) []Item {
			return []Item{{Path: rectPath(Rect{c.X - w/2, c.Y - h/2, w, h}, 2), Fill: Paint{Role: RoleText}}}
		}
		return n
	case "choice":
		n.shape = shapeDiamond
		n.w, n.h = 26, 26
		n.draw = func(c Point, w, h float64) []Item {
			return shapeItems(shapeDiamond, c, w, h, Paint{Role: RoleNodeFill}, Paint{Role: RoleNodeStroke}, 1.3, Solid)
		}
		return n
	case "note":
		ls := e.wrap(labelLines(s.label), e.size, false, 220)
		tw, th := e.textSize(ls, e.size, false)
		n.shape, n.w, n.h = "note", tw+2*seqNotePadX, th+2*seqNotePadY
		n.draw = func(c Point, w, h float64) []Item {
			r := Rect{c.X - w/2, c.Y - h/2, w, h}
			return []Item{
				{Path: rectPath(r, 2), Fill: Paint{Role: RoleNoteFill}, Stroke: Paint{Role: RoleNoteStroke}, Width: 1},
				{Text: e.text(ls, c.X, c.Y, e.size, false, Paint{Role: RoleText})},
			}
		}
		return n
	}
	title := labelLines(s.label)
	fill, stroke, text, sw, dash := nodePaints(st)
	tw, th := e.textSize(title, e.size, len(s.desc) > 0)
	dw, dh := e.textSize(s.desc, e.size, false)
	n.shape = shapeRound
	n.w = max(tw, dw) + 30
	n.h = th + 18
	if len(s.desc) > 0 {
		n.h += dh + 10
	}
	n.w = max(n.w, 60)
	desc := s.desc
	n.draw = func(c Point, w, h float64) []Item {
		r := Rect{c.X - w/2, c.Y - h/2, w, h}
		items := []Item{{Path: rectPath(r, 8), Fill: fill, Stroke: stroke, Width: sw, Dash: dash}}
		if len(desc) == 0 {
			return append(items, Item{Text: e.text(title, c.X, c.Y, e.size, false, text)})
		}
		hy := r.Y + 9 + th/2
		items = append(items, Item{Text: e.text(title, c.X, hy, e.size, true, text)})
		ly := r.Y + th + 14
		items = append(items, Item{Path: linePath(Point{r.X, ly}, Point{r.X + r.W, ly}), Stroke: stroke, Width: 1})
		items = append(items, Item{Text: e.text(desc, c.X, ly+5+dh/2, e.size, false, text)})
		return items
	}
	return n
}
