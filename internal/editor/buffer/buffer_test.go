package buffer

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
)

// refLines splits like the buffer: on '\n', stripping one trailing '\r'.
func refLines(doc []byte) [][]byte {
	parts := bytes.Split(doc, []byte{'\n'})
	for i := 0; i < len(parts)-1; i++ {
		parts[i] = bytes.TrimSuffix(parts[i], []byte{'\r'})
	}
	return parts
}

// checkAgainst verifies every read API of s against the reference doc.
func checkAgainst(t testing.TB, s Snapshot, doc []byte) {
	t.Helper()
	if got := s.Bytes(); !bytes.Equal(got, doc) {
		t.Fatalf("content mismatch:\n got %q\nwant %q", got, doc)
	}
	if s.Len() != len(doc) {
		t.Fatalf("Len = %d, want %d", s.Len(), len(doc))
	}
	lines := refLines(doc)
	if s.LineCount() != len(lines) {
		t.Fatalf("LineCount = %d, want %d", s.LineCount(), len(lines))
	}
	off := 0
	for i, l := range lines {
		if got := s.Line(i); !bytes.Equal(got, l) {
			t.Fatalf("Line(%d) = %q, want %q", i, got, l)
		}
		if got := s.LineStart(i); got != off {
			t.Fatalf("LineStart(%d) = %d, want %d", i, got, off)
		}
		if got := s.LineEnd(i); got != off+len(l) {
			t.Fatalf("LineEnd(%d) = %d, want %d", i, got, off+len(l))
		}
		next := bytes.IndexByte(doc[off:], '\n')
		if next < 0 {
			break
		}
		off += next + 1
	}
	// Offset <-> position round trips.
	line, col := 0, 0
	for o := 0; o <= len(doc); o++ {
		p := s.OffsetToPos(o)
		if p != (Pos{line, col}) {
			t.Fatalf("OffsetToPos(%d) = %v, want %v", o, p, Pos{line, col})
		}
		if col <= len(lines[line]) {
			if got := s.PosToOffset(p); got != o {
				t.Fatalf("PosToOffset(%v) = %d, want %d", p, got, o)
			}
		}
		if o < len(doc) && doc[o] == '\n' {
			line, col = line+1, 0
		} else {
			col++
		}
	}
}

func TestNewAndRead(t *testing.T) {
	cases := []string{
		"",
		"a",
		"\n",
		"\n\n",
		"hello\nworld",
		"hello\nworld\n",
		"a\r\nb\r\n",
		"a\rb\r\n\r\n",
		"äöü 日本語\n😀\n",
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q", c), func(t *testing.T) {
			b := NewString(c)
			checkAgainst(t, b.Snapshot(), []byte(c))
			if b.String() != c {
				t.Fatalf("String = %q", b.String())
			}
		})
	}
}

func TestNewCopiesInput(t *testing.T) {
	in := []byte("abc")
	b := New(in)
	in[0] = 'x'
	if b.String() != "abc" {
		t.Fatalf("buffer aliased its input: %q", b.String())
	}
}

func TestInsertDeleteBasic(t *testing.T) {
	b := NewString("hello world")
	mustReplace(t, b, 5, 0, ",")
	mustReplace(t, b, 12, 0, "!")
	mustReplace(t, b, 0, 0, ">> ")
	if got, want := b.String(), ">> hello, world!"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if _, err := b.Delete(0, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Delete(5, 1); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "hello world!"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	checkAgainst(t, b.Snapshot(), []byte("hello world!"))
}

func mustReplace(t testing.TB, b *Buffer, off, n int, s string) Edit {
	t.Helper()
	e, err := b.Replace(off, n, []byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestOutOfRange(t *testing.T) {
	b := NewString("abc")
	for _, c := range [][2]int{{-1, 0}, {4, 0}, {2, 2}, {0, -1}} {
		if _, err := b.Replace(c[0], c[1], []byte("x")); !errors.Is(err, ErrOutOfRange) {
			t.Errorf("Replace(%d,%d) err = %v", c[0], c[1], err)
		}
	}
	if b.String() != "abc" || b.Version() != 0 {
		t.Fatal("failed edit changed the buffer")
	}
}

func TestNoopEdit(t *testing.T) {
	b := NewString("abc")
	e := mustReplace(t, b, 1, 0, "")
	if b.Version() != 0 || b.CanUndo() {
		t.Fatal("no-op edit was recorded")
	}
	if e.StartByte != 1 || e.NewEndByte != 1 || e.NewEndPoint != (Point{0, 1}) {
		t.Fatalf("unexpected edit %+v", e)
	}
}

func TestEditDescriptor(t *testing.T) {
	b := NewString("ab\ncd\nef")
	e := mustReplace(t, b, 1, 4, "X\nYZ\nW") // replaces "b\ncd"
	want := Edit{
		StartByte: 1, OldEndByte: 5, NewEndByte: 7,
		StartPoint: Point{0, 1}, OldEndPoint: Point{1, 2}, NewEndPoint: Point{2, 1},
	}
	if e.StartByte != want.StartByte || e.OldEndByte != want.OldEndByte || e.NewEndByte != want.NewEndByte ||
		e.StartPoint != want.StartPoint || e.OldEndPoint != want.OldEndPoint || e.NewEndPoint != want.NewEndPoint {
		t.Fatalf("edit = %+v, want %+v", e, want)
	}
	if string(e.Text) != "X\nYZ\nW" || string(e.OldText) != "b\ncd" {
		t.Fatalf("texts = %q %q", e.Text, e.OldText)
	}
	if b.String() != "aX\nYZ\nW\nef" {
		t.Fatalf("content %q", b.String())
	}
}

func TestOnEdit(t *testing.T) {
	b := NewString("")
	var got []Edit
	remove := b.OnEdit(func(e Edit) { got = append(got, e) })
	other := b.OnEdit(func(Edit) {})
	mustReplace(t, b, 0, 0, "abc")
	b.Undo()
	other()
	remove()
	mustReplace(t, b, 0, 0, "x")
	if len(got) != 2 {
		t.Fatalf("listener got %d edits, want 2", len(got))
	}
	if got[1].OldEndByte != 3 || got[1].NewEndByte != 0 {
		t.Fatalf("undo edit %+v", got[1])
	}
}

func TestPieceExtension(t *testing.T) {
	b := NewString("0123456789")
	for i := 0; i < 100; i++ {
		mustReplace(t, b, 5+i, 0, "x")
	}
	if n := b.pieceCount(); n != 3 {
		t.Fatalf("typing created %d pieces, want 3", n)
	}
}

func TestSnapshotIsImmutable(t *testing.T) {
	b := NewString("line1\nline2\n")
	s := b.Snapshot()
	mustReplace(t, b, 0, 5, "changed")
	mustReplace(t, b, b.Len(), 0, "more\n")
	checkAgainst(t, s, []byte("line1\nline2\n"))
	checkAgainst(t, b.Snapshot(), []byte("changed\nline2\nmore\n"))
	if s.Version() >= b.Version() {
		t.Fatal("snapshot version not older")
	}
}

func TestSnapshotConcurrentReaders(t *testing.T) {
	b := NewString(strings.Repeat("abc\n", 1000))
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		s := b.Snapshot()
		want := s.Bytes()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if !bytes.Equal(s.Bytes(), want) {
					t.Error("snapshot changed under reader")
					return
				}
				_ = s.Line(i)
			}
		}()
		for i := 0; i < 200; i++ {
			mustReplace(t, b, (i*7)%b.Len(), 0, "z\n")
		}
	}
	wg.Wait()
}

func TestZeroSnapshot(t *testing.T) {
	var s Snapshot
	if s.Len() != 0 || s.LineCount() != 1 || s.Line(0) == nil || len(s.Line(0)) != 0 {
		t.Fatal("zero snapshot is not an empty document")
	}
	if s.Line(1) != nil || s.Line(-1) != nil {
		t.Fatal("out-of-range line not nil")
	}
	if _, ok := s.ByteAt(0); ok {
		t.Fatal("ByteAt on empty doc")
	}
}

func TestClamping(t *testing.T) {
	s := NewString("ab\ncd").Snapshot()
	if got := string(s.Slice(-5, 100)); got != "ab\ncd" {
		t.Fatalf("Slice clamp %q", got)
	}
	if got := string(s.Slice(4, 2)); got != "" {
		t.Fatalf("inverted Slice %q", got)
	}
	if s.LineStart(-1) != 0 || s.LineStart(9) != 3 || s.LineEnd(9) != 5 {
		t.Fatal("line clamp")
	}
	if p := s.OffsetToPos(99); p != (Pos{1, 2}) {
		t.Fatalf("OffsetToPos clamp %v", p)
	}
	if o := s.PosToOffset(Pos{0, 99}); o != 2 {
		t.Fatalf("PosToOffset col clamp %d", o)
	}
	if o := s.PosToOffset(Pos{-3, -3}); o != 0 {
		t.Fatalf("PosToOffset neg %d", o)
	}
	if o := s.PosToOffset(Pos{7, 1}); o != 4 {
		t.Fatalf("PosToOffset line clamp %d", o)
	}
	if string(s.AppendSlice([]byte("x"), 0, 2)) != "xab" {
		t.Fatal("AppendSlice")
	}
	if c, ok := s.ByteAt(3); !ok || c != 'c' {
		t.Fatal("ByteAt")
	}
	if s.LineLen(0) != 2 {
		t.Fatal("LineLen")
	}
}

func TestCRLFLines(t *testing.T) {
	b := NewString("a\r\nbb\r\n")
	if b.EOL() != CRLF {
		t.Fatal("CRLF not detected")
	}
	if string(b.Line(1)) != "bb" || b.LineEnd(0) != 1 || b.LineStart(1) != 3 {
		t.Fatalf("line %q end %d", b.Line(1), b.LineEnd(0))
	}
	if p := b.OffsetToPos(2); p != (Pos{0, 2}) {
		t.Fatalf("pos between CR and LF = %v", p)
	}
	b.SetEOL(LF)
	if b.Snapshot().EOL() != LF {
		t.Fatal("SetEOL")
	}
}

func TestChunks(t *testing.T) {
	b := NewString("hello")
	mustReplace(t, b, 2, 0, "XY")
	mustReplace(t, b, 0, 0, "<")
	s := b.Snapshot() // "<heXYllo"
	var fw, bw []string
	s.ForEachChunk(1, 7, func(off int, c []byte) bool {
		fw = append(fw, fmt.Sprintf("%d:%s", off, c))
		return true
	})
	s.ForEachChunkReverse(1, 7, func(off int, c []byte) bool {
		bw = append(bw, fmt.Sprintf("%d:%s", off, c))
		return true
	})
	if strings.Join(fw, ",") != "1:he,3:XY,5:ll" || strings.Join(bw, ",") != "5:ll,3:XY,1:he" {
		t.Fatalf("fw %v bw %v", fw, bw)
	}
	n := 0
	s.ForEachChunk(0, 8, func(int, []byte) bool { n++; return false })
	m := 0
	s.ForEachChunkReverse(0, 8, func(int, []byte) bool { m++; return false })
	if n != 1 || m != 1 {
		t.Fatal("early stop ignored")
	}
}

func TestIndex(t *testing.T) {
	s := NewString("abcabc").Snapshot()
	if s.Index(1, []byte("abc")) != 3 || s.Index(4, []byte("abc")) != -1 || s.Index(9, []byte("a")) != -1 {
		t.Fatal("Index")
	}
	if s.Index(-2, []byte("a")) != 0 || s.Index(2, nil) != 2 {
		t.Fatal("Index edge")
	}
}

func TestEOLHelpers(t *testing.T) {
	if DetectEOL([]byte("a\nb\r\nc\n")) != LF || DetectEOL([]byte("a\r\nb\r\nc\n")) != CRLF || DetectEOL(nil) != LF {
		t.Fatal("DetectEOL")
	}
	if LF.String() != "\n" || CRLF.String() != "\r\n" {
		t.Fatal("EOL.String")
	}
	if got := string(NormalizeEOL([]byte("a\r\nb\nc\rd"), CRLF)); got != "a\r\nb\r\nc\r\nd" {
		t.Fatalf("NormalizeEOL CRLF %q", got)
	}
	if got := string(NormalizeEOL([]byte("a\r\nb\nc\rd"), LF)); got != "a\nb\nc\nd" {
		t.Fatalf("NormalizeEOL LF %q", got)
	}
	in := []byte("plain\n")
	if out := NormalizeEOL(in, LF); &out[0] != &in[0] {
		t.Fatal("NormalizeEOL copied unchanged text")
	}
}

// TestRandomAgainstReference runs random edits and compares every read API
// against a plain []byte model.
func TestRandomAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []byte("ab\n\r€")
	ref := []byte("seed\ntext\r\n")
	b := New(ref)
	var snaps []Snapshot
	var refs [][]byte
	for i := 0; i < 2000; i++ {
		off := rng.IntN(len(ref) + 1)
		n := rng.IntN(len(ref) - off + 1)
		if rng.IntN(3) == 0 {
			n = 0
		}
		ins := make([]byte, rng.IntN(6))
		for j := range ins {
			ins[j] = alphabet[rng.IntN(len(alphabet))]
		}
		mustReplace(t, b, off, n, string(ins))
		ref = append(ref[:off:off], append(ins, ref[off+n:]...)...)
		if i%97 == 0 {
			checkAgainst(t, b.Snapshot(), ref)
			snaps = append(snaps, b.Snapshot())
			refs = append(refs, bytes.Clone(ref))
		}
	}
	checkAgainst(t, b.Snapshot(), ref)
	for i, s := range snaps {
		checkAgainst(t, s, refs[i])
	}
	if d := depth(b.snap.root); d > 64 {
		t.Fatalf("tree depth %d with %d pieces", d, b.pieceCount())
	}
}
