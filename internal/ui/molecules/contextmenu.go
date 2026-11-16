package molecules

import (
	"image"
	"strings"
	"unicode"
	"unicode/utf8"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// MenuCheck selects the mark of a checkable menu item.
type MenuCheck uint8

// Menu check kinds.
const (
	MenuCheckNone  MenuCheck = iota
	MenuCheckBox             // on/off toggle: check mark when Checked
	MenuCheckRadio           // exclusive option: dot when Checked
)

// MenuItem is one entry of a ContextMenu: a command, a submenu (Children)
// or a separator.
type MenuItem struct {
	// ID is reported by ContextMenu.Update when the item is chosen.
	ID    string
	Label string
	Glyph *atoms.Glyph
	// Shortcut is shown right-aligned and muted, e.g. "Cmd+Shift+O" (see
	// actions.FormatChord).
	Shortcut string
	Disabled bool
	Check    MenuCheck
	Checked  bool
	// Separator draws a divider; other fields are ignored.
	Separator bool
	// Children make the item a submenu (opened on hover, Right or Enter).
	Children []MenuItem
}

// MenuItemsFromEntries converts resolved actions (actions.Registry.Resolve)
// to menu items; icon names become atoms glyphs.
func MenuItemsFromEntries(es []actions.Entry) []MenuItem {
	out := make([]MenuItem, 0, len(es))
	for _, e := range es {
		out = append(out, MenuItem{
			ID: e.ID, Label: e.Label, Glyph: atoms.GlyphNamed(e.Icon), Shortcut: e.Shortcut,
			Disabled: e.Disabled, Check: MenuCheck(e.Check), Checked: e.Checked, Separator: e.Separator,
			Children: MenuItemsFromEntries(e.Children),
		})
	}
	return out
}

// selectable reports whether keyboard navigation may stop on it.
func (it MenuItem) selectable() bool { return !it.Separator && !it.Disabled }

// IsContextClick reports whether e asks for a context menu: a press of the
// secondary button, or on macOS (goos "darwin") a primary press with Ctrl
// held.
func IsContextClick(e pointer.Event, goos string) bool {
	if e.Kind != pointer.Press {
		return false
	}
	if e.Buttons.Contain(pointer.ButtonSecondary) {
		return true
	}
	return goos == "darwin" && e.Buttons.Contain(pointer.ButtonPrimary) && e.Modifiers.Contain(key.ModCtrl)
}

// ContextArea detects context clicks (IsContextClick) on its content. The
// content's own handlers keep receiving the events, so a row can select
// itself on the same press. It is a stateful wrapper component.
//
// A Ctrl+click on macOS is also a primary click for the content; widgets
// that react to clicks should ignore it when ContextArea reported it.
type ContextArea struct {
	// GOOS enables Ctrl+click on "darwin" (usually runtime.GOOS).
	GOOS string

	tag int
}

// Update returns the position (in the area's coordinates) of a context
// click since the last call.
func (c *ContextArea) Update(gtx layout.Context) (pos image.Point, ok bool) {
	for {
		ev, more := gtx.Event(pointer.Filter{Target: &c.tag, Kinds: pointer.Press})
		if !more {
			return pos, ok
		}
		if e, isPtr := ev.(pointer.Event); isPtr && IsContextClick(e, c.GOOS) {
			pos, ok = e.Position.Round(), true
		}
	}
}

// Layout lays out content and registers the area over its size.
func (c *ContextArea) Layout(gtx layout.Context, content layout.Widget) layout.Dimensions {
	m := op.Record(gtx.Ops)
	d := content(gtx)
	call := m.Stop()
	area := clip.Rect{Max: d.Size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &c.tag)
	call.Add(gtx.Ops)
	area.Pop()
	return d
}

// ContextMenu is a popup menu with icons, shortcuts, check/radio marks,
// separators and submenus. It opens at a point (usually the mouse
// position), stays inside its bounds by flipping at the edges, and is
// operated with the mouse (hover opens submenus, a click chooses) or the
// keyboard (Up/Down, Home/End, Right/Enter open a submenu, Left/Escape
// close one level, Enter/Space choose, a letter jumps to the next item
// starting with it). A click outside or Escape on the top level closes it.
//
// Usage: keep one ContextMenu in the window state and call Layout every
// frame (also while closed) as the last widget, in a context whose
// constraints span the area the menu may use (the window): it draws with
// op.Defer on top of everything and observes pointer presses to know the
// mouse position. Open it with ShowAtPointer from a ContextArea (or any
// handler that saw the press in the same frame) or with ShowAt for
// keyboard-invoked menus; read choices with Update.
type ContextMenu struct {
	Items []MenuItem
	// MinWidth of each panel (default 180dp).
	MinWidth unit.Dp
	// Bounds limits the menu (in Layout coordinates); the zero value
	// means the constraints of Layout.
	Bounds image.Rectangle

	open        bool
	anchor      image.Point
	wantPointer bool
	lastPress   image.Point
	blur        bool
	pressed     bool // a press happened on a row since opening
	path        []int
	sel         []int
	tags        [][]*int          // per level, per row (pointers stay stable)
	panels      []*int            // per level: blocks presses from the outside area
	rects       []image.Rectangle // panel rectangles of the last frame
	tracker     int
	scrim       int
	focus       int
	chosen      string
	hasChosen   bool
}

// ShowAt opens the menu with its top-left corner at p (Layout
// coordinates), closing open submenus.
func (m *ContextMenu) ShowAt(p image.Point) {
	m.open, m.anchor, m.wantPointer = true, p, false
	m.reset()
}

// ShowAtPointer opens the menu at the last pointer press seen by Layout
// (the press that triggered it, when called in the same frame).
func (m *ContextMenu) ShowAtPointer() {
	m.open, m.wantPointer = true, true
	m.reset()
}

func (m *ContextMenu) reset() {
	m.path, m.sel, m.pressed, m.blur = m.path[:0], append(m.sel[:0], -1), false, false
}

// Close closes the menu.
func (m *ContextMenu) Close() {
	if m.open {
		m.open, m.blur = false, true
	}
}

// Panels returns the rectangles (Layout coordinates) of the open panels
// as drawn in the last frame, top level first.
func (m *ContextMenu) Panels() []image.Rectangle { return m.rects }

// Opened reports whether the menu is shown.
func (m *ContextMenu) Opened() bool { return m.open }

// Update processes input and returns the ID of an item chosen since the
// last call. Choosing closes the menu.
func (m *ContextMenu) Update(gtx layout.Context) (id string, ok bool) {
	m.process(gtx)
	id, ok = m.chosen, m.hasChosen
	m.chosen, m.hasChosen = "", false
	return id, ok
}

// items returns the items of level l.
func (m *ContextMenu) items(l int) []MenuItem {
	items := m.Items
	for i := 0; i < l && i < len(m.path); i++ {
		if m.path[i] >= len(items) {
			return nil
		}
		items = items[m.path[i]].Children
	}
	return items
}

func (m *ContextMenu) levels() int { return len(m.path) + 1 }

func (m *ContextMenu) choose(it MenuItem) {
	m.chosen, m.hasChosen = it.ID, true
	m.Close()
}

// openSub opens the submenu of item i on level l.
func (m *ContextMenu) openSub(l, i int, selectFirst bool) {
	m.path = append(m.path[:l], i)
	m.sel = append(m.sel[:l+1], -1)
	m.sel[l] = i
	if selectFirst {
		m.sel[l+1] = next(m.items(l+1), -1, 1)
	}
}

// next returns the next selectable index after from in direction dir
// (wrapping), or -1.
func next(items []MenuItem, from, dir int) int {
	n := len(items)
	for k := 1; k <= n; k++ {
		i := ((from+dir*k)%n + n) % n
		if items[i].selectable() {
			return i
		}
	}
	return -1
}

func (m *ContextMenu) process(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.tracker, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok {
			m.lastPress = e.Position.Round()
		}
	}
	if m.wantPointer {
		m.anchor, m.wantPointer = m.lastPress, false
	}
	if m.blur {
		m.blur = false
		if gtx.Focused(&m.focus) {
			gtx.Execute(key.FocusCmd{})
		}
	}
	if !m.open {
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.scrim, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, ok := ev.(pointer.Event); ok {
			m.Close()
			return
		}
	}
	for l := range m.panels {
		for {
			if _, ok := gtx.Event(pointer.Filter{Target: m.panels[l], Kinds: pointer.Press}); !ok {
				break
			}
		}
	}
	m.pointerRows(gtx)
	if m.open {
		m.keys(gtx)
	}
}

func (m *ContextMenu) pointerRows(gtx layout.Context) {
	for l := 0; l < len(m.tags) && m.open; l++ {
		for i := range m.tags[l] {
			for {
				ev, ok := gtx.Event(pointer.Filter{Target: m.tags[l][i], Kinds: pointer.Enter | pointer.Leave | pointer.Press | pointer.Release})
				if !ok {
					break
				}
				e, ok := ev.(pointer.Event)
				items := m.items(l)
				if !ok || l >= m.levels() || i >= len(items) {
					continue
				}
				it := items[i]
				switch e.Kind {
				case pointer.Enter:
					m.hover(l, i)
				case pointer.Leave:
					if l == m.levels()-1 && m.sel[l] == i {
						m.sel[l] = -1
					}
				case pointer.Press:
					m.pressed = true
					m.hover(l, i)
				case pointer.Release:
					if m.pressed && it.selectable() && len(it.Children) == 0 {
						m.choose(it)
						return
					}
				}
			}
		}
	}
}

// hover highlights item i on level l and opens or closes submenus.
func (m *ContextMenu) hover(l, i int) {
	if l >= m.levels() {
		return
	}
	items := m.items(l)
	if i >= len(items) || items[i].Separator {
		return
	}
	if len(items[i].Children) > 0 && !items[i].Disabled {
		m.openSub(l, i, false)
		return
	}
	m.path = m.path[:l]
	m.sel = m.sel[:l+1]
	m.sel[l] = i
	if items[i].Disabled {
		m.sel[l] = -1
	}
}

func (m *ContextMenu) keys(gtx layout.Context) {
	if !gtx.Focused(&m.focus) {
		gtx.Execute(key.FocusCmd{Tag: &m.focus})
	}
	f := func(n key.Name) event.Filter { return key.Filter{Focus: &m.focus, Name: n} }
	filters := []event.Filter{
		f(key.NameUpArrow), f(key.NameDownArrow), f(key.NameLeftArrow), f(key.NameRightArrow),
		f(key.NameHome), f(key.NameEnd), f(key.NameReturn), f(key.NameEnter), f(key.NameEscape), f(key.NameSpace),
		key.Filter{Focus: &m.focus, Name: "", Optional: key.ModShift},
		key.FocusFilter{Target: &m.focus},
	}
	for m.open {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			m.key(e.Name)
		}
	}
}

func (m *ContextMenu) key(name key.Name) {
	l := m.levels() - 1
	items := m.items(l)
	cur := m.sel[l]
	var it *MenuItem
	if cur >= 0 && cur < len(items) {
		it = &items[cur]
	}
	switch name {
	case key.NameDownArrow:
		m.sel[l] = next(items, cur, 1)
	case key.NameUpArrow:
		if cur < 0 {
			cur = len(items)
		}
		m.sel[l] = next(items, cur, -1)
	case key.NameHome:
		m.sel[l] = next(items, -1, 1)
	case key.NameEnd:
		m.sel[l] = next(items, len(items), -1)
	case key.NameRightArrow:
		if it != nil && len(it.Children) > 0 && !it.Disabled {
			m.openSub(l, cur, true)
		}
	case key.NameLeftArrow:
		if l > 0 {
			m.path, m.sel = m.path[:l-1], m.sel[:l]
		}
	case key.NameEscape:
		if l == 0 {
			m.Close()
			return
		}
		m.path, m.sel = m.path[:l-1], m.sel[:l]
	case key.NameReturn, key.NameEnter, key.NameSpace:
		switch {
		case it == nil || it.Disabled:
		case len(it.Children) > 0:
			m.openSub(l, cur, true)
		default:
			m.choose(*it)
		}
	default:
		r, size := utf8.DecodeRuneInString(string(name))
		if size == 0 || size != len(name) {
			return
		}
		r = unicode.ToLower(r)
		n := len(items)
		for k := 1; k <= n; k++ {
			i := ((cur+k)%n + n) % n
			lbl := strings.TrimLeft(items[i].Label, " ")
			if first, _ := utf8.DecodeRuneInString(lbl); items[i].selectable() && unicode.ToLower(first) == r {
				m.sel[l] = i
				return
			}
		}
	}
}

// panel is the geometry of one level.
type menuPanel struct {
	rect   image.Rectangle
	rowsY  []int // top of each row relative to rect.Min.Y
	rowH   []int
	lead   int // width of the check/icon column (0 if none)
	labelW int
	shortW int
	sub    bool
}

// Layout draws the open menu on top of everything (op.Defer) and observes
// pointer presses; it takes no space itself. Call it every frame.
func (m *ContextMenu) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	m.process(gtx)
	bounds := m.Bounds
	if bounds.Empty() {
		bounds = image.Rectangle{Max: gtx.Constraints.Max}
	}
	rec := op.Record(gtx.Ops)
	// Pass-through tracker: remembers where the pointer was pressed.
	pass := pointer.PassOp{}.Push(gtx.Ops)
	tr := clip.Rect(bounds).Push(gtx.Ops)
	event.Op(gtx.Ops, &m.tracker)
	tr.Pop()
	pass.Pop()
	if m.open && len(m.Items) > 0 {
		m.layoutOpen(gtx, th, bounds)
	} else {
		m.open, m.rects = false, m.rects[:0]
	}
	op.Defer(gtx.Ops, rec.Stop())
	return layout.Dimensions{}
}

func (m *ContextMenu) layoutOpen(gtx layout.Context, th *tokens.Theme, bounds image.Rectangle) {
	const far = 1 << 16
	sc := clip.Rect{Min: image.Pt(-far, -far), Max: image.Pt(far, far)}.Push(gtx.Ops)
	event.Op(gtx.Ops, &m.scrim)
	sc.Pop()

	n := m.levels()
	for len(m.tags) < n {
		m.tags = append(m.tags, nil)
	}
	for len(m.panels) < n {
		m.panels = append(m.panels, new(int))
	}
	var prev menuPanel
	m.rects = m.rects[:0]
	for l := 0; l < n; l++ {
		items := m.items(l)
		for len(m.tags[l]) < len(items) {
			m.tags[l] = append(m.tags[l], new(int))
		}
		p := m.measure(gtx, th, items)
		size := p.rect.Size()
		var pos image.Point
		if l == 0 {
			pos = m.anchor
			if pos.X+size.X > bounds.Max.X {
				pos.X -= size.X
			}
			if pos.Y+size.Y > bounds.Max.Y {
				pos.Y -= size.Y
			}
		} else {
			pad := gtx.Dp(th.Space.XS)
			row := m.path[l-1]
			pos = image.Pt(prev.rect.Max.X-pad, prev.rect.Min.Y+prev.rowsY[row]-pad)
			if pos.X+size.X > bounds.Max.X {
				pos.X = prev.rect.Min.X - size.X + pad
			}
			if pos.Y+size.Y > bounds.Max.Y {
				pos.Y = bounds.Max.Y - size.Y
			}
		}
		pos.X = max(bounds.Min.X, min(pos.X, bounds.Max.X-size.X))
		pos.Y = max(bounds.Min.Y, pos.Y)
		p.rect = p.rect.Add(pos)
		m.drawPanel(gtx, th, l, items, p)
		m.rects = append(m.rects, p.rect)
		prev = p
	}
}

// measure computes the panel size and row layout of items at the origin.
func (m *ContextMenu) measure(gtx layout.Context, th *tokens.Theme, items []MenuItem) menuPanel {
	var p menuPanel
	pad := gtx.Dp(th.Space.XS)
	rowH := gtx.Dp(th.Size.RowH)
	sepH := gtx.Dp(th.Space.S) + 1
	mg := gtx
	mg.Constraints = layout.Constraints{Max: image.Pt(draw.Unbounded, rowH)}
	y := pad
	for _, it := range items {
		p.rowsY = append(p.rowsY, y)
		if it.Separator {
			p.rowH = append(p.rowH, sepH)
			y += sepH
			continue
		}
		p.rowH = append(p.rowH, rowH)
		y += rowH
		if it.Glyph != nil || it.Check != MenuCheckNone {
			p.lead = gtx.Dp(th.Size.Icon)
		}
		p.labelW = max(p.labelW, draw.Measure(mg, atoms.Text(it.Label).SingleLine().Fn(th)).Size.X)
		if it.Shortcut != "" {
			p.shortW = max(p.shortW, draw.Measure(mg, atoms.Muted(it.Shortcut).SingleLine().Fn(th)).Size.X)
		}
		if len(it.Children) > 0 {
			p.sub = true
		}
	}
	gap := gtx.Dp(th.Space.S)
	w := 2*pad + 2*gap + p.labelW
	if p.lead > 0 {
		w += p.lead + gap
	}
	if p.shortW > 0 {
		w += gtx.Dp(th.Space.XL) + p.shortW
	}
	if p.sub {
		w += gap + gtx.Dp(th.Size.IconSmall)
	}
	minW := m.MinWidth
	if minW == 0 {
		minW = 180
	}
	p.rect = image.Rectangle{Max: image.Pt(max(w, gtx.Dp(minW)), y+pad)}
	return p
}

func (m *ContextMenu) drawPanel(gtx layout.Context, th *tokens.Theme, l int, items []MenuItem, p menuPanel) {
	c := th.Color
	r := p.rect
	rad := gtx.Dp(th.Radius.M)
	draw.RRect(gtx.Ops, r.Add(image.Pt(0, gtx.Dp(2))), rad, tokens.WithAlpha(c.Background, 0x90))
	draw.RRect(gtx.Ops, r, rad, c.Surface)
	draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), c.BorderStrong)
	area := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, m.panels[l])
	if l == 0 {
		event.Op(gtx.Ops, &m.focus)
	}
	area.Pop()

	pad := gtx.Dp(th.Space.XS)
	gap := gtx.Dp(th.Space.S)
	w := r.Dx()
	for i, it := range items {
		top := r.Min.Y + p.rowsY[i]
		h := p.rowH[i]
		if it.Separator {
			y := top + h/2
			draw.Rect(gtx.Ops, image.Rect(r.Min.X+pad+gap, y, r.Max.X-pad-gap, y+gtx.Dp(th.Size.BorderWidth)), c.Border)
			continue
		}
		row := image.Rect(r.Min.X+pad, top, r.Max.X-pad, top+h)
		ra := clip.Rect(row).Push(gtx.Ops)
		event.Op(gtx.Ops, m.tags[l][i])
		ra.Pop()
		selected := l < len(m.sel) && m.sel[l] == i
		if selected {
			draw.RRect(gtx.Ops, row, gtx.Dp(th.Radius.S), c.Selection)
		}
		rg := gtx
		if it.Disabled {
			rg = gtx.Disabled()
		}
		textCol := c.Text
		mutedCol := c.TextMuted
		if it.Disabled {
			textCol, mutedCol = c.TextDisabled, c.TextDisabled
		}
		x := row.Min.X + gap
		cy := top + h/2
		if p.lead > 0 {
			switch {
			case it.Check == MenuCheckBox && it.Checked:
				m.place(rg, x, cy, atoms.Icon{Glyph: atoms.GlyphCheck, Color: textCol}.Fn(th))
			case it.Check == MenuCheckRadio && it.Checked:
				draw.Circle(gtx.Ops, x+p.lead/2, cy, gtx.Dp(3), textCol)
			case it.Glyph != nil:
				m.place(rg, x, cy, atoms.Icon{Glyph: it.Glyph, Color: mutedCol}.Fn(th))
			}
			x += p.lead + gap
		}
		m.place(rg, x, cy, atoms.Text(it.Label).SingleLine().WithColor(textCol).Fn(th))
		right := row.Max.X - gap
		if p.sub {
			if len(it.Children) > 0 {
				is := gtx.Dp(th.Size.IconSmall)
				m.place(rg, right-is, cy, atoms.Icon{Glyph: atoms.GlyphChevronRight, Size: th.Size.IconSmall, Color: mutedCol}.Fn(th))
			}
			right -= gtx.Dp(th.Size.IconSmall) + gap
		}
		if it.Shortcut != "" {
			lg := rg
			lg.Constraints = layout.Constraints{Max: image.Pt(w, h)}
			sc := op.Record(gtx.Ops)
			d := atoms.Muted(it.Shortcut).SingleLine().WithColor(mutedCol).Layout(lg, th)
			call := sc.Stop()
			t := op.Offset(image.Pt(right-d.Size.X, cy-d.Size.Y/2)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			t.Pop()
		}
	}
}

// place draws w with its left edge at x, vertically centered on cy.
func (m *ContextMenu) place(gtx layout.Context, x, cy int, w layout.Widget) {
	gtx.Constraints = layout.Constraints{Max: image.Pt(draw.Unbounded, draw.Unbounded)}
	rec := op.Record(gtx.Ops)
	d := w(gtx)
	call := rec.Stop()
	t := op.Offset(image.Pt(x, cy-d.Size.Y/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
}
