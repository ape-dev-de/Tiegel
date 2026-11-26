package markdown

import (
	"context"
	"strings"
	"testing"
)

func TestHighlightCodeBlocks(t *testing.T) {
	d := Parse([]byte("```go\nfunc main() {}\n```\n\n> ```sh\n> echo $HOME\n> ```\n\n```\nplain\n```\n\n```nope\nx\n```\n"))
	d.Highlight(context.Background())
	var none *Doc
	none.Highlight(context.Background())
	if b := d.Blocks[0]; len(b.Tokens) == 0 || b.Tokens[0].Role != "keyword" || b.Code[b.Tokens[0].Start:b.Tokens[0].End] != "func" {
		t.Fatalf("go tokens %+v", b.Tokens)
	}
	nested := d.Blocks[1].Children[0]
	if nested.Kind != CodeBlock || len(nested.Tokens) == 0 {
		t.Fatalf("nested code block not highlighted: %+v", nested)
	}
	if len(d.Blocks[2].Tokens) != 0 || len(d.Blocks[3].Tokens) != 0 {
		t.Fatal("plain or unknown blocks got tokens")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d2 := Parse([]byte("```go\nfunc x() {}\n```\n"))
	d2.Highlight(ctx)
	if len(d2.Blocks[0].Tokens) != 0 {
		t.Fatal("cancelled highlight still ran")
	}
}

func TestPlainMermaidPlaceholder(t *testing.T) {
	out := Parse([]byte("```mermaid\nsequenceDiagram\nA->>B: hi\n```\n\n```mermaid\nnonsense\n```\n")).PlainText(0)
	if !strings.Contains(out, "[mermaid diagram: sequence]\n    sequenceDiagram") || !strings.Contains(out, "[mermaid diagram: unknown type]") {
		t.Fatalf("plain %q", out)
	}
}
