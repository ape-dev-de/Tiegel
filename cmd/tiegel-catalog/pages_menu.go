package main

import (
	"image"
	"runtime"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// contextMenuPage shows the ContextMenu with the standard menus of
// internal/actions: right-click (Ctrl+click on macOS) into an area opens
// the menu of the chosen context at the mouse position.
func contextMenuPage() *page {
	reg := actions.Standard(runtime.GOOS)
	type demo struct {
		label string
		ctx   actions.Context
		area  molecules.ContextArea
		menu  molecules.ContextMenu
	}
	demos := []*demo{
		{label: "Project tree (right-click here)", ctx: actions.Context{Menu: actions.CtxProjectTree, Paths: []string{"src/Order.php"}, Values: map[string]string{"git": "1"}}},
		{label: "Editor with selection (right-click here)", ctx: actions.Context{Menu: actions.CtxEditor, Language: "php", Selection: true, Paths: []string{"src/Order.php"}}},
		{label: "Tab (right-click here)", ctx: actions.Context{Menu: actions.CtxTab, Paths: []string{"src/Order.php"}, Values: map[string]string{"pinned": "1"}}},
	}
	open := atoms.NewButton(atoms.Secondary, "Open with keyboard")
	var kbd molecules.ContextMenu
	chosen := "–"
	return &page{
		Level: "Molecules", Name: "ContextMenu", Glyph: atoms.GlyphMore,
		Summary: "Popup menu at the mouse position with icons, shortcuts, check/radio marks, separators and submenus; flips at the edges, keyboard navigation (arrows, Enter, Esc, letters). Items come from internal/actions.",
		Variants: func(th *tokens.Theme) []variant {
			var vs []variant
			for _, d := range demos {
				vs = append(vs, variant{d.label, fixedSize(0, 120, func(gtx layout.Context) layout.Dimensions {
					d.area.GOOS = runtime.GOOS
					if _, ok := d.area.Update(gtx); ok {
						d.menu.Items = molecules.MenuItemsFromEntries(reg.Resolve(d.ctx, nil, runtime.GOOS))
						d.menu.ShowAtPointer()
					}
					if id, ok := d.menu.Update(gtx); ok {
						chosen = id
					}
					dims := d.area.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return atoms.Surface{Kind: atoms.SurfaceEditor, Border: true, Rounded: true}.Fill(gtx, th, func(gtx layout.Context) layout.Dimensions {
							return layout.UniformInset(th.Space.M).Layout(gtx, atoms.Muted("Last chosen: "+chosen).Fn(th))
						})
					})
					// Bounds: the visible catalog area around the preview.
					d.menu.Bounds = image.Rect(-gtx.Dp(200), -gtx.Dp(200), gtx.Constraints.Max.X, gtx.Dp(420))
					d.menu.Layout(gtx, th)
					return dims
				})})
			}
			vs = append(vs, variant{"Keyboard: Down/Up, Right opens, Left/Esc closes, letters jump", fixedSize(0, 40, func(gtx layout.Context) layout.Dimensions {
				if open.Clicked(gtx) {
					kbd.Items = sampleMenuItems()
					kbd.ShowAt(image.Pt(0, gtx.Dp(36)))
				}
				if id, ok := kbd.Update(gtx); ok {
					chosen = id
				}
				d := open.Layout(gtx, th)
				kbd.Bounds = image.Rect(0, 0, gtx.Constraints.Max.X, gtx.Dp(400))
				kbd.Layout(gtx, th)
				return d
			})})
			return vs
		},
	}
}

func sampleMenuItems() []molecules.MenuItem {
	return []molecules.MenuItem{
		{ID: "view.wrap", Label: "Soft Wrap", Check: molecules.MenuCheckBox, Checked: true},
		{ID: "view.whitespace", Label: "Show Whitespace", Check: molecules.MenuCheckBox},
		{Separator: true},
		{Label: "Line Separator", Children: []molecules.MenuItem{
			{ID: "eol.lf", Label: "LF – Unix and macOS", Check: molecules.MenuCheckRadio, Checked: true},
			{ID: "eol.crlf", Label: "CRLF – Windows", Check: molecules.MenuCheckRadio},
		}},
		{ID: "file.reveal", Label: actions.RevealLabel(runtime.GOOS), Glyph: atoms.GlyphFolderOpen, Shortcut: "Alt+F1"},
		{ID: "file.delete", Label: "Delete…", Glyph: atoms.GlyphDelete, Disabled: true},
	}
}
