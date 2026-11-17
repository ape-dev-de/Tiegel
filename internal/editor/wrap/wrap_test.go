package wrap

import (
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
	"github.com/ape-dev-de/tiegel/internal/editor/fold"
)

// rowsOf splits line at its breaks.
func rowsOf(line string, width, tab int) []string {
	brk := Breaks(nil, []byte(line), width, tab)
	var out []string
	prev := 0
	for _, b := range brk {
		out = append(out, line[prev:b])
		prev = b
	}
	return append(out, line[prev:])
}

func TestBreaks(t *testing.T) {
	cases := []struct {
		line  string
		width int
		want  []string
	}{
		{"short", 10, []string{"short"}},
		{"", 10, []string{""}},
		{"exactly10!", 10, []string{"exactly10!"}},
		{"hello world foo", 11, []string{"hello ", "world foo"}},
		{"hello world foo", 8, []string{"hello ", "world ", "foo"}},
		{"abcdefghijkl", 5, []string{"abcde", "fghij", "kl"}},
		{"ab cdefghijkl", 5, []string{"ab ", "cdefg", "hijkl"}},
		{"\tabc", 4, []string{"\t", "abc"}},
		{"日本語テキスト", 6, []string{"日本語", "テキス", "ト"}},
		{"e\u0301e\u0301e\u0301", 2, []string{"e\u0301e\u0301", "e\u0301"}},
		{"x     y", 3, []string{"x  ", "   ", "y"}},
	}
	for _, c := range cases {
		got := rowsOf(c.line, c.width, 4)
		if !slices.Equal(got, c.want) {
			t.Errorf("%q @%d = %q, want %q", c.line, c.width, got, c.want)
		}
	}
}

// TestBreaksInvariants checks on random lines that every row fits (or
// holds a single cluster) and rows cover the line.
func TestBreaksInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"a", "b", " ", "\t", "日", "é", "x", "  ", "😀"}
	for n := 0; n < 2000; n++ {
		var sb strings.Builder
		for i := rng.Intn(80); i > 0; i-- {
			sb.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		line := sb.String()
		width := 1 + rng.Intn(20)
		rows := rowsOf(line, width, 4)
		if strings.Join(rows, "") != line {
			t.Fatalf("rows %q do not cover %q", rows, line)
		}
		for i, r := range rows {
			w := buffer.VisualCol([]byte(r), len(r), 4)
			if w > width && buffer.GraphemeCol([]byte(r), len(r)) > 1 {
				t.Fatalf("%q @%d: row %d %q is %d cells", line, width, i, r, w)
			}
			if r == "" && len(rows) > 1 {
				t.Fatalf("%q @%d: empty row %d", line, width, i)
			}
		}
	}
}

// TestASCIIBreaksMatch checks the fast path for printable ASCII against
// the cluster-by-cluster one.
func TestASCIIBreaksMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for n := 0; n < 3000; n++ {
		b := make([]byte, rng.Intn(120))
		for i := range b {
			b[i] = "ab  cdefgh."[rng.Intn(11)]
		}
		width := 1 + rng.Intn(30)
		fast := asciiBreaks(nil, b, width)
		slow := clusterBreaks(nil, b, width, 4)
		if !slices.Equal(fast, slow) {
			t.Fatalf("%q @%d: fast %v, slow %v", b, width, fast, slow)
		}
	}
}

func TestEnabled(t *testing.T) {
	pats := []string{"*.md", " *.TXT ", ""}
	cases := []struct {
		mode, name string
		want       bool
	}{
		{"all", "a.php", true},
		{"off", "a.md", false},
		{"files", "/x/README.md", true},
		{"files", `C:\x\notes.txt`, true},
		{"files", "a.php", false},
		{"bogus", "a.md", false},
	}
	for _, c := range cases {
		if got := Enabled(c.mode, pats, c.name); got != c.want {
			t.Errorf("Enabled(%q, %q) = %v", c.mode, c.name, got)
		}
	}
}

func randomDoc(rng *rand.Rand, lines int) string {
	words := []string{"foo", "bar", "\t", " ", "baz_qux", "日本", "x", "longidentifiername"}
	var sb strings.Builder
	for l := 0; l < lines; l++ {
		for i := rng.Intn(25); i > 0; i-- {
			sb.WriteString(words[rng.Intn(len(words))])
		}
		if l < lines-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// check compares m with a brute-force computation over s.
func check(t *testing.T, m *Model, s buffer.Snapshot) {
	t.Helper()
	if m.LineCount() != s.LineCount() {
		t.Fatalf("lines %d, want %d", m.LineCount(), s.LineCount())
	}
	total := 0
	for l := 0; l < s.LineCount(); l++ {
		want := len(Breaks(nil, s.Line(l), m.Width(), m.TabWidth())) + 1
		if got := m.Rows(l); got != want {
			t.Fatalf("line %d %q: rows %d, want %d", l, s.Line(l), got, want)
		}
		if got := m.Before(l); got != total {
			t.Fatalf("Before(%d) = %d, want %d", l, got, total)
		}
		for sub := 0; sub < want; sub++ {
			if gl, gs := m.LineAt(total + sub); gl != l || gs != sub {
				t.Fatalf("LineAt(%d) = %d,%d want %d,%d", total+sub, gl, gs, l, sub)
			}
		}
		total += want
	}
	if m.Total() != total {
		t.Fatalf("Total %d, want %d", m.Total(), total)
	}
}

func TestModelResetAndEdits(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	b := buffer.NewString(randomDoc(rng, 300))
	m := &Model{}
	m.Reset(b.Snapshot(), 12, 4)
	if !m.Valid(12, 4) || m.Valid(13, 4) {
		t.Fatal("Valid")
	}
	check(t, m, b.Snapshot())
	unsub := b.OnEdit(func(e buffer.Edit) { m.ApplyEdit(b.Snapshot(), e) })
	defer unsub()
	inserts := []string{"x", "\n", "hello world ", "\tfoo\nbar baz qux quux\n", "日本語日本語日本語"}
	for i := 0; i < 400; i++ {
		n := b.Len()
		at := rng.Intn(n + 1)
		del := 0
		if rng.Intn(3) == 0 {
			del = rng.Intn(min(40, n-at) + 1)
		}
		if _, err := b.Replace(at, del, []byte(inserts[rng.Intn(len(inserts))])); err != nil {
			t.Fatal(err)
		}
		if !m.Valid(12, 4) {
			t.Fatal("model invalidated by a regular edit")
		}
		if i%20 == 0 {
			check(t, m, b.Snapshot())
		}
	}
	check(t, m, b.Snapshot())
	m.Reset(b.Snapshot(), 30, 2)
	check(t, m, b.Snapshot())
}

func TestModelOutOfSync(t *testing.T) {
	b := buffer.NewString("a\nb\nc")
	m := &Model{}
	m.Reset(b.Snapshot(), 10, 4)
	// An edit the model never saw, then one it sees: it must give up.
	if _, err := b.Replace(0, 0, []byte("x\ny\n")); err != nil {
		t.Fatal(err)
	}
	m.ApplyEdit(b.Snapshot(), buffer.Edit{StartPoint: buffer.Point{Row: 0}, OldEndPoint: buffer.Point{Row: 0}, NewEndPoint: buffer.Point{Row: 0}})
	if m.Valid(10, 4) {
		t.Fatal("out-of-sync model still valid")
	}
	m.Invalidate()
	m.ApplyEdit(b.Snapshot(), buffer.Edit{}) // no-op when invalid
	if m.Rows(-1) != 1 || m.Rows(99) != 1 {
		t.Fatal("Rows outside the document")
	}
}

func TestEmptyModel(t *testing.T) {
	var m Model
	if l, s := m.LineAt(5); l != 0 || s != 0 {
		t.Fatal("LineAt on empty model")
	}
	m.Reset(buffer.NewString("").Snapshot(), 0, 0)
	if m.Width() != MinWidth || m.TabWidth() != 4 || m.Total() != 1 {
		t.Fatalf("defaults %d %d %d", m.Width(), m.TabWidth(), m.Total())
	}
}

const viewDoc = "short\n" + // 0: 1 row
	"aaaa bbbb cccc dddd\n" + // 1: 2 rows @10
	"{\n" + // 2
	"eeee ffff gggg hhhh iiii\n" + // 3: 3 rows (hidden by fold 2-4)
	"}\n" + // 4
	"zz" // 5

func newView(t *testing.T) (*View, *buffer.Buffer, *fold.Model) {
	t.Helper()
	b := buffer.NewString(viewDoc)
	m := &Model{}
	m.Reset(b.Snapshot(), 10, 4)
	f := fold.New(b.LineCount())
	f.SetRanges([]fold.Range{{StartLine: 2, EndLine: 4}})
	return &View{Wrap: m, Folds: f}, b, f
}

func TestViewFolds(t *testing.T) {
	v, b, f := newView(t)
	s := b.Snapshot()
	// Unfolded: rows 0 | 1,2 | 3 | 4,5,6 | 7 | 8
	if v.RowCount() != 9 {
		t.Fatalf("rows %d", v.RowCount())
	}
	if l, sub := v.LineAt(5); l != 3 || sub != 1 {
		t.Fatalf("LineAt(5) = %d,%d", l, sub)
	}
	f.Collapse(2)
	// Folded: rows 0 | 1,2 | 3 (header) | 4 (line 5)
	if v.RowCount() != 5 {
		t.Fatalf("folded rows %d", v.RowCount())
	}
	for row, want := range []int{0, 1, 1, 2, 5} {
		if l, _ := v.LineAt(row); l != want {
			t.Errorf("LineAt(%d) = %d, want %d", row, l, want)
		}
	}
	for line, want := range []int{0, 1, 3, 3, 3, 4} {
		if got := v.LineRow(line); got != want {
			t.Errorf("LineRow(%d) = %d, want %d", line, got, want)
		}
	}
	if r, c := v.RowOf(s, s.LineStart(3)+2); r != 3 || c != 0 {
		t.Errorf("RowOf hidden = %d,%d", r, c)
	}
	// Edits invalidate the cached runs through the versions.
	b.OnEdit(func(e buffer.Edit) { v.Wrap.ApplyEdit(b.Snapshot(), e); f.ApplyEdit(e) })
	if _, err := b.Replace(0, 0, []byte("new line here and more\n")); err != nil {
		t.Fatal(err)
	}
	if v.RowCount() != 8 { // +3 rows at width 10 ("new line ", "here and ", "more")
		t.Fatalf("rows after edit %d", v.RowCount())
	}
	if l, _ := v.LineAt(v.RowCount() - 1); l != 6 {
		t.Fatalf("last row line %d", l)
	}
}

func TestViewOffsets(t *testing.T) {
	v, b, _ := newView(t)
	s := b.Snapshot()
	l1 := s.LineStart(1) // "aaaa bbbb " | "cccc dddd"
	cases := []struct{ off, row, col int }{
		{0, 0, 0},
		{l1, 1, 0},
		{l1 + 9, 1, 9},
		{l1 + 10, 2, 0}, // the break belongs to the next row
		{l1 + 19, 2, 9},
	}
	for _, c := range cases {
		if r, col := v.RowOf(s, c.off); r != c.row || col != c.col {
			t.Errorf("RowOf(%d) = %d,%d want %d,%d", c.off, r, col, c.row, c.col)
		}
	}
	// Past the end of a continued row is its break, at the row end.
	if got, end := v.CaretAt(s, 1, 50); got != l1+10 || !end || v.OffsetAt(s, 1, 50) != got {
		t.Errorf("CaretAt continued row = %d %v, want %d at the row end", got, end, l1+10)
	}
	if r, c := v.CaretRow(s, l1+10, true); r != 1 || c != 10 {
		t.Errorf("CaretRow at row end = %d,%d, want 1,10", r, c)
	}
	if st, en, last := v.CaretRowBounds(s, l1+10, true); st != l1 || en != l1+10 || last {
		t.Errorf("CaretRowBounds at row end = %d %d %v", st, en, last)
	}
	if got := v.OffsetAt(s, 2, 50); got != l1+19 {
		t.Errorf("OffsetAt last row = %d, want %d", got, l1+19)
	}
	if got := v.OffsetAt(s, 2, 3); got != l1+13 {
		t.Errorf("OffsetAt(2,3) = %d", got)
	}
	st, en, last := v.RowBounds(s, l1+3)
	if st != l1 || en != l1+10 || last {
		t.Errorf("RowBounds first = %d %d %v", st, en, last)
	}
	st, en, last = v.RowBounds(s, l1+10)
	if st != l1+10 || en != l1+19 || !last {
		t.Errorf("RowBounds last = %d %d %v", st, en, last)
	}
	// Without folds.
	v2 := &View{Wrap: v.Wrap}
	if v2.RowCount() != 9 || v2.LineRow(3) != 4 {
		t.Fatalf("no folds: %d %d", v2.RowCount(), v2.LineRow(3))
	}
}
