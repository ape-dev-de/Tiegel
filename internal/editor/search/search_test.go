package search

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

func spans(ms []Match) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = fmt.Sprintf("%d-%d", m.Start, m.End)
	}
	return strings.Join(parts, ",")
}

func mustCompile(t *testing.T, p string, o Options) *Query {
	t.Helper()
	q, err := Compile(p, o)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestFindAllModes(t *testing.T) {
	doc := buffer.NewString("Foo foo $foo food foo.bar FOO").Snapshot()
	cases := []struct {
		pattern string
		opts    Options
		want    string
	}{
		{"foo", Options{}, "0-3,4-7,9-12,13-16,18-21,26-29"},
		{"foo", Options{CaseSensitive: true}, "4-7,9-12,13-16,18-21"},
		{"foo", Options{WholeWord: true}, "0-3,4-7,9-12,18-21,26-29"},
		{"foo", Options{WholeWord: true, CaseSensitive: true}, "4-7,9-12,18-21"},
		{"$foo", Options{WholeWord: true}, "8-12"},
		{"foo.bar", Options{}, "18-25"},
		{"fo+d?", Options{Regex: true, CaseSensitive: true}, "4-7,9-12,13-17,18-21"},
		{"^foo", Options{Regex: true}, "0-3"},
	}
	for _, c := range cases {
		q := mustCompile(t, c.pattern, c.opts)
		if got := spans(q.FindAll(doc, 0)); got != c.want {
			t.Errorf("%q %+v: got %s want %s", c.pattern, c.opts, got, c.want)
		}
	}
	q := mustCompile(t, "foo", Options{})
	if got := spans(q.FindAll(doc, 2)); got != "0-3,4-7" {
		t.Errorf("limit: %s", got)
	}
	if got := spans(q.FindAllIn(doc, 4, 16, 0)); got != "4-7,9-12,13-16" {
		t.Errorf("FindAllIn: %s", got)
	}
	if q.Pattern() != "foo" || q.Options() != (Options{}) {
		t.Error("accessors")
	}
}

func TestMultilineRegex(t *testing.T) {
	doc := buffer.NewString("a1\nb2\nc3").Snapshot()
	q := mustCompile(t, `^\w(\d)$`, Options{Regex: true})
	ms := q.FindAll(doc, 0)
	if spans(ms) != "0-2,3-5,6-8" {
		t.Fatalf("got %s", spans(ms))
	}
	if ms[1].Groups[2] != 4 || ms[1].Groups[3] != 5 {
		t.Fatalf("groups %v", ms[1].Groups)
	}
}

func TestCompileErrors(t *testing.T) {
	if _, err := Compile("", Options{}); !errors.Is(err, ErrEmptyPattern) {
		t.Errorf("empty: %v", err)
	}
	if _, err := Compile("(", Options{Regex: true}); err == nil {
		t.Error("invalid regex accepted")
	}
	if _, err := Compile("(", Options{}); err != nil {
		t.Errorf("literal paren: %v", err)
	}
}

func TestNextPrev(t *testing.T) {
	doc := buffer.NewString("ab ab ab").Snapshot()
	q := mustCompile(t, "ab", Options{})
	if m, ok := q.Next(doc, 1, false); !ok || m.Start != 3 {
		t.Errorf("Next(1) = %v %v", m, ok)
	}
	if m, ok := q.Next(doc, 3, false); !ok || m.Start != 3 {
		t.Errorf("Next(3) = %v", m)
	}
	if _, ok := q.Next(doc, 7, false); ok {
		t.Error("Next past end without wrap")
	}
	if m, ok := q.Next(doc, 7, true); !ok || m.Start != 0 {
		t.Errorf("Next wrap = %v", m)
	}
	if m, ok := q.Prev(doc, 6, false); !ok || m.Start != 3 {
		t.Errorf("Prev(6) = %v", m)
	}
	if _, ok := q.Prev(doc, 0, false); ok {
		t.Error("Prev before start without wrap")
	}
	if m, ok := q.Prev(doc, 0, true); !ok || m.Start != 6 {
		t.Errorf("Prev wrap = %v", m)
	}
	none := mustCompile(t, "zz", Options{})
	if _, ok := none.Next(doc, 0, true); ok {
		t.Error("no match expected")
	}
	if _, ok := none.Prev(doc, 0, true); ok {
		t.Error("no match expected")
	}
	// Empty matches advance.
	empty := mustCompile(t, "^", Options{Regex: true})
	two := buffer.NewString("a\nb").Snapshot()
	m, _ := empty.Next(two, 0, false)
	if m.Start != 2 || !m.Empty() {
		t.Errorf("empty Next = %v", m)
	}
}

func TestExpand(t *testing.T) {
	b := buffer.NewString("x = foo(1, 2);")
	q := mustCompile(t, `(?P<fn>\w+)\((\d), (\d)\)`, Options{Regex: true})
	m, _ := q.Next(b.Snapshot(), 0, false)
	if got := string(q.Expand(b.Snapshot(), m, `${fn}($3, $2)\n\t\\$$`)); got != "foo(2, 1)\n\t\\$" {
		t.Errorf("Expand = %q", got)
	}
	lit := mustCompile(t, "foo", Options{})
	m, _ = lit.Next(b.Snapshot(), 0, false)
	if got := string(lit.Expand(b.Snapshot(), m, "$1\\n")); got != "$1\\n" {
		t.Errorf("literal Expand = %q", got)
	}
	opt := mustCompile(t, `a(x)?`, Options{Regex: true})
	m, _ = opt.Next(buffer.NewString("a").Snapshot(), 0, false)
	if got := string(opt.Expand(buffer.NewString("a").Snapshot(), m, "[$1]\\q")); got != `[]\q` {
		t.Errorf("unmatched group Expand = %q", got)
	}
}

func TestReplaceAllIsOneUndoStep(t *testing.T) {
	b := buffer.NewString("$a + $b + $c")
	q := mustCompile(t, `\$(\w)`, Options{Regex: true})
	n, err := ReplaceAll(b, q, "$$this->$1")
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if b.String() != "$this->a + $this->b + $this->c" {
		t.Fatal(b.String())
	}
	b.Undo()
	if b.String() != "$a + $b + $c" {
		t.Fatalf("undo: %q", b.String())
	}
	if n, _ := ReplaceAll(b, mustCompile(t, "zz", Options{}), "y"); n != 0 || b.CanRedo() == false {
		t.Fatal("no-op replace changed history")
	}
	n, _ = ReplaceAllIn(b, q, "X", 4, 8)
	if n != 1 || b.String() != "$a + X + $c" {
		t.Fatalf("ReplaceAllIn: %d %q", n, b.String())
	}
}

func TestReplaceSingle(t *testing.T) {
	b := buffer.NewString("one two one")
	q := mustCompile(t, "one", Options{WholeWord: true})
	m, _ := q.Next(b.Snapshot(), 1, false)
	if _, err := Replace(b, q, m, "1"); err != nil {
		t.Fatal(err)
	}
	if b.String() != "one two 1" {
		t.Fatal(b.String())
	}
	if _, err := Replace(b, q, Match{Start: 50, End: 60}, "x"); err == nil {
		t.Fatal("stale match accepted")
	}
}

func TestWordBounded(t *testing.T) {
	text := []byte("über_x")
	if wordBounded(text, 1, 3) || !wordBounded(text, 0, len(text)) {
		t.Fatal("wordBounded")
	}
}

func BenchmarkFindAll100kLines(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 100_000; i++ {
		fmt.Fprintf(&sb, "    $value%d = $this->compute(%d);\n", i, i)
	}
	s := buffer.NewString(sb.String()).Snapshot()
	q, _ := Compile("compute", Options{CaseSensitive: true})
	b.ResetTimer()
	for b.Loop() {
		if len(q.FindAll(s, 0)) != 100_000 {
			b.Fatal("count")
		}
	}
}
