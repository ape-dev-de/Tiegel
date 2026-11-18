package headless

import (
	"fmt"
	"io"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/modules/phpgenerate"
)

// Generate (plugin php-generate, AP17.3): list what can be generated in the
// class at a position, or generate a constructor, getters and setters.

func init() {
	flagSpec["property"] = true
	register(&command{name: "generate",
		usage:   "generate FILE (--at LINE[:COL] | --offset N) [--kind constructor|getters|setters|gettersSetters [--property NAME]... [--dry-run]]",
		summary: "Generate in the class at a position: without --kind list the options (language server actions need serve), with --kind write the code",
		minArgs: 1, maxArgs: 1, cwdProject: true, run: runGenerate})
}

func runGenerate(c *cli) error {
	if !c.p.has("at") && !c.p.has("offset") {
		return usagef("usage: tiegel --headless generate FILE (--at LINE[:COL] | --offset N) [--kind KIND]")
	}
	pos, err := parsePos(c.p.value("at"), c.p.value("offset"))
	if err != nil {
		return err
	}
	file := c.p.args[0]
	kind := c.p.value("kind")
	if kind == "" {
		var res phpgenerate.ListResult
		if err := c.call("generate.list", phpgenerate.ListParams{Path: file, Pos: pos}, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			fmt.Fprintf(w, "# class %s\n", res.Class)
			for _, a := range res.Actions {
				id := a.Kind
				if a.Source == "lsp" {
					id = "lsp:" + a.ID
				}
				line := fmt.Sprintf("%-16s %s", id, a.Title)
				if len(a.Properties) > 0 {
					line += " (" + strings.Join(a.Properties, ", ") + ")"
				}
				if a.Disabled != "" {
					line += " – " + a.Disabled
				}
				fmt.Fprintln(w, line)
			}
		})
	}
	p := phpgenerate.ApplyParams{Path: file, Pos: pos, Kind: kind, Properties: c.p.flags["property"]}
	if c.p.has("dry-run") {
		p.Apply = new(bool)
	}
	var res phpgenerate.ApplyResult
	if err := c.call("generate.apply", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		verb := "generated"
		if !res.Applied {
			verb = "would generate"
		}
		fmt.Fprintf(w, "%s %s in %s at %d:\n%s\n", verb, strings.Join(res.Methods, ", "), res.Class, res.Offset, res.Text)
	})
}
