package headless

import (
	"fmt"
	"io"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Markdown reader (D534): "md" renders a Markdown file (or stdin with
// "-") as plain text, its outline, or the block model with --json.

func init() {
	flagSpec["width"] = true
	flagSpec["outline"] = false
	register(&command{name: "md", usage: "md FILE|- [--width N] [--outline]",
		summary: "render Markdown as plain text (- reads stdin), --outline: headings with anchors and lines; --json: the block model the reader draws",
		minArgs: 1, maxArgs: 1, cwdProject: true, run: runMarkdown})
}

func runMarkdown(c *cli) error {
	width, err := c.p.intValue("width")
	if err != nil {
		return err
	}
	p := api.MarkdownParams{Path: c.p.args[0], Plain: !c.json, Width: width}
	if p.Path == "-" {
		b, err := io.ReadAll(c.env.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		p.Path, p.Text = "", string(b)
		if p.Text == "" {
			// An empty input is an empty document, not a missing one.
			p.Text = "\n"
		}
	}
	var r api.MarkdownResult
	if err := c.call("md.render", p, &r); err != nil {
		return err
	}
	if c.p.has("outline") {
		return c.emit(r.Outline, func(w io.Writer) {
			for _, o := range r.Outline {
				fmt.Fprintf(w, "%5d  %s%s  #%s\n", o.Line, strings.Repeat("  ", o.Level-1), o.Text, o.Anchor)
			}
		})
	}
	return c.emit(r, func(w io.Writer) { fmt.Fprint(w, r.Plain) })
}
