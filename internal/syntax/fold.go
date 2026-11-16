package syntax

import (
	"bytes"
	"sort"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// foldKinds lists, per language, the node kinds that form folding regions.
var foldKinds = map[Language]map[string]bool{
	LangPHP: {
		"declaration_list":          true, // class/interface/trait bodies
		"enum_declaration_list":     true,
		"compound_statement":        true, // function/method bodies, blocks
		"array_creation_expression": true,
		"namespace_use_group":       true,
		"match_block":               true,
		"switch_block":              true,
		"property_hook_list":        true,
		"formal_parameters":         true,
		"heredoc":                   true,
		"nowdoc":                    true,
		"comment":                   true, // only block comments, see below
	},
	LangJSON: {"object": true, "array": true},
	LangYAML: {"block_mapping_pair": true, "block_sequence_item": true, "flow_mapping": true, "flow_sequence": true},
	LangXML:  {"element": true, "Comment": true, "CDSect": true},
}

// Folds returns the folding regions of the current tree, sorted by start
// line. Only multi-line regions are reported; for regions starting on the
// same line the outermost wins. For PHP, runs of consecutive "use"
// statements also fold.
func (d *Document) Folds() []Range {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.tree == nil {
		return nil
	}
	return folds(d.tree, d.text(), d.lang)
}

func folds(tree *ts.Tree, src []byte, lang Language) []Range {
	kinds, g := foldKinds[lang], grammars[lang]
	if tree == nil || kinds == nil || g == nil {
		return nil
	}
	byStart := map[int]Range{}
	add := func(r Range) {
		if r.EndLine <= r.StartLine {
			return
		}
		if old, ok := byStart[r.StartLine]; ok && old.EndLine >= r.EndLine {
			return
		}
		byStart[r.StartLine] = r
	}

	var useRun Range
	useLen := 0
	flushUse := func() {
		if useLen >= 2 {
			add(useRun)
		}
		useLen = 0
	}

	c := tree.Walk()
	defer c.Close()
	for {
		n := c.Node()
		kind := g.kindName(n)
		if kinds[kind] && (kind != "comment" || bytes.HasPrefix(src[n.StartByte():min(n.EndByte(), uint(len(src)))], []byte("/*"))) {
			add(nodeLines(n))
		}
		if lang == LangPHP && kind == "namespace_use_declaration" {
			r := nodeLines(n)
			if useLen > 0 && r.StartLine <= useRun.EndLine+1 {
				useRun.EndLine = r.EndLine
			} else {
				flushUse()
				useRun = r
			}
			useLen++
		}
		// Single-line nodes cannot contain multi-line regions; skipping
		// them prunes most of the tree.
		if n.StartPosition().Row != n.EndPosition().Row && c.GotoFirstChild() {
			continue
		}
		for !c.GotoNextSibling() {
			if !c.GotoParent() {
				flushUse()
				out := make([]Range, 0, len(byStart))
				for _, r := range byStart {
					out = append(out, r)
				}
				sort.Slice(out, func(i, j int) bool { return out[i].StartLine < out[j].StartLine })
				return out
			}
		}
	}
}

// nodeLines returns the inclusive line range of n. A node ending at column 0
// (after a trailing newline) ends on the previous line.
func nodeLines(n *ts.Node) Range {
	s, e := n.StartPosition(), n.EndPosition()
	end := int(e.Row)
	if e.Column == 0 && e.Row > s.Row {
		end--
	}
	return Range{StartLine: int(s.Row), EndLine: end}
}
