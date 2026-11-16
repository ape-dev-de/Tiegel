package gitsample

import (
	"testing"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
)

func TestFixturesParse(t *testing.T) {
	ch, err := Changes()
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 10 {
		t.Fatalf("changes = %d", len(ch))
	}
	withDiff := 0
	for _, c := range ch {
		if c.Diff != nil {
			withDiff++
		}
	}
	if withDiff != 7 {
		t.Errorf("changes with diff = %d, want 7", withDiff)
	}
	st, _ := Status()
	if st.Branch.Head != "feature/invoices" || st.Branch.Ahead != 2 || len(st.Conflicts()) != 1 {
		t.Errorf("branch status %+v", st.Branch)
	}
	u, err := UntrackedDiff()
	if err != nil || !u.IsNew || len(u.Hunks) != 1 {
		t.Fatalf("untracked: %v %+v", err, u)
	}
	m := Merge()
	if m.Conflicts() == 0 {
		t.Error("merge fixture has no conflicts")
	}
	if c := Commits(10, 5); len(c) != 5 || c[0].Hash == Commits(0, 1)[0].Hash {
		t.Error("commits paging")
	}
	if len(Branches()) != 7 {
		t.Error("branches")
	}
	big, err := LargeDiff(1000)
	if err != nil {
		t.Fatal(err)
	}
	if a, d := big.Stats(); a == 0 || a != d {
		t.Errorf("large diff stats %d/%d", a, d)
	}
	if big.Hunks[0].Lines[3].Kind != gitcli.LineDeleted {
		t.Error("large diff layout")
	}
}
