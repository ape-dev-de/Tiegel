//go:build cgo

package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// synthProject writes n PHP files: every 10th file is an interface, the
// others are classes extending the previous class of their namespace and
// implementing an interface, with 12 methods (~150 lines per file).
func synthProject(tb testing.TB, n int) string {
	tb.Helper()
	root := tb.TempDir()
	for i := 0; i < n; i++ {
		ns := fmt.Sprintf("App\\Mod%d", i/100)
		var b strings.Builder
		fmt.Fprintf(&b, "<?php\ndeclare(strict_types=1);\n\nnamespace %s;\n\nuse App\\Mod0\\Iface0;\nuse Psr\\Log\\LoggerInterface as Logger;\n\n", ns)
		if i%10 == 0 {
			fmt.Fprintf(&b, "interface Iface%d extends \\App\\Mod0\\Iface0\n{\n", i)
			for m := 0; m < 6; m++ {
				fmt.Fprintf(&b, "    public function op%d(int $a, string $b): ?array;\n", m)
			}
			b.WriteString("}\n")
		} else {
			ext := ""
			if i%10 > 1 {
				ext = fmt.Sprintf(" extends Class%d", i-1)
			}
			fmt.Fprintf(&b, "/**\n * Generated class %d.\n */\nclass Class%d%s implements Iface%d\n{\n", i, i, ext, i/10*10)
			b.WriteString("    private array $items = [];\n    protected ?Logger $logger = null;\n    public const LIMIT = 10;\n\n")
			for m := 0; m < 12; m++ {
				fmt.Fprintf(&b, "    public function op%d(int $a, string $b): ?array\n    {\n        $out = [];\n        foreach ($this->items as $k => $v) {\n            if ($k > $a && strlen($b) < self::LIMIT) {\n                $out[] = $v . $b;\n            }\n        }\n        $this->logger?->info('op%d');\n        return $out ?: null;\n    }\n\n", m, m)
			}
			b.WriteString("}\n")
		}
		dir := filepath.Join(root, "src", fmt.Sprintf("Mod%d", i/100))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		name := fmt.Sprintf("Class%d.php", i)
		if i%10 == 0 {
			name = fmt.Sprintf("Iface%d.php", i)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}

const benchFiles = 5000

func BenchmarkSync5000Files(b *testing.B) {
	root := synthProject(b, benchFiles)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix := New(Options{Root: root})
		if _, err := ix.Sync(context.Background(), nil); err != nil {
			b.Fatal(err)
		}
		ix.Close()
	}
}

func BenchmarkResyncUnchanged5000Files(b *testing.B) {
	root := synthProject(b, benchFiles)
	ix := build(b, root, Options{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ix.Sync(context.Background(), nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCacheLoad5000Files(b *testing.B) {
	root := synthProject(b, benchFiles)
	ix := build(b, root, Options{})
	dir := b.TempDir()
	if err := ix.Save(dir); err != nil {
		b.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(dir, CacheFile)); err == nil {
		b.ReportMetric(float64(st.Size())/1024, "cacheKiB")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix2 := New(Options{Root: root})
		if err := ix2.Load(dir); err != nil {
			b.Fatal(err)
		}
	}
}

// benchQueries runs the position queries against a big index.
func BenchmarkQueries5000Files(b *testing.B) {
	root := synthProject(b, benchFiles)
	ix := build(b, root, Options{})
	iface := filepath.Join(root, "src", "Mod0", "Iface0.php")
	isrc, _ := os.ReadFile(iface)
	ioff := strings.Index(string(isrc), "Iface0\n") + 2
	moff := strings.Index(string(isrc), "op3") + 1
	cls := filepath.Join(root, "src", "Mod25", "Class2509.php")
	csrc, _ := os.ReadFile(cls)
	coff := strings.Index(string(csrc), "op5") + 1
	b.Run("definition", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if locs, _ := ix.Definition(cls, csrc, coff); len(locs) != 1 {
				b.Fatal(locs)
			}
		}
	})
	b.Run("implementations-interface", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if locs, _ := ix.Implementations(iface, isrc, ioff); len(locs) < 4000 {
				b.Fatal(len(locs))
			}
		}
	})
	b.Run("implementations-method", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if locs, _ := ix.Implementations(iface, isrc, moff); len(locs) < 4000 {
				b.Fatal(len(locs))
			}
		}
	})
	b.Run("super", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if locs, _ := ix.Super(cls, csrc, coff); len(locs) == 0 {
				b.Fatal(locs)
			}
		}
	})
	b.Run("gutter", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if marks := ix.Gutter(cls); len(marks) == 0 {
				b.Fatal("no marks")
			}
		}
	})
	b.Run("symbols-class", func(b *testing.B) {
		ix.Symbols(SymbolQuery{Query: "x"}) // build the table once
		for i := 0; i < b.N; i++ {
			if r := ix.Symbols(SymbolQuery{Query: "cls251", Kinds: []Kind{KindClass}, Limit: 50}); len(r) == 0 {
				b.Fatal("no result")
			}
		}
	})
	b.Run("symbols-all", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if r := ix.Symbols(SymbolQuery{Query: "op1", Limit: 50}); len(r) == 0 {
				b.Fatal("no result")
			}
		}
	})
}

// BenchmarkDir indexes a real project (read only), e.g. a Composer project
// with vendor/: TIEGEL_INDEX_BENCH_DIR=/path/to/project. The cache goes to a
// temporary directory.
func BenchmarkDir(b *testing.B) {
	dir := os.Getenv("TIEGEL_INDEX_BENCH_DIR")
	if dir == "" {
		b.Skip("TIEGEL_INDEX_BENCH_DIR not set")
	}
	ix := New(Options{Root: dir})
	defer ix.Close()
	b.Run("sync", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ix.Reset()
			if _, err := ix.Sync(context.Background(), nil); err != nil {
				b.Fatal(err)
			}
		}
		st := ix.Stats()
		b.ReportMetric(float64(st.Files), "files")
		b.ReportMetric(float64(st.Classes), "classes")
	})
	b.Run("resync", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := ix.Sync(context.Background(), nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	cache := b.TempDir()
	b.Run("save", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := ix.Save(cache); err != nil {
				b.Fatal(err)
			}
		}
		if st, err := os.Stat(filepath.Join(cache, CacheFile)); err == nil {
			b.ReportMetric(float64(st.Size())/(1<<20), "MiB")
		}
	})
	b.Run("load", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ix2 := New(Options{Root: dir})
			if err := ix2.Load(cache); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("symbols-class", func(b *testing.B) {
		ix.Symbols(SymbolQuery{Query: "x"})
		for i := 0; i < b.N; i++ {
			ix.Symbols(SymbolQuery{Query: "ctrl", Kinds: []Kind{KindClass, KindInterface, KindTrait, KindEnum}, Limit: 50})
		}
	})
	b.Run("symbols-all", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ix.Symbols(SymbolQuery{Query: "get", Limit: 50})
		}
	})
	b.Run("implementations-countable", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ix.ClassImplementations("Countable")
		}
	})
}
