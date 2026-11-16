package gitcli

import (
	"math/rand"
	"strings"
	"testing"
)

func kinds(m *MergeResult) string {
	var s []string
	for _, b := range m.Blocks {
		s = append(s, b.Kind.String())
	}
	return strings.Join(s, ",")
}

func TestMerge3Blocks(t *testing.T) {
	cases := []struct {
		name               string
		base, ours, theirs string
		kinds              string
		conflicts          int
		result             string // rendered with default labels
	}{
		{"identical", "a\nb\n", "a\nb\n", "a\nb\n", "unchanged", 0, "a\nb\n"},
		{"ours only", "a\nb\nc\n", "a\nB\nc\n", "a\nb\nc\n", "unchanged,ours,unchanged", 0, "a\nB\nc\n"},
		{"theirs only", "a\nb\nc\n", "a\nb\nc\n", "a\nb\nC\n", "unchanged,theirs", 0, "a\nb\nC\n"},
		{"both same", "a\nb\nc\n", "a\nX\nc\n", "a\nX\nc\n", "unchanged,both,unchanged", 0, "a\nX\nc\n"},
		{"separate", "1\n2\n3\n4\n5\n", "1\nO\n3\n4\n5\n", "1\n2\n3\n4\nT\n", "unchanged,ours,unchanged,theirs", 0, "1\nO\n3\n4\nT\n"},
		{"conflict", "a\nb\nc\n", "a\nO\nc\n", "a\nT\nc\n", "unchanged,conflict,unchanged", 1,
			"a\n<<<<<<< ours\nO\n||||||| base\nb\n=======\nT\n>>>>>>> theirs\nc\n"},
		{"adjacent edits conflict", "1\n2\n3\n", "1\nO\n3\n", "1\n2\nT\n", "unchanged,conflict", 1, ""},
		{"insert same place", "a\nb\n", "a\nX\nb\n", "a\nY\nb\n", "unchanged,conflict,unchanged", 1, ""},
		{"empty base", "", "o\n", "t\n", "conflict", 1, ""},
		{"empty all", "", "", "", "", 0, ""},
		{"delete vs edit", "a\nb\nc\n", "a\nc\n", "a\nB\nc\n", "unchanged,conflict,unchanged", 1, ""},
		{"both delete", "a\nb\nc\n", "a\nc\n", "a\nc\n", "unchanged,both,unchanged", 0, "a\nc\n"},
		{"no eol conflict", "a\nb", "a\nO", "a\nT", "unchanged,conflict", 1,
			"a\n<<<<<<< ours\nO\n||||||| base\nb\n=======\nT\n>>>>>>> theirs\n"},
		{"insert at start and end", "m\n", "s\nm\n", "m\ne\n", "ours,unchanged,theirs", 0, "s\nm\ne\n"},
	}
	for _, c := range cases {
		m := Merge3([]byte(c.base), []byte(c.ours), []byte(c.theirs))
		if got := kinds(m); got != c.kinds {
			t.Errorf("%s: kinds %q, want %q", c.name, got, c.kinds)
		}
		if m.Conflicts() != c.conflicts || m.Unresolved() != c.conflicts {
			t.Errorf("%s: conflicts %d", c.name, m.Conflicts())
		}
		if c.result != "" {
			if got := string(m.Render(MarkerLabels{})); got != c.result {
				t.Errorf("%s: render %q, want %q", c.name, got, c.result)
			}
		}
	}
}

func TestMerge3StartIndexes(t *testing.T) {
	m := Merge3([]byte("1\n2\n3\n4\n5\n6\n"), []byte("0\n1\n2\n3\n4\nO\n6\n"), []byte("1\n3\n4\n5\n6\n"))
	// ours inserts 0 at start and edits 5; theirs deletes 2.
	if kinds(m) != "ours,unchanged,theirs,unchanged,ours,unchanged" {
		t.Fatalf("kinds %s", kinds(m))
	}
	b := m.Blocks[4]
	if b.BaseStart != 4 || b.OursStart != 5 || b.TheirsStart != 3 || b.Ours[0] != "O\n" {
		t.Errorf("block %+v", b)
	}
	last := m.Blocks[5]
	if last.BaseStart != 5 || last.OursStart != 6 || last.TheirsStart != 4 {
		t.Errorf("last %+v", last)
	}
	if got := string(m.Render(MarkerLabels{})); got != "0\n1\n3\n4\nO\n6\n" {
		t.Errorf("render %q", got)
	}
}

func TestMerge3Resolutions(t *testing.T) {
	base, ours, theirs := "x\nb\ny\n", "x\nO\ny\n", "x\nT\ny\n"
	cases := map[Resolution]string{
		TakeOurs:        "x\nO\ny\n",
		TakeTheirs:      "x\nT\ny\n",
		TakeOursFirst:   "x\nO\nT\ny\n",
		TakeTheirsFirst: "x\nT\nO\ny\n",
		TakeBase:        "x\nb\ny\n",
		TakeCustom:      "x\ncustom\ny\n",
	}
	for res, want := range cases {
		m := Merge3([]byte(base), []byte(ours), []byte(theirs))
		for i := range m.Blocks {
			if m.Blocks[i].Kind == MergeConflict {
				m.Blocks[i].Resolution = res
				m.Blocks[i].Custom = []byte("custom\n")
			}
		}
		if m.Unresolved() != 0 {
			t.Errorf("%d: still unresolved", res)
		}
		if got := string(m.Render(MarkerLabels{})); got != want {
			t.Errorf("resolution %d: %q, want %q", res, got, want)
		}
	}
	// Joining sides without final newline inserts one.
	m := Merge3([]byte("b"), []byte("O"), []byte("T"))
	m.Blocks[0].Resolution = TakeOursFirst
	if got := string(m.Render(MarkerLabels{})); got != "O\nT" {
		t.Errorf("join noeol: %q", got)
	}
	for _, k := range []MergeBlockKind{MergeUnchanged, MergeOursOnly, MergeTheirsOnly, MergeBothSame, MergeConflict, 42} {
		if k.String() == "" {
			t.Error("empty kind")
		}
	}
}

// TestMerge3Property checks invariants on random inputs: concatenating the
// side texts of all blocks reproduces each input, and non-conflicting
// merges of one-sided changes equal the changed side.
func TestMerge3Property(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	gen := func(base []string) []string {
		out := append([]string(nil), base...)
		for k := r.Intn(4); k > 0; k-- {
			p := r.Intn(len(out) + 1)
			switch r.Intn(3) {
			case 0:
				out = append(out[:p], append([]string{string(rune('k'+r.Intn(5))) + "\n"}, out[p:]...)...)
			case 1:
				if p < len(out) {
					out = append(out[:p], out[p+1:]...)
				}
			default:
				if p < len(out) {
					out[p] = string(rune('k'+r.Intn(5))) + "\n"
				}
			}
		}
		return out
	}
	for iter := 0; iter < 3000; iter++ {
		var base []string
		for i := r.Intn(10); i > 0; i-- {
			base = append(base, string(rune('a'+r.Intn(6)))+"\n")
		}
		ours, theirs := gen(base), gen(base)
		if r.Intn(4) == 0 {
			theirs = append([]string(nil), base...)
		}
		m := Merge3([]byte(strings.Join(base, "")), []byte(strings.Join(ours, "")), []byte(strings.Join(theirs, "")))
		var cb, co, ct []string
		for _, b := range m.Blocks {
			cb = append(cb, b.Base...)
			if b.Kind == MergeUnchanged {
				co = append(co, b.Base...)
				ct = append(ct, b.Base...)
				continue
			}
			if b.BaseStart != len(cb)-len(b.Base) || b.OursStart != len(co) || b.TheirsStart != len(ct) {
				t.Fatalf("bad start indexes in %+v", b)
			}
			co = append(co, b.Ours...)
			ct = append(ct, b.Theirs...)
		}
		if strings.Join(cb, "") != strings.Join(base, "") || strings.Join(co, "") != strings.Join(ours, "") ||
			strings.Join(ct, "") != strings.Join(theirs, "") {
			t.Fatalf("blocks do not reproduce inputs:\nbase=%q\nours=%q\ntheirs=%q\n%+v", base, ours, theirs, m.Blocks)
		}
		if strings.Join(theirs, "") == strings.Join(base, "") {
			if m.Conflicts() != 0 || string(m.Render(MarkerLabels{})) != strings.Join(ours, "") {
				t.Fatalf("one-sided merge wrong: %q", m.Render(MarkerLabels{}))
			}
		}
	}
}
