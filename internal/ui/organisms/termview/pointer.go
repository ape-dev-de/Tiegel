package termview

import (
	"image"
	"math"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"github.com/rivo/uniseg"

	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// multiClick is the longest pause between clicks of a double or triple
// click.
const multiClick = 400 * time.Millisecond

// cellAt returns the position at p: the nearest cell boundary (for
// selections), or with inCell the cell containing p (for links).
func (v *View) cellAt(p f32.Point, inCell bool) Pos {
	if v.met.lineH <= 0 || v.met.cellW <= 0 {
		return Pos{}
	}
	x := float64(int(p.X)-v.origin.X) / (float64(v.met.cellW) / 64)
	y := int(p.Y) - v.origin.Y
	row := max(0, min(v.rows-1, floorDiv(y, v.met.lineH)))
	col := int(math.Round(x))
	if inCell {
		col = int(math.Floor(x))
	}
	col = max(0, min(v.cols, col))
	return Pos{Line: v.Top() + row, Col: col}
}

func floorDiv(a, b int) int {
	if a < 0 {
		return -((-a + b - 1) / b)
	}
	return a / b
}

// line returns the snapshot line of absolute index i.
func (v *View) line(i int) (terminal.ScreenLine, bool) {
	if v.Screen == nil {
		return terminal.ScreenLine{}, false
	}
	j := i - v.Screen.Top
	if j < 0 || j >= len(v.Screen.Lines) {
		return terminal.ScreenLine{}, false
	}
	return v.Screen.Lines[j], true
}

// linkAt returns the link under a cell.
func (v *View) linkAt(p Pos) *terminal.Link {
	if v.Screen == nil {
		return nil
	}
	i := p.Line - v.Screen.Top
	if i < 0 || i >= len(v.Screen.Lines) {
		return nil
	}
	for j := range v.Screen.Lines[i].Links {
		l := &v.Screen.Lines[i].Links[j]
		if p.Col >= l.Col && p.Col < l.EndCol {
			return l
		}
	}
	return nil
}

// wideRight marks the right half of a wide character in cellTexts.
const wideRight = "\x00"

// cellTexts returns the text of every column of a line (wideRight for
// the right half of a wide character, "" for empty cells).
func cellTexts(l terminal.ScreenLine, cols int) []string {
	out := make([]string, cols)
	for _, s := range l.Spans {
		col := s.Col
		rest := s.Text
		state := -1
		for rest != "" && col < cols {
			var g string
			g, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
			out[col] = g
			if s.Width == 2 && len(s.Text) == len(g) {
				if col+1 < cols {
					out[col+1] = wideRight
				}
				col += 2
			} else {
				col++
			}
		}
	}
	return out
}

// wordChar reports whether g belongs to a word for double-click
// selection: letters, digits and the characters of paths and URLs.
func wordChar(g string) bool {
	if g == "" || g == " " || g == wideRight {
		return false
	}
	r := []rune(g)[0]
	if r > 0x7f {
		return true
	}
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-./~:@%+=?&#$", r)
}

// wordAt returns the word around column col of absolute line i
// (start, end exclusive); an empty range if there is none.
func (v *View) wordAt(i, col int) (int, int) {
	l, ok := v.line(i)
	if !ok || v.Screen == nil {
		return col, col
	}
	cells := cellTexts(l, v.Screen.Cols)
	if col >= len(cells) {
		return col, col
	}
	// The right half of a wide character belongs to its left half.
	if cells[col] == wideRight && col > 0 {
		col--
	}
	if !wordChar(cells[col]) {
		return col, col + 1
	}
	in := func(i int) bool { return wordChar(cells[i]) || cells[i] == wideRight }
	a, b := col, col+1
	for a > 0 && in(a-1) {
		a--
	}
	for b < len(cells) && in(b) {
		b++
	}
	return a, b
}

// lineRange returns the logical line around absolute line i (lines
// joined by autowrap, as far as the snapshot has them).
func (v *View) lineRange(i int) (Pos, Pos) {
	a, b := i, i
	for {
		l, ok := v.line(a - 1)
		if !ok || !l.Wrapped {
			break
		}
		a--
	}
	for {
		l, ok := v.line(b)
		if !ok || !l.Wrapped {
			break
		}
		b++
	}
	cols := 0
	if v.Screen != nil {
		cols = v.Screen.Cols
	}
	return Pos{Line: a}, Pos{Line: b, Col: cols}
}

// extend sets the selection head for p in the current unit.
func (v *View) extend(p Pos) {
	switch v.sel.unit {
	case selWord:
		wa, wb := v.wordAt(v.sel.pressAt.Line, v.sel.pressAt.Col)
		pa, pb := v.wordAt(p.Line, p.Col)
		if p.before(Pos{v.sel.pressAt.Line, wa}) {
			v.sel.anchor, v.sel.head = Pos{v.sel.pressAt.Line, wb}, Pos{p.Line, pa}
		} else {
			v.sel.anchor, v.sel.head = Pos{v.sel.pressAt.Line, wa}, Pos{p.Line, pb}
		}
	case selLine:
		a0, a1 := v.lineRange(v.sel.pressAt.Line)
		p0, p1 := v.lineRange(p.Line)
		if p.Line < a0.Line {
			v.sel.anchor, v.sel.head = a1, p0
		} else {
			v.sel.anchor, v.sel.head = a0, p1
		}
	default:
		v.sel.head = p
	}
}

// linkMod reports whether the link modifier (Cmd on macOS, else Ctrl)
// is in m.
func (v *View) linkMod(m key.Modifiers) bool {
	if v.Mac {
		return m.Contain(key.ModCommand) || m.Contain(key.ModSuper)
	}
	return m.Contain(key.ModCtrl)
}

// reporting tells whether pointer events go to the program: it asked
// for mouse reports, the view shows the screen and Shift is not held
// (Shift selects as usual).
func (v *View) reporting(m key.Modifiers) bool {
	return v.Screen != nil && v.Screen.Modes.Mouse != 0 && v.Following() && !m.Contain(key.ModShift)
}

func mouseButton(b pointer.Buttons) vt.MouseButton {
	switch {
	case b.Contain(pointer.ButtonPrimary):
		return vt.MouseLeft
	case b.Contain(pointer.ButtonSecondary):
		return vt.MouseRight
	case b.Contain(pointer.ButtonTertiary):
		return vt.MouseMiddle
	}
	return vt.MouseNone
}

// report emits a mouse event at the cell under p (screen relative).
func (v *View) report(action vt.MouseAction, btn vt.MouseButton, p f32.Point, m key.Modifiers) {
	c := v.cellAt(p, true)
	row, col := c.Line-v.Screen.History, min(c.Col, max(0, v.cols-1))
	if action == vt.MouseMove {
		if (image.Point{col, row}) == v.mouseCell {
			return
		}
	}
	v.mouseCell = image.Point{col, row}
	v.emit(MouseEvent{Mouse: vt.Mouse{Action: action, Button: btn, Row: row, Col: col,
		Shift: m.Contain(key.ModShift), Alt: m.Contain(key.ModAlt), Ctrl: m.Contain(key.ModCtrl)}})
}

func (v *View) processPointer(gtx layout.Context) {
	f := pointer.Filter{Target: v, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Scroll | pointer.Move | pointer.Leave | pointer.Cancel,
		ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}}
	for {
		ev, ok := gtx.Event(f)
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Scroll:
			v.wheel(e.Scroll.Y, e.Position, e.Modifiers)
		case pointer.Move:
			v.shortcut = v.linkMod(e.Modifiers)
			v.hoverAt = v.cellAt(e.Position, true)
			v.hover = v.linkAt(v.hoverAt)
			if v.reporting(e.Modifiers) && v.Screen.Modes.Mouse == vt.MouseModeAny {
				v.report(vt.MouseMove, vt.MouseNone, e.Position, e.Modifiers)
			}
		case pointer.Leave, pointer.Cancel:
			v.hover = nil
			v.sel.dragging = false
			v.mouseBtn = vt.MouseNone
		case pointer.Press:
			gtx.Execute(key.FocusCmd{Tag: v})
			if v.reporting(e.Modifiers) {
				v.mouseBtn = mouseButton(e.Buttons)
				v.report(vt.MousePress, v.mouseBtn, e.Position, e.Modifiers)
				continue
			}
			if !e.Buttons.Contain(pointer.ButtonPrimary) {
				continue
			}
			v.press(e)
		case pointer.Drag:
			if v.mouseBtn != vt.MouseNone {
				v.report(vt.MouseMove, v.mouseBtn, e.Position, e.Modifiers)
				continue
			}
			if v.sel.dragging {
				v.extend(v.cellAt(e.Position, false))
				// Dragging beyond the edges scrolls.
				switch {
				case int(e.Position.Y) < v.origin.Y:
					v.scrollLines(-1)
				case int(e.Position.Y) > v.origin.Y+v.rows*v.met.lineH:
					v.scrollLines(1)
				}
			}
		case pointer.Release:
			if v.mouseBtn != vt.MouseNone {
				v.report(vt.MouseRelease, v.mouseBtn, e.Position, e.Modifiers)
				v.mouseBtn = vt.MouseNone
				continue
			}
			v.release(e)
		}
	}
}

// press starts a selection (single, double or triple click).
func (v *View) press(e pointer.Event) {
	p := v.cellAt(e.Position, false)
	inCell := v.cellAt(e.Position, true)
	v.sel.pressLink = v.linkAt(inCell)
	v.sel.dragging = true
	if e.Modifiers.Contain(key.ModShift) && v.sel.active {
		v.extend(p)
		return
	}
	if e.Time-v.sel.lastPress <= multiClick && v.sel.lastPos.Line == inCell.Line && abs(v.sel.lastPos.Col-inCell.Col) <= 1 && v.sel.clicks > 0 {
		v.sel.clicks = v.sel.clicks%3 + 1
	} else {
		v.sel.clicks = 1
	}
	v.sel.lastPress, v.sel.lastPos = e.Time, inCell
	v.sel.unit = selUnit(v.sel.clicks - 1)
	v.sel.pressAt = inCell
	if v.sel.unit == selChar {
		v.sel.pressAt = p
		v.sel.active, v.sel.anchor, v.sel.head = true, p, p
		return
	}
	v.sel.active = true
	v.extend(inCell)
}

// release ends a selection or follows a link clicked without moving.
func (v *View) release(e pointer.Event) {
	if !v.sel.dragging {
		return
	}
	v.sel.dragging = false
	p := v.cellAt(e.Position, false)
	if v.sel.unit == selChar && p == v.sel.pressAt {
		v.sel.active = false
		l := v.sel.pressLink
		if l != nil && l == v.linkAt(v.cellAt(e.Position, true)) {
			switch {
			case l.URL != "" && v.linkMod(e.Modifiers):
				v.emit(OpenURLEvent{URL: l.URL})
			case l.URL == "" && l.File != "":
				v.emit(LinkEvent{Link: *l})
			}
		}
		return
	}
	if v.CopyOnSelect && v.HasSelection() {
		v.copySelection()
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// wheel scrolls by wheel pixels: programs with mouse reporting get wheel
// buttons, the alternate screen gets arrow keys (alternate scroll mode),
// otherwise the scrollback moves.
func (v *View) wheel(dy float32, p f32.Point, m key.Modifiers) {
	if v.met.lineH <= 0 {
		return
	}
	v.scroll += dy
	n := int(v.scroll / float32(v.met.lineH))
	if n == 0 {
		return
	}
	v.scroll -= float32(n * v.met.lineH)
	if v.reporting(m) {
		btn := vt.MouseWheelDown
		if n < 0 {
			btn, n = vt.MouseWheelUp, -n
		}
		for range min(n, 10) {
			v.mouseCell = image.Point{-1, -1}
			v.report(vt.MousePress, btn, p, m)
		}
		return
	}
	if v.Screen != nil && v.Screen.Modes.AltScreen && v.Screen.Modes.AltScroll {
		name := "Down"
		if n < 0 {
			name, n = "Up", -n
		}
		for range min(n, 10) {
			v.emit(KeyEvent{Key: vt.Key{Name: name}})
		}
		return
	}
	v.scrollLines(n)
}
