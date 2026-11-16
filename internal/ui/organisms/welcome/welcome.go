// Package welcome provides the start screen shown without an open project:
// recently opened projects and a path field to open a directory.
package welcome

import (
	"image"
	"path/filepath"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Event is reported by View.Update.
type Event interface{ isWelcomeEvent() }

// OpenEvent: open the project directory Path.
type OpenEvent struct{ Path string }

func (OpenEvent) isWelcomeEvent() {}

// View is the welcome organism.
type View struct {
	// Recent are project directories, most recent first.
	Recent []string
	// Version is shown under the title.
	Version string
	// Error is shown under the path field (e.g. not a directory).
	Error string

	path   atoms.TextField
	open   atoms.Button
	rows   []molecules.ListRow
	list   molecules.ScrollList
	inited bool
}

func (v *View) init() {
	if v.inited {
		return
	}
	v.inited = true
	v.path.Hint, v.path.Leading = "Project directory, e.g. ~/code/shop", atoms.GlyphFolder
	v.path.Editor.SingleLine, v.path.Editor.Submit = true, true
	v.open = atoms.Button{Text: "Open", Kind: atoms.Primary, Glyph: atoms.GlyphFolderOpen}
}

// SetPath fills the path field.
func (v *View) SetPath(p string) { v.init(); v.path.SetText(p) }

// Update returns open requests.
func (v *View) Update(gtx layout.Context) []Event {
	v.init()
	for len(v.rows) < len(v.Recent) {
		v.rows = append(v.rows, molecules.ListRow{})
	}
	var out []Event
	for {
		ev, ok := v.path.Editor.Update(gtx)
		if !ok {
			break
		}
		if _, ok := ev.(widget.SubmitEvent); ok && v.path.Text() != "" {
			out = append(out, OpenEvent{Path: v.path.Text()})
		}
	}
	if v.open.Clicked(gtx) && v.path.Text() != "" {
		out = append(out, OpenEvent{Path: v.path.Text()})
	}
	for i, p := range v.Recent {
		if c, _ := v.rows[i].Update(gtx); c {
			out = append(out, OpenEvent{Path: p})
		}
	}
	return out
}

// Layout fills the available space and centers a column of max 640dp.
func (v *View) Layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	v.Update(gtx)
	size := gtx.Constraints.Max
	atoms.Surface{Kind: atoms.SurfaceWindow}.Paint(withMin(gtx, size), th)
	colW := min(gtx.Dp(unit.Dp(640)), size.X-2*gtx.Dp(th.Space.XL))
	cg := gtx
	cg.Constraints = layout.Constraints{Min: image.Pt(colW, 0), Max: image.Pt(colW, max(0, size.Y-2*gtx.Dp(th.Space.XXL)))}
	off := image.Pt(max(0, (size.X-colW)/2), max(gtx.Dp(th.Space.XXL), size.Y/8))
	t := op.Offset(off).Push(gtx.Ops)
	v.column(th)(cg)
	t.Pop()
	return layout.Dimensions{Size: size}
}

func (v *View) column(th *tokens.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(atoms.Title("Tiegel").Fn(th)),
			layout.Rigid(atoms.Muted("PHP IDE · "+v.Version).Fn(th)),
			layout.Rigid(layout.Spacer{Height: th.Space.XL}.Layout),
			layout.Rigid(atoms.Strong("Open a project").Fn(th)),
			layout.Rigid(layout.Spacer{Height: th.Space.S}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return v.path.Layout(gtx, th) }),
					layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.open.Layout(gtx, th) }),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if v.Error == "" {
					return layout.Dimensions{}
				}
				return layout.Inset{Top: th.Space.XS}.Layout(gtx, atoms.Label{Text: v.Error, Role: atoms.RoleSmall, Color: th.Color.Danger}.Fn(th))
			}),
			layout.Rigid(layout.Spacer{Height: th.Space.XL}.Layout),
			layout.Rigid(molecules.SectionHeader{Title: "Recent projects"}.Fn(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if len(v.Recent) == 0 {
					return layout.Inset{Top: th.Space.S}.Layout(gtx, atoms.Muted("No recent projects yet.").Fn(th))
				}
				h := min(len(v.Recent), 10) * gtx.Dp(th.Size.RowH+th.Space.S)
				gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, min(h, gtx.Constraints.Max.Y)))
				return v.list.Layout(gtx, th, len(v.Recent), func(gtx layout.Context, i int) layout.Dimensions {
					p := v.Recent[i]
					return v.rows[i].Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(atoms.Icon{Glyph: atoms.GlyphFolder, Color: th.Color.Accent}.Fn(th)),
							layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
							layout.Rigid(atoms.Strong(filepath.Base(p)).Fn(th)),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = 0
								return layout.Inset{Left: th.Space.M}.Layout(gtx, atoms.Small(p).SingleLine().Fn(th))
							}),
						)
					})
				})
			}),
		)
	}
}

func withMin(gtx layout.Context, size image.Point) layout.Context {
	gtx.Constraints = layout.Exact(size)
	return gtx
}
