package diffsample

import (
	"strings"
	"testing"
)

func TestSamples(t *testing.T) {
	c := Content()
	if len(c.Blocks) < 4 || c.Left.Text != Old || c.Right.Text != New {
		t.Fatalf("content %+v", c.Blocks)
	}
	l, r := Large(1000, 40)
	if strings.Count(l, "\n") != 1000 || l == r {
		t.Fatal("large")
	}
	commits, hashes := Blame()
	if len(hashes) != strings.Count(New, "\n") || len(commits) != 4 {
		t.Fatal("blame")
	}
	seen := map[string]bool{}
	for _, h := range History(0, 50) {
		if len(h.Hash) != 40 || seen[h.Hash] || h.Parents[0] == h.Hash {
			t.Fatalf("hash %q", h.Hash)
		}
		seen[h.Hash] = true
	}
	if History(0, 2)[1].Hash != History(1, 1)[0].Hash || History(0, 1)[0].Parents[0] != History(1, 1)[0].Hash {
		t.Fatal("paging/parents not stable")
	}
}
