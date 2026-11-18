package controller

import "context"

// InlayHint is a language server hint shown inline before the character
// at Offset (a parameter name, an inferred type).
type InlayHint struct {
	Offset   int    `json:"offset"`
	Label    string `json:"label"`
	Kind     string `json:"kind,omitempty"`
	PadLeft  bool   `json:"paddingLeft,omitempty"`
	PadRight bool   `json:"paddingRight,omitempty"`
}

// InlayHints is the answer of lsp.inlayHints for a document version.
type InlayHints struct {
	Supported bool        `json:"supported"`
	Hints     []InlayHint `json:"hints"`
	Version   uint64      `json:"version"`
}

// LoadInlayHints requests the inlay hints of file; done runs on the UI
// thread (also on errors, with an empty answer, so callers can retry).
func (c *Controller) LoadInlayHints(file string, done func(InlayHints)) {
	if !c.Status.LSPRunning {
		done(InlayHints{})
		return
	}
	c.Go(func(ctx context.Context) func() {
		var res InlayHints
		if err := c.call(ctx, "lsp.inlayHints", map[string]any{"path": file}, &res); err != nil {
			res = InlayHints{}
		}
		return func() { done(res) }
	})
}
