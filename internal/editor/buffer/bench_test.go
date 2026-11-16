package buffer

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// phpSource returns a synthetic PHP file with n lines.
func phpSource(n int) []byte {
	var sb strings.Builder
	sb.WriteString("<?php\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&sb, "    $value%d = $this->compute(%d, 'string literal %d'); // comment\n", i, i, i)
	}
	return []byte(sb.String())
}

var src100k = phpSource(100_000)

func BenchmarkLoad100kLines(b *testing.B) {
	b.SetBytes(int64(len(src100k)))
	for b.Loop() {
		buf := New(src100k)
		if buf.LineCount() < 100_000 {
			b.Fatal("short")
		}
	}
}

// fragmented returns a 100k-line buffer after 10k random inserts.
func fragmented(tb testing.TB) *Buffer {
	tb.Helper()
	buf := New(src100k)
	rng := rand.New(rand.NewPCG(7, 7))
	for i := 0; i < 10_000; i++ {
		if _, err := buf.Insert(rng.IntN(buf.Len()+1), []byte("x\n")); err != nil {
			tb.Fatal(err)
		}
	}
	return buf
}

func Benchmark10kRandomInserts(b *testing.B) {
	for b.Loop() {
		fragmented(b)
	}
}

func BenchmarkInsertRandom(b *testing.B) {
	buf := fragmented(b)
	rng := rand.New(rand.NewPCG(1, 1))
	b.ResetTimer()
	for b.Loop() {
		if _, err := buf.Insert(rng.IntN(buf.Len()+1), []byte("y")); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTypingAtOneSpot(b *testing.B) {
	buf := New(src100k)
	off := buf.LineStart(50_000)
	for b.Loop() {
		if _, err := buf.Insert(off, []byte("a")); err != nil {
			b.Fatal(err)
		}
		off++
	}
}

func BenchmarkLineAccess(b *testing.B) {
	buf := fragmented(b)
	s := buf.Snapshot()
	rng := rand.New(rand.NewPCG(3, 3))
	var dst []byte
	b.ResetTimer()
	for b.Loop() {
		dst = s.AppendLine(dst[:0], rng.IntN(s.LineCount()))
	}
}

func BenchmarkLineAlloc(b *testing.B) {
	buf := fragmented(b)
	rng := rand.New(rand.NewPCG(3, 3))
	b.ResetTimer()
	for b.Loop() {
		_ = buf.Line(rng.IntN(buf.LineCount()))
	}
}

func BenchmarkOffsetToPos(b *testing.B) {
	buf := fragmented(b)
	s := buf.Snapshot()
	rng := rand.New(rand.NewPCG(5, 5))
	b.ResetTimer()
	for b.Loop() {
		_ = s.OffsetToPos(rng.IntN(s.Len()))
	}
}

// BenchmarkVisibleLines renders a screen of 60 lines, as the widget does
// per frame.
func BenchmarkVisibleLines(b *testing.B) {
	buf := fragmented(b)
	s := buf.Snapshot()
	var dst []byte
	b.ResetTimer()
	for b.Loop() {
		for i := 50_000; i < 50_060; i++ {
			dst = s.AppendLine(dst[:0], i)
		}
	}
}
