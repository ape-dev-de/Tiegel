package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMermaidRender(t *testing.T) {
	a := newTestApp(t)
	dir := t.TempDir()
	md := "# Doc\n\n```mermaid\nflowchart LR\nA-->B\n```\n\n- ```mermaid\n  pie\n  \"x\": 1\n  ```\n\n```mermaid\nflowchart TD\nA-->\n```\n"
	for name, body := range map[string]string{"README.md": md, "flow.mmd": "sequenceDiagram\nA->>B: hi\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	call(t, a, "project.open", map[string]string{"path": dir}, nil)

	var r MermaidResult
	call(t, a, "mermaid.render", MermaidParams{Path: "README.md", Format: "svg", Theme: "dark"}, &r)
	if len(r.Diagrams) != 3 || r.Diagrams[0].Kind != "flowchart" || r.Diagrams[0].Line != 3 || !strings.HasPrefix(r.Diagrams[0].SVG, "<svg") {
		t.Fatalf("markdown: %+v", r)
	}
	if r.Diagrams[1].Kind != "pie" || r.Diagrams[2].Error == nil || r.Diagrams[2].Error.Line != 2 || r.Diagrams[2].Diagram != nil {
		t.Fatalf("nested/error diagrams: %+v %+v", r.Diagrams[1], r.Diagrams[2])
	}
	call(t, a, "mermaid.render", MermaidParams{Path: "flow.mmd"}, &r)
	if len(r.Diagrams) != 1 || r.Diagrams[0].Kind != "sequence" || r.Diagrams[0].SVG != "" || r.Diagrams[0].Diagram == nil {
		t.Fatalf("file: %+v", r)
	}
	call(t, a, "mermaid.render", MermaidParams{Text: "```mermaid\npie\n\"a\":1\n```", Markdown: true}, &r)
	if len(r.Diagrams) != 1 || r.Diagrams[0].Kind != "pie" {
		t.Fatalf("markdown text: %+v", r)
	}
	wantCode(t, a, "mermaid.render", MermaidParams{}, CodeInvalidParams)
	wantCode(t, a, "mermaid.render", MermaidParams{Text: "pie", Format: "png"}, CodeInvalidParams)
	wantCode(t, a, "mermaid.render", MermaidParams{Text: "pie", Theme: "blue"}, CodeInvalidParams)
	wantCode(t, a, "mermaid.render", MermaidParams{Path: "missing.mmd"}, CodeNotFound)

	var mr MarkdownResult
	call(t, a, "md.render", MarkdownParams{Text: "```go\nfunc x() {}\n```\n"}, &mr)
	if len(mr.Doc.Blocks) != 1 || len(mr.Doc.Blocks[0].Tokens) == 0 {
		t.Fatalf("md.render did not highlight: %+v", mr.Doc.Blocks)
	}
}
