package git

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

// diverge gives main and feature conflicting changes of a.txt.
func diverge(t *testing.T, dir string) {
	t.Helper()
	modtest.Git(t, dir, "checkout", "-q", "-b", "feature")
	write(t, dir, "a.txt", strings.Replace(ten, "one", "feature", 1))
	modtest.Git(t, dir, "commit", "-q", "-am", "feature")
	modtest.Git(t, dir, "checkout", "-q", "main")
	write(t, dir, "a.txt", strings.Replace(ten, "one", "main", 1))
	modtest.Git(t, dir, "commit", "-q", "-am", "main")
}

func TestMergeAPI(t *testing.T) {
	a, dir := setup(t)
	diverge(t, dir)
	modtest.Call(t, a, "doc.open", api.PathParams{Path: "a.txt"}, nil)
	modtest.WantCode(t, a, "git.merge", MergeParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.merge", MergeParams{Branch: "feature", Abort: true}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.merge", MergeParams{Branch: "feature", NoFF: true, FFOnly: true}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.merge", MergeParams{Branch: "-x"}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.merge", MergeParams{Abort: true}, api.CodeConflict)
	var out MergeOutcome
	modtest.Call(t, a, "git.merge", MergeParams{Branch: "feature"}, &out)
	if out.State != "merging" || !reflect.DeepEqual(out.Conflicts, []string{"a.txt"}) {
		t.Fatalf("merge %+v", out)
	}
	// The open document shows the conflict markers.
	if !strings.Contains(docText(t, a, "a.txt"), "<<<<<<<") {
		t.Fatal("document not reloaded")
	}
	var conflicts []GitFileStatus
	modtest.Call(t, a, "git.conflicts", struct{}{}, &conflicts)
	if len(conflicts) != 1 {
		t.Fatalf("conflicts %+v", conflicts)
	}
	modtest.WantCode(t, a, "git.rebase", RebaseParams{Onto: "feature"}, api.CodeConflict)
	modtest.Call(t, a, "git.merge", MergeParams{Abort: true}, &out)
	if !out.Aborted || out.State != "" || docText(t, a, "a.txt") != strings.Replace(ten, "one", "main", 1) {
		t.Fatalf("abort %+v", out)
	}
	modtest.Call(t, a, "git.merge", MergeParams{Branch: "feature"}, &out)
	write(t, dir, "a.txt", ten)
	modtest.Git(t, dir, "add", "a.txt")
	modtest.Call(t, a, "git.merge", MergeParams{Continue: true}, &out)
	if out.State != "" || len(out.Conflicts) != 0 || out.Head == "" {
		t.Fatalf("continue %+v", out)
	}
}

func TestRebaseAPI(t *testing.T) {
	a, dir := setup(t)
	diverge(t, dir)
	modtest.Git(t, dir, "checkout", "-q", "feature")
	modtest.WantCode(t, a, "git.rebase", RebaseParams{}, api.CodeInvalidParams)
	modtest.WantCode(t, a, "git.rebase", RebaseParams{Continue: true}, api.CodeConflict)
	var out MergeOutcome
	modtest.Call(t, a, "git.rebase", RebaseParams{Onto: "main"}, &out)
	if out.State != "rebasing" || !reflect.DeepEqual(out.Conflicts, []string{"a.txt"}) {
		t.Fatalf("rebase %+v", out)
	}
	modtest.Call(t, a, "git.rebase", RebaseParams{Abort: true}, &out)
	if out.State != "" {
		t.Fatalf("abort %+v", out)
	}
	modtest.Call(t, a, "git.rebase", RebaseParams{Onto: "main"}, &out)
	write(t, dir, "a.txt", ten)
	modtest.Git(t, dir, "add", "a.txt")
	modtest.Call(t, a, "git.rebase", RebaseParams{Continue: true}, &out)
	if out.State != "" || len(out.Conflicts) != 0 {
		t.Fatalf("continue %+v", out)
	}
}
