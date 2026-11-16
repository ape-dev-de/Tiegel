package editorview

import (
	"image"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

// testColumn is a GutterColumn that records the lines it drew.
type testColumn struct {
	w     int
	lines []int
}

func (c *testColumn) GutterWidth(gtx layout.Context, th *tokens.Theme) int { return c.w }

func (c *testColumn) LayoutGutterLine(gtx layout.Context, th *tokens.Theme, line int, active bool) {
	if gtx.Constraints.Min.X != c.w || gtx.Constraints.Max.Y <= 0 {
		panic("cell constraints")
	}
	c.lines = append(c.lines, line)
}

func numbered(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestLineDecorationsFollowEdits(t *testing.T) {
	r := newRig(t, numbered(20))
	in := []LineDecoration{
		{StartLine: 8, EndLine: 10, Kind: DecorationModified, Inline: []InlineRange{{Line: 9, Start: 0, End: 4}}},
		{StartLine: 2, EndLine: 3, Kind: DecorationAdded},
		{StartLine: 5, EndLine: 5, Kind: DecorationDeleted},
	}
	r.e.SetLineDecorations(in)
	got := r.e.LineDecorations()
	if got[0].StartLine != 2 || got[1].StartLine != 5 || got[2].StartLine != 8 {
		t.Fatalf("not sorted: %+v", got)
	}
	if d, ok := r.e.decorationAt(9); !ok || d.Kind != DecorationModified {
		t.Fatal("decorationAt 9")
	}
	for _, l := range []int{0, 3, 5, 7, 10, 19} {
		if _, ok := r.e.decorationAt(l); ok {
			t.Errorf("line %d decorated", l)
		}
	}
	// Insert two lines at the top: everything below moves.
	r.e.Buffer().Insert(0, []byte("a\nb\n"))
	r.frame()
	got = r.e.LineDecorations()
	if got[0].StartLine != 4 || got[2].StartLine != 10 || got[2].EndLine != 12 || got[2].Inline[0].Line != 11 {
		t.Fatalf("after insert %+v", got)
	}
	if in[0].Inline[0].Line != 9 {
		t.Fatal("caller's slice modified")
	}
	// Delete lines 8..10 (inside the modified block).
	s := r.e.Buffer().Snapshot()
	r.e.Buffer().Delete(s.LineStart(9), s.LineStart(11)-s.LineStart(9))
	got = r.e.LineDecorations()
	if got[2].StartLine != 9 || got[2].EndLine != 10 {
		t.Fatalf("after delete %+v", got)
	}
	r.frame()
}

func TestLineTopAndViewport(t *testing.T) {
	r := newRig(t, numbered(200))
	lh := r.e.LineHeight()
	if r.e.LineTop(0) != 0 || r.e.LineTop(10) != 10*lh || r.e.ViewportHeight() != 600 {
		t.Fatalf("line top %d %d", r.e.LineTop(10), r.e.ViewportHeight())
	}
	if r.e.LineTop(-3) != 0 || r.e.LineTop(201) != 201*lh || r.e.LineTop(205) != 205*lh {
		t.Fatalf("out of range %d", r.e.LineTop(205))
	}
	r.e.SetScrollOffset(image.Pt(0, 5*lh))
	r.frame()
	if r.e.LineTop(10) != 5*lh || r.e.MaxScrollY() <= 0 {
		t.Fatalf("scrolled %d", r.e.LineTop(10))
	}
	if r.e.FirstVisibleLine() != 5 {
		t.Fatal("first visible")
	}
}

func TestGutterColumnAndHiddenNumbers(t *testing.T) {
	r := newRig(t, numbered(30))
	g0 := r.e.geo
	col := &testColumn{w: 120}
	r.e.SetGutterColumn(col)
	r.e.HideLineNumbers = true
	r.frame()
	g := r.e.geo
	if g.annotW != 120 || g.iconX != g0.iconX+120 {
		t.Fatalf("geometry %+v", g)
	}
	if g.gutterW >= g0.gutterW+120 {
		t.Fatal("numbers not hidden")
	}
	if len(col.lines) == 0 || col.lines[0] != 0 {
		t.Fatalf("column lines %v", col.lines)
	}
	// Clicks into the column report GutterAnnotation.
	lh := r.e.LineHeight()
	r.click(f32.Pt(30, float32(3*lh+lh/2)), 0)
	var found bool
	for _, ev := range r.takeEvents() {
		if gc, ok := ev.(GutterClickEvent); ok && gc.Area == GutterAnnotation && gc.Line == 3 {
			found = true
		}
	}
	if !found {
		t.Fatal("no annotation click")
	}
	// Right click as well.
	r.h.Router.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary, Position: f32.Pt(30, float32(lh/2))},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(30, float32(lh/2))},
	)
	r.frame()
	found = false
	for _, ev := range r.takeEvents() {
		if gc, ok := ev.(GutterClickEvent); ok && gc.Area == GutterAnnotation && gc.Secondary {
			found = true
		}
	}
	if !found {
		t.Fatal("no secondary annotation click")
	}
	r.e.SetGutterColumn(nil)
	r.frame()
	if r.e.geo.annotW != 0 {
		t.Fatal("column not removed")
	}
}

func TestDecorationsRender(t *testing.T) {
	e := New(buffer.NewString(numbered(40)))
	defer e.Close()
	e.SetLineDecorations([]LineDecoration{
		{StartLine: 1, EndLine: 3, Kind: DecorationModified, Inline: []InlineRange{{Line: 2, Start: 0, End: 4}, {Line: 2, Start: 50, End: 60}}},
		{StartLine: 5, EndLine: 6, Kind: DecorationAdded},
		{StartLine: 8, EndLine: 9, Kind: DecorationDeleted},
		{StartLine: 12, EndLine: 12, Kind: DecorationDeleted},
		{StartLine: 300, EndLine: 300, Kind: DecorationAdded},
	})
	e.SetGutterColumn(&testColumn{w: 40})
	uitest.Smoke(t, "decorated", image.Pt(600, 400), e.Layout)
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		img, err := uitest.Render(th, image.Pt(600, 400), e.Layout)
		if err != nil {
			t.Skipf("headless rendering unavailable: %v", err)
		}
		for _, k := range []DecorationKind{DecorationAdded, DecorationModified, DecorationDeleted} {
			line, inline, sep := DecorationColors(th, k)
			if n := countNear(img, line); n < 500 {
				t.Errorf("%s kind %d: line tint barely visible (%d px)", th.Mode, k, n)
			}
			_ = inline
			_ = sep
		}
		_, inline, _ := DecorationColors(th, DecorationModified)
		if n := countNear(img, inline); n < 50 {
			t.Errorf("%s: inline tint barely visible (%d px)", th.Mode, n)
		}
		_, _, sep := DecorationColors(th, DecorationDeleted)
		if n := countNear(img, sep); n < 300 {
			t.Errorf("%s: separator barely visible (%d px)", th.Mode, n)
		}
	}
}
