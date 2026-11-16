package diff_test

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight"
	"github.com/ape-dev-de/tiegel/internal/editor/highlight/tshighlight"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff/diffsample"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var area = image.Pt(1200, 640)

// frame lays out w once in h and returns the events of Update.
func frame(h *uitest.Harness, w interface {
	Update(layout.Context) []diff.Event
	Layout(layout.Context, *tokens.Theme) layout.Dimensions
}) []diff.Event {
	var ev []diff.Event
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		ev = w.Update(gtx)
		d := w.Layout(gtx, th)
		ev = append(ev, w.Update(gtx)...)
		return d
	})
	return ev
}

func sampleWindow(t testing.TB, editable bool) (*diff.DiffWindow, *buffer.Buffer) {
	t.Helper()
	w := diff.NewDiffWindow()
	w.NewHighlighter = func(b *buffer.Buffer, path string) highlight.Highlighter { return highlight.PHPDemo }
	var buf *buffer.Buffer
	if editable {
		buf = buffer.NewString(diffsample.New)
	}
	w.SetContent(diffsample.Content(), buf)
	t.Cleanup(w.Close)
	return w, buf
}

func TestDiffWindowSmoke(t *testing.T) {
	empty := diff.NewDiffWindow()
	uitest.Smoke(t, "empty", area, empty.Layout)
	w, _ := sampleWindow(t, true)
	uitest.Smoke(t, "side-by-side", area, w.Layout)
	uitest.Smoke(t, "narrow", image.Pt(320, 200), w.Layout)
	w.SetUnified(true)
	uitest.Smoke(t, "unified", area, w.Layout)
	bin := diff.NewDiffWindow()
	bin.SetContent(diff.Content{Left: diff.Side{Label: "HEAD", Path: "a.png", Binary: true}, Right: diff.Side{Label: "Working tree", Path: "a.png", Binary: true}}, nil)
	uitest.Smoke(t, "binary", area, bin.Layout)
	if !strings.Contains(w.Title(), diffsample.Path) || !strings.Contains(w.Title(), "HEAD (4f2a9c1e) ↔ Working tree") {
		t.Fatalf("title %q", w.Title())
	}
}

// TestDiffWindowScreenshots renders the window in both themes and both
// modes. With TIEGEL_SHOTS=<dir> the PNGs are written for visual review.
func TestDiffWindowScreenshots(t *testing.T) {
	dir := os.Getenv("TIEGEL_SHOTS")
	for _, th := range []*tokens.Theme{tokens.Dark(nil), tokens.Light(nil)} {
		for _, unified := range []bool{false, true} {
			w := diff.NewDiffWindow()
			w.NewHighlighter = func(b *buffer.Buffer, path string) highlight.Highlighter {
				return tshighlight.New(b, tshighlight.Options{Path: path})
			}
			w.SetContent(diffsample.Content(), buffer.NewString(diffsample.New))
			w.SetUnified(unified)
			w.Next()
			w.Next()
			var img *image.RGBA
			var err error
			// A few frames: highlighting is asynchronous, navigation needs
			// the line height of a first frame.
			for i := 0; i < 4; i++ {
				img, err = uitest.Render(th, area, w.Layout)
				if err != nil {
					w.Close()
					t.Skipf("headless rendering unavailable: %v", err)
				}
				time.Sleep(30 * time.Millisecond)
			}
			for _, k := range []editorview.DecorationKind{editorview.DecorationAdded, editorview.DecorationModified, editorview.DecorationDeleted} {
				if unified && k == editorview.DecorationModified {
					continue
				}
				line, _, _ := editorview.DecorationColors(th, k)
				if n := countNear(img, line); n < 400 {
					t.Errorf("%s unified=%v: kind %d barely visible (%d px)", th.Mode, unified, k, n)
				}
			}
			if dir != "" {
				name := "diffwindow-" + th.Mode.String()
				if unified {
					name += "-unified"
				}
				writePNG(t, filepath.Join(dir, name+".png"), img)
			}
			w.Close()
		}
	}
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func countNear(img *image.RGBA, c interface{ RGBA() (r, g, b, a uint32) }) int {
	cr, cg, cb, _ := c.RGBA()
	n := 0
	b := img.Bounds()
	near := func(a, b uint32) bool { d := int(a>>8) - int(b>>8); return d > -6 && d < 6 }
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := img.RGBAAt(x, y)
			if near(uint32(p.R)<<8, cr) && near(uint32(p.G)<<8, cg) && near(uint32(p.B)<<8, cb) {
				n++
			}
		}
	}
	return n
}

func TestDiffWindowNavigationKeys(t *testing.T) {
	w, _ := sampleWindow(t, false)
	h := uitest.NewHarness(area)
	frame(h, w)
	n := w.Count()
	if n < 4 || w.Current() != -1 {
		t.Fatalf("count %d current %d", n, w.Current())
	}
	h.Router.Queue(key.Event{Name: key.NameF7, State: key.Press})
	frame(h, w)
	h.Router.Queue(key.Event{Name: key.NameF7, State: key.Press})
	frame(h, w)
	if w.Current() != 1 {
		t.Fatalf("F7 twice: %d", w.Current())
	}
	h.Router.Queue(key.Event{Name: key.NameF7, Modifiers: key.ModShift, State: key.Press})
	frame(h, w)
	if w.Current() != 0 {
		t.Fatalf("Shift+F7: %d", w.Current())
	}
	if w.Prev() || w.Current() != 0 {
		t.Fatal("prev before first")
	}
	for w.Next() {
	}
	if w.Current() != n-1 || w.Next() {
		t.Fatal("next past last")
	}
	// The last block is scrolled into view on the right side.
	frame(h, w)
	_, right := w.Editors()
	b := w.Blocks()[n-1]
	if y := right.LineTop(b.RightStart); y < 0 || y > right.ViewportHeight() {
		t.Fatalf("block not visible: y=%d", y)
	}
	if len(diff.Shortcuts()) != 2 {
		t.Fatal("shortcuts")
	}
}

func TestDiffWindowScrollSync(t *testing.T) {
	left, right := diffsample.Large(2000, 50)
	w := diff.NewDiffWindow()
	w.SetContent(diff.Content{Left: diff.Side{Label: "HEAD", Path: "big.php", Text: left},
		Right: diff.Side{Label: "Working tree", Path: "big.php", Text: right}, Blocks: diff.Compute(left, right, false)}, nil)
	defer w.Close()
	h := uitest.NewHarness(area)
	frame(h, w)
	le, re := w.Editors()
	lh := le.LineHeight()
	// Scroll the left side to line 1000: the right side follows to the
	// corresponding line (the centers of both viewports are aligned).
	le.SetScrollOffset(image.Pt(0, 1000*lh))
	frame(h, w)
	frame(h, w)
	mid := func(e *editorview.Editor) float64 {
		return (float64(e.ScrollOffset().Y) + float64(e.ViewportHeight())/2) / float64(lh)
	}
	gb := gitcli.CompareTexts([]byte(left), []byte(right), gitcli.TextDiffOptions{})
	if le.ScrollOffset().Y != 1000*lh {
		t.Fatalf("left moved: %d", le.ScrollOffset().Y)
	}
	if want, got := gitcli.MapLine(gb, mid(le), true), mid(re); math.Abs(want-got) > 1 {
		t.Fatalf("right center at line %.1f, want %.1f", got, want)
	}
	lm, rm := int(mid(le)), int(mid(re))
	if string(le.Buffer().Snapshot().Line(lm)) != string(re.Buffer().Snapshot().Line(rm)) {
		t.Fatalf("center lines differ: %q / %q", le.Buffer().Snapshot().Line(lm), re.Buffer().Snapshot().Line(rm))
	}
	// Scrolling the right side drives the left one.
	re.SetScrollOffset(image.Pt(0, 300*lh))
	frame(h, w)
	frame(h, w)
	if re.ScrollOffset().Y != 300*lh {
		t.Fatalf("right moved: %d", re.ScrollOffset().Y)
	}
	if want, got := gitcli.MapLine(gb, mid(re), false), mid(le); math.Abs(want-got) > 1 {
		t.Fatalf("left center at line %.1f, want %.1f", got, want)
	}
	// Stable: further frames do not move anything.
	l0, r0 := le.ScrollOffset(), re.ScrollOffset()
	frame(h, w)
	frame(h, w)
	if le.ScrollOffset() != l0 || re.ScrollOffset() != r0 {
		t.Fatal("offsets drift")
	}
	// Horizontal offsets are shared.
	re.SetScrollOffset(image.Pt(40, re.ScrollOffset().Y))
	frame(h, w)
	frame(h, w)
	if le.ScrollOffset().X != re.ScrollOffset().X {
		t.Fatalf("x not synced %v %v", le.ScrollOffset(), re.ScrollOffset())
	}
}

func TestDiffWindowRevertEvent(t *testing.T) {
	w, buf := sampleWindow(t, true)
	if !w.Editable() {
		t.Fatal("not editable")
	}
	h := uitest.NewHarness(area)
	frame(h, w)
	frame(h, w)
	// Find the revert arrow of the first block: it sits in the strip next
	// to the first left line of the block.
	le, _ := w.Editors()
	b0 := w.Blocks()[0]
	header := headerHeight(t, w, h)
	lw, _, _ := (area.X-40)/2, 40, 0
	y := header + le.LineTop(b0.LeftStart)
	if b0.LeftEnd == b0.LeftStart {
		y -= 0
	} else {
		y += le.LineHeight() / 2
	}
	h.Click(image.Pt(lw+2+9, y))
	ev := frame(h, w)
	var rev *diff.RevertRequested
	for _, e := range ev {
		if r, ok := e.(diff.RevertRequested); ok {
			rev = &r
		}
	}
	if rev == nil || rev.Block != 0 {
		t.Fatalf("no revert event: %v", ev)
	}
	before := w.Count()
	if err := w.ApplyRevert(*rev); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "use App\\Billing\\Tax\\TaxRate;\n\nfinal class") {
		t.Fatalf("block not reverted:\n%s", buf.String()[:200])
	}
	frame(h, w)
	if w.Count() != before-1 {
		t.Fatalf("blocks not recomputed: %d → %d", before, w.Count())
	}
	// Reverting everything leaves no differences.
	for w.Count() > 0 {
		b := w.Blocks()[0]
		lt, _ := w.Editors()
		s := lt.Buffer().Snapshot()
		start, end := s.LineStart(b.LeftStart), s.Len()
		if b.LeftEnd < s.LineCount() {
			end = s.LineStart(b.LeftEnd)
		}
		if err := w.ApplyRevert(diff.RevertRequested{Change: b, Text: string(s.Slice(start, end))}); err != nil {
			t.Fatal(err)
		}
		frame(h, w)
	}
	if buf.String() != diffsample.Old {
		t.Fatal("not equal after reverting all blocks")
	}
	// Read-only windows refuse.
	ro, _ := sampleWindow(t, false)
	if ro.ApplyRevert(diff.RevertRequested{}) == nil {
		t.Fatal("read-only revert")
	}
}

// headerHeight finds the y where the editors start: the left editor's
// line 0 is at LineTop 0 relative to its origin, so probe by clicking is
// not needed – toolbar and header heights are measured from a layout.
func headerHeight(t *testing.T, w *diff.DiffWindow, h *uitest.Harness) int {
	t.Helper()
	le, _ := w.Editors()
	// The editors fill the area below toolbar and column headers.
	return area.Y - le.ViewportHeight()
}

func TestDiffWindowEditsRecompute(t *testing.T) {
	w, buf := sampleWindow(t, true)
	h := uitest.NewHarness(area)
	frame(h, w)
	n := w.Count()
	buf.Insert(0, []byte("// header\n"))
	ev := frame(h, w)
	if len(ev) == 0 {
		t.Fatal("no RightEdited event")
	}
	if _, ok := ev[0].(diff.RightEdited); !ok {
		t.Fatalf("event %T", ev[0])
	}
	// Recomputed after the pause (harness frames are 16ms apart).
	for i := 0; i < 15; i++ {
		frame(h, w)
	}
	if w.Count() != n+1 {
		t.Fatalf("blocks %d, want %d", w.Count(), n+1)
	}
	// Refresh with the same texts keeps the editors (and scroll).
	le, re := w.Editors()
	c := w.Content()
	w.SetContent(c, buf)
	le2, re2 := w.Editors()
	if le != le2 || re != re2 {
		t.Fatal("editors recreated on refresh")
	}
}

func TestDiffWindowToolbar(t *testing.T) {
	w, _ := sampleWindow(t, true)
	h := uitest.NewHarness(area)
	frame(h, w)
	// The toolbar row: buttons at the right end; find them by probing.
	var got []diff.Event
	for x := area.X - 8; x > area.X/2 && len(got) < 3; x -= 6 {
		h.Click(image.Pt(x, 14))
		for _, e := range frame(h, w) {
			switch e.(type) {
			case diff.IgnoreWhitespaceChanged, diff.ViewModeChanged, diff.OpenFileRequested:
				dup := false
				for _, g := range got {
					if fmt.Sprintf("%T", g) == fmt.Sprintf("%T", e) {
						dup = true
					}
				}
				if !dup {
					got = append(got, e)
				}
			}
		}
	}
	var ws, mode, open bool
	for _, e := range got {
		switch e := e.(type) {
		case diff.IgnoreWhitespaceChanged:
			ws = e.Ignore
		case diff.ViewModeChanged:
			mode = mode || e.Unified
		case diff.OpenFileRequested:
			open = e.Path == diffsample.Path
		}
	}
	if !ws || !mode || !open {
		t.Fatalf("toolbar events %v", got)
	}
	if !w.IgnoreWhitespace() || !w.Unified() {
		t.Fatal("toggle state")
	}
	w.SetIgnoreWhitespace(false)
	w.SetUnified(false)
	frame(h, w)
}
