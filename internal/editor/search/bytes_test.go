package search

import "testing"

func TestFindAllBytesAndExpand(t *testing.T) {
	q, err := Compile(`(\w+)@(\w+)`, Options{Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	text := []byte("a@b c@d e@f")
	ms := q.FindAllBytes(text, 2)
	if len(ms) != 2 || ms[1].Start != 4 {
		t.Fatalf("matches %+v", ms)
	}
	if got := string(q.ExpandBytes(text, ms[1], `$2\t$1`)); got != "d\tc" {
		t.Errorf("expand %q", got)
	}
	plain, _ := Compile("c@d", Options{})
	ms = plain.FindAllBytes(text, 0)
	if len(ms) != 1 || string(plain.ExpandBytes(text, ms[0], "$1")) != "$1" {
		t.Errorf("plain %+v", ms)
	}
}
