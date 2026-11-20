package diff_test

import (
	"fmt"
	"image"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/diff"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestDiffWindowFileNav(t *testing.T) {
	w, _ := sampleWindow(t, false)
	w.SetFileNav(diff.FileNav{Index: 1, Total: 3, Prev: "a.php", Next: "c.php"})
	if w.FileNav().Total != 3 {
		t.Fatal("nav not kept")
	}
	uitest.Smoke(t, "file nav", area, w.Layout)
	h := uitest.NewHarness(area)
	frame(h, w)
	// The file buttons are the first two toolbar buttons (32dp each after
	// a 4dp inset).
	var deltas []int
	for _, x := range []int{4 + 16, 4 + 32 + 16} {
		h.Click(image.Pt(x, 16))
		for _, e := range frame(h, w) {
			if n, ok := e.(diff.FileNavRequested); ok {
				deltas = append(deltas, n.Delta)
			}
		}
	}
	if fmt.Sprint(deltas) != "[-1 1]" {
		t.Fatalf("deltas %v", deltas)
	}
	// A single file disables the buttons; a file outside the set shows "–".
	w.SetFileNav(diff.FileNav{Index: 0, Total: 1})
	h.Click(image.Pt(4+16, 16))
	for _, e := range frame(h, w) {
		if _, ok := e.(diff.FileNavRequested); ok {
			t.Fatal("disabled button emitted")
		}
	}
	w.SetFileNav(diff.FileNav{Index: -1, Total: 2})
	uitest.Smoke(t, "file nav outside", area, w.Layout)
	w.SetFileNav(diff.FileNav{})
	h.Click(image.Pt(4+16, 16))
	for _, e := range frame(h, w) {
		if _, ok := e.(diff.FileNavRequested); ok {
			t.Fatal("hidden nav emitted")
		}
	}
}
