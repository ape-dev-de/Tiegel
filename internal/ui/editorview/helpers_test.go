package editorview

import (
	"image"
	"io"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/transfer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// rig drives an Editor through a headless input router.
type rig struct {
	t      testing.TB
	h      *uitest.Harness
	e      *Editor
	events []Event
	clock  int64 // pointer event time in ms
}

func newRig(t testing.TB, text string) *rig {
	t.Helper()
	r := &rig{t: t, h: uitest.NewHarness(image.Pt(800, 600)), e: New(buffer.NewString(text))}
	r.e.Keymap = keymap.Mac()
	t.Cleanup(r.e.Close)
	r.frame()
	return r
}

func (r *rig) widget(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	for {
		ev, ok := r.e.Update(gtx)
		if !ok {
			break
		}
		r.events = append(r.events, ev)
	}
	return r.e.Layout(gtx, th)
}

func (r *rig) frame() { r.h.Frame(r.widget) }

// focus gives the editor the keyboard focus.
func (r *rig) focus() {
	r.h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		r.e.Focus(gtx)
		return r.widget(gtx, th)
	})
	r.frame()
	if !r.e.Focused() {
		r.t.Fatal("editor did not get the focus")
	}
}

// press sends a key chord such as "Cmd+Shift+Z".
func (r *rig) press(chord string) {
	r.t.Helper()
	c, err := keymap.ParseChord(chord)
	if err != nil {
		r.t.Fatal(err)
	}
	name := key.Name(c.Key)
	if names := keymapToGio[c.Key]; len(names) > 0 {
		name = names[0]
	}
	r.h.Router.Queue(key.Event{Name: name, Modifiers: gioMods(c.Mods), State: key.Press})
	r.frame()
}

// typ types text rune by rune through the input method path.
func (r *rig) typ(s string) {
	for _, ch := range s {
		st := r.h.Router.EditorState()
		r.h.Router.Queue(key.EditEvent{Range: st.Selection.Range, Text: string(ch)})
		r.frame()
	}
}

func (r *rig) text() string { return r.e.Buffer().String() }

func (r *rig) expectText(want string) {
	r.t.Helper()
	if got := r.text(); got != want {
		r.t.Fatalf("text = %q, want %q", got, want)
	}
}

// pointAt returns the screen point of the left part of the cell at off.
func (r *rig) pointAt(off int) f32.Point {
	r.t.Helper()
	rect, ok := r.e.AnchorRect(off)
	if !ok {
		r.t.Fatalf("offset %d not visible", off)
	}
	return f32.Pt(float32(rect.Min.X)+1, float32(rect.Min.Y+rect.Dy()/2))
}

func (r *rig) pointer(kind pointer.Kind, p f32.Point, mods key.Modifiers, buttons pointer.Buttons) pointer.Event {
	return pointer.Event{Kind: kind, Source: pointer.Mouse, Position: p, Modifiers: mods, Buttons: buttons,
		Time: msDuration(r.clock)}
}

// click sends a primary click at p, one second after the previous one.
func (r *rig) click(p f32.Point, mods key.Modifiers) {
	r.clock += 1000
	r.clicks(p, mods, 1)
}

// clicks sends n quick clicks at p (double/triple click).
func (r *rig) clicks(p f32.Point, mods key.Modifiers, n int) {
	for i := 0; i < n; i++ {
		r.clock += 50
		r.queueClick(p, mods)
	}
	r.frame()
}

func (r *rig) queueClick(p f32.Point, mods key.Modifiers) {
	r.h.Router.Queue(
		r.pointer(pointer.Move, p, mods, 0),
		r.pointer(pointer.Press, p, mods, pointer.ButtonPrimary),
		r.pointer(pointer.Release, p, mods, 0),
	)
}

func (r *rig) drag(from, to f32.Point, mods key.Modifiers) {
	r.clock += 1000
	r.h.Router.Queue(
		r.pointer(pointer.Move, from, mods, 0),
		r.pointer(pointer.Press, from, mods, pointer.ButtonPrimary),
	)
	r.frame()
	mid := f32.Pt((from.X+to.X)/2, (from.Y+to.Y)/2)
	r.h.Router.Queue(
		r.pointer(pointer.Move, mid, mods, pointer.ButtonPrimary),
		r.pointer(pointer.Move, to, mods, pointer.ButtonPrimary),
	)
	r.frame()
	r.h.Router.Queue(r.pointer(pointer.Release, to, mods, 0))
	r.frame()
}

// paste delivers text as clipboard content if the editor requested it.
func (r *rig) paste(text string) {
	r.t.Helper()
	r.press("Cmd+V")
	if !r.h.Router.ClipboardRequested() {
		r.t.Fatal("paste did not request the clipboard")
	}
	r.h.Router.Queue(transfer.DataEvent{Type: "application/text", Open: func() io.ReadCloser {
		return io.NopCloser(strings.NewReader(text))
	}})
	r.frame()
}

// clipboard returns what the editor last wrote to the clipboard.
func (r *rig) clipboard() string {
	_, data, ok := r.h.Router.WriteClipboard()
	if !ok {
		return ""
	}
	return string(data)
}

func (r *rig) takeEvents() []Event {
	ev := r.events
	r.events = nil
	return ev
}

func msDuration(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }
