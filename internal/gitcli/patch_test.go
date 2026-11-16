package gitcli

import (
	"errors"
	"strings"
	"testing"
)

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line " + itoa(i) + "\n")
	}
	return b.String()
}

func replaceLine(s string, n int, with string) string {
	ls := strings.SplitAfter(s, "\n")
	ls[n-1] = with
	return strings.Join(ls, "")
}

func worktreeDiff(t *testing.T, tr *testRepo, path string) *FileDiff {
	t.Helper()
	files, err := tr.r.Diff(ctxT(t), DiffOptions{Paths: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("diff of %s: %d files", path, len(files))
	}
	return &files[0]
}

func TestStageSelectionHunk(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	orig := numbered(30)
	tr.write("f.txt", orig)
	tr.commitAll("init")
	mod := replaceLine(replaceLine(orig, 2, "TWO\n"), 28, "TWENTY-EIGHT\n")
	tr.write("f.txt", mod)

	fd := worktreeDiff(t, tr, "f.txt")
	if len(fd.Hunks) != 2 {
		t.Fatalf("hunks: %d", len(fd.Hunks))
	}
	if err := tr.r.StageSelection(ctx, fd, []HunkSelection{{Hunk: 1}}); err != nil {
		t.Fatal(err)
	}
	staged, _ := tr.r.ShowIndexFile(ctx, 0, "f.txt")
	if string(staged) != replaceLine(orig, 28, "TWENTY-EIGHT\n") {
		t.Errorf("index content:\n%s", staged)
	}
	if tr.read("f.txt") != mod {
		t.Error("work tree changed")
	}

	// Unstage it again via the staged diff.
	files, err := tr.r.Diff(ctx, DiffOptions{Staged: true})
	if err != nil || len(files) != 1 {
		t.Fatalf("staged diff: %v %v", files, err)
	}
	if err := tr.r.UnstageSelection(ctx, &files[0], files[0].SelectAll()); err != nil {
		t.Fatal(err)
	}
	staged, _ = tr.r.ShowIndexFile(ctx, 0, "f.txt")
	if string(staged) != orig {
		t.Errorf("after unstage:\n%s", staged)
	}

	// Discard the first hunk in the work tree.
	fd = worktreeDiff(t, tr, "f.txt")
	if err := tr.r.DiscardSelection(ctx, fd, []HunkSelection{{Hunk: 0}}); err != nil {
		t.Fatal(err)
	}
	if got := tr.read("f.txt"); got != replaceLine(orig, 28, "TWENTY-EIGHT\n") {
		t.Errorf("after discard:\n%s", got)
	}
}

func TestStageSelectionLines(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("f.txt", "a\nb\nc\nd\n")
	tr.commitAll("init")
	tr.write("f.txt", "a\nB\nx\ny\nc\n") // b->B, +x +y, -d
	fd := worktreeDiff(t, tr, "f.txt")
	h := fd.Hunks[0]
	idx := func(kind LineKind, text string) int {
		for i, l := range h.Lines {
			if l.Kind == kind && l.Text == text {
				return i
			}
		}
		t.Fatalf("line %c%s not found", kind, text)
		return -1
	}
	// Stage only "+x" and "-d".
	sel := []HunkSelection{{Hunk: 0, Lines: []int{idx(LineAdded, "x"), idx(LineDeleted, "d")}}}
	if err := tr.r.StageSelection(ctx, fd, sel); err != nil {
		t.Fatal(err)
	}
	staged, _ := tr.r.ShowIndexFile(ctx, 0, "f.txt")
	if string(staged) != "a\nb\nx\nc\n" {
		t.Errorf("index: %q", staged)
	}

	// Unstage just "+x" from the staged diff.
	files, _ := tr.r.Diff(ctx, DiffOptions{Staged: true})
	sh := files[0].Hunks[0]
	var xi int
	for i, l := range sh.Lines {
		if l.Kind == LineAdded && l.Text == "x" {
			xi = i
		}
	}
	if err := tr.r.UnstageSelection(ctx, &files[0], []HunkSelection{{Hunk: 0, Lines: []int{xi}}}); err != nil {
		t.Fatal(err)
	}
	staged, _ = tr.r.ShowIndexFile(ctx, 0, "f.txt")
	if string(staged) != "a\nb\nc\n" {
		t.Errorf("index after unstage: %q", staged)
	}

	if _, err := fd.Patch([]HunkSelection{{Hunk: 0, Lines: []int{0}}}); !errors.Is(err, ErrEmptySelection) {
		t.Errorf("context only selection: %v", err)
	}
	if _, err := fd.Patch([]HunkSelection{{Hunk: 5}}); err == nil {
		t.Error("out of range hunk")
	}
}

// applyModel computes the expected result of applying a line selection of
// a single-hunk full-file diff, ignoring the final newline.
func applyModel(h Hunk, sel map[int]bool) string {
	var out []string
	for i, l := range h.Lines {
		switch {
		case l.Kind == LineContext:
			out = append(out, l.Text)
		case l.Kind == LineDeleted && !sel[i]:
			out = append(out, l.Text)
		case l.Kind == LineAdded && sel[i]:
			out = append(out, l.Text)
		}
	}
	return strings.Join(out, "\n")
}

func TestPatchNoEOLAllSubsets(t *testing.T) {
	cases := []struct{ old, new string }{
		{"keep\nlast", "keep\nlast changed"},
		{"keep\nlast", "keep\nlast\nmore\n"},
		{"keep\nlast\n", "keep\nlast\nmore"},
		{"a", "b\nc"},
		{"a\nb", "a"},
		{"a\nb", "x\ny\nz"},
		{"a\n", "a"},
		{"a", "a\n"},
	}
	for _, c := range cases {
		tr := newTestRepo(t)
		ctx := ctxT(t)
		tr.write("f", c.old)
		tr.commitAll("init")
		tr.write("f", c.new)
		fd := worktreeDiff(t, tr, "f")
		if len(fd.Hunks) != 1 {
			t.Fatalf("%q->%q: %d hunks", c.old, c.new, len(fd.Hunks))
		}
		h := fd.Hunks[0]
		var changeIdx []int
		for i, l := range h.Lines {
			if l.Kind != LineContext {
				changeIdx = append(changeIdx, i)
			}
		}
		for mask := 1; mask < 1<<len(changeIdx); mask++ {
			var lines []int
			sel := map[int]bool{}
			for b, i := range changeIdx {
				if mask&(1<<b) != 0 {
					lines = append(lines, i)
					sel[i] = true
				}
			}
			tr.git("reset", "-q")
			err := tr.r.StageSelection(ctx, fd, []HunkSelection{{Hunk: 0, Lines: lines}})
			if err != nil {
				p, _ := fd.Patch([]HunkSelection{{Hunk: 0, Lines: lines}})
				t.Fatalf("%q->%q mask %b: %v\n%s", c.old, c.new, mask, err, p)
			}
			staged, _ := tr.r.ShowIndexFile(ctx, 0, "f")
			got := strings.TrimSuffix(string(staged), "\n")
			if want := applyModel(h, sel); got != want {
				t.Errorf("%q->%q mask %b: index %q, want %q", c.old, c.new, mask, staged, want)
			}
			if mask == 1<<len(changeIdx)-1 && string(staged) != c.new {
				t.Errorf("%q->%q full selection: %q", c.old, c.new, staged)
			}
		}
	}
}

func TestPatchFileOperations(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("del.txt", "one\ntwo\nthree\n")
	tr.write("ren.txt", numbered(20))
	tr.write("mode.sh", "echo\n")
	tr.commitAll("init")
	tr.git("rm", "-q", "del.txt")
	tr.git("mv", "ren.txt", "moved.txt")
	tr.write("moved.txt", replaceLine(numbered(20), 20, "last\n"))
	tr.git("update-index", "--chmod=+x", "mode.sh")
	tr.write("new.txt", "n1\nn2\n")
	tr.git("add", "-A")
	files, err := tr.r.Diff(ctx, DiffOptions{Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	tr.git("reset", "-q")

	for _, f := range files {
		f := f
		switch f.Path() {
		case "del.txt":
			// Partial deletion keeps the file.
			if err := tr.r.StageSelection(ctx, &f, []HunkSelection{{Hunk: 0, Lines: []int{0}}}); err != nil {
				t.Fatal(err)
			}
			if b, _ := tr.r.ShowIndexFile(ctx, 0, "del.txt"); string(b) != "two\nthree\n" {
				t.Errorf("partial delete: %q", b)
			}
			tr.git("reset", "-q")
			if err := tr.r.StageSelection(ctx, &f, f.SelectAll()); err != nil {
				t.Fatal(err)
			}
			if _, err := tr.r.ShowIndexFile(ctx, 0, "del.txt"); !errors.Is(err, ErrNotFound) {
				t.Errorf("full delete: %v", err)
			}
		case "moved.txt":
			// Rename without hunks: header only patch.
			p, err := f.Patch(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(p), "rename from ren.txt\nrename to moved.txt\n") || strings.Contains(string(p), "@@") {
				t.Errorf("rename patch:\n%s", p)
			}
			if err := tr.r.ApplyPatch(ctx, p, ApplyOptions{Cached: true}); err != nil {
				t.Fatal(err)
			}
			if b, _ := tr.r.ShowIndexFile(ctx, 0, "moved.txt"); string(b) != numbered(20) {
				t.Errorf("renamed content: %q", b)
			}
		case "mode.sh":
			p, err := f.Patch(nil)
			if err != nil || !strings.Contains(string(p), "old mode 100644\nnew mode 100755\n") {
				t.Fatalf("mode patch %v:\n%s", err, p)
			}
			if err := tr.r.ApplyPatch(ctx, p, ApplyOptions{Cached: true, Check: true}); err != nil {
				t.Fatal(err)
			}
		case "new.txt":
			if err := tr.r.StageSelection(ctx, &f, []HunkSelection{{Hunk: 0, Lines: []int{1}}}); err != nil {
				t.Fatal(err)
			}
			if b, _ := tr.r.ShowIndexFile(ctx, 0, "new.txt"); string(b) != "n2\n" {
				t.Errorf("partial new: %q", b)
			}
		default:
			t.Errorf("unexpected file %q", f.Path())
		}
	}

	bin := &FileDiff{NewPath: "b", OldPath: "b", IsBinary: true}
	if _, err := bin.Patch(nil); err == nil {
		t.Error("binary patch must fail")
	}
}

func TestPatchMultipleHunksRecount(t *testing.T) {
	// Selecting hunks 0 and 2 of three requires recomputed new starts.
	tr := newTestRepo(t)
	ctx := ctxT(t)
	orig := numbered(60)
	tr.write("f", orig)
	tr.commitAll("init")
	mod := strings.Replace(orig, "line 5\n", "line 5\nadd A1\nadd A2\n", 1)
	mod = strings.Replace(mod, "line 30\n", "", 1)
	mod = strings.Replace(mod, "line 55\n", "line 55\nadd C\n", 1)
	tr.write("f", mod)
	fd := worktreeDiff(t, tr, "f")
	if len(fd.Hunks) != 3 {
		t.Fatalf("hunks %d", len(fd.Hunks))
	}
	// Reverse order on purpose; Patch sorts.
	p, err := fd.Patch([]HunkSelection{{Hunk: 2}, {Hunk: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p), "@@ -53,6 +52,7 @@") {
		t.Errorf("recomputed header missing:\n%s", p)
	}
	if err := tr.r.ApplyPatch(ctx, p, ApplyOptions{Cached: true}); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(strings.Replace(orig, "line 30\n", "", 1), "line 55\n", "line 55\nadd C\n", 1)
	if b, _ := tr.r.ShowIndexFile(ctx, 0, "f"); string(b) != want {
		t.Errorf("index:\n%s", b)
	}
}

func TestDiffVariants(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	// Unborn branch: Staged and Base HEAD use the empty tree.
	tr.write("a.txt", "a\n")
	tr.git("add", "a.txt")
	files, err := tr.r.Diff(ctx, DiffOptions{Staged: true})
	if err != nil || len(files) != 1 || !files[0].IsNew {
		t.Fatalf("unborn staged: %+v %v", files, err)
	}
	files, err = tr.r.Diff(ctx, DiffOptions{Base: "HEAD"})
	if err != nil || len(files) != 1 {
		t.Fatalf("unborn HEAD: %+v %v", files, err)
	}
	first := tr.commitAll("one")
	tr.write("a.txt", "a\nb\n")
	second := tr.commitAll("two")
	files, err = tr.r.Diff(ctx, DiffOptions{Base: first, Target: second, Context: 1, NoRenames: true})
	if err != nil || len(files) != 1 || files[0].Hunks[0].NewLines != 2 {
		t.Fatalf("rev range: %+v %v", files, err)
	}
	if _, err := tr.r.Diff(ctx, DiffOptions{Base: "does-not-exist"}); err == nil {
		t.Error("bad base must fail")
	}

	tr.write("untracked.txt", "u1\nu2\n")
	fd, err := tr.r.DiffUntracked(ctx, "untracked.txt")
	if err != nil || fd == nil || !fd.IsNew || fd.Path() != "untracked.txt" || len(fd.Hunks) != 1 {
		t.Fatalf("untracked: %+v %v", fd, err)
	}
	tr.write("empty.txt", "")
	if fd, err := tr.r.DiffUntracked(ctx, "empty.txt"); err != nil || (fd != nil && len(fd.Hunks) != 0) {
		t.Errorf("empty untracked: %+v %v", fd, err)
	}
	// Stage part of an untracked file through its creation diff.
	fd, _ = tr.r.DiffUntracked(ctx, "untracked.txt")
	if err := tr.r.StageSelection(ctx, fd, []HunkSelection{{Hunk: 0, Lines: []int{0}}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := tr.r.ShowIndexFile(ctx, 0, "untracked.txt"); string(b) != "u1\n" {
		t.Errorf("partial untracked: %q", b)
	}
	if err := tr.r.ApplyPatch(ctx, []byte("garbage"), ApplyOptions{Cached: true}); err == nil {
		t.Error("garbage patch must fail")
	}
}

func TestStageUnstage(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a", "a\n")
	if err := tr.r.Stage(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	// Unborn: unstage removes from the index.
	if err := tr.r.Unstage(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if out := tr.git("ls-files"); out != "" {
		t.Errorf("ls-files after unborn unstage: %q", out)
	}
	tr.commitAll("a")
	tr.write("a", "b\n")
	tr.write("*", "literal star\n")
	if err := tr.r.Stage(ctx, "a", "*"); err != nil {
		t.Fatal(err)
	}
	if err := tr.r.Unstage(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if out := tr.git("diff", "--cached", "--name-only"); out != "*" {
		t.Errorf("staged after unstage: %q", out)
	}
	if err := tr.r.Stage(ctx); err != nil {
		t.Error(err)
	}
	if err := tr.r.Unstage(ctx); err != nil {
		t.Error(err)
	}
	if err := tr.r.Stage(ctx, "missing-file"); err == nil {
		t.Error("staging a missing path must fail")
	}
}

func TestInvert(t *testing.T) {
	files, err := ParseDiff(readFixture(t, "mixed.diff"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		inv := f.Invert()
		if inv.OldPath != f.NewPath || inv.IsNew != f.IsDeleted || inv.OldMode != f.NewMode {
			t.Errorf("invert %s: %+v", f.Path(), inv)
		}
		back := inv.Invert()
		a1, d1 := f.Stats()
		a2, d2 := inv.Stats()
		a3, d3 := back.Stats()
		if a1 != d2 || d1 != a2 || a1 != a3 || d1 != d3 {
			t.Errorf("stats %s", f.Path())
		}
	}
}

func TestUnstageNoEOLAllSubsets(t *testing.T) {
	cases := []struct{ old, new string }{
		{"keep\nlast", "keep\nlast changed"},
		{"keep\nlast", "keep\nlast\nmore\n"},
		{"a\nb", "x\ny\nz"},
		{"a", "a\n"},
	}
	for _, c := range cases {
		tr := newTestRepo(t)
		ctx := ctxT(t)
		tr.write("f", c.old)
		tr.commitAll("init")
		tr.write("f", c.new)
		for mask := 1; ; mask++ {
			tr.git("add", "f")
			files, err := tr.r.Diff(ctx, DiffOptions{Staged: true})
			if err != nil || len(files) != 1 {
				t.Fatalf("staged diff %v", err)
			}
			h := files[0].Hunks[0]
			var changeIdx []int
			for i, l := range h.Lines {
				if l.Kind != LineContext {
					changeIdx = append(changeIdx, i)
				}
			}
			if mask >= 1<<len(changeIdx) {
				break
			}
			var lines []int
			keep := map[int]bool{}
			for b, i := range changeIdx {
				if mask&(1<<b) != 0 {
					lines = append(lines, i)
				} else {
					keep[i] = true
				}
			}
			if err := tr.r.UnstageSelection(ctx, &files[0], []HunkSelection{{Hunk: 0, Lines: lines}}); err != nil {
				p, _ := files[0].Invert().Patch([]HunkSelection{{Hunk: 0, Lines: lines}})
				t.Fatalf("%q->%q mask %b: %v\n%s", c.old, c.new, mask, err, p)
			}
			staged, _ := tr.r.ShowIndexFile(ctx, 0, "f")
			if got, want := strings.TrimSuffix(string(staged), "\n"), applyModel(h, keep); got != want {
				t.Errorf("%q->%q mask %b: index %q, want %q", c.old, c.new, mask, staged, want)
			}
		}
	}
}
