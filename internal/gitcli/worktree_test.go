package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeLifecycle(t *testing.T) {
	tr := newTestRepo(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(tr.dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr.git("add", "a.txt")
	tr.git("commit", "-q", "-m", "init")
	head, _ := tr.r.ResolveCommit(ctx, "HEAD")

	// Clean tree: nothing to snapshot.
	if s, err := tr.r.StashCreate(ctx); err != nil || s != "" {
		t.Fatalf("%q %v", s, err)
	}
	if d, u, err := tr.r.Dirty(ctx); err != nil || d || u {
		t.Fatal(d, u, err)
	}
	// Dirty tree: snapshot commit, work tree unchanged.
	if err := os.WriteFile(filepath.Join(tr.dir, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tr.dir, "u.txt"), []byte("u\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d, u, _ := tr.r.Dirty(ctx); !d || !u {
		t.Fatal("dirty not detected")
	}
	snap, err := tr.r.StashCreate(ctx)
	if err != nil || snap == "" {
		t.Fatalf("%q %v", snap, err)
	}
	if got := tr.git("stash", "list"); got != "" {
		t.Fatalf("stash list touched: %q", got)
	}

	if err := tr.r.Exclude(".tiegel/worktrees/"); err != nil {
		t.Fatal(err)
	}
	_ = tr.r.Exclude(".tiegel/worktrees/")
	ex, _ := os.ReadFile(filepath.Join(tr.r.CommonDir, "info", "exclude"))
	if strings.Count(string(ex), ".tiegel/worktrees/") != 1 {
		t.Fatalf("exclude: %q", ex)
	}
	wt := filepath.Join(tr.dir, ".tiegel", "worktrees", "p1")
	if err := tr.r.WorktreeAdd(ctx, wt, "tiegel/plan-p1", snap); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(wt, "a.txt"))
	if string(b) != "two\n" {
		t.Fatalf("worktree has %q", b)
	}
	if d, u, _ := tr.r.Dirty(ctx); !d || !u {
		t.Fatal("main tree changed")
	}
	if st := tr.git("status", "--porcelain"); strings.Contains(st, ".tiegel") {
		t.Fatalf("worktree dir shows in status: %s", st)
	}
	w, err := Open(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c1, err := w.CheckpointAll(ctx, "plan p1: iteration 1")
	if err != nil || c1 == snap {
		t.Fatalf("%s %v", c1, err)
	}
	if c2, err := w.CheckpointAll(ctx, "nothing"); err != nil || c2 != c1 {
		t.Fatalf("empty checkpoint: %s %v", c2, err)
	}
	if a := tr.git("log", "-1", "--format=%an", c1); a != "Tiegel" {
		t.Fatalf("author %q", a)
	}
	patch, err := w.RawDiff(ctx, DiffOptions{Base: snap, Target: c1})
	if err != nil || !strings.Contains(string(patch), "+b") {
		t.Fatalf("%s %v", patch, err)
	}
	// Apply onto the user's (dirty) working copy.
	if err := tr.r.ApplyThreeWay(ctx, patch); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(tr.dir, "b.txt")); string(b) != "b\n" {
		t.Fatal("not applied")
	}
	// Base worktree for checks, cleaned after use.
	base := filepath.Join(tr.dir, ".tiegel", "worktrees", "p1-base")
	if err := tr.r.WorktreeAdd(ctx, base, "", head); err != nil {
		t.Fatal(err)
	}
	bw, _ := Open(ctx, base)
	_ = os.WriteFile(filepath.Join(base, "a.txt"), []byte("x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(base, "new.txt"), []byte("x\n"), 0o644)
	if err := bw.Clean(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "a.txt")); string(b) != "one\n" {
		t.Fatalf("clean: %q", b)
	}
	if _, err := os.Stat(filepath.Join(base, "new.txt")); err == nil {
		t.Fatal("untracked file kept")
	}
	for _, d := range []string{wt, base} {
		if err := tr.r.WorktreeRemove(ctx, d, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.r.WorktreeRemove(ctx, wt, true); err != nil {
		t.Fatal("second remove:", err)
	}
	if err := tr.r.BranchDelete(ctx, "tiegel/plan-p1", true); err != nil {
		t.Fatal(err)
	}
	if err := tr.r.BranchDelete(ctx, "tiegel/plan-p1", true); err == nil {
		t.Fatal("deleted twice")
	}
}
