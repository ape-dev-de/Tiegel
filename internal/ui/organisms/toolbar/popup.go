package toolbar

import (
	"image"
	"image/color"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// itemKind distinguishes the rows of a popup.
type itemKind uint8

const (
	kindAction itemKind = iota
	kindHeader
	kindSeparator
)

// item is one popup row. Choosing an action row emits ev, or opens sub as
// a submenu next to the row.
type item struct {
	kind   itemKind
	label  string
	detail string
	glyph  *atoms.Glyph
	accent bool // glyph in the accent color
	// avatar draws the project avatar of this name instead of glyph.
	avatar string
	// state draws a project state marker (hasState).
	state    ProjectState
	hasState bool
	badges   []atoms.Badge
	bold     bool
	disabled bool
	ev       Event
	sub      []item
	// lazy marks a submenu built on demand by popup.expand (branch rows:
	// building seven submenu rows per branch up front is wasted work for
	// thousands of branches, U3).
	lazy bool
	// branch is the branch of a lazy branch row.
	branch gitcli.Branch
	// alt is reported instead of ev on Shift+click or Shift+Enter (e.g.
	// open in a new window).
	alt Event
	// menu is shown as the row's submenu on a context click (right click,
	// Ctrl+click on macOS); menuOpen marks sub as that menu.
	menu     []item
	menuOpen bool
}

func (it item) selectable() bool { return it.kind == kindAction && !it.disabled }

// hasSub reports whether the row opens a submenu.
func (it item) hasSub() bool { return len(it.sub) > 0 || it.lazy }

func header(label string) item { return item{kind: kindHeader, label: label} }
func separator() item          { return item{kind: kindSeparator} }

// nextSel returns the next selectable index after from in direction dir
// (wrapping), or -1 when none is selectable.
func nextSel(items []item, from, dir int) int {
	n := len(items)
	for k := 1; k <= n; k++ {
		i := ((from+dir*k)%n + n) % n
		if items[i].selectable() {
			return i
		}
	}
	return -1
}

// popup is an inline popup list anchored below a toolbar control, with an
// optional search field and one level of submenus. It is drawn with
// op.Defer on top of everything; a press outside closes it.
//
// Keyboard: Up/Down move the selection, Enter chooses (or opens the
// submenu), Right/Left open and close a submenu (without search field),
// Escape closes the submenu, then the popup.
type popup struct {
	open   bool
	items  []item
	sel    int
	subOf  int // item whose submenu is open, -1 = none
	subSel int
	// search is the optional filter field (nil: none).
	search *molecules.SearchField
	// queryChanged is set when the search text changed; the owner
	// rebuilds the items and clears it.
	queryChanged bool
	// focus requests the keyboard focus in the next Layout.
	focus bool
	// expand builds the submenu of a lazy row (nil: lazy rows have none).
	expand func(item) []item
	// key identifies the data the items were built from; built tells
	// whether items are valid for key (U3: rebuild only on change).
	key   uint64
	built bool

	rows, subRows []molecules.ListRow
	// ctx detect context clicks on the rows with a menu.
	ctx []molecules.ContextArea
	// goos enables Ctrl+click as context click on "darwin".
	goos     string
	list     molecules.ScrollList
	keyTag   int
	scrim    int
	panel    int
	subPanel int
	// rect and subRect are the panels of the last frame relative to the
	// anchor's top-left corner (empty when closed).
	rect, subRect image.Rectangle
}

func (p *popup) show() {
	p.open, p.sel, p.subOf, p.focus, p.built = true, -1, -1, true, false
	if p.search != nil {
		p.search.SetText("")
	}
	p.list.ScrollTo(0)
}

func (p *popup) close() {
	p.closeSub()
	p.open = false
	p.rect, p.subRect = image.Rectangle{}, image.Rectangle{}
}

// closeSub closes the open submenu (a context menu row gets its plain
// behaviour back).
func (p *popup) closeSub() {
	if p.subOf >= 0 && p.subOf < len(p.items) && p.items[p.subOf].menuOpen {
		p.items[p.subOf].sub, p.items[p.subOf].menuOpen = nil, false
	}
	p.subOf = -1
}

// openMenu shows the context menu of row i as its submenu.
func (p *popup) openMenu(i int) {
	if i < 0 || i >= len(p.items) || len(p.items[i].menu) == 0 || !p.items[i].selectable() {
		return
	}
	p.closeSub()
	p.sel = i
	p.items[i].sub, p.items[i].menuOpen = p.items[i].menu, true
	p.subOf, p.subSel = i, -1
	p.setItems(p.items)
}

// activateAlt chooses the alternative event of item i (its plain event
// when it has none).
func (p *popup) activateAlt(i int) []Event {
	if i < 0 || i >= len(p.items) || !p.items[i].selectable() || p.items[i].alt == nil {
		return p.activate(i)
	}
	ev := p.items[i].alt
	p.close()
	return []Event{ev}
}

func (p *popup) query() string {
	if p.search == nil {
		return ""
	}
	return strings.TrimSpace(p.search.Text())
}

// setItems replaces the rows, keeping the selection valid.
func (p *popup) setItems(items []item) {
	p.items = items
	for len(p.rows) < len(items) {
		p.rows = append(p.rows, molecules.ListRow{})
	}
	for len(p.ctx) < len(items) {
		p.ctx = append(p.ctx, molecules.ContextArea{GOOS: p.goos})
	}
	if p.subOf >= 0 && p.subOf < len(items) && items[p.subOf].lazy && items[p.subOf].sub == nil && p.expand != nil {
		items[p.subOf].sub = p.expand(items[p.subOf])
	}
	if p.subOf >= len(items) || (p.subOf >= 0 && len(items[p.subOf].sub) == 0) {
		p.subOf = -1
	}
	if p.sel < 0 || p.sel >= len(items) || !items[p.sel].selectable() {
		p.sel = nextSel(items, -1, 1)
	}
	if p.subOf >= 0 {
		sub := items[p.subOf].sub
		for len(p.subRows) < len(sub) {
			p.subRows = append(p.subRows, molecules.ListRow{})
		}
		if p.subSel < 0 || p.subSel >= len(sub) || !sub[p.subSel].selectable() {
			p.subSel = nextSel(sub, -1, 1)
		}
	}
}

func (p *popup) keyTarget() event.Tag {
	if p.search != nil {
		return &p.search.Field.Editor
	}
	return &p.keyTag
}

// activate chooses item i: opens its submenu or reports its event.
func (p *popup) activate(i int) []Event {
	if i < 0 || i >= len(p.items) || !p.items[i].selectable() {
		return nil
	}
	it := p.items[i]
	p.sel = i
	if it.hasSub() && !it.menuOpen {
		p.closeSub()
		p.subOf, p.subSel = i, -1
		p.setItems(p.items)
		return nil
	}
	p.close()
	return []Event{it.ev}
}

func (p *popup) activateSub(j int) []Event {
	if p.subOf < 0 {
		return nil
	}
	sub := p.items[p.subOf].sub
	if j < 0 || j >= len(sub) || !sub[j].selectable() {
		return nil
	}
	p.close()
	return []Event{sub[j].ev}
}

// enter chooses the selected row of the innermost open level.
func (p *popup) enter() []Event {
	if p.subOf >= 0 {
		return p.activateSub(p.subSel)
	}
	return p.activate(p.sel)
}

func (p *popup) move(dir int) {
	if p.subOf >= 0 {
		p.subSel = nextSel(p.items[p.subOf].sub, p.subSel, dir)
		return
	}
	p.sel = nextSel(p.items, p.sel, dir)
	if p.sel < 0 {
		return
	}
	if first, last := p.list.FirstVisible(), p.list.LastVisible(); p.sel < first {
		p.list.ScrollTo(p.sel)
	} else if p.sel > last {
		p.list.ScrollTo(first + p.sel - last)
	}
}

// process handles input of an open popup and returns chosen events.
func (p *popup) process(gtx layout.Context) []Event {
	if !p.open {
		return nil
	}
	var out []Event
	for {
		e, ok := gtx.Event(pointer.Filter{Target: &p.scrim, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, ok := e.(pointer.Event); ok {
			p.close()
			return nil
		}
	}
	// Presses on the panels are swallowed so they do not reach the
	// outside area.
	for _, t := range []event.Tag{&p.panel, &p.subPanel} {
		for {
			if _, ok := gtx.Event(pointer.Filter{Target: t, Kinds: pointer.Press}); !ok {
				break
			}
		}
	}
	tag := p.keyTarget()
	filters := []event.Filter{
		key.Filter{Focus: tag, Name: key.NameEscape},
		key.Filter{Focus: tag, Name: key.NameUpArrow},
		key.Filter{Focus: tag, Name: key.NameDownArrow},
		key.Filter{Focus: tag, Name: key.NameReturn, Required: key.ModShift},
		key.Filter{Focus: tag, Name: key.NameEnter, Required: key.ModShift},
	}
	if p.search == nil {
		filters = append(filters,
			key.FocusFilter{Target: tag},
			key.Filter{Focus: tag, Name: key.NameLeftArrow},
			key.Filter{Focus: tag, Name: key.NameRightArrow},
			key.Filter{Focus: tag, Name: key.NameReturn},
			key.Filter{Focus: tag, Name: key.NameEnter},
			key.Filter{Focus: tag, Name: key.NameSpace},
		)
	}
	for p.open {
		ev, ok := gtx.Event(filters...)
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case key.NameEscape:
			if p.subOf >= 0 {
				p.closeSub()
			} else {
				p.close()
			}
		case key.NameUpArrow:
			p.move(-1)
		case key.NameDownArrow:
			p.move(1)
		case key.NameRightArrow:
			if p.subOf < 0 && p.sel >= 0 && p.items[p.sel].hasSub() {
				out = append(out, p.activate(p.sel)...)
			}
		case key.NameLeftArrow:
			p.closeSub()
		case key.NameReturn, key.NameEnter, key.NameSpace:
			if e.Modifiers.Contain(key.ModShift) && p.subOf < 0 {
				out = append(out, p.activateAlt(p.sel)...)
				break
			}
			out = append(out, p.enter()...)
		}
	}
	if p.open && p.search != nil {
		changed, submitted := p.search.Update(gtx)
		if changed {
			p.queryChanged = true
			p.closeSub()
		}
		if submitted {
			out = append(out, p.enter()...)
		}
	}
	// Only rows drawn in the last frame can have input (U3: no work per
	// row for thousands of branches).
	lo, hi := p.list.FirstVisible(), min(p.list.LastVisible(), len(p.items)-1)
	for i := max(0, lo); i <= hi; i++ {
		if !p.open || i >= len(p.rows) {
			break
		}
		if i < len(p.ctx) && len(p.items[i].menu) > 0 {
			if _, ok := p.ctx[i].Update(gtx); ok {
				// A Ctrl+click on macOS is a primary click as well.
				p.rows[i].Update(gtx)
				p.openMenu(i)
				continue
			}
		}
		if c, _, mods := p.rows[i].UpdateMods(gtx); c {
			if p.goos == "darwin" && mods.Contain(key.ModCtrl) && len(p.items[i].menu) > 0 {
				p.openMenu(i) // the click of a Ctrl+click context click
				continue
			}
			if mods.Contain(key.ModShift) {
				out = append(out, p.activateAlt(i)...)
			} else {
				out = append(out, p.activate(i)...)
			}
		}
	}
	if p.open && p.subOf >= 0 {
		for j := range p.items[p.subOf].sub {
			if !p.open || p.subOf < 0 || j >= len(p.subRows) {
				break
			}
			if c, _ := p.subRows[j].Update(gtx); c {
				out = append(out, p.activateSub(j)...)
			}
		}
	}
	return out
}

// itemHeight returns the height of a row in pixels.
func itemHeight(gtx layout.Context, th *tokens.Theme, it item) int {
	switch it.kind {
	case kindSeparator:
		return 2*gtx.Dp(th.Space.XS) + gtx.Dp(th.Size.BorderWidth)
	}
	return gtx.Dp(th.Size.RowH)
}

func itemsHeight(gtx layout.Context, th *tokens.Theme, items []item) int {
	h := 0
	for _, it := range items {
		h += itemHeight(gtx, th, it)
	}
	return h
}

// layout draws the open popup below an anchor of size anchor (the current
// origin is the anchor's top-left corner). width is the panel width,
// right the room from the anchor's left edge to the right window edge,
// maxH the maximum panel height.
func (p *popup) layout(gtx layout.Context, th *tokens.Theme, anchor image.Point, width, right, maxH int) {
	if !p.open {
		return
	}
	p.setItems(p.items)
	pad := gtx.Dp(th.Space.XS)
	searchH := 0
	if p.search != nil {
		searchH = gtx.Dp(th.Size.ControlH) + 2*gtx.Dp(th.Space.S)
	}
	listH := min(itemsHeight(gtx, th, p.items), max(0, maxH-searchH-2*pad))
	if len(p.items) == 0 {
		listH = gtx.Dp(th.Size.RowH)
	}
	pw, ph := width, listH+searchH+2*pad
	x := min(0, right-pw)
	y := anchor.Y + gtx.Dp(th.Space.XXS)

	m := op.Record(gtx.Ops)
	const far = 1 << 16
	sc := clip.Rect{Min: image.Pt(-far, -far), Max: image.Pt(far, far)}.Push(gtx.Ops)
	event.Op(gtx.Ops, &p.scrim)
	sc.Pop()

	off := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
	r := image.Rectangle{Max: image.Pt(pw, ph)}
	panelBG(gtx, th, r)
	pc := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, &p.panel)
	if p.search == nil {
		event.Op(gtx.Ops, &p.keyTag)
	}
	pc.Pop()
	if p.search != nil {
		sg := gtx
		sg.Constraints = layout.Exact(image.Pt(pw-2*gtx.Dp(th.Space.S), gtx.Dp(th.Size.ControlH)))
		st := op.Offset(image.Pt(gtx.Dp(th.Space.S), gtx.Dp(th.Space.S))).Push(gtx.Ops)
		p.search.Layout(sg, th)
		st.Pop()
	}
	lg := gtx
	lg.Constraints = layout.Exact(image.Pt(pw, listH))
	lt := op.Offset(image.Pt(0, pad+searchH)).Push(gtx.Ops)
	if len(p.items) == 0 {
		layout.Inset{Left: th.Space.M, Top: th.Space.XS}.Layout(lg, atoms.Muted("Nothing found").SingleLine().Fn(th))
	} else {
		p.list.Layout(lg, th, len(p.items), func(gtx layout.Context, i int) layout.Dimensions {
			if len(p.items[i].menu) > 0 && i < len(p.ctx) {
				return p.ctx[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.layoutItem(gtx, th, &p.rows[i], p.items[i], (i == p.sel && p.subOf < 0) || i == p.subOf)
				})
			}
			return p.layoutItem(gtx, th, &p.rows[i], p.items[i], (i == p.sel && p.subOf < 0) || i == p.subOf)
		})
	}
	lt.Pop()
	p.rect = r.Add(image.Pt(x, y))
	p.subRect = image.Rectangle{}
	if p.subOf >= 0 {
		p.layoutSub(gtx, th, image.Pt(x, y), pw, pad+searchH, listH, right)
	}
	off.Pop()
	if p.focus {
		gtx.Execute(key.FocusCmd{Tag: p.keyTarget()})
		p.focus = false
	}
	op.Defer(gtx.Ops, m.Stop())
}

// layoutSub draws the submenu next to its row (right of the panel, or
// left when there is no room).
func (p *popup) layoutSub(gtx layout.Context, th *tokens.Theme, origin image.Point, pw, top, listH, right int) {
	x := origin.X
	sub := p.items[p.subOf].sub
	pos := p.list.Position()
	y := top - pos.Offset
	if p.subOf < pos.First {
		y = top
	} else {
		for i := pos.First; i < p.subOf; i++ {
			y += itemHeight(gtx, th, p.items[i])
		}
	}
	y = min(max(y, top), top+max(0, listH-gtx.Dp(th.Size.RowH)))
	pad := gtx.Dp(th.Space.XS)
	sw := gtx.Dp(th.Shell().SubMenuW)
	sh := itemsHeight(gtx, th, sub) + 2*pad
	sx := pw - pad
	if x+sx+sw > right {
		sx = pad - sw
	}
	r := image.Rectangle{Max: image.Pt(sw, sh)}
	t := op.Offset(image.Pt(sx, y-pad)).Push(gtx.Ops)
	panelBG(gtx, th, r)
	pc := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, &p.subPanel)
	pc.Pop()
	yy := pad
	for j, it := range sub {
		rg := gtx
		h := itemHeight(gtx, th, it)
		rg.Constraints = layout.Exact(image.Pt(sw, h))
		rt := op.Offset(image.Pt(0, yy)).Push(gtx.Ops)
		p.layoutItem(rg, th, &p.subRows[j], it, j == p.subSel)
		rt.Pop()
		yy += h
	}
	t.Pop()
	p.subRect = r.Add(origin.Add(image.Pt(sx, y-pad)))
}

// panelBG draws the popup panel: shadow, surface and border.
func panelBG(gtx layout.Context, th *tokens.Theme, r image.Rectangle) {
	rad := gtx.Dp(th.Radius.L)
	draw.RRect(gtx.Ops, r.Add(image.Pt(0, gtx.Dp(th.Space.XXS))), rad, tokens.WithAlpha(th.Color.Background, 0x90))
	draw.RRect(gtx.Ops, r, rad, th.Color.Surface)
	draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), th.Color.BorderStrong)
}

// layoutItem draws one row of a popup panel across the full width.
func (p *popup) layoutItem(gtx layout.Context, th *tokens.Theme, row *molecules.ListRow, it item, selected bool) layout.Dimensions {
	w := gtx.Constraints.Max.X
	switch it.kind {
	case kindSeparator:
		h := itemHeight(gtx, th, it)
		gtx.Constraints = layout.Exact(image.Pt(w, h))
		layout.Inset{Top: th.Space.XS, Left: th.Space.S, Right: th.Space.S}.Layout(gtx, atoms.Divider{}.Fn(th))
		return layout.Dimensions{Size: image.Pt(w, h)}
	case kindHeader:
		h := itemHeight(gtx, th, it)
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*gtx.Dp(th.Space.M)), h)}
		m := op.Record(gtx.Ops)
		d := atoms.Small(strings.ToUpper(it.label)).SingleLine().Layout(cg, th)
		call := m.Stop()
		t := op.Offset(image.Pt(gtx.Dp(th.Space.M), (h-d.Size.Y)/2+gtx.Dp(th.Space.XXS))).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		return layout.Dimensions{Size: image.Pt(w, h)}
	}
	row.Selected = selected
	if it.disabled {
		gtx = gtx.Disabled()
	}
	return row.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return rowContent(gtx, th, it)
	})
}

// rowContent lays out leading marker, label, detail, state, badges and
// the submenu chevron of an action row.
func rowContent(gtx layout.Context, th *tokens.Theme, it item) layout.Dimensions {
	muted := th.Color.TextMuted
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			size := gtx.Dp(th.Size.Icon)
			var d layout.Dimensions
			switch {
			case it.avatar != "":
				d = avatar(gtx, th, it.avatar, size)
			case it.glyph != nil:
				col := muted
				if it.accent {
					col = th.Color.Accent
				}
				if !gtx.Enabled() {
					col = th.Color.TextDisabled
				}
				d = atoms.Icon{Glyph: it.glyph, Color: col}.Layout(gtx, th)
			default:
				d = layout.Dimensions{Size: image.Pt(size, size)}
			}
			d.Size.X += gtx.Dp(th.Space.S)
			return d
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			w := gtx.Constraints.Max.X
			gtx.Constraints.Min.X = 0
			l := atoms.Text(it.label).SingleLine()
			if it.bold {
				l = atoms.Strong(it.label).SingleLine()
			}
			d := layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(l.Fn(th)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if it.detail == "" {
						return layout.Dimensions{}
					}
					gtx.Constraints.Min.X = 0
					return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(it.detail).SingleLine().Fn(th))
				}),
			)
			d.Size.X = w
			return d
		}),
	}
	if it.hasState {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return stateMarker(gtx, th, it.state)
			})
		}))
	}
	for _, bd := range it.badges {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.XS}.Layout(gtx, bd.Fn(th))
		}))
	}
	if it.hasSub() {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.XS}.Layout(gtx, atoms.Icon{Glyph: atoms.GlyphChevronRight, Size: th.Size.IconSmall, Color: muted}.Fn(th))
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// stateColor returns the marker color of a project state.
func stateColor(th *tokens.Theme, s ProjectState) (fill bool, c color.NRGBA) {
	switch s {
	case ProjectBackground:
		return true, th.Color.Info
	case ProjectHibernated:
		return false, th.Color.TextMuted
	}
	return true, th.Color.Success
}

// stateMarker draws a small dot (filled for running, hollow for
// hibernated) followed by the state name.
func stateMarker(gtx layout.Context, th *tokens.Theme, s ProjectState) layout.Dimensions {
	fill, col := stateColor(th, s)
	dot := gtx.Dp(th.Space.S)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			c := dot / 2
			if fill {
				draw.Circle(gtx.Ops, c, c, c, col)
			} else {
				draw.Ring(gtx.Ops, c, c, c, max(1, gtx.Dp(th.Size.BorderWidth)), col)
			}
			return layout.Dimensions{Size: image.Pt(dot, dot)}
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: th.Space.XS}.Layout(gtx, atoms.Small(s.String()).SingleLine().Fn(th))
		}),
	)
}

// avatar draws the rounded two-letter project avatar of edge size,
// colored by the hash of name.
func avatar(gtx layout.Context, th *tokens.Theme, name string, size int) layout.Dimensions {
	col := th.AvatarColor(name)
	sz := image.Pt(size, size)
	r := image.Rectangle{Max: sz}
	draw.RRect(gtx.Ops, r, gtx.Dp(th.Radius.M), tokens.Over(tokens.WithAlpha(col, 0x40), th.Color.Surface))
	cg := gtx
	cg.Constraints = layout.Constraints{Max: sz}
	m := op.Record(gtx.Ops)
	d := atoms.Label{Text: Initials(name), Role: atoms.RoleSmall, Color: col, MaxLines: 1}.Layout(cg, th)
	draw.Centered(gtx.Ops, sz, d.Size, m.Stop())
	return layout.Dimensions{Size: sz}
}
