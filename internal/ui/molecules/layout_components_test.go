package molecules_test

import (
	"image"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestCodeLine(t *testing.T) {
	th := tokens.Dark(nil)
	seen := map[string]bool{}
	var h int
	for _, tone := range molecules.LineTones {
		bg, _ := tone.Colors(th)
		if bg.A == 0 || seen[tone.String()] {
			t.Errorf("tone %s", tone)
		}
		seen[tone.String()] = true
		for cols := 0; cols <= 2; cols++ {
			l := molecules.CodeLine{No: 12, No2: 130, Columns: cols, Digits: 4, Marker: "+",
				Text: "    $user = $this->users->find($id);", Tone: tone,
				Spans: []molecules.Span{{Start: 4, End: 9}, {Start: 30, End: 999}, {Start: 5, End: 2}}}
			d := uitest.Smoke(t, "codeline "+tone.String(), area, l.Layout)
			if d.Size.X != area.X {
				t.Errorf("code line must fill the width: %v", d.Size)
			}
			if h == 0 {
				h = d.Size.Y
			} else if d.Size.Y != h {
				t.Errorf("rows must have equal height: %d vs %d", d.Size.Y, h)
			}
		}
	}
	empty := uitest.Smoke(t, "codeline empty", area, molecules.CodeLine{Tone: molecules.ToneFiller}.Layout)
	if empty.Size.Y != h {
		t.Errorf("empty line height %d, want %d", empty.Size.Y, h)
	}
	uitest.Smoke(t, "codeline narrow", image.Pt(40, 20), molecules.CodeLine{Columns: 2, Text: "long text", Spans: []molecules.Span{{0, 4}}}.Layout)
	var got int
	uitest.Layout(th, area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		got = molecules.CodeLineHeight(gtx, th)
		return layout.Dimensions{Size: image.Pt(1, 1)}
	})
	if got != h {
		t.Errorf("CodeLineHeight %d, want %d", got, h)
	}
}

// TestCodeLineFollowsFonts checks that the cached code metrics follow
// the editor font size and family of the theme.
func TestCodeLineFollowsFonts(t *testing.T) {
	base := tokens.Dark(nil)
	height := func(th *tokens.Theme) (h int) {
		uitest.Layout(th, area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			h = molecules.CodeLineHeight(gtx, th)
			return layout.Dimensions{Size: image.Pt(1, 1)}
		})
		return h
	}
	small := height(base.WithFonts(tokens.FontPrefs{EditorSize: 10}))
	big := height(base.WithFonts(tokens.FontPrefs{EditorSize: 24}))
	if big <= small {
		t.Fatalf("code line height must grow with the font size: %d vs %d", small, big)
	}
	// Same size, other family (cached separately; falls back to Go Mono
	// with the bundled shaper, so the height is the same).
	if other := height(base.WithFonts(tokens.FontPrefs{EditorFamily: "Menlo", EditorSize: 10})); other != small {
		t.Errorf("family fallback height %d, want %d", other, small)
	}
}

func TestListRow(t *testing.T) {
	r := &molecules.ListRow{Selected: true, Indent: 16}
	d := uitest.Smoke(t, "listrow", area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return r.Layout(gtx, th, atoms.Text("row").Fn(th))
	})
	if d.Size != image.Pt(area.X, 24) {
		t.Errorf("row size %v", d.Size)
	}
	h := uitest.NewHarness(area)
	r.Selected = false
	var clicked, double bool
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		c, dbl := r.Update(gtx)
		clicked, double = clicked || c, double || dbl
		return r.Layout(gtx, th, atoms.Text("row").Fn(th))
	}
	h.Frame(w)
	h.Click(image.Pt(100, 12))
	h.Frame(w)
	if !clicked || double {
		t.Fatalf("click: %v %v", clicked, double)
	}
	h.DoubleClick(image.Pt(100, 12))
	h.Frame(w)
	if !double {
		t.Fatal("double click")
	}
	_ = r.Hovered()
}

func TestBanner(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range molecules.BannerKinds {
		if seen[k.String()] {
			t.Errorf("duplicate kind name %s", k)
		}
		seen[k.String()] = true
		b := molecules.Banner{Kind: k, Title: "Push rejected", Detail: "The remote has new commits. Pull first, then push again."}
		d := uitest.Smoke(t, "banner "+k.String(), area, b.Layout)
		if d.Size.X != area.X {
			t.Errorf("banner width %v", d.Size)
		}
	}
	uitest.Smoke(t, "banner title only", image.Pt(120, 200), molecules.Banner{Title: "x"}.Layout)
}

func TestScrollList(t *testing.T) {
	var s molecules.ScrollList
	drawn := 0
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		drawn = 0
		return s.Layout(gtx, th, 100_000, func(gtx layout.Context, i int) layout.Dimensions {
			drawn++
			return atoms.Text("row").Layout(gtx, th)
		})
	}
	d := uitest.Smoke(t, "scrolllist", area, w)
	if d.Size != area {
		t.Errorf("size %v", d.Size)
	}
	if drawn == 0 || drawn > 60 {
		t.Errorf("laid out %d rows, want only the visible ones", drawn)
	}
	h := uitest.NewHarness(area)
	h.Frame(w)
	if s.FirstVisible() != 0 || s.LastVisible() < 10 {
		t.Errorf("visible %d..%d", s.FirstVisible(), s.LastVisible())
	}
	s.ScrollTo(50_000)
	h.Frame(w)
	if s.FirstVisible() != 50_000 || s.Position().First != 50_000 {
		t.Errorf("ScrollTo: first %d", s.FirstVisible())
	}
	// Clicking the scrollbar track near the bottom scrolls forward.
	h.Click(image.Pt(area.X-4, area.Y-4))
	h.Frame(w)
	h.Frame(w)
	if s.FirstVisible() <= 50_000 {
		t.Errorf("track click did not scroll: %d", s.FirstVisible())
	}
	var empty molecules.ScrollList
	if empty.LastVisible() != -1 {
		t.Error("LastVisible before layout")
	}
	uitest.Smoke(t, "scrolllist unbounded", image.Pt(300, 1<<24), func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return empty.Layout(gtx, th, 0, nil)
	})
}

func TestSplit(t *testing.T) {
	for _, vertical := range []bool{false, true} {
		s := &molecules.Split{Vertical: vertical}
		var a, b image.Point
		w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			return s.Layout(gtx, th, func(gtx layout.Context) layout.Dimensions {
				a = gtx.Constraints.Max
				return layout.Dimensions{Size: a}
			}, func(gtx layout.Context) layout.Dimensions {
				b = gtx.Constraints.Max
				return layout.Dimensions{Size: b}
			})
		}
		d := uitest.Smoke(t, "split", area, w)
		if d.Size != area || s.Ratio != 0.5 {
			t.Fatalf("size %v ratio %v", d.Size, s.Ratio)
		}
		if vertical && (a.X != area.X || a.Y+b.Y+1 != area.Y) {
			t.Errorf("vertical panes %v %v", a, b)
		}
		if !vertical && (a.Y != area.Y || a.X+b.X+1 != area.X) {
			t.Errorf("horizontal panes %v %v", a, b)
		}
		// Extreme ratios are clamped by MinPane.
		s.Ratio, s.MinPane = 0.01, 100
		uitest.Smoke(t, "split min", area, w)
		if (vertical && a.Y != 100) || (!vertical && a.X != 100) {
			t.Errorf("min pane: %v", a)
		}
	}
	// Dragging the divider changes the ratio.
	s := &molecules.Split{}
	h := uitest.NewHarness(area)
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		return s.Layout(gtx, th, layout.Spacer{}.Layout, layout.Spacer{}.Layout)
	}
	h.Frame(w)
	mid := area.X / 2
	drag(h, image.Pt(mid, 100), image.Pt(mid+120, 100))
	h.Frame(w)
	if s.Ratio < 0.55 {
		t.Errorf("drag: ratio %v", s.Ratio)
	}
}

func TestDropdown(t *testing.T) {
	d := &molecules.Dropdown{Glyph: atoms.GlyphHistory, Description: "Recent", Items: []string{"Fix rounding", "Add invoices\n\nbody"}}
	uitest.Smoke(t, "dropdown closed", area, d.Layout)
	d.Open = true
	uitest.Smoke(t, "dropdown open", area, d.Layout)
	d.Up = true
	uitest.Smoke(t, "dropdown up", area, d.Layout)

	h := uitest.NewHarness(area)
	d = &molecules.Dropdown{Items: []string{"one", "two", "three"}}
	var idx = -1
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		if i, ok := d.Update(gtx); ok {
			idx = i
		}
		return d.Layout(gtx, th)
	}
	h.Frame(w)
	h.Click(image.Pt(14, 14)) // the button
	h.Frame(w)
	if !d.Open {
		t.Fatal("button did not open the popup")
	}
	h.Frame(w)
	// Popup starts below the 28dp button + 4dp gap, 4dp padding, 24dp rows.
	h.Click(image.Pt(60, 28+4+4+24+12))
	h.Frame(w)
	h.Frame(w)
	if idx != 1 || d.Open {
		t.Fatalf("choose: idx=%d open=%v", idx, d.Open)
	}
	// A click outside closes without choosing.
	d.Open = true
	idx = -1
	h.Frame(w)
	h.Click(image.Pt(area.X-10, area.Y-10))
	h.Frame(w)
	if d.Open || idx != -1 {
		t.Fatalf("outside click: open=%v idx=%d", d.Open, idx)
	}
	// Without items the button is disabled.
	d.Items = nil
	d.Open = true
	h.Frame(w)
	if d.Open {
		t.Error("empty dropdown stays open")
	}
}

// drag queues a primary-button drag from a to b in a few steps.
func drag(h *uitest.Harness, a, b image.Point) {
	pt := func(p image.Point) f32.Point { return f32.Pt(float32(p.X), float32(p.Y)) }
	h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pt(a)},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pt(a)},
	)
	for i := 1; i <= 4; i++ {
		p := a.Add(b.Sub(a).Mul(i).Div(4))
		h.Router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pt(p)})
	}
	h.Router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pt(b)})
}

func TestScrollListHorizontal(t *testing.T) {
	view := image.Pt(200, 200)
	s := molecules.ScrollList{Horizontal: true}
	rows := make([]*molecules.FileRow, 20)
	for i := range rows {
		rows[i] = &molecules.FileRow{Name: "short.php"}
	}
	rows[3].Name = strings.Repeat("VeryLongDirectoryName", 8) + ".php"
	rows[3].Selected = true
	var mins []int
	w := func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		mins = mins[:0]
		return s.Layout(gtx, th, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
			if !molecules.Wide(gtx) {
				t.Fatalf("row %d not wide: %v", i, gtx.Constraints)
			}
			mins = append(mins, gtx.Constraints.Min.X)
			return rows[i].Layout(gtx, th)
		})
	}
	h := uitest.NewHarness(view)
	if d := h.Frame(w); d.Size != view {
		t.Fatalf("size %v", d.Size)
	}
	h.Frame(w)
	if s.XOffset() != 0 || mins[0] != view.X {
		t.Fatalf("offset %d, row min %d", s.XOffset(), mins[0])
	}
	// A horizontal wheel/trackpad scroll moves the content.
	h.Router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(50, 50)},
		pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(50, 50), Scroll: f32.Pt(120, 0)},
	)
	h.Frame(w)
	h.Frame(w)
	if s.XOffset() <= 0 {
		t.Fatalf("horizontal scroll ignored: offset %d", s.XOffset())
	}
	// Rows fill the visible extent while scrolled.
	if mins[0] != view.X+s.XOffset() {
		t.Errorf("row min %d with offset %d", mins[0], s.XOffset())
	}
	// Scrolling far right clamps to the widest row.
	for range 50 {
		h.Router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(50, 50), Scroll: f32.Pt(500, 0)})
		h.Frame(w)
	}
	far := s.XOffset()
	rows[3].Name = "short.php"
	// Without the wide row the range shrinks once scrolled back.
	for range 50 {
		h.Router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(50, 50), Scroll: f32.Pt(-500, 0)})
		h.Frame(w)
	}
	h.Frame(w)
	if far <= 0 || s.XOffset() != 0 {
		t.Errorf("far %d, back %d", far, s.XOffset())
	}
	// Dragging the horizontal bar works too (no panic, offset in range).
	rows[3].Name = strings.Repeat("x", 300)
	h.Frame(w)
	h.Frame(w)
	h.Drag(image.Pt(10, view.Y-4), image.Pt(150, view.Y-4))
	h.Frame(w)
	h.Frame(w)
	if s.XOffset() <= 0 {
		t.Errorf("bar drag: offset %d", s.XOffset())
	}
}
