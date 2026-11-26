package mermaid

import (
	"math"
	"sort"
)

// Layered graph layout (Sugiyama style) shared by flowcharts, state, class
// and ER diagrams:
//
//  1. cycles are broken by reversing DFS back edges,
//  2. nodes get ranks by longest path (every edge spans two ranks so edge
//     labels and bends get their own in-between layer), sources are pulled
//     down next to their successors,
//  3. long edges are split by dummy nodes (one carries the edge label),
//  4. crossings are reduced by barycenter sweeps and adjacent transposition
//     with subgraph members kept contiguous,
//  5. cross-axis coordinates minimize the weighted distance to neighbors
//     (exactly, per layer, by isotonic regression), long edges straight,
//  6. subgraph boxes are kept free of foreign nodes,
//  7. edges become smooth cubic curves leaving and entering nodes along
//     the rank axis, ending at the node borders.
//
// The layout runs top-to-bottom in "layout space" (x: cross axis, y: rank
// axis) and is mapped to the requested direction at the end. Everything is
// deterministic: ties keep input order.

// gNode is an input node with its size in screen orientation; x, y is the
// output center.
type gNode struct {
	w, h    float64
	shape   string // for border ports (diamond, circle, …)
	cluster int    // innermost cluster or -1
	x, y    float64
}

// gEdge is an input edge; label size 0 means no label. Cuts shorten the
// curve at the ends for markers. Outputs: path, the anchor points (screen,
// source border → target border), label center and the end directions.
type gEdge struct {
	from, to           int
	lw, lh             float64
	startCut, endCut   float64
	path               path
	pts                []Point
	label              Point
	startDir, endDir   Point // unit vectors pointing out of the curve at its ends
	startTip, endTip   Point
	hasLabel, selfLoop bool
}

// gCluster is a subgraph: parent cluster (-1: top), title size; box is the
// output.
type gCluster struct {
	parent int
	tw, th float64
	box    Rect
	empty  bool
}

type graph struct {
	dir      string // TB, BT, LR, RL
	nodes    []*gNode
	edges    []*gEdge
	clusters []*gCluster
	nodeSep  float64
	rankSep  float64
	clusPad  float64
	portStep float64 // distance of edge ports on a shared node side
	width    float64
	height   float64
}

// lnode is a node in layout space (real node or dummy).
type lnode struct {
	real      int // index into graph.nodes, or -1
	w, h      float64
	rank      int
	chain     []int // clusters from outermost to innermost
	x, y      float64
	up, down  []ladj
	pos       int
	extraR    float64 // self loop room on the cross-high side
	labelEdge int     // edge index whose label this dummy carries, or -1
}

type ladj struct {
	n int
	w float64
}

func (g *graph) horizontal() bool { return g.dir == "LR" || g.dir == "RL" }

func (g *graph) layout() {
	if g.nodeSep == 0 {
		g.nodeSep = 40
	}
	if g.rankSep == 0 {
		g.rankSep = 50
	}
	if g.clusPad == 0 {
		g.clusPad = 14
	}
	n := len(g.nodes)
	if n == 0 {
		g.width, g.height = 16, 16
		return
	}
	hz := g.horizontal()
	// Layout-space sizes.
	ls := make([]*lnode, n)
	for i, nd := range g.nodes {
		w, h := nd.w, nd.h
		if hz {
			w, h = h, w
		}
		ls[i] = &lnode{real: i, w: w, h: h, chain: g.chain(nd.cluster), labelEdge: -1}
	}

	// 1. Cycle breaking (DFS, input order).
	reversed := make([]bool, len(g.edges))
	out := make([][]int, n)
	for ei, e := range g.edges {
		if e.from == e.to {
			e.selfLoop = true
			continue
		}
		out[e.from] = append(out[e.from], ei)
	}
	state := make([]uint8, n) // 0 new, 1 on stack, 2 done
	var dfs func(v int)
	dfs = func(v int) {
		state[v] = 1
		for _, ei := range out[v] {
			t := g.edges[ei].to
			switch state[t] {
			case 0:
				dfs(t)
			case 1:
				reversed[ei] = true
			}
		}
		state[v] = 2
	}
	for v := range n {
		if state[v] == 0 {
			dfs(v)
		}
	}
	src := func(ei int) int {
		if reversed[ei] {
			return g.edges[ei].to
		}
		return g.edges[ei].from
	}
	dst := func(ei int) int {
		if reversed[ei] {
			return g.edges[ei].from
		}
		return g.edges[ei].to
	}

	// 2. Ranking: longest path from the sources, every edge spans 2.
	preds := make([][]int, n)
	succs := make([][]int, n)
	indeg := make([]int, n)
	for ei, e := range g.edges {
		if e.selfLoop {
			continue
		}
		s, d := src(ei), dst(ei)
		succs[s] = append(succs[s], d)
		preds[d] = append(preds[d], s)
		indeg[d]++
	}
	rank := make([]int, n)
	var queue []int
	for v := range n {
		if indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	var topo []int
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		topo = append(topo, v)
		for _, s := range succs[v] {
			rank[s] = max(rank[s], rank[v]+2)
			indeg[s]--
			if indeg[s] == 0 {
				queue = append(queue, s)
			}
		}
	}
	// Pull sources (and chains above them) down towards their successors.
	for i := len(topo) - 1; i >= 0; i-- {
		v := topo[i]
		if len(preds[v]) > 0 || len(succs[v]) == 0 {
			continue
		}
		m := math.MaxInt
		for _, s := range succs[v] {
			m = min(m, rank[s]-2)
		}
		rank[v] = max(rank[v], m)
	}
	for i, l := range ls {
		l.rank = rank[i]
	}
	maxRank := 0
	for _, r := range rank {
		maxRank = max(maxRank, r)
	}
	// Cluster rank ranges (over real members, nested included).
	cmin := make([]int, len(g.clusters))
	cmax := make([]int, len(g.clusters))
	for c := range g.clusters {
		cmin[c], cmax[c] = math.MaxInt, -1
	}
	for i, l := range ls {
		for _, c := range l.chain {
			cmin[c] = min(cmin[c], rank[i])
			cmax[c] = max(cmax[c], rank[i])
		}
	}
	for c, cl := range g.clusters {
		cl.empty = cmax[c] < 0
	}

	// 3. Dummy nodes.
	type route struct{ nodes []int }
	routes := make([]route, len(g.edges))
	for ei, e := range g.edges {
		if e.selfLoop {
			ls[e.from].extraR = max(ls[e.from].extraR, 34+g.crossSize(e.lw, e.lh))
			continue
		}
		s, d := src(ei), dst(ei)
		lca := commonPrefix(ls[s].chain, ls[d].chain)
		labelRank := -1
		if e.lw > 0 || e.lh > 0 {
			mid := (rank[s] + rank[d]) / 2
			if (mid-rank[s])%2 == 0 {
				mid--
			}
			labelRank = max(mid, rank[s]+1)
		}
		r := route{nodes: []int{s}}
		prev := s
		// Back edges barely pull their end nodes sideways.
		scale := 1.0
		if reversed[ei] {
			scale = 0.01
		}
		for rk := rank[s] + 1; rk < rank[d]; rk++ {
			dn := &lnode{real: -1, rank: rk, labelEdge: -1, chain: g.dummyChain(ls[s].chain, ls[d].chain, lca, rk, cmin, cmax)}
			if rk == labelRank {
				dn.w, dn.h = e.lw, e.lh
				if hz {
					dn.w, dn.h = e.lh, e.lw
				}
				dn.labelEdge = ei
			}
			ls = append(ls, dn)
			id := len(ls) - 1
			r.nodes = append(r.nodes, id)
			link(ls, prev, id, scale)
			prev = id
		}
		link(ls, prev, d, scale)
		r.nodes = append(r.nodes, d)
		routes[ei] = r
	}

	// 4. Ordering.
	layers := make([][]int, maxRank+1)
	visited := make([]bool, len(ls))
	var place func(v int)
	place = func(v int) {
		if visited[v] {
			return
		}
		visited[v] = true
		layers[ls[v].rank] = append(layers[ls[v].rank], v)
		for _, a := range ls[v].down {
			place(a.n)
		}
	}
	for v := range n {
		if len(ls[v].up) == 0 {
			place(v)
		}
	}
	for v := range ls {
		place(v)
	}
	// Group clusters from the start.
	for _, layer := range layers {
		bary := map[int]float64{}
		for i, v := range layer {
			bary[v] = float64(i)
		}
		copy(layer, arrange(ls, layer, bary, 0))
	}
	setPos(ls, layers)
	best := cloneLayers(layers)
	bestC := crossings(ls, layers)
	for it := 0; it < 12 && bestC > 0; it++ {
		if it%2 == 0 {
			for l := 1; l < len(layers); l++ {
				sortByBary(ls, layers, l, true)
			}
		} else {
			for l := len(layers) - 2; l >= 0; l-- {
				sortByBary(ls, layers, l, false)
			}
		}
		transpose(ls, layers)
		if c := crossings(ls, layers); c < bestC {
			bestC, best = c, cloneLayers(layers)
		}
	}
	layers = best
	setPos(ls, layers)

	// 5. Cross-axis coordinates.
	g.assignX(ls, layers)
	rn := make([][]int, len(routes))
	for i := range routes {
		rn[i] = routes[i].nodes
	}
	g.alignChains(ls, layers, rn)
	g.separateClusters(ls, layers)

	// Rank-axis coordinates.
	titleLow := g.dir == "TB" || g.dir == "TD" || g.dir == ""
	titleHigh := g.dir == "BT"
	thick := make([]float64, len(layers))
	extraLow := make([]float64, len(layers))
	extraHigh := make([]float64, len(layers))
	for l, layer := range layers {
		for _, v := range layer {
			nd := ls[v]
			thick[l] = max(thick[l], nd.h)
			if nd.real < 0 {
				continue
			}
			lo, hi := 0.0, 0.0
			for _, c := range nd.chain {
				cl := g.clusters[c]
				if cmin[c] == l {
					lo += g.clusPad
					if titleLow {
						lo += cl.th + 4
					}
				}
				if cmax[c] == l {
					hi += g.clusPad
					if titleHigh {
						hi += cl.th + 4
					}
				}
			}
			extraLow[l] = max(extraLow[l], lo)
			extraHigh[l] = max(extraHigh[l], hi)
		}
	}
	y := 0.0
	for l := range layers {
		if l > 0 {
			y += thick[l-1]/2 + g.rankSep/2 + extraHigh[l-1] + extraLow[l] + thick[l]/2
		}
		for _, v := range layers[l] {
			ls[v].y = y
		}
	}

	// 6. Cluster boxes (layout space).
	cbox := make([]Rect, len(g.clusters))
	for c := len(g.clusters) - 1; c >= 0; c-- {
		cbox[c] = g.layoutClusterBox(ls, c, cbox)
	}

	// 7. Edges (layout space), then map everything to the screen.
	ports := g.ports(ls, rn)
	for ei, e := range g.edges {
		if e.selfLoop {
			g.selfLoop(e, ls[e.from])
			continue
		}
		var pts []Point
		r := routes[ei].nodes
		a, b := ls[r[0]], ls[r[len(r)-1]]
		pa, pb := ports[ei][0], ports[ei][1]
		pts = append(pts, Point{a.x + pa, a.y + g.depth(a, pa)})
		for _, d := range r[1 : len(r)-1] {
			if len(r) == 3 && ls[d].labelEdge != ei {
				// A single plain dummy is no bend: one smooth curve.
				continue
			}
			pts = append(pts, Point{ls[d].x, ls[d].y})
			if ls[d].labelEdge == ei {
				e.label = Point{ls[d].x, ls[d].y}
				e.hasLabel = true
			}
		}
		pts = append(pts, Point{b.x + pb, b.y - g.depth(b, pb)})
		if reversed[ei] {
			for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
				pts[i], pts[j] = pts[j], pts[i]
			}
		}
		g.curve(e, pts)
	}
	g.toScreen(ls, cbox)
}

// chain returns the cluster path of cluster c from the outermost.
func (g *graph) chain(c int) []int {
	var out []int
	for c >= 0 {
		out = append([]int{c}, out...)
		c = g.clusters[c].parent
	}
	return out
}

func (g *graph) crossSize(w, h float64) float64 {
	if g.horizontal() {
		return h
	}
	return w
}

// dummyChain is the cluster path of a dummy at rank rk of an edge between
// nodes with chains a and b: the deepest cluster of either endpoint whose
// rank range contains rk, else the common ancestors.
func (g *graph) dummyChain(a, b []int, lca, rk int, cmin, cmax []int) []int {
	best := a[:lca]
	for _, ch := range [][]int{a, b} {
		for k := len(ch); k > lca; k-- {
			c := ch[k-1]
			if rk >= cmin[c]-1 && rk <= cmax[c]+1 {
				if k > len(best) {
					best = ch[:k]
				}
				break
			}
		}
	}
	return best
}

func commonPrefix(a, b []int) int {
	k := 0
	for k < len(a) && k < len(b) && a[k] == b[k] {
		k++
	}
	return k
}

func link(ls []*lnode, u, v int, scale float64) {
	w := 1.0 * scale
	switch {
	case ls[u].real < 0 && ls[v].real < 0:
		w = 8
	case ls[u].real < 0 || ls[v].real < 0:
		w = 2 * scale
	}
	ls[u].down = append(ls[u].down, ladj{v, w})
	ls[v].up = append(ls[v].up, ladj{u, w})
}

func setPos(ls []*lnode, layers [][]int) {
	for _, layer := range layers {
		for i, v := range layer {
			ls[v].pos = i
		}
	}
}

func cloneLayers(layers [][]int) [][]int {
	out := make([][]int, len(layers))
	for i, l := range layers {
		out[i] = append([]int(nil), l...)
	}
	return out
}

// arrange orders the nodes of a layer by barycenter while keeping the
// members of every cluster contiguous (clusters are ordered by the mean
// barycenter of their members in the layer).
func arrange(ls []*lnode, items []int, bary map[int]float64, depth int) []int {
	type entry struct {
		key   float64
		nodes []int
		first int
		group bool
	}
	var entries []*entry
	groups := map[int]*entry{}
	for i, v := range items {
		ch := ls[v].chain
		if len(ch) <= depth {
			entries = append(entries, &entry{key: bary[v], nodes: []int{v}, first: i})
			continue
		}
		c := ch[depth]
		en := groups[c]
		if en == nil {
			en = &entry{first: i, group: true}
			groups[c] = en
			entries = append(entries, en)
		}
		en.nodes = append(en.nodes, v)
		en.key += bary[v]
	}
	for _, en := range entries {
		if en.group {
			en.key /= float64(len(en.nodes))
			en.nodes = arrange(ls, en.nodes, bary, depth+1)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].key != entries[j].key {
			return entries[i].key < entries[j].key
		}
		return entries[i].first < entries[j].first
	})
	out := make([]int, 0, len(items))
	for _, en := range entries {
		out = append(out, en.nodes...)
	}
	return out
}

// sortByBary reorders layer l by the barycenter of its neighbors in the
// layer above (down) or below.
func sortByBary(ls []*lnode, layers [][]int, l int, down bool) {
	layer := layers[l]
	bary := map[int]float64{}
	for _, v := range layer {
		adj := ls[v].up
		if !down {
			adj = ls[v].down
		}
		if len(adj) == 0 {
			bary[v] = float64(ls[v].pos)
			continue
		}
		s, w := 0.0, 0.0
		for _, a := range adj {
			s += float64(ls[a.n].pos) * a.w
			w += a.w
		}
		bary[v] = s / w
	}
	copy(layer, arrange(ls, layer, bary, 0))
	for i, v := range layer {
		ls[v].pos = i
	}
}

// crossings counts edge crossings between all adjacent layers.
func crossings(ls []*lnode, layers [][]int) int {
	total := 0
	for l := 0; l+1 < len(layers); l++ {
		var es [][2]int
		for _, v := range layers[l] {
			for _, a := range ls[v].down {
				es = append(es, [2]int{ls[v].pos, ls[a.n].pos})
			}
		}
		for i := range es {
			for j := i + 1; j < len(es); j++ {
				if (es[i][0]-es[j][0])*(es[i][1]-es[j][1]) < 0 {
					total++
				}
			}
		}
	}
	return total
}

// pairCrossings counts crossings between the edges of u and v (u left of
// v) towards both adjacent layers.
func pairCrossings(ls []*lnode, u, v int) int {
	c := 0
	for _, adj := range [2]func(*lnode) []ladj{func(n *lnode) []ladj { return n.up }, func(n *lnode) []ladj { return n.down }} {
		for _, a := range adj(ls[u]) {
			for _, b := range adj(ls[v]) {
				if ls[a.n].pos > ls[b.n].pos {
					c++
				}
			}
		}
	}
	return c
}

func sameChain(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// transpose swaps adjacent nodes of the same cluster while that reduces
// crossings.
func transpose(ls []*lnode, layers [][]int) {
	for round := 0; round < 4; round++ {
		improved := false
		for _, layer := range layers {
			for i := 0; i+1 < len(layer); i++ {
				u, v := layer[i], layer[i+1]
				if !sameChain(ls[u].chain, ls[v].chain) {
					continue
				}
				if pairCrossings(ls, v, u) < pairCrossings(ls, u, v) {
					layer[i], layer[i+1] = v, u
					ls[u].pos, ls[v].pos = i+1, i
					improved = true
				}
			}
		}
		if !improved {
			return
		}
	}
}

// sep is the minimum center distance of adjacent nodes u (left) and v.
func (g *graph) sep(u, v *lnode) float64 {
	gap := g.nodeSep
	switch {
	case u.real < 0 && v.real < 0:
		gap = 12
	case u.real < 0 || v.real < 0:
		gap = g.nodeSep / 2
	}
	s := (u.w+v.w)/2 + gap + u.extraR
	k := commonPrefix(u.chain, v.chain)
	s += g.clusPad * float64(len(u.chain)-k)
	for _, c := range v.chain[k:] {
		s += g.clusPad
		if g.horizontal() {
			s += g.clusters[c].th + 4
		}
	}
	return s
}

// assignX places nodes on the cross axis: alternating sweeps move every
// node to the weighted mean of its neighbors in both adjacent layers,
// subject to the separation constraints of its layer (solved exactly by
// pool-adjacent-violators).
func (g *graph) assignX(ls []*lnode, layers [][]int) {
	for _, layer := range layers {
		x := 0.0
		for i, v := range layer {
			if i > 0 {
				x += g.sep(ls[layer[i-1]], ls[v])
			}
			ls[v].x = x
		}
	}
	for it := 0; it < 40; it++ {
		order := make([]int, len(layers))
		for i := range order {
			if it%2 == 0 {
				order[i] = i
			} else {
				order[i] = len(layers) - 1 - i
			}
		}
		for _, l := range order {
			layer := layers[l]
			want := make([]float64, len(layer))
			wt := make([]float64, len(layer))
			for i, v := range layer {
				s, w := 0.0, 0.0
				for _, a := range ls[v].up {
					s += ls[a.n].x * a.w
					w += a.w
				}
				for _, a := range ls[v].down {
					s += ls[a.n].x * a.w
					w += a.w
				}
				if w == 0 {
					want[i], wt[i] = ls[v].x, 0.01
					continue
				}
				want[i], wt[i] = s/w, w
			}
			seps := make([]float64, len(layer))
			for i := 1; i < len(layer); i++ {
				seps[i] = g.sep(ls[layer[i-1]], ls[layer[i]])
			}
			xs := pav(want, wt, seps)
			for i, v := range layer {
				ls[v].x = xs[i]
			}
		}
	}
}

// pav minimizes Σ w_i (x_i − d_i)² subject to x_i − x_{i−1} ≥ s_i.
func pav(d, w, s []float64) []float64 {
	n := len(d)
	off := make([]float64, n)
	for i := 1; i < n; i++ {
		off[i] = off[i-1] + s[i]
	}
	type block struct {
		sw, swx float64
		start   int
		end     int
	}
	var bs []block
	for i := range n {
		bs = append(bs, block{sw: w[i], swx: w[i] * (d[i] - off[i]), start: i, end: i})
		for len(bs) > 1 {
			a, b := bs[len(bs)-2], bs[len(bs)-1]
			if a.swx/a.sw <= b.swx/b.sw {
				break
			}
			bs = bs[:len(bs)-2]
			bs = append(bs, block{sw: a.sw + b.sw, swx: a.swx + b.swx, start: a.start, end: b.end})
		}
	}
	out := make([]float64, n)
	for _, b := range bs {
		v := b.swx / b.sw
		for i := b.start; i <= b.end; i++ {
			out[i] = v + off[i]
		}
	}
	return out
}

// separateClusters pushes nodes that do not belong to a cluster out of
// the cluster's cross-axis extent in the layers it spans.
func (g *graph) separateClusters(ls []*lnode, layers [][]int) {
	if len(g.clusters) == 0 {
		return
	}
	member := func(v *lnode, c int) bool {
		for _, x := range v.chain {
			if x == c {
				return true
			}
		}
		return false
	}
	for it := 0; it < 30; it++ {
		moved := false
		for c := range g.clusters {
			lo, hi := math.Inf(1), math.Inf(-1)
			rlo, rhi := math.MaxInt, -1
			for _, v := range ls {
				if v.real >= 0 && member(v, c) {
					lo, hi = min(lo, v.x-v.w/2), max(hi, v.x+v.w/2+v.extraR)
					rlo, rhi = min(rlo, v.rank), max(rhi, v.rank)
				}
			}
			if rhi < 0 {
				continue
			}
			for _, v := range ls {
				if v.real < 0 && member(v, c) && v.rank > rlo && v.rank < rhi {
					lo, hi = min(lo, v.x-v.w/2), max(hi, v.x+v.w/2)
				}
			}
			pad := g.clusPad * float64(1+g.depthBelow(c))
			lo, hi = lo-pad, hi+pad
			if g.horizontal() {
				lo -= g.clusters[c].th + 4
			}
			for l := rlo; l <= rhi; l++ {
				layer := layers[l]
				first, last := -1, -1
				for i, v := range layer {
					if member(ls[v], c) {
						if first < 0 {
							first = i
						}
						last = i
					}
				}
				for i, v := range layer {
					nd := ls[v]
					if member(nd, c) {
						continue
					}
					gap := 8.0
					if nd.real >= 0 {
						gap = g.nodeSep / 2
					}
					left := first >= 0 && i < first || first < 0 && nd.x < (lo+hi)/2
					// The node's own cluster frames face this cluster too.
					for _, oc := range nd.chain {
						if !g.inside(c, oc) {
							gap += g.clusPad
							if g.horizontal() && !left {
								gap += g.clusters[oc].th + 4
							}
						}
					}
					if left {
						if d := nd.x + nd.w/2 + nd.extraR + gap - lo; d > 0.5 {
							for j := 0; j <= i; j++ {
								ls[layer[j]].x -= d
							}
							moved = true
						}
					} else if first < 0 || i > last {
						if d := hi + gap - (nd.x - nd.w/2); d > 0.5 {
							for j := i; j < len(layer); j++ {
								ls[layer[j]].x += d
							}
							moved = true
						}
					}
				}
			}
		}
		if !moved {
			return
		}
	}
}

// depthBelow is the nesting depth of clusters inside c.
func (g *graph) depthBelow(c int) int {
	d := 0
	for k, cl := range g.clusters {
		if cl.parent == c {
			d = max(d, 1+g.depthBelow(k))
		}
	}
	return d
}

// layoutClusterBox is the layout-space box of cluster c (children boxes
// are already computed).
func (g *graph) layoutClusterBox(ls []*lnode, c int, boxes []Rect) Rect {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, v := range ls {
		if v.real >= 0 && len(v.chain) > 0 && v.chain[len(v.chain)-1] == c {
			x0, x1 = min(x0, v.x-v.w/2), max(x1, v.x+v.w/2+v.extraR)
			y0, y1 = min(y0, v.y-v.h/2), max(y1, v.y+v.h/2)
		}
	}
	// Dummies of edges inside the cluster (within its rank range).
	for _, v := range ls {
		if v.real < 0 && len(v.chain) > 0 && v.chain[len(v.chain)-1] == c && v.y >= y0 && v.y <= y1 {
			x0, x1 = min(x0, v.x-v.w/2), max(x1, v.x+v.w/2)
		}
	}
	for k, cl := range g.clusters {
		if cl.parent == c && !cl.empty {
			b := boxes[k]
			x0, x1 = min(x0, b.X), max(x1, b.X+b.W)
			y0, y1 = min(y0, b.Y), max(y1, b.Y+b.H)
		}
	}
	if math.IsInf(x0, 1) {
		return Rect{}
	}
	p := g.clusPad
	t := g.clusters[c].th + 4
	x0, x1, y0, y1 = x0-p, x1+p, y0-p, y1+p
	switch g.dir {
	case "LR", "RL":
		x0 -= t
	case "BT":
		y1 += t
	default:
		y0 -= t
	}
	// Wide enough for the title (screen x is layout x for TB/BT, y else).
	need := g.clusters[c].tw + 2*p
	if g.horizontal() {
		if y1-y0 < need {
			d := (need - (y1 - y0)) / 2
			y0, y1 = y0-d, y1+d
		}
	} else if x1-x0 < need {
		d := (need - (x1 - x0)) / 2
		x0, x1 = x0-d, x1+d
	}
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

// curve builds the smooth path through pts (layout space): cubic segments
// with rank-axis tangents at every point; the ends are shortened by the
// marker cuts.
func (g *graph) curve(e *gEdge, pts []Point) {
	n := len(pts)
	e.endTip, e.startTip = pts[n-1], pts[0]
	sgnEnd := 1.0
	if pts[n-1].Y < pts[n-2].Y {
		sgnEnd = -1
	}
	sgnStart := 1.0
	if pts[1].Y < pts[0].Y {
		sgnStart = -1
	}
	e.endDir = Point{0, sgnEnd}
	e.startDir = Point{0, -sgnStart}
	q := append([]Point(nil), pts...)
	q[n-1].Y -= sgnEnd * e.endCut
	q[0].Y += sgnStart * e.startCut
	var p path
	p.move(q[0].X, q[0].Y)
	for i := 0; i+1 < n; i++ {
		a, b := q[i], q[i+1]
		dy := (b.Y - a.Y) / 2
		if math.Abs(a.X-b.X) < 3 {
			p.line(b.X, b.Y)
			continue
		}
		p.cube(Point{a.X, a.Y + dy}, Point{b.X, b.Y - dy}, b)
	}
	e.path = p
	e.pts = pts
}

// selfLoop routes an edge from a node to itself around its cross-high
// side (layout space).
func (g *graph) selfLoop(e *gEdge, v *lnode) {
	r := v.x + v.w/2
	top, bot := v.y-v.h/4, v.y+v.h/4
	out := 26.0
	var p path
	p.move(r, top)
	tip := Point{r, bot}
	end := Point{r + e.endCut, bot}
	p.cube(Point{r + out, top - v.h/2}, Point{r + out + 8, bot + v.h/2}, end)
	e.path = p
	e.pts = []Point{{r, top}, {r + out, v.y}, tip}
	e.endTip, e.endDir = tip, Point{-1, 0}
	e.startTip, e.startDir = Point{r, top}, Point{-1, 0}
	if e.lw > 0 || e.lh > 0 {
		e.hasLabel = true
		e.label = Point{r + out + 6 + g.crossSize(e.lw, e.lh)/2, v.y}
	}
}

// toScreen maps layout space to the requested direction, moves the
// drawing to a margin and fills node centers, edge geometry and cluster
// boxes.
func (g *graph) toScreen(ls []*lnode, cbox []Rect) {
	m := func(p Point) Point {
		switch g.dir {
		case "BT":
			return Point{p.X, -p.Y}
		case "LR":
			return Point{p.Y, p.X}
		case "RL":
			return Point{-p.Y, p.X}
		}
		return p
	}
	mv := func(p Point) Point { return m(p) } // linear: directions map alike
	mr := func(r Rect) Rect {
		a, b := m(Point{r.X, r.Y}), m(Point{r.X + r.W, r.Y + r.H})
		return Rect{min(a.X, b.X), min(a.Y, b.Y), math.Abs(b.X - a.X), math.Abs(b.Y - a.Y)}
	}
	for i, nd := range g.nodes {
		c := m(Point{ls[i].x, ls[i].y})
		nd.x, nd.y = c.X, c.Y
	}
	for _, e := range g.edges {
		for i := range e.path {
			for k := range e.path[i].Pts {
				e.path[i].Pts[k] = m(e.path[i].Pts[k])
			}
		}
		for i := range e.pts {
			e.pts[i] = m(e.pts[i])
		}
		e.label = m(e.label)
		e.startTip, e.endTip = m(e.startTip), m(e.endTip)
		e.startDir, e.endDir = mv(e.startDir), mv(e.endDir)
	}
	for c, cl := range g.clusters {
		if !cl.empty {
			cl.box = mr(cbox[c])
		}
	}
	// Bounds.
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	grow := func(x, y float64) {
		x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
	}
	for _, nd := range g.nodes {
		grow(nd.x-nd.w/2, nd.y-nd.h/2)
		grow(nd.x+nd.w/2, nd.y+nd.h/2)
	}
	for _, e := range g.edges {
		for _, s := range e.path {
			for _, p := range s.Pts {
				grow(p.X, p.Y)
			}
		}
		if e.hasLabel {
			grow(e.label.X-e.lw/2, e.label.Y-e.lh/2)
			grow(e.label.X+e.lw/2, e.label.Y+e.lh/2)
		}
	}
	for _, cl := range g.clusters {
		if !cl.empty {
			grow(cl.box.X, cl.box.Y)
			grow(cl.box.X+cl.box.W, cl.box.Y+cl.box.H)
		}
	}
	const margin = 8
	dx, dy := margin-x0, margin-y0
	sh := func(p *Point) { p.X += dx; p.Y += dy }
	for _, nd := range g.nodes {
		nd.x += dx
		nd.y += dy
	}
	for _, e := range g.edges {
		for i := range e.path {
			for k := range e.path[i].Pts {
				sh(&e.path[i].Pts[k])
			}
		}
		for i := range e.pts {
			sh(&e.pts[i])
		}
		sh(&e.label)
		sh(&e.startTip)
		sh(&e.endTip)
	}
	for _, cl := range g.clusters {
		cl.box.X += dx
		cl.box.Y += dy
	}
	g.width = x1 - x0 + 2*margin
	g.height = y1 - y0 + 2*margin
}

// ports spreads the attachment points of several edges on the same side
// of a node along that side, ordered by where the edges go, so they do not
// all meet in one point. It returns the cross-axis offsets of the source
// and target ends per edge.
func (g *graph) ports(ls []*lnode, routes [][]int) [][2]float64 {
	type end struct {
		edge, which int
		toward      float64
	}
	sides := map[[2]int][]end{} // (node, side 0 low/1 high)
	for ei, r := range routes {
		if len(r) < 2 {
			continue
		}
		sides[[2]int{r[0], 1}] = append(sides[[2]int{r[0], 1}], end{ei, 0, ls[r[1]].x})
		last := len(r) - 1
		sides[[2]int{r[last], 0}] = append(sides[[2]int{r[last], 0}], end{ei, 1, ls[r[last-1]].x})
	}
	out := make([][2]float64, len(routes))
	for key, ends := range sides {
		if len(ends) < 2 {
			continue
		}
		v := ls[key[0]]
		sort.SliceStable(ends, func(i, j int) bool {
			if ends[i].toward != ends[j].toward {
				return ends[i].toward < ends[j].toward
			}
			return ends[i].edge < ends[j].edge
		})
		span := v.w * 0.5
		if sh := g.nodes[v.real].shape; sh == shapeDiamond || sh == shapeCircle || sh == shapeDoubleCircle {
			span = v.w * 0.3
		}
		ps := g.portStep
		if ps == 0 {
			ps = 16
		}
		step := min(ps, span/float64(len(ends)-1))
		for i, en := range ends {
			out[en.edge][en.which] = (float64(i) - float64(len(ends)-1)/2) * step
		}
	}
	return out
}

// depth is the distance from the center of v to its border along the rank
// axis at cross offset dx (layout space).
func (g *graph) depth(v *lnode, dx float64) float64 {
	if v.real < 0 {
		return v.h / 2
	}
	hw, hh := v.w/2, v.h/2
	t := math.Min(math.Abs(dx)/hw, 1)
	switch g.nodes[v.real].shape {
	case shapeDiamond:
		return hh * (1 - t)
	case shapeCircle, shapeDoubleCircle, shapeStart, shapeEnd:
		return hh * math.Sqrt(1-t*t)
	case shapeStadium:
		if g.horizontal() {
			// Layout cross axis is the screen y: round ends on the rank axis.
			r := hw
			return hh - r + math.Sqrt(max(r*r-dx*dx, 0))
		}
	case shapeHexagon:
		if g.horizontal() {
			return hh - (hh-max(hh-hw/2, 0))*t
		}
	}
	return hh
}

// inside reports whether cluster c is oc or nested in it.
func (g *graph) inside(c, oc int) bool {
	for ; c >= 0; c = g.clusters[c].parent {
		if c == oc {
			return true
		}
	}
	return false
}

// alignChains straightens one-to-one connections: when an edge is the only
// way out of its source and the only way into its target, source, dummies
// and target are moved to one cross coordinate if their layers have room
// (the node with more other connections keeps its place).
func (g *graph) alignChains(ls []*lnode, layers [][]int, routes [][]int) {
	room := func(v int) (lo, hi float64) {
		nd := ls[v]
		layer := layers[nd.rank]
		lo, hi = math.Inf(-1), math.Inf(1)
		if nd.pos > 0 {
			l := ls[layer[nd.pos-1]]
			lo = l.x + g.sep(l, nd)
		}
		if nd.pos+1 < len(layer) {
			r := ls[layer[nd.pos+1]]
			hi = r.x - g.sep(nd, r)
		}
		return lo, hi
	}
	for pass := 0; pass < 3; pass++ {
		for _, r := range routes {
			if len(r) < 2 {
				continue
			}
			u, v := ls[r[0]], ls[r[len(r)-1]]
			if len(u.down) != 1 || len(v.up) != 1 {
				continue
			}
			want := u.x
			if len(v.down) > len(u.up) {
				want = v.x
			}
			lo, hi := math.Inf(-1), math.Inf(1)
			for _, n := range r {
				a, b := room(n)
				lo, hi = max(lo, a), min(hi, b)
			}
			if lo > hi+0.01 {
				continue
			}
			x := min(max(want, lo), hi)
			for _, n := range r {
				ls[n].x = x
			}
		}
	}
}
