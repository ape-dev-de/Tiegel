package diff

import (
	"image"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/gitcli"
	"github.com/ape-dev-de/tiegel/internal/ui/editorview"
)

// fakeScroller clamps like an editor does during Layout.
type fakeScroller struct {
	p    image.Point
	maxY int
}

func (f *fakeScroller) ScrollOffset() image.Point     { return f.p }
func (f *fakeScroller) SetScrollOffset(p image.Point) { f.p = p }
func (f *fakeScroller) layout()                       { f.p.Y = min(max(f.p.Y, 0), f.maxY) }

func TestScrollSyncLeaderAndClamping(t *testing.T) {
	l, r := &fakeScroller{maxY: 1000}, &fakeScroller{maxY: 300}
	double := func(y int) int { return 2 * y }
	half := func(y int) int { return y / 2 }
	var s scrollSync
	frame := func() bool {
		s.before(l, r, double, half)
		l.layout()
		r.layout()
		return s.after(l, r, double, half)
	}
	frame()
	// The user scrolls left: right follows (and is clamped to 300).
	l.p.Y = 200
	if frame() {
		t.Fatal("no extra frame needed")
	}
	if r.p.Y != 300 || l.p.Y != 200 {
		t.Fatalf("follow %v %v", l.p, r.p)
	}
	// The clamped follower must not drive the leader back.
	for i := 0; i < 3; i++ {
		frame()
	}
	if l.p.Y != 200 || r.p.Y != 300 {
		t.Fatalf("feedback %v %v", l.p, r.p)
	}
	// The user scrolls right: left follows.
	r.p.Y = 100
	frame()
	if l.p.Y != 50 {
		t.Fatalf("right lead %v", l.p)
	}
	// A side moving during its Layout (caret reveal) leads as well and
	// requests another frame.
	s.before(l, r, double, half)
	r.p.Y = 120
	if !s.after(l, r, double, half) || l.p.Y != 60 {
		t.Fatalf("layout move %v", l.p)
	}
	frame()
	if l.p.Y != 60 || r.p.Y != 120 {
		t.Fatalf("after layout move %v %v", l.p, r.p)
	}
	s.reset()
	if s.init {
		t.Fatal("reset")
	}
}

func TestBuildUnified(t *testing.T) {
	left := "a\nb\nc\nd\n"
	right := "a\nB\nc\nd\ne"
	m := buildUnified(left, right, Compute(left, right, false))
	if m.text != "a\nb\nB\nc\nd\ne" {
		t.Fatalf("text %q", m.text)
	}
	want := []uniLine{{1, 1}, {2, 0}, {0, 2}, {3, 3}, {4, 4}, {0, 5}}
	if len(m.lines) != len(want) {
		t.Fatalf("lines %v", m.lines)
	}
	for i := range want {
		if m.lines[i] != want[i] {
			t.Errorf("line %d: %v, want %v", i, m.lines[i], want[i])
		}
	}
	if len(m.starts) != 2 || m.starts[0] != 1 || m.starts[1] != 5 {
		t.Fatalf("starts %v", m.starts)
	}
	if len(m.decos) != 3 || m.decos[0].Kind != editorview.DecorationDeleted || m.decos[1].Kind != editorview.DecorationAdded {
		t.Fatalf("decos %+v", m.decos)
	}
	// Inline ranges move to the unified rows.
	l2 := "$a = foo(1);\n"
	r2 := "$a = bar(1);\n"
	m = buildUnified(l2, r2, Compute(l2, r2, false))
	if len(m.decos) != 2 || len(m.decos[0].Inline) != 1 || m.decos[0].Inline[0].Line != 0 || m.decos[1].Inline[0].Line != 1 {
		t.Fatalf("inline %+v", m.decos)
	}
}

func TestBlocksConversionAndKinds(t *testing.T) {
	bs := BlocksFromGit(gitcli.CompareTexts([]byte("a\nb\n"), []byte("b\nc\n"), gitcli.TextDiffOptions{}))
	if len(bs) != 2 || bs[0].Kind != Deleted || bs[1].Kind != Added {
		t.Fatalf("blocks %+v", bs)
	}
	for _, k := range []Kind{Added, Modified, Deleted} {
		if KindOf(k.String()) != k {
			t.Errorf("kind %v", k)
		}
	}
	d := decorations([]Block{{Kind: Modified, LeftStart: 1, LeftEnd: 2, RightStart: 3, RightEnd: 5,
		LeftInline: []Inline{{Line: 1, Start: 0, End: 1}}}}, true)
	if d[0].StartLine != 1 || d[0].EndLine != 2 || len(d[0].Inline) != 1 || d[0].Kind != editorview.DecorationModified {
		t.Fatalf("decorations %+v", d)
	}
	if g := toGit(bs); len(g) != 2 || g[1].RightStart != bs[1].RightStart {
		t.Fatal("toGit")
	}
	if !strings.Contains(Kind(9).String(), "modified") {
		t.Fatal("default kind")
	}
}
