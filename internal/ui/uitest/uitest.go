// Package uitest provides helpers for testing Tiegel UI components without a
// window: building layout contexts, smoke-testing layouts and rendering
// frames headlessly on the GPU when available.
package uitest

import (
	"fmt"
	"image"
	"image/color"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Widget is the common Layout signature of Tiegel components, bound to a
// theme by the caller.
type Widget func(gtx layout.Context, th *tokens.Theme) layout.Dimensions

// Context returns a layout context of the given maximum size (loose
// constraints) with an enabled input source and a 1:1 pixel metric.
func Context(ops *op.Ops, r *input.Router, max image.Point) layout.Context {
	return layout.Context{
		Ops:         ops,
		Now:         time.Unix(1_700_000_000, 0),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Constraints{Max: max},
		Source:      r.Source(),
	}
}

// Layout runs w once in a fresh context and returns its dimensions. A panic
// inside w is returned as an error.
func Layout(th *tokens.Theme, max image.Point, w Widget) (dims layout.Dimensions, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("layout panicked: %v", p)
		}
	}()
	var ops op.Ops
	var r input.Router
	gtx := Context(&ops, &r, max)
	dims = w(gtx, th)
	r.Frame(&ops)
	// A second frame exercises the event/update path with a live router.
	ops.Reset()
	gtx = Context(&ops, &r, max)
	dims = w(gtx, th)
	r.Frame(&ops)
	return dims, nil
}

// Smoke lays out w in both themes (with the deterministic bundled-fonts
// shaper) within max and fails t if it panics,
// returns an empty size, or exceeds the constraints. It returns the
// dimensions from the dark theme.
func Smoke(t testing.TB, name string, max image.Point, w Widget) layout.Dimensions {
	t.Helper()
	sh := tokens.NewBundledShaper()
	var first layout.Dimensions
	for i, th := range []*tokens.Theme{tokens.Dark(sh), tokens.Light(sh)} {
		d, err := Layout(th, max, w)
		if err != nil {
			t.Fatalf("%s (%s): %v", name, th.Mode, err)
		}
		if d.Size.X <= 0 || d.Size.Y <= 0 {
			t.Errorf("%s (%s): empty size %v", name, th.Mode, d.Size)
		}
		if d.Size.X > max.X || d.Size.Y > max.Y {
			t.Errorf("%s (%s): size %v exceeds constraints %v", name, th.Mode, d.Size, max)
		}
		if i == 0 {
			first = d
		}
	}
	return first
}

// Render draws w on the theme background into an image of size using the
// headless GPU renderer. It returns an error if no GPU context is available
// (e.g. on CI without a display); callers should t.Skip in that case.
func Render(th *tokens.Theme, size image.Point, w Widget) (*image.RGBA, error) {
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		return nil, fmt.Errorf("headless window: %w", err)
	}
	defer win.Release()
	var ops op.Ops
	var r input.Router
	gtx := Context(&ops, &r, size)
	paint.FillShape(&ops, th.Color.Background, clip.Rect{Max: size}.Op())
	w(gtx, th)
	if err := win.Frame(&ops); err != nil {
		return nil, fmt.Errorf("headless frame: %w", err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		return nil, fmt.Errorf("headless screenshot: %w", err)
	}
	return img, nil
}

// CountDiffering returns how many pixels of img differ from c.
func CountDiffering(img *image.RGBA, c color.NRGBA) int {
	want := color.RGBAModel.Convert(c).(color.RGBA)
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y) != want {
				n++
			}
		}
	}
	return n
}

// Harness drives a widget over several frames with a persistent input
// router, so tests can inject pointer events between frames.
type Harness struct {
	Theme  *tokens.Theme
	Max    image.Point
	Router input.Router
	ops    op.Ops
	frames int
	clock  time.Duration
}

// SkipWithoutSystemMono skips t unless an installed monospace system font
// exists (tokens.MonospaceFamilies lists more than the bundled Go Mono).
// Tests of the system shaper (tokens.NewShaper) use it, because CI
// machines may have no fonts installed.
func SkipWithoutSystemMono(t testing.TB) {
	t.Helper()
	if len(tokens.MonospaceFamilies()) < 2 {
		t.Skip("no monospace system font installed")
	}
}

// NewHarness returns a harness with the dark theme and the deterministic
// bundled-fonts shaper.
func NewHarness(max image.Point) *Harness {
	return &Harness{Theme: tokens.Dark(nil), Max: max}
}

// Frame lays out w once and hands the frame to the router.
func (h *Harness) Frame(w Widget) layout.Dimensions {
	h.ops.Reset()
	gtx := Context(&h.ops, &h.Router, h.Max)
	gtx.Now = gtx.Now.Add(time.Duration(h.frames) * 16 * time.Millisecond)
	h.frames++
	d := w(gtx, h.Theme)
	h.Router.Frame(&h.ops)
	return d
}

// Click queues a primary mouse press and release at p. The events are
// delivered during the next Frame. Consecutive clicks are one second apart,
// so they never form a double click.
func (h *Harness) Click(p image.Point) {
	h.clock += time.Second
	h.queueClick(p)
}

// DoubleClick queues two clicks at p in quick succession.
func (h *Harness) DoubleClick(p image.Point) {
	h.clock += time.Second
	h.queueClick(p)
	h.clock += 50 * time.Millisecond
	h.queueClick(p)
}

// Drag queues a primary-button drag from one point to another (press, a few
// drag moves, release), delivered during the next Frame.
func (h *Harness) Drag(from, to image.Point) {
	h.clock += time.Second
	pos := func(p image.Point) f32.Point { return f32.Pt(float32(p.X), float32(p.Y)) }
	evs := []event.Event{
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos(from), Time: h.clock},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos(from), Time: h.clock},
	}
	const steps = 4
	for i := 1; i <= steps; i++ {
		p := from.Add(to.Sub(from).Mul(i).Div(steps))
		h.clock += 10 * time.Millisecond
		evs = append(evs, pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos(p), Time: h.clock})
	}
	evs = append(evs, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos(to), Time: h.clock})
	h.Router.Queue(evs...)
}

func (h *Harness) queueClick(p image.Point) {
	h.queueClickWith(p, pointer.ButtonPrimary, 0)
}

// ClickWith queues a press and release of buttons at p with modifiers
// held (e.g. pointer.ButtonSecondary for a context click, key.ModShift
// for Shift+click).
func (h *Harness) ClickWith(p image.Point, buttons pointer.Buttons, mods key.Modifiers) {
	h.clock += time.Second
	h.queueClickWith(p, buttons, mods)
}

func (h *Harness) queueClickWith(p image.Point, buttons pointer.Buttons, mods key.Modifiers) {
	pos := f32.Pt(float32(p.X), float32(p.Y))
	h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos, Time: h.clock, Modifiers: mods},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: buttons, Position: pos, Time: h.clock, Modifiers: mods},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos, Time: h.clock, Modifiers: mods},
	)
}
