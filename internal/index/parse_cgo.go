//go:build cgo

package index

import (
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	ts "github.com/tree-sitter/go-tree-sitter"
	tsphp "github.com/tree-sitter/tree-sitter-php/bindings/go"
)

// TreeSitter reports whether this build can parse PHP (cgo). Without it
// the index can hold and query data (e.g. from the cache) but not parse
// files.
const TreeSitter = true

// grammar holds the PHP language and cached node kind names and field ids
// (avoids a cgo string conversion per node).
var grammar struct {
	once   sync.Once
	lang   *ts.Language
	kinds  []string
	fields map[string]uint16
}

func lang() *ts.Language {
	grammar.once.Do(func() {
		grammar.lang = ts.NewLanguage(tsphp.LanguagePHP())
		cnt := grammar.lang.NodeKindCount()
		grammar.kinds = make([]string, cnt)
		for i := uint32(0); i < cnt; i++ {
			grammar.kinds[i] = grammar.lang.NodeKindForId(uint16(i))
		}
		grammar.fields = map[string]uint16{}
		for _, f := range []string{"name", "body", "parameters", "return_type", "type", "object", "scope", "function", "alias", "left", "right", "value", "prefix"} {
			grammar.fields[f] = grammar.lang.FieldIdForName(f)
		}
	})
	return grammar.lang
}

func kind(n *ts.Node) string {
	if id := int(n.KindId()); id < len(grammar.kinds) {
		return grammar.kinds[id]
	}
	return n.Kind()
}

func field(n *ts.Node, name string) *ts.Node {
	if n == nil {
		return nil
	}
	return n.ChildByFieldId(grammar.fields[name])
}

// parser wraps a Tree-sitter parser; it is not safe for concurrent use.
type parser struct {
	p *ts.Parser
}

func newParser() (*parser, error) {
	p := ts.NewParser()
	if err := p.SetLanguage(lang()); err != nil {
		p.Close()
		return nil, fmt.Errorf("index: set language: %w", err)
	}
	return &parser{p: p}, nil
}

func (p *parser) close() { p.p.Close() }

// parse parses src and extracts the declarations of the file.
func (p *parser) parse(path string, src []byte) *File {
	tree := p.p.Parse(src, nil)
	if tree == nil {
		return &File{Path: path, Scopes: []Scope{{End: len(src)}}}
	}
	defer tree.Close()
	return extract(tree, path, src)
}

// locate parses src and returns its declarations and the reference at byte
// offset off.
func (p *parser) locate(path string, src []byte, off int) (*File, Ref, bool) {
	tree := p.p.Parse(src, nil)
	if tree == nil {
		return &File{Path: path}, Ref{}, false
	}
	defer tree.Close()
	f := extract(tree, path, src)
	ref, ok := locateRef(tree, src, off, f)
	return f, ref, ok
}

type extractor struct {
	src  []byte
	f    *File
	cur  *ts.TreeCursor
	decl []pendingDecl
}

type pendingDecl struct {
	node  ts.Node
	scope int
}

func extract(tree *ts.Tree, path string, src []byte) *File {
	root := tree.RootNode()
	cur := root.Walk()
	defer cur.Close()
	e := &extractor{src: src, f: &File{Path: path}, cur: cur}
	e.f.Scopes = []Scope{{Start: 0, End: len(src)}}
	e.statements(root, 0)
	for _, d := range e.decl {
		e.declaration(&d.node, d.scope)
	}
	return e.f
}

func (e *extractor) text(n *ts.Node) string {
	if n == nil {
		return ""
	}
	return string(e.src[n.StartByte():n.EndByte()])
}

func (e *extractor) named(n *ts.Node) []ts.Node { return n.NamedChildren(e.cur) }

func (e *extractor) pos(off uint, pt ts.Point) Pos { return makePos(e.src, off, pt) }

func makePos(src []byte, off uint, pt ts.Point) Pos {
	o := int(off)
	ls := max(o-int(pt.Column), 0)
	return Pos{Offset: o, Line: int(pt.Row) + 1, Col: utf8.RuneCount(src[ls:o]) + 1}
}

func (e *extractor) rng(n *ts.Node) Range {
	if n == nil {
		return Range{}
	}
	return Range{Start: e.pos(n.StartByte(), n.StartPosition()), End: e.pos(n.EndByte(), n.EndPosition())}
}

// statements walks the statements below n in scope sc, opening scopes for
// namespace definitions and collecting use statements and declarations.
func (e *extractor) statements(n *ts.Node, sc int) {
	for _, c := range e.named(n) {
		switch kind(&c) {
		case "namespace_definition":
			name := strings.TrimPrefix(e.text(field(&c, "name")), `\`)
			body := field(&c, "body")
			if body != nil {
				e.f.Scopes = append(e.f.Scopes, Scope{Namespace: name, Start: int(c.StartByte()), End: int(c.EndByte())})
				e.statements(body, len(e.f.Scopes)-1)
				continue
			}
			// statement form: the scope runs to the next namespace
			if last := &e.f.Scopes[len(e.f.Scopes)-1]; last.End > int(c.StartByte()) {
				last.End = int(c.StartByte())
			}
			e.f.Scopes = append(e.f.Scopes, Scope{Namespace: name, Start: int(c.StartByte()), End: len(e.src)})
			sc = len(e.f.Scopes) - 1
		case "namespace_use_declaration":
			e.uses(&c, sc)
		case "class_declaration", "interface_declaration", "trait_declaration", "enum_declaration", "function_definition", "const_declaration":
			e.decl = append(e.decl, pendingDecl{node: c, scope: sc})
		case "compound_statement", "if_statement", "else_clause", "else_if_clause", "colon_block", "declare_statement":
			e.statements(&c, sc)
		}
	}
}

// useKindOf returns the import kind given by a "function"/"const" keyword
// child of n.
func (e *extractor) useKindOf(n *ts.Node, def UseKind) UseKind {
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.IsNamed() {
			continue
		}
		switch kind(c) {
		case "function":
			return UseFunction
		case "const":
			return UseConst
		}
	}
	return def
}

func (e *extractor) uses(n *ts.Node, sc int) {
	k := e.useKindOf(n, UseClass)
	prefix := ""
	var clauses []ts.Node
	for _, c := range e.named(n) {
		switch kind(&c) {
		case "namespace_name", "qualified_name", "name":
			prefix = strings.Trim(e.text(&c), `\`)
		case "namespace_use_clause":
			clauses = append(clauses, c)
		case "namespace_use_group":
			clauses = append(clauses, e.named(&c)...)
		}
	}
	// "use function A, B;": the grammar puts the keyword into the first clause
	if prefix == "" && len(clauses) > 0 {
		k = e.useKindOf(&clauses[0], k)
	}
	for _, cl := range clauses {
		if kind(&cl) != "namespace_use_clause" {
			continue
		}
		ck := e.useKindOf(&cl, k)
		alias := e.text(field(&cl, "alias"))
		var name string
		for _, c := range e.named(&cl) {
			if kk := kind(&c); (kk == "name" || kk == "qualified_name") && (alias == "" || c.StartByte() < field(&cl, "alias").StartByte()) {
				name = strings.Trim(e.text(&c), `\`)
				break
			}
		}
		if name == "" {
			continue
		}
		if prefix != "" {
			name = prefix + `\` + name
		}
		if alias == "" {
			alias = lastSegment(name)
		}
		e.f.Scopes[sc].Uses = append(e.f.Scopes[sc].Uses, Use{Kind: ck, Alias: alias, FQN: name})
	}
}

func (e *extractor) declaration(n *ts.Node, sc int) {
	scope := e.f.Scopes[sc]
	switch kind(n) {
	case "class_declaration":
		e.f.Classes = append(e.f.Classes, e.classLike(KindClass, n, scope))
	case "interface_declaration":
		e.f.Classes = append(e.f.Classes, e.classLike(KindInterface, n, scope))
	case "trait_declaration":
		e.f.Classes = append(e.f.Classes, e.classLike(KindTrait, n, scope))
	case "enum_declaration":
		e.f.Classes = append(e.f.Classes, e.classLike(KindEnum, n, scope))
	case "function_definition":
		name := field(n, "name")
		e.f.Funcs = append(e.f.Funcs, Function{
			FQN: qualify(scope.Namespace, e.text(name)), Name: e.text(name), Detail: e.signature(n),
			Range: e.rng(n), NameRange: e.rng(name),
		})
	case "const_declaration":
		for _, el := range e.named(n) {
			if kind(&el) != "const_element" {
				continue
			}
			name := e.firstNamed(&el, "name")
			if name == nil {
				continue
			}
			e.f.Consts = append(e.f.Consts, Constant{
				FQN: qualify(scope.Namespace, e.text(name)), Name: e.text(name),
				Range: e.rng(n), NameRange: e.rng(name),
			})
		}
	}
}

func (e *extractor) firstNamed(n *ts.Node, k string) *ts.Node {
	for _, c := range e.named(n) {
		if kind(&c) == k {
			return &c
		}
	}
	return nil
}

// typeNames returns the resolved class names listed below n (base and
// interface clauses, trait uses).
func (e *extractor) typeNames(n *ts.Node, scope Scope) []string {
	var out []string
	for _, c := range e.named(n) {
		switch kind(&c) {
		case "name", "qualified_name", "relative_name":
			if fqn := ResolveClass(e.text(&c), scope); fqn != "" {
				out = append(out, fqn)
			}
		}
	}
	return out
}

func (e *extractor) classLike(k Kind, n *ts.Node, scope Scope) Class {
	name := field(n, "name")
	c := Class{
		Kind: k, Name: e.text(name), FQN: qualify(scope.Namespace, e.text(name)),
		Range: e.rng(n), NameRange: e.rng(name),
	}
	for _, ch := range e.named(n) {
		switch kind(&ch) {
		case "abstract_modifier":
			c.Abstract = true
		case "final_modifier":
			c.Final = true
		case "base_clause":
			c.Extends = append(c.Extends, e.typeNames(&ch, scope)...)
		case "class_interface_clause":
			c.Implements = append(c.Implements, e.typeNames(&ch, scope)...)
		case "primitive_type":
			if k == KindEnum {
				c.Detail = e.text(&ch)
			}
		}
	}
	if k == KindInterface {
		c.Abstract = true
	}
	body := field(n, "body")
	if body == nil {
		return c
	}
	for _, m := range e.named(body) {
		switch kind(&m) {
		case "method_declaration":
			mem := e.member(KindMethod, &m, field(&m, "name"))
			mem.Detail = e.signature(&m)
			if k == KindInterface {
				mem.Abstract = true
			}
			c.Members = append(c.Members, mem)
			c.Members = append(c.Members, e.promoted(&m)...)
		case "property_declaration":
			typ := e.text(field(&m, "type"))
			for _, el := range e.named(&m) {
				if kind(&el) != "property_element" {
					continue
				}
				mem := e.member(KindProperty, &m, field(&el, "name"))
				mem.Name = strings.TrimPrefix(mem.Name, "$")
				mem.Detail = typ
				c.Members = append(c.Members, mem)
			}
		case "const_declaration":
			typ := e.text(field(&m, "type"))
			for _, el := range e.named(&m) {
				if kind(&el) != "const_element" {
					continue
				}
				mem := e.member(KindClassConst, &m, e.firstNamed(&el, "name"))
				mem.Detail = typ
				c.Members = append(c.Members, mem)
			}
		case "enum_case":
			c.Members = append(c.Members, e.member(KindEnumCase, &m, field(&m, "name")))
		case "use_declaration":
			c.Traits = append(c.Traits, e.typeNames(&m, scope)...)
		}
	}
	return c
}

// member builds a member from declaration decl (modifiers) and its name.
func (e *extractor) member(k Kind, decl, name *ts.Node) Member {
	m := Member{Kind: k, Name: e.text(name), Range: e.rng(decl), NameRange: e.rng(name)}
	if name == nil {
		m.NameRange = Range{Start: m.Range.Start, End: m.Range.Start}
	}
	for _, c := range e.named(decl) {
		switch kind(&c) {
		case "visibility_modifier":
			v := strings.ToLower(e.text(&c))
			if i := strings.IndexByte(v, '('); i >= 0 { // asymmetric: "private(set)"
				continue
			}
			m.Visibility = v
		case "static_modifier":
			m.Static = true
		case "abstract_modifier":
			m.Abstract = true
		case "final_modifier":
			m.Final = true
		}
	}
	return m
}

// promoted returns constructor-promoted properties of method n.
func (e *extractor) promoted(n *ts.Node) []Member {
	params := field(n, "parameters")
	if params == nil {
		return nil
	}
	var out []Member
	for _, p := range e.named(params) {
		if kind(&p) != "property_promotion_parameter" {
			continue
		}
		m := e.member(KindProperty, &p, field(&p, "name"))
		m.Name = strings.TrimPrefix(m.Name, "$")
		m.Detail = e.text(field(&p, "type"))
		out = append(out, m)
	}
	return out
}

func (e *extractor) signature(n *ts.Node) string {
	s := compactSpace(e.text(field(n, "parameters")))
	if rt := field(n, "return_type"); rt != nil {
		s += ": " + e.text(rt)
	}
	return s
}

var parenSpace = strings.NewReplacer("( ", "(", " )", ")", ",)", ")")

func compactSpace(s string) string {
	return parenSpace.Replace(strings.Join(strings.Fields(s), " "))
}

// ---- locating the reference under the cursor ----

// nameKinds are the node kinds that spell a (possibly qualified) name.
var nameKinds = map[string]bool{"name": true, "qualified_name": true, "relative_name": true, "namespace_name": true}

// locateRef classifies the node at byte offset off.
func locateRef(tree *ts.Tree, src []byte, off int, f *File) (Ref, bool) {
	root := tree.RootNode()
	o := uint(max(0, min(off, len(src))))
	n := root.NamedDescendantForByteRange(o, o)
	// The cursor right behind a name ("Foo|") still means the name.
	if (n == nil || !nameKinds[kind(n)] && kind(n) != "relative_scope" && kind(n) != "variable_name") && o > 0 {
		if m := root.NamedDescendantForByteRange(o-1, o-1); m != nil && (nameKinds[kind(m)] || kind(m) == "relative_scope") {
			n = m
		}
	}
	if n == nil {
		return Ref{}, false
	}
	l := locator{src: src, f: f, off: off}
	return l.classify(n)
}

type locator struct {
	src []byte
	f   *File
	off int
}

func (l *locator) text(n *ts.Node) string { return string(l.src[n.StartByte():n.EndByte()]) }

// enclosingClass returns the FQN of the class-like declaration around n.
func (l *locator) enclosingClass(n *ts.Node) string {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch kind(p) {
		case "class_declaration", "interface_declaration", "trait_declaration", "enum_declaration":
			name := field(p, "name")
			if name == nil {
				return ""
			}
			return qualify(l.f.ScopeAt(int(p.StartByte())).Namespace, l.text(name))
		case "anonymous_class", "object_creation_expression":
			if kind(p) == "anonymous_class" {
				return ""
			}
		}
	}
	return ""
}

func (l *locator) classify(n *ts.Node) (Ref, bool) {
	ref := Ref{Offset: l.off, Class: l.enclosingClass(n)}
	k := kind(n)
	if k == "relative_scope" { // self / static / parent
		ref.Kind, ref.Name = RefClass, strings.ToLower(l.text(n))
		return ref, true
	}
	if k == "variable_name" {
		if p := n.Parent(); p != nil && kind(p) == "property_element" {
			return l.memberDecl(ref, KindProperty, strings.TrimPrefix(l.text(n), "$"))
		}
		if p := n.Parent(); p != nil && kind(p) == "property_promotion_parameter" {
			return l.memberDecl(ref, KindProperty, strings.TrimPrefix(l.text(n), "$"))
		}
		if p := n.Parent(); p != nil && kind(p) == "scoped_property_access_expression" {
			return l.scopedMember(ref, p, KindProperty, strings.TrimPrefix(l.text(n), "$"))
		}
		return Ref{}, false
	}
	if !nameKinds[k] {
		return Ref{}, false
	}
	// Climb to the full name: a name inside a qualified name (or its
	// namespace prefix) means the whole qualified name.
	for p := n.Parent(); p != nil && nameKinds[kind(p)]; p = n.Parent() {
		n = p
	}
	p := n.Parent()
	if p == nil {
		return Ref{}, false
	}
	name := l.text(n)
	pk := kind(p)
	isField := func(f string) bool {
		c := field(p, f)
		return c != nil && c.Id() == n.Id()
	}
	switch pk {
	case "class_declaration", "interface_declaration", "trait_declaration", "enum_declaration":
		if isField("name") {
			ref.Kind, ref.Name, ref.Decl = RefClass, `\`+qualify(l.f.ScopeAt(int(p.StartByte())).Namespace, name), true
			return ref, true
		}
	case "function_definition":
		if isField("name") {
			ref.Kind, ref.Name, ref.Decl = RefFunction, `\`+qualify(l.f.ScopeAt(int(p.StartByte())).Namespace, name), true
			return ref, true
		}
	case "method_declaration":
		if isField("name") {
			return l.memberDecl(ref, KindMethod, name)
		}
	case "enum_case":
		return l.memberDecl(ref, KindEnumCase, name)
	case "const_element":
		if first := p.NamedChild(0); first == nil || first.Id() != n.Id() {
			ref.Kind, ref.Name = RefConst, name
			return ref, true
		}
		if gp := p.Parent(); gp != nil && kind(gp) == "const_declaration" {
			if ggp := gp.Parent(); ggp != nil && (kind(ggp) == "declaration_list" || kind(ggp) == "enum_declaration_list") {
				return l.memberDecl(ref, KindClassConst, name)
			}
			ref.Kind, ref.Name, ref.Decl = RefConst, `\`+qualify(l.f.ScopeAt(int(gp.StartByte())).Namespace, name), true
			return ref, true
		}
	case "namespace_use_clause":
		return l.useClause(ref, n, p)
	case "namespace_use_declaration", "namespace_definition":
		return Ref{}, false
	case "member_call_expression", "nullsafe_member_call_expression":
		if isField("name") {
			return l.objectMember(ref, p, KindMethod, name)
		}
	case "member_access_expression", "nullsafe_member_access_expression":
		if isField("name") {
			return l.objectMember(ref, p, KindProperty, name)
		}
	case "scoped_call_expression":
		if isField("name") {
			return l.scopedMember(ref, p, KindMethod, name)
		}
	case "class_constant_access_expression":
		if first := p.NamedChild(0); first != nil && first.Id() != n.Id() {
			if strings.EqualFold(name, "class") {
				return l.scopeClass(ref, first)
			}
			return l.scopedMember(ref, p, KindClassConst, name)
		}
	case "function_call_expression":
		if isField("function") {
			ref.Kind, ref.Name = RefFunction, name
			return ref, true
		}
	case "expression_statement", "argument", "binary_expression", "assignment_expression", "return_statement",
		"array_element_initializer", "parenthesized_expression", "conditional_expression", "unary_op_expression",
		"match_condition_list", "case_statement", "property_element", "simple_parameter", "echo_statement":
		if pk == "binary_expression" && isField("right") && l.isInstanceof(p) {
			break
		}
		ref.Kind, ref.Name = RefConst, name
		return ref, true
	}
	ref.Kind, ref.Name = RefClass, name
	return ref, true
}

func (l *locator) isInstanceof(bin *ts.Node) bool {
	for i := uint(0); i < bin.ChildCount(); i++ {
		if c := bin.Child(i); !c.IsNamed() && kind(c) == "instanceof" {
			return true
		}
	}
	return false
}

func (l *locator) memberDecl(ref Ref, k Kind, name string) (Ref, bool) {
	if ref.Class == "" {
		return Ref{}, false
	}
	ref.Kind, ref.MemberKind, ref.Name, ref.Receiver, ref.Decl = RefMember, k, name, "self", true
	return ref, true
}

// objectMember handles $obj->name: only $this has a known type.
func (l *locator) objectMember(ref Ref, expr *ts.Node, k Kind, name string) (Ref, bool) {
	obj := field(expr, "object")
	if obj == nil || kind(obj) != "variable_name" || l.text(obj) != "$this" {
		ref.Kind, ref.MemberKind, ref.Name, ref.Receiver = RefMember, k, name, ""
		return ref, true
	}
	ref.Kind, ref.MemberKind, ref.Name, ref.Receiver = RefMember, k, name, "this"
	return ref, true
}

// scopedMember handles X::name, self::name, static::name, parent::name.
func (l *locator) scopedMember(ref Ref, expr *ts.Node, k Kind, name string) (Ref, bool) {
	scope := field(expr, "scope")
	if scope == nil {
		scope = expr.NamedChild(0)
	}
	if scope == nil {
		return Ref{}, false
	}
	ref.Kind, ref.MemberKind, ref.Name = RefMember, k, name
	switch kind(scope) {
	case "relative_scope":
		ref.Receiver = strings.ToLower(l.text(scope))
	case "name", "qualified_name", "relative_name":
		ref.Receiver = l.text(scope)
		switch strings.ToLower(ref.Receiver) {
		case "self", "static", "parent":
			ref.Receiver = strings.ToLower(ref.Receiver)
		}
	default:
		ref.Receiver = ""
	}
	return ref, true
}

func (l *locator) scopeClass(ref Ref, scope *ts.Node) (Ref, bool) {
	ref.Kind, ref.Name = RefClass, l.text(scope)
	if kind(scope) == "relative_scope" {
		ref.Name = strings.ToLower(ref.Name)
	}
	return ref, true
}

func (l *locator) useClause(ref Ref, n, clause *ts.Node) (Ref, bool) {
	sc := l.f.ScopeAt(int(clause.StartByte()))
	for _, u := range sc.Uses {
		// match the clause by its alias or its last segment
		alias := field(clause, "alias")
		want := lastSegment(strings.Trim(l.text(n), `\`))
		if alias != nil {
			want = l.text(alias)
		}
		if u.Alias != want {
			continue
		}
		switch u.Kind {
		case UseFunction:
			ref.Kind = RefFunction
		case UseConst:
			ref.Kind = RefConst
		default:
			ref.Kind = RefClass
		}
		ref.Name = `\` + u.FQN
		return ref, true
	}
	return Ref{}, false
}
