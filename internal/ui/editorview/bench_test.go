package editorview

import (
	"image"
	"strings"
	"testing"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/keymap"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// bigPHP returns a PHP document with n lines.
func bigPHP(n int) string {
	lines := strings.Split(samplePHP, "\n")
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(lines[i%len(lines)])
		b.WriteByte('\n')
	}
	return b.String()
}

var big100k = bigPHP(100_000)

// bigRig opens the 100k-line document with highlighting, folding and the
// caret in the middle, focused.
func bigRig(tb testing.TB) *rig {
	r := &rig{t: tb, h: uitest.NewHarness(image.Pt(1200, 900)), e: New(buffer.NewString(big100k))}
	tb.Cleanup(r.e.Close)
	r.e.Keymap = keymap.Mac()
	r.e.Highlighter = highlight.PHPDemo
	r.e.SetFoldRanges(highlight.BraceFolds(r.e.Buffer().Snapshot()))
	r.frame()
	r.focus()
	r.e.SetCaret(r.e.Buffer().LineStart(50_000) + 4)
	r.frame()
	return r
}

func (r *rig) typeKey(ch string) {
	st := r.h.Router.EditorState()
	r.h.Router.Queue(key.EditEvent{Range: st.Selection.Range, Text: ch})
	r.frame()
}

// BenchmarkLayout100k measures a steady-state frame (layout + ops + input
// routing, without GPU) of a 100k-line file.
func BenchmarkLayout100k(b *testing.B) {
	r := bigRig(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.frame()
	}
}

// BenchmarkScrollFrame100k scrolls one page per frame, so every frame
// shapes all lines anew.
func BenchmarkScrollFrame100k(b *testing.B) {
	r := bigRig(b)
	page := r.e.geo.size.Y
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.e.SetScrollOffset(image.Pt(0, (i%2000)*page))
		r.frame()
	}
}

// BenchmarkKeystroke100k measures key event → edit → next frame,
// alternating typing a character and Backspace so the line length stays
// constant.
func BenchmarkKeystroke100k(b *testing.B) {
	r := bigRig(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			r.typeKey("x")
		} else {
			r.press("Backspace")
		}
	}
}

// BenchmarkKeystrokeGPU100k includes rendering the frame on the headless
// GPU.
func BenchmarkKeystrokeGPU100k(b *testing.B) {
	r := bigRig(b)
	size := r.h.Max
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		b.Skipf("headless GPU unavailable: %v", err)
	}
	defer win.Release()
	var ops op.Ops
	frame := func() {
		ops.Reset()
		gtx := uitest.Context(&ops, &r.h.Router, size)
		r.widget(gtx, r.h.Theme)
		r.h.Router.Frame(&ops)
		if err := win.Frame(&ops); err != nil {
			b.Fatal(err)
		}
	}
	frame()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			st := r.h.Router.EditorState()
			r.h.Router.Queue(key.EditEvent{Range: st.Selection.Range, Text: "x"})
		} else {
			r.h.Router.Queue(key.Event{Name: key.NameDeleteBackward, State: key.Press})
		}
		frame()
	}
}

// BenchmarkOpen100k measures creating the buffer and editor and laying out
// the first frame.
func BenchmarkOpen100k(b *testing.B) {
	data := []byte(big100k)
	th := tokens.Dark(nil)
	for i := 0; i < b.N; i++ {
		e := New(buffer.New(data))
		e.Highlighter = highlight.PHPDemo
		var ops op.Ops
		var router input.Router
		gtx := uitest.Context(&ops, &router, image.Pt(1200, 900))
		e.Layout(gtx, th)
		e.Close()
	}
}

// TestPerformance100k checks the M3 acceptance: frame layout and
// keystroke-to-frame stay below 16 ms on a 100k-line file. Timing is
// skipped under the race detector and in -short mode.
func TestPerformance100k(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing test skipped (short mode or race detector)")
	}
	start := time.Now()
	r := bigRig(t)
	open := time.Since(start)
	// measure returns the worst and the average duration of n calls.
	measure := func(n int, fn func(i int)) (worst, avg time.Duration) {
		var total time.Duration
		for i := 0; i < n; i++ {
			t0 := time.Now()
			fn(i)
			d := time.Since(t0)
			total += d
			worst = max(worst, d)
		}
		return worst, total / time.Duration(n)
	}
	frame, _ := measure(50, func(int) { r.frame() })
	keystroke, _ := measure(50, func(i int) {
		if i%2 == 0 {
			r.typeKey("y")
		} else {
			r.press("Backspace")
		}
	})
	scroll, scrollAvg := measure(50, func(i int) {
		r.e.SetScrollOffset(image.Pt(0, i*r.e.geo.size.Y))
		r.frame()
	})
	t.Logf("open+first frame %v, worst frame %v, worst keystroke→frame %v, scroll frame worst %v avg %v",
		open, frame, keystroke, scroll, scrollAvg)
	// Scrolling a full page re-shapes every line; its worst case depends
	// on GC and machine load, so only the average is checked.
	const budget = 16 * time.Millisecond
	if frame > budget || keystroke > budget || scrollAvg > budget {
		t.Errorf("frame budget of %v exceeded", budget)
	}
	if open > 2*time.Second {
		t.Errorf("opening took %v", open)
	}
	if r.e.Buffer().LineCount() < 100_000 {
		t.Fatal("document too small")
	}
}
