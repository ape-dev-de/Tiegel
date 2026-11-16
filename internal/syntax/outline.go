//go:build cgo

package syntax

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// Outline returns the PHP structure (namespaces, classes, interfaces,
// traits, enums and their members, functions). It returns nil for other
// languages.
func (d *Document) Outline() []Symbol {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.tree == nil {
		return nil
	}
	return outline(d.tree, d.text(), d.lang)
}

func outline(tree *ts.Tree, src []byte, lang Language) []Symbol {
	if tree == nil || lang != LangPHP {
		return nil
	}
	root := tree.RootNode()
	cur := root.Walk()
	defer cur.Close()
	o := outliner{src: src, g: grammars[lang], cur: cur}
	return o.statements(root)
}

type outliner struct {
	src []byte
	g   *grammar
	cur *ts.TreeCursor
}

func (o *outliner) text(n *ts.Node) string {
	if n == nil {
		return ""
	}
	return string(o.src[n.StartByte():n.EndByte()])
}

func (o *outliner) kind(n *ts.Node) string { return o.g.kindName(n) }

// named returns the named children of n (linear, unlike NamedChild(i)).
func (o *outliner) named(n *ts.Node) []ts.Node { return n.NamedChildren(o.cur) }

// field is ChildByFieldName with a cached field id (no C string per call).
func (o *outliner) field(n *ts.Node, name string) *ts.Node {
	return n.ChildByFieldId(o.g.fieldID(name))
}

func (o *outliner) symbol(kind SymbolKind, decl *ts.Node, name *ts.Node) Symbol {
	s := Symbol{
		Kind:      kind,
		Name:      o.text(name),
		StartByte: int(decl.StartByte()),
		EndByte:   int(decl.EndByte()),
		Start:     fromTSPoint(decl.StartPosition()),
		End:       fromTSPoint(decl.EndPosition()),
	}
	if name != nil {
		s.NameStart = fromTSPoint(name.StartPosition())
	} else {
		s.NameStart = s.Start
	}
	return s
}

// statements collects top-level symbols below n (program, namespace bodies,
// conditional blocks). Statement-style namespaces ("namespace Foo;") adopt
// the following siblings.
func (o *outliner) statements(n *ts.Node) []Symbol {
	var out []Symbol
	var ns *Symbol // open statement-style namespace
	emit := func(s ...Symbol) {
		if ns != nil {
			ns.Children = append(ns.Children, s...)
			ns.EndByte = max(ns.EndByte, s[len(s)-1].EndByte)
			ns.End = s[len(s)-1].End
			return
		}
		out = append(out, s...)
	}
	closeNS := func() {
		if ns != nil {
			out = append(out, *ns)
			ns = nil
		}
	}
	for _, c := range o.named(n) {
		if o.kind(&c) == "namespace_definition" {
			closeNS()
			s := o.symbol(SymbolNamespace, &c, o.field(&c, "name"))
			if body := o.field(&c, "body"); body != nil {
				s.Children = o.statements(body)
				out = append(out, s)
			} else {
				ns = &s
			}
			continue
		}
		if syms := o.declaration(&c); len(syms) > 0 {
			emit(syms...)
		}
	}
	closeNS()
	return out
}

// declaration returns the symbols declared by statement n.
func (o *outliner) declaration(n *ts.Node) []Symbol {
	switch o.kind(n) {
	case "class_declaration":
		return []Symbol{o.classLike(SymbolClass, n)}
	case "interface_declaration":
		return []Symbol{o.classLike(SymbolInterface, n)}
	case "trait_declaration":
		return []Symbol{o.classLike(SymbolTrait, n)}
	case "enum_declaration":
		s := o.classLike(SymbolEnum, n)
		for _, c := range o.named(n) {
			if o.kind(&c) == "primitive_type" {
				s.Detail = o.text(&c)
			}
		}
		return []Symbol{s}
	case "function_definition":
		return []Symbol{o.function(SymbolFunction, n)}
	case "const_declaration":
		return o.constants(n)
	case "compound_statement", "if_statement", "else_clause", "else_if_clause", "colon_block", "declare_statement":
		return o.statements(n)
	}
	return nil
}

func (o *outliner) classLike(kind SymbolKind, n *ts.Node) Symbol {
	s := o.symbol(kind, n, o.field(n, "name"))
	s.Modifiers = o.modifiers(n)
	body := o.field(n, "body")
	if body == nil {
		return s
	}
	for _, m := range o.named(body) {
		switch o.kind(&m) {
		case "method_declaration":
			s.Children = append(s.Children, o.function(SymbolMethod, &m))
			s.Children = append(s.Children, o.promoted(&m)...)
		case "property_declaration":
			s.Children = append(s.Children, o.properties(&m)...)
		case "const_declaration":
			s.Children = append(s.Children, o.constants(&m)...)
		case "enum_case":
			s.Children = append(s.Children, o.symbol(SymbolEnumCase, &m, o.field(&m, "name")))
		}
	}
	return s
}

func (o *outliner) function(kind SymbolKind, n *ts.Node) Symbol {
	s := o.symbol(kind, n, o.field(n, "name"))
	s.Modifiers = o.modifiers(n)
	s.Detail = compactSpace(o.text(o.field(n, "parameters")))
	if rt := o.field(n, "return_type"); rt != nil {
		s.Detail += ": " + o.text(rt)
	}
	return s
}

// promoted returns constructor-promoted properties of method n.
func (o *outliner) promoted(n *ts.Node) []Symbol {
	params := o.field(n, "parameters")
	if params == nil {
		return nil
	}
	var out []Symbol
	for _, p := range o.named(params) {
		if o.kind(&p) != "property_promotion_parameter" {
			continue
		}
		s := o.symbol(SymbolProperty, &p, o.field(&p, "name"))
		s.Detail = o.text(o.field(&p, "type"))
		s.Modifiers = o.modifiers(&p)
		out = append(out, s)
	}
	return out
}

func (o *outliner) properties(n *ts.Node) []Symbol {
	mods := o.modifiers(n)
	typ := o.text(o.field(n, "type"))
	var out []Symbol
	for _, e := range o.named(n) {
		if o.kind(&e) != "property_element" {
			continue
		}
		s := o.symbol(SymbolProperty, n, o.field(&e, "name"))
		s.Detail, s.Modifiers = typ, mods
		out = append(out, s)
	}
	return out
}

func (o *outliner) constants(n *ts.Node) []Symbol {
	mods := o.modifiers(n)
	typ := o.text(o.field(n, "type"))
	var out []Symbol
	for _, e := range o.named(n) {
		if o.kind(&e) != "const_element" {
			continue
		}
		var name *ts.Node
		for _, c := range o.named(&e) {
			if o.kind(&c) == "name" {
				name = &c
				break
			}
		}
		s := o.symbol(SymbolConstant, n, name)
		s.Detail, s.Modifiers = typ, mods
		out = append(out, s)
	}
	return out
}

var modifierKinds = map[string]bool{
	"visibility_modifier": true, "static_modifier": true, "abstract_modifier": true,
	"final_modifier": true, "readonly_modifier": true, "var_modifier": true,
}

func (o *outliner) modifiers(n *ts.Node) []string {
	var out []string
	for _, c := range o.named(n) {
		if modifierKinds[o.kind(&c)] {
			out = append(out, strings.ToLower(compactSpace(o.text(&c))))
		}
	}
	return out
}

var parenSpace = strings.NewReplacer("( ", "(", " )", ")", ",)", ")")

// compactSpace collapses runs of whitespace into single spaces.
func compactSpace(s string) string {
	return parenSpace.Replace(strings.Join(strings.Fields(s), " "))
}
