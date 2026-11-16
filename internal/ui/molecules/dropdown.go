package molecules

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/internal/draw"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// dropdownMaxRows bounds the visible rows of the popup; more items scroll.
const dropdownMaxRows = 8

// DropdownOption is one entry of a select-style Dropdown (Options).
type DropdownOption struct {
	Label string
	// Detail is shown muted after the label in the popup.
	Detail string
	Glyph  *atoms.Glyph
	// Badge is an optional short text at the right end (e.g. a version).
	Badge     string
	BadgeKind atoms.BadgeKind
}

// Dropdown opens a popup list and reports the chosen entry. It has two
// forms:
//
//   - Menu button (Items): an icon button opening a list of text items
//     (e.g. recent commit messages).
//   - Select field (Options non-nil): a full-width field showing the
//     Selected option (or Placeholder); the popup lists rich options with
//     glyph, detail and badge, and choosing one updates Selected. Use
//     NewSelect for an initially empty selection.
//
// Choosing an entry closes the popup and is reported by Update; a click
// outside closes it as well. The popup is drawn with op.Defer on top of
// everything else, below the trigger (or above it with Up). It is a
// stateful component.
type Dropdown struct {
	Glyph       *atoms.Glyph
	Description string
	Items       []string
	Disabled    bool
	// Up opens the popup above the trigger.
	Up bool
	// Width of the popup (default 320dp for the menu button, the field
	// width for the select field).
	Width unit.Dp
	// Open reports whether the popup is shown; set it to open or close
	// programmatically.
	Open bool

	// Options switches to the select field form.
	Options []DropdownOption
	// Selected is the selected option index (-1 for none).
	Selected int
	// Placeholder is shown in the select field when nothing is selected.
	Placeholder string

	btn   atoms.IconButton
	field widget.Clickable
	rows  []ListRow
	list  ScrollList
	scrim int // address used as pointer tag of the outside area
	// pending is the item chosen during Layout, reported by the next Update
	// (+1, so the zero value means none).
	pending int
}

// NewSelect returns a select-style dropdown without selection.
func NewSelect(placeholder string, opts ...DropdownOption) *Dropdown {
	return &Dropdown{Options: opts, Selected: -1, Placeholder: placeholder}
}

// isSelect reports the select field form.
func (d *Dropdown) isSelect() bool { return d.Options != nil }

func (d *Dropdown) count() int {
	if d.isSelect() {
		return len(d.Options)
	}
	return len(d.Items)
}

// Update processes input and returns the index of an entry chosen since
// the last call (also when the click was processed by Layout).
func (d *Dropdown) Update(gtx layout.Context) (index int, chosen bool) {
	d.process(gtx)
	index, chosen = d.pending-1, d.pending > 0
	d.pending = 0
	return index, chosen
}

func (d *Dropdown) process(gtx layout.Context) {
	disabled := d.Disabled || d.count() == 0
	d.btn.Disabled = disabled
	if d.btn.Clicked(gtx) {
		d.Open = !d.Open
	}
	for d.field.Clicked(gtx) {
		if !disabled {
			d.Open = !d.Open
		}
	}
	if disabled {
		d.Open = false
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
		if i >= d.count() {
			break
		}
		if c, _ := d.rows[i].Update(gtx); c {
			d.pending = i + 1
			d.Open = false
			if d.isSelect() {
				d.Selected = i
			}
		}
	}
}

// Layout draws the trigger and, when open, the popup. Call Update first
// to observe choices; a choice processed here is reported by the next
// Update.
func (d *Dropdown) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	d.process(gtx)
	var bd layout.Dimensions
	pw := gtx.Dp(320)
	if d.isSelect() {
		bd = d.layoutField(gtx, th)
		pw = bd.Size.X
	} else {
		d.btn.Glyph = d.Glyph
		if d.btn.Glyph == nil {
			d.btn.Glyph = atoms.GlyphChevronDown
		}
		d.btn.Description = d.Description
		d.btn.Disabled = d.Disabled || len(d.Items) == 0
		bd = d.btn.Layout(gtx, th)
	}
	if d.Width > 0 {
		pw = gtx.Dp(d.Width)
	}
	if !d.Open {
		return bd
	}
	n := d.count()
	for len(d.rows) < n {
		d.rows = append(d.rows, ListRow{})
	}

	rowH := gtx.Dp(th.Size.RowH)
	pad := gtx.Dp(th.Space.XS)
	ph := min(n, dropdownMaxRows)*rowH + 2*pad

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
	d.list.Layout(lg, th, n, func(gtx layout.Context, i int) layout.Dimensions {
		if d.isSelect() {
			d.rows[i].Selected = i == d.Selected
			return d.rows[i].Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
				return d.layoutOption(gtx, th, d.Options[i], true)
			})
		}
		return d.rows[i].Layout(gtx, th, atoms.Text(firstLine(d.Items[i])).SingleLine().Fn(th))
	})
	lt.Pop()
	t.Pop()
	op.Defer(gtx.Ops, m.Stop())
	return bd
}

// fallbackSelectWidth is the field width when the width is unbounded.
const fallbackSelectWidth = unit.Dp(240)

// layoutField draws the select field across the available width.
func (d *Dropdown) layoutField(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	disabled := d.Disabled || len(d.Options) == 0
	if disabled {
		gtx = gtx.Disabled()
	}
	w := draw.Width(gtx, gtx.Dp(fallbackSelectWidth))
	h := gtx.Dp(th.Size.ControlH)
	size := image.Pt(w, h)
	return d.field.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		c := th.Color
		r := image.Rectangle{Max: size}
		rad := gtx.Dp(th.Radius.M)
		bg, border := c.Surface, c.BorderStrong
		switch {
		case disabled:
			bg, border = c.SurfaceAlt, c.Border
		case d.Open || gtx.Focused(&d.field):
			border = c.Focus
		case d.field.Hovered():
			bg = c.SurfaceHover
		}
		draw.RRect(gtx.Ops, r, rad, bg)
		draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), border)
		pad := gtx.Dp(th.Space.S)
		cgtx := gtx
		cgtx.Constraints = layout.Constraints{Min: image.Pt(max(0, w-2*pad), 0), Max: image.Pt(max(0, w-2*pad), h)}
		m := op.Record(gtx.Ops)
		cd := layout.Flex{Alignment: layout.Middle}.Layout(cgtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				fw := gtx.Constraints.Max.X
				gtx.Constraints.Min.X = 0
				var ld layout.Dimensions
				if d.Selected < 0 || d.Selected >= len(d.Options) {
					ld = atoms.Muted(d.Placeholder).SingleLine().Layout(gtx, th)
				} else {
					ld = d.layoutOption(gtx, th, d.Options[d.Selected], false)
				}
				ld.Size.X = fw
				return ld
			}),
			layout.Rigid(atoms.Icon{Glyph: atoms.GlyphDropDown, Color: c.TextMuted}.Fn(th)),
		)
		call := m.Stop()
		t := op.Offset(image.Pt(pad, (h-cd.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		if gtx.Enabled() {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		semantic.Button.Add(gtx.Ops)
		return layout.Dimensions{Size: size, Baseline: (h-cd.Size.Y)/2 + cd.Baseline}
	})
}

// layoutOption draws glyph, label, (detail) and badge of an option.
func (d *Dropdown) layoutOption(gtx layout.Context, th *tokens.Theme, o DropdownOption, detail bool) layout.Dimensions {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if o.Glyph == nil {
				return layout.Dimensions{}
			}
			return layout.Inset{Right: th.Space.S - th.Space.XXS}.Layout(gtx, atoms.Icon{Glyph: o.Glyph, Color: th.Color.TextMuted}.Fn(th))
		}),
		layout.Rigid(atoms.Text(o.Label).SingleLine().Fn(th)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			w := gtx.Constraints.Max.X
			gtx.Constraints.Min.X = 0
			if !detail || o.Detail == "" {
				return layout.Dimensions{Size: image.Pt(w, 0)}
			}
			dd := layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Small(o.Detail).SingleLine().Fn(th))
			dd.Size.X = w
			return dd
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if o.Badge == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: th.Space.S}.Layout(gtx, atoms.Badge{Text: o.Badge, Kind: o.BadgeKind}.Fn(th))
		}),
	)
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
