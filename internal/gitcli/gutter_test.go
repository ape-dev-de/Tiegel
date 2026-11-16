package gitcli

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestLineChanges(t *testing.T) {
	base := []byte("a\nb\nc\nd\ne\n")
	cur := []byte("a\nB\nc\nnew1\nnew2\nd\n")
	got := LineChanges(base, cur)
	want := []LineRange{
		{Kind: RangeModified, Start: 1, End: 2, BaseStart: 1, BaseEnd: 2},
		{Kind: RangeAdded, Start: 3, End: 5, BaseStart: 3, BaseEnd: 3},
		{Kind: RangeDeleted, Start: 6, End: 6, BaseStart: 4, BaseEnd: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	for _, k := range []RangeKind{RangeAdded, RangeModified, RangeDeleted, 9} {
		if k.String() == "" {
			t.Error("empty kind name")
		}
	}
	if RangeKind(9).String() != "unknown" {
		t.Error("unknown kind")
	}
	// New file: everything added.
	if got := LineChanges(nil, []byte("x\ny")); len(got) != 1 || got[0].Kind != RangeAdded || got[0].End != 2 {
		t.Errorf("new file: %+v", got)
	}
	// Missing final newline marks the last line modified.
	if got := LineChanges([]byte("a\nb\n"), []byte("a\nb")); len(got) != 1 || got[0].Kind != RangeModified || got[0].Start != 1 {
		t.Errorf("eol: %+v", got)
	}
}

func TestRollbackRestoresBase(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	words := []string{"a\n", "b\n", "c\n", "d\n", "e", "f\r\n", "\n"}
	gen := func() []byte {
		var b strings.Builder
		n := r.Intn(12)
		for i := 0; i < n; i++ {
			w := words[r.Intn(len(words))]
			if w == "e" && i != n-1 {
				w = "e\n"
			}
			b.WriteString(w)
		}
		return []byte(b.String())
	}
	for iter := 0; iter < 2000; iter++ {
		base, cur := gen(), gen()
		// Roll back ranges one at a time, always recomputing, until clean.
		for step := 0; ; step++ {
			rs := LineChanges(base, cur)
			if len(rs) == 0 {
				break
			}
			if step > 50 {
				t.Fatalf("rollback does not converge: %q -> %q", base, cur)
			}
			pick := rs[r.Intn(len(rs))]
			rep, err := Rollback(base, cur, pick)
			if err != nil {
				t.Fatal(err)
			}
			cur = rep.Apply(cur)
		}
		if string(cur) != string(base) {
			t.Fatalf("not restored: %q vs %q", cur, base)
		}
	}
	// Rolling back all ranges back to front in one go works as well.
	base, cur := []byte("1\n2\n3\n4\n5\n6\n"), []byte("1\nX\n3\n5\n6\n7\n")
	rs := LineChanges(base, cur)
	for i := len(rs) - 1; i >= 0; i-- {
		rep, err := Rollback(base, cur, rs[i])
		if err != nil {
			t.Fatal(err)
		}
		cur = rep.Apply(cur)
	}
	if string(cur) != string(base) {
		t.Errorf("back to front: %q", cur)
	}
	if _, err := Rollback(base, cur, LineRange{Start: 5, End: 99}); err == nil {
		t.Error("out of range rollback")
	}
}

func TestGutterChangesRepo(t *testing.T) {
	tr := newTestRepo(t)
	ctx := ctxT(t)
	tr.write("f.txt", "one\ntwo\nthree\n")
	tr.commitAll("init")
	tr.write("f.txt", "one\nTWO\nthree\n")
	tr.git("add", "f.txt")

	buf := []byte("one\nTWO\nthree\nfour\n")
	head, err := tr.r.GutterChanges(ctx, "f.txt", buf, BaseHEAD)
	if err != nil {
		t.Fatal(err)
	}
	if len(head) != 2 || head[0].Kind != RangeModified || head[1].Kind != RangeAdded {
		t.Errorf("vs HEAD: %+v", head)
	}
	idx, err := tr.r.GutterChanges(ctx, "f.txt", buf, BaseIndex)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) != 1 || idx[0].Kind != RangeAdded || idx[0].Start != 3 {
		t.Errorf("vs index: %+v", idx)
	}
	// Unknown file: everything is added.
	nf, err := tr.r.GutterChanges(ctx, "new.txt", []byte("x\n"), BaseHEAD)
	if err != nil || len(nf) != 1 || nf[0].Kind != RangeAdded {
		t.Errorf("new file: %+v %v", nf, err)
	}
	if _, ok, err := tr.r.BaseContent(ctx, "new.txt", BaseIndex); ok || err != nil {
		t.Errorf("missing base: %v %v", ok, err)
	}
	// CRLF work tree conversion is applied to the base.
	tr.write(".gitattributes", "*.crlf text eol=crlf\n")
	tr.write("w.crlf", "x\r\ny\r\n")
	tr.commitAll("crlf")
	content, ok, err := tr.r.BaseContent(ctx, "w.crlf", BaseHEAD)
	if err != nil || !ok || string(content) != "x\r\ny\r\n" {
		t.Errorf("filtered base: %q %v %v", content, ok, err)
	}
}
