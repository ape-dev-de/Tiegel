package git

import (
	"image"
	"reflect"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/ui/tokens"

	"github.com/ape-dev-de/tiegel/internal/ui/organisms/git/gitsample"
	"github.com/ape-dev-de/tiegel/internal/ui/uitest"
)

func TestBranchListSmoke(t *testing.T) {
	b := NewBranchList()
	uitest.Smoke(t, "branches empty", image.Pt(360, 480), b.Layout)
	b.SetBranches(gitsample.Branches())
	uitest.Smoke(t, "branches", image.Pt(360, 480), b.Layout)
	b.creating = true
	b.nameErr = "invalid"
	uitest.Smoke(t, "branches form", image.Pt(360, 480), b.Layout)
	b.SetQuery("zzz")
	uitest.Smoke(t, "branches no match", image.Pt(360, 480), b.Layout)
}

func TestBranchListFilter(t *testing.T) {
	b := NewBranchList()
	b.SetBranches(gitsample.Branches())
	if len(b.Visible()) != 7 {
		t.Fatalf("visible %v", b.Visible())
	}
	b.SetQuery("MAIN")
	if got := b.Visible(); !reflect.DeepEqual(got, []string{"main", "origin/main"}) {
		t.Fatalf("filter: %v", got)
	}
	// Section headers only for sections with matches.
	b.SetQuery("release")
	if !reflect.DeepEqual(b.view, []int{-2, 6}) {
		t.Fatalf("view %v", b.view)
	}
}

func TestBranchListActions(t *testing.T) {
	size := image.Pt(360, 480)
	h := uitest.NewHarness(size)
	b := NewBranchList()
	b.SetBranches(gitsample.Branches())
	frame(h, b)
	// Search field: 8 + 28 + 8 = 44; then "LOCAL" header (8 + text + 4),
	// then rows of 24dp. Row 0 is the current branch, row 1 "main".
	rowY := func(i int) int { return 44 + 8 + 13 + 4 + i*24 + 12 }
	h.Click(image.Pt(150, rowY(1)))
	frame(h, b)
	if sel := b.Selected(); sel == nil || sel.Name != "main" {
		t.Fatalf("selected %v", sel)
	}
	if b.checkout.Disabled || b.del.Disabled {
		t.Fatal("checkout/delete must be enabled for a local branch")
	}
	h.DoubleClick(image.Pt(150, rowY(1)))
	ev := frame(h, b)
	if len(ev) != 1 || ev[0] != (CheckoutRequested{Branch: "main"}) {
		t.Fatalf("double click: %v", ev)
	}
	// The current branch can be neither checked out nor deleted.
	h.Click(image.Pt(150, rowY(0)))
	frame(h, b)
	if !b.Selected().Current || !b.checkout.Disabled || !b.del.Disabled {
		t.Fatal("current branch actions")
	}

	// New branch from "main" via the form.
	b.selected = 1
	b.creating = true
	b.name.SetText("bad name")
	b.confirm.Disabled = false
	frame(h, b)
	b.submitForm(h)
	if b.nameErr == "" || len(b.q.events) != 0 {
		t.Fatal("invalid name accepted")
	}
	b.name.SetText("feature/x")
	b.submitForm(h)
	ev = b.q.drain()
	if len(ev) != 1 || ev[0] != (CreateBranchRequested{Name: "feature/x", Start: "main", Checkout: true}) || b.creating {
		t.Fatalf("create: %v", ev)
	}
}

// submitForm presses Enter in the name field.
func (b *BranchList) submitForm(h *uitest.Harness) {
	h.Frame(func(gtx layout.Context, th *tokens.Theme) layout.Dimensions {
		gtx.Execute(key.FocusCmd{Tag: &b.name.Editor})
		return b.Layout(gtx, th)
	})
	frame(h, b)
	h.Router.Queue(key.Event{Name: key.NameReturn, State: key.Press}, key.Event{Name: key.NameReturn, State: key.Release})
	h.Frame(b.Layout)
}

func TestValidBranchName(t *testing.T) {
	for _, n := range []string{"main", "feature/x-1", "release/1.4"} {
		if err := validBranchName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"", "-x", "a..b", "a b", "a~1", "x.lock", "a/", "/a", "a//b", "a@{1}", "a."} {
		if validBranchName(n) == nil {
			t.Errorf("%q accepted", n)
		}
	}
}
