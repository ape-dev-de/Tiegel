package headless

import (
	"fmt"
	"io"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/langphp"
)

// Inlay hints (AP17.3): the language server's parameter-name and type
// hints of a file, printed as "file:line:col: [kind] label".

func init() {
	register(&command{name: "inlay-hints", usage: "inlay-hints FILE [--server ID] [--command PATH]",
		summary: "inlay hints (parameter names, inferred types) of a file from the language server",
		minArgs: 1, maxArgs: 1, cwdProject: true, run: runInlayHints})
}

func runInlayHints(c *cli) error {
	return withLSP(c, c.p.args, func(opened []api.DocOpenResult) error {
		var res langphp.InlayHintsResult
		if err := c.call("lsp.inlayHints", langphp.InlayHintsParams{Path: c.p.args[0]}, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			if !res.Supported {
				fmt.Fprintln(c.env.Stderr, "the language server provides no inlay hints")
				return
			}
			for _, h := range res.Hints {
				kind := h.Kind
				if kind == "" {
					kind = "other"
				}
				fmt.Fprintf(w, "%s:%d:%d: [%s] %s\n", opened[0].Path, h.Line, h.Col, kind, h.Label)
			}
		})
	})
}
