package editorconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	top := t.TempDir()
	proj := filepath.Join(top, "proj")
	// Above the root file: must not be read.
	write(t, filepath.Join(top, FileName), "[*]\nindent_size = 8\ncharset = latin1\n")
	write(t, filepath.Join(proj, FileName), `root = true
# comment
[*]
indent_style = space
indent_size = 4
end_of_line = lf
charset = utf-8
trim_trailing_whitespace = true
insert_final_newline = true

[*.{yml,yaml}]
indent_size = 2

[Makefile]
indent_style = tab

[/lib/**.php]
indent_size = 3 ; inline

[file{1..3}.txt]
tab_width = 5

[[broken
x = y
`)
	write(t, filepath.Join(proj, "sub", FileName), "[*.md]\ntrim_trailing_whitespace = unset\nindent_size: 6\n")

	get := func(rel string) Props {
		t.Helper()
		p, err := Resolve(filepath.Join(proj, rel), proj)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := get("src/a.php")
	if p.IndentStyle != "space" || p.IndentSize != 4 || p.TabWidth != 4 || p.EndOfLine != "lf" || p.Charset != "utf-8" ||
		p.TrimTrailingWhitespace == nil || !*p.TrimTrailingWhitespace || p.InsertFinalNewline == nil || !*p.InsertFinalNewline {
		t.Fatalf("php: %+v", p)
	}
	if len(p.Files) != 1 || p.Empty() {
		t.Fatalf("files %v", p.Files)
	}
	if p := get("config/app.yaml"); p.IndentSize != 2 {
		t.Fatalf("yaml: %+v", p)
	}
	if p := get("Makefile"); p.IndentStyle != "tab" || p.IndentSize != 4 {
		t.Fatalf("makefile: %+v", p)
	}
	if p := get("lib/x/y.php"); p.IndentSize != 3 {
		t.Fatalf("lib: %+v", p)
	}
	if p := get("other/lib/y.php"); p.IndentSize != 4 {
		t.Fatalf("anchored glob matched elsewhere: %+v", p)
	}
	if p := get("file2.txt"); p.TabWidth != 5 {
		t.Fatalf("range: %+v", p)
	}
	if p := get("file4.txt"); p.TabWidth != 4 {
		t.Fatalf("range miss: %+v", p)
	}
	p = get("sub/readme.md")
	if p.TrimTrailingWhitespace != nil || p.IndentSize != 6 || len(p.Files) != 2 {
		t.Fatalf("unset/closer: %+v", p)
	}

	// Without root=true the search continues up to stop.
	write(t, filepath.Join(proj, FileName), "[*]\nindent_style = tab\ntab_width = 2\nindent_size = tab\n")
	p = get("x.go")
	if p.IndentStyle != "tab" || p.TabWidth != 2 || p.IndentSize != 2 || p.Charset != "" {
		t.Fatalf("stop: %+v", p)
	}
	p, err := Resolve(filepath.Join(proj, "x.go"), "")
	if err != nil || p.Charset != "latin1" || len(p.Files) < 2 {
		t.Fatalf("no stop: %+v %v", p, err)
	}

	// No files at all.
	empty := t.TempDir()
	if p, err := Resolve(filepath.Join(empty, "a"), empty); err != nil || !p.Empty() {
		t.Fatalf("empty: %+v %v", p, err)
	}
}

func TestGlobs(t *testing.T) {
	cases := []struct {
		glob, path string
		want       bool
	}{
		{"*", "/d/a/b.c", true},
		{"*.c", "/d/x.c", true},
		{"a/*.c", "/d/a/x.c", true},
		{"a/*.c", "/d/a/b/x.c", false},
		{"a/**.c", "/d/a/b/x.c", true},
		{"?.c", "/d/x.c", true},
		{"?.c", "/d/xy.c", false},
		{"[ab].c", "/d/a.c", true},
		{"[!ab].c", "/d/a.c", false},
		{"{x,y}.c", "/d/y.c", true},
		{"{single}.c", "/d/{single}.c", true},
		{"a\\*.c", "/d/a*.c", true},
		{"a]b", "/d/a]b", true},
		{"[x", "/d/[x", true},
		{"a,b", "/d/a,b", true},
		{"{a,b", "/d/a", true},
	}
	for _, c := range cases {
		re, err := compileGlob("/d", c.glob)
		if err != nil {
			t.Errorf("%s: %v", c.glob, err)
			continue
		}
		if got := re.MatchString(c.path); got != c.want {
			t.Errorf("%s ~ %s = %v (re %s)", c.glob, c.path, got, re)
		}
	}
	if _, err := compileGlob("/d", ""); err == nil {
		t.Fatal("empty glob")
	}
	if _, _, ok := numRange("a..b"); ok {
		t.Fatal("bad range")
	}
}

func TestApplyAndCharset(t *testing.T) {
	yes, no := true, false
	p := Props{TrimTrailingWhitespace: &yes, InsertFinalNewline: &yes}
	if got := string(p.Apply([]byte("a  \r\nb\t\r\nc "))); got != "a\r\nb\r\nc\r\n" {
		t.Fatalf("crlf: %q", got)
	}
	if got := string(p.Apply([]byte("x\ny"))); got != "x\ny\n" {
		t.Fatalf("lf: %q", got)
	}
	in := []byte("clean\n")
	if got := p.Apply(in); &got[0] != &in[0] {
		t.Fatal("unchanged content copied")
	}
	if got := string(p.Apply(nil)); got != "" {
		t.Fatal("empty")
	}
	for eol, want := range map[string]string{"crlf": "x\r\n", "cr": "x\r", "": "x\n"} {
		if got := string((Props{InsertFinalNewline: &yes, EndOfLine: eol}).Apply([]byte("x"))); got != want {
			t.Errorf("eol %s: %q", eol, got)
		}
	}
	if got := string((Props{InsertFinalNewline: &no, TrimTrailingWhitespace: &no}).Apply([]byte("x \n\n"))); got != "x \n\n" {
		t.Fatalf("false: %q", got)
	}

	if CheckCharset("", []byte{0xff}) != "" || CheckCharset("utf-8", []byte("ok")) != "" {
		t.Fatal("ok cases")
	}
	for _, c := range []struct {
		cs   string
		data string
		want string
	}{
		{"utf-8", "\xff", "not valid"},
		{"utf-8", "\xEF\xBB\xBFx", "BOM"},
		{"utf-8-bom", "x", "no UTF-8 BOM"},
		{"latin1", "x", "not supported"},
	} {
		if got := CheckCharset(c.cs, []byte(c.data)); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q", c.cs, got)
		}
	}
	if CheckCharset("utf-8-bom", []byte("\xEF\xBB\xBFx")) != "" {
		t.Fatal("bom ok")
	}
}
