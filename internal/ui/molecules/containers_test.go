package molecules_test

import (
	"fmt"
	"image"
	"slices"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestTreeRow(t *testing.T) {
	r := &molecules.TreeRow{Name: "$user", Value: "{App\\User}", Detail: "object", Depth: 1, Expandable: true,
		Glyph: atoms.GlyphList, Trailing: atoms.NewIcon(atoms.GlyphCopy).Fn(tokens.Dark(nil))}
	d := uitest.Smoke(t, "treerow", area, r.Layout)
	if d.Size != image.Pt(area.X, 24) {
		t.Errorf("size %v", d.Size)
	}
	r.Selected, r.Expanded, r.Expandable = true, true, false
	uitest.Smoke(t, "treerow leaf", image.Pt(60, 40), r.Layout)
}

func TestTreeRowToggleAndDoubleClick(t *testing.T) {
	h := uitest.NewHarness(area)
	r := &molecules.TreeRow{Name: "$items", Expandable: true}
	var clicked, toggled, double bool
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		c, tg, db := r.Update(gtx)
		clicked, toggled, double = clicked || c, toggled || tg, double || db
		return r.Layout(gtx, th)
	}
	h.Frame(w)
	h.Click(image.Pt(10, 12)) // arrow (indent 4dp, 16dp icon)
	h.Frame(w)
	if !toggled || !r.Expanded {
		t.Fatal("arrow click did not expand")
	}
	clicked, toggled = false, false
	h.Click(image.Pt(200, 12))
	h.Frame(w)
	if !clicked || toggled {
		t.Fatalf("row click: clicked=%v toggled=%v", clicked, toggled)
	}
	h.DoubleClick(image.Pt(200, 12))
	h.Frame(w)
	if !double || r.Expanded {
		t.Fatalf("double click: double=%v expanded=%v", double, r.Expanded)
	}
}

func TestToolbar(t *testing.T) {
	tb := &molecules.Toolbar{Items: []molecules.ToolbarItem{
		{ID: "run", Glyph: atoms.GlyphRun, Label: "Run", Shortcut: "Ctrl+R"},
		{Separator: true},
		{ID: "listen", Glyph: atoms.GlyphListen, Label: "Listen", Active: true},
		{ID: "stop", Glyph: atoms.GlyphStop, Label: "Stop", Disabled: true},
	}}
	if tb.Items[0].Tooltip() != "Run (Ctrl+R)" || tb.Items[2].Tooltip() != "Listen" {
		t.Fatal("tooltip text")
	}
	d := uitest.Smoke(t, "toolbar", area, tb.Layout)
	if d.Size.Y != 28 || d.Size.X < 3*28 {
		t.Errorf("size %v", d.Size)
	}
	h := uitest.NewHarness(area)
	var got []string
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		got = append(got, tb.Update(gtx)...)
		return tb.Layout(gtx, th)
	}
	h.Frame(w)
	h.Click(image.Pt(10, 10))
	h.Frame(w)
	stopX := d.Size.X - 10
	h.Click(image.Pt(stopX, 10)) // disabled
	h.Frame(w)
	if !slices.Equal(got, []string{"run"}) {
		t.Fatalf("clicked %v", got)
	}
	tb.ButtonSize = 24
	if d := uitest.Smoke(t, "compact", area, tb.Layout); d.Size.Y != 24 {
		t.Errorf("compact height %d", d.Size.Y)
	}
}

func TestSectionHeader(t *testing.T) {
	s := molecules.SectionHeader{Title: "Variables", Badge: "12", Trailing: atoms.NewIcon(atoms.GlyphAdd).Fn(tokens.Dark(nil))}
	if d := uitest.Smoke(t, "section", area, s.Layout); d.Size != image.Pt(area.X, 28) {
		t.Errorf("size %v", d.Size)
	}
	uitest.Smoke(t, "section plain", image.Pt(1<<24, 100), molecules.SectionHeader{Title: "x"}.Layout)
}

func TestDropdownSelect(t *testing.T) {
	dd := molecules.NewSelect("Choose interpreter",
		molecules.DropdownOption{Label: "php 8.3", Detail: "/usr/bin/php", Glyph: atoms.GlyphComputer, Badge: "8.3"},
		molecules.DropdownOption{Label: "compose:php", Detail: "php:8.2-fpm", Glyph: atoms.GlyphContainer, Badge: "best", BadgeKind: atoms.BadgeAccent},
	)
	d := uitest.Smoke(t, "select", area, dd.Layout)
	if d.Size != image.Pt(area.X, 28) {
		t.Errorf("size %v", d.Size)
	}
	dd.Open = true
	uitest.Smoke(t, "select open", area, dd.Layout)
	dd.Open = false

	h := uitest.NewHarness(area)
	idx := -1
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		if i, ok := dd.Update(gtx); ok {
			idx = i
		}
		return dd.Layout(gtx, th)
	}
	h.Frame(w)
	h.Click(image.Pt(50, 14))
	h.Frame(w)
	if !dd.Open {
		t.Fatal("click did not open")
	}
	// Second option: field 28 + gap 4 + pad 4 + one row 24 + half a row.
	h.Click(image.Pt(50, 28+4+4+24+12))
	h.Frame(w)
	h.Frame(w)
	if idx != 1 || dd.Selected != 1 || dd.Open {
		t.Fatalf("selection: idx=%d selected=%d open=%v", idx, dd.Selected, dd.Open)
	}
	// Click outside closes without choosing.
	h.Click(image.Pt(50, 14))
	h.Frame(w)
	idx = -1
	h.Click(image.Pt(400, 300))
	h.Frame(w)
	if dd.Open || idx != -1 {
		t.Fatal("click outside")
	}
	dd.Disabled = true
	dd.Open = true
	h.Frame(w)
	if dd.Open {
		t.Fatal("disabled select stays open")
	}
	uitest.Smoke(t, "select disabled", area, dd.Layout)
	uitest.Smoke(t, "select empty unbounded", image.Pt(1<<24, 100), molecules.NewSelect("none").Layout)
}

func TestTable(t *testing.T) {
	tb := &molecules.Table{Columns: []molecules.TableColumn{
		{Title: "Host"}, {Title: "Remote", Weight: 2}, {Width: 28},
	}}
	cells := 0
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return tb.Layout(gtx, th, 3, func(gtx layout.Context, row, col int) layout.Dimensions {
			cells++
			return atoms.Text(fmt.Sprintf("%d/%d", row, col)).Layout(gtx, th)
		})
	}
	d := uitest.Smoke(t, "table", area, w)
	if d.Size.X != area.X || d.Size.Y != area.Y {
		t.Errorf("size %v", d.Size)
	}
	if cells == 0 {
		t.Fatal("no cells")
	}
	h := uitest.NewHarness(area)
	var ws []int
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		ws = tb.ColumnWidths(gtx, 328)
		return layout.Dimensions{Size: image.Pt(1, 1)}
	})
	if !slices.Equal(ws, []int{100, 200, 28}) {
		t.Fatalf("widths %v", ws)
	}
	uitest.Smoke(t, "table unbounded", image.Pt(400, 1<<24), w)
}
