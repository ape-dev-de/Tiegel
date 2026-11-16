package molecules_test

import (
	"image"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

var area = image.Pt(480, 400)

func TestSearchField(t *testing.T) {
	s := molecules.NewSearchField("Search files")
	empty := uitest.Smoke(t, "search empty", area, s.Layout)
	s.SetText("Controller")
	full := uitest.Smoke(t, "search text", area, s.Layout)
	if empty.Size != full.Size || full.Size.X != area.X {
		t.Errorf("sizes differ: %v vs %v", empty.Size, full.Size)
	}
	if s.Text() != "Controller" {
		t.Fatal("Text")
	}
}

func TestSearchFieldClear(t *testing.T) {
	h := uitest.NewHarness(area)
	s := molecules.NewSearchField("Search")
	s.SetText("abc")
	h.Frame(s.Layout)
	// The clear button sits at the right end, vertically centered.
	h.Click(image.Pt(area.X-14, 14))
	var changed bool
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		changed, _ = s.Update(gtx)
		return s.Layout(gtx, th)
	})
	if !changed || s.Text() != "" {
		t.Fatalf("clear: changed=%v text=%q", changed, s.Text())
	}
}

func TestFileRow(t *testing.T) {
	for _, st := range molecules.FileStatuses {
		r := &molecules.FileRow{Name: "UserController.php", Depth: 2, Status: st, Detail: "src/Http"}
		d := uitest.Smoke(t, "filerow "+st.String(), area, r.Layout)
		if d.Size != image.Pt(area.X, 24) {
			t.Errorf("file row size %v", d.Size)
		}
	}
	th := tokens.Dark(nil)
	if molecules.StatusNone.Color(th) != th.Color.Text || molecules.StatusModified.Color(th) != th.Git.FileModified {
		t.Error("status colors")
	}
	dir := &molecules.FileRow{Name: "src", Dir: true, Expanded: true, Selected: true}
	uitest.Smoke(t, "dir", area, dir.Layout)
	deep := &molecules.FileRow{Name: "deep.php", Depth: 40}
	uitest.Smoke(t, "deep", image.Pt(200, 100), deep.Layout)
}

func TestFileRowToggle(t *testing.T) {
	h := uitest.NewHarness(area)
	r := &molecules.FileRow{Name: "src", Dir: true}
	h.Frame(r.Layout)
	// Arrow sits after the 4dp indent.
	h.Click(image.Pt(10, 12))
	var clicked, toggled bool
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		clicked, toggled = r.Update(gtx)
		return r.Layout(gtx, th)
	})
	if !toggled || !r.Expanded {
		t.Fatalf("toggle: toggled=%v expanded=%v", toggled, r.Expanded)
	}
	h.Click(image.Pt(200, 12))
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		clicked, toggled = r.Update(gtx)
		return r.Layout(gtx, th)
	})
	if !clicked || toggled {
		t.Fatalf("row click: clicked=%v toggled=%v", clicked, toggled)
	}
	h.DoubleClick(image.Pt(200, 12))
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		clicked, toggled = r.Update(gtx)
		return r.Layout(gtx, th)
	})
	if !toggled || r.Expanded {
		t.Fatalf("double click: toggled=%v expanded=%v", toggled, r.Expanded)
	}
}

func TestTab(t *testing.T) {
	var widths []int
	for _, c := range []struct{ active, modified bool }{{false, false}, {true, false}, {true, true}, {false, true}} {
		tab := molecules.NewTab("index.php")
		tab.Active, tab.Modified = c.active, c.modified
		tab.Glyph = atoms.GlyphFileCode
		d := uitest.Smoke(t, "tab", area, tab.Layout)
		if d.Size.Y != 32 {
			t.Errorf("tab height %d", d.Size.Y)
		}
		widths = append(widths, d.Size.X)
	}
	for _, w := range widths[1:] {
		if w != widths[0] {
			t.Errorf("tab width changes with state: %v", widths)
		}
	}
	long := molecules.NewTab("AVeryLongFileNameThatShouldBeTruncatedInTheTabBar_AndThenSome.php")
	if d := uitest.Smoke(t, "long tab", area, long.Layout); d.Size.X > 240 {
		t.Errorf("long tab width %d > 240", d.Size.X)
	}
}

func TestTabClose(t *testing.T) {
	h := uitest.NewHarness(area)
	tab := molecules.NewTab("index.php")
	tab.Active = true
	d := h.Frame(tab.Layout)
	h.Click(image.Pt(d.Size.X-12, d.Size.Y/2))
	var selected, closed bool
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		selected, closed = tab.Update(gtx)
		return tab.Layout(gtx, th)
	})
	if !closed {
		t.Fatalf("close not registered (selected=%v)", selected)
	}
}

func TestBreakpointRow(t *testing.T) {
	b := molecules.NewBreakpointRow("src/Service/Mailer.php", 42, "$retries > 3")
	d := uitest.Smoke(t, "breakpoint", area, b.Layout)
	if d.Size != image.Pt(area.X, 24) {
		t.Errorf("size %v", d.Size)
	}
	if b.Location() != "src/Service/Mailer.php:42" || !b.IsEnabled() {
		t.Error("location/enabled")
	}
	b.Enabled.State = atoms.Unchecked
	b.Condition = ""
	b.Selected = true
	uitest.Smoke(t, "breakpoint disabled", area, b.Layout)

	h := uitest.NewHarness(area)
	b = molecules.NewBreakpointRow("a.php", 1, "")
	h.Frame(b.Layout)
	h.Click(image.Pt(14, 12)) // checkbox
	h.Frame(b.Layout)
	if b.IsEnabled() {
		t.Error("checkbox click did not disable the breakpoint")
	}
}

func hunk() *molecules.DiffBlock {
	return &molecules.DiffBlock{
		Header: "@@ -10,4 +10,5 @@ public function handle()",
		Lines: []molecules.DiffLine{
			{Kind: molecules.DiffContext, OldNo: 10, NewNo: 10, Text: "    $user = $this->repo->find($id);"},
			{Kind: molecules.DiffRemoved, OldNo: 11, Text: "    return $user;"},
			{Kind: molecules.DiffAdded, NewNo: 11, Text: "    if ($user === null) {"},
			{Kind: molecules.DiffAdded, NewNo: 12, Text: "        throw new NotFound($id);"},
			{Kind: molecules.DiffContext, OldNo: 12, NewNo: 13, Text: "}"},
		},
	}
}

func TestDiffBlock(t *testing.T) {
	b := hunk()
	d := uitest.Smoke(t, "diff", area, b.Layout)
	if d.Size.X != area.X {
		t.Errorf("width %d", d.Size.X)
	}
	rows := len(b.Lines) + 1
	if d.Size.Y < rows*14 {
		t.Errorf("height %d too small for %d rows", d.Size.Y, rows)
	}
	b.Selectable = true
	b.Include.State = atoms.Indeterminate
	uitest.Smoke(t, "diff selectable", area, b.Layout)
	uitest.Smoke(t, "diff empty", area, (&molecules.DiffBlock{Header: "@@"}).Layout)
	if molecules.DiffAdded.Marker() != "+" || molecules.DiffRemoved.Marker() != "-" || molecules.DiffContext.Marker() != " " {
		t.Error("markers")
	}
}

func TestRenderHeadless(t *testing.T) {
	th := tokens.Dark(nil)
	b := hunk()
	img, err := uitest.Render(th, image.Pt(480, 160), b.Layout)
	if err != nil {
		t.Skipf("headless rendering unavailable: %v", err)
	}
	// Some pixel of the added line background must be present.
	want := th.Editor.DiffAddedBg
	found := false
	bounds := img.Bounds()
	for y := 0; y < bounds.Max.Y && !found; y++ {
		px := img.RGBAAt(bounds.Max.X-5, y)
		found = px.R == want.R && px.G == want.G && px.B == want.B
	}
	if !found {
		t.Error("added-line background not rendered")
	}
}
