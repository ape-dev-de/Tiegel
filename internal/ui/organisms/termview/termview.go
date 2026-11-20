// Package termview is the terminal organism: it draws a snapshot of a
// terminal session (styled cells, cursor, selection, find matches, file
// and web links) on a monospace grid and turns keyboard, mouse and
// clipboard input into events for its owner. It holds no session state
// beyond the view (scroll position, selection); the owner fetches the
// lines the view asks for (Top, Rows) from the terminal service and sets
// Screen every frame.
//
// Rendering is cached per row: a row whose spans, links and hover state
// did not change since the last frame replays its recorded operations
// instead of shaping its text again. Glyphs are placed on the cell grid
// one by one, and box drawing, block elements and powerline separators
// are drawn as geometry so that they join without gaps.
package termview

import (
	"image"
	"io"
	"strings"
	"time"

	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
)

// Event is emitted by Update.
type Event interface{ isEvent() }

// KeyEvent is a key press to encode for the program.
type KeyEvent struct{ Key vt.Key }

// TextEvent is typed text. Erase > 0 first removes that many characters
// typed before (an input method replaced its composition).
type TextEvent struct {
	Text  string
	Erase int
}

// PasteEvent is text pasted from the clipboard.
type PasteEvent struct{ Text string }

// CopyEvent asks to copy the text between two positions (the owner gets
// it from the session and writes the clipboard).
type CopyEvent struct{ From, To Pos }

// LinkEvent is a click on a file location.
type LinkEvent struct{ Link terminal.Link }

// OpenURLEvent is a Cmd+click (Ctrl+click) on a web link.
type OpenURLEvent struct{ URL string }

// MouseEvent is a mouse event for a program that asked for mouse
// reports (the owner sends it; the backend encodes it).
type MouseEvent struct{ Mouse vt.Mouse }

// ResizeEvent reports the grid size that fits the view.
type ResizeEvent struct{ Cols, Rows int }

// FindEvent asks the owner to open the find bar (Cmd+F, Ctrl+Shift+F).
type FindEvent struct{}

// EscapeEvent is Escape while it returns the focus to the editor;
// HideEvent is Shift+Escape (hide the tool window).
type (
	EscapeEvent struct{}
	HideEvent   struct{}
)

// FocusEvent reports a change of the keyboard focus.
type FocusEvent struct{ Focused bool }

func (KeyEvent) isEvent()     {}
func (TextEvent) isEvent()    {}
func (PasteEvent) isEvent()   {}
func (CopyEvent) isEvent()    {}
func (LinkEvent) isEvent()    {}
func (OpenURLEvent) isEvent() {}
func (MouseEvent) isEvent()   {}
func (ResizeEvent) isEvent()  {}
func (FindEvent) isEvent()    {}
func (EscapeEvent) isEvent()  {}
func (HideEvent) isEvent()    {}
func (FocusEvent) isEvent()   {}

// Pos is a cell position: absolute line (scrollback + screen) and column.
type Pos struct{ Line, Col int }

func (p Pos) before(q Pos) bool { return p.Line < q.Line || p.Line == q.Line && p.Col < q.Col }

// selection granularity (single, double, triple click).
type selUnit uint8

const (
	selChar selUnit = iota
	selWord
	selLine
)

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
	// elsewhere (and Cmd- or Ctrl-click for web links).
	Mac bool
	// FontSize is the text size (0: the theme's code size); LineHeight
	// the row height as a multiple of the font height (0: the theme's
	// code line height).
	FontSize   unit.Sp
	LineHeight float32
	// CopyOnSelect copies a mouse selection when the button is released.
	CopyOnSelect bool
	// Notice is shown as the last line (e.g. "process exited").
	Notice string
	// Matches are find results to highlight; Current indexes the one
	// to emphasise (-1: none).
	Matches []terminal.Match
	Current int

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
		unit             selUnit
		// lastPress and clicks count multi-clicks.
		lastPress time.Duration
		lastPos   Pos
		clicks    int
	}
	hover   *terminal.Link
	hoverAt Pos
	// shortcut tells that the link modifier (Cmd, Ctrl) is held.
	shortcut bool
	// ctrlDown and altDown track the held modifiers: on macOS the text
	// of a key (EditEvent) arrives before its key.Event.
	ctrlDown, altDown bool
	// mouse reporting: the held button and the last reported cell.
	mouseBtn  vt.MouseButton
	mouseCell image.Point
	// imePos is the end of the text the input method knows (runes).
	imePos int

	blinkStart time.Time
	bellSeen   int
	bellInit   bool
	bellUntil  time.Time

	cols, rows int
	met        metrics
	origin     image.Point
	events     []Event
	filters    []event.Filter
	rc         renderCache
}

// New returns a view that follows the output.
func New() *View { return &View{follow: true, Current: -1} }

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

// Reveal scrolls so that absolute line is visible (find results).
func (v *View) Reveal(line int) {
	if v.Screen == nil || v.rows <= 0 {
		return
	}
	top := v.Top()
	if line >= top && line < top+v.rows {
		return
	}
	v.scrollLines(line - v.rows/2 - top)
}

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

// Select sets the selection (e.g. to a find match).
func (v *View) Select(from, to Pos) {
	v.sel.active, v.sel.anchor, v.sel.head = true, from, to
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
			v.imePos = 0
			if e.Focus {
				gtx.Execute(key.SoftKeyboardCmd{Show: true})
				v.blinkStart = gtx.Now
			} else {
				v.ctrlDown, v.altDown = false, false
			}
		case key.Event:
			v.trackModifiers(e)
			if e.State == key.Press {
				v.handleKey(gtx, e)
			}
		case key.EditEvent:
			v.edit(e)
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

// edit handles text from the keyboard or an input method. An input
// method replaces its composition (e.g. a dead key ´ followed by e
// becomes é): text it replaces at the end of what it knows is erased
// first.
func (v *View) edit(e key.EditEvent) {
	t := v.editText(e.Text)
	erase := 0
	if e.Range.End == v.imePos && e.Range.Start < e.Range.End && e.Range.Start >= 0 {
		erase = e.Range.End - e.Range.Start
	}
	v.imePos = max(0, e.Range.Start) + len([]rune(e.Text))
	if t == "" && erase == 0 {
		return
	}
	v.typed()
	v.emit(TextEvent{Text: t, Erase: erase})
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
// prompt); the cursor stops blinking for a moment.
func (v *View) typed() {
	v.follow = true
	v.sel.active = false
	v.blinkStart = time.Time{}
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
		case "F":
			v.emit(FindEvent{})
		}
		// Other Cmd chords belong to the IDE (handled before the view).
		return
	case !v.Mac && ctrl && shift && !alt && (e.Name == "C" || e.Name == "V" || e.Name == "F" || e.Name == "A"):
		switch e.Name {
		case "C":
			v.copySelection()
		case "V":
			gtx.Execute(clipboard.ReadCmd{Tag: v})
		case "F":
			v.emit(FindEvent{})
		case "A":
			v.SelectAll()
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
	case shift && !ctrl && !alt && (e.Name == key.NameHome || e.Name == key.NameEnd) && v.Screen != nil && !v.Screen.Modes.AltScreen:
		// Shift+Home/End jump to the start and end of the scrollback.
		if e.Name == key.NameHome {
			v.follow, v.top = false, 0
		} else {
			v.follow = true
		}
		return
	}
	k := vt.Key{Ctrl: ctrl, Alt: alt, Shift: shift}
	if name, ok := namedKeys[e.Name]; ok {
		if name == "Space" && !ctrl && !(alt && v.metaAlt()) {
			return // typed text
		}
		if alt && !v.metaAlt() && name != "Left" && name != "Right" && name != "Backspace" && name != "Enter" {
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
