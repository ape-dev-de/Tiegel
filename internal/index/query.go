package index

import (
	"fmt"
	"sort"
	"strings"
)

// Location is an indexed declaration.
type Location struct {
	Path string // absolute; "" for supertypes that are not indexed
	Kind Kind
	// Name is the short name (member name without "$", class short name).
	Name string
	// FQN is the class or function FQN, "Class::member" for members.
	FQN string
	// Container is the declaring class of a member.
	Container string
	Library   bool
	Detail    string
	Range     Range
	NameRange Range
}

func classLoc(e classEntry) Location {
	return Location{Path: e.f.Path, Kind: e.c.Kind, Name: e.c.Name, FQN: e.c.FQN, Library: e.f.Library,
		Detail: e.c.Detail, Range: e.c.Range, NameRange: e.c.NameRange}
}

func memberLoc(e classEntry, m *Member) Location {
	return Location{Path: e.f.Path, Kind: m.Kind, Name: m.Name, FQN: e.c.FQN + "::" + m.Name, Container: e.c.FQN,
		Library: e.f.Library, Detail: m.Detail, Range: m.Range, NameRange: m.NameRange}
}

// errNoParser is returned by position queries in builds without cgo.
func (ix *Index) locate(path string, src []byte, off int) (*File, Ref, error) {
	if !TreeSitter {
		return nil, Ref{}, errNoTreeSitter()
	}
	p, err := ix.getParser()
	if err != nil {
		return nil, Ref{}, err
	}
	defer ix.putParser(p)
	if off < 0 || off > len(src) {
		return nil, Ref{}, fmt.Errorf("index: offset %d out of range", off)
	}
	f, ref, ok := p.locate(path, src, off)
	if !ok {
		return f, Ref{}, nil
	}
	return f, ref, nil
}

// RefAt returns the reference at byte offset off of src (the text of
// path). Kind is 0 when there is no navigable name at the position.
func (ix *Index) RefAt(path string, src []byte, off int) (Ref, error) {
	_, ref, err := ix.locate(path, src, off)
	return ref, err
}

// memberMatches reports whether m is the member name/kind looks for.
// Methods are case-insensitive; KindClassConst also matches enum cases.
func memberMatches(m *Member, name string, k Kind) bool {
	switch k {
	case KindMethod:
		return m.Kind == KindMethod && strings.EqualFold(m.Name, name)
	case KindClassConst, KindEnumCase:
		return (m.Kind == KindClassConst || m.Kind == KindEnumCase) && m.Name == name
	default:
		return m.Kind == k && m.Name == name
	}
}

// lineage returns the classes to search for members of fqn in PHP's
// lookup order: the class, its traits, its parent chain (each with traits),
// then all interfaces. Caller holds the read lock.
func (ix *Index) lineageLocked(fqn string) []classEntry {
	var out, ifaces []classEntry
	seen := map[string]bool{}
	var traits func(c *Class)
	traits = func(c *Class) {
		for _, t := range c.Traits {
			if seen[fold(t)] {
				continue
			}
			seen[fold(t)] = true
			for _, e := range ix.classes[fold(t)] {
				out = append(out, e)
				traits(e.c)
			}
		}
	}
	var addIfaces func(names []string)
	addIfaces = func(names []string) {
		for _, n := range names {
			if seen[fold(n)] {
				continue
			}
			seen[fold(n)] = true
			for _, e := range ix.classes[fold(n)] {
				ifaces = append(ifaces, e)
				addIfaces(e.c.Extends)
			}
		}
	}
	cur := fqn
	for depth := 0; cur != "" && depth < 64; depth++ {
		if seen[fold(cur)] {
			break
		}
		seen[fold(cur)] = true
		entries := ix.classes[fold(cur)]
		if len(entries) == 0 {
			break
		}
		e := entries[0]
		if e.c.Kind == KindInterface {
			ifaces = append(ifaces, e)
			addIfaces(e.c.Extends)
			break
		}
		out = append(out, e)
		traits(e.c)
		addIfaces(e.c.Implements)
		cur = ""
		if len(e.c.Extends) > 0 {
			cur = e.c.Extends[0]
		}
	}
	return append(out, ifaces...)
}

// findMemberLocked returns the nearest declaration of a member in the
// lineage of fqn.
func (ix *Index) findMemberLocked(fqn, name string, k Kind) (classEntry, *Member, bool) {
	for _, e := range ix.lineageLocked(fqn) {
		for i := range e.c.Members {
			if memberMatches(&e.c.Members[i], name, k) {
				return e, &e.c.Members[i], true
			}
		}
	}
	return classEntry{}, nil, false
}

// parentOf returns the parent class FQN of class fqn.
func (ix *Index) parentOfLocked(fqn string) string {
	for _, e := range ix.classes[fold(fqn)] {
		if e.c.Kind != KindInterface && len(e.c.Extends) > 0 {
			return e.c.Extends[0]
		}
	}
	return ""
}

// classOf resolves the class a reference names (self/static/parent
// relative to ref.Class).
func (ix *Index) classOfLocked(name string, ref Ref, f *File) string {
	switch strings.ToLower(name) {
	case "self", "static", "this":
		return ref.Class
	case "parent":
		if ref.Class == "" {
			return ""
		}
		return ix.parentOfLocked(ref.Class)
	case "":
		return ""
	}
	return ResolveClass(name, f.ScopeAt(ref.Offset))
}

// target is a resolved reference.
type target struct {
	kind   RefKind
	class  string // FQN of the class (RefClass) or of the member's lookup start
	member *Member
	owner  classEntry // declaring class of member
	defs   []Location
}

func (ix *Index) resolveLocked(f *File, ref Ref) target {
	t := target{kind: ref.Kind}
	switch ref.Kind {
	case RefClass:
		t.class = ix.classOfLocked(ref.Name, ref, f)
		for _, e := range ix.classes[fold(t.class)] {
			t.defs = append(t.defs, classLoc(e))
		}
	case RefFunction:
		for _, c := range resolveCallable(ref.Name, UseFunction, f.ScopeAt(ref.Offset)) {
			if es := ix.funcs[fold(c)]; len(es) > 0 {
				for _, e := range es {
					t.defs = append(t.defs, Location{Path: e.f.Path, Kind: KindFunction, Name: e.fn.Name, FQN: e.fn.FQN,
						Library: e.f.Library, Detail: e.fn.Detail, Range: e.fn.Range, NameRange: e.fn.NameRange})
				}
				break
			}
		}
	case RefConst:
		for _, c := range resolveCallable(ref.Name, UseConst, f.ScopeAt(ref.Offset)) {
			if es := ix.consts[fold(c)]; len(es) > 0 {
				for _, e := range es {
					t.defs = append(t.defs, Location{Path: e.f.Path, Kind: KindConst, Name: e.c.Name, FQN: e.c.FQN,
						Library: e.f.Library, Range: e.c.Range, NameRange: e.c.NameRange})
				}
				break
			}
		}
		if len(t.defs) == 0 { // a bare name may still be a class (e.g. in attributes)
			t.class = ResolveClass(ref.Name, f.ScopeAt(ref.Offset))
			for _, e := range ix.classes[fold(t.class)] {
				t.kind = RefClass
				t.defs = append(t.defs, classLoc(e))
			}
		}
	case RefMember:
		if ref.Decl {
			t.class = ref.Class
			for _, e := range ix.classes[fold(ref.Class)] {
				for i := range e.c.Members {
					if m := &e.c.Members[i]; memberMatches(m, ref.Name, ref.MemberKind) && m.NameRange.Start.Offset <= ref.Offset && ref.Offset <= m.NameRange.End.Offset && e.f.Path == f.Path {
						t.owner, t.member = e, m
					}
				}
			}
			if t.member == nil { // file not (yet) indexed in this state: use the parsed data
				for ci := range f.Classes {
					c := &f.Classes[ci]
					if c.FQN != ref.Class {
						continue
					}
					for i := range c.Members {
						if memberMatches(&c.Members[i], ref.Name, ref.MemberKind) {
							t.owner, t.member = classEntry{f: f, c: c}, &c.Members[i]
						}
					}
				}
			}
		} else {
			t.class = ix.classOfLocked(ref.Receiver, ref, f)
			if t.class != "" {
				if e, m, ok := ix.findMemberLocked(t.class, ref.Name, ref.MemberKind); ok {
					t.owner, t.member = e, m
				}
			}
		}
		if t.member != nil {
			t.defs = append(t.defs, memberLoc(t.owner, t.member))
		}
	}
	return t
}

// Definition returns the declarations of the symbol at byte offset off of
// src (the current text of path). Members are found through $this, self,
// static, parent and class names (inheritance chain and traits included);
// members of other expressions need type inference and yield nothing.
func (ix *Index) Definition(path string, src []byte, off int) ([]Location, error) {
	f, ref, err := ix.locate(path, src, off)
	if err != nil || ref.Kind == 0 {
		return nil, err
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.resolveLocked(f, ref).defs, nil
}

// descendantsLocked returns all transitive subtypes of fqn in breadth-first
// order.
func (ix *Index) descendantsLocked(fqn string) []classEntry {
	var out []classEntry
	seen := map[string]bool{fold(fqn): true}
	queue := []string{fqn}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range ix.subtypes[fold(cur)] {
			k := fold(e.c.FQN)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, e)
			queue = append(queue, e.c.FQN)
		}
	}
	return out
}

// implementationsLocked returns the implementations of a resolved target.
func (ix *Index) implementationsLocked(t target) []Location {
	var out []Location
	switch t.kind {
	case RefClass:
		if t.class == "" {
			return nil
		}
		if es := ix.classes[fold(t.class)]; len(es) > 0 && es[0].c.Kind == KindTrait {
			for _, e := range ix.traitUsers[fold(t.class)] {
				out = append(out, classLoc(e))
			}
			return out
		}
		for _, e := range ix.descendantsLocked(t.class) {
			if e.c.Kind != KindInterface {
				out = append(out, classLoc(e))
			}
		}
	case RefMember:
		if t.member == nil {
			return nil
		}
		out = ix.overridesLocked(t.owner, t.member)
	}
	return out
}

// overridesLocked returns the members of subtypes of owner that override
// or implement m (also through traits the subtypes use).
func (ix *Index) overridesLocked(owner classEntry, m *Member) []Location {
	var out []Location
	seen := map[*Member]bool{m: true}
	subs := ix.descendantsLocked(owner.c.FQN)
	if owner.c.Kind == KindTrait {
		subs = ix.traitUsers[fold(owner.c.FQN)]
	}
	for _, e := range subs {
		found := false
		for i := range e.c.Members {
			if mm := &e.c.Members[i]; memberMatches(mm, m.Name, m.Kind) {
				if !seen[mm] {
					seen[mm] = true
					out = append(out, memberLoc(e, mm))
				}
				found = true
				break
			}
		}
		if found {
			continue
		}
		for _, t := range e.c.Traits {
			for _, te := range ix.classes[fold(t)] {
				for i := range te.c.Members {
					if mm := &te.c.Members[i]; memberMatches(mm, m.Name, m.Kind) && !seen[mm] {
						seen[mm] = true
						out = append(out, memberLoc(te, mm))
					}
				}
			}
		}
	}
	return out
}

// Implementations returns, for the symbol at off: the implementing classes
// of an interface (transitively, also through parent classes), the
// subclasses of a class, the users of a trait, or the overriding and
// implementing methods of a method.
func (ix *Index) Implementations(path string, src []byte, off int) ([]Location, error) {
	f, ref, err := ix.locate(path, src, off)
	if err != nil || ref.Kind == 0 {
		return nil, err
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.implementationsLocked(ix.resolveLocked(f, ref)), nil
}

// ClassImplementations is Implementations for a class FQN.
func (ix *Index) ClassImplementations(fqn string) []Location {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.implementationsLocked(target{kind: RefClass, class: strings.TrimPrefix(fqn, `\`)})
}

// superLocked returns the supertypes of a class or the members a member
// overrides or implements.
func (ix *Index) superLocked(t target) []Location {
	var out []Location
	switch t.kind {
	case RefClass:
		for _, e := range ix.classes[fold(t.class)] {
			for _, n := range append(append([]string(nil), e.c.Extends...), e.c.Implements...) {
				for _, p := range ix.classes[fold(n)] {
					out = append(out, classLoc(p))
				}
			}
			break
		}
	case RefMember:
		if t.member != nil {
			out = ix.superMembersLocked(t.owner, t.member)
		}
	}
	return out
}

// superMembersLocked returns the nearest declaration of m in the parent
// class chain plus all interface declarations of m.
func (ix *Index) superMembersLocked(owner classEntry, m *Member) []Location {
	var out []Location
	foundClass := false
	for _, e := range ix.lineageLocked(owner.c.FQN) {
		if fold(e.c.FQN) == fold(owner.c.FQN) {
			continue
		}
		isIface := e.c.Kind == KindInterface
		if !isIface && foundClass {
			continue
		}
		for i := range e.c.Members {
			if mm := &e.c.Members[i]; memberMatches(mm, m.Name, m.Kind) {
				out = append(out, memberLoc(e, mm))
				if !isIface {
					foundClass = true
				}
				break
			}
		}
	}
	return out
}

// Super returns what the symbol at off overrides or implements (methods)
// or its direct supertypes (classes).
func (ix *Index) Super(path string, src []byte, off int) ([]Location, error) {
	f, ref, err := ix.locate(path, src, off)
	if err != nil || ref.Kind == 0 {
		return nil, err
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.superLocked(ix.resolveLocked(f, ref)), nil
}

// ClassAt returns the FQN of the class referenced or declared at off ("" if
// none): the class under the cursor, or the class around it.
func (ix *Index) ClassAt(path string, src []byte, off int) (string, error) {
	f, ref, err := ix.locate(path, src, off)
	if err != nil {
		return "", err
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	switch ref.Kind {
	case RefClass:
		return ix.classOfLocked(ref.Name, ref, f), nil
	case RefMember:
		if t := ix.resolveLocked(f, ref); t.member != nil {
			return t.owner.c.FQN, nil
		}
	}
	if f != nil {
		for i := len(f.Classes) - 1; i >= 0; i-- {
			c := &f.Classes[i]
			if c.Range.Start.Offset <= off && off <= c.Range.End.Offset {
				return c.FQN, nil
			}
		}
	}
	return ref.Class, nil
}

// HierarchyNode is one type of a type hierarchy with its supertypes (in
// Hierarchy.Supertypes) or subtypes (in Hierarchy.Subtypes).
type HierarchyNode struct {
	Location
	Children []HierarchyNode
}

// Hierarchy is the type hierarchy of a class.
type Hierarchy struct {
	Class      Location
	Supertypes []HierarchyNode
	Subtypes   []HierarchyNode
	// Truncated is set when the subtype tree hit the node limit.
	Truncated bool
}

// maxHierarchyNodes caps the subtype tree.
const maxHierarchyNodes = 5000

// TypeHierarchy returns the supertypes (recursively upwards) and subtypes
// (recursively downwards) of class fqn; ok is false if it is not indexed.
func (ix *Index) TypeHierarchy(fqn string) (Hierarchy, bool) {
	fqn = strings.TrimPrefix(fqn, `\`)
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	es := ix.classes[fold(fqn)]
	if len(es) == 0 {
		return Hierarchy{}, false
	}
	h := Hierarchy{Class: classLoc(es[0])}
	var up func(c *Class, path map[string]bool) []HierarchyNode
	up = func(c *Class, path map[string]bool) []HierarchyNode {
		var out []HierarchyNode
		for _, n := range append(append([]string(nil), c.Extends...), c.Implements...) {
			if path[fold(n)] {
				continue
			}
			ps := ix.classes[fold(n)]
			if len(ps) == 0 {
				out = append(out, HierarchyNode{Location: Location{FQN: n, Name: lastSegment(n)}})
				continue
			}
			path[fold(n)] = true
			out = append(out, HierarchyNode{Location: classLoc(ps[0]), Children: up(ps[0].c, path)})
			delete(path, fold(n))
		}
		return out
	}
	h.Supertypes = up(es[0].c, map[string]bool{fold(fqn): true})
	count := 0
	var down func(name string, path map[string]bool) []HierarchyNode
	down = func(name string, path map[string]bool) []HierarchyNode {
		var out []HierarchyNode
		for _, e := range ix.subtypes[fold(name)] {
			k := fold(e.c.FQN)
			if path[k] {
				continue
			}
			if count >= maxHierarchyNodes {
				h.Truncated = true
				return out
			}
			count++
			path[k] = true
			out = append(out, HierarchyNode{Location: classLoc(e), Children: down(e.c.FQN, path)})
			delete(path, k)
		}
		sortNodes(out)
		return out
	}
	h.Subtypes = down(fqn, map[string]bool{fold(fqn): true})
	return h, true
}

func sortNodes(n []HierarchyNode) {
	sort.SliceStable(n, func(i, j int) bool { return fold(n[i].FQN) < fold(n[j].FQN) })
}

// GutterMark tells, for one class or member declaration of a file, which
// declarations it overrides/implements (Up) and which override/implement
// it (Down).
type GutterMark struct {
	Line      int // 1-based line of the name
	Kind      Kind
	FQN       string
	NameRange Range
	Up        []Location
	Down      []Location
	// UpTotal and DownTotal are the counts before truncation to
	// maxGutterTargets.
	UpTotal   int
	DownTotal int
}

const maxGutterTargets = 100

// Gutter returns the gutter marks of the indexed file path, sorted by line.
func (ix *Index) Gutter(path string) []GutterMark {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	f, ok := ix.files[path]
	if !ok {
		return nil
	}
	var out []GutterMark
	add := func(g GutterMark) {
		g.UpTotal, g.DownTotal = len(g.Up), len(g.Down)
		if g.UpTotal == 0 && g.DownTotal == 0 {
			return
		}
		g.Up = g.Up[:min(len(g.Up), maxGutterTargets)]
		g.Down = g.Down[:min(len(g.Down), maxGutterTargets)]
		out = append(out, g)
	}
	for ci := range f.Classes {
		c := &f.Classes[ci]
		owner := classEntry{f: f, c: c}
		t := target{kind: RefClass, class: c.FQN}
		add(GutterMark{Line: c.NameRange.Start.Line, Kind: c.Kind, FQN: c.FQN, NameRange: c.NameRange,
			Up: ix.superLocked(t), Down: ix.implementationsLocked(t)})
		for mi := range c.Members {
			m := &c.Members[mi]
			if m.Kind != KindMethod {
				continue
			}
			add(GutterMark{Line: m.NameRange.Start.Line, Kind: m.Kind, FQN: c.FQN + "::" + m.Name, NameRange: m.NameRange,
				Up: ix.superMembersLocked(owner, m), Down: ix.overridesLocked(owner, m)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].NameRange.Start.Offset < out[j].NameRange.Start.Offset })
	return out
}
