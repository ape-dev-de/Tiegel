package api

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/markdown"
	"github.com/ape-dev-de/tiegel/internal/mermaid"
)

// Mermaid diagrams (D551): mermaid.render parses and lays out Mermaid
// sources – a diagram file or text, or every "```mermaid" block of a
// Markdown file – into the geometric model of internal/mermaid, optionally
// as SVG. Parse errors are reported per diagram (with line), never as a
// failed call, so a reader can show the source instead.

// MermaidParams are the params of "mermaid.render". Path (absolute or
// project relative) or Text is required; Text wins. Markdown files (.md,
// .markdown) or Markdown: true render every mermaid block.
type MermaidParams struct {
	Path     string `json:"path,omitempty"`
	Text     string `json:"text,omitempty"`
	Markdown bool   `json:"markdown,omitempty"`
	// Format "svg" adds the SVG document, "json" (default) only the model.
	Format string `json:"format,omitempty"`
	// Theme of the SVG: "light" (default) or "dark".
	Theme string `json:"theme,omitempty"`
}

// MermaidDiagram is one rendered diagram.
type MermaidDiagram struct {
	// Line is the 1-based source line of the diagram (Markdown: the fence).
	Line    int              `json:"line"`
	Kind    string           `json:"kind,omitempty"`
	Diagram *mermaid.Diagram `json:"diagram,omitempty"`
	SVG     string           `json:"svg,omitempty"`
	Error   *mermaid.Error   `json:"error,omitempty"`
}

// MermaidResult is the result of "mermaid.render".
type MermaidResult struct {
	Path     string           `json:"path,omitempty"`
	Diagrams []MermaidDiagram `json:"diagrams"`
}

func (a *App) registerMermaid() {
	mustHandle(a, "mermaid.render", "Lay out Mermaid diagrams (a diagram file or text, or every mermaid block of a Markdown file) as a geometric model; format svg: also as SVG", a.mermaidRender)
}

func (a *App) mermaidRender(ctx context.Context, p MermaidParams) (MermaidResult, error) {
	var res MermaidResult
	src := p.Text
	md := p.Markdown
	if src == "" {
		if p.Path == "" {
			return res, Errorf(CodeInvalidParams, "path or text is required")
		}
		abs, err := a.resolvePath(p.Path)
		if err != nil {
			return res, err
		}
		b, _, err := a.ReadText(abs)
		if err != nil {
			return res, err
		}
		src, res.Path = string(b), abs
		switch strings.ToLower(filepath.Ext(abs)) {
		case ".md", ".markdown", ".mdown":
			md = true
		}
	}
	switch p.Format {
	case "", "json", "svg":
	default:
		return res, Errorf(CodeInvalidParams, "format must be json or svg")
	}
	theme := mermaid.LightTheme
	switch p.Theme {
	case "", "light":
	case "dark":
		theme = mermaid.DarkTheme
	default:
		return res, Errorf(CodeInvalidParams, "theme must be light or dark")
	}
	type block struct {
		line int
		code string
	}
	var blocks []block
	if md {
		var walk func(bs []markdown.Block)
		walk = func(bs []markdown.Block) {
			for _, b := range bs {
				if b.Kind == markdown.CodeBlock && (strings.EqualFold(b.Lang, "mermaid") || strings.EqualFold(b.Lang, "mmd")) {
					blocks = append(blocks, block{b.Line, b.Code})
				}
				walk(b.Children)
			}
		}
		walk(markdown.Parse([]byte(src)).Blocks)
	} else {
		blocks = []block{{1, src}}
	}
	res.Diagrams = []MermaidDiagram{}
	for _, b := range blocks {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		out := MermaidDiagram{Line: b.line, Kind: mermaid.Kind(b.code)}
		d, err := mermaid.Render(b.code, mermaid.Options{})
		if err != nil {
			me, ok := err.(*mermaid.Error)
			if !ok {
				me = &mermaid.Error{Msg: err.Error()}
			}
			out.Error = me
		} else {
			out.Diagram = d
			if p.Format == "svg" {
				out.SVG = d.SVG(theme)
			}
		}
		res.Diagrams = append(res.Diagrams, out)
	}
	return res, nil
}
