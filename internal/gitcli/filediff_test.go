package gitcli

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompareTexts(t *testing.T) {
	left := "a\nb\nc\nd\ne\n"
	right := "a\nB\nc\ne\nf\n"
	got := CompareTexts([]byte(left), []byte(right), TextDiffOptions{Inline: true})
	want := []DiffBlock{
		{Kind: BlockModified, LeftStart: 1, LeftEnd: 2, RightStart: 1, RightEnd: 2},
		{Kind: BlockDeleted, LeftStart: 3, LeftEnd: 4, RightStart: 3, RightEnd: 3},
		{Kind: BlockAdded, LeftStart: 5, LeftEnd: 5, RightStart: 4, RightEnd: 5},
	}
	if len(got) != len(want) {
		t.Fatalf("blocks %+v", got)
	}
	for i := range want {
		g := got[i]
		g.LeftInline, g.RightInline = nil, nil
		if fmt.Sprint(g) != fmt.Sprint(want[i]) {
			t.Errorf("block %d: %+v, want %+v", i, g, want[i])
		}
		if want[i].Kind.String() == "unknown" {
			t.Error("kind name")
		}
	}
	// "b" vs "B" differ completely: no inline ranges.
	if got[0].LeftInline != nil {
		t.Errorf("inline for completely different line: %+v", got[0].LeftInline)
	}
	if BlockKind(9).String() != "unknown" {
		t.Error("unknown kind")
	}
}

func TestCompareTextsInlineAndWhitespace(t *testing.T) {
	left := "<?php\n$total = $net * 1.19;\nreturn $total;\n"
	right := "<?php\n$total = $net * self::VAT;\n  return   $total;\n"
	blocks := CompareTexts([]byte(left), []byte(right), TextDiffOptions{Inline: true})
	if len(blocks) != 1 || blocks[0].Kind != BlockModified || blocks[0].LeftEnd-blocks[0].LeftStart != 2 {
		t.Fatalf("blocks %+v", blocks)
	}
	b := blocks[0]
	if len(b.LeftInline) != 1 || b.LeftInline[0].Line != 1 || len(b.RightInline) != 1 || b.RightInline[0].Line != 1 {
		t.Fatalf("inline %+v / %+v", b.LeftInline, b.RightInline)
	}
	l1 := "$total = $net * 1.19;"
	r := b.LeftInline[0].Ranges[0]
	if l1[r.Start:r.End] != "1.19" && l1[r.Start:r.End] != "1" {
		t.Errorf("left range %q", l1[r.Start:r.End])
	}
	// Ignoring white space hides the indentation change.
	blocks = CompareTexts([]byte(left), []byte(right), TextDiffOptions{IgnoreWhitespace: true})
	if len(blocks) != 1 || blocks[0].LeftStart != 1 || blocks[0].LeftEnd != 2 {
		t.Fatalf("ignore ws %+v", blocks)
	}
	if CompareTexts([]byte(left), []byte(left), TextDiffOptions{}) == nil {
		// empty but non-nil is fine; nil is fine as well
		t.Log("nil result for equal texts")
	}
}

func TestWordDiff(t *testing.T) {
	a := "return $this->redirectToRoute('user_list');"
	b := "return $this->redirectToRoute('user_show');"
	ra, rb := WordDiff(a, b)
	if len(ra) != 1 || a[ra[0].Start:ra[0].End] != "list" || b[rb[0].Start:rb[0].End] != "show" {
		t.Fatalf("narrowed %v %v", ra, rb)
	}
	ra, rb = WordDiff("$a = foo(1, 2);", "$a = bar(1, 2);")
	if len(ra) != 1 || len(rb) != 1 || ra[0] != (ByteRange{5, 8}) {
		t.Fatalf("word %v %v", ra, rb)
	}
	if ra, rb := WordDiff("abc", "xyz"); ra != nil || rb != nil {
		t.Fatal("completely different")
	}
	if ra, _ := WordDiff(strings.Repeat("a ", 2000), "b"); ra != nil {
		t.Fatal("too long")
	}
	if ra, _ := WordDiff("x", "x"); ra != nil {
		t.Fatal("equal")
	}
	// Only white space changed: ranges are dropped.
	if ra, rb := WordDiff("a  b c d", "a b c d"); ra != nil || rb != nil {
		t.Fatalf("white space %v %v", ra, rb)
	}
	if p, s := commonAffixes("äbc", "äxc"); p != 2 || s != 1 {
		t.Fatalf("affixes %d %d", p, s)
	}
	if p, s := commonAffixes("aa", "aaa"); p != 2 || s != 0 {
		t.Fatalf("overlap %d %d", p, s)
	}
}

func TestMapLine(t *testing.T) {
	blocks := CompareTexts([]byte("a\nb\nc\nd\n"), []byte("a\nx\ny\nz\nc\nd\ne\n"), TextDiffOptions{})
	// a=a, b->xyz, c=c, d=d, +e
	cases := []struct {
		line     float64
		fromLeft bool
		want     float64
	}{
		{0, true, 0}, {1, true, 1}, {1.5, true, 2.5}, {2, true, 4}, {3, true, 5}, {4, true, 7},
		{4, false, 2}, {2.5, false, 1.5}, {6, false, 4}, {6.5, false, 4},
	}
	for _, c := range cases {
		if got := MapLine(blocks, c.line, c.fromLeft); got != c.want {
			t.Errorf("MapLine(%v, %v) = %v, want %v", c.line, c.fromLeft, got, c.want)
		}
	}
}

func BenchmarkCompareTexts10k(b *testing.B) {
	var l, r strings.Builder
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&l, "    $value%d = compute($input, %d);\n", i, i)
		if i%97 == 0 {
			fmt.Fprintf(&r, "    $value%d = compute($input, %d + 1);\n", i, i)
			continue
		}
		fmt.Fprintf(&r, "    $value%d = compute($input, %d);\n", i, i)
	}
	lb, rb := []byte(l.String()), []byte(r.String())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CompareTexts(lb, rb, TextDiffOptions{Inline: true})
	}
}
