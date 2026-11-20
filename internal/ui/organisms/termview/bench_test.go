package termview

import (
	"fmt"
	"image"
	"testing"

	"gioui.org/io/input"
	"gioui.org/op"

	"github.com/ape-dev-de/tiegel/internal/terminal"
	"github.com/ape-dev-de/tiegel/internal/terminal/vt"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// benchScreen is a 160×50 snapshot with colored spans whose first line
// has absolute index first (scrolling output moves it).
func benchScreen(first int) *terminal.Screen {
	sc := &terminal.Screen{Cols: 160, Rows: 50, History: first, Total: first + 50, Top: first, Running: true,
		Cursor: vt.Cursor{Row: 49, Visible: true}}
	for i := 0; i < 50; i++ {
		idx := first + i
		l := terminal.ScreenLine{Index: idx}
		col := 0
		for k := 0; k < 8; k++ {
			t := fmt.Sprintf("word%d-%d ", idx%97, k)
			l.Spans = append(l.Spans, terminal.Span{Col: col, Width: len(t), Text: t, FG: fmt.Sprintf("p%d", (idx+k)%16), Bold: k%3 == 0})
			l.Text += t
			col += len(t)
		}
		sc.Lines = append(sc.Lines, l)
	}
	return sc
}

func benchLayout(b *testing.B, next func(i int) *terminal.Screen) {
	th := tokens.Dark(tokens.NewBundledShaper())
	v := New()
	var ops op.Ops
	var r input.Router
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		ops.Reset()
		gtx := uitest.Context(&ops, &r, image.Pt(160*8+8, 50*16+8))
		v.Screen = next(i)
		v.Layout(gtx, th)
		i++
	}
}

// BenchmarkLayoutStatic: frames without new output (cursor blink,
// hover): every row comes from the cache.
func BenchmarkLayoutStatic(b *testing.B) {
	sc := benchScreen(0)
	benchLayout(b, func(int) *terminal.Screen { return sc })
}

// BenchmarkLayoutScrolling: every frame is a new snapshot scrolled by
// one line (like `cat` of a large file).
func BenchmarkLayoutScrolling(b *testing.B) {
	screens := make([]*terminal.Screen, 64)
	for i := range screens {
		screens[i] = benchScreen(i)
	}
	benchLayout(b, func(i int) *terminal.Screen { return screens[i%len(screens)] })
}

// BenchmarkLayoutNewContent: every row changes every frame (full-screen
// redraws of a TUI).
func BenchmarkLayoutNewContent(b *testing.B) {
	screens := make([]*terminal.Screen, 64)
	for i := range screens {
		screens[i] = benchScreen(i * 1000)
		screens[i].History, screens[i].Top = 0, 0
		for j := range screens[i].Lines {
			screens[i].Lines[j].Index = j
		}
	}
	benchLayout(b, func(i int) *terminal.Screen { return screens[i%len(screens)] })
}
