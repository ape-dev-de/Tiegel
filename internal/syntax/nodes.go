//go:build cgo

package syntax

import ts "github.com/tree-sitter/go-tree-sitter"

// NodesAt returns the syntax nodes containing the byte before offset off
// (the character just typed before a caret at off), innermost first, up
// to the root. It returns nil for languages without a grammar.
func (d *Document) NodesAt(off int) []Node {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.tree == nil || d.g == nil {
		return nil
	}
	return nodesAt(d.tree, d.g, len(d.text()), off)
}

func nodesAt(tree *ts.Tree, g *grammar, size, off int) []Node {
	off = min(max(off, 0), size)
	if off > 0 {
		off--
	}
	root := tree.RootNode()
	n := root.DescendantForByteRange(uint(off), uint(off))
	var out []Node
	for ; n != nil; n = n.Parent() {
		out = append(out, Node{Kind: g.kindName(n), Start: int(n.StartByte()), End: int(n.EndByte()), Named: n.IsNamed()})
	}
	return out
}
