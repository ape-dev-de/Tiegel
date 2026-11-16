package main

import (
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// page is one catalog entry.
type page struct {
	Level   string // Tokens, Atoms, Molecules
	Name    string
	Summary string
	Glyph   *atoms.Glyph
	// Variants returns the labelled previews; called every frame.
	Variants func(th *tokens.Theme) []variant

	nav molecules.FileRow
	// editor is the state of the Editor page (nil elsewhere).
	editor *editorDemo
}

// variant is one labelled preview on a page.
type variant struct {
	Label string
	W     layout.Widget
}

// catalog is the whole app state.
type catalog struct {
	th       *tokens.Theme
	pages    []*page
	selected int

	toggle  atoms.Button
	nav     layout.List
	preview layout.List
	search  *molecules.SearchField
}

func newCatalog(light bool) *catalog {
	mode := tokens.ModeDark
	if light {
		mode = tokens.ModeLight
	}
	c := &catalog{
		th:     tokens.New(mode, nil),
		pages:  buildPages(),
		search: molecules.NewSearchField("Filter components"),
	}
	c.toggle.Kind = atoms.Secondary
	c.nav.Axis = layout.Vertical
	c.preview.Axis = layout.Vertical
	return c
}

// selectPage selects the page called name (case-insensitive).
func (c *catalog) selectPage(name string) bool {
	for i, p := range c.pages {
		if strings.EqualFold(p.Name, name) {
			c.selected = i
			return true
		}
	}
	return false
}

// visible returns the indices of pages matching the filter.
func (c *catalog) visible() []int {
	q := strings.ToLower(strings.TrimSpace(c.search.Text()))
	var out []int
	for i, p := range c.pages {
		if q == "" || strings.Contains(strings.ToLower(p.Name), q) || strings.Contains(strings.ToLower(p.Level), q) {
			out = append(out, i)
		}
	}
	return out
}

// Layout draws the whole window.
func (c *catalog) Layout(gtx layout.Context) layout.Dimensions {
	if c.toggle.Clicked(gtx) {
		c.th = c.th.Toggled()
	}
	th := c.th
	paint.FillShape(gtx.Ops, th.Color.Background, clip.Rect{Max: gtx.Constraints.Max}.Op())

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(c.layoutTopBar),
		layout.Rigid(atoms.Divider{}.Fn(th)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(c.layoutSidebar),
				layout.Rigid(atoms.Divider{Vertical: true}.Fn(th)),
				layout.Flexed(1, c.layoutPreview),
			)
		}),
	)
}

func (c *catalog) layoutTopBar(gtx layout.Context) layout.Dimensions {
	th := c.th
	h := gtx.Dp(48)
	paint.FillShape(gtx.Ops, th.Color.SurfaceAlt, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, h)}.Op())
	if th.Mode == tokens.ModeDark {
		c.toggle.Text, c.toggle.Glyph = "Light mode", atoms.GlyphThemeLight
	} else {
		c.toggle.Text, c.toggle.Glyph = "Dark mode", atoms.GlyphThemeDark
	}
	w := gtx.Constraints.Max.X
	pad := gtx.Dp(th.Space.L)
	gtx.Constraints = layout.Constraints{Min: image.Pt(w-2*pad, 0), Max: image.Pt(w-2*pad, h)}
	bar := recordOps(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(atoms.Heading("Tiegel").Fn(th)),
			layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
			layout.Rigid(atoms.Muted("Component Catalog").Fn(th)),
			layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
			layout.Rigid(atoms.Badge{Text: th.Mode.String(), Kind: atoms.BadgeAccent}.Fn(th)),
			layout.Flexed(1, layout.Spacer{}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return c.toggle.Layout(gtx, th) }),
		)
	})
	bar.addAt(gtx, image.Pt(pad, (h-bar.dims.Size.Y)/2))
	return layout.Dimensions{Size: image.Pt(w, h)}
}

func (c *catalog) layoutSidebar(gtx layout.Context) layout.Dimensions {
	th := c.th
	w := gtx.Dp(240)
	gtx.Constraints = layout.Exact(image.Pt(w, gtx.Constraints.Max.Y))
	paint.FillShape(gtx.Ops, th.Color.Surface, clip.Rect{Max: gtx.Constraints.Max}.Op())

	c.search.Update(gtx)
	vis := c.visible()
	// Rows: search field, then per level a heading followed by its pages.
	type row struct {
		heading string
		page    int
	}
	rows := []row{{heading: "\x00search"}}
	last := ""
	for _, i := range vis {
		p := c.pages[i]
		if p.Level != last {
			rows = append(rows, row{heading: p.Level, page: -1})
			last = p.Level
		}
		rows = append(rows, row{page: i})
	}
	for _, i := range vis {
		if clicked, _ := c.pages[i].nav.Update(gtx); clicked {
			c.selected = i
		}
	}
	return c.nav.Layout(gtx, len(rows), func(gtx layout.Context, idx int) layout.Dimensions {
		r := rows[idx]
		switch {
		case r.heading == "\x00search":
			return layout.UniformInset(th.Space.S).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return c.search.Layout(gtx, th)
			})
		case r.heading != "":
			return layout.Inset{Left: th.Space.M, Top: th.Space.M, Bottom: th.Space.XS}.Layout(gtx,
				atoms.Small(strings.ToUpper(r.heading)).Fn(th))
		}
		p := c.pages[r.page]
		p.nav.Name = p.Name
		p.nav.Glyph = p.Glyph
		p.nav.Selected = r.page == c.selected
		return p.nav.Layout(gtx, th)
	})
}

func (c *catalog) layoutPreview(gtx layout.Context) layout.Dimensions {
	th := c.th
	p := c.pages[c.selected]
	vars := p.Variants(th)
	return c.preview.Layout(gtx, len(vars)+1, func(gtx layout.Context, i int) layout.Dimensions {
		in := layout.Inset{Left: th.Space.XL, Right: th.Space.XL, Top: th.Space.L}
		if i == 0 {
			in.Top = th.Space.XL
			return in.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
							layout.Rigid(atoms.Title(p.Name).Fn(th)),
							layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
							layout.Rigid(atoms.Badge{Text: p.Level}.Fn(th)),
						)
					}),
					layout.Rigid(layout.Spacer{Height: th.Space.XS}.Layout),
					layout.Rigid(atoms.Muted(p.Summary).Fn(th)),
				)
			})
		}
		v := vars[i-1]
		if i == len(vars) {
			in.Bottom = th.Space.XL
		}
		return in.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(atoms.Small(v.Label).Fn(th)),
				layout.Rigid(layout.Spacer{Height: th.Space.XS + th.Space.XXS}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return card(gtx, th, v.W) }),
			)
		})
	})
}

// card draws w on a bordered surface spanning the available width.
func card(gtx layout.Context, th *tokens.Theme, w layout.Widget) layout.Dimensions {
	pad := gtx.Dp(th.Space.L)
	width := gtx.Constraints.Max.X
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(max(0, width-2*pad), gtx.Constraints.Max.Y)}
	m := recordOps(cgtx, w)
	h := m.dims.Size.Y + 2*pad
	rect := image.Rectangle{Max: image.Pt(width, h)}
	rr := gtx.Dp(th.Radius.L)
	paint.FillShape(gtx.Ops, th.Color.Border, clip.UniformRRect(rect, rr).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, th.Color.Surface, clip.UniformRRect(rect.Inset(1), rr-1).Op(gtx.Ops))
	m.addAt(gtx, image.Pt(pad, pad))
	return layout.Dimensions{Size: rect.Max}
}

// flow lays out children left to right, wrapping into new lines.
func flow(gap unit.Dp, children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		g := gtx.Dp(gap)
		maxW := gtx.Constraints.Max.X
		x, y, lineH, width := 0, 0, 0, 0
		cgtx := gtx
		cgtx.Constraints.Min = image.Point{}
		for _, w := range children {
			m := recordOps(cgtx, w)
			sz := m.dims.Size
			if x > 0 && x+sz.X > maxW {
				x, y, lineH = 0, y+lineH+g, 0
			}
			m.addAt(gtx, image.Pt(x, y))
			x += sz.X + g
			lineH = max(lineH, sz.Y)
			width = max(width, x-g)
		}
		return layout.Dimensions{Size: image.Pt(width, y+lineH)}
	}
}

// column stacks children vertically with gap.
func column(gap unit.Dp, children ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		fc := make([]layout.FlexChild, 0, 2*len(children))
		for i, w := range children {
			if i > 0 {
				fc = append(fc, layout.Rigid(layout.Spacer{Height: gap}.Layout))
			}
			fc = append(fc, layout.Rigid(w))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, fc...)
	}
}

// fixedWidth constrains w to at most dp wide.
func fixedWidth(dp unit.Dp, w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(dp))
		gtx.Constraints.Min.X = min(gtx.Constraints.Min.X, gtx.Constraints.Max.X)
		return w(gtx)
	}
}
