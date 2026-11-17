package editorview

import (
	"image"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/transfer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
)

// dragMode is what a pointer drag currently does.
type dragMode uint8

const (
	dragNone dragMode = iota
	dragChars
	dragWords
	dragLines
	dragBlockPending // Alt+press; becomes dragBlock once the pointer moves
	dragBlock
	dragVBar
	dragHBar
	dragGutter
)

// pointerState tracks clicks and drags.
type pointerState struct {
	mode       dragMode
	clicks     int
	lastPress  time.Duration
	lastPos    f32.Point
	anchor     cursor.Selection // word/line or char anchor of the drag
	blockLine  int              // block selection anchor
	blockCol   int
	pressPos   f32.Point
	grabOffset int // scrollbar thumb grab offset
	before     cursor.State
}

const (
	doubleClickTime = 400 * time.Millisecond
	doubleClickDist = 4
)

// processInput handles all pending pointer, key, text and clipboard
// events. It must run every frame so the router learns the filters.
func (e *Editor) processInput(gtx layout.Context) {
	e.processPointer(gtx)
	e.processKeys(gtx)
}

// ---- keyboard ---------------------------------------------------------------

var gioKeyNames = map[key.Name]string{
	key.NameLeftArrow: keymap.KeyLeft, key.NameRightArrow: keymap.KeyRight,
	key.NameUpArrow: keymap.KeyUp, key.NameDownArrow: keymap.KeyDown,
	key.NameHome: keymap.KeyHome, key.NameEnd: keymap.KeyEnd,
	key.NamePageUp: keymap.KeyPageUp, key.NamePageDown: keymap.KeyPageDown,
	key.NameDeleteBackward: keymap.KeyBackspace, key.NameDeleteForward: keymap.KeyDelete,
	key.NameReturn: keymap.KeyEnter, key.NameEnter: keymap.KeyEnter,
	key.NameTab: keymap.KeyTab, key.NameEscape: keymap.KeyEscape, key.NameSpace: keymap.KeySpace,
}

var keymapToGio = func() map[string][]key.Name {
	m := map[string][]key.Name{}
	for g, k := range gioKeyNames {
		m[k] = append(m[k], g)
	}
	return m
}()

// shiftedSymbols maps the US-layout shifted form of a symbol key to its
// base key, so "Cmd+Shift+/" matches whether the platform reports "/" or
// "?".
var shiftedSymbols = map[string]string{
	"?": "/", "_": "-", "+": "=", "{": "[", "}": "]", "|": "\\", ":": ";", "\"": "'",
	"<": ",", ">": ".", "~": "`", "!": "1", "@": "2", "#": "3", "$": "4", "%": "5",
	"^": "6", "&": "7", "*": "8", "(": "9", ")": "0",
}

var unshiftedSymbols = func() map[string]string {
	m := map[string]string{}
	for s, b := range shiftedSymbols {
		m[b] = s
	}
	return m
}()

// ChordOf converts a Gio key event into a keymap chord.
func ChordOf(ev key.Event) keymap.Chord {
	var c keymap.Chord
	m := ev.Modifiers
	if m.Contain(key.ModShift) {
		c.Mods |= keymap.Shift
	}
	if m.Contain(key.ModCtrl) {
		c.Mods |= keymap.Ctrl
	}
	if m.Contain(key.ModAlt) {
		c.Mods |= keymap.Alt
	}
	if m.Contain(key.ModCommand) || m.Contain(key.ModSuper) {
		c.Mods |= keymap.Cmd
	}
	name := string(ev.Name)
	if k, ok := gioKeyNames[ev.Name]; ok {
		name = k
	} else if b, ok := shiftedSymbols[name]; ok && c.Mods&keymap.Shift != 0 {
		name = b
	}
	c.Key = name
	return c
}

func gioMods(m keymap.Mods) key.Modifiers {
	var g key.Modifiers
	if m&keymap.Shift != 0 {
		g |= key.ModShift
	}
	if m&keymap.Ctrl != 0 {
		g |= key.ModCtrl
	}
	if m&keymap.Alt != 0 {
		g |= key.ModAlt
	}
	if m&keymap.Cmd != 0 {
		g |= key.ModCommand
	}
	return g
}

// keyFilters returns the event filters of the editor: focus/text input,
// paste data and one key filter per bound chord.
func (e *Editor) keyFilters() []event.Filter {
	if e.filtersFor == e.Keymap && e.filtersVersion == e.Keymap.Version() && len(e.filters) > 0 {
		return e.filters
	}
	e.filtersFor, e.filtersVersion = e.Keymap, e.Keymap.Version()
	f := e.filters[:0]
	f = append(f, key.FocusFilter{Target: e}, transfer.TargetFilter{Target: e, Type: "application/text"})
	for _, b := range e.Keymap.Bindings() {
		mods := gioMods(b.Chord.Mods)
		names := keymapToGio[b.Chord.Key]
		if names == nil {
			names = []key.Name{key.Name(b.Chord.Key)}
			if s, ok := unshiftedSymbols[b.Chord.Key]; ok && b.Chord.Mods&keymap.Shift != 0 {
				names = append(names, key.Name(s))
			}
		}
		for _, n := range names {
			f = append(f, key.Filter{Focus: e, Name: n, Required: mods})
		}
	}
	e.filters = f
	return f
}

func (e *Editor) processKeys(gtx layout.Context) {
	filters := e.keyFilters()
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			break
		}
		switch ev := ev.(type) {
		case key.FocusEvent:
			e.ime.reset()
			if ev.Focus && !e.ReadOnly {
				gtx.Execute(key.SoftKeyboardCmd{Show: true})
			}
			if ev.Focus != e.focused {
				e.focused = ev.Focus
				e.emit(FocusEvent{Focused: ev.Focus})
			}
			e.blinkStart = gtx.Now
		case key.Event:
			if ev.State != key.Press {
				break
			}
			if a, ok := e.Keymap.Lookup(ChordOf(ev)); ok {
				e.blinkStart = gtx.Now
				e.Do(gtx, a)
			}
		case key.EditEvent:
			e.blinkStart = gtx.Now
			e.handleEdit(ev)
		case key.SelectionEvent:
			e.blinkStart = gtx.Now
			e.handleIMESelection(ev)
		case key.SnippetEvent:
			e.ime.snippet = ""
		case key.CompositionEvent:
			e.ime.composition = key.Range(ev)
		case transfer.DataEvent:
			r := ev.Open()
			data, err := io.ReadAll(r)
			r.Close()
			if err == nil && !e.ReadOnly {
				e.pasteText(string(data))
			}
		}
	}
	e.focused = gtx.Focused(e)
}

// Focus requests the keyboard focus.
func (e *Editor) Focus(gtx layout.Context) {
	gtx.Execute(key.FocusCmd{Tag: e})
}

// ---- pointer ----------------------------------------------------------------

func (e *Editor) processPointer(gtx layout.Context) {
	g := &e.geo
	f := pointer.Filter{
		Target:  e,
		Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Scroll | pointer.Cancel,
		ScrollX: pointer.ScrollRange{Min: -e.scroll.X, Max: g.maxScroll.X - e.scroll.X},
		ScrollY: pointer.ScrollRange{Min: -e.scroll.Y, Max: g.maxScroll.Y - e.scroll.Y},
	}
	for {
		ev, ok := gtx.Event(f)
		if !ok {
			break
		}
		pe, ok := ev.(pointer.Event)
		if !ok || !g.valid {
			continue
		}
		switch pe.Kind {
		case pointer.Scroll:
			d := pe.Scroll
			if pe.Modifiers.Contain(key.ModShift) && d.X == 0 {
				d.X, d.Y = d.Y, 0
			}
			e.scroll.X += int(math.Round(float64(d.X)))
			e.scroll.Y += int(math.Round(float64(d.Y)))
			e.clampScroll()
		case pointer.Press:
			if pe.Buttons.Contain(pointer.ButtonPrimary) || pe.Source != pointer.Mouse {
				e.press(gtx, pe)
			} else if pe.Buttons.Contain(pointer.ButtonSecondary) {
				e.secondaryPress(pe)
			}
		case pointer.Drag:
			e.drag(gtx, pe)
		case pointer.Release, pointer.Cancel:
			switch e.ptr.mode {
			case dragChars, dragWords, dragLines, dragBlock:
				e.RevealCaret()
			}
			e.ptr.mode = dragNone
		}
	}
}

func mods(m key.Modifiers) keymap.Mods {
	return ChordOf(key.Event{Modifiers: m}).Mods
}

func (e *Editor) press(gtx layout.Context, pe pointer.Event) {
	g := &e.geo
	p := pe.Position
	pt := image.Pt(int(p.X), int(p.Y))
	// Click counting.
	if pe.Time-e.ptr.lastPress < doubleClickTime && abs(p.X-e.ptr.lastPos.X) < doubleClickDist &&
		abs(p.Y-e.ptr.lastPos.Y) < doubleClickDist {
		e.ptr.clicks++
	} else {
		e.ptr.clicks = 1
	}
	e.ptr.lastPress, e.ptr.lastPos, e.ptr.pressPos = pe.Time, p, p
	e.blinkStart = gtx.Now

	switch {
	case pt.In(g.vbar) && !g.vthumb.Empty():
		e.ptr.mode = dragVBar
		if pt.In(g.vthumb) {
			e.ptr.grabOffset = pt.Y - g.vthumb.Min.Y
		} else {
			e.ptr.grabOffset = g.vthumb.Dy() / 2
			e.dragVBarTo(pt.Y)
		}
		return
	case pt.In(g.hbar) && !g.hthumb.Empty():
		e.ptr.mode = dragHBar
		if pt.In(g.hthumb) {
			e.ptr.grabOffset = pt.X - g.hthumb.Min.X
		} else {
			e.ptr.grabOffset = g.hthumb.Dx() / 2
			e.dragHBarTo(pt.X)
		}
		return
	}
	gtx.Execute(key.FocusCmd{Tag: e})
	if !e.ReadOnly {
		gtx.Execute(key.SoftKeyboardCmd{Show: true})
	}
	if pt.X < g.gutterW {
		e.gutterPress(pe, pt)
		return
	}
	off, rowEnd := e.caretAt(pt)
	caret := cursor.Caret(off)
	caret.RowEnd = rowEnd
	s := e.buf.Snapshot()
	m := pe.Modifiers
	e.expand = e.expand[:0]
	switch {
	case m.Contain(key.ModAlt) && !m.Contain(key.ModShift):
		e.ptr.before = e.cur.State()
		e.ptr.blockLine, e.ptr.blockCol = e.lineColAt(pt)
		if e.ptr.clicks == 1 {
			e.cur.AddSelection(caret)
		}
		e.ptr.mode = dragBlockPending
	case m.Contain(key.ModShift):
		sels := e.cur.Selections()
		pi := e.cur.PrimaryIndex()
		sels[pi] = cursor.Selection{Anchor: sels[pi].Anchor, Head: off, RowEnd: rowEnd}
		e.cur.SetSelections(sels, pi)
		e.ptr.anchor = cursor.Caret(sels[pi].Anchor)
		e.ptr.mode = dragChars
	case e.ptr.clicks == 2:
		start, end := cursor.WordAt(s, off)
		e.cur.SetSelections([]cursor.Selection{cursor.Range(start, end)}, 0)
		e.ptr.anchor = cursor.Range(start, end)
		e.ptr.mode = dragWords
	case e.ptr.clicks >= 3:
		start, end := lineRange(s, off)
		e.cur.SetSelections([]cursor.Selection{cursor.Range(start, end)}, 0)
		e.ptr.anchor = cursor.Range(start, end)
		e.ptr.mode = dragLines
	default:
		e.cur.SetSelections([]cursor.Selection{caret}, 0)
		e.ptr.anchor = cursor.Caret(off)
		e.ptr.mode = dragChars
	}
	e.revealCarets()
}

func (e *Editor) gutterPress(pe pointer.Event, pt image.Point) {
	g := &e.geo
	row := (pt.Y + e.scroll.Y) / max(1, g.lineH)
	if pt.Y+e.scroll.Y < 0 || row >= g.rows {
		return
	}
	line := e.rowLine(row)
	area := GutterIcons
	switch {
	case pt.X < g.annotW:
		area = GutterAnnotation
	case pt.X >= g.foldX:
		area = GutterFold
		if r, ok := e.folds.RangeAt(line); ok && r.EndLine > r.StartLine {
			e.folds.Toggle(line)
			e.caretsOutOfFolds()
		}
	case pt.X >= g.chgX:
		area = GutterChanges
	case pt.X >= g.iconX+g.iconW:
		area = GutterNumbers
	}
	e.emit(GutterClickEvent{Line: line, Area: area, Mods: mods(pe.Modifiers), Clicks: e.ptr.clicks})
	e.ptr.mode = dragGutter
}

// secondaryPress reports a right click into the gutter (e.g. to edit a
// breakpoint); right clicks into the text are ignored.
func (e *Editor) secondaryPress(pe pointer.Event) {
	g := &e.geo
	pt := image.Pt(int(pe.Position.X), int(pe.Position.Y))
	if pt.X >= g.gutterW || pt.X >= g.foldX {
		return
	}
	row := (pt.Y + e.scroll.Y) / max(1, g.lineH)
	if pt.Y+e.scroll.Y < 0 || row >= g.rows {
		return
	}
	area := GutterIcons
	switch {
	case pt.X < g.annotW:
		area = GutterAnnotation
	case pt.X >= g.chgX:
		area = GutterChanges
	case pt.X >= g.iconX+g.iconW:
		area = GutterNumbers
	}
	e.emit(GutterClickEvent{Line: e.rowLine(row), Area: area, Mods: mods(pe.Modifiers), Clicks: 1, Secondary: true})
}

func (e *Editor) drag(gtx layout.Context, pe pointer.Event) {
	g := &e.geo
	p := pe.Position
	pt := image.Pt(int(p.X), int(p.Y))
	switch e.ptr.mode {
	case dragVBar:
		e.dragVBarTo(pt.Y)
		return
	case dragHBar:
		e.dragHBarTo(pt.X)
		return
	case dragNone, dragGutter:
		return
	}
	// Auto-scroll when dragging outside the view.
	switch {
	case pt.Y < 0:
		e.scroll.Y -= g.lineH
	case pt.Y > g.size.Y:
		e.scroll.Y += g.lineH
	}
	switch {
	case pt.X < g.gutterW && e.scroll.X > 0:
		e.scroll.X -= e.met.colX(2)
	case pt.X > g.size.X:
		e.scroll.X += e.met.colX(2)
	}
	e.clampScroll()
	pt.Y = min(max(pt.Y, 0), g.size.Y-1)
	off, rowEnd := e.caretAt(pt)
	s := e.buf.Snapshot()
	a := e.ptr.anchor
	switch e.ptr.mode {
	case dragChars:
		sels := e.cur.Selections()
		pi := e.cur.PrimaryIndex()
		sels[pi] = cursor.Selection{Anchor: a.Anchor, Head: off, RowEnd: rowEnd}
		e.cur.SetSelections(sels, pi)
	case dragWords:
		start, end := cursor.WordAt(s, off)
		e.cur.SetSelections([]cursor.Selection{extendUnit(a, start, end, off)}, 0)
	case dragLines:
		start, end := lineRange(s, off)
		e.cur.SetSelections([]cursor.Selection{extendUnit(a, start, end, off)}, 0)
	case dragBlockPending:
		if abs(p.X-e.ptr.pressPos.X) < doubleClickDist && abs(p.Y-e.ptr.pressPos.Y) < doubleClickDist {
			return
		}
		e.ptr.mode = dragBlock
		fallthrough
	case dragBlock:
		line, col := e.lineColAt(pt)
		e.blockSelect(s, e.ptr.blockLine, e.ptr.blockCol, line, col)
	}
	e.blinkStart = gtx.Now
}

// extendUnit extends a word/line anchor selection to the unit [start,
// end) at off.
func extendUnit(a cursor.Selection, start, end, off int) cursor.Selection {
	if off < a.Start() {
		return cursor.Range(a.End(), start)
	}
	return cursor.Range(a.Start(), max(end, a.End()))
}

// blockSelect selects the rectangle between two (line, visual column)
// corners: one selection per visible line.
func (e *Editor) blockSelect(s buffer.Snapshot, l0, c0, l1, c1 int) {
	if l0 > l1 {
		l0, l1 = l1, l0
	}
	tw := e.tabWidth()
	var sels []cursor.Selection
	const maxLines = 10000
	for l := l0; l <= l1 && len(sels) < maxLines; l++ {
		if e.folds.IsHidden(l) {
			continue
		}
		line := s.Line(l)
		ls := s.LineStart(l)
		a := ls + buffer.ByteColFromVisual(line, c0, tw)
		h := ls + buffer.ByteColFromVisual(line, c1, tw)
		sels = append(sels, cursor.Range(a, h))
	}
	e.cur.SetSelections(sels, len(sels)-1)
}

func (e *Editor) dragVBarTo(y int) {
	g := &e.geo
	track := g.size.Y - g.vthumb.Dy()
	if track <= 0 {
		return
	}
	e.scroll.Y = (y - e.ptr.grabOffset) * g.maxScroll.Y / track
	e.clampScroll()
}

func (e *Editor) dragHBarTo(x int) {
	g := &e.geo
	track := g.hbar.Dx() - g.hthumb.Dx()
	if track <= 0 {
		return
	}
	e.scroll.X = (x - g.hbar.Min.X - e.ptr.grabOffset) * g.maxScroll.X / track
	e.clampScroll()
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// lineRange returns the full line (with line ending) containing off.
func lineRange(s buffer.Snapshot, off int) (int, int) {
	l := s.OffsetToPos(off).Line
	end := s.Len()
	if l+1 < s.LineCount() {
		end = s.LineStart(l + 1)
	}
	return s.LineStart(l), end
}

// rowColAt maps a point to a visual row and a visual column in it.
func (e *Editor) rowColAt(pt image.Point) (row, col int) {
	g := &e.geo
	y := pt.Y + e.scroll.Y
	if y > 0 {
		row = y / max(1, g.lineH)
	}
	row = min(row, max(0, g.rows-1))
	x := float64(pt.X - g.textX + e.scroll.X)
	cw := float64(e.met.cellW) / 64
	if cw > 0 && x > 0 {
		col = int(math.Floor(x/cw + 0.5))
	}
	return row, col
}

// lineColAt maps a point to a document line and a visual column of the
// whole line (as blockSelect expects): on a continuation row of a wrapped
// line the row-local column is converted to the line column of the same
// position; columns past the row end keep their distance from it.
func (e *Editor) lineColAt(pt image.Point) (line, col int) {
	row, col := e.rowColAt(pt)
	if !e.wrapped {
		return e.rowLine(row), col
	}
	line, sub := e.view.LineAt(row)
	starts, text := e.view.Segments(e.buf.Snapshot(), line)
	sub = min(sub, len(starts)-1)
	a, b := starts[sub], len(text)
	if sub+1 < len(starts) {
		b = starts[sub+1]
	}
	tw := e.tabWidth()
	c := buffer.ByteColFromVisual(text[a:b], col, tw)
	past := max(0, col-buffer.VisualCol(text[a:b], c, tw))
	return line, buffer.VisualCol(text, a+c, tw) + past
}

// offsetAt maps a point in editor coordinates to the nearest document
// offset (a grapheme boundary).
func (e *Editor) offsetAt(pt image.Point) int {
	s := e.buf.Snapshot()
	row, col := e.rowColAt(pt)
	if e.wrapped {
		return e.view.OffsetAt(s, row, col)
	}
	line := e.folds.VisibleToDoc(row)
	return s.LineStart(line) + buffer.ByteColFromVisual(s.Line(line), col, e.tabWidth())
}

// OffsetAt returns the document offset at a point in the editor's
// coordinate space of the last Layout (e.g. for hover requests).
func (e *Editor) OffsetAt(pt image.Point) int { return e.offsetAt(pt) }

// ---- text input and IME -----------------------------------------------------

// imeState mirrors what the input method knows. Gio addresses text in
// runes from the start of the document; to stay O(line) on huge files the
// editor exposes only a window of the document around the primary caret
// – rune 0 of the IME's view is byte base of the buffer.
type imeState struct {
	base        int
	baseValid   bool
	snippet     string
	sel         key.Range
	caret       key.Caret
	composition key.Range
	version     uint64
}

// maxIMEWindow bounds the bytes exposed to the input method.
const maxIMEWindow = 4096

func (s *imeState) reset() {
	*s = imeState{composition: key.Range{Start: -1, End: -1}}
}

func (e *Editor) composing() bool { return e.ime.composition.Start >= 0 }

// imeWindow returns the byte range exposed to the input method.
func (e *Editor) imeWindow(s buffer.Snapshot) (int, int) {
	p := e.cur.Primary()
	first := s.OffsetToPos(p.Start()).Line
	last := s.OffsetToPos(p.End()).Line
	last = min(last, first+16)
	start, end := s.LineStart(first), s.LineEnd(last)
	if end-start > maxIMEWindow {
		h := p.Head
		start = max(start, h-maxIMEWindow/2)
		end = min(end, start+maxIMEWindow)
		for start < h {
			if b, ok := s.ByteAt(start); !ok || utf8.RuneStart(b) {
				break
			}
			start++
		}
	}
	return start, end
}

// byteOfRune maps a rune index of the IME window to a document offset,
// walking the current buffer from the window base.
func (e *Editor) byteOfRune(r int) int {
	s := e.buf.Snapshot()
	off := e.ime.base
	if r <= 0 {
		return off
	}
	end := min(s.Len(), off+r*utf8.UTFMax)
	n := 0
	res := end
	s.ForEachChunk(off, end, func(base int, chunk []byte) bool {
		for i := 0; i < len(chunk); {
			if n == r {
				res = base + i
				return false
			}
			_, size := utf8.DecodeRune(chunk[i:])
			i += size
			n++
		}
		return true
	})
	if n < r {
		return end
	}
	return res
}

// runeOfByte counts the runes between the window base and off.
func (e *Editor) runeOfByte(off int) int {
	if off <= e.ime.base {
		return 0
	}
	return utf8.RuneCount(e.buf.Slice(e.ime.base, off))
}

func (e *Editor) compositionRange() (int, int, bool) {
	if !e.composing() || !e.ime.baseValid {
		return 0, 0, false
	}
	a, b := e.byteOfRune(e.ime.composition.Start), e.byteOfRune(e.ime.composition.End)
	if a > b {
		a, b = b, a
	}
	return a, b, b > a
}

// handleEdit applies an input method edit. Plain typing (replacing the
// current selection) goes through the cursor model so it applies to all
// cursors with auto-closing; compositions edit the primary cursor only.
func (e *Editor) handleEdit(ev key.EditEvent) {
	if e.ReadOnly {
		return
	}
	if !e.ime.baseValid {
		e.syncIMEBase()
	}
	lo, hi := ev.Range.Start, ev.Range.End
	if lo > hi {
		lo, hi = hi, lo
	}
	start, end := e.byteOfRune(lo), e.byteOfRune(hi)
	p := e.cur.Primary()
	txt := ev.Text
	e.expand = e.expand[:0]
	if !e.composing() && start == p.Start() && end == p.End() {
		switch {
		case txt == "\n" || txt == "\r" || txt == "\r\n":
			e.cur.Newline()
		case utf8.RuneCountInString(txt) == 1:
			r, _ := utf8.DecodeRuneInString(txt)
			e.cur.Type(r)
		case txt == "":
			e.cur.DeleteBackward()
		default:
			e.cur.Insert(txt)
		}
	} else {
		if _, err := e.buf.Replace(start, end-start, []byte(txt)); err != nil {
			return
		}
		sels := e.cur.Selections()
		pi := e.cur.PrimaryIndex()
		sels[pi] = cursor.Caret(start + len(txt))
		e.cur.SetSelections(sels, pi)
	}
	e.RevealCaret()
}

func (e *Editor) handleIMESelection(ev key.SelectionEvent) {
	if !e.ime.baseValid {
		e.syncIMEBase()
	}
	head, anchor := e.byteOfRune(ev.Start), e.byteOfRune(ev.End)
	sels := e.cur.Selections()
	pi := e.cur.PrimaryIndex()
	sels[pi] = cursor.Range(anchor, head)
	e.cur.SetSelections(sels, pi)
	e.RevealCaret()
}

func (e *Editor) syncIMEBase() {
	start, _ := e.imeWindow(e.buf.Snapshot())
	e.ime.base, e.ime.baseValid = start, true
}

// updateIME tells the input method about the text window, the selection
// and the caret position.
func (e *Editor) updateIME(gtx layout.Context) {
	if !e.focused {
		return
	}
	s := e.buf.Snapshot()
	start, end := e.imeWindow(s)
	if e.composing() && e.ime.baseValid && e.ime.base <= s.Len() {
		start = min(start, e.ime.base)
		end = max(end, start)
	}
	if !e.ime.baseValid || start != e.ime.base || s.Version() != e.ime.version || e.ime.snippet == "" {
		e.ime.base, e.ime.baseValid, e.ime.version = start, true, s.Version()
		txt := string(s.Slice(start, end))
		if txt != e.ime.snippet || e.ime.snippet == "" {
			e.ime.snippet = txt
			gtx.Execute(key.SnippetCmd{Tag: e, Snippet: key.Snippet{
				Range: key.Range{Start: 0, End: utf8.RuneCountInString(txt)},
				Text:  txt,
			}})
		}
	}
	p := e.cur.Primary()
	sel := key.Range{Start: e.runeOfByte(p.Head), End: e.runeOfByte(p.Anchor)}
	var caret key.Caret
	if r, ok := e.caretRect(p); ok {
		caret = key.Caret{
			Pos:     f32.Pt(float32(r.Min.X), float32(r.Min.Y+e.met.base)),
			Ascent:  float32(e.met.ascent.Ceil()),
			Descent: float32(e.met.descent.Ceil()),
		}
	}
	var bounds image.Rectangle
	if a, b, ok := e.compositionRange(); ok {
		ra, oka := e.AnchorRect(a)
		rb, okb := e.AnchorRect(b)
		if oka && okb && ra.Min.Y == rb.Min.Y {
			bounds = image.Rect(ra.Min.X, ra.Min.Y, rb.Min.X, ra.Max.Y)
		}
	}
	if sel != e.ime.sel || caret != e.ime.caret {
		e.ime.sel, e.ime.caret = sel, caret
		gtx.Execute(key.SelectionCmd{Tag: e, Range: sel, Caret: caret, CompositionBounds: bounds})
	}
}

// ---- clipboard --------------------------------------------------------------

// copyText returns the clipboard text for the selections: the selected
// texts joined by line endings, or – if nothing is selected – the lines
// of all carets (then lines is true).
func (e *Editor) copyText() (text string, lines bool) {
	s := e.buf.Snapshot()
	eol := e.buf.EOL().String()
	sels := e.cur.Selections()
	empty := true
	for _, sel := range sels {
		if !sel.Empty() {
			empty = false
			break
		}
	}
	var b strings.Builder
	if empty {
		last := -1
		for _, sel := range sels {
			l := s.OffsetToPos(sel.Head).Line
			if l == last {
				continue
			}
			last = l
			b.Write(s.Line(l))
			b.WriteString(eol)
		}
		return b.String(), true
	}
	for i, t := range e.cur.SelectedText() {
		if i > 0 {
			b.WriteString(eol)
		}
		b.WriteString(t)
	}
	return b.String(), false
}

func (e *Editor) pasteText(text string) {
	if text == "" {
		return
	}
	allEmpty := true
	for _, sel := range e.cur.Selections() {
		if !sel.Empty() {
			allEmpty = false
			break
		}
	}
	if allEmpty && e.lineClip != "" && text == e.lineClip {
		e.cur.PasteLines(text)
	} else {
		e.cur.Paste(text)
	}
	e.RevealCaret()
}
