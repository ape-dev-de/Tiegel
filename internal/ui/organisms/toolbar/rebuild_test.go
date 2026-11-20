package toolbar

import (
	"fmt"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

func manyBranches(n int) []gitcli.Branch {
	out := make([]gitcli.Branch, 0, n)
	for i := range n {
		out = append(out, gitcli.Branch{Name: fmt.Sprintf("feature/TICKET-%05d-some-work", i), Remote: i%2 == 1})
	}
	return out
}

// U3: closed popups have no items; the open one is built once and only
// rebuilt when its data or the query changes.
func TestPopupItemsBuiltOnlyWhenOpenAndChanged(t *testing.T) {
	b := sample(false)
	r := newRig(t, b, 1200)
	for _, p := range []*popup{&b.projectPop, &b.branchPop, &b.runPop} {
		if p.items != nil {
			t.Fatalf("closed popup has %d items", len(p.items))
		}
	}
	b.Show(PopupBranch)
	r.frame()
	if len(b.branchPop.items) == 0 || b.projectPop.items != nil || b.runPop.items != nil {
		t.Fatalf("items: branch %d project %d run %d", len(b.branchPop.items), len(b.projectPop.items), len(b.runPop.items))
	}
	first := &b.branchPop.items[0]
	r.frame()
	if &b.branchPop.items[0] != first {
		t.Fatal("items rebuilt without a data change")
	}
	for _, it := range b.branchPop.items {
		if it.lazy && it.sub != nil {
			t.Fatalf("submenu of %q built before it was opened", it.label)
		}
	}
	b.Branches = append(b.Branches, gitcli.Branch{Name: "hotfix"})
	r.frame()
	found := false
	for _, it := range b.branchPop.items {
		found = found || it.label == "hotfix"
	}
	if !found {
		t.Fatal("new branch not shown after data change")
	}
	b.Close()
	r.frame()
	if b.branchPop.items != nil {
		t.Fatal("closed popup keeps its items")
	}
}

// U3: a frame with an open branch popup does not allocate per branch
// once the items are built: 5000 branches cost what 500 do.
func TestBranchPopupFrameAllocations(t *testing.T) {
	frameAllocs := func(n int) float64 {
		b := sample(false)
		b.Branches = manyBranches(n)
		r := newRig(t, b, 1200)
		b.Show(PopupBranch)
		r.frame()
		r.frame()
		ic := b.icons()
		if a := testing.AllocsPerRun(20, func() { b.refresh(PopupBranch, ic) }); a > 0 {
			t.Fatalf("refresh with unchanged data allocates %.0f times", a)
		}
		return testing.AllocsPerRun(5, func() { r.frame() })
	}
	small, large := frameAllocs(500), frameAllocs(5000)
	if large-small > 50 {
		t.Fatalf("frame allocations grow with the branches: %.0f (500) vs %.0f (5000)", small, large)
	}
}

func TestContainsFold(t *testing.T) {
	for _, c := range []struct {
		s, sub string
		want   bool
	}{
		{"Feature/Login", "login", true}, {"main", "", true}, {"main", "dev", false},
		{"ÄRGER", "ärg", true}, {"ab", "abc", false},
	} {
		if got := containsFold(c.s, c.sub); got != c.want {
			t.Errorf("containsFold(%q, %q) = %v", c.s, c.sub, got)
		}
	}
}

func BenchmarkBranchPopupFrame(b *testing.B) {
	bar := sample(false)
	bar.Branches = manyBranches(5000)
	r := newRig(&testing.T{}, bar, 1200)
	bar.Show(PopupBranch)
	r.frame()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.frame()
	}
}
