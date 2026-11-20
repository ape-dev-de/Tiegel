package gitcli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// divergedRepo has main and feature, both changing a.txt (conflict) and
// each adding its own file.
func divergedRepo(t *testing.T, conflict bool) *testRepo {
	tr := newTestRepo(t)
	tr.write("a.txt", "one\ntwo\nthree\n")
	tr.commitAll("base")
	tr.git("checkout", "-q", "-b", "feature")
	tr.write("f.txt", "feature\n")
	if conflict {
		tr.write("a.txt", "one\nFEATURE\nthree\n")
	}
	tr.commitAll("feature")
	tr.git("checkout", "-q", "main")
	tr.write("m.txt", "main\n")
	if conflict {
		tr.write("a.txt", "one\nMAIN\nthree\n")
	}
	tr.commitAll("main")
	return tr
}

func TestMergeClean(t *testing.T) {
	tr := divergedRepo(t, false)
	ctx := ctxT(t)
	res, err := tr.r.Merge(ctx, "feature", MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateClean || len(res.Conflicts) != 0 || res.FastForward || res.Head == "" {
		t.Fatalf("result %+v", res)
	}
	if tr.read("f.txt") != "feature\n" || !strings.Contains(tr.git("log", "-1", "--format=%s"), "Merge branch 'feature'") {
		t.Fatal("merge commit missing")
	}
	res, err = tr.r.Merge(ctx, "feature", MergeOptions{})
	if err != nil || !res.UpToDate {
		t.Fatalf("second merge %+v %v", res, err)
	}
}

func TestMergeFastForwardAndOptions(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a.txt", "a\n")
	tr.commitAll("a")
	tr.git("branch", "next")
	tr.git("checkout", "-q", "next")
	tr.write("b.txt", "b\n")
	tr.commitAll("b")
	tr.git("checkout", "-q", "main")
	res, err := tr.r.Merge(ctx, "next", MergeOptions{FFOnly: true})
	if err != nil || !res.FastForward {
		t.Fatalf("ff %+v %v", res, err)
	}
	tr.git("reset", "-q", "--hard", "HEAD~1")
	res, err = tr.r.Merge(ctx, "next", MergeOptions{NoFF: true})
	if err != nil || res.FastForward || tr.git("rev-list", "--count", "--merges", "HEAD") != "1" {
		t.Fatalf("no-ff %+v %v", res, err)
	}
	for _, rev := range []string{"", "--abort", "missing-branch"} {
		if _, err := tr.r.Merge(ctx, rev, MergeOptions{}); err == nil {
			t.Fatalf("merge %q succeeded", rev)
		}
	}
	if _, err := tr.r.Merge(ctx, "-x", MergeOptions{}); !errors.Is(err, ErrInvalidRevision) {
		t.Fatalf("option-like revision: %v", err)
	}
	if err := tr.r.MergeAbort(ctx); !errors.Is(err, ErrNoOperation) {
		t.Fatalf("abort without merge: %v", err)
	}
	if _, err := tr.r.MergeContinue(ctx); !errors.Is(err, ErrNoOperation) {
		t.Fatalf("continue without merge: %v", err)
	}
}

func TestMergeConflictAbortAndContinue(t *testing.T) {
	tr := divergedRepo(t, true)
	ctx := ctxT(t)
	before := tr.git("rev-parse", "HEAD")
	res, err := tr.r.Merge(ctx, "feature", MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateMerging || !reflect.DeepEqual(res.Conflicts, []string{"a.txt"}) {
		t.Fatalf("conflict result %+v", res)
	}
	if _, err := tr.r.Merge(ctx, "feature", MergeOptions{}); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("merge during merge: %v", err)
	}
	if _, err := tr.r.Rebase(ctx, "feature"); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("rebase during merge: %v", err)
	}
	// Continuing with unresolved paths keeps the merge in progress.
	if res, err := tr.r.MergeContinue(ctx); err != nil || res.State != StateMerging || len(res.Conflicts) != 1 {
		t.Fatalf("continue unresolved %+v %v", res, err)
	}
	if err := tr.r.MergeAbort(ctx); err != nil {
		t.Fatal(err)
	}
	if tr.git("rev-parse", "HEAD") != before || tr.r.state() != StateClean {
		t.Fatal("abort did not restore the branch")
	}
	if _, err := tr.r.Merge(ctx, "feature", MergeOptions{}); err != nil {
		t.Fatal(err)
	}
	tr.write("a.txt", "one\nBOTH\nthree\n")
	tr.git("add", "a.txt")
	res, err = tr.r.MergeContinue(ctx)
	if err != nil || res.State != StateClean || len(res.Conflicts) != 0 {
		t.Fatalf("continue %+v %v", res, err)
	}
	if tr.git("rev-list", "--count", "--merges", "HEAD") != "1" {
		t.Fatal("no merge commit")
	}
}

func TestMergeRefusesOverwritingLocalChanges(t *testing.T) {
	tr := divergedRepo(t, true)
	tr.write("a.txt", "local edit\n")
	if _, err := tr.r.Merge(ctxT(t), "feature", MergeOptions{}); err == nil {
		t.Fatal("merge over local changes succeeded")
	}
	if tr.read("a.txt") != "local edit\n" || tr.r.state() != StateClean {
		t.Fatal("local changes touched")
	}
}

func TestRebase(t *testing.T) {
	tr := divergedRepo(t, false)
	ctx := ctxT(t)
	tr.git("checkout", "-q", "feature")
	res, err := tr.r.Rebase(ctx, "main")
	if err != nil || res.State != StateClean || len(res.Conflicts) != 0 {
		t.Fatalf("rebase %+v %v", res, err)
	}
	if tr.git("merge-base", "HEAD", "main") != tr.git("rev-parse", "main") {
		t.Fatal("feature not on top of main")
	}
	res, err = tr.r.Rebase(ctx, "main")
	if err != nil || !res.UpToDate {
		t.Fatalf("second rebase %+v %v", res, err)
	}
	if _, err := tr.r.Rebase(ctx, "-i"); !errors.Is(err, ErrInvalidRevision) {
		t.Fatalf("option-like upstream: %v", err)
	}
	if err := tr.r.RebaseAbort(ctx); !errors.Is(err, ErrNoOperation) {
		t.Fatalf("abort without rebase: %v", err)
	}
	if _, err := tr.r.RebaseContinue(ctx); !errors.Is(err, ErrNoOperation) {
		t.Fatalf("continue without rebase: %v", err)
	}
}

func TestRebaseConflict(t *testing.T) {
	tr := divergedRepo(t, true)
	ctx := ctxT(t)
	tr.git("checkout", "-q", "feature")
	before := tr.git("rev-parse", "HEAD")
	res, err := tr.r.Rebase(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateRebasing || !reflect.DeepEqual(res.Conflicts, []string{"a.txt"}) {
		t.Fatalf("conflict result %+v", res)
	}
	if err := tr.r.RebaseAbort(ctx); err != nil {
		t.Fatal(err)
	}
	if tr.git("rev-parse", "HEAD") != before || tr.r.state() != StateClean {
		t.Fatal("abort did not restore the branch")
	}
	if _, err := tr.r.Rebase(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	tr.write("a.txt", "one\nBOTH\nthree\n")
	tr.git("add", "a.txt")
	res, err = tr.r.RebaseContinue(ctx)
	if err != nil || res.State != StateClean || len(res.Conflicts) != 0 {
		t.Fatalf("continue %+v %v", res, err)
	}
	if tr.read("a.txt") != "one\nBOTH\nthree\n" || tr.git("merge-base", "HEAD", "main") != tr.git("rev-parse", "main") {
		t.Fatal("rebase result wrong")
	}
}
