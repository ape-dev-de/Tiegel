package main

import (
	"fmt"
	"strings"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

// Molecule pages added for the debug and runtime organisms.

func treeRowPage() *page {
	rows := []*molecules.TreeRow{
		{Name: "$user", Value: "{App\\Entity\\User}", Detail: "object", Expandable: true, Expanded: true},
		{Name: "id", Value: "42", Detail: "int", Depth: 1},
		{Name: "email", Value: "\"ada@example.org\"", Detail: "string", Depth: 1, Selected: true},
		{Name: "roles", Value: "array(2)", Detail: "array", Depth: 1, Expandable: true},
		{Name: "$count", Value: "3", Detail: "int"},
	}
	copyBtn := atoms.NewIconButton(atoms.GlyphCopy, "Copy value")
	status := "Click rows to select, arrows (or double click) to expand."
	return &page{
		Level: "Molecules", Name: "TreeRow", Glyph: atoms.GlyphList,
		Summary: "Generic tree row: indentation, arrow, optional glyph, name, monospace value, muted detail, trailing actions.",
		Variants: func(th *tokens.Theme) []variant {
			copyBtn.Size, copyBtn.IconSize = th.Size.RowH-th.Space.XS, th.Size.IconSmall
			return []variant{
				{"Variables-style rows – " + status, fixedWidth(520, func(gtx layout.Context) layout.Dimensions {
					if copyBtn.Clicked(gtx) {
						status = "Copy requested"
					}
					for _, r := range rows {
						if clicked, toggled, _ := r.Update(gtx); clicked || toggled {
							for _, o := range rows {
								o.Selected = o == r
							}
							status = fmt.Sprintf("Selected %s (expanded: %v)", r.Name, r.Expanded)
						}
					}
					ws := make([]layout.Widget, len(rows))
					for i, r := range rows {
						r.NameColor = th.Syntax.Property
						if strings.HasPrefix(r.Name, "$") {
							r.NameColor = th.Syntax.Variable
						}
						r.Trailing = nil
						if r.Selected {
							r.Trailing = func(gtx layout.Context) layout.Dimensions { return copyBtn.Layout(gtx, th) }
						}
						ws[i] = func(gtx layout.Context) layout.Dimensions { return r.Layout(gtx, th) }
					}
					return column(0, ws...)(gtx)
				})},
			}
		},
	}
}

func toolbarPage() *page {
	tb := &molecules.Toolbar{Items: []molecules.ToolbarItem{
		{ID: "run", Glyph: atoms.GlyphRun, Label: "Run", Shortcut: "Ctrl+R"},
		{ID: "debug", Glyph: atoms.GlyphDebug, Label: "Debug", Shortcut: "Ctrl+D"},
		{Separator: true},
		{ID: "listen", Glyph: atoms.GlyphListen, Label: "Listen for debug connections", Active: true},
		{ID: "stop", Glyph: atoms.GlyphStop, Label: "Stop", Disabled: true},
	}}
	compact := &molecules.Toolbar{ButtonSize: 24, Items: []molecules.ToolbarItem{
		{ID: "add", Glyph: atoms.GlyphAdd, Label: "Add"},
		{ID: "refresh", Glyph: atoms.GlyphRefresh, Label: "Refresh"},
	}}
	last := "–"
	return &page{
		Level: "Molecules", Name: "Toolbar", Glyph: atoms.GlyphSettings,
		Summary: "Icon buttons with tooltips (label + shortcut), toggles (Active) and separators. SectionHeader titles panel sections.",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Toolbar (listen toggles) – last click: " + last, func(gtx layout.Context) layout.Dimensions {
					for _, id := range tb.Update(gtx) {
						last = id
						if id == "listen" {
							tb.Items[3].Active = !tb.Items[3].Active
							tb.Items[4].Disabled = !tb.Items[3].Active
						}
					}
					return tb.Layout(gtx, th)
				}},
				{"SectionHeader with compact toolbar", fixedWidth(420, func(gtx layout.Context) layout.Dimensions {
					for _, id := range compact.Update(gtx) {
						last = id
					}
					return molecules.SectionHeader{Title: "Watches", Badge: "3", Trailing: func(gtx layout.Context) layout.Dimensions {
						return compact.Layout(gtx, th)
					}}.Layout(gtx, th)
				})},
			}
		},
	}
}

func tablePage() *page {
	tb := &molecules.Table{Columns: []molecules.TableColumn{{Title: "Host path", Weight: 2}, {Title: "Container path", Weight: 2}, {Title: "Source", Width: 120}}}
	data := [][3]string{
		{"~/projects/shop", "/var/www/html", "volume"},
		{"~/projects/shop/vendor", "/var/www/html/vendor", "volume"},
		{"~/.composer", "/root/.composer", "manual"},
	}
	split := &molecules.Split{Ratio: 0.35}
	vsplit := &molecules.Split{Vertical: true}
	var list molecules.ScrollList
	return &page{
		Level: "Molecules", Name: "Table", Glyph: atoms.GlyphSwap,
		Summary: "Header + scrolling rows (ScrollList) with caller-drawn cells; fixed or weighted columns. Shown inside a Split.",
		Variants: func(th *tokens.Theme) []variant {
			pane := func(title string, w layout.Widget) layout.Widget {
				return func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(molecules.SectionHeader{Title: title}.Fn(th)),
						layout.Flexed(1, w),
					)
				}
			}
			fill := func(k atoms.SurfaceKind) layout.Widget {
				return func(gtx layout.Context) layout.Dimensions { return atoms.Surface{Kind: k}.Paint(gtx, th) }
			}
			rows := func(gtx layout.Context) layout.Dimensions {
				return list.Layout(gtx, th, 200, func(gtx layout.Context, i int) layout.Dimensions {
					return layout.Inset{Left: th.Space.S, Top: th.Space.XXS, Bottom: th.Space.XXS}.Layout(gtx,
						atoms.Code(fmt.Sprintf("line %03d", i+1)).Fn(th))
				})
			}
			return []variant{
				{"Table", sized(140, func(gtx layout.Context) layout.Dimensions {
					return tb.Layout(gtx, th, len(data), func(gtx layout.Context, r, c int) layout.Dimensions {
						if c == 2 {
							return atoms.Small(data[r][c]).Layout(gtx, th)
						}
						return atoms.Code(data[r][c]).SingleLine().Layout(gtx, th)
					})
				})},
				{"Inside Splits with a ScrollList (drag the dividers)", sized(260, func(gtx layout.Context) layout.Dimensions {
					return split.Layout(gtx, th, pane("Left – 200 rows", rows), func(gtx layout.Context) layout.Dimensions {
						return vsplit.Layout(gtx, th, pane("Top", fill(atoms.SurfacePanel)), pane("Bottom", fill(atoms.SurfaceAlt)))
					})
				})},
			}
		},
	}
}
