package molecules_test

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/actions"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var menuArea = image.Pt(800, 600)

func sampleMenu() []molecules.MenuItem {
	return []molecules.MenuItem{
		{ID: "new", Label: "New", Glyph: atoms.GlyphAdd, Children: []molecules.MenuItem{
			{ID: "new.file", Label: "File…"},
			{ID: "new.dir", Label: "Directory…"},
			{ID: "new.class", Label: "PHP Class…", Disabled: true},
		}},
		{Separator: true},
		{ID: "rename", Label: "Rename…", Shortcut: "Shift+F6"},
		{ID: "delete", Label: "Delete…", Disabled: true},
		{ID: "wrap", Label: "Soft Wrap", Check: molecules.MenuCheckBox, Checked: true},
		{ID: "lf", Label: "LF", Check: molecules.MenuCheckRadio, Checked: true},
		{ID: "copy", Label: "Copy Path"},
	}
}

// menuHost lays out a context area filling the window and the menu on
// top, like a window would.
type menuHost struct {
	area   molecules.ContextArea
	menu   molecules.ContextMenu
	chosen []string
}

func (h *menuHost) layout(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
	if _, ok := h.area.Update(gtx); ok {
		h.menu.ShowAtPointer()
	}
	if id, ok := h.menu.Update(gtx); ok {
		h.chosen = append(h.chosen, id)
	}
	d := h.area.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	h.menu.Layout(gtx, th)
	return d
}

func press(h *uitest.Harness, p image.Point, buttons pointer.Buttons, mods key.Modifiers) {
	pos := f32.Pt(float32(p.X), float32(p.Y))
	t := time.Duration(len(p.String())) * time.Hour // distinct from Harness clicks
	h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos, Time: t},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: buttons, Modifiers: mods, Position: pos, Time: t},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Modifiers: mods, Position: pos, Time: t},
	)
}

func move(h *uitest.Harness, p image.Point) {
	h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(float32(p.X), float32(p.Y))})
}

func keys(h *uitest.Harness, names ...key.Name) {
	for _, n := range names {
		h.Router.Queue(key.Event{Name: n, State: key.Press})
	}
}

// rowCenter is the center of row i of a panel (4dp padding, 24dp rows,
// 9dp separators at index 1 of the sample menu).
func rowCenter(r image.Rectangle, i int) image.Point {
	y := r.Min.Y + 4 + 24*i + 12
	if i > 1 {
		y -= 24 - 9
	}
	return image.Pt(r.Min.X+40, y)
}

func TestContextMenuSmoke(t *testing.T) {
	m := &molecules.ContextMenu{Items: sampleMenu()}
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		m.Layout(gtx, th)
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	uitest.Smoke(t, "menu closed", menuArea, w)
	m.ShowAt(image.Pt(50, 50))
	uitest.Smoke(t, "menu open", menuArea, w)
	(&molecules.ContextMenu{}).ShowAt(image.Pt(1, 1)) // no items: harmless
}

func TestContextMenuMouse(t *testing.T) {
	h := uitest.NewHarness(menuArea)
	host := &menuHost{area: molecules.ContextArea{GOOS: "linux"}}
	host.menu.Items = sampleMenu()
	h.Frame(host.layout)
	press(h, image.Pt(100, 120), pointer.ButtonSecondary, 0)
	h.Frame(host.layout)
	h.Frame(host.layout)
	if !host.menu.Opened() {
		t.Fatal("right click did not open the menu")
	}
	ps := host.menu.Panels()
	if len(ps) != 1 || ps[0].Min != image.Pt(100, 120) || ps[0].Dx() < 180 {
		t.Fatalf("panel %v", ps)
	}
	// Hover opens the submenu to the right of its row.
	move(h, rowCenter(ps[0], 0))
	h.Frame(host.layout)
	h.Frame(host.layout)
	ps = host.menu.Panels()
	if len(ps) != 2 || ps[1].Min.X < ps[0].Max.X-8 || ps[1].Min.Y > ps[0].Min.Y {
		t.Fatalf("submenu panels %v", ps)
	}
	// A disabled item is not chosen.
	h.Click(image.Pt(ps[1].Min.X+40, ps[1].Min.Y+4+2*24+12))
	h.Frame(host.layout)
	if len(host.chosen) != 0 || !host.menu.Opened() {
		t.Fatalf("disabled chosen %v", host.chosen)
	}
	h.Click(image.Pt(ps[1].Min.X+40, ps[1].Min.Y+4+1*24+12))
	h.Frame(host.layout)
	h.Frame(host.layout)
	if len(host.chosen) != 1 || host.chosen[0] != "new.dir" || host.menu.Opened() {
		t.Fatalf("chosen %v open %v", host.chosen, host.menu.Opened())
	}
	// Reopen; hovering a plain row closes the submenu; clicking a
	// checked item reports it.
	press(h, image.Pt(100, 120), pointer.ButtonSecondary, 0)
	h.Frame(host.layout)
	h.Frame(host.layout)
	move(h, rowCenter(ps[0], 0))
	h.Frame(host.layout)
	h.Frame(host.layout)
	move(h, rowCenter(ps[0], 2))
	h.Frame(host.layout)
	h.Frame(host.layout)
	if len(host.menu.Panels()) != 1 {
		t.Fatal("submenu stays open")
	}
	h.Click(rowCenter(ps[0], 4))
	h.Frame(host.layout)
	if len(host.chosen) != 2 || host.chosen[1] != "wrap" {
		t.Fatalf("chosen %v", host.chosen)
	}
	// A click outside closes without choosing.
	press(h, image.Pt(100, 120), pointer.ButtonSecondary, 0)
	h.Frame(host.layout)
	h.Frame(host.layout)
	h.Click(image.Pt(700, 20))
	h.Frame(host.layout)
	if host.menu.Opened() || len(host.chosen) != 2 {
		t.Fatalf("outside click: open %v chosen %v", host.menu.Opened(), host.chosen)
	}
	// A primary click does not open it; Ctrl+click only on macOS.
	h.Click(image.Pt(100, 120))
	press(h, image.Pt(100, 120), pointer.ButtonPrimary, key.ModCtrl)
	h.Frame(host.layout)
	h.Frame(host.layout)
	if host.menu.Opened() {
		t.Fatal("opened by a primary click on linux")
	}
	host.area.GOOS = "darwin"
	press(h, image.Pt(100, 120), pointer.ButtonPrimary, key.ModCtrl)
	h.Frame(host.layout)
	h.Frame(host.layout)
	if !host.menu.Opened() {
		t.Fatal("Ctrl+click on macOS did not open")
	}
	host.menu.Close()
	h.Frame(host.layout)
	if host.menu.Opened() || len(host.menu.Panels()) != 0 {
		t.Fatal("Close")
	}
}

func TestContextMenuStaysInBounds(t *testing.T) {
	h := uitest.NewHarness(menuArea)
	m := &molecules.ContextMenu{Items: sampleMenu()}
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		m.Layout(gtx, th)
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	bounds := image.Rectangle{Max: menuArea}
	m.ShowAt(image.Pt(menuArea.X-10, menuArea.Y-10))
	h.Frame(w)
	ps := m.Panels()
	if len(ps) != 1 || !ps[0].In(bounds) || ps[0].Max.X != menuArea.X-10 || ps[0].Max.Y != menuArea.Y-10 {
		t.Fatalf("flipped panel %v", ps)
	}
	// Submenu opens to the left when there is no room on the right.
	keys(h, key.NameDownArrow)
	h.Frame(w)
	keys(h, key.NameRightArrow)
	h.Frame(w)
	h.Frame(w)
	ps = m.Panels()
	if len(ps) != 2 || !ps[1].In(bounds) || ps[1].Max.X > ps[0].Min.X+8 {
		t.Fatalf("submenu %v", ps)
	}
	// Explicit bounds.
	m.Bounds = image.Rect(100, 100, 400, 300)
	m.ShowAt(image.Pt(390, 290))
	h.Frame(w)
	if p := m.Panels()[0]; !p.In(m.Bounds) {
		t.Fatalf("explicit bounds %v", p)
	}
}

func TestContextMenuKeyboard(t *testing.T) {
	h := uitest.NewHarness(menuArea)
	m := &molecules.ContextMenu{Items: sampleMenu()}
	var chosen []string
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		if id, ok := m.Update(gtx); ok {
			chosen = append(chosen, id)
		}
		m.Layout(gtx, th)
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	m.ShowAt(image.Pt(10, 10))
	h.Frame(w)
	// Down skips the separator, Right opens the submenu at its first item,
	// Down skips the disabled one (wrapping), Enter chooses.
	keys(h, key.NameDownArrow, key.NameRightArrow, key.NameDownArrow, key.NameDownArrow, key.NameReturn)
	h.Frame(w)
	h.Frame(w)
	if len(chosen) != 1 || chosen[0] != "new.file" || m.Opened() {
		t.Fatalf("chosen %v open %v", chosen, m.Opened())
	}
	// Letter jump, Up wraps, Home/End, Left closes the submenu.
	m.ShowAt(image.Pt(10, 10))
	h.Frame(w)
	keys(h, "C", key.NameReturn)
	h.Frame(w)
	if chosen[len(chosen)-1] != "copy" {
		t.Fatalf("letter jump %v", chosen)
	}
	m.ShowAt(image.Pt(10, 10))
	h.Frame(w)
	keys(h, key.NameUpArrow, key.NameUpArrow, key.NameSpace)
	h.Frame(w)
	if chosen[len(chosen)-1] != "lf" {
		t.Fatalf("up wrap %v", chosen)
	}
	m.ShowAt(image.Pt(10, 10))
	h.Frame(w)
	keys(h, key.NameEnd, key.NameHome, key.NameReturn) // Enter on a submenu opens it
	h.Frame(w)
	h.Frame(w)
	if len(m.Panels()) != 2 {
		t.Fatalf("enter on submenu: %v", m.Panels())
	}
	keys(h, key.NameLeftArrow)
	h.Frame(w)
	h.Frame(w)
	if len(m.Panels()) != 1 || !m.Opened() {
		t.Fatal("left did not close the submenu")
	}
	keys(h, key.NameRightArrow, key.NameEscape)
	h.Frame(w)
	h.Frame(w)
	if len(m.Panels()) != 1 || !m.Opened() {
		t.Fatal("escape in submenu")
	}
	m.ShowAt(image.Pt(10, 10))
	h.Frame(w)
	keys(h, "D", key.NameReturn) // "D" selects nothing: Delete… is disabled
	h.Frame(w)
	if !m.Opened() || len(chosen) != 3 {
		t.Fatalf("disabled chosen %v", chosen)
	}
	keys(h, key.NameEscape)
	h.Frame(w)
	if m.Opened() {
		t.Fatal("escape did not close")
	}
	h.Frame(w)
}

func TestIsContextClick(t *testing.T) {
	sec := pointer.Event{Kind: pointer.Press, Buttons: pointer.ButtonSecondary}
	ctrl := pointer.Event{Kind: pointer.Press, Buttons: pointer.ButtonPrimary, Modifiers: key.ModCtrl}
	if !molecules.IsContextClick(sec, "linux") || !molecules.IsContextClick(ctrl, "darwin") || molecules.IsContextClick(ctrl, "windows") {
		t.Fatal("detection")
	}
	sec.Kind = pointer.Release
	if molecules.IsContextClick(sec, "linux") {
		t.Fatal("release")
	}
}

func TestMenuItemsFromEntries(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		r := actions.Standard(goos)
		for _, a := range r.All() {
			if a.Icon != "" && atoms.GlyphNamed(a.Icon) == nil {
				t.Errorf("action %s: unknown icon %q", a.ID, a.Icon)
			}
		}
		items := molecules.MenuItemsFromEntries(r.Resolve(actions.Context{Menu: actions.CtxProjectTree, Paths: []string{"a.php"}}, nil, goos))
		if items[0].Glyph != atoms.GlyphAdd || len(items[0].Children) == 0 || !items[1].Separator {
			t.Fatalf("items %+v", items[:2])
		}
		m := &molecules.ContextMenu{Items: items}
		m.ShowAt(image.Pt(5, 5))
		uitest.Smoke(t, "standard tree menu", menuArea, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			m.Layout(gtx, th)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
	}
	if atoms.GlyphNamed("") != nil || atoms.GlyphNamed("nope") != nil || atoms.GlyphNamed("delete") != atoms.GlyphDelete {
		t.Fatal("GlyphNamed")
	}
}
