package gitcli

import (
	"errors"
	"strings"
	"testing"
)

func TestFileHistoryFollowsRenames(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	if h, err := tr.r.FileHistory(ctx, "a.txt", FileHistoryOptions{}); err != nil || h != nil {
		t.Fatalf("unborn: %v %v", h, err)
	}
	tr.write("a.txt", "a\nb\nc\n")
	tr.write("other.txt", "x\n")
	c1 := tr.commitAll("one")
	tr.write("a.txt", "a\nB\nc\n")
	c2 := tr.commitAll("two")
	tr.git("mv", "a.txt", "b.txt")
	c3 := tr.commitAll("move")
	tr.write("other.txt", "y\n")
	tr.commitAll("unrelated")
	tr.write("b.txt", "a\nB\nc\nd\n")
	c5 := tr.commitAll("four")

	h, err := tr.r.FileHistory(ctx, "b.txt", FileHistoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 4 {
		t.Fatalf("history %+v", h)
	}
	want := []struct {
		hash, path, old string
		status          byte
	}{{c5, "b.txt", "", 'M'}, {c3, "b.txt", "a.txt", 'R'}, {c2, "a.txt", "", 'M'}, {c1, "a.txt", "", 'A'}}
	for i, w := range want {
		g := h[i]
		if g.Hash != w.hash || g.Path != w.path || g.OldPath != w.old || g.Status != w.status {
			t.Errorf("%d: %+v, want %+v", i, g, w)
		}
	}
	if h[1].ParentPath() != "a.txt" || h[0].ParentPath() != "b.txt" || h[0].Subject != "four" || h[0].AuthorName != "Test User" {
		t.Errorf("fields %+v", h[0])
	}
	if len(h[0].Refs) == 0 || !strings.Contains(h[0].Refs[0], "main") {
		t.Errorf("refs %v", h[0].Refs)
	}
	// Paging.
	p, err := tr.r.FileHistory(ctx, "b.txt", FileHistoryOptions{Skip: 1, Max: 2})
	if err != nil || len(p) != 2 || p[0].Hash != c3 || p[1].Hash != c2 {
		t.Fatalf("page %+v %v", p, err)
	}
	// A page behind the rename (git's own --skip would lose the rename).
	p, err = tr.r.FileHistory(ctx, "b.txt", FileHistoryOptions{Skip: 2})
	if err != nil || len(p) != 2 || p[0].Hash != c2 || p[1].Hash != c1 {
		t.Fatalf("page behind rename %+v %v", p, err)
	}
	if p, err := tr.r.FileHistory(ctx, "b.txt", FileHistoryOptions{Skip: 9}); err != nil || p != nil {
		t.Fatalf("skip past end %+v %v", p, err)
	}
	if p, err := tr.r.FileHistory(ctx, "b.txt", FileHistoryOptions{Skip: 1, Max: 1, NoFollow: true}); err != nil || len(p) != 1 || p[0].Hash != c3 {
		t.Fatalf("no follow paging %+v %v", p, err)
	}
	// Without --follow the history stops at the rename.
	nf, err := tr.r.FileHistory(ctx, "b.txt", FileHistoryOptions{NoFollow: true})
	if err != nil || len(nf) != 2 {
		t.Fatalf("no follow %+v %v", nf, err)
	}
	if _, err := tr.r.FileHistory(ctx, "b.txt", FileHistoryOptions{Rev: "nope"}); err == nil {
		t.Fatal("bad rev accepted")
	}
}

func TestCompareRevisions(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("keep.txt", "k\n")
	tr.write("mod.txt", "m\n")
	tr.write("del.txt", "d\n")
	tr.write("ren.txt", strings.Repeat("line of text\n", 20))
	c1 := tr.commitAll("one")
	tr.write("mod.txt", "m2\n")
	tr.git("rm", "-q", "del.txt")
	tr.git("mv", "ren.txt", "renamed.txt")
	tr.write("new file.txt", "n\n")
	c2 := tr.commitAll("two")

	files, err := tr.r.CompareRevisions(ctx, c1, c2)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ChangedFile{}
	for _, f := range files {
		got[f.Path] = f
	}
	if len(files) != 4 || got["mod.txt"].Status != 'M' || got["del.txt"].Status != 'D' || got["new file.txt"].Status != 'A' {
		t.Fatalf("files %+v", files)
	}
	if r := got["renamed.txt"]; r.Status != 'R' || r.OldPath != "ren.txt" || r.Similarity != 100 {
		t.Fatalf("rename %+v", r)
	}
	// Against the work tree.
	tr.write("keep.txt", "changed\n")
	files, err = tr.r.CompareRevisions(ctx, "", "")
	if err != nil || len(files) != 1 || files[0].Path != "keep.txt" {
		t.Fatalf("work tree %+v %v", files, err)
	}
	if _, err := tr.r.CompareRevisions(ctx, "nope", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad rev: %v", err)
	}
	if _, err := parseNameStatus([]byte("R100\x00a")); err == nil {
		t.Fatal("truncated rename accepted")
	}
	if _, err := parseNameStatus([]byte("M")); err == nil {
		t.Fatal("truncated record accepted")
	}
	if _, err := parseFileHistory([]byte("\x1ebad")); err == nil {
		t.Fatal("bad record accepted")
	}
}

func TestFileAt(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	if _, _, err := tr.r.FileAt(ctx, "HEAD", "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unborn: %v", err)
	}
	tr.write("a.txt", "one\n")
	c1 := tr.commitAll("one")
	tr.write("a.txt", "two\n")
	tr.git("add", "a.txt")
	tr.write("a.txt", "three\n")
	for _, c := range []struct{ rev, want string }{{c1, "one\n"}, {"HEAD", "one\n"}, {"", "two\n"}} {
		b, ok, err := tr.r.FileAt(ctx, c.rev, "a.txt")
		if err != nil || !ok || string(b) != c.want {
			t.Errorf("%q: %q %v %v", c.rev, b, ok, err)
		}
	}
	if _, ok, err := tr.r.FileAt(ctx, "HEAD", "missing.txt"); ok || err != nil {
		t.Fatalf("missing: %v %v", ok, err)
	}
	if _, ok, err := tr.r.FileAt(ctx, "", "missing.txt"); ok || err != nil {
		t.Fatalf("missing in index: %v %v", ok, err)
	}
}

func TestBlame(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a.txt", "one\ntwo\nthree\n")
	c1 := tr.commitAll("first")
	tr.git("config", "user.name", "Other Person")
	tr.write("a.txt", "one\nTWO\nthree\n")
	c2 := tr.commitAll("second")
	tr.git("mv", "a.txt", "b.txt")
	tr.commitAll("move")

	b, err := tr.r.Blame(ctx, "b.txt", BlameOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Lines) != 3 || b.Lines[0].Hash != c1 || b.Lines[1].Hash != c2 || b.Lines[2].Hash != c1 {
		t.Fatalf("lines %+v", b.Lines)
	}
	if b.Lines[1].Line != 2 || b.Lines[1].OrigLine != 2 {
		t.Errorf("line numbers %+v", b.Lines[1])
	}
	cm := b.Commits[c2]
	if cm == nil || cm.Author != "Other Person" || cm.AuthorMail != "test@example.com" || cm.Summary != "second" ||
		cm.Filename != "a.txt" || cm.Previous != c1 || cm.PrevPath != "a.txt" || cm.AuthorTime.IsZero() || cm.CommitTime.IsZero() || cm.Committer == "" {
		t.Fatalf("commit %+v", cm)
	}
	if !b.Commits[c1].Boundary {
		t.Error("root commit not boundary")
	}
	// Unsaved buffer content: the new line is not committed yet.
	b, err = tr.r.Blame(ctx, "b.txt", BlameOptions{Contents: []byte("one\nTWO\ninserted\nthree\n")})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Lines) != 4 || b.Lines[2].Hash != UncommittedHash || !b.Commits[UncommittedHash].Uncommitted {
		t.Fatalf("contents %+v", b.Lines)
	}
	// At a revision.
	b, err = tr.r.Blame(ctx, "a.txt", BlameOptions{Rev: c1, IgnoreWhitespace: true})
	if err != nil || len(b.Lines) != 3 || b.Lines[1].Hash != c1 {
		t.Fatalf("rev %+v %v", b, err)
	}
	if _, err := tr.r.Blame(ctx, "missing.txt", BlameOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := tr.r.Blame(ctx, "b.txt", BlameOptions{Rev: "HEAD", Contents: []byte{}}); err == nil {
		t.Fatal("rev + contents accepted")
	}
	for _, bad := range []string{"xyz 1 1\n", strings.Repeat("a", 40) + " x 1\n", strings.Repeat("a", 40) + " 1 1 1\nauthor x\n"} {
		if _, err := ParseBlame([]byte(bad)); err == nil {
			t.Errorf("bad porcelain %q accepted", bad)
		}
	}
}
