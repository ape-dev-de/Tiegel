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

func dropdownPage() *page {
	dd := molecules.NewDropdown("Choose a PHP interpreter",
		molecules.DropdownOption{Label: "php 8.3 (Homebrew)", Detail: "/opt/homebrew/bin/php", Glyph: atoms.GlyphComputer, Badge: "8.3.4", BadgeKind: atoms.BadgeInfo},
		molecules.DropdownOption{Label: "compose:php", Detail: "php:8.3-fpm", Glyph: atoms.GlyphContainer, Badge: "score 115", BadgeKind: atoms.BadgeAccent},
		molecules.DropdownOption{Label: "compose:worker", Detail: "shop/worker", Glyph: atoms.GlyphContainer, Badge: "score 40"},
	)
	disabled := molecules.NewDropdown("Disabled")
	disabled.Disabled = true
	return &page{
		Level: "Molecules", Name: "Dropdown", Glyph: atoms.GlyphDropDown,
		Summary: "Select field with a menu drawn above everything else; click outside to close.",
		Variants: func(th *tokens.Theme) []variant {
			sel := "none"
			if dd.Selected >= 0 {
				sel = dd.Options[dd.Selected].Label
			}
			return []variant{
				{"Interactive – selected: " + sel, fixedWidth(380, func(gtx layout.Context) layout.Dimensions {
					d := dd.Layout(gtx, th)
					if dd.IsOpen() {
						d.Size.Y += gtx.Dp(110) // room for the menu
					}
					return d
				})},
				{"Disabled", fixedWidth(380, func(gtx layout.Context) layout.Dimensions { return disabled.Layout(gtx, th) })},
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
	split := &molecules.SplitPane{Ratio: 0.35}
	vsplit := &molecules.SplitPane{Axis: layout.Vertical}
	var list molecules.ScrollList
	return &page{
		Level: "Molecules", Name: "Table & SplitPane", Glyph: atoms.GlyphSwap,
		Summary: "Table: header + scrolling rows with caller-drawn cells. SplitPane: draggable divider. ScrollList: virtual list with indicator.",
		Variants: func(th *tokens.Theme) []variant {
			pane := func(title string, w layout.Widget) layout.Widget {
				return func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(molecules.SectionHeader{Title: title}.Fn(th)),
						layout.Flexed(1, w),
					)
				}
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
				{"SplitPane (drag the dividers) with ScrollList", sized(260, func(gtx layout.Context) layout.Dimensions {
					return split.Layout(gtx, th, pane("Left – 200 rows", rows), func(gtx layout.Context) layout.Dimensions {
						return vsplit.Layout(gtx, th, pane("Top", atoms.Surface{}.Fn(th)), pane("Bottom", atoms.Surface{Color: th.Color.SurfaceAlt}.Fn(th)))
					})
				})},
			}
		},
	}
}
