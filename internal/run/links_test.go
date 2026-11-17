package run

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/compose"
)

func TestSplitAndQuoteWords(t *testing.T) {
	for in, want := range map[string][]string{
		"":                       nil,
		"  a  b\tc ":             {"a", "b", "c"},
		`-d "memory_limit=-1" x`: {"-d", "memory_limit=-1", "x"},
		`'it''s' "a \"b\" \\"`:   {"its", `a "b" \`},
		`a\ b c`:                 {"a b", "c"},
		`""`:                     {""},
		`x"y z"w`:                {"xy zw"},
	} {
		got, err := SplitWords(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("SplitWords(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{`'open`, `"open`, `a "b`} {
		if _, err := SplitWords(bad); err == nil {
			t.Errorf("SplitWords(%q) accepted", bad)
		}
	}
	if got := QuoteWords([]string{"php", "a b", "", "it's"}); got != `php 'a b' '' 'it'\''s'` {
		t.Fatalf("quote %s", got)
	}
}

func TestLinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	m := compose.Mappings{}.Set(root, "/app")
	l := Linker{Mappings: m, Dirs: []string{root}}
	src := func(p string) string { return filepath.Join(root, filepath.FromSlash(p)) }
	cases := []struct {
		text  string
		links []Link
	}{
		{"PHP Fatal error:  Uncaught Exception: boom in /app/src/X.php:12", []Link{{Start: 46, End: 63, Path: "/app/src/X.php", File: src("src/X.php"), Line: 12}}},
		{"#0 /app/vendor/a/b.php(34): Foo->bar()", []Link{{Start: 3, End: 26, Path: "/app/vendor/a/b.php", File: src("vendor/a/b.php"), Line: 34}}},
		{"Warning: Undefined variable $x in /app/index.php on line 7", []Link{{Start: 34, End: 58, Path: "/app/index.php", File: src("index.php"), Line: 7}}},
		{"at src/Foo.php:10:5 and tests/FooTest.php:3", []Link{
			{Start: 3, End: 19, Path: "src/Foo.php", File: src("src/Foo.php"), Line: 10, Column: 5},
			{Start: 24, End: 43, Path: "tests/FooTest.php", File: src("tests/FooTest.php"), Line: 3}}},
		{"see http://example.com:8080/x and version 8.3.4: ok", nil},
		{"no location here: 12", nil},
		{"file.php:0", nil},
	}
	for _, c := range cases {
		if got := l.Links(c.text); !reflect.DeepEqual(got, c.links) {
			t.Errorf("Links(%q)\n got %+v\nwant %+v", c.text, got, c.links)
		}
	}
	// Existence decides between bases and rejects unknown files.
	other := filepath.Join(t.TempDir(), "wd")
	l2 := Linker{Mappings: m, Dirs: []string{other, root}, Exists: func(p string) bool { return p == src("a.php") }}
	if got := l2.Links("a.php:1 b.php:2"); len(got) != 1 || got[0].File != src("a.php") {
		t.Fatalf("exists %+v", got)
	}
	// Absolute host paths stay host paths.
	host := filepath.Join(other, "x.php")
	l3 := Linker{}
	if got := l3.Links(host + ":4"); len(got) != 1 || got[0].File != host || got[0].Line != 4 {
		t.Fatalf("host %+v", got)
	}
	if runtime.GOOS != "windows" {
		if got := (Linker{Exists: func(string) bool { return false }}).Links("/nope/x.php:1"); got != nil {
			t.Fatalf("missing host file linked %+v", got)
		}
	}
	if got := StripANSI("\x1b[1;31mred\x1b[0m plain\x1b(B"); got != "red plain" {
		t.Fatalf("ansi %q", got)
	}
}
