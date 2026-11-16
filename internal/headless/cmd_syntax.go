package headless

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/modules/syntaxmod"
)

func init() {
	flagSpec["lines"] = true
	flagSpec["language"] = true
	register(
		&command{name: "highlights", usage: "highlights FILE [--lines FROM:TO] [--language L]", summary: "syntax highlighting spans: start end role text", minArgs: 1, maxArgs: 1, cwdProject: true, run: runHighlights},
		&command{name: "folds", usage: "folds FILE [--language L]", summary: "folding regions as line ranges", minArgs: 1, maxArgs: 1, cwdProject: true, run: runFolds},
		&command{name: "outline", usage: "outline FILE [--language L]", summary: "structure outline (namespaces, classes, members, functions)", minArgs: 1, maxArgs: 1, cwdProject: true, run: runOutline},
	)
}

func (c *cli) syntaxSource() syntaxmod.Source {
	return syntaxmod.Source{Path: c.p.args[0], Language: c.p.value("language")}
}

func runHighlights(c *cli) error {
	p := syntaxmod.HighlightsParams{Source: c.syntaxSource()}
	if l := c.p.value("lines"); l != "" {
		from, to, _ := strings.Cut(l, ":")
		var err1, err2 error
		p.StartLine, err1 = strconv.Atoi(from)
		if to != "" {
			p.EndLine, err2 = strconv.Atoi(to)
		}
		if err1 != nil || err2 != nil || p.StartLine < 1 {
			return usagef("--lines needs FROM:TO (1-based), got %q", l)
		}
	}
	var res syntaxmod.HighlightsResult
	if err := c.call("syntax.highlights", p, &res); err != nil {
		return err
	}
	var text []byte
	if !c.json {
		var r struct {
			Content string `json:"content"`
		}
		// A CLI run has no open documents: the file is what was highlighted.
		if err := c.call("files.read", map[string]string{"path": p.Path}, &r); err != nil {
			return err
		}
		text = []byte(r.Content)
	}
	return c.emit(res, func(w io.Writer) {
		for _, s := range res.Spans {
			snippet := ""
			if s.End <= len(text) {
				snippet = string(text[s.Start:s.End])
			}
			fmt.Fprintf(w, "%d\t%d\t%s\t%s\n", s.Start, s.End, s.Role, strconv.Quote(snippet))
		}
	})
}

func runFolds(c *cli) error {
	var res syntaxmod.FoldsResult
	if err := c.call("syntax.folds", c.syntaxSource(), &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) {
		for _, f := range res.Folds {
			fmt.Fprintf(w, "%d-%d\n", f.StartLine, f.EndLine)
		}
	})
}

func runOutline(c *cli) error {
	var res syntaxmod.OutlineResult
	if err := c.call("syntax.outline", c.syntaxSource(), &res); err != nil {
		return err
	}
	return c.emit(res, func(w io.Writer) { printSymbols(w, res.Symbols, 0) })
}

func printSymbols(w io.Writer, syms []syntaxmod.Symbol, depth int) {
	for _, s := range syms {
		mods := ""
		if len(s.Modifiers) > 0 {
			mods = strings.Join(s.Modifiers, " ") + " "
		}
		fmt.Fprintf(w, "%s%d:%d %s%s %s%s\n", strings.Repeat("  ", depth), s.NameStart.Line, s.NameStart.Col, mods, s.Kind, s.Name, s.Detail)
		printSymbols(w, s.Children, depth+1)
	}
}
