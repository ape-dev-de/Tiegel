package headless

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/modules/phpindex"
)

func init() {
	flagSpec["kind"] = true
	flagSpec["source"] = true
	flagSpec["no-libraries"] = false
	flagSpec["full"] = false
	register(
		&command{name: "symbols", usage: "symbols QUERY [--kind K]... [--limit N] [--no-libraries]", summary: "fuzzy search for classes and symbols in the PHP index: kind fqn path:line:col", minArgs: 1, maxArgs: 1, cwdProject: true, run: runSymbols},
		&command{name: "definition", usage: "definition FILE LINE[:COL] [--source auto|index|lsp]", summary: "declarations of the symbol at a position (PHP index)", minArgs: 2, maxArgs: 2, cwdProject: true, run: runNav("nav.definition")},
		&command{name: "implementations", usage: "implementations FILE LINE[:COL] [--source auto|index|lsp]", summary: "implementations, subclasses and overriding methods of the symbol at a position", minArgs: 2, maxArgs: 2, cwdProject: true, run: runNav("nav.implementations")},
		&command{name: "super", usage: "super FILE LINE[:COL]", summary: "supertypes of a class or the methods a method overrides/implements", minArgs: 2, maxArgs: 2, cwdProject: true, run: runNav("nav.super")},
		&command{name: "hierarchy", usage: "hierarchy FQN | hierarchy FILE LINE[:COL]", summary: "type hierarchy (supertypes and subtypes) of a class", minArgs: 1, maxArgs: 2, cwdProject: true, run: runHierarchy},
		&command{name: "gutter", usage: "gutter FILE", summary: "implements/overrides marks of a file: line kind fqn up N down M", minArgs: 1, maxArgs: 1, cwdProject: true, run: runGutter},
		&command{name: "index-status", usage: "index-status [--rebuild [--full]]", summary: "build (or update from the cache) the PHP symbol index and show its state", minArgs: 0, maxArgs: 0, cwdProject: true, run: runIndexStatus},
	)
	flagSpec["rebuild"] = false
}

// parseLineCol parses "LINE[:COL]" (1-based).
func parseLineCol(s string) (api.Pos, error) {
	l, c, hasCol := strings.Cut(s, ":")
	line, err := strconv.Atoi(l)
	if err != nil || line < 1 {
		return api.Pos{}, usagef("position must be LINE[:COL] (1-based), got %q", s)
	}
	col := 1
	if hasCol {
		if col, err = strconv.Atoi(c); err != nil || col < 1 {
			return api.Pos{}, usagef("position must be LINE[:COL] (1-based), got %q", s)
		}
	}
	return api.Pos{Line: line, Col: col}, nil
}

func locString(l phpindex.Location) string {
	if l.Path == "" {
		return "(not indexed)"
	}
	return fmt.Sprintf("%s:%d:%d", l.Path, l.Range.Start.Line, l.Range.Start.Col)
}

func runNav(method string) func(c *cli) error {
	return func(c *cli) error {
		at, err := parseLineCol(c.p.args[1])
		if err != nil {
			return err
		}
		var res phpindex.NavResult
		if err := c.call(method, phpindex.PosParams{Path: c.p.args[0], At: at, Source: c.p.value("source"), Wait: true}, &res); err != nil {
			return err
		}
		return c.emit(res, func(w io.Writer) {
			for _, l := range res.Locations {
				name := l.FQN
				if name == "" {
					name = "-"
				}
				kind := l.Kind
				if kind == "" {
					kind = "-"
				}
				lib := ""
				if l.Library {
					lib = " [library]"
				}
				fmt.Fprintf(w, "%s: %s %s [%s]%s\n", locString(l), kind, name, l.Source, lib)
			}
			if res.LSPError != "" && c.p.value("source") == "lsp" {
				fmt.Fprintf(c.env.Stderr, "tiegel: language server: %s\n", res.LSPError)
			}
		})
	}
}

func runSymbols(c *cli) error {
	limit, err := c.p.intValue("limit")
	if err != nil {
		return err
	}
	p := phpindex.SymbolsParams{Query: c.p.args[0], Kinds: c.p.flags["kind"], Limit: limit, Wait: true}
	if c.p.has("no-libraries") {
		no := false
		p.Libraries = &no
	}
	var res phpindex.SymbolsResult
	if err := c.call("nav.symbols", p, &res); err != nil {
		return err
	}
	if len(res.Symbols) == 0 && !c.json {
		return &quietError{code: ExitError}
	}
	return c.emit(res, func(w io.Writer) {
		for _, s := range res.Symbols {
			fmt.Fprintf(w, "%s\t%s\t%s\n", s.Kind, s.FQN, locString(s.Location))
		}
	})
}

func runHierarchy(c *cli) error {
	p := phpindex.HierarchyParams{Wait: true}
	if len(c.p.args) == 2 {
		at, err := parseLineCol(c.p.args[1])
		if err != nil {
			return err
		}
		p.Path, p.At = c.p.args[0], &at
	} else {
		p.FQN = c.p.args[0]
	}
	var res phpindex.HierarchyResult
	if err := c.call("nav.typeHierarchy", p, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		fmt.Fprintf(w, "%s %s\t%s\n", res.Class.Kind, res.Class.FQN, locString(res.Class))
		fmt.Fprintln(w, "supertypes:")
		printNodes(w, res.Supertypes, 1)
		fmt.Fprintln(w, "subtypes:")
		printNodes(w, res.Subtypes, 1)
		if res.Truncated {
			fmt.Fprintln(w, "  … (truncated)")
		}
	})
}

func printNodes(w io.Writer, nodes []phpindex.HierarchyNode, depth int) {
	for _, n := range nodes {
		kind := n.Kind
		if kind == "" {
			kind = "?"
		}
		fmt.Fprintf(w, "%s%s %s\t%s\n", strings.Repeat("  ", depth), kind, n.FQN, locString(n.Location))
		printNodes(w, n.Children, depth+1)
	}
}

func runGutter(c *cli) error {
	var res phpindex.GutterResult
	if err := c.call("nav.gutter", phpindex.GutterParams{Path: c.p.args[0], Wait: true}, &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		for _, g := range res.Marks {
			fmt.Fprintf(w, "%d\t%s %s\tup %d\tdown %d\n", g.Line, g.Kind, g.FQN, g.UpTotal, g.DownTotal)
		}
	})
}

func runIndexStatus(c *cli) error {
	var st phpindex.Status
	method := "index.status"
	if c.p.has("rebuild") {
		method = "index.rebuild"
	}
	if err := c.call(method, phpindex.StatusParams{Wait: true, Full: c.p.has("full")}, &st); err != nil {
		return err
	}
	return c.emit(st, func(w io.Writer) {
		fmt.Fprintf(w, "state:      %s\n", st.State)
		fmt.Fprintf(w, "root:       %s\n", st.Root)
		fmt.Fprintf(w, "files:      %d (library %d, vendor indexed: %s)\n", st.Files, st.Stats.Libraries, yesNo(st.Libraries))
		fmt.Fprintf(w, "classes:    %d\n", st.Classes)
		fmt.Fprintf(w, "members:    %d\n", st.Members)
		fmt.Fprintf(w, "functions:  %d\n", st.Functions)
		fmt.Fprintf(w, "constants:  %d\n", st.Constants)
		fmt.Fprintf(w, "from cache: %s\n", yesNo(st.FromCache))
		if st.LastSync != "" {
			fmt.Fprintf(w, "last scan:  %s (%d ms)\n", st.LastSync, st.Duration)
		}
		if st.Error != "" {
			fmt.Fprintf(w, "error:      %s\n", st.Error)
		}
	})
}
