package gitcli

import (
	"os"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func findFile(t *testing.T, files []FileDiff, path string) *FileDiff {
	t.Helper()
	for i := range files {
		if files[i].Path() == path {
			return &files[i]
		}
	}
	t.Fatalf("file %q not in diff", path)
	return nil
}

func TestParseDiffMixed(t *testing.T) {
	files, err := ParseDiff(readFixture(t, "mixed.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 8 {
		t.Fatalf("got %d files", len(files))
	}

	added := findFile(t, files, "added.txt")
	if !added.IsNew || added.OldPath != "" || added.NewMode != "100644" || len(added.Hunks) != 1 {
		t.Errorf("added: %+v", added)
	}
	if a, d := added.Stats(); a != 2 || d != 0 {
		t.Errorf("added stats %d/%d", a, d)
	}
	if l := added.Hunks[0].Lines[1]; l.Kind != LineAdded || l.Text != "new" || l.NewLine != 2 || l.OldLine != 0 {
		t.Errorf("added line: %+v", l)
	}

	bin := findFile(t, files, "bin.dat")
	if !bin.IsBinary || len(bin.Hunks) != 0 || bin.OldHash != "6772730" || bin.NewHash != "8d0cd2a" || bin.OldMode != "100644" {
		t.Errorf("binary: %+v", bin)
	}

	del := findFile(t, files, "del.txt")
	if !del.IsDeleted || del.NewPath != "" || del.OldMode != "100644" {
		t.Errorf("deleted: %+v", del)
	}

	mod := findFile(t, files, "mod.txt")
	if len(mod.Hunks) != 2 {
		t.Fatalf("mod hunks: %d", len(mod.Hunks))
	}
	h := mod.Hunks[1]
	if h.OldStart != 8 || h.OldLines != 3 || h.NewStart != 8 || h.NewLines != 4 || h.Section != "seven" {
		t.Errorf("hunk header: %+v", h)
	}
	if h.Header() != "@@ -8,3 +8,4 @@ seven" {
		t.Errorf("Header() = %q", h.Header())
	}
	last := h.Lines[3]
	if last.Kind != LineAdded || last.Text != "eleven" || last.NewLine != 11 {
		t.Errorf("last line %+v", last)
	}
	if ctx := h.Lines[0]; ctx.OldLine != 8 || ctx.NewLine != 8 || ctx.Kind != LineContext {
		t.Errorf("context line %+v", ctx)
	}

	ren := findFile(t, files, "new name.txt")
	if !ren.IsRename || ren.OldPath != "old name.txt" || ren.Similarity != 86 || len(ren.Hunks) != 1 {
		t.Errorf("rename: %+v", ren)
	}

	noeol := findFile(t, files, "noeol.txt")
	ls := noeol.Hunks[0].Lines
	if len(ls) != 3 || !ls[1].NoEOL || !ls[2].NoEOL || ls[0].NoEOL {
		t.Errorf("noeol lines: %+v", ls)
	}

	sh := findFile(t, files, "script.sh")
	if !sh.ModeChanged() || sh.OldMode != "100644" || sh.NewMode != "100755" || len(sh.Hunks) != 0 {
		t.Errorf("mode: %+v", sh)
	}

	weird := findFile(t, files, "we\tird.txt")
	if weird.OldPath != "we\tird.txt" || len(weird.Hunks) != 1 {
		t.Errorf("quoted: %+v", weird)
	}
	if h := weird.Hunks[0]; h.OldLines != 1 || h.NewLines != 1 || h.Header() != "@@ -1 +1 @@" {
		t.Errorf("single line hunk: %+v", h)
	}
}

func TestParseDiffPlain(t *testing.T) {
	files, err := ParseDiff(readFixture(t, "plain.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files: %+v", len(files), files)
	}
	f := files[0]
	if f.OldPath != "file.php" || f.NewPath != "file.php" {
		t.Errorf("paths %q %q", f.OldPath, f.NewPath)
	}
	ls := f.Hunks[0].Lines
	if len(ls) != 5 || ls[1].Kind != LineDeleted || ls[1].Text != "-- a line that starts with dashes" ||
		ls[2].Kind != LineAdded || ls[3].Kind != LineContext || ls[3].Text != "" {
		t.Errorf("lines %+v", ls)
	}
	if files[1].Path() != "other.txt" || files[1].Hunks[0].NewStart != 0 || files[1].Hunks[0].NewLines != 0 {
		t.Errorf("second file %+v", files[1])
	}
}

func TestParseDiffSpecial(t *testing.T) {
	files, err := ParseDiff(readFixture(t, "special.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("got %d files: %+v", len(files), files)
	}
	if !files[0].IsCombined || files[0].Path() != "conflict.txt" || len(files[0].Hunks) != 0 {
		t.Errorf("combined %+v", files[0])
	}
	if !files[1].IsBinary || !files[1].IsNew || files[1].Path() != "img.png" {
		t.Errorf("binary patch %+v", files[1])
	}
	if !files[2].IsCopy || files[2].OldPath != "copy.txt" || files[2].NewPath != "copied.txt" || files[2].Similarity != 100 {
		t.Errorf("copy %+v", files[2])
	}
	if files[3].Path() != "same name.txt" || files[3].OldPath != "same name.txt" || !files[3].IsBinary {
		t.Errorf("spaces %+v", files[3])
	}
	if _, err := files[0].Patch(nil); err == nil {
		t.Error("patch of combined diff must fail")
	}
}

func TestParseDiffErrors(t *testing.T) {
	bad := []string{
		"diff --git a/x b/x\n@@ -1,2 +1,2 @@\n a\n",   // truncated
		"diff --git a/x b/x\n@@ -a +1 @@\n",           // bad numbers
		"diff --git a/x b/x\n@@ -1 +1\n",              // no closing @@
		"diff --git a/x b/x\n@@ -1 +1 @@\n*x\n",       // bad prefix
		"diff --git a/x b/x\nsimilarity index abc%\n", // bad similarity
		"diff --git a/x b/x\n@@ 1 +1 @@\n",            // missing minus
	}
	for _, s := range bad {
		if _, err := ParseDiff([]byte(s)); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
	files, err := ParseDiff(nil)
	if err != nil || len(files) != 0 {
		t.Errorf("empty: %v %v", files, err)
	}
}

func TestHeaderPaths(t *testing.T) {
	cases := []struct{ in, a, b string }{
		{"a/x b/x", "x", "x"},
		{"a/dir with space/f b/dir with space/f", "dir with space/f", "dir with space/f"},
		{"a/old b/new", "old", "new"},
		{`"a/t\tab" "b/t\tab"`, "t\tab", "t\tab"},
		{`a/plain "b/q\"uote"`, "plain", `q"uote`},
		{"x y", "x", "y"},
	}
	for _, c := range cases {
		a, b := parseGitHeaderPaths(c.in)
		if a != c.a || b != c.b {
			t.Errorf("parseGitHeaderPaths(%q) = %q, %q", c.in, a, b)
		}
	}
	if a, b := parseGitHeaderPaths(`"a/unterminated`); a != "" || b != "" {
		t.Errorf("unterminated: %q %q", a, b)
	}
}

func TestQuoting(t *testing.T) {
	for _, p := range []string{"plain.txt", "sp ace", "t\tab", "new\nline", `back\slash`, `"q"`, "bell\a", "ü.txt"} {
		q := quotePath("a/", p)
		if got := stripPrefix(unquotePath(q)); got != p {
			t.Errorf("roundtrip %q -> %q -> %q", p, q, got)
		}
	}
	if got := unquotePath(`"\303\274\a\b\f\n\r\t\v\\\"\9"`); got != "ü\a\b\f\n\r\t\v\\\"9" {
		t.Errorf("unquote escapes: %q", got)
	}
	if got := unquotePath(`"\3"`); got != "3" {
		t.Errorf("short octal: %q", got)
	}
	if !strings.HasPrefix(quotePath("a/", "x\x01"), `"a/x\001`) {
		t.Errorf("control char quoting: %s", quotePath("a/", "x\x01"))
	}
}
