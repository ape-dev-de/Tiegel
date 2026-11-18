package controller

import (
	"context"
	"sort"
	"strings"
)

// Extract Variable / Extract Method (AP17.3) show what the language
// server offers: its code actions of kind refactor.extract whose title
// names the wanted target. Tiegel has no refactoring engine of its own.

// Extract targets.
const (
	ExtractVariable = "variable"
	ExtractMethod   = "method"
)

// extractWords are the title words of the targets (servers name them
// differently: "Extract variable", "Extract expression", "Extract
// method", "Extract function").
var extractWords = map[string][]string{
	ExtractVariable: {"variable", "expression", "constant"},
	ExtractMethod:   {"method", "function"},
}

// filterExtract keeps the actions whose title names target; if none does,
// all extract actions stay (the user picks).
func filterExtract(as []CodeAction, target string) []CodeAction {
	var out []CodeAction
	for _, a := range as {
		t := strings.ToLower(a.Title)
		for _, w := range extractWords[target] {
			if strings.Contains(t, w) {
				out = append(out, a)
				break
			}
		}
	}
	if len(out) == 0 {
		return as
	}
	return out
}

// LoadExtract asks the language server for its extract refactorings of
// [start, end) in file and offers those for target in the context
// actions list.
func (n *CodeNav) LoadExtract(file string, start, end int, target string) {
	c := n.c
	label := "Extract " + target
	if !c.Status.LSPRunning {
		c.Status.Notice = label + " " + errNoServer
		return
	}
	c.Go(func(ctx context.Context) func() {
		var as []CodeAction
		p := map[string]any{"path": file, "start": pos{Offset: start}, "only": []string{"refactor.extract"}}
		if end > start {
			p["end"] = pos{Offset: end}
		}
		err := c.call(ctx, "lsp.codeActions", p, &as)
		return func() {
			if err != nil {
				c.notice(err)
				return
			}
			as = filterExtract(as, target)
			if len(as) == 0 {
				c.Status.Notice = "The language server offers no " + strings.ToLower(label) + " here"
				return
			}
			sort.SliceStable(as, func(i, j int) bool { return as[i].Preferred && !as[j].Preferred })
			n.Actions = &CodeActions{File: file, Items: as}
			n.ActionsVersion++
		}
	})
}
