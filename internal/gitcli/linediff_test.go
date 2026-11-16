package gitcli

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestSplitLines(t *testing.T) {
	cases := map[string][]string{
		"":             nil,
		"a":            {"a"},
		"a\n":          {"a\n"},
		"a\nb":         {"a\n", "b"},
		"a\r\nb\r\n":   {"a\r\n", "b\r\n"},
		"\n\n":         {"\n", "\n"},
		"x\n\ny\n end": {"x\n", "\n", "y\n", " end"},
	}
	for in, want := range cases {
		if got := SplitLines([]byte(in)); !reflect.DeepEqual(got, want) {
			t.Errorf("SplitLines(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDiffLinesSimple(t *testing.T) {
	cases := []struct {
		a, b string
		want []Change
	}{
		{"", "", nil},
		{"abc", "abc", nil},
		{"", "ab", []Change{{0, 0, 0, 2}}},
		{"ab", "", []Change{{0, 2, 0, 0}}},
		{"abc", "axc", []Change{{1, 2, 1, 2}}},
		{"abc", "abxc", []Change{{2, 2, 2, 3}}},
		{"abcd", "acd", []Change{{1, 2, 1, 1}}},
		{"abcabba", "cbabac", nil}, // checked by property below
	}
	for _, c := range cases {
		a, b := strings.Split(c.a, ""), strings.Split(c.b, "")
		if c.a == "" {
			a = nil
		}
		if c.b == "" {
			b = nil
		}
		got := DiffLines(a, b)
		checkScript(t, a, b, got)
		if c.want != nil && !reflect.DeepEqual(got, c.want) {
			t.Errorf("DiffLines(%q,%q) = %+v, want %+v", c.a, c.b, got, c.want)
		}
		if c.want == nil && c.a == c.b && len(got) != 0 {
			t.Errorf("equal input produced changes %+v", got)
		}
	}
}

// checkScript verifies that applying changes to a yields b and returns the
// number of edited lines.
func checkScript(t *testing.T, a, b []string, cs []Change) int {
	t.Helper()
	var out []string
	pos := 0
	edits := 0
	prevEndOld, prevEndNew := -1, -1
	for _, c := range cs {
		if c.OldStart < pos || c.OldEnd < c.OldStart || c.NewEnd < c.NewStart {
			t.Fatalf("bad change %+v (pos %d) in %+v", c, pos, cs)
		}
		if c.OldStart == prevEndOld && c.NewStart == prevEndNew {
			t.Fatalf("adjacent changes not merged: %+v", cs)
		}
		if c.OldStart == c.OldEnd && c.NewStart == c.NewEnd {
			t.Fatalf("empty change %+v", c)
		}
		// unchanged lines must be equal in a and b
		if c.NewStart-len(out) != c.OldStart-pos {
			t.Fatalf("misaligned change %+v", c)
		}
		out = append(out, a[pos:c.OldStart]...)
		out = append(out, b[c.NewStart:c.NewEnd]...)
		edits += (c.OldEnd - c.OldStart) + (c.NewEnd - c.NewStart)
		pos = c.OldEnd
		prevEndOld, prevEndNew = c.OldEnd, c.NewEnd
	}
	out = append(out, a[pos:]...)
	if len(out) == 0 {
		out = nil
	}
	bb := b
	if len(bb) == 0 {
		bb = nil
	}
	if !reflect.DeepEqual(out, bb) {
		t.Fatalf("script does not transform a into b:\na=%q\nb=%q\nchanges=%+v\ngot=%q", a, b, cs, out)
	}
	return edits
}

func lcsLen(a, b []string) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return dp[0][0]
}

func randSeq(r *rand.Rand, n, alphabet int) []string {
	s := make([]string, n)
	for i := range s {
		s[i] = string(rune('a' + r.Intn(alphabet)))
	}
	return s
}

func TestDiffLinesRandomMinimal(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 3000; iter++ {
		a := randSeq(r, r.Intn(25), 1+r.Intn(6))
		var b []string
		if r.Intn(3) == 0 {
			b = randSeq(r, r.Intn(25), 1+r.Intn(6))
		} else {
			// mutate a
			b = append([]string(nil), a...)
			for k := r.Intn(5); k > 0; k-- {
				switch p := r.Intn(len(b) + 1); r.Intn(3) {
				case 0:
					b = append(b[:p], append([]string{string(rune('a' + r.Intn(8)))}, b[p:]...)...)
				case 1:
					if p < len(b) {
						b = append(b[:p], b[p+1:]...)
					}
				default:
					if p < len(b) {
						b[p] = string(rune('a' + r.Intn(8)))
					}
				}
			}
		}
		cs := DiffLines(a, b)
		edits := checkScript(t, a, b, cs)
		if want := len(a) + len(b) - 2*lcsLen(a, b); edits != want {
			t.Fatalf("not minimal: a=%q b=%q edits=%d want %d (%+v)", a, b, edits, want, cs)
		}
	}
}

func TestDiffLinesLargeUnrelated(t *testing.T) {
	var a, b []string
	for i := 0; i < 20000; i++ {
		a = append(a, "a"+itoa(i))
		b = append(b, "b"+itoa(i))
	}
	cs := DiffLines(a, b)
	if len(cs) != 1 || cs[0] != (Change{0, 20000, 0, 20000}) {
		t.Fatalf("unexpected %+v", cs)
	}
}

func TestDiffLinesLargeSimilar(t *testing.T) {
	var a []string
	for i := 0; i < 50000; i++ {
		a = append(a, "line "+itoa(i%1000))
	}
	b := append([]string(nil), a...)
	b[100] = "changed"
	b = append(b[:30000], b[30010:]...)
	b = append(b, "tail")
	cs := DiffLines(a, b)
	checkScript(t, a, b, cs)
	if len(cs) != 3 {
		t.Fatalf("want 3 changes, got %+v", cs)
	}
}
