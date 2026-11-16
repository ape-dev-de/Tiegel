package molecules

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// dropdownMaxRows bounds the visible rows of the popup; more items scroll.
const dropdownMaxRows = 8

// Dropdown is an icon button that opens a popup list of text items (e.g.
// recent commit messages). Choosing an item closes the popup and is
// reported by Update; a click outside closes it as well.
//
// The popup is drawn with op.Defer on top of everything else, below the
// button (or above it with Up). It is a stateful component.
type Dropdown struct {
	Glyph       *atoms.Glyph
	Description string
	Items       []string
	Disabled    bool
	// Up opens the popup above the button.
	Up bool
	// Width of the popup (default 320dp).
	Width unit.Dp
	// Open reports whether the popup is shown; set it to open or close
	// programmatically.
	Open bool

	btn   atoms.IconButton
	rows  []ListRow
	list  ScrollList
	scrim int // address used as pointer tag of the outside area
	// pending is the item chosen during Layout, reported by the next Update
	// (+1, so the zero value means none).
	pending int
}

// Update processes input and returns the index of an item chosen since the
// last call (also when the click was processed by Layout).
func (d *Dropdown) Update(gtx layout.Context) (index int, chosen bool) {
	d.process(gtx)
	index, chosen = d.pending-1, d.pending > 0
	d.pending = 0
	return index, chosen
}

func (d *Dropdown) process(gtx layout.Context) {
	d.btn.Disabled = d.Disabled || len(d.Items) == 0
	if d.btn.Clicked(gtx) {
		d.Open = !d.Open
	}
	if !d.Open {
		return
	}
	for {
		e, ok := gtx.Event(pointer.Filter{Target: &d.scrim, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, ok := e.(pointer.Event); ok {
			d.Open = false
		}
	}
	// Presses on the popup background are swallowed so they do not reach
	// the outside area.
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &d.list, Kinds: pointer.Press}); !ok {
			break
		}
	}
	for i := range d.rows {
		if i >= len(d.Items) {
			break
		}
		if c, _ := d.rows[i].Update(gtx); c {
			d.pending = i + 1
			d.Open = false
		}
	}
}

// Layout draws the button and, when open, the popup. Call Update first to
// observe choices; a choice processed here is reported by the next Update.
func (d *Dropdown) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	d.process(gtx)
	d.btn.Glyph = d.Glyph
	if d.btn.Glyph == nil {
		d.btn.Glyph = atoms.GlyphChevronDown
	}
	d.btn.Description = d.Description
	d.btn.Disabled = d.Disabled || len(d.Items) == 0
	if d.btn.Disabled {
		d.Open = false
	}
	bd := d.btn.Layout(gtx, th)
	if !d.Open {
		return bd
	}
	for len(d.rows) < len(d.Items) {
		d.rows = append(d.rows, ListRow{})
	}

	w := d.Width
	if w == 0 {
		w = 320
	}
	pw := gtx.Dp(w)
	rowH := gtx.Dp(th.Size.RowH)
	pad := gtx.Dp(th.Space.XS)
	ph := min(len(d.Items), dropdownMaxRows)*rowH + 2*pad

	pg := gtx
	pg.Constraints = layout.Exact(image.Pt(pw, ph))
	m := op.Record(gtx.Ops)
	// Outside area: a huge rectangle below the popup that closes it.
	const far = 1 << 16
	sc := clip.Rect{Min: image.Pt(-far, -far), Max: image.Pt(far, far)}.Push(gtx.Ops)
	event.Op(gtx.Ops, &d.scrim)
	sc.Pop()

	off := image.Pt(0, bd.Size.Y+pad)
	if d.Up {
		off.Y = -ph - pad
	}
	t := op.Offset(off).Push(gtx.Ops)
	r := image.Rectangle{Max: image.Pt(pw, ph)}
	rad := gtx.Dp(th.Radius.M)
	draw.RRect(gtx.Ops, r.Add(image.Pt(0, gtx.Dp(2))), rad, tokens.WithAlpha(th.Color.Background, 0x90))
	draw.RRect(gtx.Ops, r, rad, th.Color.Surface)
	draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), th.Color.BorderStrong)
	// Block clicks from reaching the outside area.
	pc := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, &d.list)
	pc.Pop()
	lt := op.Offset(image.Pt(0, pad)).Push(gtx.Ops)
	lg := pg
	lg.Constraints = layout.Exact(image.Pt(pw, ph-2*pad))
	d.list.Layout(lg, th, len(d.Items), func(gtx layout.Context, i int) layout.Dimensions {
		return d.rows[i].Layout(gtx, th, atoms.Text(firstLine(d.Items[i])).SingleLine().Fn(th))
	})
	lt.Pop()
	t.Pop()
	op.Defer(gtx.Ops, m.Stop())
	return bd
}

// firstLine returns s up to the first line break.
func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return s[:i]
		}
	}
	return s
}
