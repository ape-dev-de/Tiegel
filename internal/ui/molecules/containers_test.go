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

func TestScrollList(t *testing.T) {
	var s molecules.ScrollList
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return s.Layout(gtx, th, 100, func(gtx layout.Context, i int) layout.Dimensions {
			return atoms.Text(fmt.Sprintf("row %d", i)).Layout(gtx, th)
		})
	}
	d := uitest.Smoke(t, "scrolllist", image.Pt(200, 120), w)
	if d.Size.Y != 120 {
		t.Errorf("list does not fill height: %v", d.Size)
	}
	s.ScrollTo(50)
	uitest.Smoke(t, "scrolled", image.Pt(200, 120), w)
	if s.List.Position.First != 50 {
		t.Errorf("first = %d", s.List.Position.First)
	}
	// Unbounded height lays out everything.
	uitest.Smoke(t, "unbounded", image.Pt(200, 1<<24), w)
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

func TestDropdown(t *testing.T) {
	dd := molecules.NewDropdown("Choose interpreter",
		molecules.DropdownOption{Label: "php 8.3", Detail: "/usr/bin/php", Glyph: atoms.GlyphComputer, Badge: "8.3"},
		molecules.DropdownOption{Label: "compose:php", Detail: "php:8.2-fpm", Glyph: atoms.GlyphContainer, Badge: "★", BadgeKind: atoms.BadgeAccent},
	)
	d := uitest.Smoke(t, "dropdown", area, dd.Layout)
	if d.Size != image.Pt(area.X, 28) {
		t.Errorf("size %v", d.Size)
	}
	dd.SetOpen(true)
	uitest.Smoke(t, "dropdown open", area, dd.Layout)
	dd.SetOpen(false)

	h := uitest.NewHarness(area)
	changed := false
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		changed = dd.Update(gtx) || changed
		return dd.Layout(gtx, th)
	}
	h.Frame(w)
	h.Click(image.Pt(50, 14))
	h.Frame(w)
	if !dd.IsOpen() {
		t.Fatal("click did not open")
	}
	// Second option: field 28 + gap 2 + pad 4 + one row (24+4) + half a row.
	h.Click(image.Pt(50, 28+2+4+28+14))
	h.Frame(w)
	if !changed || dd.Selected != 1 || dd.IsOpen() {
		t.Fatalf("selection: changed=%v selected=%d open=%v", changed, dd.Selected, dd.IsOpen())
	}
	// Click outside closes without changing.
	h.Click(image.Pt(50, 14))
	h.Frame(w)
	changed = false
	h.Click(image.Pt(400, 300))
	h.Frame(w)
	if dd.IsOpen() || changed {
		t.Fatal("click outside")
	}
	dd.Disabled = true
	dd.SetOpen(true)
	if dd.IsOpen() {
		t.Fatal("disabled dropdown opened")
	}
	uitest.Smoke(t, "dropdown disabled", area, dd.Layout)
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

func TestSplitPane(t *testing.T) {
	sp := &molecules.SplitPane{}
	var a, b image.Point
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return sp.Layout(gtx, th,
			func(gtx layout.Context) layout.Dimensions { a = gtx.Constraints.Max; return layout.Dimensions{Size: a} },
			func(gtx layout.Context) layout.Dimensions { b = gtx.Constraints.Max; return layout.Dimensions{Size: b} })
	}
	d := uitest.Smoke(t, "split", image.Pt(401, 200), w)
	if d.Size != image.Pt(401, 200) || a.X != 200 || b.X != 200 || a.Y != 200 {
		t.Fatalf("horizontal: %v %v %v", d.Size, a, b)
	}
	h := uitest.NewHarness(image.Pt(401, 200))
	h.Frame(w)
	h.Drag(image.Pt(200, 100), image.Pt(300, 100))
	h.Frame(w)
	if sp.Ratio < 0.7 || a.X < 280 {
		t.Fatalf("drag: ratio %v, first %v", sp.Ratio, a)
	}
	// Dragging beyond the end clamps to MinSize.
	h.Drag(image.Pt(a.X, 100), image.Pt(399, 100))
	h.Frame(w)
	h.Frame(w)
	if b.X < 48 {
		t.Fatalf("second pane below minimum: %v", b)
	}
	v := &molecules.SplitPane{Axis: layout.Vertical, Ratio: 0.25}
	uitest.Smoke(t, "vertical", image.Pt(300, 401), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return v.Layout(gtx, th,
			func(gtx layout.Context) layout.Dimensions { a = gtx.Constraints.Max; return layout.Dimensions{Size: a} },
			func(gtx layout.Context) layout.Dimensions { b = gtx.Constraints.Max; return layout.Dimensions{Size: b} })
	})
	if a.Y != 100 || b.Y != 300 {
		t.Fatalf("vertical: %v %v", a, b)
	}
	if v.Dragging() {
		t.Fatal("dragging")
	}
}
