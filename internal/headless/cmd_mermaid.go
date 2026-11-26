package headless

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
)

// Mermaid diagrams (D546): "mermaid" lays out a diagram file (or every
// mermaid block of a Markdown file, or stdin with "-") and prints a
// summary, writes SVG (--svg OUT, "-" for stdout) or prints the model
// (--json).

func init() {
	flagSpec["svg"] = true
	flagSpec["theme"] = true
	flagSpec["markdown"] = false
	register(&command{name: "mermaid", usage: "mermaid FILE|- [--svg OUT|-] [--theme light|dark] [--markdown]",
		summary: "lay out Mermaid diagrams (a .mmd file, all mermaid blocks of a .md file, - reads stdin); --svg writes SVG (several diagrams: OUT-2.svg, …), --json: the geometric model",
		minArgs: 1, maxArgs: 1, cwdProject: true, run: runMermaid})
}

func runMermaid(c *cli) error {
	p := api.MermaidParams{Path: c.p.args[0], Markdown: c.p.has("markdown"), Theme: c.p.value("theme")}
	out := c.p.value("svg")
	if out != "" {
		p.Format = "svg"
	}
	if p.Path == "-" {
		b, err := io.ReadAll(c.env.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		p.Path, p.Text = "", string(b)
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("mermaid: empty input")
		}
	}
	var r api.MermaidResult
	if err := c.call("mermaid.render", p, &r); err != nil {
		return err
	}
	failed := 0
	for _, d := range r.Diagrams {
		if d.Error != nil {
			failed++
		}
	}
	if out != "" {
		n := 0
		for _, d := range r.Diagrams {
			if d.SVG == "" {
				continue
			}
			n++
			if out == "-" {
				fmt.Fprint(c.env.Stdout, d.SVG)
				continue
			}
			name := out
			if n > 1 {
				ext := filepath.Ext(out)
				name = strings.TrimSuffix(out, ext) + "-" + strconv.Itoa(n) + ext
			}
			if err := os.WriteFile(name, []byte(d.SVG), 0o644); err != nil {
				return fmt.Errorf("write svg: %w", err)
			}
			fmt.Fprintf(c.env.Stderr, "wrote %s (line %d, %s)\n", name, d.Line, d.Kind)
		}
		for _, d := range r.Diagrams {
			if d.Error != nil {
				fmt.Fprintf(c.env.Stderr, "line %d: %s\n", d.Line, d.Error.Error())
			}
		}
	} else if err := c.emit(r, func(w io.Writer) {
		if len(r.Diagrams) == 0 {
			fmt.Fprintln(w, "no mermaid diagrams")
		}
		for _, d := range r.Diagrams {
			if d.Error != nil {
				fmt.Fprintf(w, "%5d  error: %s\n", d.Line, d.Error.Error())
				continue
			}
			fmt.Fprintf(w, "%5d  %-9s %4.0fx%-4.0f %d nodes, %d edges\n", d.Line, d.Kind, d.Diagram.Width, d.Diagram.Height,
				len(d.Diagram.Nodes), len(d.Diagram.Edges))
		}
	}); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d diagrams failed to parse", failed, len(r.Diagrams))
	}
	return nil
}
