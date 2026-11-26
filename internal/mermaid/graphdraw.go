package mermaid

// Shared drawing for graph-based diagrams (state, class, ER): the caller
// builds a graph whose nodes carry a draw function, the helper lays it out
// and emits clusters, edges with markers and labels, then nodes.

type gdNode struct {
	id    string
	label string
	shape string
	w, h  float64
	draw  func(c Point, w, h float64) []Item
}

type gdEdge struct {
	from, to           int
	label              []string
	dash               Dash
	width              float64
	start, end         marker
	startNote, endNote string // cardinalities near the ends
	stroke             Paint
	invisible          bool
}

type gdCluster struct {
	id     string
	title  []string
	parent int
	fill   Paint
}

type graphDiagram struct {
	kind     string
	dir      string
	nodes    []*gdNode
	nodeOf   []int // cluster per node
	edges    []*gdEdge
	clusters []*gdCluster
	nodeSep  float64
	rankSep  float64
	portStep float64
}

func (gd *graphDiagram) add(n *gdNode, cluster int) int {
	gd.nodes = append(gd.nodes, n)
	gd.nodeOf = append(gd.nodeOf, cluster)
	return len(gd.nodes) - 1
}

func (gd *graphDiagram) render(e *env) *Diagram {
	g := &graph{dir: gd.dir, nodeSep: gd.nodeSep, rankSep: gd.rankSep, portStep: gd.portStep}
	for i, n := range gd.nodes {
		g.nodes = append(g.nodes, &gNode{w: n.w, h: n.h, shape: n.shape, cluster: gd.nodeOf[i]})
	}
	for _, c := range gd.clusters {
		tw, th := e.textSize(c.title, e.size, true)
		if len(c.title) == 0 || c.title[0] == "" {
			th = 0
		}
		g.clusters = append(g.clusters, &gCluster{parent: c.parent, tw: tw, th: th})
	}
	small := e.size * 0.86
	for _, ed := range gd.edges {
		ge := &gEdge{from: ed.from, to: ed.to}
		if len(ed.label) > 0 {
			tw, th := e.textSize(ed.label, e.size, false)
			ge.lw, ge.lh = tw+8, th+4
		}
		ge.startCut = markerLen(ed.start, ed.width)
		ge.endCut = markerLen(ed.end, ed.width)
		g.edges = append(g.edges, ge)
	}
	g.layout()
	d := &Diagram{Kind: gd.kind, Width: g.width, Height: g.height}
	for i, c := range gd.clusters {
		gc := g.clusters[i]
		if gc.empty {
			continue
		}
		fill := c.fill
		if fill.Role == RoleNone && fill.Color == "" {
			fill = Paint{Role: RoleClusterFill}
		}
		d.Items = append(d.Items, Item{Path: rectPath(gc.box, 8), Fill: fill, Stroke: Paint{Role: RoleClusterStroke}, Width: 1})
		if len(c.title) > 0 && c.title[0] != "" {
			d.Items = append(d.Items, Item{Text: e.textAt(c.title, gc.box.X+gc.box.W/2, gc.box.Y+4, e.size, true, AnchorMiddle, Paint{Role: RoleText})})
		}
		d.Nodes = append(d.Nodes, NodeInfo{ID: c.id, Label: joinLines(c.title), Shape: "cluster", Box: gc.box})
	}
	var labels []Item
	for i, ed := range gd.edges {
		ge := g.edges[i]
		d.Edges = append(d.Edges, EdgeInfo{From: gd.nodes[ed.from].id, To: gd.nodes[ed.to].id, Label: joinLines(ed.label), Points: ge.pts})
		if ed.invisible {
			continue
		}
		stroke := ed.stroke
		if stroke.Role == RoleNone && stroke.Color == "" {
			stroke = Paint{Role: RoleLine}
		}
		d.Items = append(d.Items, Item{Path: ge.path, Stroke: stroke, Width: ed.width, Dash: ed.dash})
		d.Items = append(d.Items, markerItems(ed.end, ge.endTip, ge.endDir, ed.width, stroke)...)
		d.Items = append(d.Items, markerItems(ed.start, ge.startTip, ge.startDir, ed.width, stroke)...)
		if ge.hasLabel {
			labels = append(labels, labelItems(e, ed.label, ge.label, Paint{Role: RoleText})...)
		}
		for _, end := range []struct {
			txt      string
			tip, dir Point
		}{{ed.startNote, ge.startTip, ge.startDir}, {ed.endNote, ge.endTip, ge.endDir}} {
			if end.txt == "" {
				continue
			}
			// Beside the edge, a little away from the node.
			p := Point{end.tip.X - end.dir.X*22 + end.dir.Y*12, end.tip.Y - end.dir.Y*22 - end.dir.X*12}
			labels = append(labels, Item{Text: e.text([]string{end.txt}, p.X, p.Y, small, false, Paint{Role: RoleTextMuted})})
		}
	}
	d.Items = append(d.Items, labels...)
	for i, n := range gd.nodes {
		gn := g.nodes[i]
		c := Point{gn.x, gn.y}
		d.Items = append(d.Items, n.draw(c, gn.w, gn.h)...)
		d.Nodes = append(d.Nodes, NodeInfo{ID: n.id, Label: n.label, Shape: n.shape, Box: Rect{gn.x - gn.w/2, gn.y - gn.h/2, gn.w, gn.h}})
	}
	// Cardinality texts may stick out: fit to everything.
	d.fit(e, 8)
	return d
}
