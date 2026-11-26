package editorview

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/cursor"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// dragWith drags from one point to another with the given buttons held.
func (r *rig) dragWith(from, to f32.Point, mods key.Modifiers, btn pointer.Buttons) {
	r.clock += 1000
	r.h.Router.Queue(
		r.pointer(pointer.Move, from, mods, 0),
		r.pointer(pointer.Press, from, mods, btn),
	)
	r.frame()
	mid := f32.Pt((from.X+to.X)/2, (from.Y+to.Y)/2)
	r.h.Router.Queue(
		r.pointer(pointer.Move, mid, mods, btn),
		r.pointer(pointer.Move, to, mods, btn),
	)
	r.frame()
	r.h.Router.Queue(r.pointer(pointer.Release, to, mods, 0))
	r.frame()
}

// modKey presses or releases a modifier key.
func (r *rig) modKey(name key.Name, press bool) {
	st := key.Release
	if press {
		st = key.Press
	}
	r.h.Router.Queue(key.Event{Name: name, State: st})
	r.frame()
}

func (r *rig) selTexts() []string { return r.e.Cursor().SelectedText() }

func TestMiddleDragBlockSelection(t *testing.T) {
	src := "abcdef\n\tx\nab\nabcdef"
	r := newRig(t, src)
	// From column 1 of line 0 to column 5 of line 3.
	r.dragWith(r.pointAt(1), r.pointAt(len("abcdef\n\tx\nab\n")+5), 0, pointer.ButtonTertiary)
	// The tab line covers columns 0-3, x is column 4; "ab" ends at 2.
	if got, want := r.selTexts(), []string{"bcde", "\tx", "b", "bcde"}; !slices.Equal(got, want) {
		t.Fatalf("middle drag: %q, want %q", got, want)
	}
	if p := r.e.Cursor().PrimaryIndex(); p != 3 {
		t.Fatalf("primary %d", p)
	}
	// Upwards and to the left: the heads are on the left.
	r.dragWith(r.pointAt(len("abcdef\n\tx\nab\n")+4), r.pointAt(2), 0, pointer.ButtonTertiary)
	sels := r.e.Cursor().Selections()
	if len(sels) != 4 || sels[0].Head != 2 || sels[0].Anchor != 4 || r.e.Cursor().PrimaryIndex() != 0 {
		t.Fatalf("upwards: %v primary %d", sels, r.e.Cursor().PrimaryIndex())
	}
	// Typing replaces every block line.
	r.focus()
	r.typ("#")
	r.expectText("ab#ef\n#x\nab#\nab#ef")
}

func TestMiddleClickPlacesCaretWithoutPaste(t *testing.T) {
	r := newRig(t, "abc\ndef")
	r.focus()
	r.clock += 1000
	p := r.pointAt(5)
	r.h.Router.Queue(
		r.pointer(pointer.Move, p, 0, 0),
		r.pointer(pointer.Press, p, 0, pointer.ButtonTertiary),
		r.pointer(pointer.Release, p, 0, 0),
	)
	r.frame()
	if sels := r.e.Cursor().Selections(); len(sels) != 1 || sels[0] != cursor.Caret(5) {
		t.Fatalf("middle click: %v", sels)
	}
	if r.h.Router.ClipboardRequested() {
		t.Fatal("middle click pasted")
	}
	r.expectText("abc\ndef")
}

func TestAltDragBlockAndAltShiftDragAdds(t *testing.T) {
	src := "abcdef\nabcdef\nabcdef\nabcdef"
	r := newRig(t, src)
	r.dragWith(r.pointAt(1), r.pointAt(7+3), key.ModAlt, pointer.ButtonPrimary)
	if got := r.selTexts(); !slices.Equal(got, []string{"bc", "bc"}) {
		t.Fatalf("alt drag: %q", got)
	}
	// Alt+Shift+drag adds a second block to the first.
	r.dragWith(r.pointAt(14+4), r.pointAt(21+6), key.ModAlt|key.ModShift, pointer.ButtonPrimary)
	if got := r.selTexts(); !slices.Equal(got, []string{"bc", "bc", "ef", "ef"}) {
		t.Fatalf("alt+shift drag: %q", got)
	}
	// A plain drag replaces everything again.
	r.dragWith(r.pointAt(0), r.pointAt(2), 0, pointer.ButtonPrimary)
	if got := r.selTexts(); !slices.Equal(got, []string{"ab"}) {
		t.Fatalf("plain drag: %q", got)
	}
}

// TestDoubleOptionClonesCarets: Option pressed twice and held, then the
// arrow keys clone the caret; a slow second press is a plain Option.
func TestDoubleOptionClonesCarets(t *testing.T) {
	r := newRig(t, "abcdef\nab\nabcdef\nabcdef")
	r.focus()
	r.e.SetCaret(4)
	r.modKey(key.NameAlt, true)
	r.modKey(key.NameAlt, false)
	r.modKey(key.NameAlt, true)
	r.press("Alt+Down")
	r.press("Alt+Down")
	r.press("Alt+Down")
	s := r.e.Buffer().Snapshot()
	var cols []int
	for _, sel := range r.e.Cursor().Selections() {
		cols = append(cols, s.OffsetToPos(sel.Head).Col)
	}
	if !slices.Equal(cols, []int{4, 2, 4, 4}) {
		t.Fatalf("carets at columns %v", cols)
	}
	r.modKey(key.NameAlt, false)
	// Option released: Alt+Up is Extend Selection again.
	r.press("Escape")
	r.e.SetCaret(1)
	r.press("Alt+Up")
	if got := r.selTexts(); len(got) != 1 || got[0] != "abcdef" {
		t.Fatalf("alt+up after release: %q", got)
	}
	// Too slow: no double press.
	r.e.SetCaret(1)
	r.modKey(key.NameAlt, true)
	r.modKey(key.NameAlt, false)
	for i := 0; i < 30; i++ { // 16 ms per frame
		r.frame()
	}
	r.modKey(key.NameAlt, true)
	r.press("Alt+Down")
	if n := len(r.e.Cursor().Selections()); n != 1 {
		t.Fatalf("slow double press cloned: %d carets", n)
	}
	r.modKey(key.NameAlt, false)
}

// TestDoubleCtrlClonesCaretsPC: the Windows/Linux variant with Ctrl.
func TestDoubleCtrlClonesCaretsPC(t *testing.T) {
	r := newRig(t, "abc\nabc\nabc")
	r.e.Keymap = keymap.PC()
	r.focus()
	r.e.SetCaret(len("abc\nabc\n") + 1)
	r.modKey(key.NameCtrl, true)
	r.modKey(key.NameCtrl, false)
	r.modKey(key.NameCtrl, true)
	r.press("Ctrl+Up")
	r.press("Ctrl+Up")
	r.modKey(key.NameCtrl, false)
	sels := r.e.Cursor().Selections()
	if len(sels) != 3 || sels[0].Head != 1 || !sels[0].Empty() || r.e.Cursor().PrimaryIndex() != 0 {
		t.Fatalf("carets %v primary %d", sels, r.e.Cursor().PrimaryIndex())
	}
}

func TestColumnSelectionModeKeyboard(t *testing.T) {
	r := newRig(t, "abcdef\nab\nabcdef\nabcdef")
	r.focus()
	r.e.SetCaret(1)
	r.press("Cmd+Shift+8")
	if !r.e.ColumnMode() {
		t.Fatal("column mode not on")
	}
	for _, k := range []string{"Shift+Down", "Shift+Down", "Shift+Right", "Shift+Right", "Shift+Right", "Shift+Left"} {
		r.press(k)
	}
	if got := r.selTexts(); !slices.Equal(got, []string{"bc", "b", "bc"}) {
		t.Fatalf("block: %q", got)
	}
	r.press("Shift+Up")
	if got := r.selTexts(); !slices.Equal(got, []string{"bc", "b"}) {
		t.Fatalf("block after up: %q", got)
	}
	// Typing replaces the block; Shift+Down continues from the carets.
	r.typ("X")
	r.expectText("aXdef\naX\nabcdef\nabcdef")
	r.press("Shift+Down")
	if n := len(r.e.Cursor().Selections()); n != 3 {
		t.Fatalf("after typing: %d carets", n)
	}
	// In column mode a plain mouse drag selects a block, too.
	r.dragWith(r.pointAt(0), r.pointAt(len("aXdef\n")+2), 0, pointer.ButtonPrimary)
	if got := r.selTexts(); !slices.Equal(got, []string{"aX", "aX"}) {
		t.Fatalf("drag in column mode: %q", got)
	}
	r.press("Cmd+Shift+8")
	r.e.SetCaret(1)
	r.press("Shift+Down")
	if got := r.selTexts(); len(got) != 1 {
		t.Fatalf("column mode off: %q", got)
	}
	r.e.SetColumnMode(true)
	if !r.e.ColumnMode() {
		t.Fatal("SetColumnMode")
	}
}

// TestBlockSelectionScreenshot renders a block selection over lines of
// different lengths (TIEGEL_SHOTS=<dir> writes block-selection.png).
func TestBlockSelectionScreenshot(t *testing.T) {
	r := newRig(t, "<?php\n$first  = 1;\n$x = 2;\n\n$second = 3;\n$third  = 4;\n")
	r.dragWith(r.pointAt(len("<?php\n")+1), r.pointAt(len("<?php\n$first  = 1;\n$x = 2;\n\n$second = 3;\n")+6), 0, pointer.ButtonTertiary)
	if n := len(r.e.Cursor().Selections()); n != 5 {
		t.Fatalf("%d selections", n)
	}
	th := tokens.Dark(nil)
	img, err := uitest.Render(th, image.Pt(420, 200), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		r.e.Focus(gtx)
		return r.e.Layout(gtx, th)
	})
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	if n := countNear(img, th.Editor.Cursor); n < 20 {
		t.Errorf("carets not drawn (%d px)", n)
	}
	if dir := os.Getenv("TIEGEL_SHOTS"); dir != "" {
		f, err := os.Create(filepath.Join(dir, "block-selection.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
}
