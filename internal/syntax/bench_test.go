package syntax

import (
	"bytes"
	"context"
	"testing"
)

const benchLines = 10000

func BenchmarkParse10kLines(b *testing.B) {
	src := bigPHP(benchLines)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d, err := NewDocument(context.Background(), LangPHP, src)
		if err != nil {
			b.Fatal(err)
		}
		d.Close()
	}
}

// BenchmarkEditReparse10kLines measures one keystroke in the middle of a
// 10k-line file: Edit + incremental Reparse. Insertions and deletions
// alternate so the text stays stable.
func BenchmarkEditReparse10kLines(b *testing.B) {
	src := bigPHP(benchLines)
	d := newDoc(b, LangPHP, string(src))
	pos := bytes.Index(src[len(src)/2:], []byte("$total = 0;")) + len(src)/2 + len("$total")
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			src = applyEditB(b, ctx, d, src, pos, pos, "x")
		} else {
			src = applyEditB(b, ctx, d, src, pos, pos+1, "")
		}
	}
}

func applyEditB(b *testing.B, ctx context.Context, d *Document, src []byte, start, end int, ins string) []byte {
	d.Edit(NewInputEdit(src, start, end, []byte(ins)))
	out := make([]byte, 0, len(src)+len(ins))
	out = append(out, src[:start]...)
	out = append(out, ins...)
	out = append(out, src[end:]...)
	if err := d.Reparse(ctx, out); err != nil {
		b.Fatal(err)
	}
	return out
}

// BenchmarkHighlightsViewport10kLines highlights ~60 lines in the middle of a
// 10k-line file, as a frame of the editor would.
func BenchmarkHighlightsViewport10kLines(b *testing.B) {
	src := bigPHP(benchLines)
	d := newDoc(b, LangPHP, string(src))
	start := len(src) / 2
	end := start + 60*40
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(d.Highlights(start, end)) == 0 {
			b.Fatal("no spans")
		}
	}
}

func BenchmarkFolds10kLines(b *testing.B) {
	src := bigPHP(benchLines)
	d := newDoc(b, LangPHP, string(src))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.Folds()
	}
}

func BenchmarkOutline10kLines(b *testing.B) {
	src := bigPHP(benchLines)
	d := newDoc(b, LangPHP, string(src))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = d.Outline()
	}
}
