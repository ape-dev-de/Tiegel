package git

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/core/hooks"
	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
)

const (
	mailer     = "src/Mail/Mailer.php"
	controller = "src/Http/Controller/UserController.php"
)

func newSamplePanel(t testing.TB) *CommitPanel {
	p := NewCommitPanel()
	p.SetChanges(sampleChanges(t))
	return p
}

func fileByPath(p *CommitPanel, path string) *fileSel {
	for _, f := range p.files {
		if f.change.Status.Path == path {
			return f
		}
	}
	return nil
}

func TestSelectionModelHierarchy(t *testing.T) {
	f := newFileSel(Change{Diff: diffFor(t, controller)}, groupChanges, true)
	if f.state() != atoms.Checked || f.hunkState(0) != atoms.Checked || f.hunkState(1) != atoms.Checked {
		t.Fatal("new tracked file must be fully selected")
	}
	f.setHunk(1, false)
	if f.state() != atoms.Indeterminate || f.hunkState(1) != atoms.Unchecked {
		t.Fatalf("after unchecking hunk: file %v hunk %v", f.state(), f.hunkState(1))
	}
	// Uncheck one line of hunk 0 (line 3 is "-        private UserRepository").
	if f.change.Diff.Hunks[0].Lines[3].Kind != gitcli.LineDeleted {
		t.Fatalf("fixture changed: %+v", f.change.Diff.Hunks[0].Lines[3])
	}
	f.setLine(0, 3, false)
	if f.hunkState(0) != atoms.Indeterminate {
		t.Fatalf("hunk 0 state %v", f.hunkState(0))
	}
	sel, ok := f.selection()
	if !ok || sel.Whole || len(sel.Hunks) != 1 || sel.Hunks[0].Hunk != 0 {
		t.Fatalf("selection %+v", sel)
	}
	var want []int
	for l, dl := range f.change.Diff.Hunks[0].Lines {
		if dl.Kind != gitcli.LineContext && l != 3 {
			want = append(want, l)
		}
	}
	if !reflect.DeepEqual(sel.Hunks[0].Lines, want) {
		t.Errorf("lines %v, want %v", sel.Hunks[0].Lines, want)
	}
	// The selection must produce a valid patch with the gitcli builder.
	if _, err := f.change.Diff.Patch(sel.Hunks); err != nil {
		t.Errorf("patch: %v", err)
	}
	// Context lines cannot be toggled.
	f.setLine(0, 0, false)
	if f.lines[0][0] {
		t.Error("context line selected")
	}
	f.setAll(true)
	if sel, _ := f.selection(); !sel.Whole || sel.Hunks != nil {
		t.Errorf("all selected must be Whole: %+v", sel)
	}
	f.setAll(false)
	if _, ok := f.selection(); ok || f.state() != atoms.Unchecked {
		t.Error("nothing selected")
	}
}

func TestSelectionModelWholeOnly(t *testing.T) {
	bin := newFileSel(Change{Status: gitcli.FileStatus{Path: "public/logo.png"}, Diff: &gitcli.FileDiff{NewPath: "public/logo.png", IsBinary: true}}, groupChanges, true)
	if bin.state() != atoms.Checked {
		t.Fatal("binary file selected as a whole")
	}
	if sel, ok := bin.selection(); !ok || !sel.Whole {
		t.Fatalf("binary selection %+v", sel)
	}
	conflict := newFileSel(Change{Status: gitcli.FileStatus{Kind: gitcli.KindUnmerged, Path: "x"}}, groupChanges, true)
	conflict.setAll(true)
	if conflict.state() != atoms.Unchecked {
		t.Error("conflicts must not be selectable")
	}
	un := newFileSel(Change{Status: gitcli.FileStatus{Kind: gitcli.KindUntracked, Path: "n.md"}}, groupUnversioned, false)
	if un.state() != atoms.Unchecked {
		t.Error("untracked starts unselected")
	}
	un.setAll(true)
	if sel, _ := un.selection(); !sel.Untracked || !sel.Whole {
		t.Errorf("untracked selection %+v", sel)
	}
	if g := groupState([]*fileSel{bin, conflict, un}); g != atoms.Checked {
		t.Errorf("group state %v (conflicts are ignored)", g)
	}
	bin.setAll(false)
	if g := groupState([]*fileSel{bin, un}); g != atoms.Indeterminate {
		t.Errorf("group state %v", g)
	}
	if g := groupState([]*fileSel{conflict}); g != atoms.Unchecked {
		t.Errorf("only conflicts: %v", g)
	}
}

func TestProblemFromError(t *testing.T) {
	if ProblemFromError(nil) != nil {
		t.Fatal("nil")
	}
	for k := gitcli.PushFailed; k <= gitcli.PushRepoNotFound; k++ {
		p := ProblemFromError(fmt.Errorf("wrap: %w", &gitcli.PushError{Kind: k, Message: "m", Stderr: "remote: denied"}))
		if p.Title == "" || p.Detail == "" || p.Warning {
			t.Errorf("push kind %d: %+v", k, p)
		}
		if PushErrorTitle(k) == "" {
			t.Errorf("no title for %d", k)
		}
	}
	ab := &hooks.AbortError{HookError: hooks.HookError{Hook: "commit.before", Owner: "phpstan", Err: errors.New("3 errors")}}
	if p := ProblemFromError(ab); p.Title != "Commit blocked by phpstan (commit.before)." || p.Detail != "3 errors" {
		t.Errorf("guard: %+v", p)
	}
	if p := ProblemFromError(gitcli.ErrEmptyMessage); !p.Warning {
		t.Error("empty message is a warning")
	}
	for _, err := range []error{gitcli.ErrEmptySelection, gitcli.ErrNoRemote, gitcli.ErrDetachedHead, errors.New("boom")} {
		if p := ProblemFromError(err); p.Title == "" {
			t.Errorf("%v: empty title", err)
		}
	}
}

func TestStatusOf(t *testing.T) {
	cases := []struct {
		st   gitcli.FileStatus
		want string
	}{
		{gitcli.FileStatus{Kind: gitcli.KindUntracked}, "untracked"},
		{gitcli.FileStatus{Kind: gitcli.KindIgnored}, "ignored"},
		{gitcli.FileStatus{Kind: gitcli.KindUnmerged}, "conflict"},
		{gitcli.FileStatus{Index: 'A', Worktree: '.'}, "added"},
		{gitcli.FileStatus{Index: '.', Worktree: 'D'}, "deleted"},
		{gitcli.FileStatus{Index: '.', Worktree: 'M'}, "modified"},
		{gitcli.FileStatus{Kind: gitcli.KindRenamed, Index: 'R'}, "modified"},
	}
	for _, c := range cases {
		if got := statusOf(c.st).String(); got != c.want {
			t.Errorf("%+v: %s, want %s", c.st, got, c.want)
		}
	}
}
