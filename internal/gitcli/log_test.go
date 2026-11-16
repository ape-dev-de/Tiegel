package gitcli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLog(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	if cs, err := tr.r.Log(ctx, LogOptions{}); err != nil || cs != nil {
		t.Fatalf("unborn log: %v %v", cs, err)
	}
	var hashes []string
	for i := 1; i <= 5; i++ {
		tr.write("f.txt", strings.Repeat("x\n", i))
		if i%2 == 0 {
			tr.write("other.txt", itoa(i))
		}
		hashes = append(hashes, tr.commitAll("commit "+itoa(i)))
	}
	tr.git("tag", "v1", hashes[0])
	cs, err := tr.r.Log(ctx, LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 5 || cs[0].Hash != hashes[4] || cs[0].Subject != "commit 5" || cs[4].Hash != hashes[0] {
		t.Fatalf("log: %+v", cs)
	}
	c := cs[0]
	if c.AuthorName != "Test User" || c.AuthorEmail != "test@example.com" || c.CommitterName != "Test User" ||
		c.AuthorDate.IsZero() || c.CommitDate.IsZero() || len(c.Parents) != 1 || c.Parents[0] != hashes[3] || c.IsMerge() {
		t.Errorf("commit fields: %+v", c)
	}
	if strings.Join(c.Refs, ",") != "HEAD -> main" {
		t.Errorf("refs: %q", c.Refs)
	}
	if strings.Join(cs[4].Refs, ",") != "tag: v1" || len(cs[4].Parents) != 0 {
		t.Errorf("root commit: %+v", cs[4])
	}

	page, err := tr.r.Log(ctx, LogOptions{Skip: 1, Max: 2})
	if err != nil || len(page) != 2 || page[0].Hash != hashes[3] || page[1].Hash != hashes[2] {
		t.Errorf("paging: %+v %v", page, err)
	}
	fileLog, err := tr.r.Log(ctx, LogOptions{Path: "other.txt"})
	if err != nil || len(fileLog) != 2 || fileLog[0].Hash != hashes[3] {
		t.Errorf("file log: %+v %v", fileLog, err)
	}
	tr.git("mv", "other.txt", "renamed.txt")
	tr.commitAll("rename")
	follow, err := tr.r.Log(ctx, LogOptions{Path: "renamed.txt", Follow: true})
	if err != nil || len(follow) != 3 {
		t.Errorf("follow log: %d %v", len(follow), err)
	}
	all, err := tr.r.Log(ctx, LogOptions{All: true, Rev: "ignored"})
	if err != nil || len(all) != 6 {
		t.Errorf("all: %d %v", len(all), err)
	}
	if _, err := tr.r.Log(ctx, LogOptions{Rev: "no-such-rev"}); err == nil {
		t.Error("bad rev must fail")
	}
	if _, err := parseLog([]byte("too\x1ffew\x00")); err == nil {
		t.Error("bad record must fail")
	}
	if !unixTime("x").IsZero() {
		t.Error("bad time")
	}
}

func TestMergeCommitLog(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a", "a\n")
	tr.commitAll("a")
	tr.git("checkout", "-q", "-b", "side")
	tr.write("b", "b\n")
	tr.commitAll("b")
	tr.git("checkout", "-q", "main")
	tr.write("c", "c\n")
	tr.commitAll("c")
	tr.git("merge", "-q", "--no-edit", "side")
	cs, err := tr.r.Log(ctx, LogOptions{Max: 1})
	if err != nil || len(cs) != 1 || !cs[0].IsMerge() {
		t.Fatalf("merge: %+v %v", cs, err)
	}
}

func TestShowFile(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	if _, err := tr.r.ShowFile(ctx, "HEAD", "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unborn: %v", err)
	}
	tr.write("dir/a.txt", "v1\n")
	h1 := tr.commitAll("1")
	tr.write("dir/a.txt", "v2\n")
	tr.commitAll("2")
	tr.write("dir/a.txt", "v3\n")
	tr.git("add", "dir/a.txt")

	for rev, want := range map[string]string{h1: "v1\n", "HEAD": "v2\n"} {
		b, err := tr.r.ShowFile(ctx, rev, "dir/a.txt")
		if err != nil || string(b) != want {
			t.Errorf("show %s: %q %v", rev, b, err)
		}
	}
	if b, err := tr.r.ShowIndexFile(ctx, 0, "dir/a.txt"); err != nil || string(b) != "v3\n" {
		t.Errorf("index: %q %v", b, err)
	}
	if _, err := tr.r.ShowFile(ctx, "HEAD", "missing.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if _, err := tr.r.ShowFile(ctx, "nosuchrev", "dir/a.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bad rev: %v", err)
	}
	if _, err := tr.r.ShowIndexFile(ctx, 2, "dir/a.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("stage 2 of clean file: %v", err)
	}
	if _, err := tr.r.ShowIndexFile(ctx, 7, "x"); err == nil {
		t.Error("bad stage")
	}
}

func TestBranches(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a", "1\n")
	first := tr.commitAll("first")
	tr.write("a", "2\n")
	tr.commitAll("second")

	if err := tr.r.CreateBranch(ctx, "feature", first, false); err != nil {
		t.Fatal(err)
	}
	if err := tr.r.CreateBranch(ctx, "bad..name", "", false); err == nil {
		t.Error("invalid name accepted")
	}
	if err := tr.r.CreateBranch(ctx, "feature", "", false); err == nil {
		t.Error("duplicate branch accepted")
	}
	if err := tr.r.CreateBranch(ctx, "work", "", true); err != nil {
		t.Fatal(err)
	}
	if info, _ := tr.r.Info(ctx); info.Branch != "work" {
		t.Errorf("not on work: %+v", info)
	}
	if err := tr.r.CreateBranch(ctx, "from-first", first, true); err != nil {
		t.Fatal(err)
	}
	if tr.git("rev-parse", "HEAD") != first {
		t.Error("from-first not at first")
	}

	// Fake a remote-tracking branch and an origin/HEAD symref.
	tr.git("remote", "add", "origin", "/nonexistent/origin.git")
	tr.git("update-ref", "refs/remotes/origin/remote-only", first)
	tr.git("update-ref", "refs/remotes/origin/main", first)
	tr.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	tr.git("config", "branch.main.remote", "origin")
	tr.git("config", "branch.main.merge", "refs/heads/main")
	tr.git("config", "branch.feature.remote", "origin")
	tr.git("config", "branch.feature.merge", "refs/heads/gone-branch")

	bs, err := tr.r.Branches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Branch{}
	for _, b := range bs {
		byName[b.Name] = b
	}
	if _, ok := byName["origin/HEAD"]; ok {
		t.Error("symref listed")
	}
	if b := byName["from-first"]; !b.Current || b.Remote || b.Hash != first || b.Subject != "first" || b.CommitDate.IsZero() {
		t.Errorf("current: %+v", b)
	}
	if b := byName["main"]; b.Upstream != "origin/main" || b.Ahead != 1 || b.Behind != 0 {
		t.Errorf("main tracking: %+v", b)
	}
	if b := byName["feature"]; !b.UpstreamGone {
		t.Errorf("gone upstream: %+v", b)
	}
	if b := byName["origin/remote-only"]; !b.Remote || b.Ref != "refs/remotes/origin/remote-only" {
		t.Errorf("remote: %+v", b)
	}
	var tb Branch
	parseTrack(&tb, "ahead 3, behind 4")
	if tb.Ahead != 3 || tb.Behind != 4 {
		t.Errorf("parseTrack: %+v", tb)
	}

	// Checkout local, remote (creates tracking branch) and detached.
	if err := tr.r.Checkout(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if err := tr.r.Checkout(ctx, "origin/remote-only"); err != nil {
		t.Fatal(err)
	}
	info, _ := tr.r.Info(ctx)
	if info.Branch != "remote-only" {
		t.Errorf("tracking checkout: %+v", info)
	}
	if up, _ := tr.r.ConfigGet(ctx, "branch.remote-only.merge"); up != "refs/heads/remote-only" {
		t.Errorf("tracking config: %q", up)
	}
	if err := tr.r.Checkout(ctx, first); err != nil {
		t.Fatal(err)
	}
	if info, _ := tr.r.Info(ctx); !info.Detached {
		t.Errorf("detached checkout: %+v", info)
	}
	if err := tr.r.Checkout(ctx, "no-such-ref"); err == nil {
		t.Error("bad ref checkout")
	}

	if err := tr.r.Checkout(ctx, "main"); err != nil {
		t.Fatal(err)
	}
	if err := tr.r.DeleteBranch(ctx, "work", false); err != nil {
		t.Errorf("delete merged: %v", err)
	}
	tr.r.CreateBranch(ctx, "unmerged", "", true)
	tr.write("u", "u\n")
	tr.commitAll("u")
	tr.r.Checkout(ctx, "main")
	if err := tr.r.DeleteBranch(ctx, "unmerged", false); err == nil {
		t.Error("unmerged branch deleted without force")
	}
	if err := tr.r.DeleteBranch(ctx, "unmerged", true); err != nil {
		t.Errorf("force delete: %v", err)
	}
}

// makeConflict creates a merge conflict on f.txt and returns the repo.
func makeConflict(t *testing.T) *testRepo {
	tr := newTestRepo(t)
	tr.write("f.txt", "a\nb\nc\nd\ne\n")
	tr.commitAll("base")
	tr.git("checkout", "-q", "-b", "theirs")
	tr.write("f.txt", "a\nB-theirs\nc\nd\nE\n")
	tr.commitAll("theirs")
	tr.git("checkout", "-q", "main")
	tr.write("f.txt", "A\nb-ours\nc\nd\ne\n")
	tr.commitAll("ours")
	if err := tr.gitErr("merge", "-q", "theirs"); err == nil {
		t.Fatal("expected merge conflict")
	}
	return tr
}

func TestConflictWorkflow(t *testing.T) {
	tr := makeConflict(t)
	ctx := ctxT(t)
	info, err := tr.r.Info(ctx)
	if err != nil || info.State != StateMerging {
		t.Fatalf("state: %+v %v", info, err)
	}
	st, err := tr.r.Status(ctx, StatusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	conf := st.Conflicts()
	if len(conf) != 1 || conf[0].Path != "f.txt" || conf[0].Index != 'U' || conf[0].Worktree != 'U' {
		t.Fatalf("conflicts: %+v", conf)
	}
	cv, err := tr.r.ConflictVersions(ctx, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(cv.Base) != "a\nb\nc\nd\ne\n" || string(cv.Ours) != "A\nb-ours\nc\nd\ne\n" || string(cv.Theirs) != "a\nB-theirs\nc\nd\nE\n" ||
		!cv.HasBase || !cv.HasOurs || !cv.HasTheirs {
		t.Fatalf("versions: %+v", cv)
	}
	// A diff of an unmerged file is a combined diff.
	files, err := tr.r.Diff(ctx, DiffOptions{})
	if err != nil || len(files) != 1 || !files[0].IsCombined {
		t.Errorf("combined diff: %+v %v", files, err)
	}

	m := Merge3(cv.Base, cv.Ours, cv.Theirs)
	if m.Conflicts() != 1 {
		t.Fatalf("conflicts: %d (%+v)", m.Conflicts(), m.Blocks)
	}
	for i := range m.Blocks {
		if m.Blocks[i].Kind == MergeConflict {
			m.Blocks[i].Resolution = TakeTheirs
		}
	}
	result := m.Render(MarkerLabels{})
	if string(result) != "a\nB-theirs\nc\nd\nE\n" {
		t.Errorf("result %q", result)
	}
	if err := tr.r.ResolveConflict(ctx, "f.txt", result); err != nil {
		t.Fatal(err)
	}
	st, _ = tr.r.Status(ctx, StatusOptions{})
	if len(st.Conflicts()) != 0 {
		t.Errorf("still conflicted: %+v", st.Files)
	}
	if _, err := tr.r.ConflictVersions(ctx, "f.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("resolved file versions: %v", err)
	}
	if _, err := tr.r.Commit(ctx, CommitOptions{Message: "merge"}); err != nil {
		t.Fatal(err)
	}
	cs, _ := tr.r.Log(ctx, LogOptions{Max: 1})
	if !cs[0].IsMerge() {
		t.Error("merge commit expected")
	}
}

func TestConflictAddAdd(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("base", "x\n")
	tr.commitAll("base")
	tr.git("checkout", "-q", "-b", "other")
	tr.write("new.txt", "theirs\n")
	tr.commitAll("other adds")
	tr.git("checkout", "-q", "main")
	tr.write("new.txt", "ours\n")
	tr.commitAll("main adds")
	if err := tr.gitErr("merge", "-q", "other"); err == nil {
		t.Fatal("expected conflict")
	}
	cv, err := tr.r.ConflictVersions(ctx, "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if cv.HasBase || !cv.HasOurs || !cv.HasTheirs {
		t.Errorf("add/add: %+v", cv)
	}
	m := Merge3(cv.Base, cv.Ours, cv.Theirs)
	got := string(m.Render(MarkerLabels{Ours: "HEAD", Theirs: "other"}))
	if got != "<<<<<<< HEAD\nours\n||||||| base\n=======\ntheirs\n>>>>>>> other\n" {
		t.Errorf("render: %q", got)
	}
	// Write into a new sub directory with a fresh file.
	if err := tr.r.ResolveConflict(ctx, "sub/created.txt", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tr.dir, "sub", "created.txt")); err != nil {
		t.Error(err)
	}
}
