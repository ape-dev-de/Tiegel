package git

import (
	"bytes"
	"image"
	"strings"
	"testing"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/molecules"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/git/gitsample"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

const mergePath = "src/Billing/PriceCalculator.php"

func newSampleMerge() *MergeView {
	v := NewMergeView()
	v.SetMerge(mergePath, gitsample.Merge(), gitsample.MergeLabels)
	return v
}

func TestMergeViewSmoke(t *testing.T) {
	v := NewMergeView()
	uitest.Smoke(t, "merge empty", area, v.Layout)
	v = newSampleMerge()
	uitest.Smoke(t, "merge", area, v.Layout)
	v.blocks[v.firstConflict()].editing = true
	v.dirty = true
	uitest.Smoke(t, "merge editing", area, v.Layout)
	uitest.Smoke(t, "merge narrow", image.Pt(420, 300), v.Layout)
}

func (v *MergeView) firstConflict() int {
	for i, b := range v.blocks {
		if b.Kind == gitcli.MergeConflict {
			return i
		}
	}
	return -1
}

func TestMergeViewResolveAndApply(t *testing.T) {
	h := uitest.NewHarness(area)
	src := gitsample.Merge()
	v := NewMergeView()
	v.SetMerge(mergePath, src, gitsample.MergeLabels)
	n := v.Conflicts()
	if n == 0 || v.Unresolved() != n {
		t.Fatalf("conflicts %d unresolved %d", n, v.Unresolved())
	}
	frame(h, v)
	if !v.apply.Disabled || v.markers.Disabled {
		t.Fatal("apply must wait for all conflicts; markers must be possible")
	}
	for i := 0; i < n; i++ {
		if !v.Resolve(i, gitcli.TakeTheirs, nil) {
			t.Fatalf("resolve %d", i)
		}
	}
	if v.Unresolved() != 0 {
		t.Fatal("still unresolved")
	}
	// The source result is not modified.
	if src.Unresolved() != n {
		t.Error("SetMerge must copy the blocks")
	}
	frame(h, v)
	if v.apply.Disabled || !v.markers.Disabled {
		t.Fatal("apply must be enabled when resolved")
	}
	want := v.Result().Render(gitsample.MergeLabels)
	if bytes.Contains(want, []byte("<<<<<<<")) {
		t.Fatal("resolved result has markers")
	}
	if v.Resolve(99, gitcli.TakeOurs, nil) {
		t.Error("resolve out of range")
	}
}

// buttonCenter returns the center of a toolbar button. The toolbar ends
// with Cancel, Apply with markers and Apply, right aligned with an 8dp
// inset and 4dp gaps.
func (v *MergeView) buttonCenter(h *uitest.Harness, which *atoms.Button) image.Point {
	width := func(b *atoms.Button) int {
		d, _ := uitest.Layout(h.Theme, area, func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
			c := *b
			return c.Layout(gtx, th)
		})
		return d.Size.X
	}
	x := area.X - 8
	for _, b := range []*atoms.Button{&v.apply, &v.markers, &v.cancel} {
		w := width(b)
		if b == which {
			return image.Pt(x-w/2, 18)
		}
		x -= w + 4
	}
	return image.Point{}
}

func TestMergeViewButtons(t *testing.T) {
	h := uitest.NewHarness(area)
	v := newSampleMerge()
	frame(h, v)
	// The whole sample fits: the first conflict header follows heads[0]
	// code lines. Toolbar 28 + 2*4, divider, titles (4 + small + 4),
	// divider.
	lineH, _ := uitest.Layout(h.Theme, area, molecules.CodeLine{Text: "x"}.Layout)
	title, _ := uitest.Layout(h.Theme, area, atoms.Small("Ours").Layout)
	top := 36 + 1 + 4 + title.Size.Y + 4 + 1
	colW := (area.X - 2) / 3
	y := top + v.heads[0]*lineH.Size.Y + 16
	// "Take ours" sits at the right end of the ours column.
	h.Click(image.Pt(colW-30, y))
	frame(h, v)
	frame(h, v)
	b := v.blocks[v.firstConflict()]
	if b.Resolution != gitcli.TakeOurs {
		t.Fatalf("take ours: resolution %v", b.Resolution)
	}
	// "Take theirs" sits at the left of the theirs column.
	h.Click(image.Pt(2*colW+2+30, y))
	frame(h, v)
	frame(h, v)
	if b.Resolution != gitcli.TakeTheirs {
		t.Fatalf("take theirs: resolution %v", b.Resolution)
	}
	// Reset is the last button of the result column.
	h.Click(image.Pt(2*colW-20, y))
	frame(h, v)
	frame(h, v)
	if b.Resolution != gitcli.Unresolved {
		t.Fatalf("reset: resolution %v", b.Resolution)
	}
}

func TestMergeViewApplyEvents(t *testing.T) {
	h := uitest.NewHarness(area)
	v := newSampleMerge()
	frame(h, v)
	// Apply with markers deliberately.
	h.Click(v.buttonCenter(h, &v.markers))
	ev := frame(h, v)
	if len(ev) != 1 {
		t.Fatalf("markers: %v", ev)
	}
	ap := ev[0].(MergeApplied)
	if ap.Path != mergePath || ap.Unresolved != v.Conflicts() || !bytes.Contains(ap.Content, []byte("<<<<<<< feature/invoices")) {
		t.Fatalf("applied with markers: %+v", ap)
	}
	// Resolve all with "both" and a custom edit, then apply.
	v.Resolve(0, gitcli.TakeCustom, []byte("    // merged by hand\n"))
	for i := 1; i < v.Conflicts(); i++ {
		v.Resolve(i, gitcli.TakeOursFirst, nil)
	}
	frame(h, v)
	h.Click(v.buttonCenter(h, &v.apply))
	ev = frame(h, v)
	if len(ev) != 1 {
		t.Fatalf("apply: %v", ev)
	}
	ap = ev[0].(MergeApplied)
	if ap.Unresolved != 0 || !strings.Contains(string(ap.Content), "// merged by hand\n") || bytes.Contains(ap.Content, []byte("<<<<<<<")) {
		t.Fatalf("applied: %q", ap.Content)
	}
	h.Click(v.buttonCenter(h, &v.cancel))
	if ev := frame(h, v); len(ev) != 1 || ev[0] != (MergeCancelled{Path: mergePath}) {
		t.Fatalf("cancel: %v", ev)
	}
}

func TestMergeViewEditDone(t *testing.T) {
	h := uitest.NewHarness(area)
	v := newSampleMerge()
	b := v.blocks[v.firstConflict()]
	b.editing = true
	b.editor.SetText("custom line")
	v.dirty = true
	frame(h, v)
	if len(v.rows) == 0 || !v.rows[v.heads[0]+1].edit {
		t.Fatal("edit row missing")
	}
	b.Custom = v.editedText(b)
	b.Resolution = gitcli.TakeCustom
	if lines, ok := b.Merged(); !ok || len(lines) != 1 || lines[0] != "custom line\n" {
		t.Fatalf("edited: %q %v", lines, ok)
	}
}
