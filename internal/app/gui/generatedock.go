package gui

import (
	"strings"

	"github.com/ape-dev-de/tiegel/internal/app/gui/controller"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/palette"
)

// Generate (Cmd+N, AP17.3): the backend's menu (generate.list) with the
// language server's generating code actions and the built-in constructor,
// getters and setters; a built-in generator with several candidate
// properties asks which ones next.

// showGenerate offers the Generate menu.
func (d *templateDock) showGenerate(m controller.GenerateMenu) {
	if len(m.Actions) == 0 {
		d.v.ctl.Status.Notice = "Nothing to generate here"
		return
	}
	items := make([]palette.Item, len(m.Actions))
	for i, a := range m.Actions {
		detail := a.Disabled
		if detail == "" && a.Source == "lsp" {
			detail = "language server"
		}
		if detail == "" && len(a.Properties) > 0 {
			detail = strings.Join(a.Properties, ", ")
		}
		g := atoms.GlyphFileCode
		if a.Disabled != "" {
			g = atoms.GlyphRemove
		}
		items[i] = palette.Item{Label: a.Title, Detail: detail, Glyph: g}
	}
	title := "Generate"
	if m.Class != "" {
		title += " in " + m.Class
	}
	d.v.OpenChooser(ChooserSpec{Title: title, Hint: "Filter", Items: items,
		OnQuery: func(q string) []palette.Item { return filterItems(items, q) },
		OnChoose: func(i int, q string) {
			i = chosenIndex(items, q, i)
			if i < 0 {
				return
			}
			a := m.Actions[i]
			d.v.focusEditor = true
			if a.Source != "builtin" || a.Disabled != "" || len(a.Properties) < 2 {
				d.v.ctl.RunGenerate(m, a, nil)
				return
			}
			d.v.Post(func() { d.chooseProperties(m, a) })
		}})
}

// chooseProperties asks for the properties of a built-in generator.
func (d *templateDock) chooseProperties(m controller.GenerateMenu, a controller.GenerateAction) {
	items := []palette.Item{{Label: "All properties", Detail: strings.Join(a.Properties, ", "), Glyph: atoms.GlyphDone}}
	for _, p := range a.Properties {
		items = append(items, palette.Item{Label: "$" + p, Glyph: atoms.GlyphFileCode})
	}
	d.v.OpenChooser(ChooserSpec{Title: a.Title, Hint: "Filter", Items: items,
		OnQuery: func(q string) []palette.Item { return filterItems(items, q) },
		OnChoose: func(i int, q string) {
			i = chosenIndex(items, q, i)
			d.v.focusEditor = true
			switch {
			case i == 0:
				d.v.ctl.RunGenerate(m, a, nil)
			case i > 0:
				d.v.ctl.RunGenerate(m, a, []string{a.Properties[i-1]})
			}
		}})
}

// filterItems keeps the items matching q (label or detail).
func filterItems(items []palette.Item, q string) []palette.Item {
	var out []palette.Item
	for _, it := range items {
		if matchTemplate(it, q) {
			out = append(out, it)
		}
	}
	return out
}

// chosenIndex maps index i of the items filtered by q back to items.
func chosenIndex(items []palette.Item, q string, i int) int {
	n := -1
	for j, it := range items {
		if matchTemplate(it, q) {
			n++
			if n == i {
				return j
			}
		}
	}
	return -1
}
