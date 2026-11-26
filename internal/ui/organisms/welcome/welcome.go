// Package welcome provides the start screen shown without an open project:
// recently opened projects and a path field to open a directory.
package welcome

import (
	"image"
	"path/filepath"

	"gioui.org/io/key"
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

// OpenEvent: open the project directory Path; NewWindow on Shift+click
// of a recent project (open it in a new window).
type OpenEvent struct {
	Path      string
	NewWindow bool
}

// ContextEvent: a context click (right click, Ctrl+click on macOS) on the
// recent project Path; the host shows its menu ("Open in New Window").
type ContextEvent struct{ Path string }

// BrowseEvent: "Open…" was clicked (the host shows a folder chooser).
type BrowseEvent struct{}

// RemoveEvent: remove Path from the recent projects.
type RemoveEvent struct{ Path string }

// ImportEvent: "Import Settings…" was clicked (the host opens the
// settings import wizard).
type ImportEvent struct{}

func (OpenEvent) isWelcomeEvent()    {}
func (ContextEvent) isWelcomeEvent() {}
func (BrowseEvent) isWelcomeEvent()  {}
func (RemoveEvent) isWelcomeEvent()  {}
func (ImportEvent) isWelcomeEvent()  {}

// View is the welcome organism.
type View struct {
	// Recent are project directories, most recent first.
	Recent []string
	// Version is shown under the title.
	Version string
	// Error is shown under the path field (e.g. not a directory).
	Error string
	// Hints are shortcut hints shown below the recent projects
	// ("Open…  Cmd+Shift+O").
	Hints []string
	// GOOS enables Ctrl+click as context click on "darwin".
	GOOS string

	path    atoms.TextField
	open    atoms.Button
	browse  atoms.Button
	rows    []molecules.ListRow
	ctx     []molecules.ContextArea
	removes []atoms.IconButton
	importS atoms.Button
	list    molecules.ScrollList
	inited  bool
}

func (v *View) init() {
	if v.inited {
		return
	}
	v.inited = true
	v.path.Hint, v.path.Leading = "Project directory, e.g. ~/code/shop", atoms.GlyphFolder
	v.path.Editor.SingleLine, v.path.Editor.Submit = true, true
	v.open = atoms.Button{Text: "Open", Kind: atoms.Secondary}
	v.browse = atoms.Button{Text: "Open…", Kind: atoms.Primary, Glyph: atoms.GlyphFolderOpen}
	v.importS = atoms.Button{Text: "Import Settings…", Kind: atoms.Secondary}
}

// SetPath fills the path field.
func (v *View) SetPath(p string) { v.init(); v.path.SetText(p) }

// Update returns open requests.
func (v *View) Update(gtx layout.Context) []Event {
	v.init()
	for len(v.rows) < len(v.Recent) {
		v.rows = append(v.rows, molecules.ListRow{})
		v.ctx = append(v.ctx, molecules.ContextArea{})
		v.removes = append(v.removes, atoms.IconButton{Glyph: atoms.GlyphClose, Description: "Remove from recent projects"})
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
	if v.browse.Clicked(gtx) {
		out = append(out, BrowseEvent{})
	}
	if v.importS.Clicked(gtx) {
		out = append(out, ImportEvent{})
	}
	for i, p := range v.Recent {
		if v.removes[i].Clicked(gtx) {
			out = append(out, RemoveEvent{Path: p})
			continue
		}
		v.ctx[i].GOOS = v.GOOS
		if _, ok := v.ctx[i].Update(gtx); ok {
			v.rows[i].Update(gtx) // a Ctrl+click is a primary click as well
			out = append(out, ContextEvent{Path: p})
			continue
		}
		if c, _, mods := v.rows[i].UpdateMods(gtx); c {
			if v.GOOS == "darwin" && mods.Contain(key.ModCtrl) {
				out = append(out, ContextEvent{Path: p})
				continue
			}
			out = append(out, OpenEvent{Path: p, NewWindow: mods.Contain(key.ModShift)})
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
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.browse.Layout(gtx, th) }),
					layout.Rigid(layout.Spacer{Width: th.Space.M}.Layout),
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
					return v.ctx[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions { return v.recentRow(gtx, th, i, p) })
				})
			}),
			layout.Rigid(layout.Spacer{Height: th.Space.XL}.Layout),
			layout.Rigid(molecules.SectionHeader{Title: "Coming from another IDE?"}.Fn(th)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: th.Space.S}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.importS.Layout(gtx, th) }),
						layout.Rigid(layout.Spacer{Width: th.Space.M}.Layout),
						layout.Flexed(1, atoms.Small("Keymap, fonts, theme, terminal and more from the reference IDE; preview first.").Fn(th)),
					)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if len(v.Hints) == 0 {
					return layout.Dimensions{}
				}
				rows := make([]layout.FlexChild, 0, len(v.Hints)+1)
				rows = append(rows, layout.Rigid(layout.Spacer{Height: th.Space.XL}.Layout))
				for _, h := range v.Hints {
					rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Bottom: th.Space.XS}.Layout(gtx, atoms.Muted(h).Fn(th))
					}))
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
			}),
		)
	}
}

// recentRow draws recent project i (path p).
func (v *View) recentRow(gtx layout.Context, th *tokens.Theme, i int, p string) layout.Dimensions {
	return v.rows[i].Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(atoms.Icon{Glyph: atoms.GlyphFolder, Color: th.Color.Accent}.Fn(th)),
			layout.Rigid(layout.Spacer{Width: th.Space.S}.Layout),
			layout.Rigid(atoms.Strong(filepath.Base(p)).Fn(th)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = 0
				d := layout.Inset{Left: th.Space.M}.Layout(gtx, atoms.Small(p).SingleLine().Fn(th))
				d.Size.X = gtx.Constraints.Max.X // the remove button sits at the right end
				return d
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				b := &v.removes[i]
				b.Size, b.IconSize = th.Size.RowH, th.Size.IconSmall
				if !v.rows[i].Hovered() && !b.Hovered() {
					m := op.Record(gtx.Ops)
					d := b.Layout(gtx, th)
					m.Stop()
					return d
				}
				return b.Layout(gtx, th)
			}),
		)
	})
}

func withMin(gtx layout.Context, size image.Point) layout.Context {
	gtx.Constraints = layout.Exact(size)
	return gtx
}
