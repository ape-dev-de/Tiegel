//go:build cgo

package syntaxmod

import (
	"reflect"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/modtest"
)

const phpSrc = "<?php\nnamespace App;\n\nfinal class Kernel\n{\n    public function boot(int $x): void\n    {\n        echo 'ä';\n    }\n}\n"

func TestPHP(t *testing.T) {
	a, m := newApp(t)
	modtest.Project(t, a, map[string]string{"src/Kernel.php": phpSrc, "c.json": "{\n  \"a\": [\n    1\n  ]\n}\n"})

	// From disk.
	var h HighlightsResult
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: "src/Kernel.php"}}, &h)
	if h.Language != "php" || h.Open || h.Version != 0 || len(h.Spans) == 0 {
		t.Fatalf("disk %+v", h)
	}
	roles := map[string]bool{}
	for _, s := range h.Spans {
		roles[s.Role] = true
	}
	if !roles["keyword"] || !roles["string"] || !roles["variable"] {
		t.Fatalf("roles %v", roles)
	}

	var o OutlineResult
	modtest.Call(t, a, "syntax.outline", Source{Path: "src/Kernel.php"}, &o)
	if len(o.Symbols) != 1 || o.Symbols[0].Kind != "namespace" {
		t.Fatalf("outline %+v", o.Symbols)
	}
	cls := o.Symbols[0].Children[0]
	if cls.Kind != "class" || cls.Name != "Kernel" || cls.Start.Line != 4 || cls.NameStart.Col != 13 || len(cls.Modifiers) != 1 {
		t.Fatalf("class %+v", cls)
	}
	boot := cls.Children[0]
	if boot.Kind != "method" || boot.Name != "boot" || boot.Detail == "" || boot.End.Line != 9 {
		t.Fatalf("method %+v", boot)
	}

	var f FoldsResult
	modtest.Call(t, a, "syntax.folds", Source{Path: "c.json"}, &f)
	if f.Language != "json" || len(f.Folds) == 0 || f.Folds[0] != (Fold{1, 5}) {
		t.Fatalf("folds %+v", f)
	}

	// Open document: incremental updates from doc.edited.
	modtest.Call(t, a, "doc.open", api.DocOpenParams{Path: "src/Kernel.php"}, nil)
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: "src/Kernel.php"}}, &h)
	if !h.Open || h.Version != 0 {
		t.Fatalf("open %+v", h)
	}
	first := h.Spans
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: "src/Kernel.php"}}, &h) // cached
	if !reflect.DeepEqual(first, h.Spans) {
		t.Fatal("cached result differs")
	}
	for i := 0; i < 2; i++ {
		modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "src/Kernel.php", At: api.Pos{Line: 2}, Text: "// c\n"}, nil)
	}
	drain(t, a)
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: "src/Kernel.php"}}, &h)
	if h.Version != 2 {
		t.Fatalf("version %d", h.Version)
	}
	var text api.DocText
	modtest.Call(t, a, "doc.text", api.PathParams{Path: "src/Kernel.php"}, &text)
	var fresh HighlightsResult
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Text: &text.Text, Language: "php"}}, &fresh)
	if !reflect.DeepEqual(fresh.Spans, h.Spans) {
		t.Fatalf("incremental differs from fresh parse\n%v\n%v", h.Spans, fresh.Spans)
	}
	modtest.Call(t, a, "syntax.outline", Source{Path: "src/Kernel.php"}, &o)
	if o.Symbols[0].Children[0].Start.Line != 6 {
		t.Fatalf("outline after edit %+v", o.Symbols[0].Children[0])
	}

	// A missed edit (simulated by a gap) forces a fresh parse.
	m.mu.Lock()
	for _, e := range m.docs {
		e.edited--
	}
	m.mu.Unlock()
	modtest.Call(t, a, "doc.insert", api.DocInsertParams{Path: "src/Kernel.php", At: api.Pos{Line: 1}, Text: "\n"}, nil)
	drain(t, a)
	modtest.Call(t, a, "syntax.outline", Source{Path: "src/Kernel.php"}, &o)
	if o.Version != 3 || o.Symbols[0].Children[0].Start.Line != 7 {
		t.Fatalf("after resync %+v", o)
	}
	// Language override on an open document re-creates the parse state.
	modtest.Call(t, a, "syntax.highlights", HighlightsParams{Source: Source{Path: "src/Kernel.php", Language: "plaintext"}}, &h)
	if len(h.Spans) != 0 {
		t.Fatalf("plaintext override %+v", h.Spans)
	}

	modtest.Call(t, a, "doc.close", api.DocCloseParams{Path: "src/Kernel.php", Discard: true}, nil)
	drain(t, a)
	m.mu.Lock()
	n := len(m.docs)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d documents still tracked", n)
	}
}
