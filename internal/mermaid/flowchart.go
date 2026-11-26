package mermaid

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type fcNode struct {
	id      string
	label   string // raw label (labelLines applies)
	shape   string
	classes []string
	style   style
	cluster int
}

type fcEdge struct {
	from, to   string
	label      string
	dash       Dash
	thick      bool
	invisible  bool
	start, end marker
	style      style
}

type fcCluster struct {
	id     string
	title  string
	parent int
}

type flowchart struct {
	dir        string
	nodes      []*fcNode
	byID       map[string]*fcNode
	edges      []*fcEdge
	clusters   []*fcCluster
	classDefs  map[string]style
	linkStyles map[int]style
	linkAll    style
	stack      []int // open subgraphs
	// pending classes/styles for nodes defined later
	pendingClass map[string][]string
	pendingStyle map[string]style
}

func newFlowchart() *flowchart {
	return &flowchart{dir: "TB", byID: map[string]*fcNode{}, classDefs: map[string]style{},
		linkStyles: map[int]style{}, pendingClass: map[string][]string{}, pendingStyle: map[string]style{}}
}

func normDir(s string) string {
	switch strings.ToUpper(s) {
	case "TD", "TB":
		return "TB"
	case "BT":
		return "BT"
	case "LR":
		return "LR"
	case "RL":
		return "RL"
	}
	return ""
}

func renderFlowchart(e *env, lines []srcLine) (*Diagram, error) {
	fc := newFlowchart()
	f := strings.Fields(lines[0].text)
	if len(f) > 1 {
		d := normDir(strings.TrimSuffix(f[1], ";"))
		if d == "" {
			return nil, errorf(lines[0].n, "unknown direction %q", f[1])
		}
		fc.dir = d
	}
	// "graph TD; A-->B" on one line.
	first := lines[0]
	if k := strings.IndexByte(first.text, ';'); k >= 0 {
		lines = lines[1:]
		if rest := strings.TrimSpace(first.text[k+1:]); rest != "" {
			lines = append([]srcLine{{first.n, rest}}, lines...)
		}
	} else {
		lines = lines[1:]
	}
	for _, l := range lines {
		for _, st := range splitStatements(l) {
			if err := fc.statement(st); err != nil {
				return nil, err
			}
		}
	}
	if len(fc.stack) > 0 {
		return nil, errorf(lines[len(lines)-1].n, "subgraph %q is not closed with end", fc.clusters[fc.stack[len(fc.stack)-1]].id)
	}
	return fc.render(e), nil
}

func (fc *flowchart) cluster() int {
	if len(fc.stack) == 0 {
		return -1
	}
	return fc.stack[len(fc.stack)-1]
}

func (fc *flowchart) node(id string) *fcNode {
	if n, ok := fc.byID[id]; ok {
		return n
	}
	n := &fcNode{id: id, shape: shapeRect, cluster: fc.cluster(), style: style{}}
	fc.byID[id] = n
	fc.nodes = append(fc.nodes, n)
	return n
}

func firstWord(s string) (string, string) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, unicode.IsSpace)
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

func (fc *flowchart) statement(l srcLine) error {
	kw, rest := firstWord(l.text)
	switch strings.ToLower(kw) {
	case "subgraph":
		return fc.subgraph(l, rest)
	case "end":
		if rest == "" {
			if len(fc.stack) == 0 {
				return errorf(l.n, "end without subgraph")
			}
			fc.stack = fc.stack[:len(fc.stack)-1]
			return nil
		}
	case "direction":
		if len(fc.stack) == 0 {
			if d := normDir(rest); d != "" {
				fc.dir = d
			}
		}
		return nil
	case "classdef":
		names, props := firstWord(rest)
		for _, n := range strings.Split(names, ",") {
			fc.classDefs[strings.TrimSpace(n)] = parseStyle(props)
		}
		return nil
	case "class":
		ids, cls := firstWord(rest)
		cls = strings.TrimSpace(cls)
		for _, id := range strings.Split(ids, ",") {
			id = strings.TrimSpace(id)
			if n, ok := fc.byID[id]; ok {
				n.classes = append(n.classes, cls)
			} else {
				fc.pendingClass[id] = append(fc.pendingClass[id], cls)
			}
		}
		return nil
	case "style":
		id, props := firstWord(rest)
		st := parseStyle(props)
		if n, ok := fc.byID[id]; ok {
			n.style.merge(st)
		} else {
			if fc.pendingStyle[id] == nil {
				fc.pendingStyle[id] = style{}
			}
			fc.pendingStyle[id].merge(st)
		}
		return nil
	case "linkstyle":
		which, props := firstWord(rest)
		st := parseStyle(props)
		if which == "default" {
			fc.linkAll = st
			return nil
		}
		for _, w := range strings.Split(which, ",") {
			if i, err := strconv.Atoi(strings.TrimSpace(w)); err == nil {
				fc.linkStyles[i] = st
			}
		}
		return nil
	case "click", "acctitle", "accdescr", "acctitle:", "accdescr:", "title":
		return nil
	}
	return fc.chain(l)
}

func (fc *flowchart) subgraph(l srcLine, rest string) error {
	id, title := rest, rest
	if rest == "" {
		id = "subgraph" + strconv.Itoa(len(fc.clusters))
		title = ""
	} else if k := strings.IndexAny(rest, "[\""); k > 0 && strings.HasSuffix(rest, "]") {
		id = strings.TrimSpace(rest[:k])
		title = strings.TrimSuffix(rest[k+1:], "]")
	} else if strings.HasPrefix(rest, `"`) {
		title = rest
		id = strings.Trim(rest, `"`)
	}
	fc.clusters = append(fc.clusters, &fcCluster{id: id, title: title, parent: fc.cluster()})
	fc.stack = append(fc.stack, len(fc.clusters)-1)
	return nil
}

// scanner walks a statement.
type scanner struct {
	s string
	p int
	n int // line
}

func (sc *scanner) eof() bool { return sc.p >= len(sc.s) }
func (sc *scanner) skip() {
	for sc.p < len(sc.s) && (sc.s[sc.p] == ' ' || sc.s[sc.p] == '\t') {
		sc.p++
	}
}
func (sc *scanner) peek() rune {
	r, _ := utf8.DecodeRuneInString(sc.s[sc.p:])
	return r
}
func (sc *scanner) has(p string) bool { return strings.HasPrefix(sc.s[sc.p:], p) }

func isIDRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r) || r > 0x7f && !unicode.IsSpace(r) && !unicode.IsPunct(r)
}

// chain parses "A[x] --> B & C -- y --> D".
func (fc *flowchart) chain(l srcLine) error {
	sc := &scanner{s: l.text, n: l.n}
	left, err := fc.group(sc)
	if err != nil {
		return err
	}
	for {
		sc.skip()
		if sc.eof() {
			return nil
		}
		e, err := fc.edge(sc)
		if err != nil {
			return err
		}
		right, err := fc.group(sc)
		if err != nil {
			return err
		}
		for _, a := range left {
			for _, b := range right {
				ce := *e
				ce.from, ce.to = a, b
				fc.edges = append(fc.edges, &ce)
			}
		}
		left = right
	}
}

func (fc *flowchart) group(sc *scanner) ([]string, error) {
	var ids []string
	for {
		id, err := fc.nodeRef(sc)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		sc.skip()
		if !sc.has("&") {
			return ids, nil
		}
		sc.p++
	}
}

type delim struct{ open, close, shape string }

var delims = []delim{
	{"(((", ")))", shapeDoubleCircle}, {"((", "))", shapeCircle}, {"([", "])", shapeStadium},
	{"[[", "]]", shapeSubroutine}, {"[(", ")]", shapeCylinder}, {"{{", "}}", shapeHexagon},
	{"[/", "/]", shapeLeanRight}, {"[/", `\]`, shapeTrapezoid}, {`[\`, `\]`, shapeLeanLeft},
	{`[\`, "/]", shapeTrapezoidAlt}, {">", "]", shapeFlag}, {"(", ")", shapeRound},
	{"[", "]", shapeRect}, {"{", "}", shapeDiamond},
}

var newShapes = map[string]string{
	"rect": shapeRect, "rectangle": shapeRect, "proc": shapeRect, "process": shapeRect,
	"rounded": shapeRound, "event": shapeRound, "stadium": shapeStadium, "pill": shapeStadium, "terminal": shapeStadium,
	"subroutine": shapeSubroutine, "subproc": shapeSubroutine, "fr-rect": shapeSubroutine,
	"cyl": shapeCylinder, "cylinder": shapeCylinder, "db": shapeCylinder, "database": shapeCylinder,
	"circle": shapeCircle, "circ": shapeCircle, "dbl-circ": shapeDoubleCircle, "double-circle": shapeDoubleCircle,
	"diam": shapeDiamond, "diamond": shapeDiamond, "decision": shapeDiamond, "question": shapeDiamond,
	"hex": shapeHexagon, "hexagon": shapeHexagon, "prepare": shapeHexagon,
	"lean-r": shapeLeanRight, "lean-right": shapeLeanRight, "in-out": shapeLeanRight,
	"lean-l": shapeLeanLeft, "lean-left": shapeLeanLeft, "out-in": shapeLeanLeft,
	"trap-b": shapeTrapezoid, "trapezoid": shapeTrapezoid, "priority": shapeTrapezoid,
	"trap-t": shapeTrapezoidAlt, "inv-trapezoid": shapeTrapezoidAlt, "manual": shapeTrapezoidAlt,
	"odd": shapeFlag, "flag": shapeFlag, "sm-circ": shapeStart, "start": shapeStart, "fr-circ": shapeEnd, "stop": shapeEnd,
}

// nodeRef parses one node reference with optional shape, label and class.
func (fc *flowchart) nodeRef(sc *scanner) (string, error) {
	sc.skip()
	start := sc.p
	for !sc.eof() {
		r := sc.peek()
		if isIDRune(r) || r == '.' && sc.p > start {
			sc.p += utf8.RuneLen(r)
			continue
		}
		if r == '-' && sc.p > start && sc.p+1 < len(sc.s) {
			nx, _ := utf8.DecodeRuneInString(sc.s[sc.p+1:])
			if isIDRune(nx) {
				sc.p++
				continue
			}
		}
		break
	}
	id := sc.s[start:sc.p]
	if id == "" {
		if sc.eof() {
			return "", errorf(sc.n, "missing node")
		}
		return "", errorf(sc.n, "unexpected %q", string(sc.peek()))
	}
	n := fc.node(id)
	if c := fc.cluster(); c >= 0 {
		n.cluster = c
	}
	if cls, ok := fc.pendingClass[id]; ok {
		n.classes = append(n.classes, cls...)
		delete(fc.pendingClass, id)
	}
	if st, ok := fc.pendingStyle[id]; ok {
		n.style.merge(st)
		delete(fc.pendingStyle, id)
	}
	// New shape syntax: A@{ shape: rounded, label: "x" }.
	if sc.has("@{") {
		end := strings.IndexByte(sc.s[sc.p:], '}')
		if end < 0 {
			return "", errorf(sc.n, "unterminated @{ … }")
		}
		body := sc.s[sc.p+2 : sc.p+end]
		sc.p += end + 1
		for _, kv := range splitProps(body) {
			k, v, _ := strings.Cut(kv, ":")
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			switch k {
			case "shape":
				if s, ok := newShapes[strings.Trim(v, `"'`)]; ok {
					n.shape = s
				}
			case "label":
				n.label = v
			}
		}
	}
	for _, d := range delims {
		if !sc.has(d.open) {
			continue
		}
		body := sc.s[sc.p+len(d.open):]
		var text string
		var rest string
		if strings.HasPrefix(strings.TrimSpace(body), `"`) {
			b := strings.TrimSpace(body)
			q := strings.IndexByte(b[1:], '"')
			if q < 0 {
				return "", errorf(sc.n, "unterminated string")
			}
			text = b[:q+2]
			rest = strings.TrimSpace(b[q+2:])
			if !strings.HasPrefix(rest, d.close) {
				continue
			}
			sc.p = len(sc.s) - len(rest) + len(d.close)
		} else {
			k := strings.Index(body, d.close)
			if k < 0 {
				continue
			}
			// "[/a/]" vs "[/a\]": take the nearest matching closer of this opener.
			if better := nearerCloser(d, body); better != "" && better != d.close {
				continue
			}
			text = body[:k]
			sc.p += len(d.open) + k + len(d.close)
		}
		n.shape, n.label = d.shape, text
		break
	}
	if sc.has(":::") {
		sc.p += 3
		st := sc.p
		for !sc.eof() && (isIDRune(sc.peek()) || sc.peek() == '-') {
			sc.p += utf8.RuneLen(sc.peek())
		}
		n.classes = append(n.classes, sc.s[st:sc.p])
	}
	return id, nil
}

func nearerCloser(d delim, body string) string {
	best, bi := "", -1
	for _, o := range delims {
		if o.open != d.open {
			continue
		}
		if k := strings.Index(body, o.close); k >= 0 && (bi < 0 || k < bi) {
			best, bi = o.close, k
		}
	}
	return best
}

func splitProps(s string) []string {
	var out []string
	quote, start := false, 0
	for i, r := range s {
		switch {
		case r == '"':
			quote = !quote
		case r == ',' && !quote:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// edge parses a link with optional text.
func (fc *flowchart) edge(sc *scanner) (*fcEdge, error) {
	sc.skip()
	e := &fcEdge{}
	switch {
	case sc.has("<"):
		e.start = markArrow
		sc.p++
	case (sc.has("o") || sc.has("x")) && sc.p+1 < len(sc.s) && strings.ContainsRune("-=.", rune(sc.s[sc.p+1])):
		e.start = markCircle
		if sc.s[sc.p] == 'x' {
			e.start = markCross
		}
		sc.p++
	}
	bstart := sc.p
	for !sc.eof() && strings.ContainsRune("-=.~", sc.peek()) {
		sc.p++
	}
	body := sc.s[bstart:sc.p]
	if len(body) < 2 {
		return nil, errorf(sc.n, "expected a link (-->, ---, -.->, ==>) at %q", clip(sc.s[bstart:]))
	}
	classify := func(b string) {
		switch {
		case strings.Contains(b, "~"):
			e.invisible = true
		case strings.Contains(b, "="):
			e.thick = true
		case strings.Contains(b, "."):
			e.dash = Dotted
		}
	}
	head := func() {
		if sc.eof() {
			return
		}
		switch sc.s[sc.p] {
		case '>':
			e.end = markArrow
			sc.p++
		case 'o', 'x':
			nx := sc.p + 1
			if nx >= len(sc.s) || !isIDRune(rune(sc.s[nx])) {
				e.end = markCircle
				if sc.s[sc.p] == 'x' {
					e.end = markCross
				}
				sc.p++
			}
		}
	}
	textForm := (body == "--" || body == "==" || body == "-.") && !sc.has(">")
	if textForm {
		closer := body
		if body == "-." {
			closer = ".-"
		}
		rest := sc.s[sc.p:]
		k := strings.Index(rest, closer)
		if k >= 0 {
			e.label = strings.TrimSpace(rest[:k])
			sc.p += k
			cs := sc.p
			for !sc.eof() && strings.ContainsRune("-=.", sc.peek()) {
				sc.p++
			}
			classify(body + sc.s[cs:sc.p])
			head()
			if e.start == markArrow && e.end == markNone {
				e.end = markArrow
			}
			return fc.pipeLabel(sc, e)
		}
	}
	classify(body)
	head()
	return fc.pipeLabel(sc, e)
}

func (fc *flowchart) pipeLabel(sc *scanner, e *fcEdge) (*fcEdge, error) {
	sc.skip()
	if sc.has("|") {
		k := strings.IndexByte(sc.s[sc.p+1:], '|')
		if k < 0 {
			return nil, errorf(sc.n, "unterminated |label|")
		}
		e.label = strings.TrimSpace(sc.s[sc.p+1 : sc.p+1+k])
		sc.p += k + 2
	}
	return e, nil
}

func clip(s string) string {
	if len(s) > 20 {
		return s[:20] + "…"
	}
	return s
}

// nodeStyle resolves the effective style of n.
func (fc *flowchart) nodeStyle(n *fcNode) style {
	st := style{}
	if d, ok := fc.classDefs["default"]; ok {
		st.merge(d)
	}
	for _, c := range n.classes {
		if d, ok := fc.classDefs[c]; ok {
			st.merge(d)
		}
	}
	st.merge(n.style)
	return st
}

// nodePaints returns fill, stroke and text paints and the stroke width.
func nodePaints(st style) (fill, stroke, text Paint, sw float64, dash Dash) {
	fill = Paint{Role: RoleNodeFill, Color: st.color("fill")}
	stroke = Paint{Role: RoleNodeStroke, Color: st.color("stroke")}
	text = Paint{Role: RoleText, Color: st.color("color")}
	if text.Color == "" && fill.Color != "" {
		text.Color = contrastText(fill.Color)
	}
	sw = 1.3
	if w := st.width(); w > 0 {
		sw = w
	}
	if st.dashed() {
		dash = Dashed
	}
	return
}

// linkSubgraphs redirects edges that name a subgraph to its first node
// and drops the placeholder nodes created for them.
func (fc *flowchart) linkSubgraphs() {
	within := func(c, top int) bool {
		for ; c >= 0; c = fc.clusters[c].parent {
			if c == top {
				return true
			}
		}
		return false
	}
	redirect := map[string]string{}
	for ci, c := range fc.clusters {
		n, ok := fc.byID[c.id]
		if !ok || n.label != "" {
			continue
		}
		for _, m := range fc.nodes {
			if m != n && within(m.cluster, ci) {
				redirect[c.id] = m.id
				break
			}
		}
	}
	if len(redirect) == 0 {
		return
	}
	var keep []*fcNode
	for _, n := range fc.nodes {
		if _, ok := redirect[n.id]; ok {
			delete(fc.byID, n.id)
			continue
		}
		keep = append(keep, n)
	}
	fc.nodes = keep
	for _, ed := range fc.edges {
		if r, ok := redirect[ed.from]; ok {
			ed.from = r
		}
		if r, ok := redirect[ed.to]; ok {
			ed.to = r
		}
	}
}

func (fc *flowchart) render(e *env) *Diagram {
	fc.linkSubgraphs()
	g := &graph{dir: fc.dir}
	idx := map[string]int{}
	type nlab struct {
		lines  []string
		tw, th float64
	}
	labs := make([]nlab, len(fc.nodes))
	for i, n := range fc.nodes {
		raw := n.label
		if raw == "" {
			raw = n.id
		}
		ls := e.wrap(labelLines(raw), e.size, false, 220)
		tw, th := e.textSize(ls, e.size, false)
		labs[i] = nlab{ls, tw, th}
		w, h := shapeSize(n.shape, tw, th)
		g.nodes = append(g.nodes, &gNode{w: w, h: h, shape: n.shape, cluster: n.cluster})
		idx[n.id] = i
	}
	for _, c := range fc.clusters {
		ls := labelLines(c.title)
		tw, th := e.textSize(ls, e.size, false)
		if c.title == "" {
			th = 0
		}
		g.clusters = append(g.clusters, &gCluster{parent: c.parent, tw: tw, th: th})
	}
	elabs := make([][]string, len(fc.edges))
	for i, ed := range fc.edges {
		ge := &gEdge{from: idx[ed.from], to: idx[ed.to]}
		if ed.label != "" {
			elabs[i] = e.wrap(labelLines(ed.label), e.size, false, 200)
			tw, th := e.textSize(elabs[i], e.size, false)
			ge.lw, ge.lh = tw+8, th+4
		}
		w := edgeWidth(ed)
		ge.startCut = markerLen(ed.start, w)
		ge.endCut = markerLen(ed.end, w)
		g.edges = append(g.edges, ge)
	}
	g.layout()

	d := &Diagram{Kind: "flowchart", Width: g.width, Height: g.height}
	// Subgraphs.
	for i, c := range fc.clusters {
		gc := g.clusters[i]
		if gc.empty {
			continue
		}
		d.Items = append(d.Items, Item{Path: rectPath(gc.box, 4), Fill: Paint{Role: RoleClusterFill},
			Stroke: Paint{Role: RoleClusterStroke}, Width: 1})
		if c.title != "" {
			ls := labelLines(c.title)
			d.Items = append(d.Items, Item{Text: e.textAt(ls, gc.box.X+gc.box.W/2, gc.box.Y+4, e.size, false, AnchorMiddle, Paint{Role: RoleText})})
		}
		d.Nodes = append(d.Nodes, NodeInfo{ID: c.id, Label: joinLines(labelLines(c.title)), Shape: "subgraph", Box: gc.box})
	}
	// Edges.
	var labels []Item
	for i, ed := range fc.edges {
		ge := g.edges[i]
		d.Edges = append(d.Edges, EdgeInfo{From: ed.from, To: ed.to, Label: joinLines(elabs[i]), Points: ge.pts})
		if ed.invisible {
			continue
		}
		st := style{}
		st.merge(fc.linkAll)
		if s, ok := fc.linkStyles[i]; ok {
			st.merge(s)
		}
		stroke := Paint{Role: RoleLine, Color: st.color("stroke")}
		w := edgeWidth(ed)
		if sw := st.width(); sw > 0 {
			w = sw
		}
		d.Items = append(d.Items, Item{Path: ge.path, Stroke: stroke, Width: w, Dash: ed.dash})
		d.Items = append(d.Items, markerItems(ed.end, ge.endTip, ge.endDir, w, stroke)...)
		d.Items = append(d.Items, markerItems(ed.start, ge.startTip, ge.startDir, w, stroke)...)
		if ge.hasLabel {
			labels = append(labels, labelItems(e, elabs[i], ge.label, Paint{Role: RoleText, Color: st.color("color")})...)
		}
	}
	d.Items = append(d.Items, labels...)
	// Nodes.
	for i, n := range fc.nodes {
		gn := g.nodes[i]
		fill, stroke, text, sw, dash := nodePaints(fc.nodeStyle(n))
		c := Point{gn.x, gn.y}
		d.Items = append(d.Items, shapeItems(n.shape, c, gn.w, gn.h, fill, stroke, sw, dash)...)
		off := textOffset(n.shape, gn.w, gn.h)
		d.Items = append(d.Items, Item{Text: e.text(labs[i].lines, c.X+off.X, c.Y+off.Y, e.size, false, text)})
		d.Nodes = append(d.Nodes, NodeInfo{ID: n.id, Label: joinLines(labs[i].lines), Shape: n.shape,
			Box: Rect{gn.x - gn.w/2, gn.y - gn.h/2, gn.w, gn.h}})
	}
	return d
}

func edgeWidth(ed *fcEdge) float64 {
	if ed.thick {
		return 3
	}
	return 1.4
}

// labelItems draws an edge label with a background at center c.
func labelItems(e *env, lines []string, c Point, p Paint) []Item {
	tw, th := e.textSize(lines, e.size, false)
	box := Rect{c.X - tw/2 - 4, c.Y - th/2 - 2, tw + 8, th + 4}
	return []Item{
		{Path: rectPath(box, 3), Fill: Paint{Role: RoleLabelBg}},
		{Text: e.text(lines, c.X, c.Y, e.size, false, p)},
	}
}
