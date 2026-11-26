package mermaid

import (
	"math"
	"strconv"
	"strings"
)

type seqPart struct {
	id, label string
	actor     bool
	lines     []string
	w, h, x   float64
	box       int // group box or -1
}

type seqKind uint8

const (
	evMsg seqKind = iota
	evNote
	evActivate
	evDeactivate
	evBlock // opens a block
	evElse  // divider inside a block
	evEnd   // closes a block
)

type seqEvent struct {
	kind     seqKind
	line     int
	from, to int // participants (note: from..to span)
	text     string
	arrow    seqArrow
	act, dea bool   // message activates target / deactivates source
	side     string // note: left, right, over
	block    string // block keyword
	color    string // rect color
	num      int    // autonumber of a message (0: none)
}

type seqArrow struct {
	dash  bool
	end   marker
	start marker
}

type seqBox struct {
	label string
	color string
	parts []int
}

type sequence struct {
	parts   []*seqPart
	byID    map[string]int
	events  []seqEvent
	boxes   []seqBox
	autonum bool
	numFrom int
	numStep int
	next    int // next autonumber
}

// arrows ordered longest first.
var seqArrows = []struct {
	tok string
	a   seqArrow
}{
	{"<<-->>", seqArrow{dash: true, end: markArrow, start: markArrow}},
	{"<<->>", seqArrow{end: markArrow, start: markArrow}},
	{"-->>", seqArrow{dash: true, end: markArrow}},
	{"->>", seqArrow{end: markArrow}},
	{"--x", seqArrow{dash: true, end: markCross}},
	{"--)", seqArrow{dash: true, end: markOpen}},
	{"-->", seqArrow{dash: true}},
	{"-x", seqArrow{end: markCross}},
	{"-)", seqArrow{end: markOpen}},
	{"->", seqArrow{}},
}

func (sq *sequence) part(id string, actor bool, label string) int {
	id = strings.TrimSpace(id)
	if i, ok := sq.byID[id]; ok {
		if label != "" {
			sq.parts[i].label = label
		}
		if actor {
			sq.parts[i].actor = true
		}
		return i
	}
	if label == "" {
		label = id
	}
	sq.parts = append(sq.parts, &seqPart{id: id, label: label, actor: actor, box: -1})
	sq.byID[id] = len(sq.parts) - 1
	return len(sq.parts) - 1
}

func renderSequence(e *env, lines []srcLine) (*Diagram, error) {
	sq := &sequence{byID: map[string]int{}, numFrom: 1, numStep: 1, next: 1}
	title := ""
	depth := 0
	var stack []string // "box" or "block"
	curBox := -1
	for _, l := range lines[1:] {
		t := l.text
		kw, rest := firstWord(t)
		lkw := strings.ToLower(strings.TrimSuffix(kw, ":"))
		switch lkw {
		case "participant", "actor":
			id, label := rest, ""
			if k := strings.Index(rest, " as "); k >= 0 {
				id, label = strings.TrimSpace(rest[:k]), strings.TrimSpace(rest[k+4:])
			}
			id = strings.Trim(id, `"`)
			i := sq.part(id, lkw == "actor", label)
			if curBox >= 0 && sq.parts[i].box < 0 {
				sq.parts[i].box = curBox
				sq.boxes[curBox].parts = append(sq.boxes[curBox].parts, i)
			}
			continue
		case "create":
			// "create participant X": a normal participant here.
			k2, r2 := firstWord(rest)
			if lk := strings.ToLower(k2); lk == "participant" || lk == "actor" {
				id, label := r2, ""
				if k := strings.Index(r2, " as "); k >= 0 {
					id, label = strings.TrimSpace(r2[:k]), strings.TrimSpace(r2[k+4:])
				}
				sq.part(id, lk == "actor", label)
			}
			continue
		case "destroy", "link", "links", "properties", "details", "acctitle", "accdescr":
			continue
		case "title":
			title = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
			continue
		case "autonumber":
			sq.autonum = true
			f := strings.Fields(rest)
			if len(f) > 0 && strings.ToLower(f[0]) == "off" {
				sq.autonum = false
			}
			if len(f) > 0 {
				if n, err := strconv.Atoi(f[0]); err == nil {
					sq.numFrom = n
					sq.next = n
				}
			}
			if len(f) > 1 {
				if n, err := strconv.Atoi(f[1]); err == nil && n != 0 {
					sq.numStep = n
				}
			}
			continue
		case "activate", "deactivate":
			if rest == "" {
				return nil, errorf(l.n, "%s needs a participant", lkw)
			}
			k := evActivate
			if lkw == "deactivate" {
				k = evDeactivate
			}
			sq.events = append(sq.events, seqEvent{kind: k, line: l.n, from: sq.part(rest, false, "")})
			continue
		case "note":
			ev, err := sq.note(l, rest)
			if err != nil {
				return nil, err
			}
			sq.events = append(sq.events, ev)
			continue
		case "box":
			color, label := "", rest
			if c, r := firstWord(rest); parseColor(c) != "" || strings.HasPrefix(strings.ToLower(c), "rgb") {
				if strings.HasPrefix(strings.ToLower(c), "rgb") {
					k := strings.IndexByte(rest, ')')
					if k > 0 {
						c, r = rest[:k+1], strings.TrimSpace(rest[k+1:])
					}
				}
				color, label = parseColor(c), r
			}
			if strings.ToLower(label) == "transparent" {
				label = ""
			}
			sq.boxes = append(sq.boxes, seqBox{label: label, color: color})
			curBox = len(sq.boxes) - 1
			stack = append(stack, "box")
			continue
		case "loop", "alt", "opt", "par", "critical", "break", "rect":
			ev := seqEvent{kind: evBlock, line: l.n, block: lkw, text: rest}
			if lkw == "rect" {
				ev.color, ev.text = parseColor(rest), ""
			}
			sq.events = append(sq.events, ev)
			stack = append(stack, "block")
			depth++
			continue
		case "else", "and", "option":
			if depth == 0 {
				return nil, errorf(l.n, "%s outside of a block", lkw)
			}
			sq.events = append(sq.events, seqEvent{kind: evElse, line: l.n, block: lkw, text: rest})
			continue
		case "end":
			if len(stack) == 0 {
				return nil, errorf(l.n, "end without block")
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top == "box" {
				curBox = -1
				continue
			}
			depth--
			sq.events = append(sq.events, seqEvent{kind: evEnd, line: l.n})
			continue
		}
		ev, err := sq.message(l)
		if err != nil {
			return nil, err
		}
		if sq.autonum {
			ev.num = sq.next
			sq.next += sq.numStep
		}
		sq.events = append(sq.events, ev)
	}
	if len(stack) > 0 {
		return nil, errorf(lines[len(lines)-1].n, "block is not closed with end")
	}
	d := sq.layout(e)
	if title != "" {
		d.addTitle(e, title)
	}
	return d, nil
}

func (sq *sequence) note(l srcLine, rest string) (seqEvent, error) {
	head, text, ok := strings.Cut(rest, ":")
	if !ok {
		return seqEvent{}, errorf(l.n, "note needs \": text\"")
	}
	f := strings.Fields(head)
	if len(f) < 2 {
		return seqEvent{}, errorf(l.n, "note needs a position (left of, right of, over)")
	}
	ev := seqEvent{kind: evNote, line: l.n, text: strings.TrimSpace(text)}
	var who string
	switch strings.ToLower(f[0]) {
	case "left", "right":
		if len(f) < 3 || strings.ToLower(f[1]) != "of" {
			return seqEvent{}, errorf(l.n, "expected \"%s of\"", f[0])
		}
		ev.side = strings.ToLower(f[0])
		who = strings.Join(f[2:], " ")
	case "over":
		ev.side = "over"
		who = strings.Join(f[1:], " ")
	default:
		return seqEvent{}, errorf(l.n, "unknown note position %q", f[0])
	}
	ids := strings.Split(who, ",")
	ev.from = sq.part(ids[0], false, "")
	ev.to = ev.from
	if len(ids) > 1 {
		ev.to = sq.part(ids[1], false, "")
	}
	if ev.to < ev.from {
		ev.from, ev.to = ev.to, ev.from
	}
	return ev, nil
}

func (sq *sequence) message(l srcLine) (seqEvent, error) {
	t := l.text
	head, text, _ := strings.Cut(t, ":")
	for i := 0; i < len(head); i++ {
		for _, a := range seqArrows {
			if !strings.HasPrefix(head[i:], a.tok) {
				continue
			}
			from := strings.TrimSpace(head[:i])
			to := strings.TrimSpace(head[i+len(a.tok):])
			ev := seqEvent{kind: evMsg, line: l.n, arrow: a.a, text: strings.TrimSpace(text)}
			switch {
			case strings.HasPrefix(to, "+"):
				ev.act, to = true, strings.TrimSpace(to[1:])
			case strings.HasPrefix(to, "-"):
				ev.dea, to = true, strings.TrimSpace(to[1:])
			}
			if from == "" || to == "" {
				return seqEvent{}, errorf(l.n, "message needs a sender and a receiver")
			}
			ev.from = sq.part(from, false, "")
			ev.to = sq.part(to, false, "")
			return ev, nil
		}
	}
	return seqEvent{}, errorf(l.n, "cannot parse %q", clip(t))
}

// Layout constants (units).
const (
	seqGap      = 40.0 // between participant boxes
	seqBoxPadX  = 14.0
	seqBoxPadY  = 10.0
	seqActW     = 10.0 // activation bar width
	seqSelfW    = 36.0 // self message loop width
	seqSelfH    = 22.0
	seqNotePadX = 10.0
	seqNotePadY = 7.0
)

func (sq *sequence) layout(e *env) *Diagram {
	small := e.size * 0.86
	// Participant sizes.
	maxH := 0.0
	for _, p := range sq.parts {
		p.lines = labelLines(p.label)
		tw, th := e.textSize(p.lines, e.size, false)
		p.w = max(tw+2*seqBoxPadX, 80)
		p.h = th + 2*seqBoxPadY
		if p.actor {
			p.h = 38 + th + 4
		}
		maxH = max(maxH, p.h)
	}
	n := len(sq.parts)
	// Horizontal constraints: need[i][j] minimum center distance.
	need := map[[2]int]float64{}
	req := func(i, j int, d float64) {
		if i > j {
			i, j = j, i
		}
		k := [2]int{i, j}
		need[k] = max(need[k], d)
	}
	leftRoom := 0.0
	for _, ev := range sq.events {
		switch ev.kind {
		case evMsg:
			tw, _ := e.textSize(labelLines(ev.text), e.size, false)
			if ev.from == ev.to {
				if ev.from+1 < n {
					req(ev.from, ev.from+1, max(seqSelfW+tw+16, sq.parts[ev.from+1].w/2+seqSelfW+12))
				}
				continue
			}
			req(ev.from, ev.to, tw+40)
		case evNote:
			tw, _ := e.textSize(labelLines(ev.text), e.size, false)
			nw := tw + 2*seqNotePadX
			switch ev.side {
			case "right":
				if ev.from+1 < n {
					req(ev.from, ev.from+1, nw+16)
				}
			case "left":
				if ev.from > 0 {
					req(ev.from-1, ev.from, nw+16)
				} else {
					leftRoom = max(leftRoom, nw+16)
				}
			case "over":
				if ev.to > ev.from {
					req(ev.from, ev.to, nw-50)
				}
			}
		}
	}
	x := 0.0
	for i, p := range sq.parts {
		if i == 0 {
			x = max(p.w/2, leftRoom)
		} else {
			q := sq.parts[i-1]
			x = q.x + q.w/2 + seqGap + p.w/2
			for k, d := range need {
				if k[1] == i {
					x = max(x, sq.parts[k[0]].x+d)
				}
			}
		}
		p.x = x
	}
	// Box headers.
	top := 0.0
	for _, b := range sq.boxes {
		if b.label != "" {
			top = e.lh + 10
		}
	}

	d := &Diagram{Kind: "sequence"}
	var bg, frames, lifelines, bars, msgs, notes, heads, badges []Item
	lineTop := top + maxH
	y := lineTop + 18

	// Activation state.
	type act struct{ start float64 }
	active := make([][]act, n)
	actEdge := func(p int, right bool) float64 {
		dep := len(active[p])
		if dep == 0 {
			return sq.parts[p].x
		}
		off := seqActW/2 + float64(dep-1)*seqActW/2
		if right {
			return sq.parts[p].x + off
		}
		return sq.parts[p].x - off
	}
	closeAct := func(p int, at float64) {
		if len(active[p]) == 0 {
			return
		}
		a := active[p][len(active[p])-1]
		active[p] = active[p][:len(active[p])-1]
		k := float64(len(active[p]))
		x0 := sq.parts[p].x - seqActW/2 + k*seqActW/2
		bars = append(bars, Item{Path: rectPath(Rect{x0, a.start, seqActW, max(at-a.start, 6)}, 0),
			Fill: Paint{Role: RoleActivation}, Stroke: Paint{Role: RoleNodeStroke}, Width: 1})
	}
	lastArrow := -1.0

	// Blocks: extent tracking.
	type block struct {
		ev        seqEvent
		top       float64
		x0, x1    float64
		dividers  []seqEvent
		divY      []float64
		innerDeep int
	}
	var stack []*block
	extend := func(a, b float64) {
		for _, bl := range stack {
			bl.x0, bl.x1 = min(bl.x0, a), max(bl.x1, b)
		}
	}
	for _, ev := range sq.events {
		switch ev.kind {
		case evMsg:
			ls := labelLines(ev.text)
			if ev.text == "" {
				ls = nil
			}
			tw, th := e.textSize(ls, e.size, false)
			pf, pt := sq.parts[ev.from], sq.parts[ev.to]
			ya := y + th + 6
			if ev.act {
				active[ev.to] = append(active[ev.to], act{ya})
			}
			w := 1.4
			dash := Solid
			if ev.arrow.dash {
				dash = Dashed
			}
			stroke := Paint{Role: RoleLine}
			if ev.from == ev.to {
				x0 := actEdge(ev.from, true)
				pts := []Point{{x0, ya}, {pf.x + seqSelfW, ya}, {pf.x + seqSelfW, ya + seqSelfH}, {x0 + markerLen(ev.arrow.end, w), ya + seqSelfH}}
				msgs = append(msgs, Item{Path: roundedPolyline(pts, 8), Stroke: stroke, Width: w, Dash: dash})
				msgs = append(msgs, markerItems(ev.arrow.end, Point{x0, ya + seqSelfH}, Point{-1, 0}, w, stroke)...)
				if len(ls) > 0 {
					msgs = append(msgs, Item{Text: e.textAt(ls, x0+6, y, e.size, false, AnchorStart, Paint{Role: RoleText})})
				}
				extend(pf.x-10, pf.x+seqSelfW+max(tw+12, 10))
				d.Edges = append(d.Edges, EdgeInfo{From: pf.id, To: pt.id, Label: joinLines(ls), Points: []Point{pts[0], {x0, ya + seqSelfH}}})
				lastArrow = ya
				ya += seqSelfH
			} else {
				right := pt.x > pf.x
				x0 := actEdge(ev.from, right)
				x1 := actEdge(ev.to, !right)
				dir := Point{1, 0}
				if !right {
					dir = Point{-1, 0}
				}
				s := Point{x0 + dir.X*markerLen(ev.arrow.start, w), ya}
				t := Point{x1 - dir.X*markerLen(ev.arrow.end, w), ya}
				msgs = append(msgs, Item{Path: linePath(s, t), Stroke: stroke, Width: w, Dash: dash})
				msgs = append(msgs, markerItems(ev.arrow.end, Point{x1, ya}, dir, w, stroke)...)
				msgs = append(msgs, markerItems(ev.arrow.start, Point{x0, ya}, Point{-dir.X, 0}, w, stroke)...)
				if len(ls) > 0 {
					msgs = append(msgs, Item{Text: e.textAt(ls, (pf.x+pt.x)/2, y, e.size, false, AnchorMiddle, Paint{Role: RoleText})})
				}
				lo, hi := min(pf.x, pt.x), max(pf.x, pt.x)
				mid := (pf.x + pt.x) / 2
				extend(min(lo, mid-tw/2)-4, max(hi, mid+tw/2)+4)
				d.Edges = append(d.Edges, EdgeInfo{From: pf.id, To: pt.id, Label: joinLines(ls), Points: []Point{{x0, ya}, {x1, ya}}})
				lastArrow = ya
			}
			if ev.num != 0 {
				c := Point{actEdge(ev.from, pt.x >= pf.x), lastArrow}
				lbl := strconv.Itoa(ev.num)
				r := max(8, e.measure(lbl, small*0.85, true)/2+4)
				badges = append(badges, Item{Path: ellipsePath(c, r, r), Fill: Paint{Role: RoleAccent}})
				badges = append(badges, Item{Text: e.text([]string{lbl}, c.X, c.Y, small*0.85, true, Paint{Role: RoleOnAccent})})
			}
			if ev.dea {
				closeAct(ev.from, lastArrow)
			}
			y = ya + 18
		case evNote:
			ls := e.wrap(labelLines(ev.text), e.size, false, 260)
			tw, th := e.textSize(ls, e.size, false)
			nw, nh := tw+2*seqNotePadX, th+2*seqNotePadY
			pf, pt := sq.parts[ev.from], sq.parts[ev.to]
			var bx float64
			switch ev.side {
			case "right":
				bx = actEdge(ev.from, true) + 8
			case "left":
				bx = actEdge(ev.from, false) - 8 - nw
			default:
				if ev.to > ev.from {
					span := pt.x - pf.x + 50
					nw = max(nw, span)
					bx = (pf.x+pt.x)/2 - nw/2
				} else {
					nw = max(nw, 60)
					bx = pf.x - nw/2
				}
			}
			r := Rect{bx, y, nw, nh}
			notes = append(notes, Item{Path: rectPath(r, 2), Fill: Paint{Role: RoleNoteFill}, Stroke: Paint{Role: RoleNoteStroke}, Width: 1})
			notes = append(notes, Item{Text: e.text(ls, r.X+r.W/2, r.Y+r.H/2, e.size, false, Paint{Role: RoleText})})
			extend(r.X-4, r.X+r.W+4)
			y += nh + 14
		case evActivate:
			at := y
			if lastArrow >= 0 && y-lastArrow < 24 {
				at = lastArrow
			}
			active[ev.from] = append(active[ev.from], act{at})
		case evDeactivate:
			at := y
			if lastArrow >= 0 && y-lastArrow < 24 {
				at = lastArrow
			}
			closeAct(ev.from, at)
		case evBlock:
			bl := &block{ev: ev, top: y, x0: math.Inf(1), x1: math.Inf(-1)}
			stack = append(stack, bl)
			if ev.block == "rect" {
				y += 10
			} else {
				y += e.lh + 14
			}
		case evElse:
			bl := stack[len(stack)-1]
			bl.dividers = append(bl.dividers, ev)
			bl.divY = append(bl.divY, y)
			y += e.lh + 12
		case evEnd:
			bl := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			y += 6
			// Nesting depth below, for margins.
			margin := 10.0 + 10*float64(bl.innerDeep)
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.innerDeep = max(p.innerDeep, bl.innerDeep+1)
			}
			if math.IsInf(bl.x0, 1) {
				bl.x0, bl.x1 = sq.parts[0].x, sq.parts[0].x
			}
			x0, x1 := bl.x0-margin, bl.x1+margin
			if bl.ev.block == "rect" {
				col := bl.ev.color
				bg = append(bg, Item{Path: rectPath(Rect{x0, bl.top, x1 - x0, y - bl.top}, 0), Fill: Paint{Role: RoleClusterFill, Color: col}})
				extend(x0, x1)
				y += 10
				continue
			}
			kind := bl.ev.block
			kw, _ := e.textSize([]string{kind}, small, true)
			tabW, tabH := kw+16, e.lh+6
			cond := ""
			if bl.ev.text != "" {
				cond = "[" + strings.TrimSpace(bl.ev.text) + "]"
			}
			cw, _ := e.textSize([]string{cond}, small, false)
			minW := tabW + cw + 24
			for _, dv := range bl.dividers {
				if dv.text != "" {
					w, _ := e.textSize([]string{"[" + dv.text + "]"}, small, false)
					minW = max(minW, w+24)
				}
			}
			if x1-x0 < minW {
				grow := (minW - (x1 - x0)) / 2
				x0, x1 = x0-grow, x1+grow
			}
			extend(x0, x1)
			fr := Rect{x0, bl.top, x1 - x0, y - bl.top}
			frames = append(frames, Item{Path: rectPath(fr, 0), Stroke: Paint{Role: RoleClusterStroke}, Width: 1.2})
			tab := polyPath(Point{x0, bl.top}, Point{x0 + tabW, bl.top}, Point{x0 + tabW, bl.top + tabH - 6},
				Point{x0 + tabW - 6, bl.top + tabH}, Point{x0, bl.top + tabH})
			frames = append(frames, Item{Path: tab, Fill: Paint{Role: RoleClusterFill}, Stroke: Paint{Role: RoleClusterStroke}, Width: 1.2})
			frames = append(frames, Item{Text: e.text([]string{kind}, x0+tabW/2, bl.top+tabH/2, small, true, Paint{Role: RoleText})})
			if cond != "" {
				frames = append(frames, Item{Text: e.textAt([]string{cond}, x0+tabW+10, bl.top+3, small, false, AnchorStart, Paint{Role: RoleTextMuted})})
			}
			for i, dv := range bl.dividers {
				dy := bl.divY[i]
				frames = append(frames, Item{Path: linePath(Point{x0, dy}, Point{x1, dy}), Stroke: Paint{Role: RoleClusterStroke}, Width: 1.2, Dash: Dashed})
				if dv.text != "" {
					frames = append(frames, Item{Text: e.textAt([]string{"[" + dv.text + "]"}, (x0+x1)/2, dy+4, small, false, AnchorMiddle, Paint{Role: RoleTextMuted})})
				}
			}
			y += 10
		}
	}
	for p := range active {
		for len(active[p]) > 0 {
			closeAct(p, y)
		}
	}
	lineBottom := y + 4
	// Lifelines and participant boxes (top and bottom).
	for _, p := range sq.parts {
		lifelines = append(lifelines, Item{Path: linePath(Point{p.x, lineTop}, Point{p.x, lineBottom}),
			Stroke: Paint{Role: RoleLine}, Width: 1, Dash: Dashed})
		heads = append(heads, sq.head(e, p, top+maxH-p.h, maxH)...)
		heads = append(heads, sq.head(e, p, lineBottom, maxH)...)
		d.Nodes = append(d.Nodes, NodeInfo{ID: p.id, Label: joinLines(p.lines), Shape: map[bool]string{true: "actor", false: "participant"}[p.actor],
			Box: Rect{p.x - p.w/2, top + maxH - p.h, p.w, p.h}})
	}
	// Group boxes behind everything.
	var groups []Item
	for _, b := range sq.boxes {
		if len(b.parts) == 0 {
			continue
		}
		x0, x1 := math.Inf(1), math.Inf(-1)
		for _, i := range b.parts {
			p := sq.parts[i]
			x0, x1 = min(x0, p.x-p.w/2-10), max(x1, p.x+p.w/2+10)
		}
		r := Rect{x0, 0, x1 - x0, lineBottom + maxH + 8}
		groups = append(groups, Item{Path: rectPath(r, 4), Fill: Paint{Role: RoleClusterFill, Color: b.color}, Stroke: Paint{Role: RoleClusterStroke}, Width: 1})
		if b.label != "" {
			tp := Paint{Role: RoleText, Color: contrastText(b.color)}
			groups = append(groups, Item{Text: e.textAt(labelLines(b.label), r.X+r.W/2, 4, e.size, true, AnchorMiddle, tp)})
		}
	}
	d.Items = append(d.Items, groups...)
	d.Items = append(d.Items, bg...)
	d.Items = append(d.Items, lifelines...)
	d.Items = append(d.Items, bars...)
	d.Items = append(d.Items, frames...)
	d.Items = append(d.Items, msgs...)
	d.Items = append(d.Items, notes...)
	d.Items = append(d.Items, heads...)
	d.Items = append(d.Items, badges...)
	d.fit(e, 8)
	return d
}

// head draws participant p with its top at y (boxes share height h).
func (sq *sequence) head(e *env, p *seqPart, y, h float64) []Item {
	if p.actor {
		cx := p.x
		hy := y + 9
		var body path
		body.move(cx, hy+8)
		body.line(cx, hy+22)
		body.move(cx-12, hy+13)
		body.line(cx+12, hy+13)
		body.move(cx, hy+22)
		body.line(cx-10, hy+33)
		body.move(cx, hy+22)
		body.line(cx+10, hy+33)
		return []Item{
			{Path: ellipsePath(Point{cx, hy}, 8, 8), Fill: Paint{Role: RoleNodeFill}, Stroke: Paint{Role: RoleNodeStroke}, Width: 1.4},
			{Path: body, Stroke: Paint{Role: RoleNodeStroke}, Width: 1.4},
			{Text: e.textAt(p.lines, cx, y+38+2, e.size, false, AnchorMiddle, Paint{Role: RoleText})},
		}
	}
	r := Rect{p.x - p.w/2, y, p.w, p.h}
	return []Item{
		{Path: rectPath(r, 4), Fill: Paint{Role: RoleNodeFill}, Stroke: Paint{Role: RoleNodeStroke}, Width: 1.3},
		{Text: e.text(p.lines, r.X+r.W/2, r.Y+r.H/2, e.size, false, Paint{Role: RoleText})},
	}
}

// fit moves the drawing to a margin and sets the size from the bounds of
// all items (text estimated from its lines).
func (d *Diagram) fit(e *env, margin float64) {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	grow := func(x, y float64) {
		x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
	}
	for _, it := range d.Items {
		for _, s := range it.Path {
			for _, p := range s.Pts {
				grow(p.X, p.Y)
			}
		}
		if t := it.Text; t != nil {
			grow(t.X, t.Y-t.Size)
			grow(t.X, t.Y+float64(len(t.Lines)-1)*t.LineHeight+t.Size*0.3)
		}
	}
	if math.IsInf(x0, 1) {
		d.Width, d.Height = 2*margin, 2*margin
		return
	}
	// Text extends horizontally by its measured width.
	for _, it := range d.Items {
		if t := it.Text; t != nil {
			for _, l := range t.Lines {
				w := e.measure(l, t.Size, t.Bold)
				switch t.Anchor {
				case AnchorStart:
					grow(t.X+w, t.Y)
				case AnchorEnd:
					grow(t.X-w, t.Y)
				default:
					grow(t.X-w/2, t.Y)
					grow(t.X+w/2, t.Y)
				}
			}
		}
	}
	d.shift(margin-x0, margin-y0)
	d.Width = x1 - x0 + 2*margin
	d.Height = y1 - y0 + 2*margin
}
