//go:build cgo

package phpver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// BenchmarkCheck10kLines runs the full default table over a ~10k-line file.
func BenchmarkCheck10kLines(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("<?php\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, `enum E%[1]d: string { case A = 'a'; }
final class C%[1]d
{
    public function __construct(private readonly int $x = 0) {}
    public function f(int|string $v): static
    {
        $r = match ($v) { 1 => 'a', default => 'b' };
        $o = $this?->x;
        $f = strlen(...);
        $s = "a ${r}";
        return $this;
    }
}
function g%[1]d(Foo $x = null): never { throw new E(); }

`, i)
	}
	src := sb.String()
	doc, err := syntax.NewDocument(context.Background(), syntax.LangPHP, []byte(src))
	if err != nil {
		b.Fatal(err)
	}
	defer doc.Close()
	b.ReportMetric(float64(strings.Count(src, "\n")), "lines")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ds, err := Check(doc, Version{7, 4, 0})
		if err != nil || len(ds) == 0 {
			b.Fatal(err)
		}
	}
}
