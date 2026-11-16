package gitcli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitBasics(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	if _, err := tr.r.Commit(ctx, CommitOptions{Message: "  \n"}); !errors.Is(err, ErrEmptyMessage) {
		t.Errorf("empty message: %v", err)
	}
	if msgs, err := tr.r.RecentMessages(ctx, 5); err != nil || msgs != nil {
		t.Errorf("unborn messages: %v %v", msgs, err)
	}
	tr.write("a", "a\n")
	tr.git("add", "a")
	h1, err := tr.r.Commit(ctx, CommitOptions{Message: "First commit\n\nWith body\n"})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.git("rev-parse", "HEAD"); got != h1 {
		t.Errorf("hash %s vs %s", h1, got)
	}
	if _, err := tr.r.Commit(ctx, CommitOptions{Message: "nothing"}); err == nil {
		t.Error("commit without changes must fail")
	}
	h2, err := tr.r.Commit(ctx, CommitOptions{Message: "Empty", AllowEmpty: true, NoVerify: true, SignOff: true,
		Author: "Other Person <other@example.com>"})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.git("log", "-1", "--format=%an <%ae>|%cn"); got != "Other Person <other@example.com>|Test User" {
		t.Errorf("author: %q", got)
	}
	if body := tr.git("log", "-1", "--format=%B"); !strings.Contains(body, "Signed-off-by: Test User <test@example.com>") {
		t.Errorf("signoff: %q", body)
	}
	tr.write("a", "b\n")
	tr.git("add", "a")
	h3, err := tr.r.Commit(ctx, CommitOptions{Message: "Amended", Amend: true})
	if err != nil {
		t.Fatal(err)
	}
	if h3 == h2 || tr.git("rev-parse", "HEAD^") != h1 {
		t.Error("amend did not replace HEAD")
	}
	if _, err := tr.r.Commit(ctx, CommitOptions{Message: "First commit\n\nWith body", AllowEmpty: true}); err != nil {
		t.Fatal(err)
	}
	msgs, err := tr.r.RecentMessages(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"First commit\n\nWith body", "Amended"}
	if strings.Join(msgs, "|") != strings.Join(want, "|") {
		t.Errorf("messages: %q", msgs)
	}
	if msgs, _ := tr.r.RecentMessages(ctx, 1); len(msgs) != 1 {
		t.Errorf("limit: %q", msgs)
	}
	if msgs, _ := tr.r.RecentMessages(ctx, 0); msgs != nil {
		t.Errorf("zero: %q", msgs)
	}

	id, err := tr.r.ConfigIdentity(ctx)
	if err != nil || id.String() != "Test User <test@example.com>" {
		t.Errorf("identity %v %v", id, err)
	}
	if v, err := tr.r.ConfigGet(ctx, "no.such.key"); err != nil || v != "" {
		t.Errorf("missing key: %q %v", v, err)
	}
	if _, err := tr.r.ConfigGet(ctx, "nosection"); err == nil {
		t.Error("invalid key must fail")
	}
}

func TestCommitSelectionKeepsUserIndex(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	orig := numbered(30)
	tr.write("staged.txt", "s1\n")
	tr.write("partial.txt", orig)
	tr.write("whole.txt", "w1\n")
	tr.write("gone.txt", "bye\n")
	base := tr.commitAll("init")

	// The user staged something unrelated.
	tr.write("staged.txt", "s2\n")
	tr.git("add", "staged.txt")
	// Work tree changes to commit.
	partial := replaceLine(replaceLine(orig, 2, "TWO\n"), 28, "TWENTY-EIGHT\n")
	tr.write("partial.txt", partial)
	tr.write("whole.txt", "w2\n")
	tr.write("brand-new.txt", "fresh\n")
	tr.git("rm", "-q", "--cached", "gone.txt")
	if err := removeFile(tr, "gone.txt"); err != nil {
		t.Fatal(err)
	}

	files, err := tr.r.Diff(ctx, DiffOptions{Base: "HEAD", Paths: []string{"partial.txt"}})
	if err != nil || len(files) != 1 || len(files[0].Hunks) != 2 {
		t.Fatalf("diff: %+v %v", files, err)
	}
	patch, err := files[0].Patch([]HunkSelection{{Hunk: 0}})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := tr.r.CommitSelection(ctx, CommitOptions{Message: "selected"},
		CommitSelection{Paths: []string{"whole.txt", "brand-new.txt", "gone.txt"}, Patches: [][]byte{patch}})
	if err != nil {
		t.Fatal(err)
	}
	if tr.git("rev-parse", "HEAD") != hash || tr.git("rev-parse", "HEAD^") != base {
		t.Fatal("commit not on top of base")
	}
	// Commit content.
	show := func(p string) string {
		b, err := tr.r.ShowFile(ctx, "HEAD", p)
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	if show("partial.txt") != replaceLine(orig, 2, "TWO\n") {
		t.Errorf("partial in commit:\n%s", show("partial.txt"))
	}
	if show("whole.txt") != "w2\n" || show("brand-new.txt") != "fresh\n" || show("gone.txt") != "<missing>" {
		t.Error("whole files not committed correctly")
	}
	if show("staged.txt") != "s1\n" {
		t.Error("unrelated staged change leaked into the commit")
	}
	// The user's index still has staged.txt staged.
	if got := tr.git("diff", "--cached", "--name-only"); got != "staged.txt" {
		t.Errorf("staged after commit: %q", got)
	}
	// The rest of partial.txt is an unstaged change.
	st, _ := tr.r.Status(ctx, StatusOptions{})
	for _, f := range st.Files {
		switch f.Path {
		case "partial.txt":
			if f.Index != '.' || f.Worktree != 'M' {
				t.Errorf("partial status %c%c", f.Index, f.Worktree)
			}
		case "whole.txt", "brand-new.txt", "gone.txt":
			t.Errorf("%s should be clean: %c%c", f.Path, f.Index, f.Worktree)
		}
	}
	if tr.read("partial.txt") != partial {
		t.Error("work tree modified")
	}
	// No temporary index left behind.
	if m, _ := filepath.Glob(filepath.Join(tr.r.GitDir, "tiegel-index-*")); len(m) != 0 {
		t.Errorf("leftover temp index: %v", m)
	}
}

func TestCommitSelectionAmendAndErrors(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	if _, err := tr.r.CommitSelection(ctx, CommitOptions{Message: ""}, CommitSelection{}); !errors.Is(err, ErrEmptyMessage) {
		t.Errorf("empty message: %v", err)
	}
	if _, err := tr.r.CommitSelection(ctx, CommitOptions{Message: "x"}, CommitSelection{}); !errors.Is(err, ErrEmptySelection) {
		t.Errorf("empty selection: %v", err)
	}
	// First commit on an unborn branch via selection.
	tr.write("a", "a\n")
	tr.write("b", "b\n")
	if _, err := tr.r.CommitSelection(ctx, CommitOptions{Message: "only a"}, CommitSelection{Paths: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if got := tr.git("ls-tree", "--name-only", "HEAD"); got != "a" {
		t.Errorf("tree: %q", got)
	}
	// Amend adding b, keeping the message rewrite.
	if _, err := tr.r.CommitSelection(ctx, CommitOptions{Message: "a and b", Amend: true}, CommitSelection{Paths: []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	if got := tr.git("ls-tree", "--name-only", "HEAD"); got != "a\nb" {
		t.Errorf("amended tree: %q", got)
	}
	if got := tr.git("rev-list", "--count", "HEAD"); got != "1" {
		t.Errorf("amend created a new commit: %s", got)
	}
	// Bad patch: commit fails, temp index cleaned.
	if _, err := tr.r.CommitSelection(ctx, CommitOptions{Message: "bad"}, CommitSelection{Patches: [][]byte{[]byte("nonsense")}}); err == nil {
		t.Error("bad patch must fail")
	}
	if _, err := tr.r.CommitSelection(ctx, CommitOptions{Message: "bad"}, CommitSelection{Paths: []string{"missing"}}); err == nil {
		t.Error("missing path must fail")
	}
	if m, _ := filepath.Glob(filepath.Join(tr.r.GitDir, "tiegel-index-*")); len(m) != 0 {
		t.Errorf("leftover temp index: %v", m)
	}
}

func TestCommitSelectionRename(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("old.txt", numbered(10))
	tr.commitAll("init")
	tr.git("mv", "old.txt", "new.txt")
	tr.write("new.txt", replaceLine(numbered(10), 10, "ten\n"))
	tr.git("reset", "-q") // user index without the rename
	if _, err := tr.r.CommitSelection(ctx, CommitOptions{Message: "rename"}, CommitSelection{Paths: []string{"old.txt", "new.txt"}}); err != nil {
		t.Fatal(err)
	}
	if got := tr.git("ls-tree", "--name-only", "HEAD"); got != "new.txt" {
		t.Errorf("tree: %q", got)
	}
	if st := tr.git("status", "--porcelain"); st != "" {
		t.Errorf("status not clean: %q", st)
	}
}

func TestPatchPathsAndDedupe(t *testing.T) {
	p := []byte("diff --git a/x b/y\nrename from x\nrename to y\n")
	if got := patchPaths(p); strings.Join(got, ",") != "x,y" {
		t.Errorf("patchPaths = %v", got)
	}
	if got := dedupe([]string{"a", "b", "a"}); strings.Join(got, ",") != "a,b" {
		t.Errorf("dedupe = %v", got)
	}
}

func removeFile(tr *testRepo, p string) error {
	return os.Remove(filepath.Join(tr.dir, p))
}

// Review 2026-11-16 #1: a selection commit during a merge (git merge
// --no-commit) started from HEAD, dropped the merged changes and still
// recorded the second parent.
func TestCommitSelectionRefusedDuringMerge(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a.txt", "a\n")
	tr.commitAll("init")
	tr.git("checkout", "-q", "-b", "feature")
	tr.write("feature.txt", "from feature\n")
	tr.commitAll("feature")
	tr.git("checkout", "-q", "main")
	tr.write("a.txt", "a2\n")
	before := tr.commitAll("main change")
	tr.git("merge", "--no-commit", "--no-ff", "-q", "feature")
	tr.write("local.txt", "local\n")

	_, err := tr.r.CommitSelection(ctx, CommitOptions{Message: "selection"}, CommitSelection{Paths: []string{"local.txt"}})
	if !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("CommitSelection during merge: err = %v, want ErrOperationInProgress", err)
	}
	if got := tr.git("rev-parse", "HEAD"); got != before {
		t.Fatalf("HEAD moved to %s", got)
	}
	// The merge is still in progress and its changes are staged.
	if got := tr.git("diff", "--cached", "--name-only"); got != "feature.txt" {
		t.Errorf("staged merge changes: %q", got)
	}
}

// Review 2026-11-16 #4: content that was only staged (index differs from
// both HEAD and work tree) was destroyed by the index reset after a
// partial commit of another hunk of the same file.
func TestCommitSelectionKeepsStagedOnlyContent(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	orig := numbered(30)
	tr.write("a.txt", orig)
	tr.commitAll("init")
	// Stage a version of line 2 that exists neither in HEAD nor later in
	// the work tree.
	tr.write("a.txt", replaceLine(orig, 2, "STAGED\n"))
	tr.git("add", "a.txt")
	work := replaceLine(replaceLine(orig, 2, "WORK\n"), 28, "TWENTY-EIGHT\n")
	tr.write("a.txt", work)

	files, err := tr.r.Diff(ctx, DiffOptions{Base: "HEAD", Paths: []string{"a.txt"}})
	if err != nil || len(files) != 1 || len(files[0].Hunks) != 2 {
		t.Fatalf("diff: %+v %v", files, err)
	}
	patch, err := files[0].Patch([]HunkSelection{{Hunk: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.r.CommitSelection(ctx, CommitOptions{Message: "line 28"}, CommitSelection{Patches: [][]byte{patch}}); err != nil {
		t.Fatal(err)
	}
	head, _ := tr.r.ShowFile(ctx, "HEAD", "a.txt")
	if string(head) != replaceLine(orig, 28, "TWENTY-EIGHT\n") {
		t.Errorf("committed content:\n%s", head)
	}
	index := tr.git("show", ":a.txt")
	if !strings.Contains(index, "STAGED") {
		t.Fatalf("staged-only content lost, index:\n%s", index)
	}
	// The committed change is part of the index, so it is not shown as a
	// staged revert.
	if !strings.Contains(index, "TWENTY-EIGHT") {
		t.Errorf("committed change missing in index:\n%s", index)
	}
	if tr.read("a.txt") != work {
		t.Error("work tree modified")
	}
}
