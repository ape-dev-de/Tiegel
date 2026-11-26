package main

import (
	"context"

	"gioui.org/layout"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/ui/atoms"
	"github.com/ape-dev-de/tiegel/internal/ui/organisms/mdview"
	"github.com/ape-dev-de/tiegel/internal/ui/tokens"
)

const mdviewSample = "## Markdown reader\n\nCode blocks are highlighted, `mermaid` blocks drawn as diagrams.\n\n" +
	"```mermaid\nflowchart LR\n  A[Request] --> B{Cached?}\n  B -->|yes| C[Serve]\n  B -->|no| D[(Database)]\n  D --> C\n```\n\n" +
	"```mermaid\nsequenceDiagram\n  Browser->>+Server: GET /\n  Server-->>-Browser: 200 OK\n```\n\n" +
	"```php\n<?php\nfunction greet(string $name): string {\n    return \"Hello, $name\"; // greeting\n}\n```\n"

// mdviewPage shows the Markdown reader with diagrams and highlighted code.
func mdviewPage() *page {
	doc := markdown.Parse([]byte(mdviewSample))
	doc.Highlight(context.Background())
	v := &mdview.View{}
	v.SetDoc(doc)
	return &page{
		Level: "Organisms", Name: "Markdown reader", Glyph: atoms.GlyphFile,
		Summary: "Read-only Markdown view: rich text, highlighted code blocks, Mermaid diagrams (Source/Diagram toggle, Copy).",
		Variants: func(th *tokens.Theme) []variant {
			return []variant{
				{"Diagrams and code", sized(900, func(gtx layout.Context) layout.Dimensions {
					v.Update(gtx)
					return v.Layout(gtx, th)
				})},
			}
		},
	}
}
