package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/markdown"
)

func TestMarkdownRender(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Hello\n\nSee [docs](docs/a.md).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, a, "project.open", map[string]string{"path": dir}, nil)

	var r MarkdownResult
	call(t, a, "md.render", MarkdownParams{Path: "README.md", Plain: true}, &r)
	if r.Doc == nil || len(r.Doc.Blocks) != 2 || r.Doc.Blocks[0].Kind != markdown.Heading || !strings.HasSuffix(r.Path, "README.md") {
		t.Fatalf("render %+v", r)
	}
	if !strings.Contains(r.Plain, "docs <docs/a.md>") || len(r.Outline) != 1 || r.Outline[0].Anchor != "hello" {
		t.Fatalf("plain %q outline %+v", r.Plain, r.Outline)
	}
	var o []markdown.OutlineEntry
	call(t, a, "md.outline", MarkdownParams{Text: "## A\n### B\n"}, &o)
	if len(o) != 2 || o[1].Level != 3 {
		t.Fatalf("outline %+v", o)
	}
	call(t, a, "md.outline", MarkdownParams{Text: "plain"}, &o)
	if o == nil || len(o) != 0 {
		t.Fatalf("empty outline %#v", o)
	}
	wantCode(t, a, "md.render", MarkdownParams{}, CodeInvalidParams)
	wantCode(t, a, "md.render", MarkdownParams{Path: "missing.md"}, CodeNotFound)
}
