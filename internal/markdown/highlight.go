package markdown

import (
	"context"

	"github.com/ape-dev-de/tiegel/internal/syntax"
)

// Highlight fills the Tokens of every code block (also nested in quotes
// and lists) whose language internal/syntax knows (fence aliases such as
// "yml", "shell" or "ts" included). Other code blocks stay plain. It
// stops early when ctx is done.
func (d *Doc) Highlight(ctx context.Context) {
	if d == nil {
		return
	}
	highlightBlocks(ctx, d.Blocks)
}

func highlightBlocks(ctx context.Context, bs []Block) {
	for i := range bs {
		if ctx.Err() != nil {
			return
		}
		b := &bs[i]
		if b.Kind == CodeBlock && b.Lang != "" && b.Code != "" {
			b.Tokens = nil
			for _, s := range syntax.HighlightSnippet(ctx, b.Lang, []byte(b.Code)) {
				b.Tokens = append(b.Tokens, Token{Start: s.Start, End: s.End, Role: s.Role.String()})
			}
		}
		highlightBlocks(ctx, b.Children)
	}
}
