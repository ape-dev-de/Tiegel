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

// DropdownOption is one entry of a Dropdown.
type DropdownOption struct {
	Label string
	// Detail is shown muted after the label in the menu.
	Detail string
	Glyph  *atoms.Glyph
	// Badge is an optional short text at the right end (e.g. a version).
	Badge     string
	BadgeKind atoms.BadgeKind
}

// dropdownMaxRows bounds the menu height; longer menus scroll.
const dropdownMaxRows = 10

// Dropdown is a select field: it shows the selected option and opens a menu
// of all options below itself on click. Clicking outside closes the menu.
// The field fills the available width like a TextField.
type Dropdown struct {
	Options []DropdownOption
	// Selected is the index of the selected option, -1 for none.
	Selected int
	// Placeholder is shown when nothing is selected.
	Placeholder string
	Disabled    bool

	open  bool
	field widget.Clickable
	rows  []widget.Clickable
	list  ScrollList
	scrim int // event tag of the click-outside area
}

// NewDropdown returns a dropdown without selection.
func NewDropdown(placeholder string, opts ...DropdownOption) *Dropdown {
	return &Dropdown{Options: opts, Selected: -1, Placeholder: placeholder}
}

// IsOpen reports whether the menu is shown.
func (d *Dropdown) IsOpen() bool { return d.open }

// SetOpen opens or closes the menu.
func (d *Dropdown) SetOpen(open bool) { d.open = open && !d.Disabled }

// Update processes input and reports whether the user picked a different
// option (Selected has been updated).
func (d *Dropdown) Update(gtx layout.Context) (changed bool) {
	if len(d.rows) != len(d.Options) {
		d.rows = make([]widget.Clickable, len(d.Options))
	}
	if d.Disabled {
		d.open = false
	}
	for d.field.Clicked(gtx) {
		if !d.Disabled {
			d.open = !d.open
		}
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &d.scrim, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, ok := ev.(pointer.Event); ok {
			d.open = false
		}
	}
	for i := range d.rows {
		for d.rows[i].Clicked(gtx) {
			if d.open {
				changed = changed || d.Selected != i
				d.Selected = i
				d.open = false
			}
		}
	}
	return changed
}

// Layout draws the field and, when open, the menu on top of everything.
func (d *Dropdown) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	d.Update(gtx)
	if d.Disabled {
		gtx = gtx.Disabled()
	}
	w := draw.Width(gtx, gtx.Dp(fallbackDropdownWidth))
	h := gtx.Dp(th.Size.ControlH)
	size := image.Pt(w, h)
	dims := d.field.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		c := th.Color
		r := image.Rectangle{Max: size}
		rad := gtx.Dp(th.Radius.M)
		bg, border := c.Surface, c.BorderStrong
		switch {
		case d.Disabled:
			bg, border = c.SurfaceAlt, c.Border
		case d.open || gtx.Focused(&d.field):
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
				w := gtx.Constraints.Max.X
				gtx.Constraints.Min.X = 0
				var ld layout.Dimensions
				if d.Selected < 0 || d.Selected >= len(d.Options) {
					ld = atoms.Muted(d.Placeholder).SingleLine().Layout(gtx, th)
				} else {
					ld = d.layoutOption(gtx, th, d.Options[d.Selected], false)
				}
				ld.Size.X = w
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
	if d.open {
		d.layoutMenu(gtx, th, w, h)
	}
	return dims
}

// fallbackDropdownWidth is used when the width is unbounded.
const fallbackDropdownWidth = unit.Dp(240)

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

// layoutMenu records the menu (and a full-window click catcher behind it)
// as a deferred op so it is drawn above everything else.
func (d *Dropdown) layoutMenu(gtx layout.Context, th *tokens.Theme, w, fieldH int) {
	m := op.Record(gtx.Ops)
	// Click-outside catcher: a huge area below the menu.
	const big = 1 << 16
	sc := clip.Rect(image.Rect(-big, -big, big, big)).Push(gtx.Ops)
	event.Op(gtx.Ops, &d.scrim)
	sc.Pop()

	rowH := gtx.Dp(th.Size.RowH) + gtx.Dp(th.Space.XS)
	pad := gtx.Dp(th.Space.XS)
	n := len(d.Options)
	menuH := min(n, dropdownMaxRows)*rowH + 2*pad
	off := op.Offset(image.Pt(0, fieldH+gtx.Dp(th.Space.XXS))).Push(gtx.Ops)
	r := image.Rectangle{Max: image.Pt(w, menuH)}
	rad := gtx.Dp(th.Radius.M)
	draw.RRect(gtx.Ops, r, rad, th.Color.Surface)
	draw.Border(gtx.Ops, r, rad, gtx.Dp(th.Size.BorderWidth), th.Color.BorderStrong)
	// Swallow clicks on the menu background.
	bg := clip.Rect(r).Push(gtx.Ops)
	event.Op(gtx.Ops, &d.rows)
	bg.Pop()
	lgtx := gtx
	lgtx.Constraints = layout.Exact(image.Pt(w-2*pad, menuH-2*pad))
	t := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
	d.list.Layout(lgtx, th, n, func(gtx layout.Context, i int) layout.Dimensions {
		return d.rows[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			sz := image.Pt(gtx.Constraints.Max.X, rowH)
			switch {
			case d.rows[i].Hovered():
				draw.RRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(th.Radius.S), th.Color.SurfaceHover)
			case i == d.Selected:
				draw.RRect(gtx.Ops, image.Rectangle{Max: sz}, gtx.Dp(th.Radius.S), th.Color.Selection)
			}
			p := gtx.Dp(th.Space.S)
			cgtx := gtx
			cgtx.Constraints = layout.Constraints{Min: image.Pt(sz.X-2*p, 0), Max: image.Pt(sz.X-2*p, rowH)}
			mm := op.Record(gtx.Ops)
			cd := d.layoutOption(cgtx, th, d.Options[i], true)
			call := mm.Stop()
			tt := op.Offset(image.Pt(p, (rowH-cd.Size.Y)/2)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			tt.Pop()
			pointer.CursorPointer.Add(gtx.Ops)
			return layout.Dimensions{Size: sz}
		})
	})
	t.Pop()
	off.Pop()
	op.Defer(gtx.Ops, m.Stop())
}
