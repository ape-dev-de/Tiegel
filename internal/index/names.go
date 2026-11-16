package index

import "strings"

// RefKind classifies the reference under the cursor.
type RefKind uint8

// Reference kinds.
const (
	RefClass RefKind = iota + 1
	RefFunction
	RefConst
	RefMember
)

// Ref is the symbol reference at a position of a file, before name
// resolution.
type Ref struct {
	Kind RefKind
	// Name is the class, function or constant name as written ("\"
	// prefixed when already fully qualified), "self"/"static"/"parent" for
	// relative class references, or the member name for RefMember.
	Name string
	// MemberKind is the member's kind for RefMember (KindClassConst also
	// matches enum cases).
	MemberKind Kind
	// Receiver of a member reference: "this", "self", "static", "parent",
	// a class name as written, or "" when unknown (e.g. $obj->m()).
	Receiver string
	// Class is the FQN of the enclosing class-like declaration ("" outside).
	Class string
	// Offset is the byte offset the reference was located at.
	Offset int
	// Decl is true when the cursor is on the name of a declaration.
	Decl bool
}

// reservedTypes are type names that are never classes.
var reservedTypes = map[string]bool{
	"int": true, "float": true, "string": true, "bool": true, "array": true, "callable": true,
	"iterable": true, "object": true, "mixed": true, "void": true, "null": true, "never": true,
	"false": true, "true": true, "integer": true, "double": true, "boolean": true, "resource": true,
	"self": true, "static": true, "parent": true,
}

// qualify joins a namespace and a short name.
func qualify(ns, name string) string {
	if ns == "" {
		return name
	}
	return ns + `\` + name
}

func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func namespaceOf(fqn string) string {
	if i := strings.LastIndexByte(fqn, '\\'); i >= 0 {
		return fqn[:i]
	}
	return ""
}

// ResolveClass resolves a class name as written in scope sc to its fully
// qualified name (without leading backslash) following PHP's rules: fully
// qualified names stay, "namespace\X" is relative to the current namespace,
// a first segment matching a class import is replaced by the import,
// everything else is prefixed with the current namespace. Reserved type
// names and self/static/parent yield "".
func ResolveClass(name string, sc Scope) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if name[0] == '\\' {
		return strings.TrimPrefix(name, `\`)
	}
	if rest, ok := cutPrefixFold(name, `namespace\`); ok {
		return qualify(sc.Namespace, rest)
	}
	first, rest, qualified := strings.Cut(name, `\`)
	if !qualified && reservedTypes[fold(name)] {
		return ""
	}
	for _, u := range sc.Uses {
		if u.Kind == UseClass && strings.EqualFold(u.Alias, first) {
			if qualified {
				return u.FQN + `\` + rest
			}
			return u.FQN
		}
	}
	return qualify(sc.Namespace, name)
}

// resolveCallable returns the candidate FQNs of a function (kind
// UseFunction) or constant (UseConst) name in scope sc, in lookup order:
// imports, then the current namespace, then the global fallback for
// unqualified names.
func resolveCallable(name string, kind UseKind, sc Scope) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if name[0] == '\\' {
		return []string{name[1:]}
	}
	if rest, ok := cutPrefixFold(name, `namespace\`); ok {
		return []string{qualify(sc.Namespace, rest)}
	}
	if first, rest, ok := strings.Cut(name, `\`); ok {
		// qualified: the first segment may be a class/namespace import
		for _, u := range sc.Uses {
			if u.Kind == UseClass && strings.EqualFold(u.Alias, first) {
				return []string{u.FQN + `\` + rest}
			}
		}
		return []string{qualify(sc.Namespace, name)}
	}
	for _, u := range sc.Uses {
		if u.Kind == kind && (u.Alias == name || kind == UseFunction && strings.EqualFold(u.Alias, name)) {
			return []string{u.FQN}
		}
	}
	if sc.Namespace == "" {
		return []string{name}
	}
	return []string{qualify(sc.Namespace, name), name}
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}
