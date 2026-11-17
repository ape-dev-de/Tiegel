package wrap

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/editor/buffer"
)

// doc100k is a 100k-line document of code-like lines: indented, mostly
// short, every tenth line long enough to wrap at 80 cells.
var doc100k = func() string {
	rng := rand.New(rand.NewSource(3))
	words := []string{"$foo", "->bar()", "=", "return", "array_map(", "'string literal'", "$this", ";", "if", "{"}
	var sb strings.Builder
	for l := 0; l < 100_000; l++ {
		sb.WriteString("        "[:rng.Intn(3)*4])
		n := rng.Intn(8)
		if l%10 == 0 {
			n = 30
		}
		for i := 0; i < n; i++ {
			sb.WriteString(words[rng.Intn(len(words))])
			sb.WriteByte(' ')
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}()

// longDoc100k has 100k long lines (most wrap), the worst case of Reset.
var longDoc100k = randomDoc(rand.New(rand.NewSource(3)), 100_000)

// BenchmarkReset100k measures wrapping a 100k-line document from scratch
// (opening a file, changing the window width).
func BenchmarkReset100k(b *testing.B) {
	s := buffer.NewString(doc100k).Snapshot()
	var m Model
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Reset(s, 80+i%2, 4)
	}
}

// BenchmarkResetLong100k is BenchmarkReset100k with mostly wrapping
// lines.
func BenchmarkResetLong100k(b *testing.B) {
	s := buffer.NewString(longDoc100k).Snapshot()
	var m Model
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Reset(s, 80+i%2, 4)
	}
}

// BenchmarkEdit100k measures re-wrapping after typing a character in the
// middle of a 100k-line document.
func BenchmarkEdit100k(b *testing.B) {
	buf := buffer.NewString(doc100k)
	var m Model
	m.Reset(buf.Snapshot(), 80, 4)
	buf.OnEdit(func(e buffer.Edit) { m.ApplyEdit(buf.Snapshot(), e) })
	at := buf.LineStart(50_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			_, _ = buf.Replace(at, 0, []byte("x"))
		} else {
			_, _ = buf.Replace(at, 1, nil)
		}
		_ = m.Total()
	}
}

// BenchmarkNewline100k measures re-wrapping after inserting and removing
// a line break near the top (the row sums of all blocks below change).
func BenchmarkNewline100k(b *testing.B) {
	buf := buffer.NewString(doc100k)
	var m Model
	m.Reset(buf.Snapshot(), 80, 4)
	buf.OnEdit(func(e buffer.Edit) { m.ApplyEdit(buf.Snapshot(), e) })
	at := buf.LineStart(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			_, _ = buf.Replace(at, 0, []byte("\n"))
		} else {
			_, _ = buf.Replace(at, 1, nil)
		}
		_ = m.Total()
	}
}

// BenchmarkLineAt100k measures mapping a row to its line.
func BenchmarkLineAt100k(b *testing.B) {
	var m Model
	m.Reset(buffer.NewString(doc100k).Snapshot(), 80, 4)
	total := m.Total()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.LineAt((i * 7919) % total)
	}
}
