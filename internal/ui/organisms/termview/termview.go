// Package termview is the terminal organism: it draws a snapshot of a
// terminal session (styled cells, cursor, selection, file links) on a
// monospace grid and turns keyboard, mouse and clipboard input into
// events for its owner. It holds no session state beyond the view (scroll
// position, selection); the owner fetches the lines the view asks for
// (Top, Rows) from the terminal service and sets Screen every frame.
package termview

import (
	"image"
	"image/color"
	"io"
	"math"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"golang.org/x/image/math/fixed"

	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Event is emitted by Update.
type Event interface{ isEvent() }

// KeyEvent is a key press to encode for the program.
type KeyEvent struct{ Key vt.Key }

// TextEvent is typed text.
type TextEvent struct{ Text string }

// PasteEvent is text pasted from the clipboard.
type PasteEvent struct{ Text string }

// CopyEvent asks to copy the text between two positions (the owner gets
// it from the session and writes the clipboard).
type CopyEvent struct{ From, To Pos }

// LinkEvent is a click on a file location.
type LinkEvent struct{ Link terminal.Link }

// ResizeEvent reports the grid size that fits the view.
type ResizeEvent struct{ Cols, Rows int }

// EscapeEvent is Escape while it returns the focus to the editor;
// HideEvent is Shift+Escape (hide the tool window).
type (
	EscapeEvent struct{}
	HideEvent   struct{}
)

// FocusEvent reports a change of the keyboard focus.
type FocusEvent struct{ Focused bool }

func (KeyEvent) isEvent()    {}
func (TextEvent) isEvent()   {}
func (PasteEvent) isEvent()  {}
func (CopyEvent) isEvent()   {}
func (LinkEvent) isEvent()   {}
func (ResizeEvent) isEvent() {}
func (EscapeEvent) isEvent() {}
func (HideEvent) isEvent()   {}
func (FocusEvent) isEvent()  {}

// Pos is a cell position: absolute line (scrollback + screen) and column.
type Pos struct{ Line, Col int }

func (p Pos) before(q Pos) bool { return p.Line < q.Line || p.Line == q.Line && p.Col < q.Col }

// View is the terminal organism. Set the inputs before Update.
type View struct {
	// Screen is the latest snapshot (nil: nothing to show yet).
	Screen *terminal.Screen
	// OptionAsMeta makes Option (Alt) + key send Escape + key on macOS
	// instead of the character the keyboard layout types; on other
	// systems Alt always works as Meta.
	OptionAsMeta bool
	// EscapeToEditor turns Escape into EscapeEvent instead of sending it
	// to the program.
	EscapeToEditor bool
	// Mac selects the clipboard keys: Cmd+C/Cmd+V on macOS, Ctrl+Shift+C/V
	// elsewhere.
	Mac bool
	// FontSize is the text size (0: the theme's code size).
	FontSize unit.Sp
	// Notice is shown as the last line (e.g. "process exited").
	Notice string

	focused  bool
	focusReq bool
	blurReq  bool
	// follow keeps the view at the bottom; otherwise top is the first
	// visible absolute line.
	follow bool
	top    int
	scroll float32 // accumulated wheel pixels

	sel struct {
		active, dragging bool
		anchor, head     Pos
		pressAt          Pos
		pressLink        *terminal.Link
	}
	hover *terminal.Link
	// ctrlDown and altDown track the held modifiers: on macOS the text
	// of a key (EditEvent) arrives before its key.Event.
	ctrlDown, altDown bool

	cols, rows int
	met        metrics
	origin     image.Point
	events     []Event
	filters    []event.Filter
}

// New returns a view that follows the output.
func New() *View { return &View{follow: true} }

// metrics are the cell measurements of the font.
type metrics struct {
	key   metricsKey
	cellW fixed.Int26_6
	lineH int
	textH int
}

type metricsKey struct {
	shaper *text.Shaper
	font   font.Font
	ppem   fixed.Int26_6
}

// Focus requests the keyboard focus on the next frame.
func (v *View) Focus() { v.focusReq, v.blurReq = true, false }

// Blur gives the keyboard focus up on the next frame (if nothing else
// took it meanwhile).
func (v *View) Blur() { v.blurReq, v.focusReq = true, false }

// Focused reports whether the view has the keyboard focus.
func (v *View) Focused() bool { return v.focused }

// Following reports whether the view sticks to the newest output.
func (v *View) Following() bool { return v.follow || v.Screen == nil }

// Top returns the absolute index of the first visible line: the owner
// fetches Rows lines from there (the screen when following).
func (v *View) Top() int {
	if v.Screen == nil {
		return 0
	}
	if v.Following() {
		return v.Screen.History
	}
	return v.top
}

// Rows returns the number of visible rows (0 before the first layout).
func (v *View) Rows() int { return v.rows }

// Cols returns the number of visible columns.
func (v *View) Cols() int { return v.cols }

// ScrollToBottom makes the view follow the output again.
func (v *View) ScrollToBottom() { v.follow = true }

// HasSelection reports whether text is selected.
func (v *View) HasSelection() bool { return v.sel.active && v.sel.anchor != v.sel.head }

// Selection returns the selected range (start before end).
func (v *View) Selection() (from, to Pos) {
	a, b := v.sel.anchor, v.sel.head
	if b.before(a) {
		a, b = b, a
	}
	return a, b
}

// ClearSelection removes the selection.
func (v *View) ClearSelection() { v.sel.active = false }

// SelectAll selects scrollback and screen.
func (v *View) SelectAll() {
	if v.Screen == nil {
		return
	}
	v.sel.active = true
	v.sel.anchor = Pos{}
	v.sel.head = Pos{Line: v.Screen.Total - 1, Col: v.Screen.Cols}
}

func (v *View) emit(e Event) { v.events = append(v.events, e) }

// Update processes input and returns the events (including a
// ResizeEvent of the previous Layout).
func (v *View) Update(gtx layout.Context) []Event {
	// v.events may hold a ResizeEvent of the last Layout.
	if v.focusReq {
		v.focusReq = false
		gtx.Execute(key.FocusCmd{Tag: v})
	}
	if v.blurReq {
		v.blurReq = false
		if gtx.Focused(v) {
			gtx.Execute(key.FocusCmd{})
		}
	}
	v.processKeys(gtx)
	v.processPointer(gtx)
	out := v.events
	v.events = nil
	return out
}

func (v *View) keyFilters() []event.Filter {
	if v.filters != nil {
		return v.filters
	}
	mods := key.ModShift | key.ModCtrl | key.ModAlt | key.ModCommand | key.ModSuper
	v.filters = []event.Filter{
		key.FocusFilter{Target: v},
		transfer.TargetFilter{Target: v, Type: "application/text"},
		// Every key while focused (Tab is a system key and needs its
		// own filter).
		key.Filter{Focus: v, Optional: mods},
		key.Filter{Focus: v, Name: key.NameTab, Optional: mods},
	}
	return v.filters
}

// namedKeys maps Gio key names to vt key names.
var namedKeys = map[key.Name]string{
	key.NameUpArrow: "Up", key.NameDownArrow: "Down", key.NameLeftArrow: "Left", key.NameRightArrow: "Right",
	key.NameHome: "Home", key.NameEnd: "End", key.NamePageUp: "PageUp", key.NamePageDown: "PageDown",
	key.NameDeleteForward: "Delete", key.NameDeleteBackward: "Backspace", key.NameReturn: "Enter", key.NameEnter: "Enter",
	key.NameTab: "Tab", key.NameEscape: "Escape", key.NameSpace: "Space",
	key.NameF1: "F1", key.NameF2: "F2", key.NameF3: "F3", key.NameF4: "F4", key.NameF5: "F5", key.NameF6: "F6",
	key.NameF7: "F7", key.NameF8: "F8", key.NameF9: "F9", key.NameF10: "F10", key.NameF11: "F11", key.NameF12: "F12",
}

func (v *View) processKeys(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(v.keyFilters()...)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case key.FocusEvent:
			if e.Focus != v.focused {
				v.focused = e.Focus
				v.emit(FocusEvent{Focused: e.Focus})
			}
			if e.Focus {
				gtx.Execute(key.SoftKeyboardCmd{Show: true})
			} else {
				v.ctrlDown, v.altDown = false, false
			}
		case key.Event:
			v.trackModifiers(e)
			if e.State == key.Press {
				v.handleKey(gtx, e)
			}
		case key.EditEvent:
			if t := v.editText(e.Text); t != "" {
				v.typed()
				v.emit(TextEvent{Text: t})
			}
		case transfer.DataEvent:
			r := e.Open()
			data, err := io.ReadAll(io.LimitReader(r, 1<<20))
			r.Close()
			if err == nil && len(data) > 0 {
				v.typed()
				v.emit(PasteEvent{Text: string(data)})
			}
		}
	}
	v.focused = gtx.Focused(v)
}

// trackModifiers follows the held Ctrl and Alt keys.
func (v *View) trackModifiers(e key.Event) {
	switch e.Name {
	case key.NameCtrl:
		v.ctrlDown = e.State == key.Press
	case key.NameAlt:
		v.altDown = e.State == key.Press
	default:
		if e.State == key.Press {
			v.ctrlDown = e.Modifiers.Contain(key.ModCtrl)
			v.altDown = e.Modifiers.Contain(key.ModAlt)
		}
	}
}

// altGr reports whether Ctrl+Alt is AltGr (Windows, Linux: the layout
// types characters such as @ with it).
func (v *View) altGr(ctrl, alt bool) bool { return !v.Mac && ctrl && alt }

// editText returns the typed text to send: text of keys the key events
// encode (Ctrl, Alt as Meta, control characters of Enter, Tab,
// Backspace) is dropped.
func (v *View) editText(t string) string {
	if !v.altGr(v.ctrlDown, v.altDown) && (v.ctrlDown || v.altDown && v.metaAlt()) {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, t)
}

// typed scrolls to the bottom and drops the selection (input goes to the
// prompt).
func (v *View) typed() {
	v.follow = true
	v.sel.active = false
}

func (v *View) handleKey(gtx layout.Context, e key.Event) {
	m := e.Modifiers
	cmd := m.Contain(key.ModCommand) || m.Contain(key.ModSuper)
	ctrl, alt, shift := m.Contain(key.ModCtrl), m.Contain(key.ModAlt), m.Contain(key.ModShift)
	// Clipboard and view keys.
	switch {
	case v.Mac && cmd && !ctrl && !alt:
		switch e.Name {
		case "C":
			v.copySelection()
		case "V":
			gtx.Execute(clipboard.ReadCmd{Tag: v})
		case "A":
			v.SelectAll()
		}
		// Other Cmd chords belong to the IDE (handled before the view).
		return
	case !v.Mac && ctrl && shift && !alt && (e.Name == "C" || e.Name == "V"):
		if e.Name == "C" {
			v.copySelection()
		} else {
			gtx.Execute(clipboard.ReadCmd{Tag: v})
		}
		return
	case e.Name == key.NameEscape && !ctrl && !alt:
		if shift {
			v.emit(HideEvent{})
			return
		}
		if v.EscapeToEditor {
			v.emit(EscapeEvent{})
			return
		}
	case shift && !ctrl && !alt && (e.Name == key.NamePageUp || e.Name == key.NamePageDown):
		// Shift+PageUp/Down scroll the scrollback.
		n := max(1, v.rows-1)
		if e.Name == key.NamePageUp {
			n = -n
		}
		v.scrollLines(n)
		return
	}
	k := vt.Key{Ctrl: ctrl, Alt: alt, Shift: shift}
	if name, ok := namedKeys[e.Name]; ok {
		if name == "Space" && !ctrl && !(alt && v.metaAlt()) {
			return // typed text
		}
		if alt && !v.metaAlt() && name != "Left" && name != "Right" && name != "Backspace" {
			// Option on macOS without Meta: the layout types the key.
			k.Alt = false
		}
		k.Name = name
		v.typed()
		v.emit(KeyEvent{Key: k})
		return
	}
	// Single characters: only with Ctrl, or Alt as Meta; plain typing
	// arrives as text.
	r := []rune(string(e.Name))
	if len(r) != 1 {
		return
	}
	if !ctrl && !(alt && v.metaAlt()) || v.altGr(ctrl, alt) {
		return
	}
	if alt && !v.metaAlt() {
		k.Alt = false
	}
	k.Name = string(e.Name)
	if !ctrl && shift {
		// Meta with a shifted symbol: the text is the shifted character;
		// Gio names the key by its unshifted label for letters only.
		k.Shift = true
	}
	v.typed()
	v.emit(KeyEvent{Key: k})
}

// metaAlt reports whether Alt works as Meta.
func (v *View) metaAlt() bool { return !v.Mac || v.OptionAsMeta }

func (v *View) copySelection() {
	if !v.HasSelection() {
		return
	}
	from, to := v.Selection()
	v.emit(CopyEvent{From: from, To: to})
}

func (v *View) scrollLines(n int) {
	if v.Screen == nil || n == 0 {
		return
	}
	top := v.Top() + n
	maxTop := v.Screen.History
	if top >= maxTop {
		v.follow = true
		return
	}
	v.follow = false
	v.top = max(0, top)
}

// ---- pointer ---------------------------------------------------------------

// cellAt returns the position at p: the nearest cell boundary (for
// selections), or with inCell the cell containing p (for links).
func (v *View) cellAt(p f32.Point, inCell bool) Pos {
	if v.met.lineH <= 0 || v.met.cellW <= 0 {
		return Pos{}
	}
	x := float64(int(p.X)-v.origin.X) / (float64(v.met.cellW) / 64)
	y := int(p.Y) - v.origin.Y
	row := max(0, min(v.rows-1, y/v.met.lineH))
	col := int(math.Round(x))
	if inCell {
		col = int(math.Floor(x))
	}
	col = max(0, min(v.cols, col))
	return Pos{Line: v.Top() + row, Col: col}
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
			v.wheel(e.Scroll.Y)
		case pointer.Move:
			v.hover = v.linkAt(v.cellAt(e.Position, true))
		case pointer.Leave, pointer.Cancel:
			v.hover = nil
			v.sel.dragging = false
		case pointer.Press:
			if !e.Buttons.Contain(pointer.ButtonPrimary) {
				continue
			}
			gtx.Execute(key.FocusCmd{Tag: v})
			p := v.cellAt(e.Position, false)
			v.sel.pressAt, v.sel.pressLink = p, v.linkAt(v.cellAt(e.Position, true))
			v.sel.dragging = true
			if e.Modifiers.Contain(key.ModShift) && v.sel.active {
				v.sel.head = p
				continue
			}
			v.sel.active, v.sel.anchor, v.sel.head = true, p, p
		case pointer.Drag:
			if v.sel.dragging {
				p := v.cellAt(e.Position, false)
				v.sel.head = p
				// Dragging beyond the edges scrolls.
				switch {
				case int(e.Position.Y) < v.origin.Y:
					v.scrollLines(-1)
				case int(e.Position.Y) > v.origin.Y+v.rows*v.met.lineH:
					v.scrollLines(1)
				}
			}
		case pointer.Release:
			if !v.sel.dragging {
				continue
			}
			v.sel.dragging = false
			p := v.cellAt(e.Position, false)
			if p == v.sel.pressAt {
				v.sel.active = false
				if l := v.sel.pressLink; l != nil && l == v.linkAt(v.cellAt(e.Position, true)) {
					v.emit(LinkEvent{Link: *l})
				}
			}
		}
	}
}

// wheel scrolls by wheel pixels; on the alternate screen (full-screen
// programs) the wheel sends arrow keys instead.
func (v *View) wheel(dy float32) {
	if v.met.lineH <= 0 {
		return
	}
	v.scroll += dy
	n := int(v.scroll / float32(v.met.lineH))
	if n == 0 {
		return
	}
	v.scroll -= float32(n * v.met.lineH)
	if v.Screen != nil && v.Screen.Modes.AltScreen {
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

// ---- layout ----------------------------------------------------------------

func (v *View) updateMetrics(gtx layout.Context, th *tokens.Theme) {
	size := v.FontSize
	if size <= 0 {
		size = th.Text.Code
	}
	k := metricsKey{shaper: th.Shaper, font: th.Font.Mono, ppem: fixed.I(gtx.Sp(size))}
	if v.met.key == k && v.met.lineH > 0 {
		return
	}
	m := metrics{key: k}
	th.Shaper.LayoutString(text.Parameters{Font: k.font, PxPerEm: k.ppem, MaxWidth: 1 << 20, Locale: gtx.Locale}, "0")
	var asc, desc fixed.Int26_6
	for g, ok := th.Shaper.NextGlyph(); ok; g, ok = th.Shaper.NextGlyph() {
		if m.cellW == 0 {
			m.cellW, asc, desc = g.Advance, g.Ascent, g.Descent
		}
	}
	if m.cellW <= 0 {
		m.cellW = k.ppem * 6 / 10
	}
	if asc+desc <= 0 {
		asc, desc = k.ppem*8/10, k.ppem*2/10
	}
	m.textH = (asc + desc).Ceil()
	m.lineH = max(1, int(math.Ceil(float64(m.textH)*1.2)))
	v.met = m
}

func (m metrics) colX(col int) int { return (fixed.Int26_6(col) * m.cellW).Round() }

// Layout draws the terminal filling the constraints.
func (v *View) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.updateMetrics(gtx, th)
	size := gtx.Constraints.Max
	pad := gtx.Dp(4)
	v.origin = image.Pt(pad, pad)
	cols := max(1, (size.X-2*pad)*64/max(1, int(v.met.cellW)))
	rows := max(1, (size.Y-2*pad)/v.met.lineH)
	if cols != v.cols || rows != v.rows {
		v.cols, v.rows = cols, rows
		v.emit(ResizeEvent{Cols: cols, Rows: rows})
	}
	tp := th.Terminal
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.FillShape(gtx.Ops, tp.Background, clip.Rect{Max: size}.Op())
	event.Op(gtx.Ops, v)
	if v.hover != nil {
		pointer.CursorPointer.Add(gtx.Ops)
	} else {
		pointer.CursorText.Add(gtx.Ops)
	}
	sc := v.Screen
	if sc == nil {
		return layout.Dimensions{Size: size}
	}
	if !v.follow && v.top >= sc.History {
		v.follow = true
	}
	top := v.Top()
	f := th.Font.Mono
	fontSize := v.FontSize
	if fontSize <= 0 {
		fontSize = th.Text.Code
	}
	from, to := v.Selection()
	for row := 0; row < rows; row++ {
		idx := top + row
		i := idx - sc.Top
		y := pad + row*v.met.lineH
		if v.HasSelection() && idx >= from.Line && idx <= to.Line {
			c0, c1 := 0, sc.Cols
			if idx == from.Line {
				c0 = from.Col
			}
			if idx == to.Line {
				c1 = to.Col
			}
			if c1 > c0 {
				r := image.Rect(pad+v.met.colX(c0), y, pad+v.met.colX(c1), y+v.met.lineH)
				draw.Rect(gtx.Ops, r, tp.Selection)
			}
		}
		if i < 0 || i >= len(sc.Lines) {
			continue
		}
		v.layoutLine(gtx, th, f, fontSize, sc.Lines[i], pad, y)
	}
	// Cursor (only when the screen row is visible and the program runs).
	cur := sc.Cursor
	if sc.Running && cur.Visible && sc.History+cur.Row >= top && sc.History+cur.Row < top+rows {
		y := pad + (sc.History+cur.Row-top)*v.met.lineH
		x := pad + v.met.colX(cur.Col)
		r := image.Rect(x, y, x+max(1, v.met.colX(1)), y+v.met.lineH)
		switch {
		case !v.focused:
			draw.Border(gtx.Ops, r, 0, max(1, gtx.Dp(1)), tp.Cursor)
		case cur.Shape >= 5:
			draw.Rect(gtx.Ops, image.Rect(x, y, x+max(1, gtx.Dp(2)), y+v.met.lineH), tp.Cursor)
		case cur.Shape == 3 || cur.Shape == 4:
			draw.Rect(gtx.Ops, image.Rect(x, y+v.met.lineH-max(1, gtx.Dp(2)), r.Max.X, y+v.met.lineH), tp.Cursor)
		default:
			draw.Rect(gtx.Ops, r, tokens.WithAlpha(tp.Cursor, 0xb0))
		}
	}
	if v.Notice != "" && sc.Total-top <= rows {
		y := pad + (sc.Total-top)*v.met.lineH
		if y+v.met.lineH <= size.Y {
			t := op.Offset(image.Pt(pad, y+(v.met.lineH-v.met.textH)/2)).Push(gtx.Ops)
			draw.Text(gtx, th.Shaper, draw.TextOpts{Font: th.Font.UI, Size: th.Text.Small, Color: th.Color.TextMuted, MaxLines: 1}, v.Notice)
			t.Pop()
		}
	}
	return layout.Dimensions{Size: size}
}

// colors resolves the colors of a span.
func colors(th *tokens.Theme, s terminal.Span) (fg, bg color.NRGBA, bgSet bool) {
	tp := th.Terminal
	resolve := func(c string, def color.NRGBA, bold bool) (color.NRGBA, bool) {
		i, r, g, b, ok := terminal.ParseColor(c)
		switch {
		case !ok:
			return def, false
		case i >= 0:
			if bold && i < 8 {
				i += 8 // bold brightens the normal colors
			}
			return th.TerminalColor(uint8(i)), true
		}
		return color.NRGBA{R: r, G: g, B: b, A: 0xff}, true
	}
	fg, _ = resolve(s.FG, tp.Foreground, s.Bold)
	bg, bgSet = resolve(s.BG, tp.Background, false)
	if s.Inverse {
		fg, bg, bgSet = bg, fg, true
	}
	if s.Faint {
		fg = tokens.Mix(fg, bg, 0.4)
	}
	return fg, bg, bgSet
}

func (v *View) layoutLine(gtx layout.Context, th *tokens.Theme, f font.Font, size unit.Sp, l terminal.ScreenLine, pad, y int) {
	m := v.met
	for _, s := range l.Spans {
		fg, bg, bgSet := colors(th, s)
		x0, x1 := pad+m.colX(s.Col), pad+m.colX(s.Col+s.Width)
		if bgSet {
			draw.Rect(gtx.Ops, image.Rect(x0, y, x1, y+m.lineH), bg)
		}
		if s.Hidden || strings.TrimSpace(s.Text) == "" {
			continue
		}
		sf := f
		if s.Bold {
			sf.Weight = font.Bold
		}
		if s.Italic {
			sf.Style = font.Italic
		}
		t := op.Offset(image.Pt(x0, y+(m.lineH-m.textH)/2)).Push(gtx.Ops)
		lgtx := gtx
		lgtx.Constraints = layout.Exact(image.Pt(max(1, x1-x0+m.colX(2)), m.lineH))
		lgtx.Constraints.Min = image.Point{}
		rec := op.Record(gtx.Ops)
		paint.ColorOp{Color: fg}.Add(gtx.Ops)
		mat := rec.Stop()
		widget.Label{MaxLines: 1}.Layout(lgtx, th.Shaper, sf, size, s.Text, mat)
		t.Pop()
		lw := max(1, gtx.Dp(1))
		if s.Underline {
			draw.Rect(gtx.Ops, image.Rect(x0, y+m.lineH-lw-1, x1, y+m.lineH-1), fg)
		}
		if s.Strike {
			draw.Rect(gtx.Ops, image.Rect(x0, y+m.lineH/2, x1, y+m.lineH/2+lw), fg)
		}
	}
	for i := range l.Links {
		lk := &l.Links[i]
		c := th.Terminal.Link
		if v.hover == nil || v.hover.Col != lk.Col || v.hover.File != lk.File {
			c = tokens.WithAlpha(c, 0x90)
		}
		lw := max(1, gtx.Dp(1))
		draw.Rect(gtx.Ops, image.Rect(pad+m.colX(lk.Col), y+m.lineH-lw, pad+m.colX(lk.EndCol), y+m.lineH), c)
	}
}
