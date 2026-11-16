package gitcli

import (
	"strings"
	"testing"
)

func TestParseStatusFixture(t *testing.T) {
	recs := []string{
		"# branch.oid 1234567890abcdef",
		"# branch.head feature",
		"# branch.upstream origin/feature",
		"# branch.ab +2 -3",
		"1 .M N... 100644 100644 100644 aaaa bbbb mod with space.txt",
		"1 A. N... 000000 100644 100644 0000 cccc added.txt",
		"2 R. N... 100644 100644 100644 dddd eeee R87 new.txt",
		"old.txt",
		"2 C. N... 100644 100644 100644 dddd eeee C100 copy.txt",
		"src.txt",
		"u UU N... 100644 100644 100644 100644 h1 h2 h3 conflict.txt",
		"? untracked.txt",
		"! ignored.log",
	}
	st, err := ParseStatus([]byte(strings.Join(recs, "\x00") + "\x00"))
	if err != nil {
		t.Fatal(err)
	}
	b := st.Branch
	if b.OID != "1234567890abcdef" || b.Head != "feature" || b.Upstream != "origin/feature" ||
		!b.HasAheadBeh || b.Ahead != 2 || b.Behind != 3 || b.Detached || b.Initial {
		t.Errorf("branch: %+v", b)
	}
	if len(st.Files) != 7 {
		t.Fatalf("files: %+v", st.Files)
	}
	f := st.Files[0]
	if f.Path != "mod with space.txt" || f.Index != '.' || f.Worktree != 'M' || f.HasStaged() || !f.HasUnstaged() {
		t.Errorf("mod: %+v", f)
	}
	if f := st.Files[1]; !f.HasStaged() || f.HasUnstaged() || f.Modes[0] != "000000" {
		t.Errorf("added: %+v", f)
	}
	if f := st.Files[2]; f.Kind != KindRenamed || f.OrigPath != "old.txt" || f.Score != 87 || f.Copied {
		t.Errorf("rename: %+v", f)
	}
	if f := st.Files[3]; !f.Copied || f.OrigPath != "src.txt" || f.Score != 100 {
		t.Errorf("copy: %+v", f)
	}
	if f := st.Files[4]; !f.IsConflict() || f.Hashes[2] != "h3" || f.Path != "conflict.txt" || f.HasStaged() || f.HasUnstaged() {
		t.Errorf("conflict: %+v", f)
	}
	if c := st.Conflicts(); len(c) != 1 {
		t.Errorf("conflicts: %+v", c)
	}
	if f := st.Files[5]; f.Kind != KindUntracked || f.Path != "untracked.txt" || !f.HasUnstaged() {
		t.Errorf("untracked: %+v", f)
	}
	if f := st.Files[6]; f.Kind != KindIgnored || f.Path != "ignored.log" {
		t.Errorf("ignored: %+v", f)
	}
	for k, want := range map[EntryKind]string{KindOrdinary: "ordinary", KindRenamed: "renamed", KindUnmerged: "unmerged", KindUntracked: "untracked", KindIgnored: "ignored", 99: "unknown"} {
		if k.String() != want {
			t.Errorf("%d.String() = %q", k, k.String())
		}
	}

	st, err = ParseStatus([]byte("# branch.oid (initial)\x00# branch.head (detached)\x00"))
	if err != nil || !st.Branch.Initial || !st.Branch.Detached || st.Branch.HasAheadBeh {
		t.Errorf("initial: %+v %v", st, err)
	}
}

func TestParseStatusErrors(t *testing.T) {
	for _, s := range []string{
		"1 M N... x",
		"2 R. N... 100644 100644 100644 d e R87 new.txt",
		"2 R. N... 100644 100644 100644 d e Rxx new.txt\x00old",
		"2 R. N... 100644 100644 100644 d e  new.txt\x00old",
		"u UU short",
		"X what",
	} {
		if _, err := ParseStatus([]byte(s)); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}

func TestStatusRealRepo(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("keep.txt", "keep\n")
	tr.write("rename-me.txt", strings.Repeat("content line\n", 20))
	tr.write(".gitignore", "*.log\n")
	tr.commitAll("init")

	tr.write("keep.txt", "changed\n")
	tr.git("mv", "rename-me.txt", "renamed.txt")
	tr.write("dir/new file.txt", "new\n")
	tr.write("debug.log", "x\n")

	st, err := tr.r.Status(ctx, StatusOptions{Ignored: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch.Head != "main" || st.Branch.OID == "" || st.Branch.Upstream != "" {
		t.Errorf("branch: %+v", st.Branch)
	}
	got := map[string]FileStatus{}
	for _, f := range st.Files {
		got[f.Path] = f
	}
	if f := got["keep.txt"]; f.Worktree != 'M' {
		t.Errorf("keep: %+v", f)
	}
	if f := got["renamed.txt"]; f.Kind != KindRenamed || f.OrigPath != "rename-me.txt" {
		t.Errorf("renamed: %+v", f)
	}
	if f := got["dir/new file.txt"]; f.Kind != KindUntracked {
		t.Errorf("untracked (all mode lists files, not dirs): %+v", got)
	}
	if f := got["debug.log"]; f.Kind != KindIgnored {
		t.Errorf("ignored: %+v", f)
	}

	st, err = tr.r.Status(ctx, StatusOptions{NoUntracked: true, Paths: []string{"keep.txt"}})
	if err != nil || len(st.Files) != 1 || st.Files[0].Path != "keep.txt" {
		t.Errorf("limited status: %+v %v", st, err)
	}
}

func TestStatusAheadBehind(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("a", "1\n")
	tr.commitAll("1")
	tr.git("branch", "base")
	tr.git("branch", "--set-upstream-to=base")
	tr.write("a", "2\n")
	tr.commitAll("2")
	st, err := tr.r.Status(ctx, StatusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch.Upstream != "base" || st.Branch.Ahead != 1 || st.Branch.Behind != 0 || !st.Branch.HasAheadBeh {
		t.Errorf("ahead/behind: %+v", st.Branch)
	}
}
